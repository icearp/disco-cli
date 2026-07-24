package policy

import (
	"context"
	"strings"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestLoadPacks_AWSWAF(t *testing.T) {
	mods, err := LoadPacks([]string{"aws-waf"})
	if err != nil {
		t.Fatalf("LoadPacks: %v", err)
	}
	if len(mods) != 5 {
		t.Errorf("aws-waf: got %d modules, want 5", len(mods))
	}
	for path := range mods {
		if !strings.HasPrefix(path, "aws-waf/") {
			t.Errorf("module key %q missing aws-waf/ prefix", path)
		}
	}
}

func TestLoadPacks_Unknown(t *testing.T) {
	_, err := LoadPacks([]string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "available: aws-waf") {
		t.Errorf("want unknown-pack error mentioning aws-waf, got %v", err)
	}
}

func TestLoadPacks_Empty(t *testing.T) {
	mods, err := LoadPacks(nil)
	if err != nil {
		t.Fatalf("LoadPacks nil: %v", err)
	}
	if len(mods) != 0 {
		t.Errorf("empty input: got %d modules, want 0", len(mods))
	}
}

// TestAWSWAF_Compiles asserts the bundled pack compiles cleanly through
// the full engine path — catches any rego.v1 violations / sprintf shape
// drift in the embedded modules at build time.
func TestAWSWAF_Compiles(t *testing.T) {
	mods, err := LoadPacks([]string{"aws-waf"})
	if err != nil {
		t.Fatalf("LoadPacks: %v", err)
	}
	if _, err := NewEngine(context.Background(), nil, mods); err != nil {
		t.Fatalf("aws-waf compile: %v", err)
	}
}

// TestAWSWAF_FiresEndToEnd evaluates a bundled pack against a matching
// resource and asserts the finding is produced with its refUrl populated.
// This is the only end-to-end proof of the input.* / Finding wire contract:
// a rename that desyncs the input map key from a rego `input.<key>` reference
// (or the rego `refUrl` output key from the Finding json tag) would make the
// rule silently stop firing — invisible to the compile-only tests above.
func TestAWSWAF_FiresEndToEnd(t *testing.T) {
	mods, err := LoadPacks([]string{"aws-waf"})
	if err != nil {
		t.Fatalf("LoadPacks: %v", err)
	}
	eng, err := NewEngine(context.Background(), nil, mods)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	name := "vol-unencrypted"
	findings, err := eng.Evaluate(context.Background(), []store.Resource{{
		ID:             "res-1",
		Provider:       "aws",
		Type:           "aws:ec2:volume",
		NativeID:       "vol-0abc123",
		Name:           &name,
		AttributesJSON: `{"Encrypted":false}`,
	}})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	var got *Finding
	for i := range findings {
		if findings[i].ID == "waf-sec-ebs-encryption-at-rest" {
			got = &findings[i]
		}
	}
	if got == nil {
		// A missing finding here means input.nativeId (used in the rule's
		// sprintf) went undefined and collapsed the rule body — i.e. the
		// input-key rename desynced from the rego reference.
		t.Fatalf("waf-sec-ebs-encryption-at-rest did not fire; got %+v", findings)
	}
	if got.RefURL == "" {
		t.Error("finding RefURL empty — rego `refUrl` output key does not match the Finding json tag")
	}
	if got.ResourceID != "res-1" {
		t.Errorf("ResourceID = %q, want res-1", got.ResourceID)
	}
}
