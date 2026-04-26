package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveConfigRelationships) }

// resolveConfigRelationships wires AWS Config recorders, delivery channels,
// and rules to their dependencies: IAM role, S3 bucket, KMS key, SNS topic,
// Lambda function (custom rules).
func resolveConfigRelationships(acct *account, st *store.Store) error {
	if err := resolveConfigRecorders(acct, st); err != nil {
		return err
	}
	if err := resolveConfigDeliveryChannels(acct, st); err != nil {
		return err
	}
	return resolveConfigRules(acct, st)
}

func resolveConfigRecorders(acct *account, st *store.Store) error {
	recs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeConfigRecorder},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range recs {
		var attrs struct {
			RoleARN *string `json:"RoleARN"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if role := sv(attrs.RoleARN); role != "" {
			roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if err := st.UpsertRelationship(r.ID, roleID, store.RelAssumes, "directed", nil); err != nil {
				return fmt.Errorf("upsert config-recorder→iam: %w", err)
			}
		}
	}
	return nil
}

func resolveConfigDeliveryChannels(acct *account, st *store.Store) error {
	dcs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeConfigDeliveryChannel},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range dcs {
		var attrs struct {
			S3BucketName *string `json:"S3BucketName"`
			S3KmsKeyArn  *string `json:"S3KmsKeyArn"`
			SnsTopicARN  *string `json:"SnsTopicARN"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if bucket := sv(attrs.S3BucketName); bucket != "" {
			bid := store.ResourceID("aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+bucket)
			if err := st.UpsertRelationship(r.ID, bid, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert config-dc→s3: %w", err)
			}
		}
		if kid, ok := kmsIdx.resolveKMSKeyID(sv(attrs.S3KmsKeyArn), sv(r.Region), acct.ID); ok {
			if err := st.UpsertRelationship(r.ID, kid, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert config-dc→kms: %w", err)
			}
		}
		if sns := sv(attrs.SnsTopicARN); sns != "" {
			sid := store.ResourceID("aws", acct.ID, TypeSNSTopic, sns)
			if err := st.UpsertRelationship(r.ID, sid, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert config-dc→sns: %w", err)
			}
		}
	}
	return nil
}

func resolveConfigRules(acct *account, st *store.Store) error {
	rules, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeConfigRule},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range rules {
		var attrs struct {
			Source *struct {
				Owner            string  `json:"Owner"`
				SourceIdentifier *string `json:"SourceIdentifier"`
			} `json:"Source"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Source == nil {
			continue
		}
		if attrs.Source.Owner != "CUSTOM_LAMBDA" {
			continue
		}
		arn := sv(attrs.Source.SourceIdentifier)
		if !strings.HasPrefix(arn, "arn:aws:lambda:") {
			continue
		}
		lid := store.ResourceID("aws", acct.ID, TypeLambdaFunction, arn)
		if err := st.UpsertRelationship(r.ID, lid, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert config-rule→lambda: %w", err)
		}
	}
	return nil
}
