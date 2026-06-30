package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func finspaceKxEnvARN(acct, envID string) string {
	return fmt.Sprintf("arn:aws:finspace:%s:%s:kxEnvironment/%s", testRegion, acct, envID)
}

func TestResolveFinspaceKxChildEnv(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	envARN := finspaceKxEnvARN(acct.ID, "env-1")
	envID := upsertTestResource(t, st, "aws", acct.ID, TypeFinspaceKxEnvironment, envARN, testRegion, "{}")

	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeFinspaceKxCluster, envARN+"/cluster/c1", testRegion, "{}")
	dbID := upsertTestResource(t, st, "aws", acct.ID, TypeFinspaceKxDatabase, envARN+"/database/db1", testRegion, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeFinspaceKxScalingGroup, envARN+"/scaling-group/sg1", testRegion, "{}")
	volID := upsertTestResource(t, st, "aws", acct.ID, TypeFinspaceKxVolume, envARN+"/volume/v1", testRegion, "{}")
	userARN := envARN + "/kxUser/u1"
	userID := upsertTestResource(t, st, "aws", acct.ID, TypeFinspaceKxUser, userARN, testRegion, "{}")

	if err := resolveFinspaceKxChildEnv(acct, st); err != nil {
		t.Fatalf("resolveFinspaceKxChildEnv: %v", err)
	}
	for _, childID := range []string{clusterID, dbID, sgID, volID, userID} {
		rels, _ := st.RelationshipsFrom(childID)
		assertRelationship(t, rels, childID, envID, store.RelAttachedTo)
	}
}

func TestResolveFinspaceKxChildEnv_NoEnv(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	// Cluster whose parent environment was never scanned: no edge, no FK error.
	envARN := finspaceKxEnvARN(acct.ID, "env-missing")
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeFinspaceKxCluster, envARN+"/cluster/c1", testRegion, "{}")
	if err := resolveFinspaceKxChildEnv(acct, st); err != nil {
		t.Fatalf("resolveFinspaceKxChildEnv: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(clusterID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveFinspaceKxDataview(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	envARN := finspaceKxEnvARN(acct.ID, "env-1")
	envID := upsertTestResource(t, st, "aws", acct.ID, TypeFinspaceKxEnvironment, envARN, testRegion, "{}")
	dbNative := envARN + "/database/db1"
	dbID := upsertTestResource(t, st, "aws", acct.ID, TypeFinspaceKxDatabase, dbNative, testRegion, "{}")
	dvID := upsertTestResource(t, st, "aws", acct.ID, TypeFinspaceKxDataview, dbNative+"/dataview/dv1", testRegion, "{}")

	if err := resolveFinspaceKxDataview(acct, st); err != nil {
		t.Fatalf("resolveFinspaceKxDataview: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dvID)
	assertRelationship(t, rels, dvID, dbID, store.RelAttachedTo)
	assertRelationship(t, rels, dvID, envID, store.RelAttachedTo)
}

func TestResolveFinspaceKxEnvironmentKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/k-1", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	envARN := finspaceKxEnvARN(acct.ID, "env-1")
	attrs := fmt.Sprintf(`{"KmsKeyId":"%s"}`, keyARN)
	envID := upsertTestResource(t, st, "aws", acct.ID, TypeFinspaceKxEnvironment, envARN, testRegion, attrs)

	if err := resolveFinspaceKxEnvironmentKMS(acct, st); err != nil {
		t.Fatalf("resolveFinspaceKxEnvironmentKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(envID)
	assertRelationship(t, rels, envID, keyID, store.RelUses)
}

func TestResolveFinspaceKxEnvironmentKMS_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	envARN := finspaceKxEnvARN(acct.ID, "env-1")
	envID := upsertTestResource(t, st, "aws", acct.ID, TypeFinspaceKxEnvironment, envARN, testRegion, "{}")
	if err := resolveFinspaceKxEnvironmentKMS(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(envID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}
