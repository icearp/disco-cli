package redact

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
)

// Reason-tagged placeholders written by RedactUnlessSafe and
// RedactIfSensitive. The plain
// Placeholder stays the output of RedactScalar and RedactSubtree.
const (
	// PlaceholderKeyName replaces a value whose key name marks it as a
	// secret and whose value is not a reference or a boolean.
	PlaceholderKeyName = "[REDACTED:key-name]"
	// PlaceholderValueShape replaces a value shaped like a credential,
	// whatever its key.
	PlaceholderValueShape = "[REDACTED:value-shape]"
	// PlaceholderRule replaces any other value that is not a safe shape, and
	// every scalar under an object or array found where the rule expected a
	// scalar.
	PlaceholderRule = "[REDACTED:rule]"
)

// classify returns value unchanged only when it is an allowlisted shape,
// else the reason-tagged placeholder that replaces it; sensitiveKey says the
// value's key name marks it as a likely secret. A credential shape anywhere
// in the string redacts first, even inside an allowlisted shape. References
// and booleans are kept under any key, because they name a secret or a
// setting without holding one; resource ids only under a key that is not
// sensitive. Numbers are never kept: a PIN, card or account number is one.
// The credential, reference and resource-id shapes beyond the provider-neutral
// ones come from RegisterSignatures.
//
// Residual: a secret that happens to be, or is written in, an allowlisted
// shape (reference, boolean, resource id) is kept unless it also contains a
// credential shape.
func classify(sensitiveKey bool, value any) any {
	switch v := value.(type) {
	case nil, bool:
		return value
	case string:
		return classifyString(sensitiveKey, v)
	case map[string]any, []any:
		return redactAllScalarsWith(value, PlaceholderRule)
	}
	if sensitiveKey {
		return PlaceholderKeyName
	}
	return PlaceholderRule
}

// classifyIfSensitive is classify for a value that is a setting unless shown
// otherwise: under a sensitive key name classify judges it; under any other
// it is kept unless it is an object or array (classify redacts every scalar
// in it, since inner keys go unjudged) or a string containing a credential
// shape.
func classifyIfSensitive(sensitiveKey bool, value any) any {
	if sensitiveKey {
		return classify(true, value)
	}
	switch v := value.(type) {
	case map[string]any, []any:
		return classify(false, value)
	case string:
		if hasCredential(v) {
			return PlaceholderValueShape
		}
	}
	return value
}

func classifyString(sensitiveKey bool, s string) any {
	sigs := registeredSignatures()
	switch {
	case hasCredential(s):
		return PlaceholderValueShape
	case anyMatch(sigs.Reference, s), isBoolString(s):
		return s
	case sensitiveKey:
		return PlaceholderKeyName
	case anyMatch(sigs.ResourceID, s):
		return s
	}
	return PlaceholderRule
}

// hasCredential reports whether s contains a provider-neutral or a
// registered credential shape.
func hasCredential(s string) bool {
	return isCredentialShape(s) || anyMatch(registeredSignatures().Credential, s)
}

var (
	jwtRE = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]*`)
	pemRE = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]+-----`)
	// Any userinfo, password or not: a token is often the user part alone
	// (https://<token>@github.com).
	userinfoURLRE = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^/?#\s@]+@`)
)

// isCredentialShape reports whether s contains a provider-neutral credential
// shape: a JWT, a PEM block, a URL with userinfo, a connection string with a
// secret field, or a JSON document (whose inner keys a single key name cannot
// vouch for).
func isCredentialShape(s string) bool {
	return jwtRE.MatchString(s) || pemRE.MatchString(s) ||
		userinfoURLRE.MatchString(s) || isConnectionString(s) || isEmbeddedJSON(s)
}

// isConnectionString reports whether s holds a name=value pair, separated
// by ';', '&' or '?', whose name is secret-looking
// ("Server=db;Password=x", "https://h/?token=x").
func isConnectionString(s string) bool {
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == '&' || r == '?' }) {
		if name, _, ok := strings.Cut(part, "="); ok && isSensitiveKey(name) {
			return true
		}
	}
	return false
}

func isEmbeddedJSON(s string) bool {
	t := strings.TrimSpace(s)
	return (strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")) && json.Valid([]byte(t))
}

func isBoolString(s string) bool {
	return strings.EqualFold(s, "true") || strings.EqualFold(s, "false")
}

// sensitiveStems are matched as substrings of the lowercased key with its
// separators removed, so DB_PASSWORD, dbPassword and DBPASSWORD all match.
var sensitiveStems = []string{"passw", "passphrase", "secret", "token", "credential", "apikey", "privatekey", "accesskey"}

// sensitiveWords are too short to match as substrings ("pass" is in
// "bypass", "auth" in "oauthclientid"), so they must be a whole word of the
// key; "key" only as its last word (API_KEY, stripeKey, not KeyName).
var sensitiveWords = []string{"pass", "pwd", "pin", "auth", "creds"}

// isSensitiveKey reports whether key names a value that is likely a secret.
func isSensitiveKey(key string) bool {
	words := keyWords(key)
	if len(words) == 0 {
		return false
	}
	joined := strings.Join(words, "")
	for _, stem := range sensitiveStems {
		if strings.Contains(joined, stem) {
			return true
		}
	}
	for _, w := range words {
		for _, s := range sensitiveWords {
			if w == s {
				return true
			}
		}
	}
	return words[len(words)-1] == "key"
}

// keyWords splits key into lowercase words at non-alphanumerics and at
// lower-to-upper case changes ("dbPass_word" -> db, pass, word).
func keyWords(key string) []string {
	var words []string
	var cur strings.Builder
	var prev rune
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}
	for _, r := range key {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && unicode.IsLower(prev):
			flush()
			cur.WriteRune(unicode.ToLower(r))
		default:
			cur.WriteRune(unicode.ToLower(r))
		}
		prev = r
	}
	flush()
	return words
}
