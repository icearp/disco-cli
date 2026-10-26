package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveSNSTopicRelationships,
		EdgeDecl{TypeSNSTopic, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeSNSTopic, TypeSQSQueue, store.RelRoutesTo},
	)
}

// resolveSNSTopicRelationships links each SNS topic to its KMS key (customer-
// managed key encrypting messages at rest) and to its dead-letter SQS queue
// (parsed from RedrivePolicy).
func resolveSNSTopicRelationships(acct *account, st *store.Store) error {
	topics, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSNSTopic},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range topics {
		var attrs map[string]string
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		// Topic → KMS.
		if keyID, ok := kmsIdx.resolveKMSKeyID(attrs["KmsMasterKeyId"], sv(r.Region), acct.ID); ok {
			if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert sns-topic→kms: %w", err)
			}
		}
		// Topic → SQS DLQ via RedrivePolicy (JSON-encoded string inside attrs).
		if rp := attrs["RedrivePolicy"]; rp != "" {
			dlqARN := redrivePolicyDLQ(rp)
			if dlqARN != "" {
				dlqID := store.ResourceID("aws", acct.ID, dlqARN)
				if err := st.UpsertRelationship(r.ID, dlqID, store.RelRoutesTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert sns-topic→dlq: %w", err)
				}
			}
		}
	}
	return nil
}

// redrivePolicyDLQ extracts deadLetterTargetArn from a RedrivePolicy JSON string.
// Returns "" if absent or malformed.
func redrivePolicyDLQ(rp string) string {
	var policy struct {
		DeadLetterTargetArn string `json:"deadLetterTargetArn"`
	}
	if err := json.Unmarshal([]byte(rp), &policy); err != nil {
		return ""
	}
	return policy.DeadLetterTargetArn
}
