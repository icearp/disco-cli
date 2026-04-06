package aws

import (
	"testing"

	"codeburg.org/icearp/disco/internal/store"
)

// TestResolveRDSRelationships verifies that an RDS DB instance's VPC ID is
// correctly extracted from the nested DBSubnetGroup.VpcId JSON path.
// The nested struct is a common source of typos — a flat VpcId would silently
// produce zero relationships.
func TestResolveRDSRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	dbARN := "arn:aws:rds:us-east-1:123456789012:db:my-db"
	attrsJSON := `{"DBSubnetGroup": {"VpcId": "vpc-rds-111"}}`
	dbID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBInstance, dbARN, region, attrsJSON)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(region, acct.ID, "vpc", "vpc-rds-111"), region, "{}")

	if err := resolveRDSRelationships(acct, st); err != nil {
		t.Fatalf("resolveRDSRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(dbID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != vpcID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected db -[attached-to]-> vpc, got %+v", rels[0])
	}
}

// TestResolveRDSRelationships_NoSubnetGroup verifies graceful handling when
// DBSubnetGroup is absent from the attributes.
func TestResolveRDSRelationships_NoSubnetGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	dbARN := "arn:aws:rds:us-east-1:123456789012:db:bare-db"
	dbID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBInstance, dbARN, "", "{}")

	if err := resolveRDSRelationships(acct, st); err != nil {
		t.Fatalf("resolveRDSRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(dbID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
