package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	directconnecttypes "github.com/aws/aws-sdk-go-v2/service/directconnect/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveDXLagMembers,
		EdgeDecl{TypeDirectConnectConnection, TypeDirectConnectLag, store.RelAttachedTo},
		EdgeDecl{TypeDirectConnectInterconnect, TypeDirectConnectLag, store.RelAttachedTo},
	)
	registerResolver(
		resolveDXResiliencyGroupConnections,
		EdgeDecl{TypeDirectConnectResiliencyGroup, TypeDirectConnectConnection, store.RelUses},
	)
	registerResolver(
		resolveDXVIRefs,
		EdgeDecl{TypeDirectConnectPrivateVirtualInterface, TypeDirectConnectConnection, store.RelAttachedTo},
		EdgeDecl{TypeDirectConnectPublicVirtualInterface, TypeDirectConnectConnection, store.RelAttachedTo},
		EdgeDecl{TypeDirectConnectTransitVirtualInterface, TypeDirectConnectConnection, store.RelAttachedTo},
		EdgeDecl{TypeDirectConnectPrivateVirtualInterface, TypeDirectConnectLag, store.RelAttachedTo},
		EdgeDecl{TypeDirectConnectPublicVirtualInterface, TypeDirectConnectLag, store.RelAttachedTo},
		EdgeDecl{TypeDirectConnectTransitVirtualInterface, TypeDirectConnectLag, store.RelAttachedTo},
		EdgeDecl{TypeDirectConnectPrivateVirtualInterface, TypeDirectConnectDirectConnectGateway, store.RelUses},
		EdgeDecl{TypeDirectConnectTransitVirtualInterface, TypeDirectConnectDirectConnectGateway, store.RelUses},
		EdgeDecl{TypeDirectConnectPrivateVirtualInterface, TypeEC2VPNGateway, store.RelAttachedTo},
	)
	registerResolver(
		resolveDXGatewayAssociationRefs,
		EdgeDecl{TypeDirectConnectDirectConnectGatewayAssociation, TypeDirectConnectDirectConnectGateway, store.RelAttachedTo},
		EdgeDecl{TypeDirectConnectDirectConnectGatewayAssociation, TypeEC2VPNGateway, store.RelUses},
		EdgeDecl{TypeDirectConnectDirectConnectGatewayAssociation, TypeEC2TransitGateway, store.RelUses},
		EdgeDecl{TypeDirectConnectDirectConnectGatewayAssociation, TypeIAMAccount, store.RelUses},
	)
}

func dxConnectionARN(region, acct, id string) string {
	return fmt.Sprintf("arn:aws:directconnect:%s:%s:dxcon/%s", region, acct, id)
}

func dxLagARN(region, acct, id string) string {
	return fmt.Sprintf("arn:aws:directconnect:%s:%s:dxlag/%s", region, acct, id)
}

// dxInterconnectARN is the connection ARN: the Service Reference gives
// interconnects no resource type of their own, and their ids are dxcon- ids.
// This assumes DescribeConnections never lists an interconnect in the account
// that owns it; if it did, both rows would share one NativeID and the store's
// warning about a NativeID that maps to two types would fire.
func dxInterconnectARN(region, acct, id string) string {
	return dxConnectionARN(region, acct, id)
}

func dxGatewayARN(acct, id string) string {
	return fmt.Sprintf("arn:aws:directconnect::%s:dx-gateway/%s", acct, id)
}

// resolveDXLagMembers wires each connection and interconnect to its LAG via
// the `LagId` attr.
func resolveDXLagMembers(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Limit: util.AllResources,
		Types: []string{TypeDirectConnectConnection, TypeDirectConnectInterconnect},
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	lagSet, err := scannedIDSet(acct, st, TypeDirectConnectLag)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			LagID *string `json:"LagId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s: %w", r.Type, r.NativeID, err))
			continue
		}
		lagID := sv(attrs.LagID)
		if lagID == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, dxLagARN(sv(r.Region), acct.ID, lagID))
		if !lagSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return errors.Join(append(decodeErrs, fmt.Errorf("upsert dx %s→lag: %w", r.Type, err))...)
		}
	}
	return errors.Join(decodeErrs...)
}

// resolveDXResiliencyGroupConnections wires each resiliency group to the
// connections its embedded associations name, except those already
// disassociated.
func resolveDXResiliencyGroupConnections(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDirectConnectResiliencyGroup}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	connSet, err := scannedIDSet(acct, st, TypeDirectConnectConnection)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			Associations []struct {
				ConnectionArn *string `json:"ConnectionArn"`
				State         string  `json:"State"`
			} `json:"Associations"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s: %w", r.Type, r.NativeID, err))
			continue
		}
		for _, a := range attrs.Associations {
			arn := sv(a.ConnectionArn)
			if arn == "" || a.State == string(directconnecttypes.ResiliencyGroupAssociationStateDisassociated) {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, arn)
			if !connSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return errors.Join(append(decodeErrs, fmt.Errorf("upsert dx resiliency-group→connection: %w", err))...)
			}
		}
	}
	return errors.Join(decodeErrs...)
}

// resolveDXVIRefs wires each virtual interface to its Connection or LAG (both
// share `ConnectionID` but differ in ARN shape — check both target sets), to
// the DX gateway it terminates on (`DirectConnectGatewayID`, private + transit
// only), and for private VIs to the VPN gateway via `VirtualGatewayID`.
func resolveDXVIRefs(acct *account, st *store.Store) error {
	viTypes := []string{
		TypeDirectConnectPrivateVirtualInterface,
		TypeDirectConnectPublicVirtualInterface,
		TypeDirectConnectTransitVirtualInterface,
	}
	connSet, err := scannedIDSet(acct, st, TypeDirectConnectConnection)
	if err != nil {
		return err
	}
	lagSet, err := scannedIDSet(acct, st, TypeDirectConnectLag)
	if err != nil {
		return err
	}
	dxgwSet, err := scannedIDSet(acct, st, TypeDirectConnectDirectConnectGateway)
	if err != nil {
		return err
	}
	vgwSet, err := scannedIDSet(acct, st, TypeEC2VPNGateway)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, vt := range viTypes {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{vt}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				ConnectionID           *string `json:"ConnectionId"`
				DirectConnectGatewayID *string `json:"DirectConnectGatewayId"`
				VirtualGatewayID       *string `json:"VirtualGatewayId"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s: %w", r.Type, r.NativeID, err))
				continue
			}
			region := sv(r.Region)
			if cid := sv(attrs.ConnectionID); cid != "" {
				// VI's ConnectionID may point to a Connection (`dxcon-...`) or LAG (`dxlag-...`).
				if strings.HasPrefix(cid, "dxlag") {
					tgtID := store.ResourceID("aws", acct.ID, dxLagARN(region, acct.ID, cid))
					if lagSet[tgtID] {
						if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
							return errors.Join(append(decodeErrs, fmt.Errorf("upsert dx vi→lag: %w", err))...)
						}
					}
				} else {
					tgtID := store.ResourceID("aws", acct.ID, dxConnectionARN(region, acct.ID, cid))
					if connSet[tgtID] {
						if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
							return errors.Join(append(decodeErrs, fmt.Errorf("upsert dx vi→connection: %w", err))...)
						}
					}
				}
			}
			if gw := sv(attrs.DirectConnectGatewayID); gw != "" {
				tgtID := store.ResourceID("aws", acct.ID, dxGatewayARN(acct.ID, gw))
				if dxgwSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return errors.Join(append(decodeErrs, fmt.Errorf("upsert dx vi→dxgw: %w", err))...)
					}
				}
			}
			if vgw := sv(attrs.VirtualGatewayID); vgw != "" {
				tgtID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "vpn-gateway", vgw))
				if vgwSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return errors.Join(append(decodeErrs, fmt.Errorf("upsert dx vi→vgw: %w", err))...)
					}
				}
			}
		}
	}
	return errors.Join(decodeErrs...)
}

// dxAssociatedGateway is the gateway side of a DX gateway association.
type dxAssociatedGateway struct {
	ID           *string `json:"Id"`
	Type         string  `json:"Type"`
	Region       *string `json:"Region"`
	OwnerAccount *string `json:"OwnerAccount"`
}

// resolveDXGatewayAssociationRefs wires gateway-association rows to the parent
// DX gateway (via `DirectConnectGatewayID`) and to the associated VPN-gateway /
// TGW captured in `AssociatedGateway`. A gateway another account owns is not
// stored under the scanned account, so the association is linked to that
// owner's aws:iam:account self-node instead, inserting a reference-only
// placeholder when the owner has no row.
func resolveDXGatewayAssociationRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDirectConnectDirectConnectGatewayAssociation}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	dxgwSet, err := scannedIDSet(acct, st, TypeDirectConnectDirectConnectGateway)
	if err != nil {
		return err
	}
	vgwSet, err := scannedIDSet(acct, st, TypeEC2VPNGateway)
	if err != nil {
		return err
	}
	tgwSet, err := scannedIDSet(acct, st, TypeEC2TransitGateway)
	if err != nil {
		return err
	}
	type ownerEdge struct{ fromID, owner string }
	var (
		decodeErrs []error
		ownerEdges []ownerEdge
	)
	for _, r := range rows {
		var attrs struct {
			DirectConnectGatewayID *string              `json:"DirectConnectGatewayId"`
			AssociatedGateway      *dxAssociatedGateway `json:"AssociatedGateway"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s: %w", r.Type, r.NativeID, err))
			continue
		}
		if gw := sv(attrs.DirectConnectGatewayID); gw != "" {
			tgtID := store.ResourceID("aws", acct.ID, dxGatewayARN(acct.ID, gw))
			if dxgwSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return errors.Join(append(decodeErrs, fmt.Errorf("upsert dx assoc→dxgw: %w", err))...)
				}
			}
		}
		ag := attrs.AssociatedGateway
		if ag == nil || sv(ag.ID) == "" {
			continue
		}
		if owner := sv(ag.OwnerAccount); owner != "" && owner != acct.ID {
			ownerEdges = append(ownerEdges, ownerEdge{fromID: r.ID, owner: owner})
			continue
		}
		var tgtID string
		switch ag.Type {
		case "virtualPrivateGateway":
			if id := store.ResourceID("aws", acct.ID, ec2ARN(sv(ag.Region), acct.ID, "vpn-gateway", sv(ag.ID))); vgwSet[id] {
				tgtID = id
			}
		case "transitGateway":
			if id := store.ResourceID("aws", acct.ID, ec2ARN(sv(ag.Region), acct.ID, "transit-gateway", sv(ag.ID))); tgwSet[id] {
				tgtID = id
			}
		}
		if tgtID == "" {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return errors.Join(append(decodeErrs, fmt.Errorf("upsert dx assoc→%s: %w", ag.Type, err))...)
		}
	}
	if len(ownerEdges) == 0 {
		return errors.Join(decodeErrs...)
	}
	owners := map[string]bool{}
	var placeholders []*store.Resource
	for _, e := range ownerEdges {
		if !owners[e.owner] {
			owners[e.owner] = true
			placeholders = append(placeholders, iamAccountPlaceholder(e.owner, rows[0].DiscoveredBy))
		}
	}
	if _, err := st.InsertResourcesIfAbsent(placeholders); err != nil {
		return errors.Join(append(decodeErrs, fmt.Errorf("insert dx associated-gateway owner account placeholders: %w", err))...)
	}
	edgeAttrs := mustJSON(map[string]string{"role": "associated-gateway-owner"})
	for _, e := range ownerEdges {
		toID := store.ResourceID("aws", e.owner, iamAccountNativeID(e.owner))
		if err := st.UpsertRelationship(e.fromID, toID, store.RelUses, "directed", &edgeAttrs); err != nil {
			return errors.Join(append(decodeErrs, fmt.Errorf("upsert dx assoc→owner account: %w", err))...)
		}
	}
	return errors.Join(decodeErrs...)
}
