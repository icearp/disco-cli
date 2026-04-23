package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveSSMRelationships) }

// resolveSSMRelationships emits edges for SecureString parameters → KMS keys
// (or aliases). Skip AWS-managed aliases (alias/aws/*) since those keys are
// never scanned.
func resolveSSMRelationships(acct *account, st *store.Store) error {
	params, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSSMParameter},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	type attrs struct {
		KeyId *string `json:"KeyId"`
		Type  string  `json:"Type"`
	}
	for _, r := range params {
		var a attrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		if a.Type != "SecureString" {
			continue
		}
		key := sv(a.KeyId)
		if key == "" || strings.HasPrefix(key, "alias/aws/") {
			continue
		}
		region := ""
		if r.Region != nil {
			region = *r.Region
		}
		var targetType, targetARN string
		switch {
		case strings.HasPrefix(key, "arn:"):
			// Full ARN — classify by the segment right before the separator.
			if strings.Contains(key, ":alias/") {
				targetType = TypeKMSAlias
			} else {
				targetType = TypeKMSKey
			}
			targetARN = key
		case strings.HasPrefix(key, "alias/"):
			targetType = TypeKMSAlias
			targetARN = fmt.Sprintf("arn:aws:kms:%s:%s:%s", region, acct.ID, key)
		default:
			targetType = TypeKMSKey
			targetARN = fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", region, acct.ID, key)
		}
		keyID := store.ResourceID("aws", acct.ID, targetType, targetARN)
		if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert ssm-parameter→kms: %w", err)
		}
	}
	return nil
}
