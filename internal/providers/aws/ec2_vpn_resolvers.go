package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveVPNConnectionRelationships,
		EdgeDecl{TypeEC2VPNConnection, TypeEC2VPNGateway, store.RelAttachedTo},
		EdgeDecl{TypeEC2VPNConnection, TypeEC2CustomerGateway, store.RelAttachedTo},
	)
	registerResolver(
		resolveVPNGatewayRoutePropagations,
		EdgeDecl{TypeEC2VPNGateway, TypeEC2RouteTable, store.RelRoutesTo},
	)
}

func resolveVPNConnectionRelationships(acct *account, st *store.Store) error {
	conns, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2VPNConnection},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range conns {
		var attrs struct {
			VpnGatewayID      *string `json:"VpnGatewayID"`
			CustomerGatewayID *string `json:"CustomerGatewayID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.VpnGatewayID != nil {
			vgwID := store.ResourceID("aws", acct.ID, TypeEC2VPNGateway, ec2ARN(region, acct.ID, "vpn-gateway", *attrs.VpnGatewayID))
			if err := st.UpsertRelationship(r.ID, vgwID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert vpn-connection→vpn-gateway relationship: %w", err)
			}
		}
		if attrs.CustomerGatewayID != nil {
			cgwID := store.ResourceID("aws", acct.ID, TypeEC2CustomerGateway, ec2ARN(region, acct.ID, "customer-gateway", *attrs.CustomerGatewayID))
			if err := st.UpsertRelationship(r.ID, cgwID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert vpn-connection→customer-gateway relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveVPNGatewayRoutePropagations walks each route-table's PropagatingVgws
// list and emits a `routes-to` edge from the VGW to the route-table. The list
// is populated by EnableVgwRoutePropagation; each VGW that propagates BGP
// routes into the table appears as one entry. FK-safe: skips VGWs not in the
// scanned set.
func resolveVPNGatewayRoutePropagations(acct *account, st *store.Store) error {
	rts, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2RouteTable},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	vgwSet, err := scannedIDSet(acct, st, TypeEC2VPNGateway)
	if err != nil {
		return fmt.Errorf("load vpn-gateway id-set: %w", err)
	}
	for _, r := range rts {
		var attrs struct {
			PropagatingVgws []struct {
				GatewayID *string `json:"GatewayId"`
			} `json:"PropagatingVgws"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		for _, p := range attrs.PropagatingVgws {
			if p.GatewayID == nil || *p.GatewayID == "" {
				continue
			}
			vgwID := store.ResourceID("aws", acct.ID, TypeEC2VPNGateway,
				ec2ARN(region, acct.ID, "vpn-gateway", *p.GatewayID))
			if !vgwSet[vgwID] {
				continue
			}
			if err := st.UpsertRelationship(vgwID, r.ID, store.RelRoutesTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert vpn-gateway→route-table propagation: %w", err)
			}
		}
	}
	return nil
}
