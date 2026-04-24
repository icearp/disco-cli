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
	registerResolver(resolveCloudTrailEventDataStoreRelationships)
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

// resolveCloudTrailEventDataStoreRelationships links each Lake event data
// store to its KMS key (uses) and federation IAM role (assumes). FK-safe via
// scanned id sets — cross-account targets silently skip.
func resolveCloudTrailEventDataStoreRelationships(acct *account, st *store.Store) error {
	eds, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCloudTrailEventDataStore},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(eds) == 0 {
		return nil
	}
	keys, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeKMSKey},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	keyIDs := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		keyIDs[k.ID] = struct{}{}
	}
	roles, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeIAMRole},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	roleIDs := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		roleIDs[r.ID] = struct{}{}
	}

	type attrs struct {
		KmsKeyId          *string `json:"KmsKeyId"`
		FederationRoleArn *string `json:"FederationRoleArn"`
	}
	for _, r := range eds {
		var a attrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		// EDS → KMS
		if k := sv(a.KmsKeyId); k != "" && !strings.HasPrefix(k, "alias/aws/") {
			keyARN := kmsKeyTargetARN(k, sv(r.Region), acct.ID)
			keyID := store.ResourceID("aws", acct.ID, TypeKMSKey, keyARN)
			if _, ok := keyIDs[keyID]; ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert cloudtrail-eds→kms: %w", err)
				}
			}
		}
		// EDS → federation IAM role
		if roleARN := sv(a.FederationRoleArn); roleARN != "" {
			roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
			if _, ok := roleIDs[roleID]; ok {
				if err := st.UpsertRelationship(r.ID, roleID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert cloudtrail-eds→role: %w", err)
				}
			}
		}
	}
	return nil
}
