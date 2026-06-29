package aws

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveDSQLClusterKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clusterARN := "arn:aws:dsql:us-east-1:" + testAccountID + ":cluster/abc123"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-1"
	attrs := `{"EncryptionDetails":{"KmsKeyArn":"` + keyARN + `"}}`

	cID := upsertTestResource(t, st, "aws", acct.ID, TypeDSQLCluster, clusterARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveDSQLClusterKMS(acct, st); err != nil {
		t.Fatalf("resolveDSQLClusterKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, kID, store.RelUses)
}

func TestResolveDSQLClusterKMS_NoEncryption(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeDSQLCluster,
		"arn:aws:dsql:us-east-1:"+testAccountID+":cluster/abc123", testRegion, "{}")
	if err := resolveDSQLClusterKMS(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
}

func TestResolveDSQLStreamCluster(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clusterARN := "arn:aws:dsql:us-east-1:" + testAccountID + ":cluster/abc123"
	streamARN := "arn:aws:dsql:us-east-1:" + testAccountID + ":cluster/abc123/stream/s-1"
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeDSQLCluster, clusterARN, testRegion, "{}")
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeDSQLStream, streamARN, testRegion, `{"ClusterIdentifier":"abc123"}`)

	if err := resolveDSQLStreamCluster(acct, st); err != nil {
		t.Fatalf("resolveDSQLStreamCluster: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sID)
	assertRelationship(t, rels, sID, cID, store.RelAttachedTo)
}

func TestResolveDSQLStreamCluster_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	streamARN := "arn:aws:dsql:us-east-1:" + testAccountID + ":cluster/abc123/stream/s-1"
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeDSQLStream, streamARN, testRegion, "{}")
	if err := resolveDSQLStreamCluster(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(sID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}
