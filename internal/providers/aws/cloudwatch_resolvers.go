package aws

import (
	"encoding/json"
	"fmt"
	"regexp"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveCloudWatchRelationships) }

// resolveCloudWatchRelationships runs both CloudWatch relationship passes.
func resolveCloudWatchRelationships(acct *account, st *store.Store) error {
	if err := resolveAlarmSNSActions(acct, st); err != nil {
		return err
	}
	return resolveCompositeAlarmChildren(acct, st)
}

// resolveAlarmSNSActions links both metric alarms and composite alarms to the
// SNS topics they notify via AlarmActions, OKActions, and
// InsufficientDataActions. Relationship: uses.
func resolveAlarmSNSActions(acct *account, st *store.Store) error {
	resources, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeCloudWatchAlarm, TypeCloudWatchCompositeAlarm},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range resources {
		var attrs struct {
			AlarmActions            []string `json:"AlarmActions"`
			OKActions               []string `json:"OKActions"`
			InsufficientDataActions []string `json:"InsufficientDataActions"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		seen := make(map[string]bool)
		all := append(append(attrs.AlarmActions, attrs.OKActions...), attrs.InsufficientDataActions...)
		for _, arn := range all {
			if arn == "" || seen[arn] {
				continue
			}
			seen[arn] = true
			topicID := store.ResourceID("aws", acct.ID, TypeSNSTopic, arn)
			if err := st.UpsertRelationship(r.ID, topicID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert alarm→sns relationship: %w", err)
			}
		}
	}
	return nil
}

// alarmRuleTokenRe matches quoted strings inside a CloudWatch composite alarm
// AlarmRule expression. Tokens are alarm names or ARNs.
var alarmRuleTokenRe = regexp.MustCompile(`"([^"]+)"`)

// resolveCompositeAlarmChildren links each composite alarm to the child alarms
// referenced in its AlarmRule expression. The rule is a boolean expression
// such as: ALARM("arn:aws:cloudwatch:…") AND OK("other-alarm")
// Tokens are resolved against a pre-built map from NativeID/Name → store ID.
// Relationship: contains.
func resolveCompositeAlarmChildren(acct *account, st *store.Store) error {
	// Load all known alarms (both types) once to build lookup maps.
	all, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeCloudWatchAlarm, TypeCloudWatchCompositeAlarm},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	// Index by NativeID (ARN) and by Name for O(1) lookup.
	byARN := make(map[string]string, len(all))
	byName := make(map[string]string, len(all))
	for _, r := range all {
		byARN[r.NativeID] = r.ID
		if r.Name != nil {
			byName[*r.Name] = r.ID
		}
	}

	for _, r := range all {
		if r.Type != TypeCloudWatchCompositeAlarm {
			continue
		}
		var attrs struct {
			AlarmRule *string `json:"AlarmRule"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		rule := sv(attrs.AlarmRule)
		if rule == "" {
			continue
		}
		for _, match := range alarmRuleTokenRe.FindAllStringSubmatch(rule, -1) {
			token := match[1]
			if token == "" {
				continue
			}
			// Resolve token by ARN first, then by name.
			childID, ok := byARN[token]
			if !ok {
				childID, ok = byName[token]
			}
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(r.ID, childID, store.RelContains, "directed", nil); err != nil {
				return fmt.Errorf("upsert composite-alarm→child relationship: %w", err)
			}
		}
	}
	return nil
}
