package aws

import (
	"testing"

	"codeburg.org/icearp/disco/internal/store"
)

// TestResolveELBRelationships verifies that an ELB load balancer's VPC ID is
// correctly extracted from the nested lb.VpcId JSON path.
// Note: the ELB resolver stores the load balancer struct under the key "lb"
// (lowercase), not "Lb" — this test locks in that casing.
func TestResolveELBRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	lbARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/my-lb/abc"
	attrsJSON := `{"lb": {"VpcId": "vpc-lb-777"}}`
	lbID := upsertTestResource(t, st, "aws", acct.ID, TypeELBLoadBalancer, lbARN, region, attrsJSON)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(region, acct.ID, "vpc", "vpc-lb-777"), region, "{}")

	if err := resolveELBRelationships(acct, st); err != nil {
		t.Fatalf("resolveELBRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(lbID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != vpcID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected lb -[attached-to]-> vpc, got %+v", rels[0])
	}
}

// TestResolveELBRelationships_NoVPC verifies that a load balancer with no VPC
// produces no relationships and no error.
func TestResolveELBRelationships_NoVPC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	lbARN := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/bare-lb/xyz"
	lbID := upsertTestResource(t, st, "aws", acct.ID, TypeELBLoadBalancer, lbARN, "", "{}")

	if err := resolveELBRelationships(acct, st); err != nil {
		t.Fatalf("resolveELBRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(lbID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
