package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveTransferAgreementRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	sARN := fmt.Sprintf("arn:aws:transfer:%s:%s:server/s-1", testRegion, acct.ID)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeTransferServer, sARN, testRegion, `{"ServerId":"s-1"}`)
	pARN := fmt.Sprintf("arn:aws:transfer:%s:%s:profile/p-1", testRegion, acct.ID)
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeTransferProfile, pARN, testRegion, `{"ProfileId":"p-1"}`)
	aARN := fmt.Sprintf("arn:aws:transfer:%s:%s:agreement/a-1", testRegion, acct.ID)
	attrs := `{"ServerId":"s-1","LocalProfileId":"p-1"}`
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeTransferAgreement, aARN, testRegion, attrs)
	if err := resolveTransferAgreementRefs(acct, st); err != nil {
		t.Fatalf("resolveTransferAgreementRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, sID, store.RelAttachedTo)
	assertRelationship(t, rels, aID, pID, store.RelUses)
}

func TestResolveTransferUserParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	sARN := fmt.Sprintf("arn:aws:transfer:%s:%s:server/s-1", testRegion, acct.ID)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeTransferServer, sARN, testRegion, `{"ServerId":"s-1"}`)
	uARN := fmt.Sprintf("arn:aws:transfer:%s:%s:user/s-1/alice", testRegion, acct.ID)
	uID := upsertTestResource(t, st, "aws", acct.ID, TypeTransferUser, uARN, testRegion, "{}")
	if err := resolveTransferUserParent(acct, st); err != nil {
		t.Fatalf("resolveTransferUserParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(uID)
	assertRelationship(t, rels, uID, sID, store.RelAttachedTo)
}

func TestResolveTransferUserRole(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/Tx", acct.ID)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	uARN := fmt.Sprintf("arn:aws:transfer:%s:%s:user/s-1/alice", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"Role":%q}`, roleARN)
	uID := upsertTestResource(t, st, "aws", acct.ID, TypeTransferUser, uARN, testRegion, attrs)
	if err := resolveTransferUserRole(acct, st); err != nil {
		t.Fatalf("resolveTransferUserRole: %v", err)
	}
	rels, _ := st.RelationshipsFrom(uID)
	assertRelationship(t, rels, uID, rID, store.RelAssumes)
}

func TestResolveTransferServerLoggingRole(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/Log", acct.ID)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	sARN := fmt.Sprintf("arn:aws:transfer:%s:%s:server/s-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"LoggingRole":%q,"ServerId":"s-1"}`, roleARN)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeTransferServer, sARN, testRegion, attrs)
	if err := resolveTransferServerLoggingRole(acct, st); err != nil {
		t.Fatalf("resolveTransferServerLoggingRole: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sID)
	assertRelationship(t, rels, sID, rID, store.RelAssumes)
}

func TestResolveTransferHostKeyParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	sARN := fmt.Sprintf("arn:aws:transfer:%s:%s:server/s-1", testRegion, acct.ID)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeTransferServer, sARN, testRegion, `{"ServerId":"s-1"}`)
	hkARN := fmt.Sprintf("arn:aws:transfer:%s:%s:host-key/s-1/hostkey-1", testRegion, acct.ID)
	hkID := upsertTestResource(t, st, "aws", acct.ID, TypeTransferHostKey, hkARN, testRegion, `{"HostKeyId":"hostkey-1"}`)
	if err := resolveTransferHostKeyParent(acct, st); err != nil {
		t.Fatalf("resolveTransferHostKeyParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(hkID)
	assertRelationship(t, rels, hkID, sID, store.RelAttachedTo)
}

func TestResolveTransferHostKeyParent_NoMatch(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	hkARN := fmt.Sprintf("arn:aws:transfer:%s:%s:host-key/s-missing/hostkey-1", testRegion, acct.ID)
	hkID := upsertTestResource(t, st, "aws", acct.ID, TypeTransferHostKey, hkARN, testRegion, "{}")
	if err := resolveTransferHostKeyParent(acct, st); err != nil {
		t.Fatalf("resolveTransferHostKeyParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(hkID)
	if len(rels) != 0 {
		t.Errorf("expected no relationships, got %d", len(rels))
	}
}
