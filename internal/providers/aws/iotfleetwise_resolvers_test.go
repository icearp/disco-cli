package aws

import (
	"fmt"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	iotfwtypes "github.com/aws/aws-sdk-go-v2/service/iotfleetwise/types"
	"github.com/icearp/disco-cli/store"
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

func iotFWSignalAttrs(fqn, iface string) string {
	s := iotfwtypes.SignalDecoder{FullyQualifiedName: sdkaws.String(fqn), Type: iotfwtypes.SignalDecoderTypeCanSignal}
	if iface != "" {
		s.InterfaceId = sdkaws.String(iface)
	}
	return mustJSON(s)
}

// TestResolveIoTFWSignalInterfaces_SameManifestOnly links a signal decoder to
// the network interface of its own manifest only: dm-b's decoder names can0,
// which only dm-a has, and a decoder without an interface id links nowhere.
func TestResolveIoTFWSignalInterfaces_SameManifestOnly(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ifaceA := upsertTestResource(t, st, "aws", testAccountID, TypeIoTFWDecoderManifestNetworkInterface, iotFWTestIfaceA1, testRegion,
		mustJSON(iotfwtypes.NetworkInterface{InterfaceId: sdkaws.String("can0"), Type: iotfwtypes.NetworkInterfaceTypeCanInterface}))
	sigA := upsertTestResource(t, st, "aws", testAccountID, TypeIoTFWDecoderManifestSignal, iotFWTestSignalA1, testRegion, iotFWSignalAttrs("Vehicle.Speed", "can0"))
	sigB := upsertTestResource(t, st, "aws", testAccountID, TypeIoTFWDecoderManifestSignal, iotFWTestSignalB1, testRegion, iotFWSignalAttrs("Vehicle.Speed", "can0"))
	sigNoIface := upsertTestResource(t, st, "aws", testAccountID, TypeIoTFWDecoderManifestSignal, iotFWTestSignalA2, testRegion, iotFWSignalAttrs("Vehicle.Powertrain.Rpm", ""))

	if err := resolveIoTFWSignalInterfaces(acct, st); err != nil {
		t.Fatalf("resolveIoTFWSignalInterfaces: %v", err)
	}
	rels, err := st.RelationshipsFrom(sigA)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("signal %s edges = %v; want exactly the uses edge to %s", iotFWTestSignalA1, rels, iotFWTestIfaceA1)
	}
	assertRelationship(t, rels, sigA, ifaceA, store.RelUses)
	for _, id := range []string{sigB, sigNoIface} {
		rels, err := st.RelationshipsFrom(id)
		if err != nil {
			t.Fatalf("RelationshipsFrom: %v", err)
		}
		if len(rels) != 0 {
			t.Errorf("signal %s edges = %v; want none", id, rels)
		}
	}
}

func TestResolveIoTFWSignalInterfaces_Errors(t *testing.T) {
	cases := []struct {
		name, nativeID, attrs string
	}{
		{name: "NativeID without signal segment", nativeID: iotFWTestManifestA + "/Vehicle.Speed", attrs: iotFWSignalAttrs("Vehicle.Speed", "can0")},
		{name: "undecodable attributes", nativeID: iotFWTestSignalA1, attrs: `{"InterfaceId": 7}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			upsertTestResource(t, st, "aws", testAccountID, TypeIoTFWDecoderManifestSignal, tc.nativeID, testRegion, tc.attrs)
			if err := resolveIoTFWSignalInterfaces(newTestAccount(testAccountID), st); err == nil {
				t.Errorf("resolveIoTFWSignalInterfaces(%s) = nil; want an error", tc.nativeID)
			}
		})
	}
}
