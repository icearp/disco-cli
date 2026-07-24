package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveInstanceRelationships,
		EdgeDecl{TypeEC2Instance, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeEC2Instance, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeEC2Instance, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeEC2Instance, TypeEC2Volume, store.RelAttachedTo},
		EdgeDecl{TypeEC2Instance, TypeIAMInstanceProfile, store.RelUses},
		EdgeDecl{TypeEC2Instance, TypeEC2KeyPair, store.RelUses},
		EdgeDecl{TypeEC2Instance, TypeEC2Image, store.RelUses},
		EdgeDecl{TypeEC2Instance, TypeEC2NetworkInterface, store.RelAttachedTo},
	)
	registerResolver(
		resolveInstanceConnectEndpointRelationships,
		EdgeDecl{TypeEC2InstanceConnectEndpoint, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeEC2InstanceConnectEndpoint, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(
		resolveSecurityGroupVPCAssociationRelationships,
		EdgeDecl{TypeEC2SecurityGroupVPCAssociation, TypeEC2SecurityGroup, store.RelAttachedTo},
		EdgeDecl{TypeEC2SecurityGroupVPCAssociation, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(
		resolveSnapshotRelationships,
		EdgeDecl{TypeEC2Snapshot, TypeEC2Volume, store.RelAttachedTo},
		EdgeDecl{TypeEC2Snapshot, TypeKMSKey, store.RelUses},
	)
}

// resolveSnapshotRelationships wires each EBS snapshot to its source volume
// (FK-safe — snapshots outlive deleted volumes) and the KMS key that
// encrypts it.
func resolveSnapshotRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2Snapshot}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	volSet, err := scannedIDSet(acct, st, TypeEC2Volume)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			VolumeID *string `json:"VolumeId"`
			KmsKeyID *string `json:"KmsKeyId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if v := sv(attrs.VolumeID); v != "" {
			tgtID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "volume", v))
			if volSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ec2 snapshot→volume: %w", err)
				}
			}
		}
		if ref := sv(attrs.KmsKeyID); ref != "" {
			if keyID, ok := kmsIdx.resolveKMSKeyID(ref, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ec2 snapshot→kms: %w", err)
				}
			}
		}
	}
	return nil
}

// instanceAttrs captures the fields used from an EC2 instance's JSON blob.
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

// instanceTargetSets bundles the FK-safe id sets the instance resolver uses.
type instanceTargetSets struct {
	keyPairByNameRegion map[string]string
	imageByID           map[string]string
}

func resolveInstanceRelationships(acct *account, st *store.Store) error {
	instances, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2Instance},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	sets, err := loadInstanceTargetSets(acct, st)
	if err != nil {
		return err
	}
	for _, r := range instances {
		var attrs instanceAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if err := emitInstanceVPCSubnetEdges(st, acct, r, region, attrs); err != nil {
			return err
		}
		if err := emitInstanceSGEdges(st, acct, r, region, attrs); err != nil {
			return err
		}
		if err := emitInstanceVolumeEdges(st, acct, r, region, attrs); err != nil {
			return err
		}
		if err := emitInstanceProfileEdge(st, acct, r, attrs); err != nil {
			return err
		}
		if err := emitInstanceKeyPairEdge(st, r, region, attrs, sets); err != nil {
			return err
		}
		if err := emitInstanceImageEdge(st, acct, r, region, attrs, sets); err != nil {
			return err
		}
		if err := emitInstanceENIEdges(st, acct, r, region, attrs); err != nil {
			return err
		}
	}
	return nil
}

func loadInstanceTargetSets(acct *account, st *store.Store) (instanceTargetSets, error) {
	var sets instanceTargetSets
	// (region, key-name) → key-pair resource ID. Instances carry KeyName
	// only; KeyPair NativeID uses KeyPairId so the ARN can't be rebuilt
	// from the name alone.
	keyPairs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2KeyPair},
		Limit: util.AllResources,
	})
	if err != nil {
		return sets, err
	}
	sets.keyPairByNameRegion = make(map[string]string, len(keyPairs))
	for _, kp := range keyPairs {
		if kp.Name == nil {
			continue
		}
		sets.keyPairByNameRegion[sv(kp.Region)+"\x00"+*kp.Name] = kp.ID
	}
	// AMI id set from scanned images. Public/Marketplace/shared AMIs aren't
	// scanned, so instance→AMI edges are FK-safe only for AMIs we own.
	images, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2Image},
		Limit: util.AllResources,
	})
	if err != nil {
		return sets, err
	}
	sets.imageByID = make(map[string]string, len(images))
	for _, img := range images {
		sets.imageByID[img.ID] = img.ID
	}
	return sets, nil
}

func emitInstanceVPCSubnetEdges(st *store.Store, acct *account, r store.Resource, region string, attrs instanceAttrs) error {
	if attrs.VpcID != nil {
		vpcID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "vpc", *attrs.VpcID))
		if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert instance→vpc relationship: %w", err)
		}
	}
	if attrs.SubnetID != nil {
		subnetID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "subnet", *attrs.SubnetID))
		if err := st.UpsertRelationship(r.ID, subnetID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert instance→subnet relationship: %w", err)
		}
	}
	return nil
}

func emitInstanceSGEdges(st *store.Store, acct *account, r store.Resource, region string, attrs instanceAttrs) error {
	for _, sg := range attrs.SecurityGroups {
		if sg.GroupID == nil {
			continue
		}
		sgID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "security-group", *sg.GroupID))
		if err := st.UpsertRelationship(r.ID, sgID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert instance→security-group relationship: %w", err)
		}
	}
	return nil
}

func emitInstanceVolumeEdges(st *store.Store, acct *account, r store.Resource, region string, attrs instanceAttrs) error {
	for _, bdm := range attrs.BlockDeviceMappings {
		if bdm.Ebs == nil || bdm.Ebs.VolumeID == nil {
			continue
		}
		volID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "volume", *bdm.Ebs.VolumeID))
		if err := st.UpsertRelationship(r.ID, volID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert instance→volume relationship: %w", err)
		}
	}
	return nil
}

func emitInstanceProfileEdge(st *store.Store, acct *account, r store.Resource, attrs instanceAttrs) error {
	if attrs.IamInstanceProfile == nil || sv(attrs.IamInstanceProfile.Arn) == "" {
		return nil
	}
	ipID := store.ResourceID("aws", acct.ID, *attrs.IamInstanceProfile.Arn)
	if err := st.UpsertRelationship(r.ID, ipID, store.RelUses, "directed", nil); err != nil {
		return fmt.Errorf("upsert instance→instance-profile relationship: %w", err)
	}
	return nil
}

func emitInstanceKeyPairEdge(st *store.Store, r store.Resource, region string, attrs instanceAttrs, sets instanceTargetSets) error {
	name := sv(attrs.KeyName)
	if name == "" {
		return nil
	}
	kpID, ok := sets.keyPairByNameRegion[region+"\x00"+name]
	if !ok {
		return nil
	}
	if err := st.UpsertRelationship(r.ID, kpID, store.RelUses, "directed", nil); err != nil {
		return fmt.Errorf("upsert instance→key-pair relationship: %w", err)
	}
	return nil
}

// emitInstanceImageEdge emits instance→AMI only for self-owned AMIs;
// public/Marketplace AMIs aren't scanned and skip silently.
func emitInstanceImageEdge(st *store.Store, acct *account, r store.Resource, region string, attrs instanceAttrs, sets instanceTargetSets) error {
	id := sv(attrs.ImageID)
	if id == "" {
		return nil
	}
	amiID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "image", id))
	if _, ok := sets.imageByID[amiID]; !ok {
		return nil
	}
	if err := st.UpsertRelationship(r.ID, amiID, store.RelUses, "directed", nil); err != nil {
		return fmt.Errorf("upsert instance→image relationship: %w", err)
	}
	return nil
}

func emitInstanceENIEdges(st *store.Store, acct *account, r store.Resource, region string, attrs instanceAttrs) error {
	for _, eni := range attrs.NetworkInterfaces {
		if sv(eni.NetworkInterfaceID) == "" {
			continue
		}
		eniID := store.ResourceID("aws", acct.ID,
			ec2ARN(region, acct.ID, "network-interface", *eni.NetworkInterfaceID))

		if err := st.UpsertRelationship(r.ID, eniID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert instance→eni relationship: %w", err)
		}
	}
	return nil
}

// resolveSecurityGroupVPCAssociationRelationships links each SG VPC association to its SG and VPC.
func resolveSecurityGroupVPCAssociationRelationships(acct *account, st *store.Store) error {
	assocs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2SecurityGroupVPCAssociation},
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
			sgID := store.ResourceID("aws", acct.ID,
				ec2ARN(region, acct.ID, "security-group", *attrs.GroupID))

			if err := st.UpsertRelationship(r.ID, sgID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert sg-vpc-assoc→sg relationship: %w", err)
			}
		}
		if attrs.VpcID != nil {
			vpcID := store.ResourceID("aws", acct.ID,
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2InstanceConnectEndpoint},
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
			subnetID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "subnet", *attrs.SubnetID))
			if err := st.UpsertRelationship(r.ID, subnetID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert instance-connect-endpoint→subnet relationship: %w", err)
			}
		}
		if attrs.VpcID != nil {
			vpcID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "vpc", *attrs.VpcID))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert instance-connect-endpoint→vpc relationship: %w", err)
			}
		}
	}
	return nil
}
