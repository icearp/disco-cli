package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveInstanceRelationships)
	registerResolver(resolveInstanceConnectEndpointRelationships)
	registerResolver(resolveSecurityGroupVPCAssociationRelationships)
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
