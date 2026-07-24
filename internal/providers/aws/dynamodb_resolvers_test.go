package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

// TestResolveDynamoDBGlobalTableRelationships_HappyPath verifies a global
// table links to its regional replica table via a "contains" relationship.
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

// TestResolveDynamoDBGlobalTableRelationships_Empty verifies a global table
// with no ReplicationGroup data produces no relationships and no panic.
func TestResolveDynamoDBGlobalTableRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	upsertTestResource(t, st, "aws", testAccountID,
		TypeDynamoDBGlobalTable, "arn:aws:dynamodb:::123:global-table/Empty", "", `{}`)

	if err := resolveDynamoDBGlobalTableRelationships(acct, st); err != nil {
		t.Fatalf("resolver error: %v", err)
	}
}

// TestResolveDynamoDBTableRelationships_KMS verifies KMS link for a table
// configured with customer-managed SSE.
func TestResolveDynamoDBTableRelationships_KMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	tableARN := "arn:aws:dynamodb:us-east-1:" + testAccountID + ":table/Enc"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/abcd"
	attrs := `{"SSEDescription":{"KMSMasterKeyArn":"` + keyARN + `"}}`

	tableID := upsertTestResource(t, st, "aws", testAccountID, TypeDynamoDBTable, tableARN, testRegion, attrs)
	keyID := upsertTestResource(t, st, "aws", testAccountID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveDynamoDBTableRelationships(acct, st); err != nil {
		t.Fatalf("resolver error: %v", err)
	}
	rels, err := st.RelationshipsFrom(tableID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, tableID, keyID, store.RelUses)
}

// TestResolveDynamoDBStreamRelationships verifies a table with streaming
// enabled emits a "contains" edge to its stream.
func TestResolveDynamoDBStreamRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	tableARN := "arn:aws:dynamodb:us-east-1:" + testAccountID + ":table/Streamed"
	streamARN := "arn:aws:dynamodb:us-east-1:" + testAccountID + ":table/Streamed/stream/2024-01-01T00:00:00.000"
	attrs := `{"LatestStreamArn":"` + streamARN + `"}`

	tableID := upsertTestResource(t, st, "aws", testAccountID, TypeDynamoDBTable, tableARN, testRegion, attrs)
	streamID := upsertTestResource(t, st, "aws", testAccountID, TypeDynamoDBStream, streamARN, testRegion, "{}")

	if err := resolveDynamoDBStreamRelationships(acct, st); err != nil {
		t.Fatalf("resolver error: %v", err)
	}
	rels, err := st.RelationshipsFrom(tableID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, tableID, streamID, store.RelContains)
}

// TestResolveDynamoDBStreamRelationships_NoStream verifies no edges when
// streaming is not enabled.
func TestResolveDynamoDBStreamRelationships_NoStream(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	tableARN := "arn:aws:dynamodb:us-east-1:" + testAccountID + ":table/NoStream"
	tableID := upsertTestResource(t, st, "aws", testAccountID, TypeDynamoDBTable, tableARN, testRegion, "{}")

	if err := resolveDynamoDBStreamRelationships(acct, st); err != nil {
		t.Fatalf("resolver error: %v", err)
	}
	rels, err := st.RelationshipsFrom(tableID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveDynamoDBBackupRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	tableARN := "arn:aws:dynamodb:us-east-1:" + testAccountID + ":table/Orders"
	backupARN := "arn:aws:dynamodb:us-east-1:" + testAccountID + ":table/Orders/backup/01234567890123-abcdef12"
	tableID := upsertTestResource(t, st, "aws", testAccountID, TypeDynamoDBTable, tableARN, testRegion, "{}")
	backupID := upsertTestResource(t, st, "aws", testAccountID, TypeDynamoDBBackup, backupARN, testRegion, `{"TableArn":"`+tableARN+`"}`)

	if err := resolveDynamoDBBackupRelationships(acct, st); err != nil {
		t.Fatalf("resolveDynamoDBBackupRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(backupID)
	assertRelationship(t, rels, backupID, tableID, store.RelAttachedTo)
}

func TestResolveDynamoDBBackupRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	backupARN := "arn:aws:dynamodb:us-east-1:" + testAccountID + ":table/Orders/backup/01234567890123-abcdef12"
	backupID := upsertTestResource(t, st, "aws", testAccountID, TypeDynamoDBBackup, backupARN, testRegion, "{}")
	if err := resolveDynamoDBBackupRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(backupID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}
