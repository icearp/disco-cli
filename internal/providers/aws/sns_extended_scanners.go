package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/icearp/disco-cli/store"
)

// scanSNSExtended discovers SNS subscriptions and per-topic policies.
// AWS::SNS::TopicInlinePolicy is skip-logged: it's the same runtime Policy
// attribute as AWS::SNS::TopicPolicy, just with inline lifecycle semantics
// and no distinct API surface.
func scanSNSExtended(ctx context.Context, client snsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	t, i, ferr := scanSNSSubscriptions(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanSNSTopicPolicies(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanSNSSubscriptions(ctx context.Context, client snsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sns.NewListSubscriptionsPaginator(client, &sns.ListSubscriptionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "sns:ListSubscriptions", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("sns:ListSubscriptions: %w", err)
		}
		for _, s := range out.Subscriptions {
			arn := sv(s.SubscriptionArn)
			if arn == "" || arn == "PendingConfirmation" {
				continue
			}
			label := sv(s.Endpoint)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSNSSubscription, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "sns subscriptions")
}

// scanSNSTopicPolicies emits one row per topic with a non-empty Policy
// attribute. Source: ListTopics → GetTopicAttributes per topic. Synth ARN:
// {topicARN}/policy.
func scanSNSTopicPolicies(ctx context.Context, client snsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sns.NewListTopicsPaginator(client, &sns.ListTopicsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "sns:ListTopics(policies)", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("sns:ListTopics(policies): %w", err)
		}
		for _, t := range out.Topics {
			arn := sv(t.TopicArn)
			if arn == "" {
				continue
			}
			ta, derr := client.GetTopicAttributes(ctx, &sns.GetTopicAttributesInput{TopicArn: &arn})
			if derr != nil {
				if isAccessDenied(derr) {
					continue
				}
				return 0, 0, fmt.Errorf("sns:GetTopicAttributes(policy): %w", derr)
			}
			policy := ta.Attributes["Policy"]
			if policy == "" {
				continue
			}
			policyARN := arn + "/policy"
			label := "policy"
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSNSTopicPolicy, NativeID: policyARN,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(map[string]string{"TopicArn": arn, "Policy": policy}), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "sns topic-policies")
}
