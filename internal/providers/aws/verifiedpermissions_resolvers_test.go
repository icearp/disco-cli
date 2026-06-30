package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveVPChildToPolicyStore(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	psARN := fmt.Sprintf("arn:aws:verifiedpermissions::%s:policy-store/abc", acct.ID)
	psID := upsertTestResource(t, st, "aws", acct.ID, TypeVerifiedPermissionsPolicyStore, psARN, testRegion, "{}")
	pARN := psARN + "/policy/p1"
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeVerifiedPermissionsPolicy, pARN, testRegion, "{}")
	tARN := psARN + "/policy-template/t1"
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeVerifiedPermissionsPolicyTemplate, tARN, testRegion, "{}")
	isARN := psARN + "/identity-source/i1"
	isID := upsertTestResource(t, st, "aws", acct.ID, TypeVerifiedPermissionsIdentitySource, isARN, testRegion, "{}")
	if err := resolveVPChildToPolicyStore(acct, st); err != nil {
		t.Fatalf("resolveVPChildToPolicyStore: %v", err)
	}
	for _, srcID := range []string{pID, tID, isID} {
		rels, _ := st.RelationshipsFrom(srcID)
		assertRelationship(t, rels, srcID, psID, store.RelAttachedTo)
	}
}

func TestResolveVPPolicyStoreAliasParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	psARN := fmt.Sprintf("arn:aws:verifiedpermissions::%s:policy-store/abc", acct.ID)
	psID := upsertTestResource(t, st, "aws", acct.ID, TypeVerifiedPermissionsPolicyStore, psARN, testRegion, `{"PolicyStoreId":"abc"}`)
	aliasARN := fmt.Sprintf("arn:aws:verifiedpermissions::%s:policy-store-alias/myalias", acct.ID)
	aliasID := upsertTestResource(t, st, "aws", acct.ID, TypeVerifiedPermissionsPolicyStoreAlias, aliasARN, testRegion, `{"PolicyStoreId":"abc","AliasName":"myalias"}`)
	if err := resolveVPPolicyStoreAliasParent(acct, st); err != nil {
		t.Fatalf("resolveVPPolicyStoreAliasParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aliasID)
	assertRelationship(t, rels, aliasID, psID, store.RelAttachedTo)
}

func TestResolveVPPolicyStoreAliasParent_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	aliasARN := fmt.Sprintf("arn:aws:verifiedpermissions::%s:policy-store-alias/myalias", acct.ID)
	aliasID := upsertTestResource(t, st, "aws", acct.ID, TypeVerifiedPermissionsPolicyStoreAlias, aliasARN, testRegion, "{}")
	if err := resolveVPPolicyStoreAliasParent(acct, st); err != nil {
		t.Fatalf("resolveVPPolicyStoreAliasParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aliasID)
	if len(rels) != 0 {
		t.Fatalf("expected no edges for alias with no attrs, got %d", len(rels))
	}
}
