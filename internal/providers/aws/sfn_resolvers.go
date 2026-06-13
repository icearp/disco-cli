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
		resolveSFNRelationships,
		EdgeDecl{TypeSFNStateMachine, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeSFNStateMachine, TypeLogsLogGroup, store.RelUses},
		EdgeDecl{TypeSFNStateMachine, TypeLambdaFunction, store.RelRoutesTo},
		EdgeDecl{TypeSFNStateMachine, TypeSNSTopic, store.RelRoutesTo},
		EdgeDecl{TypeSFNStateMachine, TypeSQSQueue, store.RelRoutesTo},
		EdgeDecl{TypeSFNStateMachine, TypeKinesisStream, store.RelRoutesTo},
		EdgeDecl{TypeSFNStateMachine, TypeFirehoseDeliveryStream, store.RelRoutesTo},
		EdgeDecl{TypeSFNStateMachine, TypeDynamoDBTable, store.RelRoutesTo},
		EdgeDecl{TypeSFNStateMachine, TypeSFNStateMachine, store.RelRoutesTo},
	)
}

// resolveSFNRelationships links each state machine to its IAM role, any
// CloudWatch log group destinations, and any downstream AWS services
// referenced from task states in the state-machine Definition.
func resolveSFNRelationships(acct *account, st *store.Store) error {
	sms, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSFNStateMachine},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}

	type cwDest struct {
		CloudWatchLogsLogGroup *struct {
			LogGroupArn *string `json:"LogGroupArn"`
		} `json:"CloudWatchLogsLogGroup"`
	}
	type logCfg struct {
		Destinations []cwDest `json:"Destinations"`
	}
	type smAttrs struct {
		RoleArn              *string `json:"RoleArn"`
		Definition           *string `json:"Definition"`
		LoggingConfiguration *logCfg `json:"LoggingConfiguration"`
	}

	for _, r := range sms {
		var a smAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		// State machine → IAM role (execution role)
		if sv(a.RoleArn) != "" {
			roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, *a.RoleArn)
			if err := st.UpsertRelationship(r.ID, roleID, store.RelAssumes, "directed", nil); err != nil {
				return fmt.Errorf("upsert sfn→role: %w", err)
			}
		}
		// State machine → log groups
		if a.LoggingConfiguration != nil {
			for _, d := range a.LoggingConfiguration.Destinations {
				if d.CloudWatchLogsLogGroup == nil {
					continue
				}
				lgARN := sv(d.CloudWatchLogsLogGroup.LogGroupArn)
				if lgARN == "" {
					continue
				}
				lgARN = strings.TrimSuffix(lgARN, ":*")
				lgID := store.ResourceID("aws", acct.ID, TypeLogsLogGroup, lgARN)
				if err := st.UpsertRelationship(r.ID, lgID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert sfn→log-group: %w", err)
				}
			}
		}
		// State machine → downstream service targets via Definition.States.*.Resource
		if def := sv(a.Definition); def != "" {
			seen := make(map[string]bool)
			for _, arn := range extractSFNStateResources(def) {
				targetType := sfnTargetType(arn)
				if targetType == "" {
					continue
				}
				tid := store.ResourceID("aws", acct.ID, targetType, arn)
				if seen[tid] {
					continue
				}
				seen[tid] = true
				if err := st.UpsertRelationship(r.ID, tid, store.RelRoutesTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert sfn→target: %w", err)
				}
			}
		}
	}
	return nil
}

// extractSFNStateResources walks a State Machine Definition JSON string and
// collects each state's Resource field (AWS service integration ARN or
// lambda ARN). Non-ARN "Resource" values like "arn:aws:states:::lambda:invoke"
// are also returned — filtering happens in sfnTargetType.
func extractSFNStateResources(definition string) []string {
	var parsed struct {
		States map[string]struct {
			Resource   *string `json:"Resource"`
			Parameters any     `json:"Parameters"`
		} `json:"States"`
	}
	if err := json.Unmarshal([]byte(definition), &parsed); err != nil {
		return nil
	}
	var out []string
	for _, s := range parsed.States {
		if s.Resource != nil && *s.Resource != "" {
			out = append(out, *s.Resource)
		}
		// Service integrations often place the target ARN inside Parameters
		// (e.g. "FunctionName", "TopicArn", "QueueUrl", "TableName",
		// "StreamName", "DeliveryStreamName"). Recursively scan Parameters
		// for any string-valued field containing an ARN.
		collectARNs(s.Parameters, &out)
	}
	return out
}

// collectARNs recursively walks a decoded JSON value and appends any string
// that looks like an AWS ARN to out.
func collectARNs(v any, out *[]string) {
	switch x := v.(type) {
	case string:
		if strings.HasPrefix(x, "arn:aws:") {
			*out = append(*out, x)
		}
	case map[string]any:
		for _, vv := range x {
			collectARNs(vv, out)
		}
	case []any:
		for _, vv := range x {
			collectARNs(vv, out)
		}
	}
}

// sfnTargetType maps a target ARN seen in a state-machine Definition to the
// corresponding disco resource type. Returns "" for built-in service ARNs
// like "arn:aws:states:::lambda:invoke" and anything we don't track.
func sfnTargetType(arn string) string {
	// Built-in service integration ARNs like "arn:aws:states:::sns:publish"
	// carry ":::" because region+account segments are empty. Skip them —
	// they are verbs, not resources.
	if strings.Contains(arn, ":::") {
		return ""
	}
	switch {
	case strings.Contains(arn, ":function:"):
		return TypeLambdaFunction
	case strings.Contains(arn, ":sns:"):
		return TypeSNSTopic
	case strings.Contains(arn, ":sqs:"):
		return TypeSQSQueue
	case strings.Contains(arn, ":kinesis:") && strings.Contains(arn, ":stream/"):
		return TypeKinesisStream
	case strings.Contains(arn, ":firehose:") && strings.Contains(arn, ":deliverystream/"):
		return TypeFirehoseDeliveryStream
	case strings.Contains(arn, ":dynamodb:") && strings.Contains(arn, ":table/"):
		return TypeDynamoDBTable
	case strings.Contains(arn, ":states:") && strings.Contains(arn, ":stateMachine:"):
		return TypeSFNStateMachine
	default:
		return ""
	}
}
