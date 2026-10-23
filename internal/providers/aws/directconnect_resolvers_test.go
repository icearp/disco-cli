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

// A row whose attributes do not decode fails the resolver, but only after the
// rows named before and after it (ListResources orders by name) got their
// edges.
func TestResolveDX_UndecodableAttrsKeepOtherRows(t *testing.T) {
	acct := newTestAccount(testAccountID)
	for _, tc := range []struct {
		name    string
		resolve func(*account, *store.Store) error
		typ     string
		kind    string
		// seed stores the edge target and returns its id with the source's
		// attributes and NativeID builder.
		seed func(t *testing.T, st *store.Store) (target, attrs string, nativeID func(string) string)
	}{
		{
			name: "lag members", resolve: resolveDXLagMembers, typ: TypeDirectConnectConnection, kind: store.RelAttachedTo,
			seed: func(t *testing.T, st *store.Store) (string, string, func(string) string) {
				lag := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectLag, dxLagARN(testRegion, acct.ID, "dxlag-1"), testRegion, "{}")
				return lag, mustJSON(directconnecttypes.Connection{LagId: sdkaws.String("dxlag-1")}),
					func(id string) string { return dxConnectionARN(testRegion, acct.ID, id) }
			},
		},
		{
			name: "resiliency group connections", resolve: resolveDXResiliencyGroupConnections, typ: TypeDirectConnectResiliencyGroup, kind: store.RelUses,
			seed: func(t *testing.T, st *store.Store) (string, string, func(string) string) {
				conn := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectConnection, dxTestConn1, testRegion, "{}")
				return conn, mustJSON(dxResiliencyGroupAttrs{Associations: []directconnecttypes.ResiliencyGroupAssociation{{ConnectionArn: sdkaws.String(dxTestConn1)}}}),
					func(id string) string { return "arn:aws:directconnect::123456789012:dx-resiliency-group/" + id }
			},
		},
		{
			name: "virtual interfaces", resolve: resolveDXVIRefs, typ: TypeDirectConnectPrivateVirtualInterface, kind: store.RelAttachedTo,
			seed: func(t *testing.T, st *store.Store) (string, string, func(string) string) {
				conn := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectConnection, dxTestConn1, testRegion, "{}")
				return conn, mustJSON(directconnecttypes.VirtualInterface{ConnectionId: sdkaws.String("dxcon-1")}),
					func(id string) string { return "arn:aws:directconnect:us-east-1:123456789012:dxvif/" + id }
			},
		},
		{
			name: "gateway associations", resolve: resolveDXGatewayAssociationRefs, typ: TypeDirectConnectDirectConnectGatewayAssociation, kind: store.RelAttachedTo,
			seed: func(t *testing.T, st *store.Store) (string, string, func(string) string) {
				gw := upsertTestResource(t, st, "aws", acct.ID, TypeDirectConnectDirectConnectGateway, dxGatewayARN(acct.ID, "g-1"), testRegion, "{}")
				return gw, mustJSON(directconnecttypes.DirectConnectGatewayAssociation{DirectConnectGatewayId: sdkaws.String("g-1")}),
					func(id string) string { return dxGatewayARN(acct.ID, "g-1") + "/association/" + id }
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			target, attrs, nativeID := tc.seed(t, st)
			first := upsertTestResourceNamed(t, st, tc.typ, nativeID("a"), testRegion, attrs, "a-good")
			upsertTestResourceNamed(t, st, tc.typ, nativeID("b"), testRegion, `{"LagId":7,"Associations":7,"ConnectionId":7,"DirectConnectGatewayId":7}`, "b-bad")
			last := upsertTestResourceNamed(t, st, tc.typ, nativeID("c"), testRegion, attrs, "c-good")
			var typeErr *json.UnmarshalTypeError
			if err := tc.resolve(acct, st); !errors.As(err, &typeErr) {
				t.Errorf("resolve = %v; want the decode error", err)
			}
			for _, id := range []string{first, last} {
				rels, err := st.RelationshipsFrom(id)
				if err != nil {
					t.Fatal(err)
				}
				assertRelationship(t, rels, id, target, tc.kind)
				if len(rels) != 1 {
					t.Errorf("edges of %s = %v; want only the one to %s", id, rels, target)
				}
			}
		})
	}
}

// An association whose gateway another account owns is linked to that
// account's self-node, inserted as a placeholder; one owned by the scanned
// account is linked to the gateway itself and not to an account.
func TestResolveDXGatewayAssociationRefs_CrossAccountGatewayOwner(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	const other = "210987654321"
	vgwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPNGateway, ec2ARN(testRegion, acct.ID, "vpn-gateway", "vgw-1"), testRegion, "{}")
	assoc := func(id, owner string) string {
		attrs := mustJSON(directconnecttypes.DirectConnectGatewayAssociation{AssociatedGateway: &directconnecttypes.AssociatedGateway{
			Id: sdkaws.String("vgw-1"), Type: directconnecttypes.GatewayTypeVirtualPrivateGateway, Region: sdkaws.String(testRegion), OwnerAccount: sdkaws.String(owner),
		}})
		return upsertTestResourceNamed(t, st, TypeDirectConnectDirectConnectGatewayAssociation, dxGatewayARN(acct.ID, "g-1")+"/association/"+id, testRegion, attrs, id)
	}
	crossID := assoc("a-cross", other)
	// Named between the two, so the owner edge is written past a row that
	// does not decode, whose error is still returned.
	upsertTestResourceNamed(t, st, TypeDirectConnectDirectConnectGatewayAssociation, dxGatewayARN(acct.ID, "g-1")+"/association/a-bad", testRegion, `{"AssociatedGateway":7}`, "a-bad")
	ownID := assoc("a-own", acct.ID)
	var typeErr *json.UnmarshalTypeError
	if err := resolveDXGatewayAssociationRefs(acct, st); !errors.As(err, &typeErr) {
		t.Errorf("resolveDXGatewayAssociationRefs = %v; want the decode error", err)
	}
	ownerID := store.ResourceID("aws", other, iamAccountNativeID(other))
	rels, err := st.RelationshipsFrom(crossID)
	if err != nil {
		t.Fatal(err)
	}
	assertRelationship(t, rels, crossID, ownerID, store.RelUses)
	if len(rels) != 1 {
		t.Fatalf("cross-account association edges = %v; want only the owner account", rels)
	}
	var edgeAttrs map[string]string
	if rels[0].Attributes == nil || json.Unmarshal([]byte(*rels[0].Attributes), &edgeAttrs) != nil || edgeAttrs["role"] != "associated-gateway-owner" {
		t.Errorf("owner edge attributes = %v; want role associated-gateway-owner", rels[0].Attributes)
	}
	accounts, err := st.ListResources(store.ResourceFilter{Types: []string{TypeIAMAccount}, Limit: 10})
	if err != nil || len(accounts) != 1 || accounts[0].ID != ownerID {
		t.Errorf("account rows = %v (err %v); want only the placeholder for %s", accounts, err, other)
	}
	rels, err = st.RelationshipsFrom(ownID)
	if err != nil {
		t.Fatal(err)
	}
	assertRelationship(t, rels, ownID, vgwID, store.RelUses)
	if len(rels) != 1 {
		t.Errorf("own association edges = %v; want only the VPN gateway", rels)
	}
}

// A failed edge or placeholder write returns the store write error together
// with the decode errors collected before it, so neither hides the other.
func TestResolveDX_WriteFailureKeepsDecodeErrors(t *testing.T) {
	acct := newTestAccount(testAccountID)
	const other = "210987654321"
	vgw := ec2ARN(testRegion, acct.ID, "vpn-gateway", "vgw-1")
	tgw := ec2ARN(testRegion, acct.ID, "transit-gateway", "tgw-1")
	vif := func(id string) string { return "arn:aws:directconnect:us-east-1:123456789012:dxvif/" + id }
	assoc := func(id string) string { return dxGatewayARN(acct.ID, "g-1") + "/association/" + id }
	ownGateway := func(id string, typ directconnecttypes.GatewayType) string {
		return mustJSON(directconnecttypes.DirectConnectGatewayAssociation{AssociatedGateway: &directconnecttypes.AssociatedGateway{
			Id: sdkaws.String(id), Type: typ, Region: sdkaws.String(testRegion), OwnerAccount: sdkaws.String(acct.ID),
		}})
	}
	foreign := mustJSON(directconnecttypes.DirectConnectGatewayAssociation{AssociatedGateway: &directconnecttypes.AssociatedGateway{
		Id: sdkaws.String("vgw-9"), Type: directconnecttypes.GatewayTypeVirtualPrivateGateway, OwnerAccount: sdkaws.String(other),
	}})
	refuseEdges := `CREATE TRIGGER refuse_edges BEFORE INSERT ON relationships BEGIN SELECT RAISE(ABORT, 'test: edge write refused'); END`
	refusePlaceholders := `CREATE TRIGGER refuse_accounts BEFORE INSERT ON resources WHEN NEW.type = '` + TypeIAMAccount +
		`' BEGIN SELECT RAISE(ABORT, 'test: placeholder write refused'); END`
	for _, tc := range []struct {
		name     string
		resolve  func(*account, *store.Store) error
		typ      string
		nativeID func(string) string
		target   struct{ typ, nativeID string } // the stored edge target; typ "" for none
		attrs    string                         // the good row's attributes
		trigger  string
	}{
		{
			"lag member", resolveDXLagMembers, TypeDirectConnectConnection, func(id string) string { return dxConnectionARN(testRegion, acct.ID, id) },
			struct{ typ, nativeID string }{TypeDirectConnectLag, dxLagARN(testRegion, acct.ID, "dxlag-1")},
			mustJSON(directconnecttypes.Connection{LagId: sdkaws.String("dxlag-1")}), refuseEdges,
		},
		{
			"resiliency group connection", resolveDXResiliencyGroupConnections, TypeDirectConnectResiliencyGroup,
			func(id string) string { return "arn:aws:directconnect::123456789012:dx-resiliency-group/" + id },
			struct{ typ, nativeID string }{TypeDirectConnectConnection, dxTestConn1},
			mustJSON(dxResiliencyGroupAttrs{Associations: []directconnecttypes.ResiliencyGroupAssociation{{ConnectionArn: sdkaws.String(dxTestConn1)}}}), refuseEdges,
		},
		{
			"vif lag", resolveDXVIRefs, TypeDirectConnectPrivateVirtualInterface, vif,
			struct{ typ, nativeID string }{TypeDirectConnectLag, dxLagARN(testRegion, acct.ID, "dxlag-1")},
			mustJSON(directconnecttypes.VirtualInterface{ConnectionId: sdkaws.String("dxlag-1")}), refuseEdges,
		},
		{
			"vif connection", resolveDXVIRefs, TypeDirectConnectPrivateVirtualInterface, vif,
			struct{ typ, nativeID string }{TypeDirectConnectConnection, dxTestConn1},
			mustJSON(directconnecttypes.VirtualInterface{ConnectionId: sdkaws.String("dxcon-1")}), refuseEdges,
		},
		{
			"vif gateway", resolveDXVIRefs, TypeDirectConnectPrivateVirtualInterface, vif,
			struct{ typ, nativeID string }{TypeDirectConnectDirectConnectGateway, dxGatewayARN(acct.ID, "g-1")},
			mustJSON(directconnecttypes.VirtualInterface{DirectConnectGatewayId: sdkaws.String("g-1")}), refuseEdges,
		},
		{
			"vif vpn gateway", resolveDXVIRefs, TypeDirectConnectPrivateVirtualInterface, vif,
			struct{ typ, nativeID string }{TypeEC2VPNGateway, vgw},
			mustJSON(directconnecttypes.VirtualInterface{VirtualGatewayId: sdkaws.String("vgw-1")}), refuseEdges,
		},
		{
			"association gateway", resolveDXGatewayAssociationRefs, TypeDirectConnectDirectConnectGatewayAssociation, assoc,
			struct{ typ, nativeID string }{TypeDirectConnectDirectConnectGateway, dxGatewayARN(acct.ID, "g-1")},
			mustJSON(directconnecttypes.DirectConnectGatewayAssociation{DirectConnectGatewayId: sdkaws.String("g-1")}), refuseEdges,
		},
		{
			"association transit gateway", resolveDXGatewayAssociationRefs, TypeDirectConnectDirectConnectGatewayAssociation, assoc,
			struct{ typ, nativeID string }{TypeEC2TransitGateway, tgw},
			ownGateway("tgw-1", directconnecttypes.GatewayTypeTransitGateway), refuseEdges,
		},
		{
			"association owner placeholder", resolveDXGatewayAssociationRefs, TypeDirectConnectDirectConnectGatewayAssociation, assoc,
			struct{ typ, nativeID string }{},
			foreign, refusePlaceholders,
		},
		{
			"association owner edge", resolveDXGatewayAssociationRefs, TypeDirectConnectDirectConnectGatewayAssociation, assoc,
			struct{ typ, nativeID string }{},
			foreign, refuseEdges,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			if tc.target.typ != "" {
				upsertTestResource(t, st, "aws", acct.ID, tc.target.typ, tc.target.nativeID, testRegion, "{}")
			}
			// Named first, so its decode error is collected before the write fails.
			upsertTestResourceNamed(t, st, tc.typ, tc.nativeID("a"), testRegion,
				`{"LagId":7,"Associations":7,"ConnectionId":7,"DirectConnectGatewayId":7,"AssociatedGateway":7}`, "a-bad")
			upsertTestResourceNamed(t, st, tc.typ, tc.nativeID("b"), testRegion, tc.attrs, "b-good")
			if _, err := st.DB().Exec(tc.trigger); err != nil {
				t.Fatalf("create trigger: %v", err)
			}
			err := tc.resolve(acct, st)
			var typeErr *json.UnmarshalTypeError
			if !errors.Is(err, store.ErrStoreWrite) || !errors.As(err, &typeErr) {
				t.Errorf("resolve = %v; want both the store write error and the decode error", err)
			}
		})
	}
}
