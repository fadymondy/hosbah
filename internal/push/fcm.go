package push

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/fadymondy/hosbah/internal/brand"
)

/*
The FCM HTTP v1 sender, ported from Mahaam (managy internal/push/native.go). One credential and one
sender for both phones: Firebase relays to APNs for iOS once the APNs key is uploaded to the
Firebase project. Configured by

	HOSBAH_FCM_PROJECT_ID            the Firebase project id
	HOSBAH_FCM_SERVICE_ACCOUNT_FILE  path to the service account JSON (or HOSBAH_FCM_SERVICE_ACCOUNT inline)
	HOSBAH_FCM_ENDPOINT              optional API base URL, for tests (default https://fcm.googleapis.com)

With the project or the account missing, push is off: alerts still reach the bell, the
notification channels and the desktop app (which listens on the team's notification stream).

Authentication is the OAuth 2 JWT-bearer grant a service account uses: sign a short JWT with the
account's private key, trade it at the account's token_uri for an access token, and reuse that
token until shortly before it expires. Written here rather than pulled in as the Google client
library, which would add a large dependency tree for one POST.
*/

const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

type serviceAccount struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// Message is one notification for one install.
type Message struct {
	Title, Body string
	// Data reaches the app with the notification: team, kind, subject_type, subject_id, url.
	Data map[string]string
	// Tag collapses repeats of the same thing (a flapping app is one banner, updated).
	Tag string
}

// Result is what became of one message.
type Result int

const (
	SentOK     Result = iota
	SentGone          // the token names no install any more: forget it
	SentFailed        // anything else: keep it, try again next time
)

// Sender delivers one message to one FCM registration token.
type Sender interface {
	Send(ctx context.Context, token string, m Message) (Result, error)
}

type fcmSender struct {
	project, email, tokenURI, endpoint string
	key                                *rsa.PrivateKey
	http                               *http.Client

	mu      sync.Mutex
	access  string
	expires time.Time
}

// SenderFromEnv returns the FCM sender, or nil (and no error) when push is not configured.
func SenderFromEnv() (Sender, error) {
	project := strings.TrimSpace(brand.Getenv("FCM_PROJECT_ID"))
	raw := strings.TrimSpace(brand.Getenv("FCM_SERVICE_ACCOUNT"))
	file := strings.TrimSpace(brand.Getenv("FCM_SERVICE_ACCOUNT_FILE"))
	if project == "" || (raw == "" && file == "") {
		return nil, nil
	}
	body := []byte(raw)
	if raw == "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", brand.EnvName("FCM_SERVICE_ACCOUNT_FILE"), err)
		}
		body = b
	}
	endpoint := strings.TrimRight(strings.TrimSpace(brand.Getenv("FCM_ENDPOINT")), "/")
	if endpoint == "" {
		endpoint = "https://fcm.googleapis.com"
	}
	return newFCM(project, endpoint, body)
}

func newFCM(project, endpoint string, account []byte) (*fcmSender, error) {
	var sa serviceAccount
	if err := json.Unmarshal(account, &sa); err != nil {
		return nil, fmt.Errorf("fcm: not a service account JSON: %w", err)
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(sa.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("fcm: service account private_key: %w", err)
	}
	if sa.ClientEmail == "" {
		return nil, errors.New("fcm: service account has no client_email")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return &fcmSender{project: project, email: sa.ClientEmail, tokenURI: sa.TokenURI, endpoint: endpoint, key: key,
		http: &http.Client{Timeout: 20 * time.Second}}, nil
}

// token returns a valid access token, fetching one when there is none or it is about to expire.
func (f *fcmSender) token(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.access != "" && time.Until(f.expires) > time.Minute {
		return f.access, nil
	}
	now := time.Now()
	assertion, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": f.email, "scope": fcmScope, "aud": f.tokenURI,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}).SignedString(f.key)
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {assertion}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.tokenURI, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := f.http.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return "", fmt.Errorf("fcm token: %d %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil || out.AccessToken == "" {
		return "", fmt.Errorf("fcm token: unreadable response (%v)", err)
	}
	f.access, f.expires = out.AccessToken, now.Add(time.Duration(out.ExpiresIn)*time.Second)
	return f.access, nil
}

func (f *fcmSender) forgetToken() {
	f.mu.Lock()
	f.access = ""
	f.mu.Unlock()
}

// Send delivers one message to one registration token. Android shows it on the "alerts" channel
// the app creates (high importance); iOS gets a sound and groups it by kind.
func (f *fcmSender) Send(ctx context.Context, token string, m Message) (Result, error) {
	note := map[string]any{"channel_id": "alerts"}
	android := map[string]any{"priority": "high", "notification": note}
	aps := map[string]any{"sound": "default", "thread-id": m.Data["kind"]}
	headers := map[string]string{}
	if tag := collapseTag(m.Tag); tag != "" {
		android["collapse_key"] = tag
		note["tag"] = tag
		headers["apns-collapse-id"] = tag
	}
	msg := map[string]any{
		"token":        token,
		"notification": map[string]string{"title": m.Title, "body": m.Body},
		"data":         m.Data,
		"android":      android,
		"apns":         map[string]any{"headers": headers, "payload": map[string]any{"aps": aps}},
	}
	return f.post(ctx, msg)
}

// post sends one FCM v1 message (the "message" object) and classifies the answer.
func (f *fcmSender) post(ctx context.Context, msg map[string]any) (Result, error) {
	access, err := f.token(ctx)
	if err != nil {
		return SentFailed, err
	}
	payload, _ := json.Marshal(map[string]any{"message": msg})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		f.endpoint+"/v1/projects/"+url.PathEscape(f.project)+"/messages:send", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	res, err := f.http.Do(req)
	if err != nil {
		return SentFailed, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	switch {
	case res.StatusCode < 300:
		return SentOK, nil
	case res.StatusCode == http.StatusUnauthorized:
		f.forgetToken() // revoked or expired early: fetch a fresh one next time
		return SentFailed, fmt.Errorf("fcm: 401 %s", strings.TrimSpace(string(b)))
	case res.StatusCode == http.StatusNotFound || bytes.Contains(b, []byte("UNREGISTERED")):
		// The app was uninstalled, or the token rotated and this one is dead.
		return SentGone, nil
	case res.StatusCode == http.StatusBadRequest && bytes.Contains(b, []byte("registration token")):
		// A token that was never valid (truncated, from another project).
		return SentGone, nil
	default:
		return SentFailed, fmt.Errorf("fcm: %d %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
}

// collapseTag trims a tag to APNs' 64-byte limit on apns-collapse-id.
func collapseTag(t string) string {
	if len(t) > 64 {
		t = t[:64]
	}
	return t
}
