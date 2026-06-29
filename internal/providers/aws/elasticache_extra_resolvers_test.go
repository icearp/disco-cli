package aws

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveElastiCacheSnapshotRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	ccID := upsertTestResourceNamed(t, st, TypeElastiCacheCacheCluster,
		"arn:aws:elasticache:"+region+":"+testAccountID+":cluster:cc-1", region, "{}", "cc-1")
	keyARN := "arn:aws:kms:" + region + ":" + testAccountID + ":key/k-1"
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, region, "{}")
	snapARN := "arn:aws:elasticache:" + region + ":" + testAccountID + ":snapshot:snap-1"
	attrs := `{"CacheClusterId":"cc-1","KmsKeyId":"` + keyARN + `"}`
	snapID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheSnapshot, snapARN, region, attrs)

	if err := resolveElastiCacheSnapshotRelationships(acct, st); err != nil {
		t.Fatalf("resolveElastiCacheSnapshotRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(snapID)
	assertRelationship(t, rels, snapID, ccID, store.RelAttachedTo)
	assertRelationship(t, rels, snapID, kID, store.RelUses)
}

func TestResolveElastiCacheSnapshotRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	snapARN := "arn:aws:elasticache:" + region + ":" + testAccountID + ":snapshot:snap-1"
	snapID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheSnapshot, snapARN, region, "{}")
	if err := resolveElastiCacheSnapshotRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(snapID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveElastiCacheServerlessSnapshotRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	scID := upsertTestResourceNamed(t, st, TypeElastiCacheServerlessCache,
		"arn:aws:elasticache:"+region+":"+testAccountID+":serverlesscache:sc-1", region, "{}", "sc-1")
	snapARN := "arn:aws:elasticache:" + region + ":" + testAccountID + ":serverlesscachesnapshot:scs-1"
	attrs := `{"ServerlessCacheConfiguration":{"ServerlessCacheName":"sc-1"}}`
	snapID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheServerlessCacheSnapshot, snapARN, region, attrs)

	if err := resolveElastiCacheServerlessSnapshotRelationships(acct, st); err != nil {
		t.Fatalf("resolveElastiCacheServerlessSnapshotRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(snapID)
	assertRelationship(t, rels, snapID, scID, store.RelAttachedTo)
}

func TestResolveElastiCacheServerlessSnapshotRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	snapARN := "arn:aws:elasticache:" + region + ":" + testAccountID + ":serverlesscachesnapshot:scs-1"
	snapID := upsertTestResource(t, st, "aws", acct.ID, TypeElastiCacheServerlessCacheSnapshot, snapARN, region, "{}")
	if err := resolveElastiCacheServerlessSnapshotRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(snapID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}
