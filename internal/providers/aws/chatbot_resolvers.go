package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		noopChatbotHierarchy,
		EdgeDecl{TypeChatbotSlackChannelConfiguration, TypeChatbotSlackUserIdentity, store.RelContains},
		EdgeDecl{TypeChatbotMicrosoftTeamsChannelConfiguration, TypeChatbotMicrosoftTeamsUserIdentity, store.RelContains},
	)
	registerResolver(
		resolveChatbotRelationships,
		EdgeDecl{TypeChatbotChimeWebhookConfiguration, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeChatbotChimeWebhookConfiguration, TypeSNSTopic, store.RelUses},
		EdgeDecl{TypeChatbotSlackUserIdentity, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeChatbotMicrosoftTeamsUserIdentity, TypeIAMRole, store.RelUses},
	)
}

// noopChatbotHierarchy only carries the EdgeDecl metadata for the channel
// configuration -> user identity contains edges, which the scanner records
// itself (scanChatbotSlackUserIdentities, scanChatbotTeamsUserIdentities).
func noopChatbotHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveChatbotRelationships emits uses edges from each Chime webhook
// configuration to its IAM role and SNS topics, and from each Slack and
// Microsoft Teams user identity to the IAM role it maps the chat user to.
func resolveChatbotRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Limit: util.AllResources,
		Types: []string{TypeChatbotChimeWebhookConfiguration, TypeChatbotSlackUserIdentity, TypeChatbotMicrosoftTeamsUserIdentity},
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleIDs, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	topicIDs, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	// SnsTopicArns is set only on Chime webhook configurations.
	type attrs struct {
		IamRoleArn   *string  `json:"IamRoleArn"`
		SnsTopicArns []string `json:"SnsTopicArns"`
	}
	for _, r := range rows {
		var a attrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			return fmt.Errorf("decode %s %s: %w", r.Type, r.NativeID, err)
		}
		if err := upsertIfScanned(acct, st, roleIDs, r.ID, sv(a.IamRoleArn), store.RelUses, "chatbot→iam-role"); err != nil {
			return err
		}
		for _, topicARN := range a.SnsTopicArns {
			if err := upsertIfScanned(acct, st, topicIDs, r.ID, topicARN, store.RelUses, "chatbot→sns-topic"); err != nil {
				return err
			}
		}
	}
	return nil
}
