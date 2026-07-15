package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveStorageLensRelationships,
		EdgeDecl{TypeS3StorageLens, TypeS3Bucket, store.RelUses},
	)
}

// resolveStorageLensRelationships emits uses edges from each S3 Storage Lens
// config to its scoped buckets (Include.Buckets[]) and to its metrics-export
// bucket (DataExport.S3BucketDestination.Arn). Exclude.Buckets[] is skipped —
// it marks absence of coverage, not a relationship. Cross-account export
// targets are FK-safe-skipped since they may be unscanned.
func resolveStorageLensRelationships(acct *account, st *store.Store) error {
	lenses, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeS3StorageLens},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(lenses) == 0 {
		return nil
	}
	buckets, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeS3Bucket},
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
			id := store.ResourceID("aws", acct.ID, bucketARN)
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

func init() {
	registerResolver(
		resolveS3MRAPRegionBuckets,
		EdgeDecl{TypeS3MultiRegionAccessPoint, TypeS3Bucket, store.RelUses},
	)
}

// resolveS3MRAPRegionBuckets wires each multi-region access point to the
// underlying buckets in its Regions[] report. FK-safe: buckets in unscanned
// regions/accounts skip silently.
func resolveS3MRAPRegionBuckets(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeS3MultiRegionAccessPoint}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	bucketSet, err := scannedIDSet(acct, st, TypeS3Bucket)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Regions []struct {
				Bucket *string `json:"Bucket"`
			} `json:"Regions"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, reg := range attrs.Regions {
			b := sv(reg.Bucket)
			if b == "" {
				continue
			}
			barn := "arn:aws:s3:::" + b
			tgt := store.ResourceID("aws", acct.ID, barn)
			if !bucketSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert s3-mrap→bucket: %w", err)
			}
		}
	}
	return nil
}
