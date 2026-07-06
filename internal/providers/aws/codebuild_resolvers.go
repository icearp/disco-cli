package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCodeBuildFleet}, Limit: util.AllResources,
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCodeBuildReportGroup}, Limit: util.AllResources,
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

// codeBuildArtifacts mirrors the project's Artifacts / Cache shape — Type
// flags S3-vs-other, Location is the bucket-or-path string.
type codeBuildArtifacts struct {
	Type     *string `json:"Type"`
	Location *string `json:"Location"`
}

// codeBuildProjectAttrs mirrors the BatchGetProjects fields the resolver
// walks. PascalCase JSON tags match mustJSON of the SDK struct.
type codeBuildProjectAttrs struct {
	ServiceRole        *string `json:"ServiceRole"`
	ResourceAccessRole *string `json:"ResourceAccessRole"`
	EncryptionKey      *string `json:"EncryptionKey"`
	VpcConfig          *struct {
		VpcID            *string  `json:"VpcId"`
		Subnets          []string `json:"Subnets"`
		SecurityGroupIDs []string `json:"SecurityGroupIds"`
	} `json:"VpcConfig"`
	Artifacts          *codeBuildArtifacts  `json:"Artifacts"`
	SecondaryArtifacts []codeBuildArtifacts `json:"SecondaryArtifacts"`
	Cache              *codeBuildArtifacts  `json:"Cache"`
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

// codeBuildTargetSets bundles the FK-safe target id sets so per-edge-kind
// helpers below take a single struct rather than seven maps.
type codeBuildTargetSets struct {
	roleSet   map[string]bool
	vpcSet    map[string]bool
	subnetSet map[string]bool
	sgSet     map[string]bool
	bucketSet map[string]bool
	lgSet     map[string]bool
	kidx      *kmsResolveIndex
}

// resolveCodeBuildProjectRefs walks each enriched CodeBuild project body
// (BatchGetProjects) and emits edges for ServiceRole (IAM), EncryptionKey
// (KMS), VpcConfig (VPC + subnet + SG), Artifacts/SecondaryArtifacts/Cache
// (S3 via Location when type S3), LogsConfig.CloudWatchLogs (log group),
// and LogsConfig.S3Logs (S3). All edges FK-safe.
func resolveCodeBuildProjectRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCodeBuildProject},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	sets, err := loadCodeBuildTargetSets(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs codeBuildProjectAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if err := emitCodeBuildRoleEdges(st, acct, r, attrs, sets); err != nil {
			return err
		}
		if err := emitCodeBuildKMSEdge(st, acct, r, region, attrs, sets); err != nil {
			return err
		}
		if err := emitCodeBuildVPCEdges(st, acct, r, region, attrs, sets); err != nil {
			return err
		}
		if err := emitCodeBuildS3Edges(st, acct, r, attrs, sets); err != nil {
			return err
		}
		if err := emitCodeBuildLogsEdges(st, acct, r, region, attrs, sets); err != nil {
			return err
		}
	}
	return nil
}

func loadCodeBuildTargetSets(acct *account, st *store.Store) (codeBuildTargetSets, error) {
	var sets codeBuildTargetSets
	var err error
	if sets.roleSet, err = scannedIDSet(acct, st, TypeIAMRole); err != nil {
		return sets, err
	}
	if sets.vpcSet, err = scannedIDSet(acct, st, TypeEC2VPC); err != nil {
		return sets, err
	}
	if sets.subnetSet, err = scannedIDSet(acct, st, TypeEC2Subnet); err != nil {
		return sets, err
	}
	if sets.sgSet, err = scannedIDSet(acct, st, TypeEC2SecurityGroup); err != nil {
		return sets, err
	}
	if sets.bucketSet, err = scannedIDSet(acct, st, TypeS3Bucket); err != nil {
		return sets, err
	}
	if sets.lgSet, err = scannedIDSet(acct, st, TypeLogsLogGroup); err != nil {
		return sets, err
	}
	if sets.kidx, err = loadKMSResolveIndex(acct, st); err != nil {
		return sets, err
	}
	return sets, nil
}

func emitCodeBuildRoleEdges(st *store.Store, acct *account, r store.Resource, attrs codeBuildProjectAttrs, sets codeBuildTargetSets) error {
	for _, pair := range []struct {
		arn string
		tag string
	}{
		{sv(attrs.ServiceRole), "role"},
		{sv(attrs.ResourceAccessRole), "resource-access-role"},
	} {
		if pair.arn == "" {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, TypeIAMRole, pair.arn)
		if !sets.roleSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
			return fmt.Errorf("upsert codebuild project→%s: %w", pair.tag, err)
		}
	}
	return nil
}

func emitCodeBuildKMSEdge(st *store.Store, acct *account, r store.Resource, region string, attrs codeBuildProjectAttrs, sets codeBuildTargetSets) error {
	k := sv(attrs.EncryptionKey)
	if k == "" {
		return nil
	}
	keyID, ok := sets.kidx.resolveKMSKeyID(k, region, acct.ID)
	if !ok {
		return nil
	}
	if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
		return fmt.Errorf("upsert codebuild project→kms: %w", err)
	}
	return nil
}

func emitCodeBuildVPCEdges(st *store.Store, acct *account, r store.Resource, region string, attrs codeBuildProjectAttrs, sets codeBuildTargetSets) error {
	vc := attrs.VpcConfig
	if vc == nil {
		return nil
	}
	if vpc := sv(vc.VpcID); vpc != "" {
		tgt := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", vpc))
		if sets.vpcSet[tgt] {
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert codebuild project→vpc: %w", err)
			}
		}
	}
	for _, sn := range vc.Subnets {
		if sn == "" {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", sn))
		if !sets.subnetSet[tgt] {
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
		tgt := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", sg))
		if !sets.sgSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert codebuild project→sg: %w", err)
		}
	}
	return nil
}

// codeBuildArtifactsLocation returns the S3 Location field iff the
// artifacts entry is type-S3; empty otherwise (no edge to emit).
func codeBuildArtifactsLocation(a *codeBuildArtifacts) string {
	if a == nil || sv(a.Type) != "S3" {
		return ""
	}
	return sv(a.Location)
}

func emitCodeBuildBucketEdge(st *store.Store, acct *account, r store.Resource, sets codeBuildTargetSets, bucketName, tag string) error {
	if bucketName == "" {
		return nil
	}
	// Cache/Artifacts.Location for S3 type carries `bucket-name/path`;
	// strip path for bucket lookup.
	if i := strings.IndexByte(bucketName, '/'); i >= 0 {
		bucketName = bucketName[:i]
	}
	tgt := store.ResourceID("aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+bucketName)
	if !sets.bucketSet[tgt] {
		return nil
	}
	if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
		return fmt.Errorf("upsert codebuild project→%s: %w", tag, err)
	}
	return nil
}

func emitCodeBuildS3Edges(st *store.Store, acct *account, r store.Resource, attrs codeBuildProjectAttrs, sets codeBuildTargetSets) error {
	if err := emitCodeBuildBucketEdge(st, acct, r, sets, codeBuildArtifactsLocation(attrs.Artifacts), "artifacts-s3"); err != nil {
		return err
	}
	for i := range attrs.SecondaryArtifacts {
		if err := emitCodeBuildBucketEdge(st, acct, r, sets, codeBuildArtifactsLocation(&attrs.SecondaryArtifacts[i]), "secondary-artifacts-s3"); err != nil {
			return err
		}
	}
	return emitCodeBuildBucketEdge(st, acct, r, sets, codeBuildArtifactsLocation(attrs.Cache), "cache-s3")
}

func emitCodeBuildLogsEdges(st *store.Store, acct *account, r store.Resource, region string, attrs codeBuildProjectAttrs, sets codeBuildTargetSets) error {
	lc := attrs.LogsConfig
	if lc == nil {
		return nil
	}
	if lc.CloudWatchLogs != nil && sv(lc.CloudWatchLogs.Status) != "DISABLED" {
		if name := sv(lc.CloudWatchLogs.GroupName); name != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeLogsLogGroup, logGroupNativeIDFromName(acct.ID, region, name))
			if sets.lgSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert codebuild project→log-group: %w", err)
				}
			}
		}
	}
	if lc.S3Logs != nil && sv(lc.S3Logs.Status) != "DISABLED" {
		if err := emitCodeBuildBucketEdge(st, acct, r, sets, sv(lc.S3Logs.Location), "s3-logs"); err != nil {
			return err
		}
	}
	return nil
}
