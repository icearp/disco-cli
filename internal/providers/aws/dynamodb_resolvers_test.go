package aws

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveDynamoDBGlobalTableRelationships_HappyPath verifies that a global
// table is linked to its regional replica table via a "contains" relationship.
func TestResolveDynamoDBGlobalTableRelationships_HappyPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	replicaARN := "arn:aws:dynamodb:us-east-1:" + testAccountID + ":table/MyTable"
	globalARN := "arn:aws:dynamodb:::" + testAccountID + ":global-table/MyTable"

	// Global table with one replica in the ReplicationGroup.
	globalID := upsertTestResource(t, st, "aws", testAccountID,
		TypeDynamoDBGlobalTable, globalARN, "",
		`{"ReplicationGroup":[{"ReplicaArn":"`+replicaARN+`"}]}`)

	// The replica table that should be linked.
	tableID := upsertTestResource(t, st, "aws", testAccountID,
		TypeDynamoDBTable, replicaARN, testRegion, `{}`)

	if err := resolveDynamoDBGlobalTableRelationships(acct, st); err != nil {
		t.Fatalf("resolver error: %v", err)
	}

	rels, err := st.RelationshipsFrom(globalID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, globalID, tableID, store.RelContains)
}

// TestResolveDynamoDBGlobalTableRelationships_Empty verifies that a global
// table with no ReplicationGroup data produces no relationships and no panic.
func TestResolveDynamoDBGlobalTableRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	upsertTestResource(t, st, "aws", testAccountID,
		TypeDynamoDBGlobalTable, "arn:aws:dynamodb:::123:global-table/Empty", "", `{}`)

	if err := resolveDynamoDBGlobalTableRelationships(acct, st); err != nil {
		t.Fatalf("resolver error: %v", err)
	}
}
