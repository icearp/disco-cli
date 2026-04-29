package policy

import (
	"context"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
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
	eng, err := NewEngineFromModules(context.Background(), map[string]string{"ebs.rego": src})
	if err != nil {
		t.Fatalf("NewEngineFromModules: %v", err)
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

// TestEngine_EmptyPolicies confirms an engine built with no modules
// evaluates cleanly and emits zero findings.
func TestEngine_EmptyPolicies(t *testing.T) {
	eng, err := NewEngine(context.Background(), nil)
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
