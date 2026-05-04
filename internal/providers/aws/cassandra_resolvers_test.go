package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveCassandraTableKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	tARN := fmt.Sprintf("arn:aws:cassandra:%s:%s:keyspace/ks1/table/t1", testRegion, acct.ID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"EncryptionSpecification":{"Type":"CUSTOMER_MANAGED_KMS_KEY","KmsKeyIdentifier":%q}}`, keyARN)

	tID := upsertTestResource(t, st, "aws", acct.ID, TypeCassandraTable, tARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveCassandraTableKMS(acct, st); err != nil {
		t.Fatalf("resolveCassandraTableKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tID)
	assertRelationship(t, rels, tID, kID, store.RelUses)
}
