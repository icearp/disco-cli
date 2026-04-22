package store

import (
	"encoding/json"
	"strings"
)

// redactedPlaceholder replaces scrubbed values in attribute JSON.
const redactedPlaceholder = "[REDACTED]"

// sensitiveKeySubstrings are lower-cased substrings; any JSON object key
// containing one has its value replaced with redactedPlaceholder.
// Denylist (not allowlist) keeps maintenance low while catching the high-risk
// surface: SDK responses that embed secrets, tokens, presigned URLs, keys.
var sensitiveKeySubstrings = []string{
	"password",
	"passphrase",
	"secret",
	"token",
	"signature",
	"presignedurl",
	"credential",
	"privatekey",
	"apikey",
	"bearer",
	"authorization",
}

// scrubAttributes walks a JSON blob and redacts values under sensitive keys.
// Returns the original bytes unchanged if parsing fails (malformed JSON is
// caller's problem; we never want to silently drop data).
func scrubAttributes(raw string) string {
	if raw == "" {
		return raw
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	scrubbed := scrubValue(v)
	out, err := json.Marshal(scrubbed)
	if err != nil {
		return raw
	}
	return string(out)
}

// scrubValue recurses over decoded JSON. Maps get key-level inspection;
// slices recurse element-wise; scalars pass through.
func scrubValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if isSensitiveKey(k) {
				t[k] = redactedPlaceholder
				continue
			}
			t[k] = scrubValue(child)
		}
		return t
	case []any:
		for i, child := range t {
			t[i] = scrubValue(child)
		}
		return t
	default:
		return v
	}
}

// isSensitiveKey reports whether key contains any denylist substring.
func isSensitiveKey(key string) bool {
	lk := strings.ToLower(key)
	for _, s := range sensitiveKeySubstrings {
		if strings.Contains(lk, s) {
			return true
		}
	}
	return false
}
