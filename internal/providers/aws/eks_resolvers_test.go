package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

// TestResolveEKSRelationships verifies an EKS cluster's VPC ID is extracted
// from the nested ResourcesVpcConfig.VpcId JSON path.
func TestResolveEKSRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	clusterARN := "arn:aws:eks:us-east-1:123456789012:cluster/my-cluster"
	attrsJSON := `{"ResourcesVpcConfig": {"VpcId": "vpc-eks-999"}}`
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeEKSCluster, clusterARN, region, attrsJSON)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(region, acct.ID, "vpc", "vpc-eks-999"), region, "{}")

	if err := resolveEKSRelationships(acct, st); err != nil {
		t.Fatalf("resolveEKSRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != vpcID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected cluster -[attached-to]-> vpc, got %+v", rels[0])
	}
}

// TestResolveEKSRelationships_NoVPC verifies a cluster with no VPC config
// produces no relationships and no error.
func TestResolveEKSRelationships_NoVPC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	clusterARN := "arn:aws:eks:us-east-1:123456789012:cluster/bare-cluster"
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeEKSCluster, clusterARN, "", "{}")

	if err := resolveEKSRelationships(acct, st); err != nil {
		t.Fatalf("resolveEKSRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
