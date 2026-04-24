package store

import (
	"encoding/json"
	"slices"
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
	"accesskey",        // AWS AccessKeyId and variants
	"connectionstring", // Azure connection strings embed keys
	"sastoken",         // Azure Shared Access Signature tokens
	"keymaterial",      // EC2 key pair material, KMS imports
	"userdata",         // EC2 UserData — init scripts frequently carry secrets
}

// containerRedactKeys are lower-cased key names whose entire scalar
// descendants are redacted wholesale. Used for user-defined key/value
// containers (Lambda Environment.Variables, CodeBuild env vars) where
// individual key names are attacker-controlled and don't match the
// substring denylist but values are effectively always secrets.
var containerRedactKeys = []string{
	"variables", // Lambda Environment.Variables, CodeBuild, StepFunctions
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
// slices recurse element-wise; scalars pass through. A sensitive key only
// redacts a scalar value — if the value is itself an object or array we
// recurse so that structural keys like "Secrets" (a container of
// {ValueFrom} refs) aren't wiped out, while scalar leaks under the same
// key name (e.g. "SecretString") are still redacted.
func scrubValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if isContainerRedactKey(k) {
				t[k] = redactAllScalars(child)
				continue
			}
			switch child.(type) {
			case map[string]any, []any:
				t[k] = scrubValue(child)
			default:
				if isSensitiveKey(k) {
					t[k] = redactedPlaceholder
				} else {
					t[k] = scrubValue(child)
				}
			}
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

// redactAllScalars walks v and replaces every scalar (non-map, non-slice)
// value with redactedPlaceholder. Used for containers whose values are
// user-controlled and assumed secret regardless of key name.
func redactAllScalars(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			t[k] = redactAllScalars(child)
		}
		return t
	case []any:
		for i, child := range t {
			t[i] = redactAllScalars(child)
		}
		return t
	case nil:
		return nil
	default:
		return redactedPlaceholder
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

// isContainerRedactKey reports whether key names a container whose scalar
// descendants should all be redacted.
func isContainerRedactKey(key string) bool {
	return slices.Contains(containerRedactKeys, strings.ToLower(key))
}
