package volatile

import (
	"encoding/json"
	"testing"
)

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("unmarshal %q: %v", s, err)
	}
	return m
}

func TestApply_DropsTopLevelKey(t *testing.T) {
	resetForTest()
	Register(TypeRules{Type: "x:y", Paths: []string{"UploadSequenceToken"}})

	in := `{"LogStreamName":"s1","UploadSequenceToken":"4903985960"}`
	got := decode(t, Apply("x:y", in))

	if _, ok := got["UploadSequenceToken"]; ok {
		t.Errorf("UploadSequenceToken should be dropped, got %v", got["UploadSequenceToken"])
	}
	if got["LogStreamName"] != "s1" {
		t.Errorf("sibling key altered: %v", got["LogStreamName"])
	}
}

func TestApply_DropsNestedDottedKey(t *testing.T) {
	resetForTest()
	Register(TypeRules{Type: "x:y", Paths: []string{"A.B"}})

	in := `{"A":{"B":"drop","C":"keep"},"D":"keep"}`
	got := decode(t, Apply("x:y", in))

	a, ok := got["A"].(map[string]any)
	if !ok {
		t.Fatalf("A not an object: %v", got["A"])
	}
	if _, ok := a["B"]; ok {
		t.Errorf("A.B should be dropped, got %v", a["B"])
	}
	if a["C"] != "keep" {
		t.Errorf("A.C altered: %v", a["C"])
	}
	if got["D"] != "keep" {
		t.Errorf("D altered: %v", got["D"])
	}
}

func TestApply_MissingKeyNoOp(t *testing.T) {
	resetForTest()
	Register(TypeRules{Type: "x:y", Paths: []string{"UploadSequenceToken", "A.B"}})

	in := `{"LogStreamName":"s1"}`
	out := Apply("x:y", in)
	if got := decode(t, out); got["LogStreamName"] != "s1" {
		t.Errorf("unexpected change: %v", got)
	}
}

func TestApply_NoRulesReturnsRawUnchanged(t *testing.T) {
	resetForTest()
	in := `{"UploadSequenceToken":"4903985960"}`
	if out := Apply("unregistered", in); out != in {
		t.Errorf("no-rules Apply altered input: %q", out)
	}
}

func TestApply_MalformedJSONReturnedUnchanged(t *testing.T) {
	resetForTest()
	Register(TypeRules{Type: "x:y", Paths: []string{"UploadSequenceToken"}})

	in := `{not valid json`
	if out := Apply("x:y", in); out != in {
		t.Errorf("malformed JSON must pass through unchanged, got %q", out)
	}
}

func TestApply_EmptyInputUnchanged(t *testing.T) {
	resetForTest()
	Register(TypeRules{Type: "x:y", Paths: []string{"UploadSequenceToken"}})
	if out := Apply("x:y", ""); out != "" {
		t.Errorf("empty input must stay empty, got %q", out)
	}
}

func TestHasRules(t *testing.T) {
	resetForTest()
	Register(TypeRules{Type: "x:y", Paths: []string{"Token"}})
	if !HasRules("x:y") {
		t.Error("HasRules(x:y) = false, want true")
	}
	if HasRules("other") {
		t.Error("HasRules(other) = true, want false")
	}
}

// TestRegister_IgnoresEmpty guards the defensive no-ops: an empty Type, an empty
// path list, or all-empty path strings register nothing.
func TestRegister_IgnoresEmpty(t *testing.T) {
	resetForTest()
	Register(TypeRules{Type: "", Paths: []string{"Token"}})
	Register(TypeRules{Type: "x:y", Paths: nil})
	Register(TypeRules{Type: "x:y", Paths: []string{""}})
	if HasRules("") || HasRules("x:y") {
		t.Error("empty registrations should install no rules")
	}
}
