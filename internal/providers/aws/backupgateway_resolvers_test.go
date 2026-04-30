package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveBackupGatewayRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	kmsARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/key-bgw", testRegion, acct.ID)
	hypARN := fmt.Sprintf("arn:aws:backup-gateway:%s:%s:hypervisor/hyp-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"KmsKeyArn":%q,"Name":"vc1"}`, kmsARN)

	hID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupGatewayHypervisor, hypARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, kmsARN, testRegion, "{}")

	if err := resolveBackupGatewayRelationships(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(hID)
	assertRelationship(t, rels, hID, kID, store.RelUses)
}

func TestResolveBackupGatewayRelationships_NoKey(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	hypARN := fmt.Sprintf("arn:aws:backup-gateway:%s:%s:hypervisor/hyp-2", testRegion, acct.ID)
	hID := upsertTestResource(t, st, "aws", acct.ID, TypeBackupGatewayHypervisor, hypARN, testRegion, `{"Name":"vc2"}`)

	if err := resolveBackupGatewayRelationships(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(hID)
	if len(rels) != 0 {
		t.Errorf("unexpected rels: %+v", rels)
	}
}
