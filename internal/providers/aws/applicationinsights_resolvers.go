package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveApplicationInsightsRelationships,
		EdgeDecl{TypeApplicationInsightsApplication, TypeSNSTopic, store.RelUses},
	)
	registerResolver(
		noopAppInsightsHierarchy,
		EdgeDecl{TypeApplicationInsightsApplication, TypeApplicationInsightsComponent, store.RelContains},
		EdgeDecl{TypeApplicationInsightsComponent, TypeApplicationInsightsWorkload, store.RelContains},
		EdgeDecl{TypeApplicationInsightsApplication, TypeApplicationInsightsLogPatternSet, store.RelContains},
		EdgeDecl{TypeApplicationInsightsLogPatternSet, TypeApplicationInsightsLogPattern, store.RelContains},
	)
	members := make([]EdgeDecl, len(appInsightsComponentMemberTypes))
	for i, t := range appInsightsComponentMemberTypes {
		members[i] = EdgeDecl{TypeApplicationInsightsComponent, t, store.RelUses}
	}
	registerResolver(resolveAppInsightsComponentMembers, members...)
}

// noopAppInsightsHierarchy only declares the parent -> child contains edges;
// scanApplicationInsights records them through upsertContained.
func noopAppInsightsHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// appInsightsComponentMemberTypes are the scanned types a component's
// ResourceList can name: the resource types the SDK's ResourceType field doc
// lists (EC2 instance, Auto Scaling group, Classic and Application ELB, SQS
// queue) plus RDS DB instances, DynamoDB tables and Lambda functions from the
// service's supported-component list. Each is stored under its own ARN, so a
// ResourceList ARN resolves by NativeID alone.
var appInsightsComponentMemberTypes = []string{
	TypeEC2Instance,
	TypeAutoScalingGroup,
	TypeELBClassicLoadBalancer,
	TypeELBv2LoadBalancer,
	TypeSQSQueue,
	TypeRDSDBInstance,
	TypeDynamoDBTable,
	TypeLambdaFunction,
}

// resolveAppInsightsComponentMembers links each component to the scanned
// resources in its ResourceList (from DescribeComponent). An ARN of an
// unscanned resource or of a type outside appInsightsComponentMemberTypes gets
// no edge.
func resolveAppInsightsComponentMembers(acct *account, st *store.Store) error {
	comps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeApplicationInsightsComponent},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(comps) == 0 {
		return nil
	}
	members, err := nativeIDToResourceID(st, acct.ID, appInsightsComponentMemberTypes...)
	if err != nil {
		return err
	}
	for _, c := range comps {
		var attrs struct {
			ResourceList []string `json:"ResourceList"`
		}
		if err := json.Unmarshal([]byte(c.AttributesJSON), &attrs); err != nil {
			return fmt.Errorf("decode applicationinsights component %s attributes: %w", c.NativeID, err)
		}
		for _, arn := range attrs.ResourceList {
			memberID, ok := members[arn]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(c.ID, memberID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert applicationinsights component→member: %w", err)
			}
		}
	}
	return nil
}

// resolveApplicationInsightsRelationships emits application → SNS topic edges
// for OpsItemSNSTopicArn and SNSNotificationArn (legacy field). Both reference
// SNS topics that disco may have scanned; FK-safe via scanned topic id set.
func resolveApplicationInsightsRelationships(acct *account, st *store.Store) error {
	apps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeApplicationInsightsApplication},
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
		topicID := store.ResourceID("aws", acct.ID, topicARN)
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
