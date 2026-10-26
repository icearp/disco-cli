package aws

import (
	"cmp"
	"encoding/json"
	"fmt"
	"strings"

	lftypes "github.com/aws/aws-sdk-go-v2/service/lakeformation/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveLakeFormationResourceTargets,
		EdgeDecl{TypeLakeFormationResource, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeLakeFormationResource, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		resolveLakeFormationOptInTargets,
		EdgeDecl{TypeLakeFormationOptIn, TypeIAMRole, store.RelAttachedTo},
		EdgeDecl{TypeLakeFormationOptIn, TypeIAMUser, store.RelAttachedTo},
		EdgeDecl{TypeLakeFormationOptIn, TypeGlueDatabase, store.RelAttachedTo},
		EdgeDecl{TypeLakeFormationOptIn, TypeGlueTable, store.RelAttachedTo},
		EdgeDecl{TypeLakeFormationOptIn, TypeLakeFormationResource, store.RelAttachedTo},
		EdgeDecl{TypeLakeFormationOptIn, TypeLakeFormationDataCellsFilter, store.RelAttachedTo},
		EdgeDecl{TypeLakeFormationOptIn, TypeLakeFormationTag, store.RelAttachedTo},
	)
}

// resolveLakeFormationOptInTargets links each opt-in to its principal and to
// the catalog resource it covers, when that target was scanned. Catalog,
// LF-tag-policy and LF-tag-expression resources map to no scanned type, and a
// table named only by wildcard matches no scanned table, so neither emits an
// edge.
func resolveLakeFormationOptInTargets(acct *account, st *store.Store) error {
	optIns, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeLakeFormationOptIn},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(optIns) == 0 {
		return nil
	}
	scanned, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types: []string{
			TypeIAMRole, TypeIAMUser, TypeGlueDatabase, TypeGlueTable,
			TypeLakeFormationResource, TypeLakeFormationDataCellsFilter, TypeLakeFormationTag,
		},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	targetIDs := make(map[string]bool, len(scanned))
	for _, r := range scanned {
		targetIDs[r.ID] = true
	}

	for _, r := range optIns {
		var o lftypes.LakeFormationOptInsInfo
		if err := json.Unmarshal([]byte(r.AttributesJSON), &o); err != nil {
			return fmt.Errorf("decode lakeformation opt-in %s: %w", r.NativeID, err)
		}
		for _, nativeID := range lfOptInTargetNativeIDs(acct.partition(), sv(r.Region), acct.ID, o) {
			tID := store.ResourceID("aws", acct.ID, nativeID)
			if !targetIDs[tID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert lakeformation opt-in→%s: %w", nativeID, err)
			}
		}
	}
	return nil
}

// lfOptInTargetNativeIDs returns the NativeIDs, in the shapes their scanners
// store, of the principal and the catalog resource an opt-in names. A resource
// in another account's catalog yields an id no scanned row of this account has.
func lfOptInTargetNativeIDs(partition, region, accountID string, o lftypes.LakeFormationOptInsInfo) []string {
	var ids []string
	if o.Principal != nil && sv(o.Principal.DataLakePrincipalIdentifier) != "" {
		ids = append(ids, sv(o.Principal.DataLakePrincipalIdentifier))
	}
	r := o.Resource
	if r == nil {
		return ids
	}
	switch {
	case r.Database != nil:
		ids = append(ids, glueDatabaseARN(partition, region, cmp.Or(sv(r.Database.CatalogId), accountID), sv(r.Database.Name)))
	case r.Table != nil:
		ids = append(ids, glueTableARN(partition, region, cmp.Or(sv(r.Table.CatalogId), accountID), sv(r.Table.DatabaseName), sv(r.Table.Name)))
	case r.TableWithColumns != nil:
		t := r.TableWithColumns
		ids = append(ids, glueTableARN(partition, region, cmp.Or(sv(t.CatalogId), accountID), sv(t.DatabaseName), sv(t.Name)))
	case r.DataLocation != nil:
		// A registered location's NativeID is its S3 ARN, which carries no
		// account, so another catalog's registration must be excluded here.
		if cmp.Or(sv(r.DataLocation.CatalogId), accountID) == accountID {
			ids = append(ids, sv(r.DataLocation.ResourceArn))
		}
	case r.DataCellsFilter != nil:
		f := r.DataCellsFilter
		ids = append(ids, lfDataCellsFilterARN(partition, region, cmp.Or(sv(f.TableCatalogId), accountID), sv(f.DatabaseName), sv(f.TableName), sv(f.Name)))
	case r.LFTag != nil:
		ids = append(ids, lfTagARN(partition, region, cmp.Or(sv(r.LFTag.CatalogId), accountID), sv(r.LFTag.TagKey)))
	}
	return ids
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
//     ResourceArn is `arn:{partition}:s3:::bucket[/prefix]`; strip path
//     suffix to recover the bucket ARN (s3BucketARNFromLocation recognizes
//     only the aws partition). FK-safe via scanned-bucket id set.
//   - resource → IAM role (assumes) — the service role registered to access
//     the location. FK-safe via scanned IAM role id set.
//
// Cross-account refs (foreign bucket / foreign role) skip silently.
func resolveLakeFormationResourceTargets(acct *account, st *store.Store) error {
	resources, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
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
			bID := store.ResourceID("aws", acct.ID, bucketARN)
			if _, ok := bucketIDs[bID]; ok {
				if err := st.UpsertRelationship(r.ID, bID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert lakeformation resource→s3 bucket: %w", err)
				}
			}
		}
		if roleARN := sv(attrs.RoleArn); roleARN != "" {
			rID := store.ResourceID("aws", acct.ID, roleARN)
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
// Lake Formation registers `arn:{partition}:s3:::bucket` or
// `arn:{partition}:s3:::bucket/prefix/`; the S3 scanner stores the bucket as
// `arn:{partition}:s3:::bucket` in the account's partition. This matcher
// recognizes only the aws partition: it returns empty string for an S3 ARN in
// any other partition, as for a non-S3 ARN, so no edge is emitted.
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
