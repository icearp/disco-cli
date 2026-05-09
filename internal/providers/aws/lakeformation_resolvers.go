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
		resolveLakeFormationResourceTargets,
		EdgeDecl{TypeLakeFormationResource, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeLakeFormationResource, TypeIAMRole, store.RelAssumes},
	)
}

// lakeFormationResourceAttrs mirrors the verbatim ResourceInfo fields used by
// the resolver. PascalCase tags match mustJSON of the SDK v2 struct.
type lakeFormationResourceAttrs struct {
	ResourceArn *string `json:"ResourceArn"`
	RoleArn     *string `json:"RoleArn"`
}

// resolveLakeFormationResourceTargets emits two edges per registered Lake
// Formation data location:
//
//   - resource → S3 bucket (uses) — the registered S3 location's bucket.
//     ResourceArn is `arn:aws:s3:::bucket[/prefix]`; strip path suffix to
//     recover the bucket ARN. FK-safe via scanned-bucket id set.
//   - resource → IAM role (assumes) — the service role registered to access
//     the location. FK-safe via scanned IAM role id set.
//
// Cross-account refs (foreign bucket / foreign role) skip silently.
func resolveLakeFormationResourceTargets(acct *account, st *store.Store) error {
	resources, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeLakeFormationResource},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(resources) == 0 {
		return nil
	}

	bucketIDs, err := resourceIDSet(st, acct.ID, TypeS3Bucket)
	if err != nil {
		return err
	}
	roleIDs, err := resourceIDSet(st, acct.ID, TypeIAMRole)
	if err != nil {
		return err
	}

	for _, r := range resources {
		var attrs lakeFormationResourceAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if bucketARN := s3BucketARNFromLocation(sv(attrs.ResourceArn)); bucketARN != "" {
			bID := store.ResourceID("aws", acct.ID, TypeS3Bucket, bucketARN)
			if _, ok := bucketIDs[bID]; ok {
				if err := st.UpsertRelationship(r.ID, bID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert lakeformation resource→s3 bucket: %w", err)
				}
			}
		}
		if roleARN := sv(attrs.RoleArn); roleARN != "" {
			rID := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
			if _, ok := roleIDs[rID]; ok {
				if err := st.UpsertRelationship(r.ID, rID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert lakeformation resource→iam role: %w", err)
				}
			}
		}
	}
	return nil
}

// s3BucketARNFromLocation collapses an S3 location ARN to its bucket ARN.
// Lake Formation registers `arn:aws:s3:::bucket` or
// `arn:aws:s3:::bucket/prefix/`; the bucket NativeID stored by the S3
// scanner is `arn:aws:s3:::bucket`. Returns empty string for non-S3 ARNs.
func s3BucketARNFromLocation(arn string) string {
	const prefix = "arn:aws:s3:::"
	if !strings.HasPrefix(arn, prefix) {
		return ""
	}
	rest := arn[len(prefix):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" {
		return ""
	}
	return prefix + rest
}
