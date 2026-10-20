package redact

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
)

// Reason-tagged placeholders written by RedactUnlessSafe. The plain
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
// setting without holding one; EC2 resource ids only under a key that is not
// sensitive. Numbers are never kept: a PIN, card or account number is one.
//
// Residual: a secret that happens to be, or is written in, an allowlisted
// shape (ARN, SSM/Secrets Manager reference, boolean, EC2 id) is kept unless
// it also contains a credential shape.
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

func classifyString(sensitiveKey bool, s string) any {
	switch {
	case isCredentialShape(s):
		return PlaceholderValueShape
	case isReference(s), isBoolString(s):
		return s
	case sensitiveKey:
		return PlaceholderKeyName
	case isResourceID(s):
		return s
	}
	return PlaceholderRule
}

var (
	// No word boundaries: a key glued to other text ("xAKIA…") is still a key.
	accessKeyRE = regexp.MustCompile(`(?:AKIA|ASIA)[0-9A-Z]{16}`)
	jwtRE       = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]*`)
	pemRE       = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]+-----`)
	// Any userinfo, password or not: a token is often the user part alone
	// (https://<token>@github.com).
	userinfoURLRE = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^/?#\s@]+@`)
)

// isCredentialShape reports whether s contains an AWS access key id, a JWT,
// a PEM block, a URL with userinfo, a connection string with a secret
// field, or a JSON document (whose inner keys a single key name cannot
// vouch for).
func isCredentialShape(s string) bool {
	return accessKeyRE.MatchString(s) || jwtRE.MatchString(s) || pemRE.MatchString(s) ||
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

// ec2IDPrefixes are the EC2 id prefixes kept as resource ids.
var ec2IDPrefixes = []string{
	"sg", "subnet", "vpc", "i", "eni", "vol", "snap", "ami", "eipalloc", "igw",
	"nat", "rtb", "acl", "vpce", "tgw", "tgw-attach", "pcx", "lt",
}

// arnPattern is an unanchored ARN: partition, an empty or real-looking
// region, an empty, 12-digit or "aws" account, and a resource of the
// characters ARNs use, with the Lambda ":$LATEST" qualifier allowed only as
// a suffix.
const arnPattern = `arn:aws(?:-cn|-us-gov|-eusc|-iso(?:-[a-z])?)?:[a-z0-9-]+:(?:[a-z]{2,4}(?:-[a-z]+)+-\d)?:(?:\d{12}|aws)?:[A-Za-z0-9_+=,.@/:*-]+(?::\$LATEST)?`

var (
	arnRE = regexp.MustCompile(`^` + arnPattern + `$`)
	// A CloudFormation dynamic reference to an SSM parameter by name or ARN
	// (optionally versioned) or to a Secrets Manager secret, or the Parameter
	// Store path that references a Secrets Manager secret.
	dynamicRefRE = regexp.MustCompile(`^(?:\{\{resolve:(?:ssm|ssm-secure):(?:` + arnPattern + `|[A-Za-z0-9_./-]+)(?::\d+)?\}\}|\{\{resolve:secretsmanager:[A-Za-z0-9/_+=.@:-]+\}\}|/aws/reference/secretsmanager/[A-Za-z0-9/_+=.@:-]+)$`)
	// An EC2 id: a listed prefix and 8 or 17 lowercase hex digits.
	resourceIDRE = regexp.MustCompile(`^(?:` + strings.Join(ec2IDPrefixes, "|") + `)-(?:[0-9a-f]{8}|[0-9a-f]{17})$`)
)

// maxARNLen is the longest ARN IAM accepts; Go's regexp caps a repeat
// count at 1000, so the length is checked outside arnRE.
const maxARNLen = 2048

// isReference reports whether s is an ARN or a reference to an SSM
// parameter or a Secrets Manager secret: it names where a secret lives
// without holding it.
func isReference(s string) bool {
	return (len(s) <= maxARNLen && arnRE.MatchString(s)) || dynamicRefRE.MatchString(s)
}

func isBoolString(s string) bool {
	return strings.EqualFold(s, "true") || strings.EqualFold(s, "false")
}

func isResourceID(s string) bool { return resourceIDRE.MatchString(s) }

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
