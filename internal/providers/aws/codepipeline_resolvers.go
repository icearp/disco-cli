package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveCodePipelineWebhookToPipeline,
		EdgeDecl{TypeCodePipelineWebhook, TypeCodePipelinePipeline, store.RelAttachedTo},
	)
	registerResolver(
		resolveCodePipelinePipelineRefs,
		EdgeDecl{TypeCodePipelinePipeline, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeCodePipelinePipeline, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeCodePipelinePipeline, TypeKMSKey, store.RelUses},
	)
}

// resolveCodePipelinePipelineRefs wires each pipeline to its service role,
// artifact-store S3 buckets, and KMS keys. GetPipeline body: Pipeline.RoleArn
// + ArtifactStore (or ArtifactStores map per region).
func resolveCodePipelinePipelineRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCodePipelinePipeline}, Limit: util.AllResources,
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
	type artifactStore struct {
		Location      *string `json:"Location"`
		EncryptionKey *struct {
			ID *string `json:"Id"`
		} `json:"EncryptionKey"`
	}
	for _, r := range rows {
		var attrs struct {
			Pipeline *struct {
				RoleArn        *string                  `json:"RoleArn"`
				ArtifactStore  *artifactStore           `json:"ArtifactStore"`
				ArtifactStores map[string]artifactStore `json:"ArtifactStores"`
			} `json:"Pipeline"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Pipeline == nil {
			continue
		}
		region := sv(r.Region)
		if rarn := sv(attrs.Pipeline.RoleArn); rarn != "" {
			tgt := store.ResourceID("aws", acct.ID, TypeIAMRole, rarn)
			if roleSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert codepipeline→role: %w", err)
				}
			}
		}
		stores := []artifactStore{}
		if attrs.Pipeline.ArtifactStore != nil {
			stores = append(stores, *attrs.Pipeline.ArtifactStore)
		}
		for _, as := range attrs.Pipeline.ArtifactStores {
			stores = append(stores, as)
		}
		for _, as := range stores {
			if loc := sv(as.Location); loc != "" {
				barn := "arn:aws:s3:::" + loc
				tgt := store.ResourceID("aws", acct.ID, TypeS3Bucket, barn)
				if bucketSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert codepipeline→s3: %w", err)
					}
				}
			}
			if as.EncryptionKey != nil {
				if ref := sv(as.EncryptionKey.ID); ref != "" {
					if keyID, ok := idx.resolveKMSKeyID(ref, region, acct.ID); ok {
						if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
							return fmt.Errorf("upsert codepipeline→kms: %w", err)
						}
					}
				}
			}
		}
	}
	return nil
}

// resolveCodePipelineWebhookToPipeline wires each webhook to its target
// pipeline via Definition.TargetPipeline (a pipeline name).
func resolveCodePipelineWebhookToPipeline(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCodePipelineWebhook}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	pSet, err := scannedIDSet(acct, st, TypeCodePipelinePipeline)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Definition *struct {
				TargetPipeline *string `json:"TargetPipeline"`
			} `json:"Definition"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Definition == nil {
			continue
		}
		n := sv(attrs.Definition.TargetPipeline)
		if n == "" {
			continue
		}
		pARN := fmt.Sprintf("arn:aws:codepipeline:%s:%s:%s", sv(r.Region), acct.ID, n)
		tgtID := store.ResourceID("aws", acct.ID, TypeCodePipelinePipeline, pARN)
		if !pSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert codepipeline webhook→pipeline: %w", err)
		}
	}
	return nil
}
