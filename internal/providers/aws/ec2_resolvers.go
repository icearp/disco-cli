package aws

import (
	"encoding/json"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveInstanceRelationships)
	registerResolver(resolveSubnetVPCRelationships)
	registerResolver(resolveIGWRelationships)
	registerResolver(resolveRouteTableRelationships)
	registerResolver(resolveNatGatewayRelationships)
	registerResolver(resolveEIPRelationships)
	registerResolver(resolveNetworkInterfaceRelationships)
	registerResolver(resolveNetworkACLRelationships)
	registerResolver(resolveVPCEndpointRelationships)
	registerResolver(resolveVPCPeeringRelationships)
	registerResolver(resolveVPNConnectionRelationships)
	registerResolver(resolveTGWAttachmentRelationships)
	registerResolver(resolveFlowLogRelationships)
	registerResolver(resolveInstanceConnectEndpointRelationships)
}

// instanceAttrs captures the fields we need from an EC2 instance's JSON blob.
type instanceAttrs struct {
	InstanceId         *string `json:"InstanceId"`
	VpcId              *string `json:"VpcId"`
	SubnetId           *string `json:"SubnetId"`
	IamInstanceProfile *struct {
		Arn *string `json:"Arn"`
	} `json:"IamInstanceProfile"`
	SecurityGroups []struct {
		GroupId *string `json:"GroupId"`
	} `json:"SecurityGroups"`
	BlockDeviceMappings []struct {
		Ebs *struct {
			VolumeId *string `json:"VolumeId"`
		} `json:"Ebs"`
	} `json:"BlockDeviceMappings"`
}

func resolveInstanceRelationships(acct *account, st *store.Store) error {
	instances, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2Instance},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range instances {
		var attrs instanceAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		// Instance → VPC
		if attrs.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", *attrs.VpcId))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert instance→vpc relationship: %w", err)
			}
		}
		// Instance → Subnet
		if attrs.SubnetId != nil {
			subnetID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", *attrs.SubnetId))
			if err := st.UpsertRelationship(r.ID, subnetID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert instance→subnet relationship: %w", err)
			}
		}
		// Instance → Security Groups
		for _, sg := range attrs.SecurityGroups {
			if sg.GroupId != nil {
				sgID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", *sg.GroupId))
				if err := st.UpsertRelationship(r.ID, sgID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert instance→security-group relationship: %w", err)
				}
			}
		}
		// Instance → EBS Volumes (from block device mappings)
		for _, bdm := range attrs.BlockDeviceMappings {
			if bdm.Ebs != nil && bdm.Ebs.VolumeId != nil {
				volID := store.ResourceID("aws", acct.ID, TypeEC2Volume, ec2ARN(region, acct.ID, "volume", *bdm.Ebs.VolumeId))
				if err := st.UpsertRelationship(r.ID, volID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert instance→volume relationship: %w", err)
				}
			}
		}
		// Instance → IAM Role (via instance profile ARN)
		// The instance profile ARN is arn:aws:iam::{account}:instance-profile/{name}.
		// The role ARN is not directly in the instance attrs, so we skip this
		// relationship here; it would require an IAM GetInstanceProfile call.
	}
	return nil
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

func resolveFlowLogRelationships(acct *account, st *store.Store) error {
	logs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2FlowLog},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range logs {
		var attrs struct {
			ResourceId   *string `json:"ResourceId"`
			ResourceType *string `json:"ResourceType"` // "VPC", "Subnet", "NetworkInterface"
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ResourceId == nil || attrs.ResourceType == nil {
			continue
		}
		region := sv(r.Region)
		var targetType, arnResourceType string
		switch *attrs.ResourceType {
		case "VPC":
			targetType, arnResourceType = TypeEC2VPC, "vpc"
		case "Subnet":
			targetType, arnResourceType = TypeEC2Subnet, "subnet"
		case "NetworkInterface":
			targetType, arnResourceType = TypeEC2NetworkInterface, "network-interface"
		default:
			continue
		}
		targetID := store.ResourceID("aws", acct.ID, targetType, ec2ARN(region, acct.ID, arnResourceType, *attrs.ResourceId))
		if err := st.UpsertRelationship(r.ID, targetID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert flow-log→resource relationship: %w", err)
		}
	}
	return nil
}

func resolveInstanceConnectEndpointRelationships(acct *account, st *store.Store) error {
	ices, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2InstanceConnectEndpoint},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range ices {
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
				return fmt.Errorf("upsert instance-connect-endpoint→subnet relationship: %w", err)
			}
		}
		if attrs.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", *attrs.VpcId))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert instance-connect-endpoint→vpc relationship: %w", err)
			}
		}
	}
	return nil
}
