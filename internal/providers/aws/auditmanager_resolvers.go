package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveAuditManagerAssessmentTargets,
		EdgeDecl{TypeAuditManagerAssessment, TypeAuditManagerFramework, store.RelUses},
		EdgeDecl{TypeAuditManagerAssessment, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeAuditManagerAssessment, TypeS3Bucket, store.RelUses},
	)
}

// auditManagerAssessmentAttrs mirrors the verbatim Assessment fields used
// by the resolver. PascalCase tags match mustJSON of the SDK v2 struct.
type auditManagerAssessmentAttrs struct {
	Framework *struct {
		Arn *string `json:"Arn"`
	} `json:"Framework"`
	Metadata *struct {
		Roles []struct {
			RoleArn *string `json:"RoleArn"`
		} `json:"Roles"`
		AssessmentReportsDestination *struct {
			Destination     *string `json:"Destination"`
			DestinationType *string `json:"DestinationType"`
		} `json:"AssessmentReportsDestination"`
	} `json:"Metadata"`
}

// resolveAuditManagerAssessmentTargets emits the assessment's outbound edges:
//   - assessment → framework (uses) via Framework.Arn
//   - assessment → IAM role (assumes) per Metadata.Roles[]
//   - assessment → S3 bucket (uses) via Metadata.AssessmentReportsDestination
//     (only when DestinationType == S3)
//
// FK-safe via per-type id sets. Cross-account / unscanned targets skip.
func resolveAuditManagerAssessmentTargets(acct *account, st *store.Store) error {
	assessments, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeAuditManagerAssessment},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(assessments) == 0 {
		return nil
	}

	frameworkIDs, err := resourceIDSet(st, acct.ID, TypeAuditManagerFramework)
	if err != nil {
		return err
	}
	roleIDs, err := resourceIDSet(st, acct.ID, TypeIAMRole)
	if err != nil {
		return err
	}
	bucketIDs, err := resourceIDSet(st, acct.ID, TypeS3Bucket)
	if err != nil {
		return err
	}

	for _, a := range assessments {
		var attrs auditManagerAssessmentAttrs
		if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil {
			continue
		}

		if attrs.Framework != nil {
			if fwARN := sv(attrs.Framework.Arn); fwARN != "" {
				fID := store.ResourceID("aws", acct.ID, TypeAuditManagerFramework, fwARN)
				if _, ok := frameworkIDs[fID]; ok {
					if err := st.UpsertRelationship(a.ID, fID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert auditmanager assessment→framework: %w", err)
					}
				}
			}
		}

		if attrs.Metadata != nil {
			for _, r := range attrs.Metadata.Roles {
				roleARN := sv(r.RoleArn)
				if roleARN == "" {
					continue
				}
				rID := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
				if _, ok := roleIDs[rID]; ok {
					if err := st.UpsertRelationship(a.ID, rID, store.RelAssumes, "directed", nil); err != nil {
						return fmt.Errorf("upsert auditmanager assessment→iam role: %w", err)
					}
				}
			}
			if attrs.Metadata.AssessmentReportsDestination != nil {
				dest := attrs.Metadata.AssessmentReportsDestination
				if sv(dest.DestinationType) == "S3" {
					if bucketARN := s3BucketARNFromS3URL(sv(dest.Destination)); bucketARN != "" {
						bID := store.ResourceID("aws", acct.ID, TypeS3Bucket, bucketARN)
						if _, ok := bucketIDs[bID]; ok {
							if err := st.UpsertRelationship(a.ID, bID, store.RelUses, "directed", nil); err != nil {
								return fmt.Errorf("upsert auditmanager assessment→s3 bucket: %w", err)
							}
						}
					}
				}
			}
		}
	}
	return nil
}
