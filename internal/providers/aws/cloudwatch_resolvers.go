package aws

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveCloudWatchRelationships,
		EdgeDecl{TypeCloudWatchAlarm, TypeSNSTopic, store.RelUses},
		EdgeDecl{TypeCloudWatchCompositeAlarm, TypeSNSTopic, store.RelUses},
		EdgeDecl{TypeCloudWatchCompositeAlarm, TypeCloudWatchAlarm, store.RelContains},
		EdgeDecl{TypeCloudWatchCompositeAlarm, TypeCloudWatchCompositeAlarm, store.RelContains},
		EdgeDecl{TypeCloudWatchAlarm, TypeEC2Instance, store.RelUses},
		EdgeDecl{TypeCloudWatchAlarm, TypeRDSDBInstance, store.RelUses},
		EdgeDecl{TypeCloudWatchAlarm, TypeRDSDBCluster, store.RelUses},
		EdgeDecl{TypeCloudWatchAlarm, TypeLambdaFunction, store.RelUses},
		EdgeDecl{TypeCloudWatchAlarm, TypeSQSQueue, store.RelUses},
		EdgeDecl{TypeCloudWatchAlarm, TypeDynamoDBTable, store.RelUses},
		EdgeDecl{TypeCloudWatchAlarm, TypeELBv2LoadBalancer, store.RelUses},
		EdgeDecl{TypeCloudWatchAlarm, TypeEKSCluster, store.RelUses},
	)
	registerResolver(
		resolveCWMetricStreamRefs,
		EdgeDecl{TypeCloudWatchMetricStream, TypeFirehoseDeliveryStream, store.RelUses},
		EdgeDecl{TypeCloudWatchMetricStream, TypeIAMRole, store.RelUses},
	)
}

// resolveCWMetricStreamRefs wires metric-stream → Firehose delivery-stream
// (FirehoseArn) and IAM role (RoleArn).
func resolveCWMetricStreamRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCloudWatchMetricStream}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	fhSet, err := scannedIDSet(acct, st, TypeFirehoseDeliveryStream)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			FirehoseArn *string `json:"FirehoseArn"`
			RoleArn     *string `json:"RoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if f := sv(attrs.FirehoseArn); f != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeFirehoseDeliveryStream, f)
			if fhSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert cw ms→firehose: %w", err)
				}
			}
		}
		if role := sv(attrs.RoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert cw ms→role: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveCloudWatchRelationships runs all CloudWatch relationship passes.
func resolveCloudWatchRelationships(acct *account, st *store.Store) error {
	if err := resolveAlarmSNSActions(acct, st); err != nil {
		return err
	}
	if err := resolveCompositeAlarmChildren(acct, st); err != nil {
		return err
	}
	return resolveAlarmDimensions(acct, st)
}

// resolveAlarmSNSActions links both metric alarms and composite alarms to the
// SNS topics they notify via AlarmActions, OKActions, and
// InsufficientDataActions. Relationship: uses.
func resolveAlarmSNSActions(acct *account, st *store.Store) error {
	resources, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
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
// referenced in its AlarmRule expression, a boolean expression such as
// ALARM("arn:aws:cloudwatch:…") AND OK("other-alarm"). Tokens resolve via a
// pre-built NativeID/Name → store ID map. Relationship: contains.
func resolveCompositeAlarmChildren(acct *account, st *store.Store) error {
	// Load all known alarms (both types) once to build lookup maps.
	all, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
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

// alarmDimKey pairs CloudWatch metric Namespace + Dimension Name.
type alarmDimKey struct{ namespace, dim string }

// alarmDimTarget names the scanned resource type and how to build a lookup key
// from the dimension Value.
type alarmDimTarget struct {
	rtype string
	// mode: "name" = index (type,region,name); "ec2-instance" = rebuild EC2 ARN;
	// "elbv2-suffix" = match NativeID suffix "loadbalancer/<value>".
	mode string
}

// alarmDimMap covers CloudWatch namespaces whose scanners are already landed;
// extend only when the matched-type scanner exists.
var alarmDimMap = map[alarmDimKey]alarmDimTarget{
	{"AWS/EC2", "InstanceId"}:              {TypeEC2Instance, "ec2-instance"},
	{"AWS/RDS", "DBInstanceIdentifier"}:    {TypeRDSDBInstance, "name"},
	{"AWS/RDS", "DBClusterIdentifier"}:     {TypeRDSDBCluster, "name"},
	{"AWS/Lambda", "FunctionName"}:         {TypeLambdaFunction, "name"},
	{"AWS/SQS", "QueueName"}:               {TypeSQSQueue, "name"},
	{"AWS/SNS", "TopicName"}:               {TypeSNSTopic, "name"},
	{"AWS/DynamoDB", "TableName"}:          {TypeDynamoDBTable, "name"},
	{"AWS/ApplicationELB", "LoadBalancer"}: {TypeELBv2LoadBalancer, "elbv2-suffix"},
	{"AWS/NetworkELB", "LoadBalancer"}:     {TypeELBv2LoadBalancer, "elbv2-suffix"},
	{"AWS/EKS", "ClusterName"}:             {TypeEKSCluster, "name"},
}

// resolveAlarmDimensions links metric alarms to the resource identified by
// their Namespace + Dimensions. Covers both top-level (MetricAlarm) and
// metric-math (Metrics[].MetricStat.Metric) forms. Edge: uses.
func resolveAlarmDimensions(acct *account, st *store.Store) error {
	// Collect scanned types referenced by alarmDimMap, then build lookup indexes.
	nameIdx := map[string]string{}      // "<type>|<region>|<name>" → resourceID
	ec2InstIdx := map[string]string{}   // "<region>|<instance-id>" → resourceID
	elbSuffixIdx := map[string]string{} // e.g. "app/my-lb/abc" → resourceID

	wantTypes := map[string]struct{}{}
	for _, t := range alarmDimMap {
		wantTypes[t.rtype] = struct{}{}
	}
	types := make([]string, 0, len(wantTypes))
	for t := range wantTypes {
		types = append(types, t)
	}
	targets, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: types, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range targets {
		region := sv(r.Region)
		switch r.Type {
		case TypeEC2Instance:
			// NativeID tail is "instance/i-...".
			if idx := strings.LastIndex(r.NativeID, "instance/"); idx != -1 {
				ec2InstIdx[region+"|"+r.NativeID[idx+len("instance/"):]] = r.ID
			}
		case TypeELBv2LoadBalancer:
			// NativeID tail is "loadbalancer/<app|net>/<name>/<id>".
			if idx := strings.LastIndex(r.NativeID, "loadbalancer/"); idx != -1 {
				elbSuffixIdx[r.NativeID[idx+len("loadbalancer/"):]] = r.ID
			}
		default:
			if r.Name != nil && *r.Name != "" {
				nameIdx[r.Type+"|"+region+"|"+*r.Name] = r.ID
			}
		}
	}

	alarms, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCloudWatchAlarm},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, a := range alarms {
		type dim struct{ Name, Value *string }
		var attrs struct {
			Namespace  *string
			Dimensions []dim
			Metrics    []struct {
				MetricStat *struct {
					Metric *struct {
						Namespace  *string
						Dimensions []dim
					}
				}
			}
		}
		if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(a.Region)
		seen := map[string]bool{}
		emit := func(ns string, dims []dim) error {
			for _, d := range dims {
				dname := sv(d.Name)
				dval := sv(d.Value)
				if dname == "" || dval == "" {
					continue
				}
				tgt, ok := alarmDimMap[alarmDimKey{ns, dname}]
				if !ok {
					continue
				}
				var toID string
				switch tgt.mode {
				case "ec2-instance":
					toID = ec2InstIdx[region+"|"+dval]
				case "elbv2-suffix":
					toID = elbSuffixIdx[dval]
				default:
					toID = nameIdx[tgt.rtype+"|"+region+"|"+dval]
				}
				if toID == "" || seen[toID] {
					continue
				}
				seen[toID] = true
				if err := st.UpsertRelationship(a.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert alarm→dimension: %w", err)
				}
			}
			return nil
		}
		if err := emit(sv(attrs.Namespace), attrs.Dimensions); err != nil {
			return err
		}
		for _, m := range attrs.Metrics {
			if m.MetricStat == nil || m.MetricStat.Metric == nil {
				continue
			}
			if err := emit(sv(m.MetricStat.Metric.Namespace), m.MetricStat.Metric.Dimensions); err != nil {
				return err
			}
		}
	}
	return nil
}
