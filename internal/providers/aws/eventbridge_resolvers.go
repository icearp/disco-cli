package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveEventBridgeRelationships)
	registerResolver(resolveEventBridgeAPIDestinationConnection)
}

// resolveEventBridgeRelationships links each rule to its event bus and targets.
// Target ARNs are type-detected by ARN prefix (same pattern as lambdaESMSourceType).
func resolveEventBridgeRelationships(acct *account, st *store.Store) error {
	rules, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEventsRule},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}

	type target struct {
		Arn *string `json:"Arn"`
	}
	type ruleInner struct {
		EventBusArn  *string `json:"EventBusArn"`
		EventBusName *string `json:"EventBusName"`
	}
	type attrs struct {
		Rule    ruleInner `json:"Rule"`
		Targets []target  `json:"Targets"`
	}

	for _, r := range rules {
		var a attrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		// Rule → EventBus
		busARN := sv(a.Rule.EventBusArn)
		if busARN == "" && sv(a.Rule.EventBusName) != "" {
			// Default bus has no ARN field; reconstruct from region+account.
			region := sv(r.Region)
			busARN = fmt.Sprintf("arn:aws:events:%s:%s:event-bus/%s", region, acct.ID, sv(a.Rule.EventBusName))
		}
		if busARN != "" {
			busID := store.ResourceID("aws", acct.ID, TypeEventsEventBus, busARN)
			if err := st.UpsertRelationship(r.ID, busID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert events-rule→bus: %w", err)
			}
		}
		// Rule → targets
		for _, t := range a.Targets {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			targetType := eventBridgeTargetType(arn)
			if targetType == "" {
				continue
			}
			targetID := store.ResourceID("aws", acct.ID, targetType, arn)
			if err := st.UpsertRelationship(r.ID, targetID, store.RelRoutesTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert events-rule→target: %w", err)
			}
		}
	}
	return nil
}

// eventBridgeTargetType returns the disco resource type for a target ARN.
// Returns "" for target types we don't track.
func eventBridgeTargetType(arn string) string {
	switch {
	case strings.Contains(arn, ":function:"):
		return TypeLambdaFunction
	case strings.Contains(arn, ":sns:"):
		return TypeSNSTopic
	case strings.Contains(arn, ":sqs:"):
		return TypeSQSQueue
	case strings.Contains(arn, ":kinesis:"):
		return TypeKinesisStream
	case strings.Contains(arn, ":states:") && strings.Contains(arn, ":stateMachine:"):
		return TypeSFNStateMachine
	case strings.Contains(arn, ":firehose:") && strings.Contains(arn, ":deliverystream/"):
		return TypeFirehoseDeliveryStream
	case strings.Contains(arn, ":events:") && strings.Contains(arn, ":api-destination/"):
		return TypeEventsAPIDestination
	default:
		return ""
	}
}

// resolveEventBridgeAPIDestinationConnection emits uses edges from each API
// destination to the connection it references (ConnectionArn). FK-safe via
// scanned-connection id set — cross-account refs silently skip.
func resolveEventBridgeAPIDestinationConnection(acct *account, st *store.Store) error {
	dests, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEventsAPIDestination},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(dests) == 0 {
		return nil
	}
	conns, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEventsConnection},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	connIDs := make(map[string]struct{}, len(conns))
	for _, c := range conns {
		connIDs[c.ID] = struct{}{}
	}

	type attrs struct {
		ConnectionArn *string `json:"ConnectionArn"`
	}
	for _, d := range dests {
		var a attrs
		if err := json.Unmarshal([]byte(d.AttributesJSON), &a); err != nil {
			continue
		}
		connARN := sv(a.ConnectionArn)
		if connARN == "" {
			continue
		}
		connID := store.ResourceID("aws", acct.ID, TypeEventsConnection, connARN)
		if _, ok := connIDs[connID]; !ok {
			continue
		}
		if err := st.UpsertRelationship(d.ID, connID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert events-api-destination→connection: %w", err)
		}
	}
	return nil
}
