package aws

import (
	"encoding/json"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveVPNConnectionRelationships)
	registerResolver(resolveTGWAttachmentRelationships)
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
			VpnGatewayId      *string `json:"VpnGatewayId"`
			CustomerGatewayId *string `json:"CustomerGatewayId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.VpnGatewayId != nil {
			vgwID := store.ResourceID("aws", acct.ID, TypeEC2VPNGateway, ec2ARN(region, acct.ID, "vpn-gateway", *attrs.VpnGatewayId))
			if err := st.UpsertRelationship(r.ID, vgwID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert vpn-connection→vpn-gateway relationship: %w", err)
			}
		}
		if attrs.CustomerGatewayId != nil {
			cgwID := store.ResourceID("aws", acct.ID, TypeEC2CustomerGateway, ec2ARN(region, acct.ID, "customer-gateway", *attrs.CustomerGatewayId))
			if err := st.UpsertRelationship(r.ID, cgwID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert vpn-connection→customer-gateway relationship: %w", err)
			}
		}
	}
	return nil
}

func resolveTGWAttachmentRelationships(acct *account, st *store.Store) error {
	atts, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2TransitGatewayAttachment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range atts {
		var attrs struct {
			TransitGatewayId  *string `json:"TransitGatewayId"`
			TransitGatewayArn *string `json:"TransitGatewayArn"`
			ResourceId        *string `json:"ResourceId"`   // VPC ID when ResourceType == "vpc"
			ResourceType      *string `json:"ResourceType"` // "vpc", "vpn", "peering", etc.
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		// Attachment → Transit Gateway (use ARN if available, else build from ID).
		if attrs.TransitGatewayArn != nil {
			tgwID := store.ResourceID("aws", acct.ID, TypeEC2TransitGateway, *attrs.TransitGatewayArn)
			if err := st.UpsertRelationship(r.ID, tgwID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert tgw-attachment→transit-gateway relationship: %w", err)
			}
		} else if attrs.TransitGatewayId != nil {
			tgwID := store.ResourceID("aws", acct.ID, TypeEC2TransitGateway, ec2ARN(region, acct.ID, "transit-gateway", *attrs.TransitGatewayId))
			if err := st.UpsertRelationship(r.ID, tgwID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert tgw-attachment→transit-gateway relationship: %w", err)
			}
		}
		// Attachment → VPC (only when ResourceType is "vpc").
		if attrs.ResourceType != nil && *attrs.ResourceType == "vpc" && attrs.ResourceId != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", *attrs.ResourceId))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert tgw-attachment→vpc relationship: %w", err)
			}
		}
	}
	return nil
}
