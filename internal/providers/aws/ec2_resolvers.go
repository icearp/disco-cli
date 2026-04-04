package aws

import (
	"encoding/json"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

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
		Provider: "aws", AccountID: acct.ID, Types: []string{"aws:ec2:instance"},
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
		// Instance → VPC
		if attrs.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, "aws:ec2:vpc", *attrs.VpcId)
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert instance→vpc relationship: %w", err)
			}
		}
		// Instance → Subnet
		if attrs.SubnetId != nil {
			subnetID := store.ResourceID("aws", acct.ID, "aws:ec2:subnet", *attrs.SubnetId)
			if err := st.UpsertRelationship(r.ID, subnetID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert instance→subnet relationship: %w", err)
			}
		}
		// Instance → Security Groups
		for _, sg := range attrs.SecurityGroups {
			if sg.GroupId != nil {
				sgID := store.ResourceID("aws", acct.ID, "aws:ec2:security-group", *sg.GroupId)
				if err := st.UpsertRelationship(r.ID, sgID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert instance→security-group relationship: %w", err)
				}
			}
		}
		// Instance → EBS Volumes (from block device mappings)
		for _, bdm := range attrs.BlockDeviceMappings {
			if bdm.Ebs != nil && bdm.Ebs.VolumeId != nil {
				volID := store.ResourceID("aws", acct.ID, "aws:ec2:volume", *bdm.Ebs.VolumeId)
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
		Provider: "aws", AccountID: acct.ID, Types: []string{"aws:ec2:subnet"},
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
			vpcID := store.ResourceID("aws", acct.ID, "aws:ec2:vpc", *attrs.VpcId)
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert subnet→vpc relationship: %w", err)
			}
		}
	}
	return nil
}

func resolveIGWRelationships(acct *account, st *store.Store) error {
	igws, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{"aws:ec2:internet-gateway"},
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
				vpcID := store.ResourceID("aws", acct.ID, "aws:ec2:vpc", *att.VpcId)
				if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert igw→vpc relationship: %w", err)
				}
			}
		}
	}
	return nil
}
