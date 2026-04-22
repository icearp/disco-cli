package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveELBClassicRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	lbName := "my-classic-lb"
	nativeID := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/%s", region, acct.ID, lbName)
	lbID := upsertTestResource(t, st, "aws", acct.ID, TypeELBClassicLoadBalancer, nativeID, region,
		`{"VPCId": "vpc-classic-111"}`)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(region, acct.ID, "vpc", "vpc-classic-111"), region, "{}")

	if err := resolveELBClassicRelationships(acct, st); err != nil {
		t.Fatalf("resolveELBClassicRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(lbID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != vpcID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected classic-lb -[attached-to]-> vpc, got %+v", rels[0])
	}
}

// TestResolveELBClassicRelationships_NoVPC covers EC2-Classic LBs (no VPC)
// and the empty-attrs guard.
func TestResolveELBClassicRelationships_NoVPC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	lbName := "ec2-classic-lb"
	nativeID := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/%s", region, acct.ID, lbName)
	lbID := upsertTestResource(t, st, "aws", acct.ID, TypeELBClassicLoadBalancer, nativeID, region, "{}")

	if err := resolveELBClassicRelationships(acct, st); err != nil {
		t.Fatalf("resolveELBClassicRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(lbID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
