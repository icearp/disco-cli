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
