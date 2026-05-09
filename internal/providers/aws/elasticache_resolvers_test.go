package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

// --- CacheCluster → ReplicationGroup ---

// TestResolveElastiCacheRelationships verifies that a Redis cache cluster with a
// ReplicationGroupId is linked to its replication group with an attached-to edge.
func TestResolveElastiCacheRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	rgARN := "arn:aws:elasticache:us-east-1:123456789012:replicationgroup:my-rg"
	clusterARN := "arn:aws:elasticache:us-east-1:123456789012:cluster:my-rg-0001-001"

	rgAttrs := `{"ReplicationGroupId":"my-rg"}`
	clusterAttrs := `{"ReplicationGroupId":"my-rg"}`

	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheCacheCluster, clusterARN, testRegion, clusterAttrs)
	rgID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheReplicationGroup, rgARN, testRegion, rgAttrs)

	if err := resolveElastiCacheRelationships(acct, st); err != nil {
		t.Fatalf("resolveElastiCacheRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	// cluster→rg (attached-to) only; subnet-group and param-group absent in this test
	hasRG := false
	for _, rel := range rels {
		if rel.ToID == rgID && rel.Kind == store.RelAttachedTo {
			hasRG = true
		}
	}
	if !hasRG {
		t.Errorf("expected cluster -[attached-to]-> replication-group; got %+v", rels)
	}
}

// TestResolveElastiCacheRelationships_Memcached verifies that a Memcached cluster
// (no ReplicationGroupId) produces no relationships.
func TestResolveElastiCacheRelationships_Memcached(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clusterARN := "arn:aws:elasticache:us-east-1:123456789012:cluster:my-memcached"
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheCacheCluster, clusterARN, testRegion, "{}")

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

// --- CacheCluster → SubnetGroup ---

func TestResolveElastiCacheClusterToSubnetGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	sgARN := "arn:aws:elasticache:us-east-1:123456789012:subnetgroup:my-sg"
	clusterARN := "arn:aws:elasticache:us-east-1:123456789012:cluster:my-cluster"

	sgAttrs := `{"CacheSubnetGroupName":"my-sg"}`
	clusterAttrs := `{"CacheSubnetGroupName":"my-sg"}`

	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheCacheCluster, clusterARN, testRegion, clusterAttrs)
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheSubnetGroup, sgARN, testRegion, sgAttrs)

	if err := resolveElastiCacheRelationships(acct, st); err != nil {
		t.Fatalf("resolveElastiCacheRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	hasSG := false
	for _, rel := range rels {
		if rel.ToID == sgID && rel.Kind == store.RelAttachedTo {
			hasSG = true
		}
	}
	if !hasSG {
		t.Errorf("expected cluster -[attached-to]-> subnet-group; got %+v", rels)
	}
}

// TestResolveElastiCacheClusterToSubnetGroup_Empty verifies no panic on a cluster
// with no CacheSubnetGroupName.
func TestResolveElastiCacheClusterToSubnetGroup_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clusterARN := "arn:aws:elasticache:us-east-1:123456789012:cluster:my-cluster"
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheCacheCluster, clusterARN, testRegion, "{}")

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

// --- CacheCluster → ParameterGroup ---

func TestResolveElastiCacheClusterToParameterGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	pgARN := "arn:aws:elasticache:us-east-1:123456789012:parametergroup:my-pg"
	clusterARN := "arn:aws:elasticache:us-east-1:123456789012:cluster:my-cluster"

	pgAttrs := `{"CacheParameterGroupName":"my-pg"}`
	clusterAttrs := `{"CacheParameterGroup":{"CacheParameterGroupName":"my-pg"}}`

	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheCacheCluster, clusterARN, testRegion, clusterAttrs)
	pgID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheParameterGroup, pgARN, testRegion, pgAttrs)

	if err := resolveElastiCacheRelationships(acct, st); err != nil {
		t.Fatalf("resolveElastiCacheRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	hasPG := false
	for _, rel := range rels {
		if rel.ToID == pgID && rel.Kind == store.RelUses {
			hasPG = true
		}
	}
	if !hasPG {
		t.Errorf("expected cluster -[uses]-> parameter-group; got %+v", rels)
	}
}

// TestResolveElastiCacheClusterToParameterGroup_Missing verifies no panic when
// CacheParameterGroup is absent.
func TestResolveElastiCacheClusterToParameterGroup_Missing(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clusterARN := "arn:aws:elasticache:us-east-1:123456789012:cluster:my-cluster"
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheCacheCluster, clusterARN, testRegion, "{}")

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

// --- ReplicationGroup → SubnetGroup ---

func TestResolveElastiCacheReplicationGroupToSubnetGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	rgARN := "arn:aws:elasticache:us-east-1:123456789012:replicationgroup:my-rg"
	sgARN := "arn:aws:elasticache:us-east-1:123456789012:subnetgroup:my-sg"

	rgAttrs := `{"ReplicationGroupId":"my-rg","CacheSubnetGroupName":"my-sg"}`
	sgAttrs := `{"CacheSubnetGroupName":"my-sg"}`

	rgID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheReplicationGroup, rgARN, testRegion, rgAttrs)
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheSubnetGroup, sgARN, testRegion, sgAttrs)

	if err := resolveElastiCacheRelationships(acct, st); err != nil {
		t.Fatalf("resolveElastiCacheRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(rgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	hasSG := false
	for _, rel := range rels {
		if rel.ToID == sgID && rel.Kind == store.RelAttachedTo {
			hasSG = true
		}
	}
	if !hasSG {
		t.Errorf("expected replication-group -[attached-to]-> subnet-group; got %+v", rels)
	}
}

// --- ReplicationGroup → UserGroups ---

func TestResolveElastiCacheReplicationGroupToUserGroups(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	rgARN := "arn:aws:elasticache:us-east-1:123456789012:replicationgroup:my-rg"
	ugARN := "arn:aws:elasticache:us-east-1:123456789012:usergroup:my-ug"

	rgAttrs := `{"ReplicationGroupId":"my-rg","UserGroupIds":["my-ug"]}`
	ugAttrs := `{"UserGroupId":"my-ug"}`

	rgID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheReplicationGroup, rgARN, testRegion, rgAttrs)
	ugID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheUserGroup, ugARN, testRegion, ugAttrs)

	if err := resolveElastiCacheRelationships(acct, st); err != nil {
		t.Fatalf("resolveElastiCacheRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(rgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	hasUG := false
	for _, rel := range rels {
		if rel.ToID == ugID && rel.Kind == store.RelAttachedTo {
			hasUG = true
		}
	}
	if !hasUG {
		t.Errorf("expected replication-group -[attached-to]-> user-group; got %+v", rels)
	}
}

// --- GlobalReplicationGroup → ReplicationGroup ---

func TestResolveElastiCacheGlobalRGToReplicationGroups(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	grgARN := "arn:aws:elasticache::123456789012:globalreplicationgroup:my-grg"
	rgARN := "arn:aws:elasticache:us-east-1:123456789012:replicationgroup:my-rg"

	// Global RG lists its member RGs in Members[].
	grgAttrs := `{"GlobalReplicationGroupId":"my-grg","Members":[{"ReplicationGroupId":"my-rg"}]}`
	rgAttrs := `{"ReplicationGroupId":"my-rg"}`

	grgID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheGlobalReplicationGroup, grgARN, testRegion, grgAttrs)
	rgID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheReplicationGroup, rgARN, testRegion, rgAttrs)

	if err := resolveElastiCacheRelationships(acct, st); err != nil {
		t.Fatalf("resolveElastiCacheRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(grgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	hasRG := false
	for _, rel := range rels {
		if rel.ToID == rgID && rel.Kind == store.RelContains {
			hasRG = true
		}
	}
	if !hasRG {
		t.Errorf("expected global-rg -[contains]-> replication-group; got %+v", rels)
	}
}

// --- UserGroup → User ---

func TestResolveElastiCacheUserGroupToUsers(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	ugARN := "arn:aws:elasticache:us-east-1:123456789012:usergroup:my-ug"
	userARN := "arn:aws:elasticache:us-east-1:123456789012:user:my-user"

	ugAttrs := `{"UserGroupId":"my-ug","UserIds":["my-user"]}`
	userAttrs := `{"UserId":"my-user"}`

	ugID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheUserGroup, ugARN, testRegion, ugAttrs)
	userID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheUser, userARN, testRegion, userAttrs)

	if err := resolveElastiCacheRelationships(acct, st); err != nil {
		t.Fatalf("resolveElastiCacheRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(ugID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	hasUser := false
	for _, rel := range rels {
		if rel.ToID == userID && rel.Kind == store.RelContains {
			hasUser = true
		}
	}
	if !hasUser {
		t.Errorf("expected user-group -[contains]-> user; got %+v", rels)
	}
}

// --- ServerlessCache → UserGroup ---

func TestResolveElastiCacheServerlessCacheToUserGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	scARN := "arn:aws:elasticache:us-east-1:123456789012:serverlesscache:my-sc"
	ugARN := "arn:aws:elasticache:us-east-1:123456789012:usergroup:my-ug"

	scAttrs := `{"ServerlessCacheName":"my-sc","UserGroupId":"my-ug"}`
	ugAttrs := `{"UserGroupId":"my-ug"}`

	scID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheServerlessCache, scARN, testRegion, scAttrs)
	ugID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheUserGroup, ugARN, testRegion, ugAttrs)

	if err := resolveElastiCacheRelationships(acct, st); err != nil {
		t.Fatalf("resolveElastiCacheRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(scID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	hasUG := false
	for _, rel := range rels {
		if rel.ToID == ugID && rel.Kind == store.RelAttachedTo {
			hasUG = true
		}
	}
	if !hasUG {
		t.Errorf("expected serverless-cache -[attached-to]-> user-group; got %+v", rels)
	}
}

// TestResolveElastiCacheServerlessCacheToUserGroup_Empty verifies no panic when
// UserGroupId is absent.
func TestResolveElastiCacheServerlessCacheToUserGroup_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	scARN := "arn:aws:elasticache:us-east-1:123456789012:serverlesscache:my-sc"
	scID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheServerlessCache, scARN, testRegion, "{}")

	if err := resolveElastiCacheRelationships(acct, st); err != nil {
		t.Fatalf("resolveElastiCacheRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(scID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// TestIsDefaultElastiCachePG covers the AWS pre-created parameter-group
// name shape (default + version-tagged) and the customer-name fallthrough.
func TestIsDefaultElastiCachePG(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"default.redis6.x", true},
		{"default.memcached1.6", true},
		{"default.valkey7", true},
		{"default", false},              // bare prefix, no version → customer-allowed
		{"default-prod", false},         // dash, no period
		{"my-default.cluster", false},   // doesn't start with "default"
		{"default.custom-suffix", true}, // exotic future engine still managed
		{"production-cluster-pg", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isDefaultElastiCachePG(c.name); got != c.want {
			t.Errorf("isDefaultElastiCachePG(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestResolveElastiCacheSubnetGroupVPC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	sgARN := fmt.Sprintf("arn:aws:elasticache:%s:%s:subnetgroup:default", testRegion, acct.ID)
	attrs := `{"VpcId":"vpc-1","Subnets":[{"SubnetIdentifier":"subnet-1"}]}`
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheSubnetGroup, sgARN, testRegion, attrs)
	if err := resolveElastiCacheSubnetGroupVPC(acct, st); err != nil {
		t.Fatalf("resolveElastiCacheSubnetGroupVPC: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sgID)
	assertRelationship(t, rels, sgID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, sgID, subID, store.RelAttachedTo)
}
