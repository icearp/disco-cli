package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveDRSRecoveryInstanceRefs,
		EdgeDecl{TypeDRSRecoveryInstanceResource, TypeEC2Instance, store.RelUses},
		EdgeDecl{TypeDRSRecoveryInstanceResource, TypeDRSSourceServerResource, store.RelAttachedTo},
	)
	registerResolver(
		resolveDRSSourceServerRefs,
		EdgeDecl{TypeDRSSourceServerResource, TypeDRSSourceNetworkResource, store.RelAttachedTo},
	)
	registerResolver(
		resolveDRSSourceNetworkRefs,
		EdgeDecl{TypeDRSSourceNetworkResource, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(
		resolveDRSReplicationTemplateRefs,
		EdgeDecl{TypeDRSReplicationConfigurationTemplateResource, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeDRSReplicationConfigurationTemplateResource, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeDRSReplicationConfigurationTemplateResource, TypeEC2SecurityGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveDRSLaunchTemplateRefs,
		EdgeDecl{TypeDRSLaunchConfigurationTemplateResource, TypeS3Bucket, store.RelUses},
	)
}

// drsIDIndex maps a DRS child id (from each scanned row's attrs) to its
// resource ID, so resolvers can resolve a bare id reference without
// reconstructing ARNs.
func drsIDIndex(acct *account, st *store.Store, rtype, idKey string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{rtype}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		var attrs map[string]any
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id, ok := attrs[idKey].(string); ok && id != "" {
			idx[id] = r.ID
		}
	}
	return idx, nil
}

// resolveDRSRecoveryInstanceRefs wires each recovery instance to the EC2
// instance it launched and to the source server it recovers.
func resolveDRSRecoveryInstanceRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDRSRecoveryInstanceResource}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	ec2Set, err := scannedIDSet(acct, st, TypeEC2Instance)
	if err != nil {
		return err
	}
	srcServerIdx, err := drsIDIndex(acct, st, TypeDRSSourceServerResource, "SourceServerID")
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Ec2InstanceID  *string `json:"Ec2InstanceID"`
			SourceServerID *string `json:"SourceServerID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.Ec2InstanceID); id != "" {
			tgtID := store.ResourceID("aws", acct.ID, ec2ARN(sv(r.Region), acct.ID, "instance", id))
			if ec2Set[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert drs recovery-instance→ec2: %w", err)
				}
			}
		}
		if id := sv(attrs.SourceServerID); id != "" {
			if tgtID, ok := srcServerIdx[id]; ok {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert drs recovery-instance→source-server: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveDRSSourceServerRefs wires each source server to the source network it
// belongs to.
func resolveDRSSourceServerRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDRSSourceServerResource}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	srcNetIdx, err := drsIDIndex(acct, st, TypeDRSSourceNetworkResource, "SourceNetworkID")
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			SourceNetworkID *string `json:"SourceNetworkID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.SourceNetworkID); id != "" {
			if tgtID, ok := srcNetIdx[id]; ok {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert drs source-server→source-network: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveDRSSourceNetworkRefs wires each source network to the VPC it was
// recovered into (LaunchedVpcID — the source VPC lives in another account/region
// and is not scanned here).
func resolveDRSSourceNetworkRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDRSSourceNetworkResource}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			LaunchedVpcID *string `json:"LaunchedVpcID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.LaunchedVpcID); id != "" {
			tgtID := store.ResourceID("aws", acct.ID, ec2ARN(sv(r.Region), acct.ID, "vpc", id))
			if vpcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert drs source-network→vpc: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveDRSReplicationTemplateRefs wires each replication-configuration
// template to its EBS encryption key, staging subnet, and replication-server
// security groups.
func resolveDRSReplicationTemplateRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDRSReplicationConfigurationTemplateResource}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	subSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			EbsEncryptionKeyArn                 *string  `json:"EbsEncryptionKeyArn"`
			StagingAreaSubnetID                 *string  `json:"StagingAreaSubnetId"`
			ReplicationServersSecurityGroupsIDs []string `json:"ReplicationServersSecurityGroupsIDs"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if ref := sv(attrs.EbsEncryptionKeyArn); ref != "" {
			if keyID, ok := kmsIdx.resolveKMSKeyID(ref, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert drs replication-template→kms: %w", err)
				}
			}
		}
		if sn := sv(attrs.StagingAreaSubnetID); sn != "" {
			tgtID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "subnet", sn))
			if subSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert drs replication-template→subnet: %w", err)
				}
			}
		}
		for _, sg := range attrs.ReplicationServersSecurityGroupsIDs {
			if sg == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "security-group", sg))
			if sgSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert drs replication-template→sg: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveDRSLaunchTemplateRefs wires each launch-configuration template to the
// S3 bucket it exports recovery artifacts to.
func resolveDRSLaunchTemplateRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDRSLaunchConfigurationTemplateResource}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	bucketSet, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ExportBucketArn *string `json:"ExportBucketArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if b := sv(attrs.ExportBucketArn); b != "" {
			tgtID := store.ResourceID("aws", acct.ID, b)
			if bucketSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert drs launch-template→s3: %w", err)
				}
			}
		}
	}
	return nil
}
