package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveDocDBElasticClusterRefs,
		EdgeDecl{TypeDocDBElasticCluster, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeDocDBElasticCluster, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeDocDBElasticCluster, TypeEC2SecurityGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveDocDBElasticSnapshotRefs,
		EdgeDecl{TypeDocDBElasticClusterSnapshot, TypeDocDBElasticCluster, store.RelAttachedTo},
		EdgeDecl{TypeDocDBElasticClusterSnapshot, TypeKMSKey, store.RelUses},
	)
}

// resolveDocDBElasticSnapshotRefs wires each snapshot to its source cluster
// (FK-safe — snapshots outlive deleted clusters) and its encrypting KMS key.
// GetClusterSnapshot carries ClusterArn / KmsKeyId.
func resolveDocDBElasticSnapshotRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDocDBElasticClusterSnapshot}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	clusterSet, err := scannedIDSet(acct, st, TypeDocDBElasticCluster)
	if err != nil {
		return err
	}
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ClusterArn *string `json:"ClusterArn"`
			KmsKeyID   *string `json:"KmsKeyId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if c := sv(attrs.ClusterArn); c != "" {
			tgtID := store.ResourceID("aws", acct.ID, c)
			if clusterSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert docdb-elastic snapshot→cluster: %w", err)
				}
			}
		}
		if ref := sv(attrs.KmsKeyID); ref != "" {
			if keyID, ok := idx.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert docdb-elastic snapshot→kms: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveDocDBElasticClusterRefs wires each elastic cluster to its CMEK,
// VPC subnets, and security groups. GetCluster carries KmsKeyId / SubnetIds
// / VpcSecurityGroupIds.
func resolveDocDBElasticClusterRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDocDBElasticCluster}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	subnetSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KmsKeyID            *string  `json:"KmsKeyId"`
			SubnetIDs           []string `json:"SubnetIds"`
			VpcSecurityGroupIDs []string `json:"VpcSecurityGroupIds"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if ref := sv(attrs.KmsKeyID); ref != "" {
			if keyID, ok := idx.resolveKMSKeyID(ref, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert docdb-elastic→kms: %w", err)
				}
			}
		}
		for _, sn := range attrs.SubnetIDs {
			tgt := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "subnet", sn))
			if !subnetSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert docdb-elastic→subnet: %w", err)
			}
		}
		for _, sg := range attrs.VpcSecurityGroupIDs {
			tgt := store.ResourceID("aws", acct.ID, ec2ARN(region, acct.ID, "security-group", sg))
			if !sgSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert docdb-elastic→sg: %w", err)
			}
		}
	}
	return nil
}
