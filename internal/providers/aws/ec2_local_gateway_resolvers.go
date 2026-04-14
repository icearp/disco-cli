package aws

import (
	"encoding/json"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveLocalGatewayRouteTableVPCAssociationRelationships)
	registerResolver(resolveLocalGatewayRouteTableVIGAssociationRelationships)
}

// resolveLocalGatewayRouteTableVPCAssociationRelationships links each VPC association
// to its route table and VPC.
func resolveLocalGatewayRouteTableVPCAssociationRelationships(acct *account, st *store.Store) error {
	assocs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2LocalGatewayRouteTableVPCAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range assocs {
		var attrs struct {
			LocalGatewayRouteTableArn *string `json:"LocalGatewayRouteTableArn"`
			VpcId                     *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.LocalGatewayRouteTableArn != nil {
			rtID := store.ResourceID("aws", acct.ID, TypeEC2LocalGatewayRouteTable, *attrs.LocalGatewayRouteTableArn)
			if err := st.UpsertRelationship(r.ID, rtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert lgw-rtb-vpc-assoc→route-table relationship: %w", err)
			}
		}
		if attrs.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC,
				ec2ARN(region, acct.ID, "vpc", *attrs.VpcId))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert lgw-rtb-vpc-assoc→vpc relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveLocalGatewayRouteTableVIGAssociationRelationships links each VIG association
// to its route table and virtual interface group.
func resolveLocalGatewayRouteTableVIGAssociationRelationships(acct *account, st *store.Store) error {
	assocs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2LocalGatewayRouteTableVIGAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range assocs {
		var attrs struct {
			LocalGatewayRouteTableArn           *string `json:"LocalGatewayRouteTableArn"`
			LocalGatewayVirtualInterfaceGroupId *string `json:"LocalGatewayVirtualInterfaceGroupId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.LocalGatewayRouteTableArn != nil {
			rtID := store.ResourceID("aws", acct.ID, TypeEC2LocalGatewayRouteTable, *attrs.LocalGatewayRouteTableArn)
			if err := st.UpsertRelationship(r.ID, rtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert lgw-rtb-vig-assoc→route-table relationship: %w", err)
			}
		}
		if attrs.LocalGatewayVirtualInterfaceGroupId != nil {
			vigID := store.ResourceID("aws", acct.ID, TypeEC2LocalGatewayVirtualInterfaceGroup,
				ec2ARN(region, acct.ID, "local-gateway-virtual-interface-group", *attrs.LocalGatewayVirtualInterfaceGroupId))
			if err := st.UpsertRelationship(r.ID, vigID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert lgw-rtb-vig-assoc→vig relationship: %w", err)
			}
		}
	}
	return nil
}
