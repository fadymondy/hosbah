package deploy

import (
	"context"
	"strings"
	"testing"
)

// A hostname verifies with the new record or the pre-rename one, whichever prefix its token has.
func TestVerifyTXTAcceptsTheNewAndThePreRenameRecord(t *testing.T) {
	old := LookupTXT
	t.Cleanup(func() { LookupTXT = old })
	records := map[string][]string{}
	LookupTXT = func(_ context.Context, name string) ([]string, error) { return records[name], nil }

	const secret = "0123456789abcdef0123456789abcdef"
	for _, stored := range []string{"cloudy-verify=" + secret, "hosbah-verify=" + secret} {
		for name, value := range map[string]string{
			"_cloudy-verify.shop.example.com": "cloudy-verify=" + secret, // made before the rename
			"_hosbah-verify.shop.example.com": "hosbah-verify=" + secret, // what people are told now
		} {
			clear(records)
			records[name] = []string{"unrelated", " " + value + " "}
			if ok, found := checkVerifyTXT(context.Background(), "shop.example.com", stored); !ok || !found {
				t.Errorf("stored %q, TXT %s=%s: ok=%v found=%v", stored, name, value, ok, found)
			}
		}
	}

	clear(records)
	records["_hosbah-verify.shop.example.com"] = []string{"hosbah-verify=ffff"}
	if ok, found := checkVerifyTXT(context.Background(), "shop.example.com", "cloudy-verify="+secret); ok || !found {
		t.Errorf("a different secret must not verify: ok=%v found=%v", ok, found)
	}
	clear(records)
	if ok, found := checkVerifyTXT(context.Background(), "shop.example.com", "cloudy-verify="+secret); ok || found {
		t.Errorf("no record: ok=%v found=%v", ok, found)
	}
	records["_hosbah-verify.shop.example.com"] = []string{secret} // the bare secret is not a record
	if ok, _ := checkVerifyTXT(context.Background(), "shop.example.com", "cloudy-verify="+secret); ok {
		t.Error("a value without a prefix must not verify")
	}
}

func TestVerifyInstructionsUseTheNewNames(t *testing.T) {
	if VerifyName("a.example.com") != "_hosbah-verify.a.example.com" {
		t.Fatal(VerifyName("a.example.com"))
	}
	if VerifyValue("cloudy-verify=abc") != "hosbah-verify=abc" || VerifyValue("hosbah-verify=abc") != "hosbah-verify=abc" {
		t.Fatal("a stored pre-rename token is shown in the new form")
	}
	if tok := newVerifyToken([]byte{0xab}); tok != "hosbah-verify=ab" {
		t.Fatal(tok)
	}
	if msg := notVerifiedYet("no TXT record found", "a.example.com", "cloudy-verify=abc"); !strings.Contains(msg, "_hosbah-verify.a.example.com") ||
		!strings.Contains(msg, "hosbah-verify=abc") || strings.Contains(msg, "cloudy") {
		t.Fatal(msg)
	}
}
