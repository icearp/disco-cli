package policy

import (
	"context"
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestEngine_Smoke compiles a trivial v1 Rego module that flags
// unencrypted EBS volumes and asserts a single Finding is emitted with
// the expected severity and resource ID.
func TestEngine_Smoke(t *testing.T) {
	src := `package disco
import rego.v1

deny contains f if {
	input.type == "aws:ec2:volume"
	input.attributes.Encrypted == false
	f := {
		"id": "ebs-unencrypted",
		"severity": "high",
		"message": "EBS volume is unencrypted",
	}
}
`
	eng, err := NewEngine(context.Background(), nil, map[string]string{"ebs.rego": src})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	resources := []store.Resource{
		{
			ID: "abc", Provider: "aws", Type: "aws:ec2:volume",
			AttributesJSON: `{"Encrypted": false}`,
		},
		{
			ID: "def", Provider: "aws", Type: "aws:ec2:volume",
			AttributesJSON: `{"Encrypted": true}`,
		},
	}
	findings, err := eng.Evaluate(context.Background(), resources)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings len: want 1, got %d (%+v)", len(findings), findings)
	}
	f := findings[0]
	if f.ID != "ebs-unencrypted" || f.Severity != "high" || f.ResourceID != "abc" || f.Provider != "aws" {
		t.Errorf("unexpected finding: %+v", f)
	}
}

// TestResourceToInput_FreshnessFields guards F15: custody timestamps,
// scan-run IDs, parsed tags, and the managed flag must surface so policies
// can write freshness-bound and tag-scoped rules.
func TestResourceToInput_FreshnessFields(t *testing.T) {
	tags := `{"env":"prod","team":"core"}`
	name, region, zone, status := "web", "us-east-2", "us-east-2a", "running"
	acctName := "prod-account"
	created := "2026-01-01T00:00:00Z"
	r := &store.Resource{
		ID: "abc", Provider: "aws", AccountID: "111", AccountName: &acctName,
		Type: "aws:iam:access-key", NativeID: "AKIA...",
		Name: &name, Region: &region, Zone: &zone, Status: &status,
		AttributesJSON: `{"Active":true}`, TagsJSON: &tags,
		CreatedAt:         &created,
		DiscoveredAt:      "2026-05-06T11:00:00Z",
		DiscoveredBy:      "scan-1",
		ManagedByProvider: false,
	}
	in, err := resourceToInput(r)
	if err != nil {
		t.Fatalf("resourceToInput: %v", err)
	}
	for _, k := range []string{
		"discovered_at", "discovered_by",
		"created_at", "account_name", "zone", "managed_by_provider", "tags",
	} {
		if _, ok := in[k]; !ok {
			t.Errorf("missing key: %s", k)
		}
	}
	tagsOut, ok := in["tags"].(map[string]any)
	if !ok || tagsOut["env"] != "prod" {
		t.Errorf("tags not parsed: %v", in["tags"])
	}
	if in["managed_by_provider"] != false {
		t.Errorf("managed_by_provider: %v", in["managed_by_provider"])
	}
}

// TestResourceToInput_NilTagsEmptyMap: rows without tags must yield an
// empty map (not nil) so Rego rules iterate without nil-guarding.
func TestResourceToInput_NilTagsEmptyMap(t *testing.T) {
	r := &store.Resource{
		ID: "abc", Provider: "aws", AccountID: "111", Type: "aws:s3:bucket",
		NativeID: "b1", AttributesJSON: "{}",
	}
	in, err := resourceToInput(r)
	if err != nil {
		t.Fatalf("resourceToInput: %v", err)
	}
	tags, ok := in["tags"].(map[string]any)
	if !ok {
		t.Fatalf("tags not map[string]any: %T", in["tags"])
	}
	if len(tags) != 0 {
		t.Errorf("want empty map, got %v", tags)
	}
}

// TestEngine_EmptyPolicies confirms an engine built with no modules
// evaluates cleanly and emits zero findings.
func TestEngine_EmptyPolicies(t *testing.T) {
	eng, err := NewEngine(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	findings, err := eng.Evaluate(context.Background(), []store.Resource{
		{ID: "abc", Provider: "aws", Type: "aws:ec2:volume", AttributesJSON: "{}"},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("want 0 findings, got %d", len(findings))
	}
}
