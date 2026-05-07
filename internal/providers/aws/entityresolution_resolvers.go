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
		resolveEntityResolutionPolicyStatementToParent,
		EdgeDecl{TypeEntityResolutionPolicyStatement, TypeEntityResolutionMatchingWorkflow, store.RelAttachedTo},
		EdgeDecl{TypeEntityResolutionPolicyStatement, TypeEntityResolutionIDMappingWorkflow, store.RelAttachedTo},
		EdgeDecl{TypeEntityResolutionPolicyStatement, TypeEntityResolutionIDNamespace, store.RelAttachedTo},
		EdgeDecl{TypeEntityResolutionPolicyStatement, TypeEntityResolutionSchemaMapping, store.RelAttachedTo},
	)
	registerResolver(
		resolveEntityResolutionMatchingWorkflowRefs,
		EdgeDecl{TypeEntityResolutionMatchingWorkflow, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeEntityResolutionMatchingWorkflow, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeEntityResolutionMatchingWorkflow, TypeGlueTable, store.RelUses},
		EdgeDecl{TypeEntityResolutionMatchingWorkflow, TypeS3Bucket, store.RelUses},
	)
}

// resolveEntityResolutionMatchingWorkflowRefs wires each matching-workflow to
// its IAM role (RoleArn), output CMKs (OutputSourceConfig[].KMSArn), input
// glue tables (InputSourceConfig[].InputSourceARN), and output S3 buckets
// (OutputSourceConfig[].OutputS3Path). All four field paths land on the
// GetMatchingWorkflow body that the scanner now fans out per row.
func resolveEntityResolutionMatchingWorkflowRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEntityResolutionMatchingWorkflow}, Limit: util.AllResources,
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
	tableSet, err := scannedIDSet(acct, st, TypeGlueTable)
	if err != nil {
		return err
	}
	bucketSet, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RoleArn           *string `json:"RoleArn"`
			InputSourceConfig []struct {
				InputSourceARN *string `json:"InputSourceARN"`
			} `json:"InputSourceConfig"`
			OutputSourceConfig []struct {
				OutputS3Path *string `json:"OutputS3Path"`
				KMSArn       *string `json:"KMSArn"`
			} `json:"OutputSourceConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if ra := sv(attrs.RoleArn); ra != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeIAMRole, ra)
			if roleSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert er-mw→role: %w", err)
				}
			}
		}
		for _, in := range attrs.InputSourceConfig {
			if a := sv(in.InputSourceARN); a != "" {
				tgt := store.ResourceID("aws", acct.ID, TypeGlueTable, a)
				if tableSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert er-mw→glue-table: %w", err)
					}
				}
			}
		}
		seenBucket := map[string]struct{}{}
		for _, out := range attrs.OutputSourceConfig {
			if k := sv(out.KMSArn); k != "" {
				if keyID, ok := idx.resolveKMSKeyID(k, region, acct.ID); ok {
					if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert er-mw→kms: %w", err)
					}
				}
			}
			if p := sv(out.OutputS3Path); p != "" {
				bucket := s3BucketFromS3URI(p)
				if bucket == "" {
					continue
				}
				if _, ok := seenBucket[bucket]; ok {
					continue
				}
				seenBucket[bucket] = struct{}{}
				bARN := "arn:aws:s3:::" + bucket
				tgt := store.ResourceID("aws", acct.ID, TypeS3Bucket, bARN)
				if !bucketSet[tgt] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert er-mw→s3: %w", err)
				}
			}
		}
	}
	return nil
}

// s3BucketFromS3URI extracts the bucket name from an `s3://bucket/key/path`
// URI. Returns "" when the URI is malformed or not an s3:// scheme.
func s3BucketFromS3URI(uri string) string {
	const prefix = "s3://"
	if !strings.HasPrefix(uri, prefix) {
		return ""
	}
	rest := uri[len(prefix):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
}

// resolveEntityResolutionPolicyStatementToParent wires each policy-statement
// to its parent via NativeID `{parentARN}/policy` strip; parent may be a
// matching-workflow, id-mapping-workflow, id-namespace, or schema-mapping —
// dispatch by ARN substring.
func resolveEntityResolutionPolicyStatementToParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEntityResolutionPolicyStatement}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	mwSet, err := scannedIDSet(acct, st, TypeEntityResolutionMatchingWorkflow)
	if err != nil {
		return err
	}
	imwSet, err := scannedIDSet(acct, st, TypeEntityResolutionIDMappingWorkflow)
	if err != nil {
		return err
	}
	insSet, err := scannedIDSet(acct, st, TypeEntityResolutionIDNamespace)
	if err != nil {
		return err
	}
	smSet, err := scannedIDSet(acct, st, TypeEntityResolutionSchemaMapping)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parent := strings.TrimSuffix(r.NativeID, "/policy")
		if parent == r.NativeID {
			continue
		}
		var tgtType string
		var present bool
		switch {
		case strings.Contains(parent, "/matchingworkflow/"), strings.Contains(parent, ":matchingworkflow/"):
			tgtType = TypeEntityResolutionMatchingWorkflow
			present = mwSet[store.ResourceID("aws", acct.ID, tgtType, parent)]
		case strings.Contains(parent, "/idmappingworkflow/"), strings.Contains(parent, ":idmappingworkflow/"):
			tgtType = TypeEntityResolutionIDMappingWorkflow
			present = imwSet[store.ResourceID("aws", acct.ID, tgtType, parent)]
		case strings.Contains(parent, "/idnamespace/"), strings.Contains(parent, ":idnamespace/"):
			tgtType = TypeEntityResolutionIDNamespace
			present = insSet[store.ResourceID("aws", acct.ID, tgtType, parent)]
		case strings.Contains(parent, "/schemamapping/"), strings.Contains(parent, ":schemamapping/"):
			tgtType = TypeEntityResolutionSchemaMapping
			present = smSet[store.ResourceID("aws", acct.ID, tgtType, parent)]
		default:
			continue
		}
		if !present {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, tgtType, parent)
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert er policy-statement→%s: %w", tgtType, err)
		}
	}
	return nil
}
