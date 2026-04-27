package azure

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveFunctionAppRelationships covers Functions edges:
//   - AzureWebJobsStorage classic connection string → storage account.
//   - @Microsoft.KeyVault(SecretUri=...) reference → key vault.
//
// Sidecar populated directly (bypassing scanner); resolver consumes it.
func TestResolveFunctionAppRelationships(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-fa")

	storID := upsertTestResource(t, st, "azure", sub.ID, TypeStorageStorageAccount,
		"/subscriptions/sub-fa/resourceGroups/RG/providers/Microsoft.Storage/storageAccounts/funcstor", "eastus", "{}")
	vaultID := upsertTestResource(t, st, "azure", sub.ID, TypeKeyVaultVault,
		"/subscriptions/sub-fa/resourceGroups/RG/providers/Microsoft.KeyVault/vaults/myvault", "eastus", "{}")

	siteNativeID := "/subscriptions/sub-fa/resourceGroups/RG/providers/Microsoft.Web/sites/myfunc"
	siteID := upsertTestResource(t, st, "azure", sub.ID, TypeAppServiceSite, siteNativeID, "eastus", `{"kind":"functionapp"}`)

	// Reset package sidecar between tests then populate.
	functionAppSettingsMu.Lock()
	functionAppSettings = map[string]map[string]map[string]string{}
	functionAppSettingsMu.Unlock()
	recordFunctionAppSettings(sub.ID, siteID, map[string]string{
		"AzureWebJobsStorage":      "DefaultEndpointsProtocol=https;AccountName=funcstor;AccountKey=k;EndpointSuffix=core.windows.net",
		"DB_SECRET":                "@Microsoft.KeyVault(SecretUri=https://myvault.vault.azure.net/secrets/db/abc)",
		"PLAIN":                    "not-a-secret",
		"APPLICATIONINSIGHTS_CONN": "InstrumentationKey=xxx", // ignored — no AI scanner
	})

	if err := resolveFunctionAppRelationships(sub, st); err != nil {
		t.Fatalf("resolveFunctionAppRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(siteID)
	got := map[string]bool{}
	for _, r := range rels {
		got[r.ToID] = true
	}
	if !got[storID] {
		t.Errorf("missing function-app → storage edge, got %+v", rels)
	}
	if !got[vaultID] {
		t.Errorf("missing function-app → vault edge, got %+v", rels)
	}
}

// TestResolveFunctionAppRelationships_IdentityForm verifies the
// AzureWebJobsStorage__accountName identity-form variant resolves.
func TestResolveFunctionAppRelationships_IdentityForm(t *testing.T) {
	st := newTestStore(t)
	sub := newTestSubscription("sub-fa-msi")

	storID := upsertTestResource(t, st, "azure", sub.ID, TypeStorageStorageAccount,
		"/subscriptions/sub-fa-msi/resourceGroups/RG/providers/Microsoft.Storage/storageAccounts/msifunc", "eastus", "{}")
	siteNativeID := "/subscriptions/sub-fa-msi/resourceGroups/RG/providers/Microsoft.Web/sites/msifunc"
	siteID := upsertTestResource(t, st, "azure", sub.ID, TypeAppServiceSite, siteNativeID, "eastus", `{"kind":"functionapp"}`)

	functionAppSettingsMu.Lock()
	functionAppSettings = map[string]map[string]map[string]string{}
	functionAppSettingsMu.Unlock()
	recordFunctionAppSettings(sub.ID, siteID, map[string]string{
		"AzureWebJobsStorage__accountName": "msifunc",
	})

	if err := resolveFunctionAppRelationships(sub, st); err != nil {
		t.Fatalf("resolveFunctionAppRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(siteID)
	if len(rels) != 1 || rels[0].ToID != storID || rels[0].Kind != store.RelUses {
		t.Errorf("identity form: expected uses → storage, got %+v", rels)
	}
}

func TestVaultNameFromKeyVaultReference(t *testing.T) {
	cases := []struct{ in, want string }{
		{"@Microsoft.KeyVault(SecretUri=https://myvault.vault.azure.net/secrets/db/abc)", "myvault"},
		{"@Microsoft.KeyVault(VaultName=myvault;SecretName=db;SecretVersion=v1)", "myvault"},
		{"plain-string", ""},
		{"@Microsoft.KeyVault(garbage)", ""},
	}
	for _, tc := range cases {
		if got := vaultNameFromKeyVaultReference(tc.in); got != tc.want {
			t.Errorf("vaultNameFromKeyVaultReference(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStorageAccountNameFromConnString(t *testing.T) {
	cases := []struct{ in, want string }{
		{"DefaultEndpointsProtocol=https;AccountName=foo;AccountKey=k", "foo"},
		{"accountname=BAR", "BAR"}, // case-insensitive key
		{"NoAccountHere=x", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := storageAccountNameFromConnString(tc.in); got != tc.want {
			t.Errorf("storageAccountNameFromConnString(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
