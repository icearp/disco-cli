package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveDXConnectionToLag,
		EdgeDecl{TypeDirectConnectConnection, TypeDirectConnectLag, store.RelAttachedTo},
	)
	registerResolver(resolveDXVIRefs,
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
	registerResolver(resolveDXGatewayAssociationRefs,
		EdgeDecl{TypeDirectConnectDirectConnectGatewayAssociation, TypeDirectConnectDirectConnectGateway, store.RelAttachedTo},
		EdgeDecl{TypeDirectConnectDirectConnectGatewayAssociation, TypeEC2VPNGateway, store.RelUses},
		EdgeDecl{TypeDirectConnectDirectConnectGatewayAssociation, TypeEC2TransitGateway, store.RelUses},
	)
}

func dxConnectionARN(region, acct, id string) string {
	return fmt.Sprintf("arn:aws:directconnect:%s:%s:dxcon/%s", region, acct, id)
}

func dxLagARN(region, acct, id string) string {
	return fmt.Sprintf("arn:aws:directconnect:%s:%s:dxlag/%s", region, acct, id)
}

func dxGatewayARN(acct, id string) string {
	return fmt.Sprintf("arn:aws:directconnect::%s:dx-gateway/%s", acct, id)
}

// resolveDXConnectionToLag wires Connection → LAG via the `LagId` attr.
func resolveDXConnectionToLag(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDirectConnectConnection}, Limit: util.AllResources,
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
	for _, r := range rows {
		var attrs struct {
			LagId *string `json:"LagId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		lagID := sv(attrs.LagId)
		if lagID == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeDirectConnectLag, dxLagARN(sv(r.Region), acct.ID, lagID))
		if !lagSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert dx connection→lag: %w", err)
		}
	}
	return nil
}

// resolveDXVIRefs wires each virtual interface to its underlying Connection or
// LAG (via `ConnectionId` — DX connections and LAGs share the same field but
// distinct ARN shapes; check both target sets), to the DX gateway it terminates
// on (`DirectConnectGatewayId`, private + transit only), and — for private VIs
// — to the VPN gateway via `VirtualGatewayId`.
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
	for _, vt := range viTypes {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{vt}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				ConnectionId           *string `json:"ConnectionId"`
				DirectConnectGatewayId *string `json:"DirectConnectGatewayId"`
				VirtualGatewayId       *string `json:"VirtualGatewayId"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			region := sv(r.Region)
			if cid := sv(attrs.ConnectionId); cid != "" {
				// VI's ConnectionId may point to a Connection (`dxcon-...`) or LAG (`dxlag-...`).
				if strings.HasPrefix(cid, "dxlag") {
					tgtID := store.ResourceID("aws", acct.ID, TypeDirectConnectLag, dxLagARN(region, acct.ID, cid))
					if lagSet[tgtID] {
						if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
							return fmt.Errorf("upsert dx vi→lag: %w", err)
						}
					}
				} else {
					tgtID := store.ResourceID("aws", acct.ID, TypeDirectConnectConnection, dxConnectionARN(region, acct.ID, cid))
					if connSet[tgtID] {
						if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
							return fmt.Errorf("upsert dx vi→connection: %w", err)
						}
					}
				}
			}
			if gw := sv(attrs.DirectConnectGatewayId); gw != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeDirectConnectDirectConnectGateway, dxGatewayARN(acct.ID, gw))
				if dxgwSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert dx vi→dxgw: %w", err)
					}
				}
			}
			if vgw := sv(attrs.VirtualGatewayId); vgw != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeEC2VPNGateway, ec2ARN(region, acct.ID, "vpn-gateway", vgw))
				if vgwSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert dx vi→vgw: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// resolveDXGatewayAssociationRefs wires gateway-association rows to the parent
// DX gateway (via `DirectConnectGatewayId`) and to the associated VPN-gateway /
// TGW captured in `AssociatedGateway`.
func resolveDXGatewayAssociationRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDirectConnectDirectConnectGatewayAssociation}, Limit: util.AllResources,
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
	for _, r := range rows {
		var attrs struct {
			DirectConnectGatewayId *string `json:"DirectConnectGatewayId"`
			AssociatedGateway      *struct {
				Id           *string `json:"Id"`
				Type         string  `json:"Type"`
				Region       *string `json:"Region"`
				OwnerAccount *string `json:"OwnerAccount"`
			} `json:"AssociatedGateway"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if gw := sv(attrs.DirectConnectGatewayId); gw != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeDirectConnectDirectConnectGateway, dxGatewayARN(acct.ID, gw))
			if dxgwSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert dx assoc→dxgw: %w", err)
				}
			}
		}
		if attrs.AssociatedGateway != nil {
			ag := attrs.AssociatedGateway
			gid := sv(ag.Id)
			if gid == "" {
				continue
			}
			gwRegion := sv(ag.Region)
			gwAcct := sv(ag.OwnerAccount)
			if gwAcct == "" {
				gwAcct = acct.ID
			}
			switch ag.Type {
			case "virtualPrivateGateway":
				tgtID := store.ResourceID("aws", acct.ID, TypeEC2VPNGateway, ec2ARN(gwRegion, gwAcct, "vpn-gateway", gid))
				if vgwSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert dx assoc→vgw: %w", err)
					}
				}
			case "transitGateway":
				tgtID := store.ResourceID("aws", acct.ID, TypeEC2TransitGateway, ec2ARN(gwRegion, gwAcct, "transit-gateway", gid))
				if tgwSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert dx assoc→tgw: %w", err)
					}
				}
			}
		}
	}
	return nil
}
