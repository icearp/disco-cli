package aws

import (
	"testing"

	"codeburg.org/icearp/disco/internal/store"
)

// TestResolveElastiCacheRelationships verifies that a Redis cache cluster with a
// ReplicationGroupId is linked to its replication group with an attached-to edge.
func TestResolveElastiCacheRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	rgARN := "arn:aws:elasticache:us-east-1:123456789012:replicationgroup:my-rg"
	clusterARN := "arn:aws:elasticache:us-east-1:123456789012:cluster:my-rg-0001-001"

	rgAttrs := `{"ReplicationGroupId":"my-rg"}`
	clusterAttrs := `{"ReplicationGroupId":"my-rg"}`

	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheCluster, clusterARN, testRegion, clusterAttrs)
	rgID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheReplicationGroup, rgARN, testRegion, rgAttrs)

	if err := resolveElastiCacheRelationships(acct, st); err != nil {
		t.Fatalf("resolveElastiCacheRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != rgID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected cluster -[attached-to]-> replication-group; got %+v", rels[0])
	}
}

// TestResolveElastiCacheRelationships_Memcached verifies that a Memcached cluster
// (no ReplicationGroupId) produces no relationships.
func TestResolveElastiCacheRelationships_Memcached(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clusterARN := "arn:aws:elasticache:us-east-1:123456789012:cluster:my-memcached"
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheCluster, clusterARN, testRegion, "{}")

	if err := resolveElastiCacheRelationships(acct, st); err != nil {
		t.Fatalf("resolveElastiCacheRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
