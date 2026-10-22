package util

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

// TestMustJSON_Struct verifies a struct marshals to JSON correctly.
func TestMustJSON_Struct(t *testing.T) {
	v := struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}{"alice", 30}

	got := MustJSON(v)
	want := `{"name":"alice","age":30}`
	if got != want {
		t.Errorf("MustJSON: got %q, want %q", got, want)
	}
}

// TestMustJSON_Nil verifies that nil marshals to "null" (not "{}").
func TestMustJSON_Nil(t *testing.T) {
	got := MustJSON(nil)
	if got != "null" {
		t.Errorf("MustJSON(nil): got %q, want %q", got, "null")
	}
}

// TestMustJSON_Map verifies a map marshals correctly.
func TestMustJSON_Map(t *testing.T) {
	m := map[string]string{"key": "value"}
	got := MustJSON(m)
	if got != `{"key":"value"}` {
		t.Errorf("MustJSON(map): got %q", got)
	}
}

// TestMustJSON_MarshalFailureIsRecognisable pins that a value json.Marshal
// rejects yields a JSON object IsMarshalFailure recognises, carrying the
// marshal error, never a plain "{}" indistinguishable from empty attributes.
func TestMustJSON_MarshalFailureIsRecognisable(t *testing.T) {
	got := MustJSON(struct{ Ratio float64 }{math.NaN()})
	if !IsMarshalFailure(got) {
		t.Errorf("IsMarshalFailure(MustJSON(NaN)) = false for %s; want true", got)
	}
	var obj map[string]string
	if err := json.Unmarshal([]byte(got), &obj); err != nil {
		t.Fatalf("MustJSON(NaN) = %s is not a JSON object: %v", got, err)
	}
	if len(obj) != 1 || obj[marshalFailedKey] == "" {
		t.Errorf("MustJSON(NaN) = %v; want one non-empty %q member", obj, marshalFailedKey)
	}
}

// TestIsMarshalFailure_OrdinaryOutput pins that IsMarshalFailure rejects what
// MustJSON returns for values that do marshal.
func TestIsMarshalFailure_OrdinaryOutput(t *testing.T) {
	for _, v := range []any{nil, map[string]string{}, struct{ A string }{"x"}, []int{1}} {
		if s := MustJSON(v); IsMarshalFailure(s) {
			t.Errorf("IsMarshalFailure(%s) = true; want false", s)
		}
	}
}

// TestIsMarshalFailure_KeyAmongOtherMembers pins that a value which itself
// carries the failure key next to other members is not taken for MustJSON's
// failure object: the store would otherwise skip redacting its other members.
func TestIsMarshalFailure_KeyAmongOtherMembers(t *testing.T) {
	v := struct {
		Err    string `json:"disco:marshalError"`
		Secret string
	}{"x", "hunter2"}
	if s := MustJSON(v); IsMarshalFailure(s) {
		t.Errorf("IsMarshalFailure(%s) = true; want false", s)
	}
}

// TestSv_Nil verifies Sv returns "" for a nil pointer.
func TestSv_Nil(t *testing.T) {
	if got := Sv(nil); got != "" {
		t.Errorf("Sv(nil): got %q, want %q", got, "")
	}
}

// TestSv_Value verifies Sv dereferences a non-nil pointer.
func TestSv_Value(t *testing.T) {
	s := "hello"
	if got := Sv(&s); got != "hello" {
		t.Errorf("Sv(&s): got %q, want %q", got, "hello")
	}
}

// TestTimeRFC3339_Nil verifies a nil *time.Time returns nil.
func TestTimeRFC3339_Nil(t *testing.T) {
	if got := TimeRFC3339(nil); got != nil {
		t.Errorf("TimeRFC3339(nil): got %v, want nil", got)
	}
}

// TestTimeRFC3339_Value verifies a non-nil time formats as RFC3339 UTC.
func TestTimeRFC3339_Value(t *testing.T) {
	ts := time.Date(2024, 3, 15, 12, 0, 0, 0, time.UTC)
	got := TimeRFC3339(&ts)
	if got == nil {
		t.Fatal("TimeRFC3339: got nil, want non-nil")
	}
	if *got != "2024-03-15T12:00:00Z" {
		t.Errorf("TimeRFC3339: got %q, want %q", *got, "2024-03-15T12:00:00Z")
	}
}

// TestAllResources verifies the sentinel is large enough to serve as a
// "no limit" constant in ListResources calls.
func TestAllResources(t *testing.T) {
	if AllResources < 1_000_000 {
		t.Errorf("AllResources = %d, expected a very large number", AllResources)
	}
}
