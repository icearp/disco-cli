package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

// TestResolveInstanceRelationships_AMI verifies the instance→AMI `uses` edge
// when the AMI has been scanned (self-owned AMI case).
func TestResolveInstanceRelationships_AMI(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	amiARN := ec2ARN(region, acct.ID, "image", "ami-123")
	amiID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Image, amiARN, region, `{"ImageId":"ami-123"}`)

	instARN := ec2ARN(region, acct.ID, "instance", "i-ami")
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, instARN, region,
		`{"InstanceId":"i-ami","ImageId":"ami-123"}`)

	if err := resolveInstanceRelationships(acct, st); err != nil {
		t.Fatalf("resolveInstanceRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(instID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, instID, amiID, store.RelUses)
}

// TestResolveInstanceRelationships_AMIUnscanned verifies no edge and no FK
// error when the instance references an AMI that isn't in the store
// (public/Marketplace/shared AMI).
func TestResolveInstanceRelationships_AMIUnscanned(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	instARN := ec2ARN(region, acct.ID, "instance", "i-pub")
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, instARN, region,
		`{"InstanceId":"i-pub","ImageId":"ami-public"}`)

	if err := resolveInstanceRelationships(acct, st); err != nil {
		t.Fatalf("resolveInstanceRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(instID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	for _, rel := range rels {
		if rel.Kind == store.RelUses {
			t.Errorf("unexpected uses edge to %s for unscanned AMI", rel.ToID)
		}
	}
}

// TestResolveInstanceRelationships_AMIEmpty verifies no panic when ImageId is
// absent from instance attrs.
func TestResolveInstanceRelationships_AMIEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	instARN := ec2ARN(region, acct.ID, "instance", "i-noami")
	upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, instARN, region,
		`{"InstanceId":"i-noami"}`)

	if err := resolveInstanceRelationships(acct, st); err != nil {
		t.Fatalf("resolveInstanceRelationships: %v", err)
	}
}
