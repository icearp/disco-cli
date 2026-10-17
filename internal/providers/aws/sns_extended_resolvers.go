package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
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
	registerResolver(
		resolveSNSPlatformApplicationRefs,
		EdgeDecl{TypeSNSPlatformApplication, TypeSNSTopic, store.RelUses},
		EdgeDecl{TypeSNSPlatformApplication, TypeIAMRole, store.RelUses},
	)
}

// resolveSNSPlatformApplicationRefs wires each platform application to the
// topics its endpoint and delivery-failure events are published to, and to
// the IAM roles SNS assumes to log delivery feedback.
func resolveSNSPlatformApplicationRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSNSPlatformApplication}, Limit: util.AllResources,
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
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var app struct {
			Attributes map[string]string `json:"Attributes"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &app); err != nil {
			return fmt.Errorf("decode sns platform-application %s: %w", r.NativeID, err)
		}
		for _, key := range []string{"EventEndpointCreated", "EventEndpointDeleted", "EventEndpointUpdated", "EventDeliveryFailure"} {
			if err := upsertIfScanned(acct, st, topicSet, r.ID, app.Attributes[key], store.RelUses, "sns platform-application→topic"); err != nil {
				return err
			}
		}
		for _, key := range []string{"SuccessFeedbackRoleArn", "FailureFeedbackRoleArn"} {
			if err := upsertIfScanned(acct, st, roleSet, r.ID, app.Attributes[key], store.RelUses, "sns platform-application→role"); err != nil {
				return err
			}
		}
	}
	return nil
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
