package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveIoTFWRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	scARN := fmt.Sprintf("arn:aws:iotfleetwise:%s:%s:signal-catalog/sc1", testRegion, acct.ID)
	scID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTFWSignalCatalog, scARN, testRegion, "{}")
	mmARN := fmt.Sprintf("arn:aws:iotfleetwise:%s:%s:model-manifest/mm1", testRegion, acct.ID)
	mmAttrs := fmt.Sprintf(`{"SignalCatalogArn":"%s"}`, scARN)
	mmID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTFWModelManifest, mmARN, testRegion, mmAttrs)
	dmARN := fmt.Sprintf("arn:aws:iotfleetwise:%s:%s:decoder-manifest/dm1", testRegion, acct.ID)
	dmAttrs := fmt.Sprintf(`{"ModelManifestArn":"%s"}`, mmARN)
	dmID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTFWDecoderManifest, dmARN, testRegion, dmAttrs)
	cARN := fmt.Sprintf("arn:aws:iotfleetwise:%s:%s:campaign/c1", testRegion, acct.ID)
	cAttrs := fmt.Sprintf(`{"SignalCatalogArn":"%s"}`, scARN)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTFWCampaign, cARN, testRegion, cAttrs)
	fARN := fmt.Sprintf("arn:aws:iotfleetwise:%s:%s:fleet/f1", testRegion, acct.ID)
	fAttrs := fmt.Sprintf(`{"SignalCatalogArn":"%s"}`, scARN)
	fID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTFWFleet, fARN, testRegion, fAttrs)
	vARN := fmt.Sprintf("arn:aws:iotfleetwise:%s:%s:vehicle/v1", testRegion, acct.ID)
	vAttrs := fmt.Sprintf(`{"DecoderManifestArn":"%s","ModelManifestArn":"%s"}`, dmARN, mmARN)
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTFWVehicle, vARN, testRegion, vAttrs)

	if err := resolveIoTFWRefs(acct, st); err != nil {
		t.Fatalf("resolveIoTFWRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mmID)
	assertRelationship(t, rels, mmID, scID, store.RelUses)
	rels, _ = st.RelationshipsFrom(dmID)
	assertRelationship(t, rels, dmID, mmID, store.RelUses)
	rels, _ = st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, scID, store.RelUses)
	rels, _ = st.RelationshipsFrom(fID)
	assertRelationship(t, rels, fID, scID, store.RelUses)
	rels, _ = st.RelationshipsFrom(vID)
	assertRelationship(t, rels, vID, dmID, store.RelUses)
	assertRelationship(t, rels, vID, mmID, store.RelUses)
}
