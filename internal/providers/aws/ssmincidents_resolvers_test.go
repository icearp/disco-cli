package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveSSMIRSetKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/k-1", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	rsARN := fmt.Sprintf("arn:aws:ssm-incidents::%s:replication-set/r-1", acct.ID)
	attrs := fmt.Sprintf(`{"RegionMap":{"%s":{"SseKmsKeyId":"%s"}}}`, testRegion, keyARN)
	rsID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMIncidentsReplicationSet, rsARN, testRegion, attrs)
	if err := resolveSSMIRSetKMS(acct, st); err != nil {
		t.Fatalf("resolveSSMIRSetKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rsID)
	assertRelationship(t, rels, rsID, keyID, store.RelUses)
}
