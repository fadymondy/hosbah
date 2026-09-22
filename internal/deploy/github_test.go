package deploy

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func sign(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	secret := []byte("s3cret")
	body := []byte(`{"ref":"refs/heads/main"}`)
	good := sign(secret, body)
	if !VerifySignature(secret, body, good) {
		t.Fatal("a correct signature was refused")
	}
	for name, h := range map[string]string{
		"tampered body":  sign(secret, []byte(`{"ref":"refs/heads/evil"}`)),
		"other secret":   sign([]byte("other"), body),
		"sha1 header":    "sha1=" + strings.TrimPrefix(good, "sha256="),
		"no prefix":      strings.TrimPrefix(good, "sha256="),
		"not hex":        "sha256=zz",
		"empty":          "",
	} {
		if VerifySignature(secret, body, h) {
			t.Errorf("%s accepted", name)
		}
	}
	if VerifySignature(nil, body, good) {
		t.Error("an empty secret accepted a signature")
	}
}

func TestUnconfiguredGitHub(t *testing.T) {
	t.Setenv("HOSBAH_GITHUB_APP_ID", "")
	t.Setenv("CLOUDY_GITHUB_APP_ID", "")
	g := GitHubFromEnv()
	if g.Configured() {
		t.Fatal("configured without credentials")
	}
	if _, _, err := g.InstallationToken(context.Background(), 1); err != ErrGitHubNotConfigured {
		t.Fatalf("want ErrGitHubNotConfigured, got %v", err)
	}
	if g.VerifyWebhook([]byte("x"), "sha256=00") {
		t.Fatal("an unconfigured app verified a webhook")
	}
}

// The installation-token exchange against a fake GitHub: the app JWT is RS256 over the App's key,
// names the App as issuer, and is short-lived.
func TestInstallationTokenExchange(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	now := time.Unix(1_800_000_000, 0)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/app/installations/42/access_tokens" {
			http.Error(w, "wrong endpoint", 404)
			return
		}
		jwt := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		parts := strings.Split(jwt, ".")
		if len(parts) != 3 {
			http.Error(w, "bad jwt", 401)
			return
		}
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		if rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig) != nil {
			http.Error(w, "bad signature", 401)
			return
		}
		raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var claims struct {
			Iat, Exp int64
			Iss      string
		}
		_ = json.Unmarshal(raw, &claims)
		if claims.Iss != "1234" || claims.Exp-claims.Iat > 11*60 || claims.Iat > now.Unix() {
			http.Error(w, "bad claims", 401)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"ghs_abc","expires_at":"2027-01-15T09:00:00Z"}`))
	}))
	defer fake.Close()

	g := &githubApp{appID: 1234, key: key, secret: []byte("x"), api: fake.URL, http: fake.Client(), now: func() time.Time { return now }}
	tok, exp, err := g.InstallationToken(context.Background(), 42)
	if err != nil || tok != "ghs_abc" || exp.IsZero() {
		t.Fatalf("token %q exp %v err %v", tok, exp, err)
	}
	if _, _, err := g.InstallationToken(context.Background(), 7); err == nil {
		t.Error("a failed exchange returned no error")
	}
}
