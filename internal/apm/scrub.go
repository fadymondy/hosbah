package apm

import "strings"

// sensitiveKeyParts: an attribute whose key contains any of these (case-insensitively) is dropped
// outright rather than stored, however it arrived (span attribute, resource attribute, log record
// attribute, or an exception event's attributes). Hosbah never stores request bodies or auth headers
// at all — this is the belt-and-braces net for anything an app's own instrumentation attached.
var sensitiveKeyParts = []string{"password", "token", "secret", "authorization", "cookie"}

func isSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, p := range sensitiveKeyParts {
		if strings.Contains(k, p) {
			return true
		}
	}
	return false
}

// maxAttrs and maxAttrValueLen cap how much of a span's attribute set is kept, so one noisy
// instrumentation library cannot blow up storage.
const (
	maxAttrs        = 24
	maxAttrValueLen = 512
)

// scrubAttrs drops sensitive keys, caps value length, and caps the number of attributes kept.
func scrubAttrs(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if isSensitiveKey(k) {
			continue
		}
		if s, ok := v.(string); ok && len(s) > maxAttrValueLen {
			v = s[:maxAttrValueLen]
		}
		out[k] = v
		if len(out) >= maxAttrs {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
