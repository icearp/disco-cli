package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveCodeBuildProjectRefs,
		EdgeDecl{TypeCodeBuildProject, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeCodeBuildProject, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeCodeBuildProject, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeCodeBuildProject, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeCodeBuildProject, TypeEC2SecurityGroup, store.RelAttachedTo},
		EdgeDecl{TypeCodeBuildProject, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeCodeBuildProject, TypeLogsLogGroup, store.RelUses},
	)
	registerResolver(
		resolveCodeBuildFleetRefs,
		EdgeDecl{TypeCodeBuildFleet, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeCodeBuildFleet, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeCodeBuildFleet, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeCodeBuildFleet, TypeEC2SecurityGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveCodeBuildReportGroupRefs,
		EdgeDecl{TypeCodeBuildReportGroup, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeCodeBuildReportGroup, TypeS3Bucket, store.RelUses},
	)
}

// resolveCodeBuildFleetRefs walks each enriched fleet (BatchGetFleets) and
// emits FleetServiceRole (IAM) + VpcConfig (VPC, subnets, SGs).
func resolveCodeBuildFleetRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCodeBuildFleet}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
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
			FleetServiceRole *string `json:"FleetServiceRole"`
			VpcConfig        *struct {
				VpcID            *string  `json:"VpcId"`
				Subnets          []string `json:"Subnets"`
				SecurityGroupIDs []string `json:"SecurityGroupIds"`
			} `json:"VpcConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if rarn := sv(attrs.FleetServiceRole); strings.Contains(rarn, ":role/") {
			tgt := store.ResourceID("aws", acct.ID, TypeIAMRole, rarn)
			if roleSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert cb-fleet→role: %w", err)
				}
			}
		}
		if attrs.VpcConfig != nil {
			if v := sv(attrs.VpcConfig.VpcID); v != "" {
				tgt := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", v))
				if vpcSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert cb-fleet→vpc: %w", err)
					}
				}
			}
			for _, sn := range attrs.VpcConfig.Subnets {
				tgt := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", sn))
				if !subnetSet[tgt] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert cb-fleet→subnet: %w", err)
				}
			}
			for _, sg := range attrs.VpcConfig.SecurityGroupIDs {
				tgt := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", sg))
				if !sgSet[tgt] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert cb-fleet→sg: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveCodeBuildReportGroupRefs walks each enriched report-group
// (BatchGetReportGroups) and emits ExportConfig.S3Destination edges:
// EncryptionKey (KMS) and Bucket (S3).
func resolveCodeBuildReportGroupRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCodeBuildReportGroup}, Limit: util.AllResources,
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
	bucketSet, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ExportConfig *struct {
				S3Destination *struct {
					Bucket        *string `json:"Bucket"`
					EncryptionKey *string `json:"EncryptionKey"`
				} `json:"S3Destination"`
			} `json:"ExportConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ExportConfig == nil || attrs.ExportConfig.S3Destination == nil {
			continue
		}
		s3 := attrs.ExportConfig.S3Destination
		if ref := sv(s3.EncryptionKey); ref != "" {
			if keyID, ok := idx.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert cb-rg→kms: %w", err)
				}
			}
		}
		if b := sv(s3.Bucket); b != "" {
			barn := "arn:aws:s3:::" + b
			tgt := store.ResourceID("aws", acct.ID, TypeS3Bucket, barn)
			if bucketSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert cb-rg→s3: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveCodeBuildProjectRefs walks each enriched CodeBuild project body
// (BatchGetProjects-populated) and emits source-side edges for ServiceRole
// (IAM), EncryptionKey (KMS), VpcConfig (VPC + subnet + SG), Artifacts/
// SecondaryArtifacts/Cache (S3 bucket via Location field when type S3),
// LogsConfig.CloudWatchLogs (log group), LogsConfig.S3Logs (S3 bucket).
// All edges FK-safe.
func resolveCodeBuildProjectRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCodeBuildProject},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
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
	bucketSet, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	lgSet, err := scannedIDSet(acct, st, TypeLogsLogGroup)
	if err != nil {
		return err
	}
	kidx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	type artifacts struct {
		Type     *string `json:"Type"`
		Location *string `json:"Location"`
	}
	for _, r := range rows {
		var attrs struct {
			ServiceRole        *string `json:"ServiceRole"`
			ResourceAccessRole *string `json:"ResourceAccessRole"`
			EncryptionKey      *string `json:"EncryptionKey"`
			VpcConfig          *struct {
				VpcID            *string  `json:"VpcId"`
				Subnets          []string `json:"Subnets"`
				SecurityGroupIDs []string `json:"SecurityGroupIds"`
			} `json:"VpcConfig"`
			Artifacts          *artifacts  `json:"Artifacts"`
			SecondaryArtifacts []artifacts `json:"SecondaryArtifacts"`
			Cache              *artifacts  `json:"Cache"`
			LogsConfig         *struct {
				CloudWatchLogs *struct {
					Status    *string `json:"Status"`
					GroupName *string `json:"GroupName"`
				} `json:"CloudWatchLogs"`
				S3Logs *struct {
					Status   *string `json:"Status"`
					Location *string `json:"Location"`
				} `json:"S3Logs"`
			} `json:"LogsConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		emitRole := func(arn string) error {
			if arn == "" {
				return nil
			}
			tgt := store.ResourceID("aws", acct.ID, TypeIAMRole, arn)
			if !roleSet[tgt] {
				return nil
			}
			return st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil)
		}
		if err := emitRole(sv(attrs.ServiceRole)); err != nil {
			return fmt.Errorf("upsert codebuild project→role: %w", err)
		}
		if err := emitRole(sv(attrs.ResourceAccessRole)); err != nil {
			return fmt.Errorf("upsert codebuild project→resource-access-role: %w", err)
		}
		if k := sv(attrs.EncryptionKey); k != "" {
			if keyID, ok := kidx.resolveKMSKeyID(k, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert codebuild project→kms: %w", err)
				}
			}
		}
		if vc := attrs.VpcConfig; vc != nil {
			if vpc := sv(vc.VpcID); vpc != "" {
				vpcARN := ec2ARN(region, acct.ID, "vpc", vpc)
				tgt := store.ResourceID("aws", acct.ID, TypeEC2VPC, vpcARN)
				if vpcSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert codebuild project→vpc: %w", err)
					}
				}
			}
			for _, sn := range vc.Subnets {
				if sn == "" {
					continue
				}
				snARN := ec2ARN(region, acct.ID, "subnet", sn)
				tgt := store.ResourceID("aws", acct.ID, TypeEC2Subnet, snARN)
				if !subnetSet[tgt] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert codebuild project→subnet: %w", err)
				}
			}
			for _, sg := range vc.SecurityGroupIDs {
				if sg == "" {
					continue
				}
				sgARN := ec2ARN(region, acct.ID, "security-group", sg)
				tgt := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, sgARN)
				if !sgSet[tgt] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert codebuild project→sg: %w", err)
				}
			}
		}
		emitS3 := func(bucketName string) error {
			if bucketName == "" {
				return nil
			}
			// Cache/Artifacts.Location for S3 type carries `bucket-name/path`;
			// strip path for bucket lookup.
			if i := strings.IndexByte(bucketName, '/'); i >= 0 {
				bucketName = bucketName[:i]
			}
			bArn := "arn:aws:s3:::" + bucketName
			tgt := store.ResourceID("aws", acct.ID, TypeS3Bucket, bArn)
			if !bucketSet[tgt] {
				return nil
			}
			return st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil)
		}
		s3Loc := func(a *artifacts) string {
			if a == nil || sv(a.Type) != "S3" {
				return ""
			}
			return sv(a.Location)
		}
		if err := emitS3(s3Loc(attrs.Artifacts)); err != nil {
			return fmt.Errorf("upsert codebuild project→artifacts-s3: %w", err)
		}
		for i := range attrs.SecondaryArtifacts {
			if err := emitS3(s3Loc(&attrs.SecondaryArtifacts[i])); err != nil {
				return fmt.Errorf("upsert codebuild project→secondary-artifacts-s3: %w", err)
			}
		}
		if err := emitS3(s3Loc(attrs.Cache)); err != nil {
			return fmt.Errorf("upsert codebuild project→cache-s3: %w", err)
		}
		if lc := attrs.LogsConfig; lc != nil {
			if lc.CloudWatchLogs != nil && sv(lc.CloudWatchLogs.Status) != "DISABLED" {
				if name := sv(lc.CloudWatchLogs.GroupName); name != "" {
					lgARN := logGroupNativeIDFromName(acct.ID, region, name)
					tgt := store.ResourceID("aws", acct.ID, TypeLogsLogGroup, lgARN)
					if lgSet[tgt] {
						if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
							return fmt.Errorf("upsert codebuild project→log-group: %w", err)
						}
					}
				}
			}
			if lc.S3Logs != nil && sv(lc.S3Logs.Status) != "DISABLED" {
				if err := emitS3(sv(lc.S3Logs.Location)); err != nil {
					return fmt.Errorf("upsert codebuild project→s3-logs: %w", err)
				}
			}
		}
	}
	return nil
}
