//go:build paid

package serve

import "strings"

// forbiddenBodyKeys are JSON keys the scan request body MUST NOT carry.
// Two flavours:
//
//   - cloud credentials: callers must use ambient creds (Fargate task IAM
//     role / env / ADC). Letting a body specify creds turns the API into
//     an arbitrary-credential proxy.
//   - server-pinned config: dsn, pg_dsn, database_url, tenant_id. Only the
//     container's startup env (set by Lambda RunTask) defines these. A body
//     carrying them is either a misconfigured caller or an attempt to make
//     the container act on a different tenant's data.
//
// Match is case-insensitive and recursive — a nested object like
// `{"aws": {"access_key": "..."}}` is rejected.
var forbiddenBodyKeys = map[string]struct{}{
	"credentials":          {},
	"access_key":           {},
	"secret_key":           {},
	"service_account_json": {},
	"client_secret":        {},
	"password":             {},
	"api_key":              {},
	"bearer_token":         {},
	"dsn":                  {},
	"pg_dsn":               {},
	"database_url":         {},
	"tenant_id":            {},
}

// scrubBody returns the offending key path (e.g. "aws.access_key") if any
// forbidden key is present anywhere in the decoded body, "" otherwise.
// Callers should reject 400 with `details: {"forbidden_key": "<path>"}`.
//
// The walker handles arbitrary JSON: maps recurse, slices iterate, scalars
// stop. Forbidden keys are matched on the path segment, not the value, so
// `{"aws": {"region": "us-east-1"}}` is fine but `{"x": {"password": ...}}`
// is rejected even when nested deep.
func scrubBody(v any) string {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			lk := strings.ToLower(k)
			if _, bad := forbiddenBodyKeys[lk]; bad {
				return k
			}
			if hit := scrubBody(child); hit != "" {
				return k + "." + hit
			}
		}
	case []any:
		for i, el := range t {
			if hit := scrubBody(el); hit != "" {
				return formatIndex(i) + "." + hit
			}
		}
	}
	return ""
}

// formatIndex stringifies an array index for the error path.
func formatIndex(i int) string {
	if i == 0 {
		return "[0]"
	}
	// Tiny manual itoa avoids strconv import for one call site.
	var digits [20]byte
	pos := len(digits)
	for i > 0 {
		pos--
		digits[pos] = byte('0' + i%10)
		i /= 10
	}
	return "[" + string(digits[pos:]) + "]"
}
