package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

func init() {
	registerService(serviceEntry{
		name: "aws:sns",
		fn:   scanSNS,
		emits: []coverage.TypeDecl{
			{Service: "sns", DiscoType: TypeSNSTopic},
			{Service: "sns", DiscoType: TypeSNSSubscription},
			{Service: "sns", DiscoType: TypeSNSTopicPolicy},
		},
	})
}

// snsAPI is the narrow set of SNS operations scanSNSTopics calls. *sns.Client
// satisfies it; tests supply a hand-rolled stub. Methods keep the SDK's
// variadic option-fn shape so `NewListTopicsPaginator(client, ...)` still
// compiles.
type snsAPI interface {
	ListTopics(context.Context, *sns.ListTopicsInput, ...func(*sns.Options)) (*sns.ListTopicsOutput, error)
	GetTopicAttributes(context.Context, *sns.GetTopicAttributesInput, ...func(*sns.Options)) (*sns.GetTopicAttributesOutput, error)
	ListSubscriptions(context.Context, *sns.ListSubscriptionsInput, ...func(*sns.Options)) (*sns.ListSubscriptionsOutput, error)
}

// scanSNS discovers SNS topics in one region. ListTopics returns only ARNs;
// GetTopicAttributes runs concurrently to fetch the attributes map
// (KmsMasterKeyId, RedrivePolicy, etc.) the resolver needs.
func scanSNS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := sns.NewFromConfig(acct.cfg, func(o *sns.Options) { o.Region = region })
	t, i, ferr := scanSNSTopics(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	t, i, ferr = scanSNSExtended(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

// scanSNSTopics is the testable scan body: depends only on the narrow snsAPI
// interface so unit tests can inject a stub client without HTTP mocks.
// scanSNS wires the concrete *sns.Client.
func scanSNSTopics(ctx context.Context, client snsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := sns.NewListTopicsPaginator(client, &sns.ListTopicsInput{})
	return pageScanConcurrent(ctx, "sns:ListTopics", acct, region, st,
		p.HasMorePages,
		func(c context.Context) (*sns.ListTopicsOutput, error) { return p.NextPage(c) },
		func(o *sns.ListTopicsOutput) []string {
			out := make([]string, 0, len(o.Topics))
			for _, t := range o.Topics {
				out = append(out, sv(t.TopicArn))
			}
			return out
		},
		func(gctx context.Context, arn string) (*store.Resource, error) {
			attrsOut, err := client.GetTopicAttributes(gctx, &sns.GetTopicAttributesInput{TopicArn: &arn})
			if err != nil {
				if isAccessDenied(err) {
					return nil, nil
				}
				return nil, fmt.Errorf("sns:GetTopicAttributes %s: %w", arn, err)
			}
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSNSTopic,
				NativeID:       arn,
				Name:           &arn,
				Region:         &region,
				AttributesJSON: mustJSON(attrsOut.Attributes),
				DiscoveredBy:   scanID,
			}, nil
		}, 0)
}
