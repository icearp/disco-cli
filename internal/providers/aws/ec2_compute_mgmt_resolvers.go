package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveInstanceRelationships,
		EdgeDecl{TypeEC2Instance, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeEC2Instance, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeEC2Instance, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeEC2Instance, TypeEC2Volume, store.RelAttachedTo},
		EdgeDecl{TypeEC2Instance, TypeIAMInstanceProfile, store.RelUses},
		EdgeDecl{TypeEC2Instance, TypeEC2KeyPair, store.RelUses},
		EdgeDecl{TypeEC2Instance, TypeEC2Image, store.RelUses},
		EdgeDecl{TypeEC2Instance, TypeEC2NetworkInterface, store.RelAttachedTo},
	)
	registerResolver(resolveInstanceConnectEndpointRelationships,
		EdgeDecl{TypeEC2InstanceConnectEndpoint, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeEC2InstanceConnectEndpoint, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(resolveSecurityGroupVPCAssociationRelationships,
		EdgeDecl{TypeEC2SecurityGroupVPCAssociation, TypeEC2SecurityGroup, store.RelAttachedTo},
		EdgeDecl{TypeEC2SecurityGroupVPCAssociation, TypeEC2VPC, store.RelAttachedTo},
	)
}

// instanceAttrs captures the fields we need from an EC2 instance's JSON blob.
type instanceAttrs struct {
	InstanceID         *string `json:"InstanceID"`
	ImageID            *string `json:"ImageID"`
	VpcID              *string `json:"VpcID"`
	SubnetID           *string `json:"SubnetID"`
	KeyName            *string `json:"KeyName"`
	IamInstanceProfile *struct {
		Arn *string `json:"Arn"`
	} `json:"IamInstanceProfile"`
	SecurityGroups []struct {
		GroupID *string `json:"GroupID"`
	} `json:"SecurityGroups"`
	BlockDeviceMappings []struct {
		Ebs *struct {
			VolumeID *string `json:"VolumeID"`
		} `json:"Ebs"`
	} `json:"BlockDeviceMappings"`
	NetworkInterfaces []struct {
		NetworkInterfaceID *string `json:"NetworkInterfaceID"`
	} `json:"NetworkInterfaces"`
}

func resolveInstanceRelationships(acct *account, st *store.Store) error {
	instances, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2Instance},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	// Build a (region, key-name) → key-pair resource ID index once. Instances
	// carry KeyName only; the KeyPair scanner stores NativeID by KeyPairId, so
	// we cannot rebuild the target ARN from name alone.
	keyPairs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2KeyPair},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	keyPairByNameRegion := make(map[string]string, len(keyPairs))
	for _, kp := range keyPairs {
		if kp.Name == nil {
			continue
		}
		keyPairByNameRegion[sv(kp.Region)+"\x00"+*kp.Name] = kp.ID
	}
	// Build an AMI id set from scanned images. Public/Marketplace/shared AMIs
	// aren't scanned, so instance→AMI edges are FK-safe only for AMIs we own.
	images, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2Image},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	imageByID := make(map[string]string, len(images))
	for _, img := range images {
		imageByID[img.ID] = img.ID
	}
	for _, r := range instances {
		var attrs instanceAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		// Instance → VPC
		if attrs.VpcID != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", *attrs.VpcID))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert instance→vpc relationship: %w", err)
			}
		}
		// Instance → Subnet
		if attrs.SubnetID != nil {
			subnetID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", *attrs.SubnetID))
			if err := st.UpsertRelationship(r.ID, subnetID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert instance→subnet relationship: %w", err)
			}
		}
		// Instance → Security Groups
		for _, sg := range attrs.SecurityGroups {
			if sg.GroupID != nil {
				sgID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", *sg.GroupID))
				if err := st.UpsertRelationship(r.ID, sgID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert instance→security-group relationship: %w", err)
				}
			}
		}
		// Instance → EBS Volumes (from block device mappings)
		for _, bdm := range attrs.BlockDeviceMappings {
			if bdm.Ebs != nil && bdm.Ebs.VolumeID != nil {
				volID := store.ResourceID("aws", acct.ID, TypeEC2Volume, ec2ARN(region, acct.ID, "volume", *bdm.Ebs.VolumeID))
				if err := st.UpsertRelationship(r.ID, volID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert instance→volume relationship: %w", err)
				}
			}
		}
		// Instance → IAM instance profile
		if attrs.IamInstanceProfile != nil && sv(attrs.IamInstanceProfile.Arn) != "" {
			ipID := store.ResourceID("aws", acct.ID, TypeIAMInstanceProfile, *attrs.IamInstanceProfile.Arn)
			if err := st.UpsertRelationship(r.ID, ipID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert instance→instance-profile relationship: %w", err)
			}
		}
		// Instance → KeyPair (name → id via index)
		if name := sv(attrs.KeyName); name != "" {
			if kpID, ok := keyPairByNameRegion[region+"\x00"+name]; ok {
				if err := st.UpsertRelationship(r.ID, kpID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert instance→key-pair relationship: %w", err)
				}
			}
		}
		// Instance → AMI (self-owned only; public/Marketplace AMIs skipped).
		if id := sv(attrs.ImageID); id != "" {
			amiID := store.ResourceID("aws", acct.ID, TypeEC2Image, ec2ARN(region, acct.ID, "image", id))
			if _, ok := imageByID[amiID]; ok {
				if err := st.UpsertRelationship(r.ID, amiID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert instance→image relationship: %w", err)
				}
			}
		}
		// Instance → Network Interfaces
		for _, eni := range attrs.NetworkInterfaces {
			if sv(eni.NetworkInterfaceID) == "" {
				continue
			}
			eniID := store.ResourceID("aws", acct.ID, TypeEC2NetworkInterface,
				ec2ARN(region, acct.ID, "network-interface", *eni.NetworkInterfaceID))
			if err := st.UpsertRelationship(r.ID, eniID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert instance→eni relationship: %w", err)
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
			GroupID *string `json:"GroupID"`
			VpcID   *string `json:"VpcID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.GroupID != nil {
			sgID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup,
				ec2ARN(region, acct.ID, "security-group", *attrs.GroupID))
			if err := st.UpsertRelationship(r.ID, sgID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert sg-vpc-assoc→sg relationship: %w", err)
			}
		}
		if attrs.VpcID != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC,
				ec2ARN(region, acct.ID, "vpc", *attrs.VpcID))
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
				return fmt.Errorf("upsert instance-connect-endpoint→subnet relationship: %w", err)
			}
		}
		if attrs.VpcID != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", *attrs.VpcID))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert instance-connect-endpoint→vpc relationship: %w", err)
			}
		}
	}
	return nil
}
