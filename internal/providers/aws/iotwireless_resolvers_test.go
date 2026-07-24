package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveIoTWirelessDestinationRole(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/iotw-role", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	dARN := fmt.Sprintf("arn:aws:iotwireless:%s:%s:Destination/myDest", testRegion, acct.ID)
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTWirelessDestination, dARN, testRegion,
		fmt.Sprintf(`{"Name":"myDest","RoleArn":"%s"}`, roleARN))
	if err := resolveIoTWirelessDestinationRole(acct, st); err != nil {
		t.Fatalf("resolveIoTWirelessDestinationRole: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dID)
	assertRelationship(t, rels, dID, roleID, store.RelUses)
}

func TestResolveIoTWirelessDeviceToDestination(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dARN := fmt.Sprintf("arn:aws:iotwireless:%s:%s:Destination/myDest", testRegion, acct.ID)
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTWirelessDestination, dARN, testRegion,
		`{"Name":"myDest"}`)
	wdARN := fmt.Sprintf("arn:aws:iotwireless:%s:%s:WirelessDevice/dev-1", testRegion, acct.ID)
	wdID := upsertTestResource(t, st, "aws", acct.ID, TypeIoTWirelessWirelessDevice, wdARN, testRegion,
		`{"DestinationName":"myDest"}`)
	if err := resolveIoTWirelessDeviceToDestination(acct, st); err != nil {
		t.Fatalf("resolveIoTWirelessDeviceToDestination: %v", err)
	}
	rels, _ := st.RelationshipsFrom(wdID)
	assertRelationship(t, rels, wdID, dID, store.RelAttachedTo)
}
