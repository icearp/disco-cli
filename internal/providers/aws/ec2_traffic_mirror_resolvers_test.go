package aws

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveTrafficMirrorSessionRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	filterARN := ec2ARN(region, acct.ID, "traffic-mirror-filter", "tmf-aaa")
	filterID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TrafficMirrorFilter, filterARN, region, "{}")

	targetARN := ec2ARN(region, acct.ID, "traffic-mirror-target", "tmt-bbb")
	targetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TrafficMirrorTarget, targetARN, region, "{}")

	sessionARN := ec2ARN(region, acct.ID, "traffic-mirror-session", "tms-ccc")
	attrsJSON := `{"TrafficMirrorFilterId": "tmf-aaa", "TrafficMirrorTargetId": "tmt-bbb"}`
	sessionID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TrafficMirrorSession, sessionARN, region, attrsJSON)

	if err := resolveTrafficMirrorSessionRelationships(acct, st); err != nil {
		t.Fatalf("resolveTrafficMirrorSessionRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(sessionID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Errorf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, sessionID, filterID, store.RelUses)
	assertRelationship(t, rels, sessionID, targetID, store.RelAttachedTo)
}

func TestResolveTrafficMirrorSessionRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	id := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TrafficMirrorSession,
		ec2ARN("us-east-1", acct.ID, "traffic-mirror-session", "bare"), "us-east-1", "{}")

	if err := resolveTrafficMirrorSessionRelationships(acct, st); err != nil {
		t.Fatalf("resolveTrafficMirrorSessionRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
