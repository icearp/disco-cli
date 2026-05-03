package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveVPNConnectionRelationships,
		EdgeDecl{TypeEC2VPNConnection, TypeEC2VPNGateway, store.RelAttachedTo},
		EdgeDecl{TypeEC2VPNConnection, TypeEC2CustomerGateway, store.RelAttachedTo},
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
