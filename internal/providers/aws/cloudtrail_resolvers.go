package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveCloudTrailRelationships)
}

// resolveCloudTrailRelationships links each trail to its S3 bucket, KMS key,
// and CloudWatch Logs log group.
func resolveCloudTrailRelationships(acct *account, st *store.Store) error {
	trails, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCloudTrailTrail},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}

	type trailInner struct {
		S3BucketName              *string `json:"S3BucketName"`
		KMSKeyId                  *string `json:"KMSKeyId"`
		CloudWatchLogsLogGroupArn *string `json:"CloudWatchLogsLogGroupArn"`
	}
	type attrs struct {
		Trail trailInner `json:"Trail"`
	}

	for _, r := range trails {
		var a attrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		t := a.Trail
		// Trail → S3 bucket
		if bucket := sv(t.S3BucketName); bucket != "" {
			bucketARN := "arn:aws:s3:::" + bucket
			bucketID := store.ResourceID("aws", acct.ID, TypeS3Bucket, bucketARN)
			if err := st.UpsertRelationship(r.ID, bucketID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert cloudtrail→s3: %w", err)
			}
		}
		// Trail → KMS key
		if sv(t.KMSKeyId) != "" {
			keyID := store.ResourceID("aws", acct.ID, TypeKMSKey, *t.KMSKeyId)
			if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert cloudtrail→kms: %w", err)
			}
		}
		// Trail → CloudWatch log group
		// ARN format: arn:aws:logs:<region>:<account>:log-group:<name>:*
		if lgARN := sv(t.CloudWatchLogsLogGroupArn); lgARN != "" {
			// Strip trailing ":*" appended by the SDK.
			lgARN = strings.TrimSuffix(lgARN, ":*")
			lgID := store.ResourceID("aws", acct.ID, TypeLogsLogGroup, lgARN)
			if err := st.UpsertRelationship(r.ID, lgID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert cloudtrail→log-group: %w", err)
			}
		}
	}
	return nil
}
