package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveClientVPNEndpointRelationships,
		EdgeDecl{TypeEC2ClientVPNEndpoint, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(resolveClientVPNTargetNetworkAssociationRelationships,
		EdgeDecl{TypeEC2ClientVPNTargetNetworkAssociation, TypeEC2ClientVPNEndpoint, store.RelAttachedTo},
		EdgeDecl{TypeEC2ClientVPNTargetNetworkAssociation, TypeEC2Subnet, store.RelAttachedTo},
	)
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
			VpcID *string `json:"VpcID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.VpcID != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC,
				ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.VpcID))
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
			ClientVpnEndpointID *string `json:"ClientVpnEndpointID"`
			TargetNetworkID     *string `json:"TargetNetworkID"` // subnet ID
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.ClientVpnEndpointID != nil {
			epID := store.ResourceID("aws", acct.ID, TypeEC2ClientVPNEndpoint,
				ec2ARN(region, acct.ID, "client-vpn-endpoint", *attrs.ClientVpnEndpointID))
			if err := st.UpsertRelationship(r.ID, epID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert client-vpn-target-assoc→endpoint relationship: %w", err)
			}
		}
		if attrs.TargetNetworkID != nil {
			subnetID := store.ResourceID("aws", acct.ID, TypeEC2Subnet,
				ec2ARN(region, acct.ID, "subnet", *attrs.TargetNetworkID))
			if err := st.UpsertRelationship(r.ID, subnetID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert client-vpn-target-assoc→subnet relationship: %w", err)
			}
		}
	}
	return nil
}
