package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveNeptuneGraphSnapshotRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gARN := neptuneGraphARN(testRegion, acct.ID, "g-1")
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeNeptuneGraphGraph, gARN, testRegion, "{}")
	kARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abcdef-1234", testRegion, acct.ID)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, kARN, testRegion, "{}")
	snARN := fmt.Sprintf("arn:aws:neptune-graph:%s:%s:graph-snapshot/sn-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"SourceGraphId":"g-1","KmsKeyIdentifier":"%s"}`, kARN)
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeNeptuneGraphGraphSnapshot, snARN, testRegion, attrs)
	if err := resolveNeptuneGraphSnapshotRefs(acct, st); err != nil {
		t.Fatalf("resolveNeptuneGraphSnapshotRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(snID)
	assertRelationship(t, rels, snID, gID, store.RelAttachedTo)
	assertRelationship(t, rels, snID, kID, store.RelUses)
}

func TestResolveNeptuneGraphPrivateEndpointRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gARN := neptuneGraphARN(testRegion, acct.ID, "g-1")
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeNeptuneGraphGraph, gARN, testRegion, "{}")
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	peARN := gARN + "/private-graph-endpoint/vpc-1"
	peID := upsertTestResource(t, st, "aws", acct.ID, TypeNeptuneGraphPrivateGraphEndpoint, peARN, testRegion, `{"VpcId":"vpc-1"}`)
	if err := resolveNeptuneGraphPrivateEndpointRefs(acct, st); err != nil {
		t.Fatalf("resolveNeptuneGraphPrivateEndpointRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(peID)
	assertRelationship(t, rels, peID, gID, store.RelAttachedTo)
	assertRelationship(t, rels, peID, vpcID, store.RelAttachedTo)
}
