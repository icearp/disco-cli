package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveSNSTopicRelationships,
		EdgeDecl{TypeSNSTopic, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeSNSTopic, TypeSQSQueue, store.RelRoutesTo},
	)
}

// resolveSNSTopicRelationships links each SNS topic to its KMS key (when a
// customer-managed key encrypts messages at rest) and to the SQS queue
// configured as its dead-letter target (parsed from RedrivePolicy).
func resolveSNSTopicRelationships(acct *account, st *store.Store) error {
	topics, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSNSTopic},
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
				dlqID := store.ResourceID("aws", acct.ID, TypeSQSQueue, dlqARN)
				if err := st.UpsertRelationship(r.ID, dlqID, store.RelRoutesTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert sns-topic→dlq: %w", err)
				}
			}
		}
	}
	return nil
}

// kmsKeyTargetARN normalizes a KMS key reference (key ID, alias, or ARN) to a
// full key ARN so it matches the NativeID of a scanned KMS key. Aliases and
// bare key IDs are assumed to live in the caller's account and region.
func kmsKeyTargetARN(ref, region, acct string) string {
	if strings.HasPrefix(ref, "arn:") {
		return ref
	}
	if strings.HasPrefix(ref, "alias/") {
		return fmt.Sprintf("arn:aws:kms:%s:%s:%s", region, acct, ref)
	}
	return fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", region, acct, ref)
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
