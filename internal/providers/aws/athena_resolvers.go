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
		resolveAthenaWorkgroupTargets,
		EdgeDecl{TypeAthenaWorkgroup, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeAthenaWorkgroup, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveAthenaDataCatalogLambda,
		EdgeDecl{TypeAthenaDataCatalog, TypeLambdaFunction, store.RelUses},
	)
}

// athenaWorkGroupAttrs mirrors the verbatim WorkGroup fields used by the
// resolver. PascalCase tags match mustJSON of the SDK v2 struct.
type athenaWorkGroupAttrs struct {
	Configuration *struct {
		ResultConfiguration *struct {
			OutputLocation          *string `json:"OutputLocation"`
			EncryptionConfiguration *struct {
				KmsKey *string `json:"KmsKey"`
			} `json:"EncryptionConfiguration"`
		} `json:"ResultConfiguration"`
	} `json:"Configuration"`
}

// resolveAthenaWorkgroupTargets emits two edges per workgroup:
//   - workgroup → S3 bucket (uses) via ResultConfiguration.OutputLocation
//     (`s3://bucket/prefix/`).
//   - workgroup → KMS key (uses) via
//     ResultConfiguration.EncryptionConfiguration.KmsKey.
//
// FK-safe via scanned-bucket id set + KMS resolve index. Cross-account
// refs and AWS-managed default keys (`alias/aws/*`) skip silently.
func resolveAthenaWorkgroupTargets(acct *account, st *store.Store) error {
	wgs, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeAthenaWorkgroup},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(wgs) == 0 {
		return nil
	}

	bucketIDs, err := resourceIDSet(st, acct.ID, TypeS3Bucket)
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}

	for _, wg := range wgs {
		var attrs athenaWorkGroupAttrs
		if err := json.Unmarshal([]byte(wg.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Configuration == nil || attrs.Configuration.ResultConfiguration == nil {
			continue
		}
		rc := attrs.Configuration.ResultConfiguration

		if bucketARN := s3BucketARNFromS3URL(sv(rc.OutputLocation)); bucketARN != "" {
			bID := store.ResourceID("aws", acct.ID, TypeS3Bucket, bucketARN)
			if _, ok := bucketIDs[bID]; ok {
				if err := st.UpsertRelationship(wg.ID, bID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert athena workgroup→s3 bucket: %w", err)
				}
			}
		}

		if rc.EncryptionConfiguration != nil && wg.Region != nil {
			if keyID, ok := kmsIdx.resolveKMSKeyID(sv(rc.EncryptionConfiguration.KmsKey), *wg.Region, acct.ID); ok {
				if err := st.UpsertRelationship(wg.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert athena workgroup→kms: %w", err)
				}
			}
		}
	}
	return nil
}

// athenaDataCatalogAttrs mirrors the verbatim DataCatalog fields used by
// the resolver.
type athenaDataCatalogAttrs struct {
	Type       string            `json:"Type"`
	Parameters map[string]string `json:"Parameters"`
}

// resolveAthenaDataCatalogLambda emits a `uses` edge from each LAMBDA-type
// data catalog to the Lambda function(s) backing it. Athena LAMBDA
// catalogs encode the function ARN(s) in the catalog's Parameters map
// under keys like `function`, `metadata-function`, `record-function`.
// HIVE catalogs use `metadata-function`. GLUE catalogs reference the
// implicit Glue Data Catalog (no per-catalog edge to emit; see
// aws:glue:database closure). FEDERATED catalogs (Athena-managed Lambda)
// behave like LAMBDA for the resolver.
func resolveAthenaDataCatalogLambda(acct *account, st *store.Store) error {
	cats, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeAthenaDataCatalog},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(cats) == 0 {
		return nil
	}

	lambdaIDs, err := resourceIDSet(st, acct.ID, TypeLambdaFunction)
	if err != nil {
		return err
	}
	if len(lambdaIDs) == 0 {
		return nil
	}

	for _, c := range cats {
		var attrs athenaDataCatalogAttrs
		if err := json.Unmarshal([]byte(c.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Type != "LAMBDA" && attrs.Type != "HIVE" && attrs.Type != "FEDERATED" {
			continue
		}
		seen := map[string]struct{}{}
		for _, key := range []string{"function", "metadata-function", "record-function"} {
			arn := strings.TrimSpace(attrs.Parameters[key])
			if arn == "" {
				continue
			}
			if _, dup := seen[arn]; dup {
				continue
			}
			seen[arn] = struct{}{}
			lID := store.ResourceID("aws", acct.ID, TypeLambdaFunction, arn)
			if _, ok := lambdaIDs[lID]; !ok {
				continue
			}
			if err := st.UpsertRelationship(c.ID, lID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert athena data-catalog→lambda: %w", err)
			}
		}
	}
	return nil
}
