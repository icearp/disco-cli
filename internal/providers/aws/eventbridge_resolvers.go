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
		resolveEventBridgeRelationships,
		EdgeDecl{TypeEventsRule, TypeEventsEventBus, store.RelAttachedTo},
		EdgeDecl{TypeEventsRule, TypeLambdaFunction, store.RelRoutesTo},
		EdgeDecl{TypeEventsRule, TypeSNSTopic, store.RelRoutesTo},
		EdgeDecl{TypeEventsRule, TypeSQSQueue, store.RelRoutesTo},
		EdgeDecl{TypeEventsRule, TypeKinesisStream, store.RelRoutesTo},
		EdgeDecl{TypeEventsRule, TypeSFNStateMachine, store.RelRoutesTo},
		EdgeDecl{TypeEventsRule, TypeFirehoseDeliveryStream, store.RelRoutesTo},
		EdgeDecl{TypeEventsRule, TypeEventsAPIDestination, store.RelRoutesTo},
	)
	registerResolver(
		resolveEventBridgeAPIDestinationConnection,
		EdgeDecl{TypeEventsAPIDestination, TypeEventsConnection, store.RelUses},
	)
}

// resolveEventBridgeRelationships links each rule to its event bus and targets.
// Target ARNs are type-detected by ARN prefix (same pattern as lambdaESMSourceType).
func resolveEventBridgeRelationships(acct *account, st *store.Store) error {
	rules, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEventsRule},
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
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEventsAPIDestination},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(dests) == 0 {
		return nil
	}
	conns, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEventsConnection},
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

func init() {
	registerResolver(
		resolveEventBridgeBusRefs,
		EdgeDecl{TypeEventsEventBus, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeEventsEventBus, TypeSQSQueue, store.RelRoutesTo},
	)
	registerResolver(
		resolveEventBridgeConnectionRefs,
		EdgeDecl{TypeEventsConnection, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeEventsConnection, TypeSecretsManagerSecret, store.RelUses},
	)
}

// resolveEventBridgeBusRefs wires each event-bus to its KmsKeyIdentifier
// (CMEK) and DeadLetterConfig.Arn (SQS DLQ).
func resolveEventBridgeBusRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEventsEventBus}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	sqsSet, err := scannedIDSet(acct, st, TypeSQSQueue)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KmsKeyIdentifier *string `json:"KmsKeyIdentifier"`
			DeadLetterConfig *struct {
				Arn *string `json:"Arn"`
			} `json:"DeadLetterConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if ref := sv(attrs.KmsKeyIdentifier); ref != "" {
			if keyID, ok := idx.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert event-bus→kms: %w", err)
				}
			}
		}
		if attrs.DeadLetterConfig != nil {
			if dlqARN := sv(attrs.DeadLetterConfig.Arn); strings.Contains(dlqARN, ":sqs:") {
				tgt := store.ResourceID("aws", acct.ID, TypeSQSQueue, dlqARN)
				if sqsSet[tgt] {
					if err := st.UpsertRelationship(r.ID, tgt, store.RelRoutesTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert event-bus→sqs-dlq: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// resolveEventBridgeConnectionRefs wires each connection to its CMEK and
// Secrets Manager auth secret (SecretArn).
func resolveEventBridgeConnectionRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEventsConnection}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	secretSet, err := scannedIDSet(acct, st, TypeSecretsManagerSecret)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KmsKeyIdentifier *string `json:"KmsKeyIdentifier"`
			SecretArn        *string `json:"SecretArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if ref := sv(attrs.KmsKeyIdentifier); ref != "" {
			if keyID, ok := idx.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert connection→kms: %w", err)
				}
			}
		}
		if sa := sv(attrs.SecretArn); strings.Contains(sa, ":secretsmanager:") {
			tgt := store.ResourceID("aws", acct.ID, TypeSecretsManagerSecret, sa)
			if secretSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert connection→secret: %w", err)
				}
			}
		}
	}
	return nil
}
