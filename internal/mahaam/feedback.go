// Package mahaam files Hosbah's own problems into its Mahaam project, through the same public
// intake the browser widget uses (POST {MAHAAM_URL}/api/feedback/embed with the project's public
// key). It mirrors Mahaam's Go SDK (github.com/fadymondy/mahaam/sdk/go), which is private and
// so cannot be a build dependency here.
//
// Configuration (all optional; without a key nothing is sent):
//
//	MAHAAM_URL           the Mahaam console, e.g. https://console.mahaam.app
//	MAHAAM_FEEDBACK_KEY  the Hosbah project's public key (pfk_…); public by design
//	MAHAAM_APP_URL       a URL on one of the key's allowed origins, sent as page_url
package mahaam

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// Config is the reporter's configuration; FromEnv reads it from the environment.
type Config struct {
	BaseURL string
	Key     string
	AppURL  string
	HTTP    *http.Client
	Log     *slog.Logger
	// Dedupe is how long the same problem (by title) is filed only once. Default 10 minutes.
	Dedupe time.Duration
}

// FromEnv reads MAHAAM_URL, MAHAAM_FEEDBACK_KEY and MAHAAM_APP_URL.
func FromEnv() Config {
	return Config{
		BaseURL: strings.TrimRight(strings.TrimSpace(os.Getenv("MAHAAM_URL")), "/"),
		Key:     strings.TrimSpace(os.Getenv("MAHAAM_FEEDBACK_KEY")),
		AppURL:  strings.TrimSpace(os.Getenv("MAHAAM_APP_URL")),
	}
}

// Client files reports. The zero value and a nil *Client are disabled and safe to use.
type Client struct {
	cfg  Config
	mu   sync.Mutex
	seen map[string]time.Time
}

// New returns a client; it is disabled when the base URL or the key is missing.
func New(cfg Config) *Client {
	if cfg.HTTP == nil {
		cfg.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Dedupe == 0 {
		cfg.Dedupe = 10 * time.Minute
	}
	return &Client{cfg: cfg, seen: map[string]time.Time{}}
}

// Enabled reports whether reports are sent.
func (c *Client) Enabled() bool { return c != nil && c.cfg.BaseURL != "" && c.cfg.Key != "" }

// Report is one issue.
type Report struct {
	Title    string
	Body     string
	Type     string // bug | feature | task
	Priority string // low | medium | high | urgent
	PageURL  string
	Extra    map[string]string
}

// APIError is a refusal from the intake.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("mahaam feedback: %d %s", e.Status, e.Message) }

// ErrDisabled is returned when no base URL or key is configured.
var ErrDisabled = errors.New("mahaam feedback: not configured")

// Send files the report and returns its issue key (MH-n).
func (c *Client) Send(ctx context.Context, r Report) (string, error) {
	if !c.Enabled() {
		return "", ErrDisabled
	}
	if strings.TrimSpace(r.Title) == "" {
		return "", errors.New("mahaam feedback: title is required")
	}
	if r.PageURL == "" {
		r.PageURL = c.cfg.AppURL
	}
	fields := map[string]string{
		"public_key": c.cfg.Key, "title": truncate(r.Title, 200), "body": truncate(r.Body, 20000),
		"issue_type": r.Type, "priority": r.Priority, "page_url": r.PageURL,
	}
	for k, v := range r.Extra {
		if _, taken := fields[k]; !taken {
			fields[k] = v
		}
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if v != "" {
			_ = mw.WriteField(k, v)
		}
	}
	_ = mw.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/api/feedback/embed", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	// The intake checks the Origin against the key's allowlist; a server report has none of its
	// own, so it presents the app's origin (the same URL that goes in page_url).
	if o := origin(c.cfg.AppURL); o != "" {
		req.Header.Set("Origin", o)
	}
	resp, err := c.cfg.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var out struct {
		Key   string `json:"key"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := out.Error
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return "", &APIError{Status: resp.StatusCode, Message: msg}
	}
	return out.Key, nil
}

// fresh reports whether a problem with this title has not been filed within the dedupe window
// (and records it), so a crash loop files one issue rather than thousands.
func (c *Client) fresh(title string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if t, ok := c.seen[title]; ok && now.Sub(t) < c.cfg.Dedupe {
		return false
	}
	c.seen[title] = now
	if len(c.seen) > 1000 { // keep the map bounded
		for k, t := range c.seen {
			if now.Sub(t) >= c.cfg.Dedupe {
				delete(c.seen, k)
			}
		}
	}
	return true
}

// OnError is a togo "error" hook (k.Hooks.On("error", …)): togo fires it for every panic its
// recovery middleware catches and for every k.ReportError. It files the error as a high-priority
// bug in the background — never delaying the request — once per dedupe window per message. The
// stack is taken here, while a panic is still unwinding, so it shows where the problem happened.
func (c *Client) OnError(_ context.Context, payload any) error {
	err, ok := payload.(error)
	if !ok || err == nil || !c.Enabled() {
		return nil
	}
	title := truncate("API error: "+err.Error(), 200)
	if !c.fresh(title) {
		return nil
	}
	rep := Report{
		Title:    title,
		Body:     fmt.Sprintf("%s\n\n```\n%s\n```", err.Error(), debug.Stack()),
		Type:     "bug",
		Priority: "high",
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if key, err := c.Send(ctx, rep); err != nil {
			c.cfg.Log.Warn("mahaam feedback: could not file the error", "err", err)
		} else {
			c.cfg.Log.Info("mahaam feedback: error filed", "issue", key)
		}
	}()
	return nil
}

func origin(u string) string {
	i := strings.Index(u, "://")
	if i < 0 {
		return ""
	}
	if j := strings.IndexByte(u[i+3:], '/'); j >= 0 {
		return u[:i+3+j]
	}
	return u
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
