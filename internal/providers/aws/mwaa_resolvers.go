package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveMWAAEnvironmentRefs,
		EdgeDecl{TypeMWAAEnvironment, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeMWAAEnvironment, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeMWAAEnvironment, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeMWAAEnvironment, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeMWAAEnvironment, TypeEC2SecurityGroup, store.RelAttachedTo},
	)
}

// resolveMWAAEnvironmentRefs wires each Airflow environment to its
// execution + service IAM roles, KMS CMEK, S3 source bucket, VPC subnets
// and security groups. GetEnvironment body shape.
func resolveMWAAEnvironmentRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeMWAAEnvironment}, Limit: util.AllResources,
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
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	bucketSet, err := scannedIDSet(acct, st, TypeS3Bucket)
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
			ExecutionRoleArn     *string `json:"ExecutionRoleArn"`
			ServiceRoleArn       *string `json:"ServiceRoleArn"`
			KmsKey               *string `json:"KmsKey"`
			SourceBucketArn      *string `json:"SourceBucketArn"`
			NetworkConfiguration *struct {
				SubnetIDs        []string `json:"SubnetIds"`
				SecurityGroupIDs []string `json:"SecurityGroupIds"`
			} `json:"NetworkConfiguration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		for _, ra := range []*string{attrs.ExecutionRoleArn, attrs.ServiceRoleArn} {
			rarn := sv(ra)
			if !strings.Contains(rarn, ":role/") {
				continue
			}
			tgt := store.ResourceID("aws", acct.ID, TypeIAMRole, rarn)
			if !roleSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
				return fmt.Errorf("upsert mwaa-env→role: %w", err)
			}
		}
		if ref := sv(attrs.KmsKey); ref != "" {
			if keyID, ok := idx.resolveKMSKeyID(ref, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert mwaa-env→kms: %w", err)
				}
			}
		}
		if barn := sv(attrs.SourceBucketArn); strings.HasPrefix(barn, "arn:aws:s3:::") {
			tgt := store.ResourceID("aws", acct.ID, TypeS3Bucket, barn)
			if bucketSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert mwaa-env→s3: %w", err)
				}
			}
		}
		if attrs.NetworkConfiguration != nil {
			for _, sn := range attrs.NetworkConfiguration.SubnetIDs {
				tgt := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", sn))
				if !subnetSet[tgt] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert mwaa-env→subnet: %w", err)
				}
			}
			for _, sg := range attrs.NetworkConfiguration.SecurityGroupIDs {
				tgt := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", sg))
				if !sgSet[tgt] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert mwaa-env→sg: %w", err)
				}
			}
		}
	}
	return nil
}
