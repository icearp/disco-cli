package aws

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveIoTSWAssetToModel(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	mARN := iotSWARN(testRegion, acct.ID, "asset-model", "m-1")
	mID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTSWAssetModel, mARN, testRegion, "{}")
	aARN := iotSWARN(testRegion, acct.ID, "asset", "a-1")
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTSWAsset, aARN, testRegion, `{"AssetModelId":"m-1"}`)
	if err := resolveIoTSWAssetToModel(acct, st); err != nil {
		t.Fatalf("resolveIoTSWAssetToModel: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, mID, store.RelUses)
}

func TestResolveIoTSWAccessPolicyTarget(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pARN := iotSWARN(testRegion, acct.ID, "portal", "p-1")
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTSWPortal, pARN, testRegion, "{}")
	prjARN := iotSWARN(testRegion, acct.ID, "project", "prj-1")
	prjID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTSWProject, prjARN, testRegion, "{}")
	apPortalARN := iotSWARN(testRegion, acct.ID, "access-policy", "ap-1")
	apPortalID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTSWAccessPolicy, apPortalARN, testRegion,
		`{"Resource":{"Portal":{"Id":"p-1"}}}`)
	apPrjARN := iotSWARN(testRegion, acct.ID, "access-policy", "ap-2")
	apPrjID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTSWAccessPolicy, apPrjARN, testRegion,
		`{"Resource":{"Project":{"Id":"prj-1"}}}`)
	if err := resolveIoTSWAccessPolicyTarget(acct, st); err != nil {
		t.Fatalf("resolveIoTSWAccessPolicyTarget: %v", err)
	}
	rels, _ := st.RelationshipsFrom(apPortalID)
	assertRelationship(t, rels, apPortalID, pID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(apPrjID)
	assertRelationship(t, rels, apPrjID, prjID, store.RelAttachedTo)
}
