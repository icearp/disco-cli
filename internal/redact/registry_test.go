package redact

import (
	"encoding/json"
	"testing"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestApply_ScalarLiteralPath(t *testing.T) {
	resetForTest()
	Register(TypeRules{
		Type:       "x:y",
		Attributes: []Rule{{Path: "MasterUserPassword", Mode: RedactScalar}},
	})
	in := `{"MasterUsername":"admin","MasterUserPassword":"hunter2"}`
	out := Apply("x:y", in)
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["MasterUserPassword"] != Placeholder {
		t.Errorf("password not redacted: %v", got["MasterUserPassword"])
	}
	if got["MasterUsername"] != "admin" {
		t.Errorf("username altered: %v", got["MasterUsername"])
	}
}

func TestApply_MapWildcard(t *testing.T) {
	resetForTest()
	Register(TypeRules{
		Type:       "lambda",
		Attributes: []Rule{{Path: "Environment.Variables.*", Mode: RedactScalar}},
	})
	in := mustJSON(t, map[string]any{
		"Environment": map[string]any{
			"Variables": map[string]any{"DB_PASS": "s3cret", "DEBUG": "1"},
		},
	})
	out := Apply("lambda", in)
	var got map[string]any
	_ = json.Unmarshal([]byte(out), &got)
	vars := got["Environment"].(map[string]any)["Variables"].(map[string]any)
	if vars["DB_PASS"] != Placeholder || vars["DEBUG"] != Placeholder {
		t.Errorf("wildcard redact missed: %v", vars)
	}
}

func TestApply_ArrayWildcard(t *testing.T) {
	resetForTest()
	Register(TypeRules{
		Type: "ecs",
		Attributes: []Rule{
			{Path: "ContainerDefinitions[*].Secrets[*].Value", Mode: RedactScalar},
		},
	})
	in := mustJSON(t, map[string]any{
		"ContainerDefinitions": []any{
			map[string]any{
				"Name": "app",
				"Secrets": []any{
					map[string]any{"Name": "DB_URL", "Value": "postgres://x"},
				},
			},
		},
	})
	out := Apply("ecs", in)
	var got map[string]any
	_ = json.Unmarshal([]byte(out), &got)
	secrets := got["ContainerDefinitions"].([]any)[0].(map[string]any)["Secrets"].([]any)
	v := secrets[0].(map[string]any)
	if v["Value"] != Placeholder {
		t.Errorf("array-wildcard miss: %v", v)
	}
	if v["Name"] != "DB_URL" {
		t.Errorf("sibling clobbered: %v", v["Name"])
	}
}

func TestApply_SubtreeMode(t *testing.T) {
	resetForTest()
	Register(TypeRules{
		Type:       "kafka",
		Attributes: []Rule{{Path: "ClientAuthentication.Sasl", Mode: RedactSubtree}},
	})
	in := `{"ClientAuthentication":{"Sasl":{"Scram":{"Enabled":true,"Inner":"x"}},"Tls":{"Enabled":true}}}`
	out := Apply("kafka", in)
	var got map[string]any
	_ = json.Unmarshal([]byte(out), &got)
	scram := got["ClientAuthentication"].(map[string]any)["Sasl"].(map[string]any)["Scram"].(map[string]any)
	if scram["Enabled"] != Placeholder || scram["Inner"] != Placeholder {
		t.Errorf("subtree miss: %v", scram)
	}
	if got["ClientAuthentication"].(map[string]any)["Tls"].(map[string]any)["Enabled"] != true {
		t.Errorf("Tls clobbered")
	}
}

func TestApply_NoRulesPassthrough(t *testing.T) {
	resetForTest()
	in := `{"x":1}`
	if Apply("nope", in) != in {
		t.Errorf("expected passthrough")
	}
}

func TestApply_MalformedJSONPassthrough(t *testing.T) {
	resetForTest()
	Register(TypeRules{Type: "t", Attributes: []Rule{{Path: "x", Mode: RedactScalar}}})
	in := `{not json`
	if Apply("t", in) != in {
		t.Errorf("expected passthrough on malformed")
	}
}

func TestApply_MissingPathSilent(t *testing.T) {
	resetForTest()
	Register(TypeRules{Type: "t", Attributes: []Rule{{Path: "Missing.Path", Mode: RedactScalar}}})
	in := `{"x":1}`
	out := Apply("t", in)
	if out != in {
		t.Errorf("expected unchanged: %s", out)
	}
}

func TestApply_ScalarModeOnObjectLeavesAlone(t *testing.T) {
	resetForTest()
	Register(TypeRules{Type: "t", Attributes: []Rule{{Path: "Obj", Mode: RedactScalar}}})
	in := `{"Obj":{"k":"v"}}`
	out := Apply("t", in)
	var got map[string]any
	_ = json.Unmarshal([]byte(out), &got)
	obj := got["Obj"].(map[string]any)
	if obj["k"] != "v" {
		t.Errorf("RedactScalar should not touch object children: %v", obj)
	}
}

func TestHasRules(t *testing.T) {
	resetForTest()
	Register(TypeRules{Type: "a", Attributes: []Rule{{Path: "x", Mode: RedactScalar}}})
	if !HasRules("a") {
		t.Errorf("expected HasRules(a)=true")
	}
	if HasRules("b") {
		t.Errorf("expected HasRules(b)=false")
	}
}

func TestRegister_AppendsSameType(t *testing.T) {
	resetForTest()
	Register(TypeRules{Type: "z", Attributes: []Rule{{Path: "A", Mode: RedactScalar}}})
	Register(TypeRules{Type: "z", Attributes: []Rule{{Path: "B", Mode: RedactScalar}}})
	in := `{"A":"1","B":"2","C":"3"}`
	out := Apply("z", in)
	var got map[string]any
	_ = json.Unmarshal([]byte(out), &got)
	if got["A"] != Placeholder || got["B"] != Placeholder || got["C"] != "3" {
		t.Errorf("append-rules miss: %v", got)
	}
}
