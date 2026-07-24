package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveDXConnectionToLag(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	lagARN := dxLagARN(testRegion, acct.ID, "dxlag-aaa")
	lagID := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectLag, lagARN, testRegion, "{}")
	connARN := dxConnectionARN(testRegion, acct.ID, "dxcon-bbb")
	connID := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectConnection, connARN, testRegion, `{"LagId":"dxlag-aaa"}`)
	if err := resolveDXConnectionToLag(acct, st); err != nil {
		t.Fatalf("resolveDXConnectionToLag: %v", err)
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
