package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveSNSSubscriptionRefs,
		EdgeDecl{TypeSNSSubscription, TypeSNSTopic, store.RelAttachedTo},
		EdgeDecl{TypeSNSSubscription, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeSNSSubscription, TypeSQSQueue, store.RelUses},
	)
	registerResolver(
		resolveSNSTopicPolicyToTopic,
		EdgeDecl{TypeSNSTopicPolicy, TypeSNSTopic, store.RelAttachedTo},
	)
}

// resolveSNSSubscriptionRefs wires each subscription to its topic
// (TopicArn) and — when the protocol is lambda/sqs — to the underlying
// function or queue endpoint.
func resolveSNSSubscriptionRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSNSSubscription}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	topicSet, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	fnSet, err := scannedIDSet(acct, st, TypeLambdaFunction)
	if err != nil {
		return err
	}
	qSet, err := scannedIDSet(acct, st, TypeSQSQueue)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			TopicArn *string `json:"TopicArn"`
			Endpoint *string `json:"Endpoint"`
			Protocol *string `json:"Protocol"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if t := sv(attrs.TopicArn); t != "" {
			tgtID := store.ResourceID("aws", acct.ID, t)
			if topicSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert sns sub→topic: %w", err)
				}
			}
		}
		ep := sv(attrs.Endpoint)
		if ep == "" {
			continue
		}
		switch sv(attrs.Protocol) {
		case "lambda":
			tgtID := store.ResourceID("aws", acct.ID, ep)
			if fnSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert sns sub→lambda: %w", err)
				}
			}
		case "sqs":
			tgtID := store.ResourceID("aws", acct.ID, ep)
			if qSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert sns sub→sqs: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveSNSTopicPolicyToTopic wires the synthetic topic-policy row to its
// parent topic via NativeID `{topicARN}/policy` strip.
func resolveSNSTopicPolicyToTopic(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSNSTopicPolicy}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	topicSet, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parent := strings.TrimSuffix(r.NativeID, "/policy")
		if parent == r.NativeID {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, parent)
		if !topicSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert sns topic-policy→topic: %w", err)
		}
	}
	return nil
}
