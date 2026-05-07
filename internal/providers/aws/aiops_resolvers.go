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
		resolveAIOpsRelationships,
		EdgeDecl{TypeAIOpsInvestigationGroup, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeAIOpsInvestigationGroup, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeAIOpsInvestigationGroup, TypeSNSTopic, store.RelUses},
	)
}

// resolveAIOpsRelationships emits edges from each AIOps investigation group to
// its KMS key (uses), IAM role (uses), and SNS topics surfaced via the chatbot
// notification channel map. ChatbotNotificationChannel keys are SNS topic ARNs.
func resolveAIOpsRelationships(acct *account, st *store.Store) error {
	groups, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAIOpsInvestigationGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return nil
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	roleIDs, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	topicIDs, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}

	type encCfg struct {
		KmsKeyID *string `json:"KmsKeyId"`
	}
	type attrs struct {
		Arn                        *string             `json:"Arn"`
		RoleArn                    *string             `json:"RoleArn"`
		EncryptionConfiguration    *encCfg             `json:"EncryptionConfiguration"`
		ChatbotNotificationChannel map[string][]string `json:"ChatbotNotificationChannel"`
	}
	for _, g := range groups {
		var a attrs
		if err := json.Unmarshal([]byte(g.AttributesJSON), &a); err != nil {
			continue
		}
		region := sv(g.Region)
		if a.EncryptionConfiguration != nil {
			if keyID, ok := kmsIdx.resolveKMSKeyID(sv(a.EncryptionConfiguration.KmsKeyID), region, acct.ID); ok {
				if err := st.UpsertRelationship(g.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert aiops→kms: %w", err)
				}
			}
		}
		if roleArn := sv(a.RoleArn); roleArn != "" {
			roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, roleArn)
			if _, ok := roleIDs[roleID]; ok {
				if err := st.UpsertRelationship(g.ID, roleID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert aiops→iam-role: %w", err)
				}
			}
		}
		for topicARN := range a.ChatbotNotificationChannel {
			if !strings.HasPrefix(topicARN, "arn:aws:sns:") {
				continue
			}
			topicID := store.ResourceID("aws", acct.ID, TypeSNSTopic, topicARN)
			if _, ok := topicIDs[topicID]; ok {
				if err := st.UpsertRelationship(g.ID, topicID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert aiops→sns: %w", err)
				}
			}
		}
	}
	return nil
}
