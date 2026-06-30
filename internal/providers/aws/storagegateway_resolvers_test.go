package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func sgwGatewayARN(region, acct, id string) string {
	return fmt.Sprintf("arn:aws:storagegateway:%s:%s:gateway/%s", region, acct, id)
}

func TestResolveStorageGatewayChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	gwARN := sgwGatewayARN(testRegion, acct.ID, "sgw-11AA22BB")
	gwID := upsertTestResource(t, st, "aws", acct.ID, TypeStorageGatewayGateway, gwARN, testRegion, "{}")

	volARN := fmt.Sprintf("%s/volume/vol-1122334455667788", gwARN)
	volAttrs := fmt.Sprintf(`{"VolumeARN":%q,"GatewayARN":%q}`, volARN, gwARN)
	volID := upsertTestResource(t, st, "aws", acct.ID, TypeStorageGatewayVolume, volARN, testRegion, volAttrs)

	shareARN := fmt.Sprintf("arn:aws:storagegateway:%s:%s:share/share-ABCDEF", testRegion, acct.ID)
	shareAttrs := fmt.Sprintf(`{"FileShareARN":%q,"GatewayARN":%q}`, shareARN, gwARN)
	shareID := upsertTestResource(t, st, "aws", acct.ID, TypeStorageGatewayShare, shareARN, testRegion, shareAttrs)

	if err := resolveStorageGatewayChildren(acct, st); err != nil {
		t.Fatalf("resolveStorageGatewayChildren: %v", err)
	}
	relsVol, _ := st.RelationshipsFrom(volID)
	assertRelationship(t, relsVol, volID, gwID, store.RelAttachedTo)
	relsShare, _ := st.RelationshipsFrom(shareID)
	assertRelationship(t, relsShare, shareID, gwID, store.RelAttachedTo)
}

func TestResolveStorageGatewayChildren_NoGatewayARN(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	gwARN := sgwGatewayARN(testRegion, acct.ID, "sgw-11AA22BB")
	upsertTestResource(t, st, "aws", acct.ID, TypeStorageGatewayGateway, gwARN, testRegion, "{}")

	// Archived tape: empty GatewayARN must yield no edge (and no panic on
	// missing attrs).
	tapeARN := fmt.Sprintf("arn:aws:storagegateway:%s:%s:tape/TEST00001", testRegion, acct.ID)
	tapeID := upsertTestResource(t, st, "aws", acct.ID, TypeStorageGatewayTape, tapeARN, testRegion, "{}")

	if err := resolveStorageGatewayChildren(acct, st); err != nil {
		t.Fatalf("resolveStorageGatewayChildren: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(tapeID); len(rels) != 0 {
		t.Errorf("expected no edge for archived tape, got %d", len(rels))
	}
}

func TestResolveStorageGatewayDevices(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	gwARN := sgwGatewayARN(testRegion, acct.ID, "sgw-11AA22BB")
	gwID := upsertTestResource(t, st, "aws", acct.ID, TypeStorageGatewayGateway, gwARN, testRegion, "{}")

	devARN := fmt.Sprintf("%s/device/AMZN_SGW-11AA22BB_TAPEDRIVE_00010", gwARN)
	devID := upsertTestResource(t, st, "aws", acct.ID, TypeStorageGatewayDevice, devARN, testRegion, "{}")

	if err := resolveStorageGatewayDevices(acct, st); err != nil {
		t.Fatalf("resolveStorageGatewayDevices: %v", err)
	}
	rels, _ := st.RelationshipsFrom(devID)
	assertRelationship(t, rels, devID, gwID, store.RelAttachedTo)
}

func TestResolveStorageGatewayDevices_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	// No gateways scanned — resolver short-circuits, no error.
	if err := resolveStorageGatewayDevices(acct, st); err != nil {
		t.Fatalf("resolveStorageGatewayDevices empty: %v", err)
	}
}
