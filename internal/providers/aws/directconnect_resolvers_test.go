package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	directconnecttypes "github.com/aws/aws-sdk-go-v2/service/directconnect/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveDXLagMembers_Interconnect(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	lagID := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectLag, dxLagARN(testRegion, acct.ID, "dxlag-aaa"), testRegion, "{}")
	attrs, err := json.Marshal(directconnecttypes.Interconnect{InterconnectId: sdkaws.String("dxcon-ic1"), LagId: sdkaws.String("dxlag-aaa")})
	if err != nil {
		t.Fatal(err)
	}
	icID := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectInterconnect, dxInterconnectARN(testRegion, acct.ID, "dxcon-ic1"), testRegion, string(attrs))
	bareID := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectInterconnect, dxInterconnectARN(testRegion, acct.ID, "dxcon-ic2"), testRegion, "{}")
	if err := resolveDXLagMembers(acct, st); err != nil {
		t.Fatalf("resolveDXLagMembers: %v", err)
	}
	rels, _ := st.RelationshipsFrom(icID)
	assertRelationship(t, rels, icID, lagID, store.RelAttachedTo)
	if rels, _ := st.RelationshipsFrom(bareID); len(rels) != 0 {
		t.Errorf("interconnect without LagId has edges %v; want none", rels)
	}
}

func TestResolveDXLagMembers_UndecodableAttrsFail(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectInterconnect, dxInterconnectARN(testRegion, acct.ID, "dxcon-ic1"), testRegion, `{"LagId":7}`)
	var typeErr *json.UnmarshalTypeError
	if err := resolveDXLagMembers(acct, st); !errors.As(err, &typeErr) {
		t.Errorf("resolveDXLagMembers = %v; want the decode error", err)
	}
}

func TestResolveDXResiliencyGroupConnections(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	conn1 := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectConnection, dxConnectionARN(testRegion, acct.ID, "dxcon-1"), testRegion, "{}")
	conn2 := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectConnection, dxConnectionARN(testRegion, acct.ID, "dxcon-2"), testRegion, "{}")
	assoc := func(id string, state directconnecttypes.ResiliencyGroupAssociationState) directconnecttypes.ResiliencyGroupAssociation {
		return directconnecttypes.ResiliencyGroupAssociation{ConnectionArn: sdkaws.String(dxConnectionARN(testRegion, acct.ID, id)), State: state}
	}
	attrs, err := json.Marshal(dxResiliencyGroupAttrs{
		ResiliencyGroup: directconnecttypes.ResiliencyGroup{ResiliencyGroupId: sdkaws.String("rg-a")},
		Associations: []directconnecttypes.ResiliencyGroupAssociation{
			assoc("dxcon-1", directconnecttypes.ResiliencyGroupAssociationStateAssociated),
			assoc("dxcon-2", directconnecttypes.ResiliencyGroupAssociationStateDisassociated),
			assoc("dxcon-unscanned", directconnecttypes.ResiliencyGroupAssociationStateAssociated),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	groupID := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectResiliencyGroup, dxTestGroupA, testRegion, string(attrs))
	bareID := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectResiliencyGroup, dxTestGroupB, testRegion, "{}")
	if err := resolveDXResiliencyGroupConnections(acct, st); err != nil {
		t.Fatalf("resolveDXResiliencyGroupConnections: %v", err)
	}
	rels, _ := st.RelationshipsFrom(groupID)
	assertRelationship(t, rels, groupID, conn1, store.RelUses)
	if len(rels) != 1 {
		t.Errorf("group edges = %v; want only the one to dxcon-1 (dxcon-2 disassociated %s, dxcon-unscanned not scanned)", rels, conn2)
	}
	if rels, _ := st.RelationshipsFrom(bareID); len(rels) != 0 {
		t.Errorf("group without associations has edges %v; want none", rels)
	}
}

func TestResolveDXLagMembers_Connection(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	lagARN := dxLagARN(testRegion, acct.ID, "dxlag-aaa")
	lagID := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectLag, lagARN, testRegion, "{}")
	connARN := dxConnectionARN(testRegion, acct.ID, "dxcon-bbb")
	connID := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectConnection, connARN, testRegion, `{"LagId":"dxlag-aaa"}`)
	if err := resolveDXLagMembers(acct, st); err != nil {
		t.Fatalf("resolveDXLagMembers: %v", err)
	}
	rels, _ := st.RelationshipsFrom(connID)
	assertRelationship(t, rels, connID, lagID, store.RelAttachedTo)
}

func TestResolveDXVIRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	connARN := dxConnectionARN(testRegion, acct.ID, "dxcon-bbb")
	connID := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectConnection, connARN, testRegion, "{}")
	dxgwARN := dxGatewayARN(acct.ID, "abc-1234")
	dxgwID := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectDirectConnectGateway, dxgwARN, testRegion, "{}")
	vgwARN := ec2ARN(testRegion, acct.ID, "vpn-gateway", "vgw-1")
	vgwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPNGateway, vgwARN, testRegion, "{}")

	pvifARN := fmt.Sprintf("arn:aws:directconnect:%s:%s:dxvif/dxvif-priv", testRegion, acct.ID)
	pvifAttrs := `{"ConnectionId":"dxcon-bbb","DirectConnectGatewayId":"abc-1234","VirtualGatewayId":"vgw-1"}`
	pvifID := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectPrivateVirtualInterface, pvifARN, testRegion, pvifAttrs)

	if err := resolveDXVIRefs(acct, st); err != nil {
		t.Fatalf("resolveDXVIRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pvifID)
	assertRelationship(t, rels, pvifID, connID, store.RelAttachedTo)
	assertRelationship(t, rels, pvifID, dxgwID, store.RelUses)
	assertRelationship(t, rels, pvifID, vgwID, store.RelAttachedTo)
}

func TestResolveDXGatewayAssociationRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dxgwARN := dxGatewayARN(acct.ID, "g-1")
	dxgwID := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectDirectConnectGateway, dxgwARN, testRegion, "{}")
	tgwARN := ec2ARN(testRegion, acct.ID, "transit-gateway", "tgw-1")
	tgwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGateway, tgwARN, testRegion, "{}")

	assocARN := fmt.Sprintf("arn:aws:directconnect::%s:dx-gateway/g-1/association/a-1", acct.ID)
	attrs := fmt.Sprintf(`{"DirectConnectGatewayId":"g-1","AssociatedGateway":{"Id":"tgw-1","Type":"transitGateway","Region":"%s","OwnerAccount":"%s"}}`, testRegion, acct.ID)
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectDirectConnectGatewayAssociation, assocARN, testRegion, attrs)
	if err := resolveDXGatewayAssociationRefs(acct, st); err != nil {
		t.Fatalf("resolveDXGatewayAssociationRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	assertRelationship(t, rels, assocID, dxgwID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, tgwID, store.RelUses)
}
