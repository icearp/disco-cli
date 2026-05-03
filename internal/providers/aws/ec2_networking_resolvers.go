package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveSubnetVPCRelationships,
		EdgeDecl{TypeEC2Subnet, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(resolveIGWRelationships,
		EdgeDecl{TypeEC2InternetGateway, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(resolveRouteTableRelationships,
		EdgeDecl{TypeEC2RouteTable, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(resolveRouteTableRoutes,
		EdgeDecl{TypeEC2RouteTable, TypeEC2InternetGateway, store.RelRoutesTo},
		EdgeDecl{TypeEC2RouteTable, TypeEC2VPNGateway, store.RelRoutesTo},
		EdgeDecl{TypeEC2RouteTable, TypeEC2VPCEndpoint, store.RelRoutesTo},
		EdgeDecl{TypeEC2RouteTable, TypeEC2NatGateway, store.RelRoutesTo},
		EdgeDecl{TypeEC2RouteTable, TypeEC2TransitGateway, store.RelRoutesTo},
		EdgeDecl{TypeEC2RouteTable, TypeEC2VPCPeeringConnection, store.RelRoutesTo},
		EdgeDecl{TypeEC2RouteTable, TypeEC2NetworkInterface, store.RelRoutesTo},
		EdgeDecl{TypeEC2RouteTable, TypeEC2EgressOnlyIGW, store.RelRoutesTo},
		EdgeDecl{TypeEC2RouteTable, TypeEC2CarrierGateway, store.RelRoutesTo},
		EdgeDecl{TypeEC2RouteTable, TypeEC2Instance, store.RelRoutesTo},
	)
	registerResolver(resolveSecurityGroupVPC,
		EdgeDecl{TypeEC2SecurityGroup, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(resolveNatGatewayRelationships,
		EdgeDecl{TypeEC2NatGateway, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeEC2NatGateway, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(resolveEIPRelationships,
		EdgeDecl{TypeEC2EIP, TypeEC2Instance, store.RelAttachedTo},
		EdgeDecl{TypeEC2EIP, TypeEC2NetworkInterface, store.RelAttachedTo},
	)
	registerResolver(resolveNetworkInterfaceRelationships,
		EdgeDecl{TypeEC2NetworkInterface, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeEC2NetworkInterface, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeEC2NetworkInterface, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(resolveNetworkACLRelationships,
		EdgeDecl{TypeEC2NetworkACL, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeEC2NetworkACL, TypeEC2Subnet, store.RelAttachedTo},
	)
	registerResolver(resolveVPCEndpointRelationships,
		EdgeDecl{TypeEC2VPCEndpoint, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(resolveVPCPeeringRelationships,
		EdgeDecl{TypeEC2VPCPeeringConnection, TypeEC2VPC, store.RelPeer},
	)
	registerResolver(resolveCarrierGatewayRelationships,
		EdgeDecl{TypeEC2CarrierGateway, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(resolveVPCEndpointServicePermissionsRelationships,
		EdgeDecl{TypeEC2VPCEndpointServicePermissions, TypeEC2VPCEndpointService, store.RelAttachedTo},
	)
}

func resolveSubnetVPCRelationships(acct *account, st *store.Store) error {
	subnets, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2Subnet},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range subnets {
		var attrs struct {
			VpcID *string `json:"VpcID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.VpcID != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.VpcID))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert subnet→vpc relationship: %w", err)
			}
		}
	}
	return nil
}

func resolveIGWRelationships(acct *account, st *store.Store) error {
	igws, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2InternetGateway},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range igws {
		var attrs struct {
			Attachments []struct {
				VpcID *string `json:"VpcID"`
			} `json:"Attachments"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, att := range attrs.Attachments {
			if att.VpcID != nil {
				vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(sv(r.Region), acct.ID, "vpc", *att.VpcID))
				if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert igw→vpc relationship: %w", err)
				}
			}
		}
	}
	return nil
}

func resolveRouteTableRelationships(acct *account, st *store.Store) error {
	rts, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2RouteTable},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range rts {
		var attrs struct {
			VpcID *string `json:"VpcID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.VpcID != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.VpcID))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert route-table→vpc relationship: %w", err)
			}
		}
	}
	return nil
}

func resolveNatGatewayRelationships(acct *account, st *store.Store) error {
	ngws, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2NatGateway},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range ngws {
		var attrs struct {
			SubnetID *string `json:"SubnetID"`
			VpcID    *string `json:"VpcID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.SubnetID != nil {
			subnetID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", *attrs.SubnetID))
			if err := st.UpsertRelationship(r.ID, subnetID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert nat-gateway→subnet relationship: %w", err)
			}
		}
		if attrs.VpcID != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", *attrs.VpcID))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert nat-gateway→vpc relationship: %w", err)
			}
		}
	}
	return nil
}

func resolveEIPRelationships(acct *account, st *store.Store) error {
	eips, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2EIP},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range eips {
		var attrs struct {
			InstanceID         *string `json:"InstanceID"`
			NetworkInterfaceID *string `json:"NetworkInterfaceID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.InstanceID != nil {
			instID := store.ResourceID("aws", acct.ID, TypeEC2Instance, ec2ARN(region, acct.ID, "instance", *attrs.InstanceID))
			if err := st.UpsertRelationship(r.ID, instID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert eip→instance relationship: %w", err)
			}
		}
		if attrs.NetworkInterfaceID != nil {
			eniID := store.ResourceID("aws", acct.ID, TypeEC2NetworkInterface, ec2ARN(region, acct.ID, "network-interface", *attrs.NetworkInterfaceID))
			if err := st.UpsertRelationship(r.ID, eniID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert eip→network-interface relationship: %w", err)
			}
		}
	}
	return nil
}

func resolveNetworkInterfaceRelationships(acct *account, st *store.Store) error {
	enis, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2NetworkInterface},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range enis {
		var attrs struct {
			SubnetID *string `json:"SubnetID"`
			VpcID    *string `json:"VpcID"`
			Groups   []struct {
				GroupID *string `json:"GroupID"`
			} `json:"Groups"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.SubnetID != nil {
			subnetID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", *attrs.SubnetID))
			if err := st.UpsertRelationship(r.ID, subnetID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert eni→subnet relationship: %w", err)
			}
		}
		if attrs.VpcID != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", *attrs.VpcID))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert eni→vpc relationship: %w", err)
			}
		}
		for _, sg := range attrs.Groups {
			if sg.GroupID != nil {
				sgID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", *sg.GroupID))
				if err := st.UpsertRelationship(r.ID, sgID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert eni→security-group relationship: %w", err)
				}
			}
		}
	}
	return nil
}

func resolveNetworkACLRelationships(acct *account, st *store.Store) error {
	nacls, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2NetworkACL},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range nacls {
		var attrs struct {
			VpcID *string `json:"VpcID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.VpcID != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.VpcID))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert nacl→vpc relationship: %w", err)
			}
		}
	}
	return nil
}

func resolveVPCEndpointRelationships(acct *account, st *store.Store) error {
	eps, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2VPCEndpoint},
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
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.VpcID))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert vpc-endpoint→vpc relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveCarrierGatewayRelationships links each carrier gateway to its VPC.
func resolveCarrierGatewayRelationships(acct *account, st *store.Store) error {
	cgws, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2CarrierGateway},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range cgws {
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
				return fmt.Errorf("upsert carrier-gateway→vpc relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveVPCEndpointServicePermissionsRelationships links each permission to its service.
func resolveVPCEndpointServicePermissionsRelationships(acct *account, st *store.Store) error {
	perms, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2VPCEndpointServicePermissions},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range perms {
		var attrs struct {
			ServiceID *string `json:"ServiceID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ServiceID != nil {
			svcID := store.ResourceID("aws", acct.ID, TypeEC2VPCEndpointService,
				ec2ARN(sv(r.Region), acct.ID, "vpc-endpoint-service", *attrs.ServiceID))
			if err := st.UpsertRelationship(r.ID, svcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert vpc-endpoint-service-permission→service relationship: %w", err)
			}
		}
	}
	return nil
}

func resolveVPCPeeringRelationships(acct *account, st *store.Store) error {
	pcs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2VPCPeeringConnection},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range pcs {
		var attrs struct {
			RequesterVpcInfo struct {
				VpcID   *string `json:"VpcID"`
				OwnerID *string `json:"OwnerID"`
				Region  *string `json:"Region"`
			} `json:"RequesterVpcInfo"`
			AccepterVpcInfo struct {
				VpcID   *string `json:"VpcID"`
				OwnerID *string `json:"OwnerID"`
				Region  *string `json:"Region"`
			} `json:"AccepterVpcInfo"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		// Requester VPC — use account from the peering info in case it's cross-account.
		if attrs.RequesterVpcInfo.VpcID != nil {
			reqAcct := acct.ID
			if attrs.RequesterVpcInfo.OwnerID != nil {
				reqAcct = *attrs.RequesterVpcInfo.OwnerID
			}
			reqRegion := sv(r.Region)
			if attrs.RequesterVpcInfo.Region != nil {
				reqRegion = *attrs.RequesterVpcInfo.Region
			}
			vpcID := store.ResourceID("aws", reqAcct, TypeEC2VPC, ec2ARN(reqRegion, reqAcct, "vpc", *attrs.RequesterVpcInfo.VpcID))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelPeer, "directed", nil); err != nil {
				return fmt.Errorf("upsert peering→requester-vpc relationship: %w", err)
			}
		}
		// Accepter VPC.
		if attrs.AccepterVpcInfo.VpcID != nil {
			accAcct := acct.ID
			if attrs.AccepterVpcInfo.OwnerID != nil {
				accAcct = *attrs.AccepterVpcInfo.OwnerID
			}
			accRegion := sv(r.Region)
			if attrs.AccepterVpcInfo.Region != nil {
				accRegion = *attrs.AccepterVpcInfo.Region
			}
			vpcID := store.ResourceID("aws", accAcct, TypeEC2VPC, ec2ARN(accRegion, accAcct, "vpc", *attrs.AccepterVpcInfo.VpcID))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelPeer, "directed", nil); err != nil {
				return fmt.Errorf("upsert peering→accepter-vpc relationship: %w", err)
			}
		}
	}
	return nil
}
