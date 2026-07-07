package gcp

import (
	"encoding/json"
	"testing"

	"codeberg.org/icearp/disco/internal/redact"
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

func TestRedact_CloudFunction_EnvVariables(t *testing.T) {
	in := map[string]any{
		"name": "fn",
		"serviceConfig": map[string]any{
			"environmentVariables": map[string]any{"DB": "secret", "DEBUG": "1"},
		},
	}
	got := applyAndDecode(t, TypeCloudFunction, in)
	vars := got["serviceConfig"].(map[string]any)["environmentVariables"].(map[string]any)
	if vars["DB"] != redact.Placeholder || vars["DEBUG"] != redact.Placeholder {
		t.Errorf("env not redacted: %v", vars)
	}
}

func TestRedact_CloudRunSvc_ContainerEnv(t *testing.T) {
	in := map[string]any{
		"template": map[string]any{
			"containers": []any{
				map[string]any{
					"env": []any{
						map[string]any{"name": "DB", "value": "secret"},
						map[string]any{"name": "DEBUG", "value": "1"},
					},
				},
			},
		},
	}
	got := applyAndDecode(t, TypeCloudRunSvc, in)
	envs := got["template"].(map[string]any)["containers"].([]any)[0].(map[string]any)["env"].([]any)
	for _, e := range envs {
		em := e.(map[string]any)
		if em["value"] != redact.Placeholder {
			t.Errorf("env value not redacted: %v", em)
		}
	}
}

func TestRedact_SQLInstance_RootPassword(t *testing.T) {
	got := applyAndDecode(t, TypeSQLInstance, map[string]any{
		"name":         "sql1",
		"rootPassword": "hunter2",
	})
	if got["rootPassword"] != redact.Placeholder {
		t.Errorf("rootPassword not redacted")
	}
	if got["name"] != "sql1" {
		t.Errorf("name clobbered")
	}
}

func TestRedact_IAMSAKey_PrivateKeyData(t *testing.T) {
	got := applyAndDecode(t, TypeIAMSAKey, map[string]any{
		"name":           "k1",
		"privateKeyData": "base64encoded",
	})
	if got["privateKeyData"] != redact.Placeholder {
		t.Errorf("privateKeyData not redacted")
	}
}

func TestRedact_SQLUser_Password(t *testing.T) {
	got := applyAndDecode(t, TypeSQLUser, map[string]any{
		"name":     "appuser",
		"password": "hunter2",
	})
	if got["password"] != redact.Placeholder {
		t.Errorf("password not redacted")
	}
	if got["name"] != "appuser" {
		t.Errorf("name clobbered")
	}
}

func TestRedact_IAMCredential_ClientSecret(t *testing.T) {
	got := applyAndDecode(t, TypeIAMCredential, map[string]any{
		"name":         "projects/p1/locations/global/oauthClients/c1/credentials/cred1",
		"displayName":  "prod credential",
		"clientSecret": "s3cr3t-live-value",
	})
	if got["clientSecret"] != redact.Placeholder {
		t.Errorf("clientSecret not redacted")
	}
	if got["displayName"] != "prod credential" {
		t.Errorf("displayName clobbered")
	}
}

func TestRedact_IAMProvider_OidcClientSecret(t *testing.T) {
	got := applyAndDecode(t, TypeIAMProvider, map[string]any{
		"name":        "locations/global/workforcePools/pool1/providers/prov1",
		"displayName": "Okta",
		"oidc": map[string]any{
			"issuerUri": "https://okta.example.com",
			"clientSecret": map[string]any{
				"value": map[string]any{
					"plainText":  "s3cr3t",
					"thumbprint": "abc123",
				},
			},
		},
	})
	oidc, ok := got["oidc"].(map[string]any)
	if !ok {
		t.Fatalf("oidc missing or wrong shape: %+v", got)
	}
	value, ok := oidc["clientSecret"].(map[string]any)["value"].(map[string]any)
	if !ok {
		t.Fatalf("oidc.clientSecret.value missing or wrong shape: %+v", oidc)
	}
	if value["plainText"] != redact.Placeholder {
		t.Errorf("plainText not redacted")
	}
	if value["thumbprint"] != "abc123" {
		t.Errorf("thumbprint clobbered")
	}
	if oidc["issuerUri"] != "https://okta.example.com" {
		t.Errorf("issuerUri clobbered")
	}
}

func TestRedact_CloudIdentityInboundOidcSsoProfile_ClientSecret(t *testing.T) {
	got := applyAndDecode(t, TypeCloudIdentityInboundOidcSsoProfile, map[string]any{
		"name":        "inboundOidcSsoProfiles/o1",
		"displayName": "Okta OIDC",
		"rpConfig": map[string]any{
			"clientId":     "abc123",
			"clientSecret": "s3cr3t",
		},
	})
	rpConfig, ok := got["rpConfig"].(map[string]any)
	if !ok {
		t.Fatalf("rpConfig missing or wrong shape: %+v", got)
	}
	if rpConfig["clientSecret"] != redact.Placeholder {
		t.Errorf("clientSecret not redacted")
	}
	if rpConfig["clientId"] != "abc123" {
		t.Errorf("clientId clobbered")
	}
	if got["displayName"] != "Okta OIDC" {
		t.Errorf("displayName clobbered")
	}
}
