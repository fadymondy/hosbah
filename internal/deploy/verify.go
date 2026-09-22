package deploy

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

/*
Ownership TXT records for app domains and proxy hosts.

A hostname is verified by a TXT record _hosbah-verify.<hostname> whose value is
hosbah-verify=<hex>. Records made before the rename from Cloudy are named _cloudy-verify.<hostname>
with the value cloudy-verify=<hex>, and tokens stored then carry the cloudy-verify= prefix. Both
names are looked up and either prefix matches, so a record someone already created keeps working
and a new one can use the new name. What people are told to create is always the new form.
*/

const (
	verifyLabel             = "_hosbah-verify."
	legacyVerifyLabel       = "_cloudy-verify."
	verifyValuePrefix       = "hosbah-verify="
	legacyVerifyValuePrefix = "cloudy-verify="
)

// newVerifyToken is the stored token of a new domain or proxy host.
func newVerifyToken(secret []byte) string { return verifyValuePrefix + hex.EncodeToString(secret) }

// VerifyName is the TXT record name to create for hostname.
func VerifyName(hostname string) string { return verifyLabel + hostname }

// VerifyValue is the TXT value to create for a stored token (a pre-rename token is shown in the new
// form; both verify).
func VerifyValue(stored string) string {
	if s := verifySecret(stored); s != "" {
		return verifyValuePrefix + s
	}
	return stored
}

// verifySecret is the random part of a token or a TXT value, whichever prefix it carries.
func verifySecret(v string) string {
	v = strings.TrimSpace(v)
	for _, p := range []string{verifyValuePrefix, legacyVerifyValuePrefix} {
		if s, ok := strings.CutPrefix(v, p); ok {
			return s
		}
	}
	return ""
}

// verifyMatches: a TXT value proves ownership of a stored token when their secrets are equal.
func verifyMatches(record, stored string) bool {
	s := verifySecret(stored)
	return s != "" && verifySecret(record) == s
}

// checkVerifyTXT looks the hostname's verification record up under both names. found reports
// whether any TXT record exists there at all (for the "does not match" message).
func checkVerifyTXT(ctx context.Context, hostname, stored string) (ok, found bool) {
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, name := range []string{verifyLabel + hostname, legacyVerifyLabel + hostname} {
		records, err := LookupTXT(lctx, name)
		if err == nil && len(records) > 0 {
			found = true
		}
		for _, r := range records {
			if verifyMatches(r, stored) {
				return true, true
			}
		}
	}
	return false, found
}

// notVerifiedYet tells a person which record to create, in the new form.
func notVerifiedYet(why, hostname, stored string) string {
	return fmt.Sprintf("Not verified yet (%s). Add a TXT record %s with the value %s, then verify again.",
		why, VerifyName(hostname), VerifyValue(stored))
}
