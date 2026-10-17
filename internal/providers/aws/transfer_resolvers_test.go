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

func TestResolveTransferAccessRole(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	access := transferDescribedAccess("S-1-5-21-1")
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, sv(access.Role), "", "{}")
	aNative := transferAccessNativeID(transferTestServerARN("s-1"), "S-1-5-21-1")
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeTransferAccess, aNative, testRegion, mustJSON(access))
	if err := resolveTransferAccessRole(acct, st); err != nil {
		t.Fatalf("resolveTransferAccessRole: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, rID, store.RelAssumes)
}

func TestResolveTransferAccessRole_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, sv(transferDescribedAccess("S-1-5-21-1").Role), "", "{}")
	aNative := transferAccessNativeID(transferTestServerARN("s-1"), "S-1-5-21-1")
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeTransferAccess, aNative, testRegion, "{}")
	if err := resolveTransferAccessRole(acct, st); err != nil {
		t.Fatalf("resolveTransferAccessRole: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	if len(rels) != 0 {
		t.Errorf("relationships = %v; want none", rels)
	}
}

func TestResolveTransferRoleEdge_DecodeErrorReturned(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	// ListResources orders by name, so the bad row "a" is read before the
	// valid row "b", whose edge must survive the decode error.
	bad := transferAccessNativeID(transferTestServerARN("s-1"), "S-1-5-21-1")
	upsertTestResourceNamed(t, st, TypeTransferAccess, bad, testRegion, `["not an object"]`, "a")
	access := transferDescribedAccess("S-1-5-21-2")
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, sv(access.Role), "", "{}")
	valid := transferAccessNativeID(transferTestServerARN("s-1"), "S-1-5-21-2")
	vID := upsertTestResourceNamed(t, st, TypeTransferAccess, valid, testRegion, mustJSON(access), "b")
	if err := resolveTransferAccessRole(acct, st); err == nil {
		t.Fatal("resolve: nil error, want the decode error")
	}
	rels, _ := st.RelationshipsFrom(vID)
	assertRelationship(t, rels, vID, rID, store.RelAssumes)
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
