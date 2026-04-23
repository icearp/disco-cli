package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveSQSQueueRelationships) }

// resolveSQSQueueRelationships links each queue to its KMS key and to the
// dead-letter queue configured via RedrivePolicy.
func resolveSQSQueueRelationships(acct *account, st *store.Store) error {
	queues, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSQSQueue},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range queues {
		var attrs map[string]string
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		// Queue → KMS (customer-managed only). AWS-managed key alias/aws/sqs is skipped.
		if keyRef := attrs["KmsMasterKeyId"]; keyRef != "" && !strings.HasPrefix(keyRef, "alias/aws/") {
			target := kmsKeyTargetARN(keyRef, sv(r.Region), acct.ID)
			keyID := store.ResourceID("aws", acct.ID, TypeKMSKey, target)
			if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert sqs-queue→kms: %w", err)
			}
		}
		// Queue → DLQ via RedrivePolicy.
		if rp := attrs["RedrivePolicy"]; rp != "" {
			if dlqARN := redrivePolicyDLQ(rp); dlqARN != "" {
				dlqID := store.ResourceID("aws", acct.ID, TypeSQSQueue, dlqARN)
				if err := st.UpsertRelationship(r.ID, dlqID, store.RelRoutesTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert sqs-queue→dlq: %w", err)
				}
			}
		}
	}
	return nil
}
