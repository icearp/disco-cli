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
