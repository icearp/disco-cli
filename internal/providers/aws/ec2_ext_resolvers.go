package aws

import (
	"encoding/json"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveCarrierGatewayRelationships)
	registerResolver(resolveVPCEndpointServicePermissionsRelationships)
	registerResolver(resolveSecurityGroupRuleRelationships)
	registerResolver(resolveSecurityGroupVPCAssociationRelationships)
	registerResolver(resolveNetworkInterfaceAttachmentRelationships)
	registerResolver(resolveVolumeAttachmentRelationships)
	registerResolver(resolveEIPAssociationRelationships)
	registerResolver(resolveSubnetRouteTableAssociationRelationships)
	registerResolver(resolveSubnetNetworkACLAssociationRelationships)
	registerResolver(resolveVPCDHCPOptionsAssociationRelationships)
	registerResolver(resolveVPCGatewayAttachmentRelationships)
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

// resolveSecurityGroupRuleRelationships links each SG ingress/egress rule to its security group.
func resolveSecurityGroupRuleRelationships(acct *account, st *store.Store) error {
	rules, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeEC2SecurityGroupIngress, TypeEC2SecurityGroupEgress},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range rules {
		var attrs struct {
			GroupId *string `json:"GroupId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.GroupId != nil {
			sgID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup,
				ec2ARN(sv(r.Region), acct.ID, "security-group", *attrs.GroupId))
			if err := st.UpsertRelationship(r.ID, sgID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert sg-rule→security-group relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveSecurityGroupVPCAssociationRelationships links each SG VPC association to its SG and VPC.
func resolveSecurityGroupVPCAssociationRelationships(acct *account, st *store.Store) error {
	assocs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2SecurityGroupVPCAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range assocs {
		var attrs struct {
			GroupId *string `json:"GroupId"`
			VpcId   *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.GroupId != nil {
			sgID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup,
				ec2ARN(region, acct.ID, "security-group", *attrs.GroupId))
			if err := st.UpsertRelationship(r.ID, sgID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert sg-vpc-assoc→sg relationship: %w", err)
			}
		}
		if attrs.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC,
				ec2ARN(region, acct.ID, "vpc", *attrs.VpcId))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert sg-vpc-assoc→vpc relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveNetworkInterfaceAttachmentRelationships links each ENI attachment to its instance.
// Note: the attachment attributes do not include NetworkInterfaceId, so only the
// instance relationship is resolved here.
func resolveNetworkInterfaceAttachmentRelationships(acct *account, st *store.Store) error {
	atts, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2NetworkInterfaceAttachment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range atts {
		var attrs struct {
			InstanceId *string `json:"InstanceId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.InstanceId != nil {
			instID := store.ResourceID("aws", acct.ID, TypeEC2Instance,
				ec2ARN(sv(r.Region), acct.ID, "instance", *attrs.InstanceId))
			if err := st.UpsertRelationship(r.ID, instID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert eni-attachment→instance relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveVolumeAttachmentRelationships links each volume attachment to its volume and instance.
func resolveVolumeAttachmentRelationships(acct *account, st *store.Store) error {
	atts, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2VolumeAttachment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range atts {
		var attrs struct {
			VolumeId   *string `json:"VolumeId"`
			InstanceId *string `json:"InstanceId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.VolumeId != nil {
			volID := store.ResourceID("aws", acct.ID, TypeEC2Volume,
				ec2ARN(region, acct.ID, "volume", *attrs.VolumeId))
			if err := st.UpsertRelationship(r.ID, volID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert volume-attachment→volume relationship: %w", err)
			}
		}
		if attrs.InstanceId != nil {
			instID := store.ResourceID("aws", acct.ID, TypeEC2Instance,
				ec2ARN(region, acct.ID, "instance", *attrs.InstanceId))
			if err := st.UpsertRelationship(r.ID, instID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert volume-attachment→instance relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveEIPAssociationRelationships links each EIP association to its EIP and (if present) instance.
func resolveEIPAssociationRelationships(acct *account, st *store.Store) error {
	assocs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2EIPAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range assocs {
		var attrs struct {
			AllocationId *string `json:"AllocationId"`
			InstanceId   *string `json:"InstanceId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.AllocationId != nil {
			eipID := store.ResourceID("aws", acct.ID, TypeEC2EIP,
				ec2ARN(region, acct.ID, "elastic-ip", *attrs.AllocationId))
			if err := st.UpsertRelationship(r.ID, eipID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert eip-assoc→eip relationship: %w", err)
			}
		}
		if attrs.InstanceId != nil {
			instID := store.ResourceID("aws", acct.ID, TypeEC2Instance,
				ec2ARN(region, acct.ID, "instance", *attrs.InstanceId))
			if err := st.UpsertRelationship(r.ID, instID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert eip-assoc→instance relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveSubnetRouteTableAssociationRelationships links each subnet route table
// association to its subnet and route table.
func resolveSubnetRouteTableAssociationRelationships(acct *account, st *store.Store) error {
	assocs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2SubnetRouteTableAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range assocs {
		var attrs struct {
			SubnetId     *string `json:"SubnetId"`
			RouteTableId *string `json:"RouteTableId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.SubnetId != nil {
			subnetID := store.ResourceID("aws", acct.ID, TypeEC2Subnet,
				ec2ARN(region, acct.ID, "subnet", *attrs.SubnetId))
			if err := st.UpsertRelationship(r.ID, subnetID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert subnet-rt-assoc→subnet relationship: %w", err)
			}
		}
		if attrs.RouteTableId != nil {
			rtID := store.ResourceID("aws", acct.ID, TypeEC2RouteTable,
				ec2ARN(region, acct.ID, "route-table", *attrs.RouteTableId))
			if err := st.UpsertRelationship(r.ID, rtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert subnet-rt-assoc→route-table relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveSubnetNetworkACLAssociationRelationships links each subnet NACL association
// to its subnet and network ACL.
func resolveSubnetNetworkACLAssociationRelationships(acct *account, st *store.Store) error {
	assocs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2SubnetNetworkACLAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range assocs {
		var attrs struct {
			SubnetId      *string `json:"SubnetId"`
			NetworkAclId  *string `json:"NetworkAclId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.SubnetId != nil {
			subnetID := store.ResourceID("aws", acct.ID, TypeEC2Subnet,
				ec2ARN(region, acct.ID, "subnet", *attrs.SubnetId))
			if err := st.UpsertRelationship(r.ID, subnetID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert subnet-nacl-assoc→subnet relationship: %w", err)
			}
		}
		if attrs.NetworkAclId != nil {
			naclID := store.ResourceID("aws", acct.ID, TypeEC2NetworkACL,
				ec2ARN(region, acct.ID, "network-acl", *attrs.NetworkAclId))
			if err := st.UpsertRelationship(r.ID, naclID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert subnet-nacl-assoc→nacl relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveVPCDHCPOptionsAssociationRelationships links each VPC-DHCP-options association
// to its VPC and DHCP options set.
func resolveVPCDHCPOptionsAssociationRelationships(acct *account, st *store.Store) error {
	assocs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2VPCDHCPOptionsAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range assocs {
		var attrs struct {
			VpcId         *string `json:"VpcId"`
			DhcpOptionsId *string `json:"DhcpOptionsId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC,
				ec2ARN(region, acct.ID, "vpc", *attrs.VpcId))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert vpc-dhcp-assoc→vpc relationship: %w", err)
			}
		}
		if attrs.DhcpOptionsId != nil {
			dhcpID := store.ResourceID("aws", acct.ID, TypeEC2DHCPOptions,
				ec2ARN(region, acct.ID, "dhcp-options", *attrs.DhcpOptionsId))
			if err := st.UpsertRelationship(r.ID, dhcpID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert vpc-dhcp-assoc→dhcp-options relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveVPCGatewayAttachmentRelationships links each IGW-VPC attachment to its VPC.
// Note: the attachment attributes do not include InternetGatewayId, so only the
// VPC relationship is resolved here.
func resolveVPCGatewayAttachmentRelationships(acct *account, st *store.Store) error {
	atts, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2VPCGatewayAttachment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range atts {
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
				return fmt.Errorf("upsert vpc-gateway-attachment→vpc relationship: %w", err)
			}
		}
	}
	return nil
}
