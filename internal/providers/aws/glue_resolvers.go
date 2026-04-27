package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveGlueTableS3Location)
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
