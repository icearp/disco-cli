package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveDocDBElasticClusterRefs,
		EdgeDecl{TypeDocDBElasticCluster, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeDocDBElasticCluster, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeDocDBElasticCluster, TypeEC2SecurityGroup, store.RelAttachedTo},
	)
}

// resolveDocDBElasticClusterRefs wires each elastic cluster to its CMEK,
// VPC subnets, and security groups. GetCluster body shape carries
// KmsKeyId / SubnetIds / VpcSecurityGroupIds.
func resolveDocDBElasticClusterRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeDocDBElasticCluster}, Limit: util.AllResources,
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
			tgt := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", sn))
			if !subnetSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert docdb-elastic→subnet: %w", err)
			}
		}
		for _, sg := range attrs.VpcSecurityGroupIDs {
			tgt := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", sg))
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
