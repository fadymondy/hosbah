package apm

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// numberOrID matches runs of digits, and long hex/base62-ish tokens (uuids, ids, hashes), so two
// exceptions that differ only by a request id or a row id still fingerprint the same.
var numberOrID = regexp.MustCompile(`[0-9a-fA-F]{8,}|[0-9]+`)

// normalizeMessage strips the parts of an exception message likely to vary between otherwise
// identical occurrences (numbers, ids, hex/uuid-shaped tokens), so fingerprinting groups them.
func normalizeMessage(s string) string {
	s = numberOrID.ReplaceAllString(s, "#")
	return strings.TrimSpace(s)
}

// topFrames returns up to n non-empty lines of stacktrace, normalized, for the fingerprint —
// grouping is on the shape of where the exception happened, not on line numbers or ids in the
// message.
func topFrames(stacktrace string, n int) []string {
	lines := strings.Split(stacktrace, "\n")
	var out []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		out = append(out, normalizeMessage(l))
		if len(out) >= n {
			break
		}
	}
	return out
}

// Fingerprint groups occurrences of "the same" exception into one issue: same type, same
// normalized message, and the same shape of the first few stack frames (numbers/ids stripped so a
// request id or row id in a frame does not fragment the group).
func Fingerprint(typ, message, stacktrace string) string {
	h := sha256.New()
	h.Write([]byte(typ))
	h.Write([]byte{0})
	h.Write([]byte(normalizeMessage(message)))
	for _, f := range topFrames(stacktrace, 5) {
		h.Write([]byte{0})
		h.Write([]byte(f))
	}
	return hex.EncodeToString(h.Sum(nil))
}
