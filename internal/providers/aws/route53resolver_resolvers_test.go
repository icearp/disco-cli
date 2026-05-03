package aws

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveR53RResolverConfigVPC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cfgARN := r53rARN(testRegion, acct.ID, "resolver-config", "vpc-001")
	cfgID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53ResolverResolverConfig, cfgARN, testRegion, `{"ResourceId":"vpc-001"}`)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-001"), testRegion, "{}")

	if err := resolveR53RResolverConfigVPC(acct, st); err != nil {
		t.Fatalf("resolveR53RResolverConfigVPC: %v", err)
	}
	rels, err := st.RelationshipsFrom(cfgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, cfgID, vpcID, store.RelAttachedTo)
}

func TestResolveR53RResolverConfigVPC_UnscannedVPCSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cfgARN := r53rARN(testRegion, acct.ID, "resolver-config", "vpc-missing")
	cfgID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53ResolverResolverConfig, cfgARN, testRegion, `{"ResourceId":"vpc-missing"}`)
	if err := resolveR53RResolverConfigVPC(acct, st); err != nil {
		t.Fatalf("resolveR53RResolverConfigVPC: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cfgID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveR53RResolverRuleAssoc(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	assocARN := r53rARN(testRegion, acct.ID, "resolver-rule-association", "rslvr-assoc-001")
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53ResolverResolverRuleAssociation, assocARN, testRegion,
		`{"VPCId":"vpc-001","ResolverRuleId":"rslvr-rr-001"}`)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-001"), testRegion, "{}")
	ruleID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53ResolverResolverRule, r53rARN(testRegion, acct.ID, "resolver-rule", "rslvr-rr-001"), testRegion, "{}")

	if err := resolveR53RResolverRuleAssoc(acct, st); err != nil {
		t.Fatalf("resolveR53RResolverRuleAssoc: %v", err)
	}
	rels, err := st.RelationshipsFrom(assocID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, assocID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, ruleID, store.RelAttachedTo)
}

func TestResolveR53RResolverRuleAssoc_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53ResolverResolverRuleAssociation,
		r53rARN(testRegion, acct.ID, "resolver-rule-association", "rslvr-assoc-bare"), testRegion, "{}")
	if err := resolveR53RResolverRuleAssoc(acct, st); err != nil {
		t.Fatalf("resolveR53RResolverRuleAssoc: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveR53RFirewallRuleGroupAssoc(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	assocARN := r53rARN(testRegion, acct.ID, "firewall-rule-group-association", "rslvr-frgassoc-001")
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeRoute53ResolverFirewallRuleGroupAssociation, assocARN, testRegion, `{"VpcId":"vpc-001"}`)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-001"), testRegion, "{}")

	if err := resolveR53RFirewallRuleGroupAssoc(acct, st); err != nil {
		t.Fatalf("resolveR53RFirewallRuleGroupAssoc: %v", err)
	}
	rels, err := st.RelationshipsFrom(assocID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, assocID, vpcID, store.RelAttachedTo)
}
