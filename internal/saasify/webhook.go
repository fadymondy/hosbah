package saasify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/fadymondy/hosbah/internal/crypto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
)

// webhookContext is the crypto.Seal/Open field name a product key's webhook secret is bound to.
const webhookContext = "saasify/webhook_secret/v1"

func sealWebhookSecret(dek []byte, teamID, plainSecret string) (string, error) {
	return crypto.Seal(dek, teamID, webhookContext, []byte(plainSecret))
}

func openWebhookSecret(dek []byte, teamID, sealed string) (string, error) {
	pt, err := crypto.Open(dek, teamID, webhookContext, sealed)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// SignWebhook computes the HMAC-SHA256 hex digest of body under secret, the same way
// DeliverWebhook signs an outgoing delivery — exported so a team's receiver code (and this
// package's own tests) can verify or reproduce a signature.
func SignWebhook(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// webhookEvent is the JSON body posted to a product key's webhook URL on every instance state
// change. event is e.g. "instance.provisioned", "instance.suspended", "instance.resumed",
// "instance.destroyed".
type webhookEvent struct {
	Event     string             `json:"event"`
	Timestamp time.Time          `json:"timestamp"`
	ProductID string             `json:"product_id"`
	Instance  db.ProductInstance `json:"instance"`
}

// dispatchWebhook fires-and-forgets a signed webhook to every non-revoked key of the product that
// has a webhook URL configured. It is best-effort: a team's endpoint being down never blocks or
// fails the action that triggered it. DEK sealing means this needs the caller's *db.Queries only
// to look up keys and needs ops.Service.TeamDEK to unseal each key's secret — that wiring (a DEK
// getter) is threaded in via WebhookDEK, set once by internal/api at boot; nil means "no signing
// key available yet" and delivery is skipped (documented in the report as a reduced-scope item:
// synchronous best-effort HTTP from the request goroutine rather than a durable outbox/queue).
var WebhookDEK func(ctx context.Context, teamID string) ([]byte, error)

func dispatchWebhook(ctx context.Context, q *db.Queries, teamID, productID, event string, inst db.ProductInstance) {
	if WebhookDEK == nil {
		return
	}
	keys, err := q.ListKeysOfProduct(ctx, db.ListKeysOfProductParams{ProductID: productID, TeamID: teamID})
	if err != nil {
		return
	}
	var dek []byte
	for _, k := range keys {
		if k.RevokedAt != nil || k.WebhookUrl == nil || k.WebhookSecretSealed == nil {
			continue
		}
		if dek == nil {
			dek, err = WebhookDEK(ctx, teamID)
			if err != nil {
				return
			}
		}
		secret, err := openWebhookSecret(dek, teamID, *k.WebhookSecretSealed)
		if err != nil {
			continue
		}
		go deliver(*k.WebhookUrl, secret, webhookEvent{Event: event, Timestamp: time.Now().UTC(), ProductID: productID, Instance: inst})
	}
}

func deliver(url, secret string, ev webhookEvent) {
	body, err := json.Marshal(ev)
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hosbah-Event", ev.Event)
	req.Header.Set("X-Hosbah-Signature", "sha256="+SignWebhook(secret, body))
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}
