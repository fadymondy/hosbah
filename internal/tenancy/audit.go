package tenancy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"time"

	db "github.com/fadymondy/hosbah/internal/db/gen"
)

/*
Record writes an audit entry in togo audit's activity_log format, plus team_id.

It runs on the caller's transaction (the q from Tx), so the entry commits or rolls back with
the change it describes — the togo audit plugin's own Log() writes through the pool, outside
any transaction, and has no tenant column. actorID is the user (or "mcp:<token id>" for a
machine call). changes must never contain a secret: token plaintext, hashes, sealed values.
*/
func Record(ctx context.Context, q *db.Queries, teamID, actorID, action, subjectType, subjectID string, changes map[string]any) error {
	if changes == nil {
		changes = map[string]any{}
	}
	changes["team_id"] = teamID
	body, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	meta := MetaFrom(ctx)
	tid := teamID
	return q.InsertActivity(ctx, db.InsertActivityParams{
		ID:          newID(),
		Action:      action,
		SubjectType: subjectType,
		SubjectID:   subjectID,
		CauserID:    actorID,
		Changes:     string(body),
		Ip:          meta.IP,
		UserAgent:   meta.UserAgent,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		TeamID:      &tid,
	})
}

// RequestMeta is the request context an audit entry carries.
type RequestMeta struct {
	IP        string
	UserAgent string
	// Method is the HTTP method, so code that only sees a context (a suspended team lets a
	// platform admin read, not write) can tell a read from a change.
	Method string
}

type metaKey struct{}

// WithMeta attaches request metadata to a context.
func WithMeta(ctx context.Context, m RequestMeta) context.Context {
	return context.WithValue(ctx, metaKey{}, m)
}

// MetaFrom returns the request metadata on ctx (zero value if none).
func MetaFrom(ctx context.Context) RequestMeta {
	m, _ := ctx.Value(metaKey{}).(RequestMeta)
	return m
}

// MetaMiddleware records the client address and user agent for audit entries.
//
// Deliberately RemoteAddr, not X-Forwarded-For: togo audit's own middleware trusts the
// X-Forwarded-For and X-User-Id headers, which any client can set — a spoofable causer. When
// Hosbah runs behind a proxy, the proxy's address is what is recorded until a trusted-proxy
// setting exists.
func MetaMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if host, _, err := net.SplitHostPort(ip); err == nil {
			ip = host
		}
		ctx := WithMeta(r.Context(), RequestMeta{IP: ip, UserAgent: r.UserAgent(), Method: r.Method})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
