package aws

import (
	"fmt"
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

func TestResolveIoTSWPortalRole(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/iotsw-portal", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	pARN := iotSWARN(testRegion, acct.ID, "portal", "p-1")
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTSWPortal, pARN, testRegion,
		fmt.Sprintf(`{"RoleArn":%q}`, roleARN))
	if err := resolveIoTSWPortalRole(acct, st); err != nil {
		t.Fatalf("resolveIoTSWPortalRole: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, roleID, store.RelAssumes)
}

func TestResolveIoTSWGatewayThing(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	thingName := "core-device-1"
	thingARN := fmt.Sprintf("arn:aws:iot:%s:%s:thing/%s", testRegion, acct.ID, thingName)
	regionVal := testRegion
	thingRow := &store.Resource{
		Provider: "aws", AccountID: acct.ID, Type: TypeIoTThing,
		NativeID: thingARN, Name: &thingName, Region: &regionVal,
		AttributesJSON: "{}", DiscoveredBy: testScanID,
	}
	if _, err := st.UpsertResource(thingRow); err != nil {
		t.Fatalf("upsert thing: %v", err)
	}
	thingID := store.ResourceID("aws", acct.ID, TypeIoTThing, thingARN)
	gARN := iotSWARN(testRegion, acct.ID, "gateway", "gw-1")
	gAttrs := fmt.Sprintf(`{"GatewayPlatform":{"GreengrassV2":{"CoreDeviceThingName":%q}}}`, thingName)
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTSWGateway, gARN, testRegion, gAttrs)
	if err := resolveIoTSWGatewayThing(acct, st); err != nil {
		t.Fatalf("resolveIoTSWGatewayThing: %v", err)
	}
	rels, _ := st.RelationshipsFrom(gID)
	assertRelationship(t, rels, gID, thingID, store.RelUses)
}

func TestResolveIoTSWAssetModelHierarchies(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	parentARN := iotSWARN(testRegion, acct.ID, "asset-model", "m-parent")
	childARN := iotSWARN(testRegion, acct.ID, "asset-model", "m-child")
	attrs := `{"AssetModelHierarchies":[{"ChildAssetModelId":"m-child"}]}`

	pID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTSWAssetModel, parentARN, testRegion, attrs)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTSWAssetModel, childARN, testRegion, "{}")

	if err := resolveIoTSWAssetModelHierarchies(acct, st); err != nil {
		t.Fatalf("resolveIoTSWAssetModelHierarchies: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, cID, store.RelUses)
}

func TestResolveIoTSWDatasetRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	dARN := iotSWARN(testRegion, acct.ID, "dataset", "d-1")
	kbARN := "arn:aws:bedrock:" + testRegion + ":" + testAccountID + ":knowledge-base/kb-xyz"
	roleARN := "arn:aws:iam::" + testAccountID + ":role/iotsw-ds"
	attrs := fmt.Sprintf(`{"DatasetSource":{"SourceDetail":{"Kendra":{"KnowledgeBaseArn":%q,"RoleArn":%q}}}}`, kbARN, roleARN)

	dID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTSWDataset, dARN, testRegion, attrs)
	kbID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockKnowledgeBase, kbARN, testRegion, "{}")
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")

	if err := resolveIoTSWDatasetRefs(acct, st); err != nil {
		t.Fatalf("resolveIoTSWDatasetRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dID)
	assertRelationship(t, rels, dID, kbID, store.RelUses)
	assertRelationship(t, rels, dID, rID, store.RelAssumes)
}
