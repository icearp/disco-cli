package aws

import (
	"encoding/json"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveClientVPNEndpointRelationships)
	registerResolver(resolveClientVPNTargetNetworkAssociationRelationships)
}

// resolveClientVPNEndpointRelationships links each Client VPN endpoint to its VPC.
func resolveClientVPNEndpointRelationships(acct *account, st *store.Store) error {
	eps, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2ClientVPNEndpoint},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range eps {
		var attrs struct {
			VpcId *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC,
				ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.VpcId))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert client-vpn-endpoint→vpc relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveClientVPNTargetNetworkAssociationRelationships links each target network
// association to its endpoint and subnet.
func resolveClientVPNTargetNetworkAssociationRelationships(acct *account, st *store.Store) error {
	assocs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2ClientVPNTargetNetworkAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range assocs {
		var attrs struct {
			ClientVpnEndpointId *string `json:"ClientVpnEndpointId"`
			TargetNetworkId     *string `json:"TargetNetworkId"` // subnet ID
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.ClientVpnEndpointId != nil {
			epID := store.ResourceID("aws", acct.ID, TypeEC2ClientVPNEndpoint,
				ec2ARN(region, acct.ID, "client-vpn-endpoint", *attrs.ClientVpnEndpointId))
			if err := st.UpsertRelationship(r.ID, epID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert client-vpn-target-assoc→endpoint relationship: %w", err)
			}
		}
		if attrs.TargetNetworkId != nil {
			subnetID := store.ResourceID("aws", acct.ID, TypeEC2Subnet,
				ec2ARN(region, acct.ID, "subnet", *attrs.TargetNetworkId))
			if err := st.UpsertRelationship(r.ID, subnetID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert client-vpn-target-assoc→subnet relationship: %w", err)
			}
		}
	}
	return nil
}
