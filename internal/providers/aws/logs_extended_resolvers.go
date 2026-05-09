package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveLogsLogStreamParent,
		EdgeDecl{TypeLogsLogStream, TypeLogsLogGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveLogsMetricFilterParent,
		EdgeDecl{TypeLogsMetricFilter, TypeLogsLogGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveLogsSubscriptionFilterRefs,
		EdgeDecl{TypeLogsSubscriptionFilter, TypeLogsLogGroup, store.RelAttachedTo},
		EdgeDecl{TypeLogsSubscriptionFilter, TypeLambdaFunction, store.RelRoutesTo},
		EdgeDecl{TypeLogsSubscriptionFilter, TypeKinesisStream, store.RelRoutesTo},
		EdgeDecl{TypeLogsSubscriptionFilter, TypeFirehoseDeliveryStream, store.RelRoutesTo},
		EdgeDecl{TypeLogsSubscriptionFilter, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		resolveLogsDestinationTargets,
		EdgeDecl{TypeLogsDestination, TypeKinesisStream, store.RelRoutesTo},
		EdgeDecl{TypeLogsDestination, TypeFirehoseDeliveryStream, store.RelRoutesTo},
		EdgeDecl{TypeLogsDestination, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		resolveLogsTransformerParent,
		EdgeDecl{TypeLogsTransformer, TypeLogsLogGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveLogsQueryDefinitionLogGroups,
		EdgeDecl{TypeLogsQueryDefinition, TypeLogsLogGroup, store.RelUses},
	)
}

// logGroupARNFromChild strips a trailing `/<kind>/<name>` from a synthetic
// child NativeID (`{lgARN}/{stream|filter|subscription}/{name}`) to recover
// the parent log-group ARN. Returns "" when the input has no recognized
// child segment.
func logGroupARNFromChild(arn string) string {
	for _, kind := range []string{"/stream/", "/filter/", "/subscription/"} {
		if i := strings.Index(arn, kind); i >= 0 {
			return arn[:i]
		}
	}
	return ""
}

func resolveLogsLogStreamParent(acct *account, st *store.Store) error {
	return resolveLogsChildToGroup(acct, st, TypeLogsLogStream, "stream")
}

func resolveLogsMetricFilterParent(acct *account, st *store.Store) error {
	return resolveLogsChildToGroup(acct, st, TypeLogsMetricFilter, "metric-filter")
}

func resolveLogsChildToGroup(acct *account, st *store.Store, ctype, label string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{ctype},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	groupSet, err := scannedIDSet(acct, st, TypeLogsLogGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parent := logGroupARNFromChild(r.NativeID)
		if parent == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeLogsLogGroup, parent)
		if !groupSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert logs-%s→log-group: %w", label, err)
		}
	}
	return nil
}

// resolveLogsSubscriptionFilterRefs links each subscription filter to its
// parent log-group (NativeID parse) and to the DestinationArn target — a
// lambda function, kinesis stream, or firehose delivery stream. RoleArn
// (when present, for IAM-pass-through targets like kinesis) emits an
// assumes edge.
func resolveLogsSubscriptionFilterRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLogsSubscriptionFilter},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	groupSet, err := scannedIDSet(acct, st, TypeLogsLogGroup)
	if err != nil {
		return err
	}
	lambdaSet, err := scannedIDSet(acct, st, TypeLambdaFunction)
	if err != nil {
		return err
	}
	streamSet, err := scannedIDSet(acct, st, TypeKinesisStream)
	if err != nil {
		return err
	}
	firehoseSet, err := scannedIDSet(acct, st, TypeFirehoseDeliveryStream)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if parent := logGroupARNFromChild(r.NativeID); parent != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeLogsLogGroup, parent)
			if groupSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert logs-sub→log-group: %w", err)
				}
			}
		}
		var attrs struct {
			DestinationArn *string `json:"DestinationArn"`
			RoleArn        *string `json:"RoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		dest := sv(attrs.DestinationArn)
		if dest != "" {
			var tgtType string
			var set map[string]bool
			switch {
			case strings.HasPrefix(dest, "arn:aws:lambda:"):
				tgtType, set = TypeLambdaFunction, lambdaSet
			case strings.HasPrefix(dest, "arn:aws:kinesis:"):
				tgtType, set = TypeKinesisStream, streamSet
			case strings.HasPrefix(dest, "arn:aws:firehose:"):
				tgtType, set = TypeFirehoseDeliveryStream, firehoseSet
			}
			if tgtType != "" {
				tgtID := store.ResourceID("aws", acct.ID, tgtType, dest)
				if set[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelRoutesTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert logs-sub→%s: %w", tgtType, err)
					}
				}
			}
		}
		if arn := sv(attrs.RoleArn); arn != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, arn)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert logs-sub→role: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveLogsDestinationTargets walks each destination's TargetArn (kinesis
// or firehose) and RoleArn.
func resolveLogsDestinationTargets(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLogsDestination},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	streamSet, err := scannedIDSet(acct, st, TypeKinesisStream)
	if err != nil {
		return err
	}
	firehoseSet, err := scannedIDSet(acct, st, TypeFirehoseDeliveryStream)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			TargetArn *string `json:"TargetArn"`
			RoleArn   *string `json:"RoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if arn := sv(attrs.TargetArn); arn != "" {
			var tgtType string
			var set map[string]bool
			switch {
			case strings.HasPrefix(arn, "arn:aws:kinesis:"):
				tgtType, set = TypeKinesisStream, streamSet
			case strings.HasPrefix(arn, "arn:aws:firehose:"):
				tgtType, set = TypeFirehoseDeliveryStream, firehoseSet
			}
			if tgtType != "" {
				tgtID := store.ResourceID("aws", acct.ID, tgtType, arn)
				if set[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelRoutesTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert logs-dest→%s: %w", tgtType, err)
					}
				}
			}
		}
		if arn := sv(attrs.RoleArn); arn != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, arn)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert logs-dest→role: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveLogsTransformerParent — transformer NativeID is the parent log-group
// ARN (singleton per group); emit attached-to.
func resolveLogsTransformerParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLogsTransformer},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	groupSet, err := scannedIDSet(acct, st, TypeLogsLogGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		tgtID := store.ResourceID("aws", acct.ID, TypeLogsLogGroup, r.NativeID)
		if !groupSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert logs-transformer→log-group: %w", err)
		}
	}
	return nil
}

// resolveLogsQueryDefinitionLogGroups walks each query's LogGroupNames[] and
// emits uses → log-group for each named group present in the local store.
func resolveLogsQueryDefinitionLogGroups(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLogsQueryDefinition},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	groupSet, err := scannedIDSet(acct, st, TypeLogsLogGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			LogGroupNames []string `json:"LogGroupNames"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		for _, name := range attrs.LogGroupNames {
			if name == "" {
				continue
			}
			arn := logGroupNativeIDFromName(acct.ID, region, name)
			tgtID := store.ResourceID("aws", acct.ID, TypeLogsLogGroup, arn)
			if !groupSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert logs-query-def→log-group: %w", err)
			}
		}
	}
	return nil
}
