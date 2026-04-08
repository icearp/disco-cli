package aws

import (
	"encoding/json"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveTGWConnectRelationships)
	registerResolver(resolveTGWConnectPeerRelationships)
	registerResolver(resolveTGWMulticastDomainRelationships)
	registerResolver(resolveTGWRouteTableRelationships)
	registerResolver(resolveTGWVPCAttachmentRelationships)
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
			TransitGatewayId *string `json:"TransitGatewayId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.TransitGatewayId != nil {
			tgwID := store.ResourceID("aws", acct.ID, TypeEC2TransitGateway,
				ec2ARN(region, acct.ID, "transit-gateway", *attrs.TransitGatewayId))
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
			TransitGatewayAttachmentId *string `json:"TransitGatewayAttachmentId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.TransitGatewayAttachmentId != nil {
			connID := store.ResourceID("aws", acct.ID, TypeEC2TransitGatewayConnect,
				ec2ARN(region, acct.ID, "transit-gateway-connect", *attrs.TransitGatewayAttachmentId))
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
			TransitGatewayId *string `json:"TransitGatewayId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.TransitGatewayId != nil {
			tgwID := store.ResourceID("aws", acct.ID, TypeEC2TransitGateway,
				ec2ARN(region, acct.ID, "transit-gateway", *attrs.TransitGatewayId))
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
			TransitGatewayId *string `json:"TransitGatewayId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.TransitGatewayId != nil {
			tgwID := store.ResourceID("aws", acct.ID, TypeEC2TransitGateway,
				ec2ARN(region, acct.ID, "transit-gateway", *attrs.TransitGatewayId))
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
			TransitGatewayId *string `json:"TransitGatewayId"`
			VpcId            *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.TransitGatewayId != nil {
			tgwID := store.ResourceID("aws", acct.ID, TypeEC2TransitGateway,
				ec2ARN(region, acct.ID, "transit-gateway", *attrs.TransitGatewayId))
			if err := st.UpsertRelationship(r.ID, tgwID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert tgw-vpc-att→tgw relationship: %w", err)
			}
		}
		if attrs.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC,
				ec2ARN(region, acct.ID, "vpc", *attrs.VpcId))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert tgw-vpc-att→vpc relationship: %w", err)
			}
		}
	}
	return nil
}
