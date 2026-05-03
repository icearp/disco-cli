package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveStorageLensRelationships,
		EdgeDecl{TypeS3StorageLens, TypeS3Bucket, store.RelUses},
	)
}

// resolveStorageLensRelationships emits uses edges from each S3 Storage Lens
// configuration to the buckets it scopes (Include.Buckets[]) and to the S3
// bucket that receives its metrics export (DataExport.S3BucketDestination.Arn).
// Exclude.Buckets[] is deliberately skipped — it represents absence of coverage,
// not a relationship. Cross-account export targets are FK-safe-skipped since
// they may not be scanned.
func resolveStorageLensRelationships(acct *account, st *store.Store) error {
	lenses, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeS3StorageLens},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(lenses) == 0 {
		return nil
	}
	buckets, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeS3Bucket},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	bucketSet := make(map[string]struct{}, len(buckets))
	for _, b := range buckets {
		bucketSet[b.ID] = struct{}{}
	}
	for _, r := range lenses {
		var attrs struct {
			Include *struct {
				Buckets []string `json:"Buckets"`
			} `json:"Include"`
			DataExport *struct {
				S3BucketDestination *struct {
					Arn *string `json:"Arn"`
				} `json:"S3BucketDestination"`
			} `json:"DataExport"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		emit := func(bucketARN string) error {
			if bucketARN == "" {
				return nil
			}
			id := store.ResourceID("aws", acct.ID, TypeS3Bucket, bucketARN)
			if _, ok := bucketSet[id]; !ok {
				return nil
			}
			return st.UpsertRelationship(r.ID, id, store.RelUses, "directed", nil)
		}
		if attrs.Include != nil {
			for _, b := range attrs.Include.Buckets {
				if err := emit(b); err != nil {
					return fmt.Errorf("upsert storage-lens→include-bucket: %w", err)
				}
			}
		}
		if attrs.DataExport != nil && attrs.DataExport.S3BucketDestination != nil {
			if err := emit(sv(attrs.DataExport.S3BucketDestination.Arn)); err != nil {
				return fmt.Errorf("upsert storage-lens→export-bucket: %w", err)
			}
		}
	}
	return nil
}
