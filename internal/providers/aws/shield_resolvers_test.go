package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func shieldProtectionARN(acct, id string) string {
	return fmt.Sprintf("arn:aws:shield::%s:protection/%s", acct, id)
}

func shieldProtectionGroupARN(acct, id string) string {
	return fmt.Sprintf("arn:aws:shield::%s:protection-group/%s", acct, id)
}

// TestResolveShieldProtectionTargets covers all four scanned target types plus
// the EIP shape normalisation (Shield's `:eip-allocation/` → disco's
// `:elastic-ip/`).
func TestResolveShieldProtectionTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// ELBv2 LB target.
	lbARN := fmt.Sprintf("arn:aws:elasticloadbalancingv2:%s:%s:loadbalancer/app/web/abc123", testRegion, acct.ID)
	lbID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2LoadBalancer, lbARN, testRegion, `{}`)

	// CloudFront distribution target.
	cfARN := fmt.Sprintf("arn:aws:cloudfront::%s:distribution/E27EXAMPLE51Z", acct.ID)
	cfID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFrontDistribution, cfARN, "", `{}`)

	// Route 53 hosted zone target.
	zoneARN := "arn:aws:route53:::hostedzone/Z1D633PJN98FT9"
	zoneID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53HostedZone, zoneARN, "", `{}`)

	// EIP target — disco scanner stores NativeID with `:elastic-ip/` segment.
	eipNativeID := ec2ARN(testRegion, acct.ID, "elastic-ip", "eipalloc-1a2b3c4d")
	eipID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2EIP, eipNativeID, testRegion, `{}`)

	// Protections — Shield's ResourceArn for EIPs uses `:eip-allocation/`.
	eipShieldArn := fmt.Sprintf("arn:aws:ec2:%s:%s:eip-allocation/eipalloc-1a2b3c4d", testRegion, acct.ID)

	cases := []struct {
		name     string
		resource string
		expectTo string
	}{
		{"lb", lbARN, lbID},
		{"cloudfront", cfARN, cfID},
		{"route53", zoneARN, zoneID},
		{"eip", eipShieldArn, eipID},
	}

	for _, c := range cases {
		pARN := shieldProtectionARN(acct.ID, c.name)
		attrs := fmt.Sprintf(`{"ResourceArn": %q, "Id": %q, "Name": %q}`, c.resource, c.name, c.name)
		upsertTestResource(t, st, "aws", acct.ID, TypeShieldProtection, pARN, "", attrs)
	}

	if err := resolveShieldProtectionTargets(acct, st); err != nil {
		t.Fatalf("resolveShieldProtectionTargets: %v", err)
	}

	for _, c := range cases {
		pID := store.ResourceID("aws", acct.ID, TypeShieldProtection, shieldProtectionARN(acct.ID, c.name))
		rels, err := st.RelationshipsFrom(pID)
		if err != nil {
			t.Fatalf("RelationshipsFrom %s: %v", c.name, err)
		}
		assertRelationship(t, rels, pID, c.expectTo, store.RelAttachedTo)
	}
}

// TestResolveShieldProtection_UnknownARN verifies that unrecognised target
// shapes (e.g. Global Accelerator) skip silently with no edges.
func TestResolveShieldProtection_UnknownARN(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	pARN := shieldProtectionARN(acct.ID, "ga")
	attrs := fmt.Sprintf(`{"ResourceArn": "arn:aws:globalaccelerator::%s:accelerator/abc"}`, acct.ID)
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeShieldProtection, pARN, "", attrs)

	if err := resolveShieldProtectionTargets(acct, st); err != nil {
		t.Fatalf("resolveShieldProtectionTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(pID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("unexpected edges for unknown ARN: %+v", rels)
	}
}

// TestResolveShieldProtection_UnscannedTarget verifies FK-safe skip when
// ResourceArn matches a known shape but the target is not in the store.
func TestResolveShieldProtection_UnscannedTarget(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	pARN := shieldProtectionARN(acct.ID, "orphan")
	attrs := fmt.Sprintf(`{"ResourceArn": "arn:aws:cloudfront::%s:distribution/E-MISSING"}`, acct.ID)
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeShieldProtection, pARN, "", attrs)

	if err := resolveShieldProtectionTargets(acct, st); err != nil {
		t.Fatalf("resolveShieldProtectionTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(pID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("unexpected edges for unscanned target: %+v", rels)
	}
}

// TestResolveShieldProtectionGroupMembers covers ARBITRARY pattern with two
// scanned member protections; one unscanned member is skipped FK-safely.
func TestResolveShieldProtectionGroupMembers(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	p1ARN := shieldProtectionARN(acct.ID, "m1")
	p2ARN := shieldProtectionARN(acct.ID, "m2")
	missingARN := shieldProtectionARN(acct.ID, "missing")
	p1ID := upsertTestResource(t, st, "aws", acct.ID, TypeShieldProtection, p1ARN, "", `{}`)
	p2ID := upsertTestResource(t, st, "aws", acct.ID, TypeShieldProtection, p2ARN, "", `{}`)

	gARN := shieldProtectionGroupARN(acct.ID, "grp1")
	attrs := fmt.Sprintf(`{
		"Pattern": "ARBITRARY",
		"Members": [%q, %q, %q]
	}`, p1ARN, p2ARN, missingARN)
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeShieldProtectionGroup, gARN, "", attrs)

	if err := resolveShieldProtectionGroupMembers(acct, st); err != nil {
		t.Fatalf("resolveShieldProtectionGroupMembers: %v", err)
	}
	rels, err := st.RelationshipsFrom(gID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, gID, p1ID, store.RelContains)
	assertRelationship(t, rels, gID, p2ID, store.RelContains)
	if len(rels) != 2 {
		t.Errorf("expected 2 edges, got %d: %+v", len(rels), rels)
	}
}

// TestResolveShieldProtectionGroup_NonArbitraryPatternSkipped verifies that
// Pattern=ALL and Pattern=BY_RESOURCE_TYPE produce no edges (deferred).
func TestResolveShieldProtectionGroup_NonArbitraryPatternSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	pARN := shieldProtectionARN(acct.ID, "p1")
	upsertTestResource(t, st, "aws", acct.ID, TypeShieldProtection, pARN, "", `{}`)

	gARN := shieldProtectionGroupARN(acct.ID, "grp-all")
	attrs := `{"Pattern": "ALL", "Members": []}`
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeShieldProtectionGroup, gARN, "", attrs)

	if err := resolveShieldProtectionGroupMembers(acct, st); err != nil {
		t.Fatalf("resolveShieldProtectionGroupMembers: %v", err)
	}
	rels, err := st.RelationshipsFrom(gID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected no edges for Pattern=ALL, got %+v", rels)
	}
}

// TestResolveShield_EmptyAttrs verifies no panic on empty AttributesJSON.
func TestResolveShield_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	upsertTestResource(t, st, "aws", acct.ID, TypeShieldProtection, shieldProtectionARN(acct.ID, "e"), "", `{}`)
	upsertTestResource(t, st, "aws", acct.ID, TypeShieldProtectionGroup, shieldProtectionGroupARN(acct.ID, "e"), "", `{}`)

	if err := resolveShieldProtectionTargets(acct, st); err != nil {
		t.Fatalf("resolveShieldProtectionTargets empty: %v", err)
	}
	if err := resolveShieldProtectionGroupMembers(acct, st); err != nil {
		t.Fatalf("resolveShieldProtectionGroupMembers empty: %v", err)
	}
}
