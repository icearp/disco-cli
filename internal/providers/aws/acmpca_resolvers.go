package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveACMPCARelationships,
		EdgeDecl{TypeACMPrivateCA, TypeS3Bucket, store.RelUses},
	)
}

// resolveACMPCARelationships emits CA → S3 bucket edges for the CRL
// distribution bucket when the RevocationConfiguration references one.
func resolveACMPCARelationships(acct *account, st *store.Store) error {
	cas, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeACMPrivateCA},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	type crlCfg struct {
		S3BucketName *string `json:"S3BucketName"`
	}
	type revCfg struct {
		CrlConfiguration *crlCfg `json:"CrlConfiguration"`
	}
	type attrs struct {
		RevocationConfiguration *revCfg `json:"RevocationConfiguration"`
	}
	for _, r := range cas {
		var a attrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		if a.RevocationConfiguration == nil || a.RevocationConfiguration.CrlConfiguration == nil {
			continue
		}
		bucket := sv(a.RevocationConfiguration.CrlConfiguration.S3BucketName)
		if bucket == "" {
			continue
		}
		bucketID := store.ResourceID("aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+bucket)
		if err := st.UpsertRelationship(r.ID, bucketID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert acm-pca→s3 crl: %w", err)
		}
	}
	return nil
}
