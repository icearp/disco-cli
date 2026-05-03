package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestScrubAttributes_RedactsSensitiveKeys(t *testing.T) {
	input := `{
		"Name": "mySecret",
		"SecretString": "hunter2",
		"ARN": "arn:aws:secretsmanager:us-east-1:123:secret:foo",
		"Password": "p@ss",
		"nested": {
			"AccessToken": "abc",
			"SessionToken": "xyz",
			"Other": "keep"
		},
		"items": [
			{"apiKey": "k1", "region": "us-east-1"},
			{"signatureV4": "sig"}
		],
		"PresignedURL": "https://example/?X-Amz-Signature=..."
	}`

	out := scrubAttributes(input)
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Values should become "[REDACTED]"; structure intact.
	mustRedacted := []string{"SecretString", "Password", "PresignedURL"}
	for _, k := range mustRedacted {
		if got[k] != redactedPlaceholder {
			t.Errorf("%s not redacted: %v", k, got[k])
		}
	}
	nested := got["nested"].(map[string]any)
	if nested["AccessToken"] != redactedPlaceholder {
		t.Errorf("nested.AccessToken not redacted")
	}
	if nested["SessionToken"] != redactedPlaceholder {
		t.Errorf("nested.SessionToken not redacted")
	}
	if nested["Other"] != "keep" {
		t.Errorf("nested.Other clobbered")
	}

	// Non-sensitive keys preserved.
	if got["Name"] != "mySecret" {
		t.Errorf("Name clobbered")
	}
	if got["ARN"] == redactedPlaceholder {
		t.Errorf("ARN falsely redacted")
	}

	items := got["items"].([]any)
	first := items[0].(map[string]any)
	if first["apiKey"] != redactedPlaceholder {
		t.Errorf("items[0].apiKey not redacted")
	}
	if first["region"] != "us-east-1" {
		t.Errorf("items[0].region clobbered")
	}
	second := items[1].(map[string]any)
	if second["signatureV4"] != redactedPlaceholder {
		t.Errorf("items[1].signatureV4 not redacted")
	}
}

func TestScrubAttributes_EmptyAndMalformed(t *testing.T) {
	if got := scrubAttributes(""); got != "" {
		t.Errorf("empty input changed: %q", got)
	}
	// Malformed JSON passes through untouched.
	bad := `{"not":valid`
	if got := scrubAttributes(bad); got != bad {
		t.Errorf("malformed mutated: %q", got)
	}
}

func TestScrubAttributes_ExtendedDenylist(t *testing.T) {
	input := `{
		"AccessKeyId": "AKIA...",
		"ConnectionString": "Endpoint=sb://x;SharedAccessKey=abc",
		"SasToken": "?sv=2021",
		"KeyMaterial": "-----BEGIN PRIVATE KEY-----",
		"UserData": "#!/bin/bash\nexport DB_PASS=hunter2"
	}`
	out := scrubAttributes(input)
	for _, k := range []string{"AccessKeyId", "ConnectionString", "SasToken", "KeyMaterial", "UserData"} {
		if !strings.Contains(out, `"`+k+`":"[REDACTED]"`) {
			t.Errorf("%s not redacted: %s", k, out)
		}
	}
}

func TestScrubAttributes_LambdaEnvironmentVariables(t *testing.T) {
	input := `{
		"FunctionName": "my-fn",
		"Environment": {
			"Variables": {
				"DATABASE_URL": "postgres://u:p@h/db",
				"OPENAI_KEY": "sk-abc",
				"LOG_LEVEL": "debug"
			}
		}
	}`
	out := scrubAttributes(input)
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["FunctionName"] != "my-fn" {
		t.Errorf("FunctionName clobbered: %v", got["FunctionName"])
	}
	vars := got["Environment"].(map[string]any)["Variables"].(map[string]any)
	for _, k := range []string{"DATABASE_URL", "OPENAI_KEY", "LOG_LEVEL"} {
		if vars[k] != redactedPlaceholder {
			t.Errorf("Variables[%s] not redacted: %v", k, vars[k])
		}
	}
}

func TestScrubAttributes_CaseInsensitive(t *testing.T) {
	input := `{"PASSWORD":"x","Secret_Token":"y","auth":"z"}`
	out := scrubAttributes(input)
	if !strings.Contains(out, `"PASSWORD":"[REDACTED]"`) {
		t.Errorf("uppercase PASSWORD not redacted: %s", out)
	}
	if !strings.Contains(out, `"Secret_Token":"[REDACTED]"`) {
		t.Errorf("Secret_Token not redacted: %s", out)
	}
	// "auth" alone isn't in denylist (only "authorization"); preserved.
	if strings.Contains(out, `"auth":"[REDACTED]"`) {
		t.Errorf("bare 'auth' over-redacted")
	}
}

func TestUpsertResources_ScrubsAttributes(t *testing.T) {
	st := openTestStore(t)
	attrs := `{"SecretString":"hunter2","Name":"ok"}`
	id := ResourceID("aws", "111", "aws:secretsmanager:secret", "foo")
	r := &Resource{
		ID:             id,
		Provider:       "aws",
		AccountID:      "111",
		Type:           "aws:secretsmanager:secret",
		NativeID:       "foo",
		AttributesJSON: attrs,
		DiscoveredBy:   testScanID,
	}
	if _, err := st.UpsertResource(r); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := st.GetResource(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if strings.Contains(got.AttributesJSON, "hunter2") {
		t.Errorf("secret leaked into DB: %s", got.AttributesJSON)
	}
	if !strings.Contains(got.AttributesJSON, redactedPlaceholder) {
		t.Errorf("no redaction marker in stored attrs: %s", got.AttributesJSON)
	}
}

// TestScrubAttributes_KeyVaultRefURIAllowed verifies that an Azure Key Vault
// reference URI (a pointer, not material) is preserved verbatim under a key
// whose name matches the denylist. Resolvers downstream (AGW → KV, App
// Service config refs, etc.) need the URI to FK to the vault.
func TestScrubAttributes_KeyVaultRefURIAllowed(t *testing.T) {
	input := `{
		"properties": {
			"sslCertificates": [
				{"name": "cert1", "properties": {"keyVaultSecretId": "https://myvault.vault.azure.net/secrets/cert1/abc123"}},
				{"name": "cert2", "properties": {"keyVaultSecretId": "https://gov.vault.usgovcloudapi.net/certificates/cert2"}}
			],
			"someSecretToken": "hunter2",
			"keyVaultSecretId": "not-a-uri"
		}
	}`
	out := scrubAttributes(input)
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	props := got["properties"].(map[string]any)
	certs := props["sslCertificates"].([]any)
	for i, want := range []string{
		"https://myvault.vault.azure.net/secrets/cert1/abc123",
		"https://gov.vault.usgovcloudapi.net/certificates/cert2",
	} {
		c := certs[i].(map[string]any)["properties"].(map[string]any)
		if c["keyVaultSecretId"] != want {
			t.Errorf("ssl cert %d: keyVaultSecretId got %v, want %q", i, c["keyVaultSecretId"], want)
		}
	}
	if props["someSecretToken"] != redactedPlaceholder {
		t.Errorf("scalar secret value should still redact: %v", props["someSecretToken"])
	}
	if props["keyVaultSecretId"] != redactedPlaceholder {
		t.Errorf("non-URI value under denylist key must redact: %v", props["keyVaultSecretId"])
	}
}

func TestIsReferenceURI(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://myvault.vault.azure.net/secrets/foo", true},
		{"https://myvault.vault.azure.net/secrets/foo/v1", true},
		{"https://myvault.vault.azure.net/keys/k1", true},
		{"https://myvault.vault.azure.net/certificates/c1", true},
		{"https://gov.vault.usgovcloudapi.net/secrets/foo", true},
		{"https://cn.vault.azure.cn/secrets/foo", true},
		{"https://de.vault.microsoftazure.de/secrets/foo", true},
		{"https://myvault.vault.azure.net/", false}, // no object path
		{"https://myvault.vault.azure.net/secrets/", false},
		{"https://example.com/secrets/foo", false},            // not a vault host
		{"http://myvault.vault.azure.net/secrets/foo", false}, // not https
		{"hunter2", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isReferenceURI(tc.in); got != tc.want {
			t.Errorf("isReferenceURI(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestIsAWSARN(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"arn:aws:iam::123456789012:role/MyRole", true},
		{"arn:aws:secretsmanager:us-east-1:123:secret:db/pw", true},
		{"arn:aws:apigateway:us-east-1::/apis/abc/integrations/i1", true},
		{"arn:aws-cn:s3:::bucket/key", true},
		{"arn:aws-us-gov:lambda:us-gov-west-1:123:function:f", true},
		{"arn:awsfoo:s3:::bucket", false}, // partition must terminate at colon/dash
		{"arn:aws:iam:role", false},       // only 3 colons
		{"plain-secret-string", false},
		{"https://example.com/secrets/foo", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isAWSARN(tc.in); got != tc.want {
			t.Errorf("isAWSARN(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestScrubAttributesAllowsARNUnderSensitiveKey(t *testing.T) {
	in := `{"CredentialsArn":"arn:aws:iam::123456789012:role/MyRole","SecretString":"hunter2","SecretArn":"arn:aws:secretsmanager:us-east-1:123:secret:db/pw","Token":"raw-token"}`
	out := scrubAttributes(in)
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["CredentialsArn"] != "arn:aws:iam::123456789012:role/MyRole" {
		t.Errorf("CredentialsArn ARN should pass through: %v", m["CredentialsArn"])
	}
	if m["SecretArn"] != "arn:aws:secretsmanager:us-east-1:123:secret:db/pw" {
		t.Errorf("SecretArn should pass through: %v", m["SecretArn"])
	}
	if m["SecretString"] != redactedPlaceholder {
		t.Errorf("SecretString must still redact: %v", m["SecretString"])
	}
	if m["Token"] != redactedPlaceholder {
		t.Errorf("Token must still redact: %v", m["Token"])
	}
}
