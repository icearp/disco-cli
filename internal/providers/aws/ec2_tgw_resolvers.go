package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveTGWAttachmentRelationships,
		EdgeDecl{TypeEC2TransitGatewayAttachment, TypeEC2TransitGateway, store.RelAttachedTo},
		EdgeDecl{TypeEC2TransitGatewayAttachment, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(resolveTGWConnectRelationships,
		EdgeDecl{TypeEC2TransitGatewayConnect, TypeEC2TransitGateway, store.RelAttachedTo},
	)
	registerResolver(resolveTGWConnectPeerRelationships,
		EdgeDecl{TypeEC2TransitGatewayConnectPeer, TypeEC2TransitGatewayConnect, store.RelAttachedTo},
	)
	registerResolver(resolveTGWMulticastDomainRelationships,
		EdgeDecl{TypeEC2TransitGatewayMulticastDomain, TypeEC2TransitGateway, store.RelAttachedTo},
	)
	registerResolver(resolveTGWRouteTableRelationships,
		EdgeDecl{TypeEC2TransitGatewayRouteTable, TypeEC2TransitGateway, store.RelAttachedTo},
	)
	registerResolver(resolveTGWVPCAttachmentRelationships,
		EdgeDecl{TypeEC2TransitGatewayVPCAttachment, TypeEC2TransitGateway, store.RelAttachedTo},
		EdgeDecl{TypeEC2TransitGatewayVPCAttachment, TypeEC2VPC, store.RelAttachedTo},
	)
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
			TransitGatewayID  *string `json:"TransitGatewayID"`
			TransitGatewayArn *string `json:"TransitGatewayArn"`
			ResourceID        *string `json:"ResourceID"`   // VPC ID when ResourceType == "vpc"
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
		} else if attrs.TransitGatewayID != nil {
			tgwID := store.ResourceID("aws", acct.ID, TypeEC2TransitGateway, ec2ARN(region, acct.ID, "transit-gateway", *attrs.TransitGatewayID))
			if err := st.UpsertRelationship(r.ID, tgwID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert tgw-attachment→transit-gateway relationship: %w", err)
			}
		}
		// Attachment → VPC (only when ResourceType is "vpc").
		if attrs.ResourceType != nil && *attrs.ResourceType == "vpc" && attrs.ResourceID != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", *attrs.ResourceID))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert tgw-attachment→vpc relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveTGWConnectRelationships links each TGW Connect to its parent TGW and
// the underlying transport attachment.
func resolveTGWConnectRelationships(acct *account, st *store.Store) error {
	conns, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2TransitGatewayConnect},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range conns {
		var attrs struct {
			TransitGatewayID *string `json:"TransitGatewayID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.TransitGatewayID != nil {
			tgwID := store.ResourceID("aws", acct.ID, TypeEC2TransitGateway,
				ec2ARN(region, acct.ID, "transit-gateway", *attrs.TransitGatewayID))
			if err := st.UpsertRelationship(r.ID, tgwID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert tgw-connect→tgw relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveTGWConnectPeerRelationships links each Connect Peer to its Connect attachment.
func resolveTGWConnectPeerRelationships(acct *account, st *store.Store) error {
	peers, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2TransitGatewayConnectPeer},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range peers {
		var attrs struct {
			TransitGatewayAttachmentID *string `json:"TransitGatewayAttachmentID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.TransitGatewayAttachmentID != nil {
			connID := store.ResourceID("aws", acct.ID, TypeEC2TransitGatewayConnect,
				ec2ARN(region, acct.ID, "transit-gateway-connect", *attrs.TransitGatewayAttachmentID))
			if err := st.UpsertRelationship(r.ID, connID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert tgw-connect-peer→tgw-connect relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveTGWMulticastDomainRelationships links each multicast domain to its TGW.
func resolveTGWMulticastDomainRelationships(acct *account, st *store.Store) error {
	domains, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2TransitGatewayMulticastDomain},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range domains {
		var attrs struct {
			TransitGatewayID *string `json:"TransitGatewayID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.TransitGatewayID != nil {
			tgwID := store.ResourceID("aws", acct.ID, TypeEC2TransitGateway,
				ec2ARN(region, acct.ID, "transit-gateway", *attrs.TransitGatewayID))
			if err := st.UpsertRelationship(r.ID, tgwID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert tgw-mcast-domain→tgw relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveTGWRouteTableRelationships links each TGW route table to its TGW.
func resolveTGWRouteTableRelationships(acct *account, st *store.Store) error {
	rts, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2TransitGatewayRouteTable},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range rts {
		var attrs struct {
			TransitGatewayID *string `json:"TransitGatewayID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.TransitGatewayID != nil {
			tgwID := store.ResourceID("aws", acct.ID, TypeEC2TransitGateway,
				ec2ARN(region, acct.ID, "transit-gateway", *attrs.TransitGatewayID))
			if err := st.UpsertRelationship(r.ID, tgwID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert tgw-route-table→tgw relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveTGWVPCAttachmentRelationships links each TGW VPC attachment to its TGW and VPC.
func resolveTGWVPCAttachmentRelationships(acct *account, st *store.Store) error {
	atts, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2TransitGatewayVPCAttachment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range atts {
		var attrs struct {
			TransitGatewayID *string `json:"TransitGatewayID"`
			VpcID            *string `json:"VpcID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.TransitGatewayID != nil {
			tgwID := store.ResourceID("aws", acct.ID, TypeEC2TransitGateway,
				ec2ARN(region, acct.ID, "transit-gateway", *attrs.TransitGatewayID))
			if err := st.UpsertRelationship(r.ID, tgwID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert tgw-vpc-att→tgw relationship: %w", err)
			}
		}
		if attrs.VpcID != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC,
				ec2ARN(region, acct.ID, "vpc", *attrs.VpcID))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert tgw-vpc-att→vpc relationship: %w", err)
			}
		}
	}
	return nil
}
