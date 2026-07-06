package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResource_MarshalJSON_SnakeAndNested(t *testing.T) {
	tags := `{"env":"prod"}`
	name := "web"
	r := Resource{
		ID: "abc", Provider: "aws", AccountID: "111",
		Type: "aws:ec2:volume", NativeID: "vol-1",
		Name:           &name,
		AttributesJSON: `{"Encrypted":false,"Size":8}`,
		TagsJSON:       &tags,
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if _, ok := m["NativeID"]; ok {
		t.Errorf("PascalCase NativeID leaked: %s", b)
	}
	if m["native_id"] != "vol-1" {
		t.Errorf("want native_id=vol-1, got %v", m["native_id"])
	}
	attrs, ok := m["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("attributes not nested object: %T %v", m["attributes"], m["attributes"])
	}
	if attrs["Encrypted"] != false {
		t.Errorf("attrs.Encrypted: got %v", attrs["Encrypted"])
	}
	tagsOut, ok := m["tags"].(map[string]any)
	if !ok || tagsOut["env"] != "prod" {
		t.Errorf("tags not nested or wrong: %v", m["tags"])
	}
	for _, banned := range []string{"AttributesJSON", "TagsJSON"} {
		if _, ok := m[banned]; ok {
			t.Errorf("legacy key %s still emitted: %s", banned, b)
		}
	}
}

// TestResource_MarshalJSON_AlwaysPresent enforces F6 (focus-group review):
// documented Resource keys (`name`, `tags`, `attributes`, ...) always emit as
// `null`/`{}` zero values, never dropped, so consumers can traverse
// `r.attributes.X` / `r.tags.Y` without presence guards.
func TestResource_MarshalJSON_AlwaysPresent(t *testing.T) {
	r := Resource{ID: "abc", Provider: "aws", AccountID: "111", Type: "aws:s3:bucket", NativeID: "b1"}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, key := range []string{`"attributes":{}`, `"tags":{}`, `"name":null`, `"status":null`, `"managed_by_provider":false`} {
		if !strings.Contains(s, key) {
			t.Errorf("expected %q in output, got %s", key, s)
		}
	}
}

func TestResource_MarshalJSON_MalformedAttrs(t *testing.T) {
	r := Resource{
		ID: "abc", Provider: "aws", AccountID: "111", Type: "aws:s3:bucket", NativeID: "b1",
		AttributesJSON: `not json`,
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal must not error on malformed attrs: %v", err)
	}
	// Malformed legacy blob renders as the empty-object zero, not omitted —
	// consumers see "no parsed attrs" without breaking the schema contract.
	if !strings.Contains(string(b), `"attributes":{}`) {
		t.Errorf("invalid attrs should render as {}, got: %s", b)
	}
}

func TestResource_RoundTrip(t *testing.T) {
	tags := `{"env":"prod","team":"core"}`
	name := "web"
	region := "us-east-2"
	orig := Resource{
		ID: "abc", Provider: "aws", AccountID: "111",
		Type: "aws:ec2:volume", NativeID: "vol-1",
		Name: &name, Region: &region,
		AttributesJSON:    `{"Encrypted":false}`,
		TagsJSON:          &tags,
		DiscoveredAt:      "2026-05-06T00:00:00Z",
		DiscoveredBy:      "scan-1",
		ManagedByProvider: false,
	}
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Resource
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.NativeID != orig.NativeID || got.Provider != orig.Provider {
		t.Errorf("scalar fields lost: %+v", got)
	}
	var origAttrs, gotAttrs map[string]any
	if err := json.Unmarshal([]byte(orig.AttributesJSON), &origAttrs); err != nil {
		t.Fatalf("orig attrs parse: %v", err)
	}
	if err := json.Unmarshal([]byte(got.AttributesJSON), &gotAttrs); err != nil {
		t.Fatalf("round-trip attrs parse: %v\n%s", err, got.AttributesJSON)
	}
	if origAttrs["Encrypted"] != gotAttrs["Encrypted"] {
		t.Errorf("attrs lost on round-trip: orig=%v got=%v", origAttrs, gotAttrs)
	}
	if got.TagsJSON == nil {
		t.Fatalf("tags lost on round-trip")
	}
	var origTags, gotTags map[string]string
	_ = json.Unmarshal([]byte(*orig.TagsJSON), &origTags)
	_ = json.Unmarshal([]byte(*got.TagsJSON), &gotTags)
	if origTags["env"] != gotTags["env"] || origTags["team"] != gotTags["team"] {
		t.Errorf("tags content drift: orig=%v got=%v", origTags, gotTags)
	}
}
