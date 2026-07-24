package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveIpamPolicyRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	ipamARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":ipam/ipam-aaa"
	ipamID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IPAM, ipamARN, region, "{}")
	polARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":ipam-policy/ipam-pol-1"
	polID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IpamPolicy, polARN, region, `{"IpamId":"ipam-aaa"}`)

	if err := resolveIpamPolicyRelationships(acct, st); err != nil {
		t.Fatalf("resolveIpamPolicyRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(polID)
	assertRelationship(t, rels, polID, ipamID, store.RelAttachedTo)
}

func TestResolveIpamPolicyRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	polARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":ipam-policy/ipam-pol-1"
	polID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IpamPolicy, polARN, region, "{}")
	if err := resolveIpamPolicyRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(polID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveIpamVerificationTokenRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	ipamARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":ipam/ipam-aaa"
	ipamID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IPAM, ipamARN, region, "{}")
	tokARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":ipam-external-resource-verification-token/ipam-ervt-1"
	tokID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IpamExternalResourceVerificationToken, tokARN, region, `{"IpamId":"ipam-aaa"}`)

	if err := resolveIpamVerificationTokenRelationships(acct, st); err != nil {
		t.Fatalf("resolveIpamVerificationTokenRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tokID)
	assertRelationship(t, rels, tokID, ipamID, store.RelAttachedTo)
}

func TestResolveIpamVerificationTokenRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	tokARN := "arn:aws:ec2:" + region + ":" + testAccountID + ":ipam-external-resource-verification-token/ipam-ervt-1"
	tokID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IpamExternalResourceVerificationToken, tokARN, region, "{}")
	if err := resolveIpamVerificationTokenRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(tokID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}
