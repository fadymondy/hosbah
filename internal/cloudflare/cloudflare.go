// Package cloudflare is Hosbah's small, dependency-free client for the Cloudflare v4 API: verify a
// token, find the zone a hostname belongs to, and manage DNS records idempotently. It is used for
// the platform's own apps domain (every app gets <prefix><app>.<domain>) and for teams that connect
// their Cloudflare account, so custom domains get their DNS and verification records automatically.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is Cloudflare's API root.
const DefaultBaseURL = "https://api.cloudflare.com/client/v4"

// Client talks to Cloudflare with one API token.
type Client struct {
	token   string
	baseURL string
	http    *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL points the client elsewhere (tests).
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }

// WithHTTPClient replaces the HTTP client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// New returns a client for token (an API token, not a global key).
func New(token string, opts ...Option) *Client {
	c := &Client{token: strings.TrimSpace(token), baseURL: DefaultBaseURL, http: &http.Client{Timeout: 20 * time.Second}}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Error is an API failure. The token never appears in it.
type Error struct {
	Status int
	Codes  []int
	Msg    string
}

func (e *Error) Error() string { return fmt.Sprintf("cloudflare: %s (HTTP %d)", e.Msg, e.Status) }

// ErrNotFound: no zone or record matched.
var ErrNotFound = errors.New("cloudflare: not found")

type envelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo *struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body, out any) (*envelope, error) {
	if c.token == "" {
		return nil, errors.New("cloudflare: no API token")
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, &Error{Status: resp.StatusCode, Msg: "unexpected response"}
	}
	if !env.Success || resp.StatusCode >= 300 {
		e := &Error{Status: resp.StatusCode, Msg: "request failed"}
		var msgs []string
		for _, x := range env.Errors {
			e.Codes = append(e.Codes, x.Code)
			msgs = append(msgs, x.Message)
		}
		if len(msgs) > 0 {
			e.Msg = strings.Join(msgs, "; ")
		}
		return nil, e
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return nil, fmt.Errorf("cloudflare: decoding result: %w", err)
		}
	}
	return &env, nil
}

// Verify checks the token is valid and active.
func (c *Client) Verify(ctx context.Context) error {
	var r struct {
		Status string `json:"status"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/user/tokens/verify", nil, nil, &r); err != nil {
		return err
	}
	if r.Status != "active" {
		return fmt.Errorf("cloudflare: the token is %s", r.Status)
	}
	return nil
}

// Zone is a DNS zone (a domain) on the account.
type Zone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Zones lists every zone the token can see.
func (c *Client) Zones(ctx context.Context) ([]Zone, error) {
	var all []Zone
	for page := 1; ; page++ {
		var zs []Zone
		env, err := c.do(ctx, http.MethodGet, "/zones", url.Values{"per_page": {"50"}, "page": {fmt.Sprint(page)}}, nil, &zs)
		if err != nil {
			return nil, err
		}
		all = append(all, zs...)
		if env.ResultInfo == nil || page >= env.ResultInfo.TotalPages || len(zs) == 0 {
			return all, nil
		}
	}
}

// ZoneFor finds the zone hostname belongs to: the longest zone name that is the hostname itself or
// a parent of it (so a.b.example.com picks b.example.com over example.com when both are zones).
func (c *Client) ZoneFor(ctx context.Context, hostname string) (Zone, error) {
	host := strings.TrimSuffix(strings.ToLower(hostname), ".")
	for h := host; strings.Contains(h, "."); {
		var zs []Zone
		if _, err := c.do(ctx, http.MethodGet, "/zones", url.Values{"name": {h}}, nil, &zs); err != nil {
			return Zone{}, err
		}
		if len(zs) > 0 {
			return zs[0], nil
		}
		_, h, _ = strings.Cut(h, ".")
	}
	return Zone{}, ErrNotFound
}

// Record is a DNS record.
type Record struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl,omitempty"`      // 1 = automatic
	Priority int    `json:"priority,omitempty"` // MX only
	Proxied  *bool  `json:"proxied,omitempty"`
	Comment  string `json:"comment,omitempty"`
}

// Records lists records in a zone by exact name and (optionally) type.
func (c *Client) Records(ctx context.Context, zoneID, name, typ string) ([]Record, error) {
	q := url.Values{"name": {strings.ToLower(name)}, "per_page": {"100"}}
	if typ != "" {
		q.Set("type", typ)
	}
	var rs []Record
	_, err := c.do(ctx, http.MethodGet, "/zones/"+url.PathEscape(zoneID)+"/dns_records", q, nil, &rs)
	return rs, err
}

// Upsert makes the zone hold exactly one record of r.Type at r.Name with r's content: it updates
// the first existing one, deletes any extra duplicates, or creates it. Calling it again is a no-op.
func (c *Client) Upsert(ctx context.Context, zoneID string, r Record) (Record, error) {
	if r.TTL == 0 {
		r.TTL = 1
	}
	existing, err := c.Records(ctx, zoneID, r.Name, r.Type)
	if err != nil {
		return Record{}, err
	}
	base := "/zones/" + url.PathEscape(zoneID) + "/dns_records"
	var out Record
	if len(existing) == 0 {
		_, err = c.do(ctx, http.MethodPost, base, nil, r, &out)
		return out, err
	}
	for _, extra := range existing[1:] {
		if _, err := c.do(ctx, http.MethodDelete, base+"/"+url.PathEscape(extra.ID), nil, nil, nil); err != nil {
			return Record{}, err
		}
	}
	cur := existing[0]
	if cur.Content == r.Content && (r.Proxied == nil || cur.Proxied != nil && *cur.Proxied == *r.Proxied) {
		return cur, nil
	}
	_, err = c.do(ctx, http.MethodPut, base+"/"+url.PathEscape(cur.ID), nil, r, &out)
	return out, err
}

// Delete removes every record of typ (all types when empty) at name. Nothing to delete is success.
func (c *Client) Delete(ctx context.Context, zoneID, name, typ string) error {
	rs, err := c.Records(ctx, zoneID, name, typ)
	if err != nil {
		return err
	}
	for _, r := range rs {
		if _, err := c.do(ctx, http.MethodDelete, "/zones/"+url.PathEscape(zoneID)+"/dns_records/"+url.PathEscape(r.ID), nil, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

// PointHost upserts the A (IPv4) or AAAA (IPv6) record for host → ip in whichever zone owns host.
func (c *Client) PointHost(ctx context.Context, host, ip string, proxied bool) (Zone, Record, error) {
	z, err := c.ZoneFor(ctx, host)
	if err != nil {
		return Zone{}, Record{}, err
	}
	typ := "A"
	if strings.Contains(ip, ":") {
		typ = "AAAA"
	}
	r, err := c.Upsert(ctx, z.ID, Record{Type: typ, Name: host, Content: ip, Proxied: &proxied, Comment: ManagedComment})
	return z, r, err
}

// ManagedComment marks the records Hosbah created; only those are ever deleted.
const ManagedComment = "managed by Hosbah"

// legacyManagedComment marks records created before the rename from Cloudy; they are still ours.
const legacyManagedComment = "managed by Cloudy"

func managed(comment string) bool { return comment == ManagedComment || comment == legacyManagedComment }

// Managed reports whether Hosbah created r (its comment carries the marker, old or new).
func Managed(r Record) bool { return managed(r.Comment) }

// CreateRecord adds one record, marked as Hosbah's.
func (c *Client) CreateRecord(ctx context.Context, zoneID string, r Record) (Record, error) {
	if r.TTL == 0 {
		r.TTL = 1
	}
	r.ID, r.Comment = "", ManagedComment
	var out Record
	_, err := c.do(ctx, http.MethodPost, "/zones/"+url.PathEscape(zoneID)+"/dns_records", nil, r, &out)
	return out, err
}

// RecordPatch is a partial update: nil fields are left as they are.
type RecordPatch struct {
	Content *string `json:"content,omitempty"`
	Proxied *bool   `json:"proxied,omitempty"`
	Comment *string `json:"comment,omitempty"`
}

// PatchRecord updates only the given fields of one record.
func (c *Client) PatchRecord(ctx context.Context, zoneID, recordID string, p RecordPatch) (Record, error) {
	var out Record
	_, err := c.do(ctx, http.MethodPatch, "/zones/"+url.PathEscape(zoneID)+"/dns_records/"+url.PathEscape(recordID), nil, p, &out)
	return out, err
}

// DeleteRecord removes one record by id. The caller decides whether it may (see Managed).
func (c *Client) DeleteRecord(ctx context.Context, zoneID, recordID string) error {
	_, err := c.do(ctx, http.MethodDelete, "/zones/"+url.PathEscape(zoneID)+"/dns_records/"+url.PathEscape(recordID), nil, nil, nil)
	return err
}

// DeleteManaged removes the records at name that Hosbah created, leaving the user's own alone.
func (c *Client) DeleteManaged(ctx context.Context, zoneID, name string) error {
	rs, err := c.Records(ctx, zoneID, name, "")
	if err != nil {
		return err
	}
	for _, r := range rs {
		if !managed(r.Comment) {
			continue
		}
		if _, err := c.do(ctx, http.MethodDelete, "/zones/"+url.PathEscape(zoneID)+"/dns_records/"+url.PathEscape(r.ID), nil, nil, nil); err != nil {
			return err
		}
	}
	return nil
}
