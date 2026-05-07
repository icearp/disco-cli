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
		resolveS3BucketPolicyRelationships,
		EdgeDecl{TypeS3BucketPolicy, TypeS3Bucket, store.RelAttachedTo},
	)
	registerResolver(
		resolveS3AccessGrantRelationships,
		EdgeDecl{TypeS3AccessGrant, TypeS3AccessGrantsInstance, store.RelAttachedTo},
	)
	registerResolver(
		resolveS3AccessGrantsLocationRelationships,
		EdgeDecl{TypeS3AccessGrantsLocation, TypeS3AccessGrantsInstance, store.RelAttachedTo},
	)
	registerResolver(
		resolveS3AccessPointRelationships,
		EdgeDecl{TypeS3AccessPoint, TypeS3Bucket, store.RelAttachedTo},
	)
	registerResolver(
		resolveS3MRAPPolicyRelationships,
		EdgeDecl{TypeS3MultiRegionAccessPointPolicy, TypeS3MultiRegionAccessPoint, store.RelAttachedTo},
	)
	registerResolver(
		resolveS3BucketEncryptionRelationships,
		EdgeDecl{TypeS3Bucket, TypeKMSKey, store.RelUses},
	)
}

// resolveS3BucketEncryptionRelationships emits bucket→KMS `uses` edges from
// the per-bucket SSE config collected by scanS3BucketEncryptions. Only rules
// with SSEAlgorithm "aws:kms" / "aws:kms:dsse" and a non-empty KMSMasterKeyID
// produce edges; resolveKMSKeyID handles alias-name → key-ARN resolution.
func resolveS3BucketEncryptionRelationships(acct *account, st *store.Store) error {
	if len(acct.s3BucketEncryption) == 0 {
		return nil
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for bucket, entry := range acct.s3BucketEncryption {
		if entry.Config == nil {
			continue
		}
		bucketARN := fmt.Sprintf("arn:aws:s3:::%s", bucket)
		bucketID := store.ResourceID("aws", acct.ID, TypeS3Bucket, bucketARN)
		for _, rule := range entry.Config.Rules {
			if rule.ApplyServerSideEncryptionByDefault == nil {
				continue
			}
			def := rule.ApplyServerSideEncryptionByDefault
			alg := string(def.SSEAlgorithm)
			if alg != "aws:kms" && alg != "aws:kms:dsse" {
				continue
			}
			keyID, ok := kmsIdx.resolveKMSKeyID(sv(def.KMSMasterKeyID), entry.Region, acct.ID)
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(bucketID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert s3-bucket→kms: %w", err)
			}
		}
	}
	return nil
}

// resolveS3BucketPolicyRelationships links each bucket policy to its bucket.
// The policy NativeID is arn:aws:s3:::{bucket}/policy; stripping "/policy" gives
// the bucket ARN that was stored by the S3 bucket scanner.
func resolveS3BucketPolicyRelationships(acct *account, st *store.Store) error {
	policies, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeS3BucketPolicy},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range policies {
		// NativeID = arn:aws:s3:::{bucket}/policy → strip "/policy" suffix.
		bucketARN := strings.TrimSuffix(r.NativeID, "/policy")
		bucketID := store.ResourceID("aws", acct.ID, TypeS3Bucket, bucketARN)
		if err := st.UpsertRelationship(r.ID, bucketID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert bucket-policy→bucket relationship: %w", err)
		}
	}
	return nil
}

// buildAccessGrantsInstanceByRegion returns a region→resource-ID map of
// access grants instances for this account. Each region has at most one.
func buildAccessGrantsInstanceByRegion(acct *account, st *store.Store) (map[string]string, error) {
	instances, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeS3AccessGrantsInstance},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(instances))
	for _, inst := range instances {
		if inst.Region != nil {
			m[*inst.Region] = inst.ID
		}
	}
	return m, nil
}

// resolveS3AccessGrantRelationships links each access grant to the access
// grants instance in the same region. The list API does not return the
// instance ARN on each grant, so we match by region (at most one instance
// per account per region).
func resolveS3AccessGrantRelationships(acct *account, st *store.Store) error {
	grants, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeS3AccessGrant},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(grants) == 0 {
		return nil
	}
	instanceByRegion, err := buildAccessGrantsInstanceByRegion(acct, st)
	if err != nil {
		return err
	}
	for _, r := range grants {
		region := sv(r.Region)
		instanceID, ok := instanceByRegion[region]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, instanceID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert access-grant→instance relationship: %w", err)
		}
	}
	return nil
}

// resolveS3AccessGrantsLocationRelationships links each registered location to
// the access grants instance in the same region.
func resolveS3AccessGrantsLocationRelationships(acct *account, st *store.Store) error {
	locations, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeS3AccessGrantsLocation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(locations) == 0 {
		return nil
	}
	instanceByRegion, err := buildAccessGrantsInstanceByRegion(acct, st)
	if err != nil {
		return err
	}
	for _, r := range locations {
		region := sv(r.Region)
		instanceID, ok := instanceByRegion[region]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, instanceID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert access-grants-location→instance relationship: %w", err)
		}
	}
	return nil
}

// resolveS3AccessPointRelationships links each S3 access point to its bucket.
// The bucket name is stored in the Bucket attribute; the bucket ARN is
// arn:aws:s3:::{bucket-name}.
func resolveS3AccessPointRelationships(acct *account, st *store.Store) error {
	aps, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeS3AccessPoint},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range aps {
		var attrs struct {
			Bucket *string `json:"Bucket"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Bucket == nil {
			continue
		}
		bucketARN := fmt.Sprintf("arn:aws:s3:::%s", *attrs.Bucket)
		bucketID := store.ResourceID("aws", acct.ID, TypeS3Bucket, bucketARN)
		if err := st.UpsertRelationship(r.ID, bucketID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert access-point→bucket relationship: %w", err)
		}
	}
	return nil
}

// resolveS3MRAPPolicyRelationships links each MRAP policy to its MRAP.
// The policy NativeID is arn:aws:s3::{account}:accesspoint/{name}/policy;
// stripping "/policy" gives the MRAP NativeID stored by the MRAP scanner.
func resolveS3MRAPPolicyRelationships(acct *account, st *store.Store) error {
	policies, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeS3MultiRegionAccessPointPolicy},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range policies {
		mrapARN := strings.TrimSuffix(r.NativeID, "/policy")
		mrapID := store.ResourceID("aws", acct.ID, TypeS3MultiRegionAccessPoint, mrapARN)
		if err := st.UpsertRelationship(r.ID, mrapID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert mrap-policy→mrap relationship: %w", err)
		}
	}
	return nil
}
