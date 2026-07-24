package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveIPAMScopeRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	ipamARN := "arn:aws:ec2:us-east-1:123456789012:ipam/ipam-aaa"
	ipamID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IPAM, ipamARN, region, "{}")

	scopeARN := "arn:aws:ec2:us-east-1:123456789012:ipam-scope/ipam-scope-bbb"
	attrsJSON := `{"IpamArn": "arn:aws:ec2:us-east-1:123456789012:ipam/ipam-aaa"}`
	scopeID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IPAMScope, scopeARN, region, attrsJSON)

	if err := resolveIPAMScopeRelationships(acct, st); err != nil {
		t.Fatalf("resolveIPAMScopeRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(scopeID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, scopeID, ipamID, store.RelAttachedTo)
}

func TestResolveIPAMScopeRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	id := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IPAMScope,
		"arn:aws:ec2:us-east-1:123456789012:ipam-scope/bare", "us-east-1", "{}")

	if err := resolveIPAMScopeRelationships(acct, st); err != nil {
		t.Fatalf("resolveIPAMScopeRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveIPAMPoolRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	scopeARN := "arn:aws:ec2:us-east-1:123456789012:ipam-scope/ipam-scope-bbb"
	scopeID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IPAMScope, scopeARN, region, "{}")

	poolARN := "arn:aws:ec2:us-east-1:123456789012:ipam-pool/ipam-pool-ccc"
	attrsJSON := `{"IpamScopeArn": "arn:aws:ec2:us-east-1:123456789012:ipam-scope/ipam-scope-bbb"}`
	poolID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IPAMPool, poolARN, region, attrsJSON)

	if err := resolveIPAMPoolRelationships(acct, st); err != nil {
		t.Fatalf("resolveIPAMPoolRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(poolID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, poolID, scopeID, store.RelAttachedTo)
}

func TestResolveIPAMResourceDiscoveryAssociationRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	ipamARN := "arn:aws:ec2:us-east-1:123456789012:ipam/ipam-aaa"
	ipamID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IPAM, ipamARN, region, "{}")

	rdARN := "arn:aws:ec2:us-east-1:123456789012:ipam-resource-discovery/ipam-res-disc-ddd"
	rdID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IPAMResourceDiscovery, rdARN, region, "{}")

	assocARN := "arn:aws:ec2:us-east-1:123456789012:ipam-resource-discovery-association/ipam-res-disc-assoc-eee"
	attrsJSON := `{
		"IpamArn": "arn:aws:ec2:us-east-1:123456789012:ipam/ipam-aaa",
		"IpamResourceDiscoveryArn": "arn:aws:ec2:us-east-1:123456789012:ipam-resource-discovery/ipam-res-disc-ddd"
	}`
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IPAMResourceDiscoveryAssociation, assocARN, region, attrsJSON)

	if err := resolveIPAMResourceDiscoveryAssociationRelationships(acct, st); err != nil {
		t.Fatalf("resolveIPAMResourceDiscoveryAssociationRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(assocID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Errorf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, assocID, ipamID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, rdID, store.RelUses)
}
