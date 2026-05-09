package aws

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveVerifiedAccessGroupRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	instARN := ec2ARN(region, acct.ID, "verified-access-instance", "vai-aaa")
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VerifiedAccessInstance, instARN, region, "{}")

	groupARN := ec2ARN(region, acct.ID, "verified-access-group", "vag-bbb")
	attrsJSON := `{"VerifiedAccessInstanceId": "vai-aaa"}`
	groupID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VerifiedAccessGroup, groupARN, region, attrsJSON)

	if err := resolveVerifiedAccessGroupRelationships(acct, st); err != nil {
		t.Fatalf("resolveVerifiedAccessGroupRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(groupID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, groupID, instID, store.RelAttachedTo)
}

func TestResolveVerifiedAccessGroupRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	id := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VerifiedAccessGroup,
		ec2ARN("us-east-1", acct.ID, "verified-access-group", "bare"), "us-east-1", "{}")

	if err := resolveVerifiedAccessGroupRelationships(acct, st); err != nil {
		t.Fatalf("resolveVerifiedAccessGroupRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveVerifiedAccessEndpointRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	groupARN := ec2ARN(region, acct.ID, "verified-access-group", "vag-bbb")
	groupID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VerifiedAccessGroup, groupARN, region, "{}")

	epARN := ec2ARN(region, acct.ID, "verified-access-endpoint", "vae-ccc")
	attrsJSON := `{"VerifiedAccessGroupId": "vag-bbb"}`
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VerifiedAccessEndpoint, epARN, region, attrsJSON)

	if err := resolveVerifiedAccessEndpointRelationships(acct, st); err != nil {
		t.Fatalf("resolveVerifiedAccessEndpointRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(epID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, epID, groupID, store.RelAttachedTo)
}
