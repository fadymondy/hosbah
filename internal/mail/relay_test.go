package mail

import (
	"regexp"
	"testing"

	"github.com/fadymondy/hosbah/internal/agentproto"
	db "github.com/fadymondy/hosbah/internal/db/gen"
)

func TestRandRelayLocalPartIsValidAndVaries(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		lp := randRelayLocalPart()
		if !agentproto.ValidMailLocalPart(lp) {
			t.Fatalf("randRelayLocalPart produced an invalid local part: %q", lp)
		}
		if !regexp.MustCompile(`^relay-[a-z0-9]{10}$`).MatchString(lp) {
			t.Fatalf("randRelayLocalPart shape changed unexpectedly: %q", lp)
		}
		seen[lp] = true
	}
	if len(seen) < 45 {
		t.Fatalf("randRelayLocalPart looks non-random: only %d distinct values in 50 draws", len(seen))
	}
}

func TestRelaySealContextIsBoundToID(t *testing.T) {
	a, b := relaySealContext("id-a"), relaySealContext("id-b")
	if a == b {
		t.Fatal("two different relay ids must not share a seal context")
	}
	if relaySealContext("id-a") != a {
		t.Fatal("relaySealContext must be deterministic for the same id")
	}
}

func TestSMTPEnvVarsAndNamesMatch(t *testing.T) {
	kv := smtpEnvVars("mail.example.com", "relay-abc@example.com", "s3cr3t")
	names := smtpEnvNames()
	if len(kv) != len(names) {
		t.Fatalf("smtpEnvVars and smtpEnvNames must cover the same set: %d vs %d", len(kv), len(names))
	}
	got := map[string]string{}
	for _, pair := range kv {
		got[pair[0]] = pair[1]
	}
	for _, name := range names {
		if _, ok := got[name]; !ok {
			t.Errorf("smtpEnvNames lists %s but smtpEnvVars does not set it", name)
		}
	}
	if got["SMTP_HOST"] != "mail.example.com" || got["SMTP_PORT"] != "587" ||
		got["SMTP_USERNAME"] != "relay-abc@example.com" || got["SMTP_PASSWORD"] != "s3cr3t" ||
		got["SMTP_FROM"] != "relay-abc@example.com" {
		t.Fatalf("smtpEnvVars set unexpected values: %+v", got)
	}
}

func TestRelayViewNeverCarriesAPassword(t *testing.T) {
	row := db.MailRelayCredential{ID: "r1", MailDomainID: "d1", AppID: "a1", LocalPart: "relay-xyz", Status: "ready"}
	v := relayView(row, "example.com")
	if v.Email != "relay-xyz@example.com" {
		t.Fatalf("unexpected email: %s", v.Email)
	}
	// Relay (the API-facing type) has no password field at all; this is a compile-time guarantee,
	// exercised here so a future field addition is deliberate, not an accidental leak.
	_ = struct {
		ID, MailDomainID, AppID, LocalPart, Email string
		Attached                                  bool
		Status                                    string
		Error                                     *string
	}(v)
}
