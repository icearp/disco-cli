package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveGlueTableS3Location,
		EdgeDecl{TypeGlueTable, TypeS3Bucket, store.RelUses},
	)
	registerResolver(resolveGlueTableDatabase,
		EdgeDecl{TypeGlueTable, TypeGlueDatabase, store.RelAttachedTo},
	)
	registerResolver(resolveGlueJobRefs,
		EdgeDecl{TypeGlueJob, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeGlueJob, TypeS3Bucket, store.RelUses},
	)
	registerResolver(resolveGlueCrawlerRefs,
		EdgeDecl{TypeGlueCrawler, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeGlueCrawler, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeGlueCrawler, TypeGlueDatabase, store.RelAttachedTo},
	)
}

// resolveGlueTableDatabase emits an `attached-to` edge from each Glue
// table to the database it lives in. Database NativeID shape:
// `arn:aws:glue:{r}:{a}:database/{name}`.
func resolveGlueTableDatabase(acct *account, st *store.Store) error {
	tables, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGlueTable},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	dbSet, err := scannedIDSet(acct, st, TypeGlueDatabase)
	if err != nil {
		return err
	}
	for _, r := range tables {
		var attrs struct {
			DatabaseName *string `json:"DatabaseName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.DatabaseName == nil || *attrs.DatabaseName == "" {
			continue
		}
		dbARN := fmt.Sprintf("arn:aws:glue:%s:%s:database/%s", sv(r.Region), acct.ID, *attrs.DatabaseName)
		dbID := store.ResourceID("aws", acct.ID, TypeGlueDatabase, dbARN)
		if !dbSet[dbID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, dbID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert glue-table→database: %w", err)
		}
	}
	return nil
}

// glueRoleARN normalises a Role field. The Glue API accepts either a full
// IAM role ARN or a bare role name. The bare-name form gets rebuilt into
// the canonical ARN shape `arn:aws:iam::{acct}:role/{name}` for FK
// lookup. IAM roles have no region segment so the source resource's
// region is irrelevant here.
func glueRoleARN(acctID, roleField string) string {
	if roleField == "" {
		return ""
	}
	if strings.HasPrefix(roleField, "arn:") {
		return roleField
	}
	return fmt.Sprintf("arn:aws:iam::%s:role/%s", acctID, roleField)
}

// resolveGlueJobRefs walks each Job's Role + Command.ScriptLocation and
// emits role + S3 bucket edges.
func resolveGlueJobRefs(acct *account, st *store.Store) error {
	jobs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGlueJob},
		Limit: util.AllResources,
	})
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
	for _, r := range jobs {
		var attrs struct {
			Role    *string `json:"Role"`
			Command *struct {
				ScriptLocation *string `json:"ScriptLocation"`
			} `json:"Command"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if roleARN := glueRoleARN(acct.ID, sv(attrs.Role)); roleARN != "" {
			roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
			if roleSet[roleID] {
				if err := st.UpsertRelationship(r.ID, roleID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert glue-job→role: %w", err)
				}
			}
		}
		if attrs.Command != nil {
			if bARN := s3BucketARNFromS3URL(sv(attrs.Command.ScriptLocation)); bARN != "" {
				bID := store.ResourceID("aws", acct.ID, TypeS3Bucket, bARN)
				if bucketSet[bID] {
					if err := st.UpsertRelationship(r.ID, bID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert glue-job→bucket: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// resolveGlueCrawlerRefs walks each Crawler's Role, Targets.S3Targets[],
// and DatabaseName fields and emits the corresponding edges.
func resolveGlueCrawlerRefs(acct *account, st *store.Store) error {
	crawlers, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGlueCrawler},
		Limit: util.AllResources,
	})
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
	dbSet, err := scannedIDSet(acct, st, TypeGlueDatabase)
	if err != nil {
		return err
	}
	for _, r := range crawlers {
		var attrs struct {
			Role         *string `json:"Role"`
			DatabaseName *string `json:"DatabaseName"`
			Targets      *struct {
				S3Targets []struct {
					Path *string `json:"Path"`
				} `json:"S3Targets"`
			} `json:"Targets"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if roleARN := glueRoleARN(acct.ID, sv(attrs.Role)); roleARN != "" {
			roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
			if roleSet[roleID] {
				if err := st.UpsertRelationship(r.ID, roleID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert glue-crawler→role: %w", err)
				}
			}
		}
		if attrs.DatabaseName != nil && *attrs.DatabaseName != "" {
			dbARN := fmt.Sprintf("arn:aws:glue:%s:%s:database/%s", sv(r.Region), acct.ID, *attrs.DatabaseName)
			dbID := store.ResourceID("aws", acct.ID, TypeGlueDatabase, dbARN)
			if dbSet[dbID] {
				if err := st.UpsertRelationship(r.ID, dbID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert glue-crawler→database: %w", err)
				}
			}
		}
		if attrs.Targets != nil {
			seen := map[string]bool{}
			for _, t := range attrs.Targets.S3Targets {
				bARN := s3BucketARNFromS3URL(sv(t.Path))
				if bARN == "" || seen[bARN] {
					continue
				}
				seen[bARN] = true
				bID := store.ResourceID("aws", acct.ID, TypeS3Bucket, bARN)
				if !bucketSet[bID] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, bID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert glue-crawler→bucket: %w", err)
				}
			}
		}
	}
	return nil
}

// glueTableAttrs mirrors the verbatim Table fields used by the resolver.
type glueTableAttrs struct {
	StorageDescriptor *struct {
		Location *string `json:"Location"`
	} `json:"StorageDescriptor"`
}

// resolveGlueTableS3Location emits a `uses` edge from each Glue table to
// the S3 bucket backing its `StorageDescriptor.Location`. Format is
// `s3://bucket[/prefix...]`; the bucket portion is parsed and matched
// against scanned S3 buckets. Non-S3 locations (HDFS, JDBC, federated
// catalogs, empty) skip silently. FK-safe via scanned-bucket id set;
// cross-account bucket refs skip.
func resolveGlueTableS3Location(acct *account, st *store.Store) error {
	tables, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeGlueTable},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(tables) == 0 {
		return nil
	}

	bucketIDs, err := resourceIDSet(st, acct.ID, TypeS3Bucket)
	if err != nil {
		return err
	}
	if len(bucketIDs) == 0 {
		return nil
	}

	for _, tbl := range tables {
		var attrs glueTableAttrs
		if err := json.Unmarshal([]byte(tbl.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.StorageDescriptor == nil {
			continue
		}
		bucketARN := s3BucketARNFromS3URL(sv(attrs.StorageDescriptor.Location))
		if bucketARN == "" {
			continue
		}
		bID := store.ResourceID("aws", acct.ID, TypeS3Bucket, bucketARN)
		if _, ok := bucketIDs[bID]; !ok {
			continue
		}
		if err := st.UpsertRelationship(tbl.ID, bID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert glue table→s3 bucket: %w", err)
		}
	}
	return nil
}

// s3BucketARNFromS3URL collapses an `s3://bucket[/prefix...]` URL to the
// canonical bucket ARN `arn:aws:s3:::bucket`. Returns empty string for
// non-s3:// schemes (HDFS paths, JDBC URIs, etc.) and malformed inputs.
// Sibling of `s3BucketARNFromLocation` (which handles `arn:aws:s3:::`
// prefixes used by Lake Formation registered locations).
func s3BucketARNFromS3URL(url string) string {
	const scheme = "s3://"
	if !strings.HasPrefix(url, scheme) {
		return ""
	}
	rest := url[len(scheme):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" {
		return ""
	}
	return "arn:aws:s3:::" + rest
}
