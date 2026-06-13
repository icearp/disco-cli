package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveApplicationInsightsRelationships,
		EdgeDecl{TypeApplicationInsightsApplication, TypeSNSTopic, store.RelUses},
	)
}

// resolveApplicationInsightsRelationships emits application → SNS topic edges
// for OpsItemSNSTopicArn and SNSNotificationArn (legacy field). Both reference
// SNS topics that disco may have scanned; FK-safe via scanned topic id set.
func resolveApplicationInsightsRelationships(acct *account, st *store.Store) error {
	apps, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeApplicationInsightsApplication},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(apps) == 0 {
		return nil
	}
	topicIDs, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	type attrs struct {
		OpsItemSNSTopicArn *string `json:"OpsItemSNSTopicArn"`
		SNSNotificationArn *string `json:"SNSNotificationArn"`
	}
	emit := func(srcID, topicARN string) error {
		if topicARN == "" {
			return nil
		}
		topicID := store.ResourceID("aws", acct.ID, TypeSNSTopic, topicARN)
		if _, ok := topicIDs[topicID]; !ok {
			return nil
		}
		if err := st.UpsertRelationship(srcID, topicID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert applicationinsights→sns: %w", err)
		}
		return nil
	}
	for _, a := range apps {
		var at attrs
		if err := json.Unmarshal([]byte(a.AttributesJSON), &at); err != nil {
			continue
		}
		if err := emit(a.ID, sv(at.OpsItemSNSTopicArn)); err != nil {
			return err
		}
		if err := emit(a.ID, sv(at.SNSNotificationArn)); err != nil {
			return err
		}
	}
	return nil
}
