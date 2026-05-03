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
//
// Reference-URI allowlist: a scalar value whose shape matches a known
// pointer-not-material pattern (e.g. an Azure Key Vault reference URI
// `https://{vault}.vault.azure.net/{secrets|keys|certificates}/{name}[/{ver}]`)
// is preserved verbatim even when its key matches the denylist. This unblocks
// resolver edges keyed on the reference target (AGW → Key Vault, App Service
// → KV-backed app settings, AKS secret-store CSI, etc.) without leaking
// secret material — the URI is a pointer, the secret stays in Key Vault.
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
					if s, ok := child.(string); ok && (isReferenceURI(s) || isAWSARN(s)) {
						t[k] = s
					} else {
						t[k] = redactedPlaceholder
					}
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

// keyVaultDNSSuffixes covers Azure public, US-government, China, and Germany
// Key Vault DNS roots. Used by isReferenceURI to allow KV reference URIs
// (pointers, not material) past the denylist.
var keyVaultDNSSuffixes = []string{
	".vault.azure.net",
	".vault.usgovcloudapi.net",
	".vault.azure.cn",
	".vault.microsoftazure.de",
}

// keyVaultObjectPaths are the path prefixes immediately after the vault host
// for KV reference URIs.
var keyVaultObjectPaths = []string{"/secrets/", "/keys/", "/certificates/"}

// isReferenceURI reports whether s is a known pointer-style URI whose shape
// guarantees no secret material is embedded — only addressing data (vault
// host + object name + optional version). Currently recognises Azure Key
// Vault reference URIs across all four cloud DNS suffixes; extend the lists
// to add more pointer shapes (e.g. AWS Secrets Manager ARNs already FK by
// shape, no allowlist needed).
// isAWSARN reports whether s is shaped as an AWS ARN. ARNs are pointers
// (service + region + account + resource path) and never embed secret
// material, so a denylisted key whose value is an ARN — `CredentialsArn`,
// `SecretArn`, `TokenSourceArn`, `AuthorizationHeaderArn`, etc. — is safe to
// preserve so resolvers can wire the edge. Shape: `arn:<partition>:<service>:`
// where partition matches `aws`, `aws-cn`, `aws-us-gov`. Requires at least
// the five colons of a canonical ARN to avoid letting through bare strings
// that merely start with "arn:".
func isAWSARN(s string) bool {
	if !strings.HasPrefix(s, "arn:aws") {
		return false
	}
	// "arn:aws:s:r:a:..." → 5 colons minimum.
	if strings.Count(s, ":") < 5 {
		return false
	}
	// Reject `arn:awsfoo:...` — partition must terminate at colon.
	rest := s[len("arn:aws"):]
	if !(strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, "-cn:") || strings.HasPrefix(rest, "-us-gov:") || strings.HasPrefix(rest, "-iso:") || strings.HasPrefix(rest, "-iso-b:")) {
		return false
	}
	return true
}

func isReferenceURI(s string) bool {
	if !strings.HasPrefix(s, "https://") {
		return false
	}
	rest := s[len("https://"):]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return false
	}
	host := strings.ToLower(rest[:slash])
	path := rest[slash:]
	hostMatch := false
	for _, suffix := range keyVaultDNSSuffixes {
		if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
			hostMatch = true
			break
		}
	}
	if !hostMatch {
		return false
	}
	for _, p := range keyVaultObjectPaths {
		if strings.HasPrefix(path, p) && len(path) > len(p) {
			return true
		}
	}
	return false
}
