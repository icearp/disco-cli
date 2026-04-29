package rules

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestBuiltins_Parse asserts every embedded YAML loads cleanly — guards against
// typos in the shipped rule set.
func TestBuiltins_Parse(t *testing.T) {
	rs, err := Builtins()
	if err != nil {
		t.Fatalf("Builtins: %v", err)
	}
	if len(rs) == 0 {
		t.Fatal("no builtin rules embedded")
	}
	seen := map[string]bool{}
	for _, r := range rs {
		if seen[r.ID] {
			t.Errorf("duplicate builtin id %q", r.ID)
		}
		seen[r.ID] = true
		if r.ID == "" {
			t.Errorf("builtin rule missing id (source=%s)", r.Source)
		}
		if _, err := ParseSeverity(string(r.Severity)); err != nil {
			t.Errorf("rule %s: %v", r.ID, err)
		}
	}
}

// pickBuiltin returns the rule with the given ID from the embedded set or
// fails the test. Keeps the exposure tests below tied to the actual shipped
// YAML rather than redeclaring the rule body in Go.
func pickBuiltin(t *testing.T, id string) Rule {
	t.Helper()
	rs, err := Builtins()
	if err != nil {
		t.Fatalf("Builtins: %v", err)
	}
	for _, r := range rs {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("builtin %q not found", id)
	return Rule{}
}

// TestBuiltin_AWSExposure exercises the shipped YAML rule against a synthetic
// fixture: one ENI with a public IP and an open SG (should fire) + one ENI
// with a public IP and a locked SG (should not).
func TestBuiltin_AWSExposure(t *testing.T) {
	st, scanID := newTestStore(t)
	openSG := &store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:security-group",
		NativeID:       "sg-open",
		AttributesJSON: `{"IpPermissions":[{"FromPort":22,"ToPort":22,"IpRanges":[{"CidrIp":"0.0.0.0/0"}]}]}`,
		DiscoveredBy:   scanID,
	}
	lockedSG := &store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:security-group",
		NativeID:       "sg-locked",
		AttributesJSON: `{"IpPermissions":[{"FromPort":22,"ToPort":22,"IpRanges":[{"CidrIp":"10.0.0.0/8"}]}]}`,
		DiscoveredBy:   scanID,
	}
	exposed := &store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:network-interface",
		NativeID:       "eni-exposed",
		AttributesJSON: `{"Association":{"PublicIp":"203.0.113.7"}}`,
		DiscoveredBy:   scanID,
	}
	safe := &store.Resource{
		Provider: "aws", AccountID: "111", Type: "aws:ec2:network-interface",
		NativeID:       "eni-safe",
		AttributesJSON: `{"Association":{"PublicIp":"198.51.100.4"}}`,
		DiscoveredBy:   scanID,
	}
	if _, err := st.UpsertResources([]*store.Resource{openSG, lockedSG, exposed, safe}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.UpsertRelationship(exposed.ID, openSG.ID, store.RelUses, "directed", nil); err != nil {
		t.Fatalf("rel exposed: %v", err)
	}
	if err := st.UpsertRelationship(safe.ID, lockedSG.ID, store.RelUses, "directed", nil); err != nil {
		t.Fatalf("rel safe: %v", err)
	}

	rule := pickBuiltin(t, "aws-eni-internet-exposed")
	findings, err := Evaluate(st, []Rule{rule}, "")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings: got %d, want 1: %+v", len(findings), findings)
	}
	if findings[0].ResourceID != exposed.ID {
		t.Errorf("wrong ENI flagged: got %s want %s", findings[0].ResourceID, exposed.ID)
	}
	if findings[0].Tags["nist-800-53"] == nil {
		t.Errorf("expected tags propagated onto finding, got %+v", findings[0].Tags)
	}
}

// TestBuiltin_AzureExposure mirrors the AWS exposure test for Azure NIC + NSG.
func TestBuiltin_AzureExposure(t *testing.T) {
	st, scanID := newTestStore(t)
	openNSG := &store.Resource{
		Provider: "azure", AccountID: "sub-1", Type: "azure:microsoft.network:network-security-group",
		NativeID:       "/subscriptions/sub-1/resourceGroups/RG/providers/Microsoft.Network/networkSecurityGroups/open",
		AttributesJSON: `{"properties":{"securityRules":[{"properties":{"sourceAddressPrefix":"*","access":"Allow","direction":"Inbound"}}]}}`,
		DiscoveredBy:   scanID,
	}
	exposed := &store.Resource{
		Provider: "azure", AccountID: "sub-1", Type: "azure:microsoft.network:network-interface",
		NativeID:       "/subscriptions/sub-1/resourceGroups/RG/providers/Microsoft.Network/networkInterfaces/exposed",
		AttributesJSON: `{"properties":{"ipConfigurations":[{"properties":{"publicIPAddress":{"id":"/subscriptions/sub-1/.../publicIPAddresses/pip"}}}]}}`,
		DiscoveredBy:   scanID,
	}
	if _, err := st.UpsertResources([]*store.Resource{openNSG, exposed}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.UpsertRelationship(exposed.ID, openNSG.ID, store.RelUses, "directed", nil); err != nil {
		t.Fatalf("rel: %v", err)
	}

	rule := pickBuiltin(t, "azure-nic-internet-exposed")
	findings, err := Evaluate(st, []Rule{rule}, "")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(findings) != 1 || findings[0].ResourceID != exposed.ID {
		t.Fatalf("findings: %+v", findings)
	}
}

// TestBuiltin_GCPExposure mirrors the exposure test for GCP instance + firewall.
func TestBuiltin_GCPExposure(t *testing.T) {
	st, scanID := newTestStore(t)
	fw := &store.Resource{
		Provider: "gcp", AccountID: "proj-1", Type: "gcp:compute:firewall",
		NativeID:       "projects/proj-1/global/firewalls/allow-all",
		AttributesJSON: `{"sourceRanges":["0.0.0.0/0"],"direction":"INGRESS"}`,
		DiscoveredBy:   scanID,
	}
	inst := &store.Resource{
		Provider: "gcp", AccountID: "proj-1", Type: "gcp:compute:instance",
		NativeID:       "projects/proj-1/zones/us-central1-a/instances/exposed",
		AttributesJSON: `{"networkInterfaces":[{"accessConfigs":[{"natIP":"203.0.113.5"}]}]}`,
		DiscoveredBy:   scanID,
	}
	if _, err := st.UpsertResources([]*store.Resource{fw, inst}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Firewall resolver direction (R4.5): firewall → instance via `uses`.
	if err := st.UpsertRelationship(fw.ID, inst.ID, store.RelUses, "directed", nil); err != nil {
		t.Fatalf("rel: %v", err)
	}

	rule := pickBuiltin(t, "gcp-instance-internet-exposed")
	findings, err := Evaluate(st, []Rule{rule}, "")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(findings) != 1 || findings[0].ResourceID != inst.ID {
		t.Fatalf("findings: %+v", findings)
	}
}
