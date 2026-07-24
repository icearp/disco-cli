package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveSpotInstanceRequestRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	instARN := ec2ARN(region, acct.ID, "instance", "i-1")
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, instARN, region, "{}")
	sirARN := ec2ARN(region, acct.ID, "spot-instances-request", "sir-1")
	sirID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SpotInstanceRequest, sirARN, region, `{"InstanceId":"i-1"}`)

	if err := resolveSpotInstanceRequestRelationships(acct, st); err != nil {
		t.Fatalf("resolveSpotInstanceRequestRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sirID)
	assertRelationship(t, rels, sirID, instID, store.RelAttachedTo)
}

func TestResolveSpotInstanceRequestRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	sirARN := ec2ARN(region, acct.ID, "spot-instances-request", "sir-1")
	sirID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SpotInstanceRequest, sirARN, region, "{}")
	if err := resolveSpotInstanceRequestRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(sirID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveInstanceEventWindowRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	instID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance,
		ec2ARN(region, acct.ID, "instance", "i-1"), region, "{}")
	hostID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Host,
		ec2ARN(region, acct.ID, "dedicated-host", "h-1"), region, "{}")
	wARN := ec2ARN(region, acct.ID, "instance-event-window", "iew-1")
	attrs := `{"AssociationTarget":{"InstanceIds":["i-1"],"DedicatedHostIds":["h-1"]}}`
	wID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2InstanceEventWindow, wARN, region, attrs)

	if err := resolveInstanceEventWindowRelationships(acct, st); err != nil {
		t.Fatalf("resolveInstanceEventWindowRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(wID)
	assertRelationship(t, rels, wID, instID, store.RelAttachedTo)
	assertRelationship(t, rels, wID, hostID, store.RelAttachedTo)
}

func TestResolveInstanceEventWindowRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	wARN := ec2ARN(region, acct.ID, "instance-event-window", "iew-1")
	wID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2InstanceEventWindow, wARN, region, "{}")
	if err := resolveInstanceEventWindowRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(wID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}
