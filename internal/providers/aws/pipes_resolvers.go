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
		resolvePipesPipeRefs,
		EdgeDecl{TypePipesPipe, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypePipesPipe, TypeKMSKey, store.RelUses},
		EdgeDecl{TypePipesPipe, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypePipesPipe, TypeSNSTopic, store.RelRoutesTo},
		EdgeDecl{TypePipesPipe, TypeSQSQueue, store.RelRoutesTo},
		EdgeDecl{TypePipesPipe, TypeKinesisStream, store.RelRoutesTo},
		EdgeDecl{TypePipesPipe, TypeSFNStateMachine, store.RelRoutesTo},
		EdgeDecl{TypePipesPipe, TypeFirehoseDeliveryStream, store.RelRoutesTo},
		EdgeDecl{TypePipesPipe, TypeEventsAPIDestination, store.RelRoutesTo},
		EdgeDecl{TypePipesPipe, TypeEventsEventBus, store.RelRoutesTo},
		EdgeDecl{TypePipesPipe, TypeDynamoDBTable, store.RelRoutesTo},
		EdgeDecl{TypePipesPipe, TypeMSKCluster, store.RelRoutesTo},
	)
}

// pipesARNType extends eventBridgeTargetType with pipes-specific source
// types (Kafka clusters, DynamoDB streams, EventBus targets).
func pipesARNType(arn string) string {
	switch {
	case strings.Contains(arn, ":kafka:") && strings.Contains(arn, ":cluster/"):
		return TypeMSKCluster
	case strings.Contains(arn, ":dynamodb:") && strings.Contains(arn, ":table/"):
		return TypeDynamoDBTable
	case strings.Contains(arn, ":events:") && strings.Contains(arn, ":event-bus/"):
		return TypeEventsEventBus
	}
	return eventBridgeTargetType(arn)
}

// resolvePipesPipeRefs walks DescribePipe body and emits RoleArn (assumes),
// KmsKeyIdentifier (uses), and Source/Target/Enrichment ARNs via substring
// dispatch. Skips AWS service-integration ARNs (`:::` empty account/region
// segments).
func resolvePipesPipeRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypePipesPipe}, Limit: util.AllResources,
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
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	tgtSets := map[string]map[string]bool{}
	for _, t := range []string{
		TypeLambdaFunction, TypeSNSTopic, TypeSQSQueue, TypeKinesisStream,
		TypeSFNStateMachine, TypeFirehoseDeliveryStream, TypeEventsAPIDestination,
		TypeEventsEventBus, TypeDynamoDBTable, TypeMSKCluster,
	} {
		set, err := scannedIDSet(acct, st, t)
		if err != nil {
			return err
		}
		tgtSets[t] = set
	}
	for _, r := range rows {
		var attrs struct {
			RoleArn          *string `json:"RoleArn"`
			KmsKeyIdentifier *string `json:"KmsKeyIdentifier"`
			Source           *string `json:"Source"`
			Target           *string `json:"Target"`
			Enrichment       *string `json:"Enrichment"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if rarn := sv(attrs.RoleArn); strings.Contains(rarn, ":role/") {
			tgt := store.ResourceID("aws", acct.ID, TypeIAMRole, rarn)
			if roleSet[tgt] {
				if err := st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert pipe→role: %w", err)
				}
			}
		}
		if ref := sv(attrs.KmsKeyIdentifier); ref != "" {
			if keyID, ok := idx.resolveKMSKeyID(ref, region, acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert pipe→kms: %w", err)
				}
			}
		}
		for _, ep := range []struct {
			arn  string
			kind string
		}{
			{sv(attrs.Source), "source"},
			{sv(attrs.Target), "target"},
			{sv(attrs.Enrichment), "enrichment"},
		} {
			if ep.arn == "" || strings.Contains(ep.arn, ":::") {
				continue
			}
			ttype := pipesARNType(ep.arn)
			if ttype == "" {
				continue
			}
			set := tgtSets[ttype]
			tgt := store.ResourceID("aws", acct.ID, ttype, ep.arn)
			if !set[tgt] {
				continue
			}
			rel := store.RelRoutesTo
			if ttype == TypeLambdaFunction && ep.kind == "enrichment" {
				rel = store.RelUses
			}
			if err := st.UpsertRelationship(r.ID, tgt, rel, "directed", nil); err != nil {
				return fmt.Errorf("upsert pipe→%s: %w", ttype, err)
			}
		}
	}
	return nil
}
