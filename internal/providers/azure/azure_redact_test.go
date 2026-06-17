package azure

import (
	"encoding/json"
	"testing"

	"codeberg.org/icearp/disco/internal/redact"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
)

func applyAndDecode(t *testing.T, resourceType string, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := redact.Apply(resourceType, string(raw))
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal redacted: %v", err)
	}
	return got
}

func TestRedact_SQLServer_AdminPassword(t *testing.T) {
	in := map[string]any{
		"properties": map[string]any{
			"administratorLogin":         "sqladmin",
			"administratorLoginPassword": "hunter2",
		},
	}
	got := applyAndDecode(t, TypeSQLServer, in)
	props := got["properties"].(map[string]any)
	if props["administratorLoginPassword"] != redact.Placeholder {
		t.Errorf("password not redacted: %v", props["administratorLoginPassword"])
	}
	if props["administratorLogin"] != "sqladmin" {
		t.Errorf("login clobbered")
	}
}

func TestRedact_AppServiceSite_AppSettingsAndConnStrings(t *testing.T) {
	in := map[string]any{
		"properties": map[string]any{
			"siteConfig": map[string]any{
				"appSettings": []any{
					map[string]any{"name": "DB_PASS", "value": "hunter2"},
					map[string]any{"name": "LOG_LEVEL", "value": "debug"},
				},
				"connectionStrings": []any{
					map[string]any{"name": "default", "type": "SQLAzure", "connectionString": "Server=...;Password=hunter2"},
				},
			},
		},
	}
	got := applyAndDecode(t, TypeAppServiceSite, in)
	cfg := got["properties"].(map[string]any)["siteConfig"].(map[string]any)
	for _, e := range cfg["appSettings"].([]any) {
		em := e.(map[string]any)
		if em["value"] != redact.Placeholder {
			t.Errorf("appSetting value not redacted: %v", em)
		}
		if em["name"] == "" {
			t.Errorf("appSetting name clobbered")
		}
	}
	cs := cfg["connectionStrings"].([]any)[0].(map[string]any)
	if cs["connectionString"] != redact.Placeholder {
		t.Errorf("connectionString not redacted: %v", cs["connectionString"])
	}
	if cs["type"] != "SQLAzure" {
		t.Errorf("type clobbered")
	}
}

// TestRedact_CognitiveServicesAccount asserts the plaintext secrets that ship
// on the standard Accounts.List response (apiProperties connection strings /
// search key + migrationToken) come back redacted, while non-secret fields
// (endpoint) survive. Built from the real SDK struct so an SDK field rename
// breaks the test on go mod tidy rather than silently leaking.
func TestRedact_CognitiveServicesAccount(t *testing.T) {
	acct := armcognitiveservices.Account{
		Properties: &armcognitiveservices.AccountProperties{
			Endpoint:       to.Ptr("https://ai.cognitiveservices.azure.com/"),
			MigrationToken: to.Ptr("secret-migration-token"),
			APIProperties: &armcognitiveservices.APIProperties{
				EventHubConnectionString:       to.Ptr("Endpoint=sb://eh/;SharedAccessKey=secret"),
				QnaAzureSearchEndpointKey:      to.Ptr("search-admin-key"),
				StorageAccountConnectionString: to.Ptr("DefaultEndpointsProtocol=https;AccountKey=secret"),
			},
		},
	}
	got := applyAndDecode(t, TypeCognitiveServicesAccount, acct)
	props := got["properties"].(map[string]any)
	if props["migrationToken"] != redact.Placeholder {
		t.Errorf("migrationToken not redacted: %v", props["migrationToken"])
	}
	if props["endpoint"] != "https://ai.cognitiveservices.azure.com/" {
		t.Errorf("endpoint clobbered: %v", props["endpoint"])
	}
	api := props["apiProperties"].(map[string]any)
	for _, k := range []string{"eventHubConnectionString", "qnaAzureSearchEndpointKey", "storageAccountConnectionString"} {
		if api[k] != redact.Placeholder {
			t.Errorf("apiProperties.%s not redacted: %v", k, api[k])
		}
	}
}

func TestRedact_AppServiceSite_KeyVaultRefPreserved(t *testing.T) {
	// Azure Key Vault reference URIs sit at properties.siteConfig.appSettings
	// values; previous sanitize.go preserved them via isReferenceURI shape
	// recogniser. Under per-type rules, the value is a plain scalar at the
	// rule path — REDACTED. That's the intentional trade-off: KV-ref consumers
	// (resolvers wiring App Service → KV) read the *resolved* settings via
	// Microsoft.Web/sites/config Get with KeyVault references, not the raw
	// settings array. If a resolver depends on this URI, it should read from
	// the dedicated config resource (a separate scanner emit).
	in := map[string]any{
		"properties": map[string]any{
			"siteConfig": map[string]any{
				"appSettings": []any{
					map[string]any{"name": "KV_REF", "value": "@Microsoft.KeyVault(SecretUri=https://v.vault.azure.net/secrets/foo/abc)"},
				},
			},
		},
	}
	got := applyAndDecode(t, TypeAppServiceSite, in)
	v := got["properties"].(map[string]any)["siteConfig"].(map[string]any)["appSettings"].([]any)[0].(map[string]any)["value"]
	if v != redact.Placeholder {
		t.Errorf("expected redaction; got %v", v)
	}
}
