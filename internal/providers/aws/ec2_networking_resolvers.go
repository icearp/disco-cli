package aws

import (
	"encoding/json"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveSubnetVPCRelationships)
	registerResolver(resolveIGWRelationships)
	registerResolver(resolveRouteTableRelationships)
	registerResolver(resolveNatGatewayRelationships)
	registerResolver(resolveEIPRelationships)
	registerResolver(resolveNetworkInterfaceRelationships)
	registerResolver(resolveNetworkACLRelationships)
	registerResolver(resolveVPCEndpointRelationships)
	registerResolver(resolveVPCPeeringRelationships)
	registerResolver(resolveCarrierGatewayRelationships)
	registerResolver(resolveVPCEndpointServicePermissionsRelationships)
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
			VpcId *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.VpcId))
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
				VpcId *string `json:"VpcId"`
			} `json:"Attachments"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, att := range attrs.Attachments {
			if att.VpcId != nil {
				vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(sv(r.Region), acct.ID, "vpc", *att.VpcId))
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
			VpcId *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.VpcId))
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
			SubnetId *string `json:"SubnetId"`
			VpcId    *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.SubnetId != nil {
			subnetID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", *attrs.SubnetId))
			if err := st.UpsertRelationship(r.ID, subnetID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert nat-gateway→subnet relationship: %w", err)
			}
		}
		if attrs.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", *attrs.VpcId))
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
			InstanceId         *string `json:"InstanceId"`
			NetworkInterfaceId *string `json:"NetworkInterfaceId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.InstanceId != nil {
			instID := store.ResourceID("aws", acct.ID, TypeEC2Instance, ec2ARN(region, acct.ID, "instance", *attrs.InstanceId))
			if err := st.UpsertRelationship(r.ID, instID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert eip→instance relationship: %w", err)
			}
		}
		if attrs.NetworkInterfaceId != nil {
			eniID := store.ResourceID("aws", acct.ID, TypeEC2NetworkInterface, ec2ARN(region, acct.ID, "network-interface", *attrs.NetworkInterfaceId))
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
			SubnetId *string `json:"SubnetId"`
			VpcId    *string `json:"VpcId"`
			Groups   []struct {
				GroupId *string `json:"GroupId"`
			} `json:"Groups"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.SubnetId != nil {
			subnetID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", *attrs.SubnetId))
			if err := st.UpsertRelationship(r.ID, subnetID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert eni→subnet relationship: %w", err)
			}
		}
		if attrs.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", *attrs.VpcId))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert eni→vpc relationship: %w", err)
			}
		}
		for _, sg := range attrs.Groups {
			if sg.GroupId != nil {
				sgID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", *sg.GroupId))
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
			VpcId *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.VpcId))
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
			VpcId *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.VpcId))
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
			VpcId *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC,
				ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.VpcId))
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
			ServiceId *string `json:"ServiceId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ServiceId != nil {
			svcID := store.ResourceID("aws", acct.ID, TypeEC2VPCEndpointService,
				ec2ARN(sv(r.Region), acct.ID, "vpc-endpoint-service", *attrs.ServiceId))
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
				VpcId   *string `json:"VpcId"`
				OwnerId *string `json:"OwnerId"`
				Region  *string `json:"Region"`
			} `json:"RequesterVpcInfo"`
			AccepterVpcInfo struct {
				VpcId   *string `json:"VpcId"`
				OwnerId *string `json:"OwnerId"`
				Region  *string `json:"Region"`
			} `json:"AccepterVpcInfo"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		// Requester VPC — use account from the peering info in case it's cross-account.
		if attrs.RequesterVpcInfo.VpcId != nil {
			reqAcct := acct.ID
			if attrs.RequesterVpcInfo.OwnerId != nil {
				reqAcct = *attrs.RequesterVpcInfo.OwnerId
			}
			reqRegion := sv(r.Region)
			if attrs.RequesterVpcInfo.Region != nil {
				reqRegion = *attrs.RequesterVpcInfo.Region
			}
			vpcID := store.ResourceID("aws", reqAcct, TypeEC2VPC, ec2ARN(reqRegion, reqAcct, "vpc", *attrs.RequesterVpcInfo.VpcId))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelPeer, "directed", nil); err != nil {
				return fmt.Errorf("upsert peering→requester-vpc relationship: %w", err)
			}
		}
		// Accepter VPC.
		if attrs.AccepterVpcInfo.VpcId != nil {
			accAcct := acct.ID
			if attrs.AccepterVpcInfo.OwnerId != nil {
				accAcct = *attrs.AccepterVpcInfo.OwnerId
			}
			accRegion := sv(r.Region)
			if attrs.AccepterVpcInfo.Region != nil {
				accRegion = *attrs.AccepterVpcInfo.Region
			}
			vpcID := store.ResourceID("aws", accAcct, TypeEC2VPC, ec2ARN(accRegion, accAcct, "vpc", *attrs.AccepterVpcInfo.VpcId))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelPeer, "directed", nil); err != nil {
				return fmt.Errorf("upsert peering→accepter-vpc relationship: %w", err)
			}
		}
	}
	return nil
}
