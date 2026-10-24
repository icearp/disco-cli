package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	iottypes "github.com/aws/aws-sdk-go-v2/service/iot/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTTopicRule, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTTopicRuleDestination, Service: "iot"})
}

type iotTopicAPI interface {
	iotTagsAPI
	ListTopicRules(context.Context, *iot.ListTopicRulesInput, ...func(*iot.Options)) (*iot.ListTopicRulesOutput, error)
	GetTopicRule(context.Context, *iot.GetTopicRuleInput, ...func(*iot.Options)) (*iot.GetTopicRuleOutput, error)
	ListTopicRuleDestinations(context.Context, *iot.ListTopicRuleDestinationsInput, ...func(*iot.Options)) (*iot.ListTopicRuleDestinationsOutput, error)
	GetTopicRuleDestination(context.Context, *iot.GetTopicRuleDestinationInput, ...func(*iot.Options)) (*iot.GetTopicRuleDestinationOutput, error)
}

// scanIoTTopic runs the topic-rule phases, adding each to p, and reports
// whether a write failed.
func scanIoTTopic(ctx context.Context, client iotTopicAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	return runIoTPhases(p,
		func(p *phaseTotals) bool { return scanIoTTopicRules(ctx, client, acct, region, st, scanID, p, tags) },
		func(p *phaseTotals) bool {
			return scanIoTTopicRuleDestinations(ctx, client, acct, region, st, scanID, p)
		},
	)
}

// The maxResults caps the model documents: ListTopicRules 1-10000,
// ListTopicRuleDestinations 1-1000.
const (
	iotTopicRulesPageCap            = 10000
	iotTopicRuleDestinationsPageCap = 1000
)

func scanIoTTopicRules(ctx context.Context, client iotTopicAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListTopicRules", "iot topic rules", "RuleName",
		func(ctx context.Context, token *string) ([]iottypes.TopicRuleListItem, *string, error) {
			out, err := client.ListTopicRules(ctx, &iot.ListTopicRulesInput{NextToken: token, MaxResults: sdkaws.Int32(iotTopicRulesPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Rules, out.NextToken, nil
		},
		func(x iottypes.TopicRuleListItem) *string { return x.RuleName },
		func(x iottypes.TopicRuleListItem) *store.Resource {
			return iotListedRow(acct, region, scanID, TypeIoTTopicRule, x.RuleArn, x.RuleName, x)
		})
	d := list.describe(ctx, st, acct, region, "iot:GetTopicRule", neverSkip,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, derr := client.GetTopicRule(ctx, &iot.GetTopicRuleInput{RuleName: &name})
			if derr != nil {
				return nil, derr
			}
			arn := sv(out.RuleArn)
			if arn == "" {
				return nil, nil
			}
			rname := name
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTTopicRule,
				NativeID:       arn,
				Name:           &rname,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStoreTagged(ctx, st, acct, region, p, list, d, tags.reader())
}

func scanIoTTopicRuleDestinations(ctx context.Context, client iotTopicAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListTopicRuleDestinations", "iot topic rule destinations", "Arn",
		func(ctx context.Context, token *string) ([]iottypes.TopicRuleDestinationSummary, *string, error) {
			out, err := client.ListTopicRuleDestinations(ctx, &iot.ListTopicRuleDestinationsInput{NextToken: token, MaxResults: sdkaws.Int32(iotTopicRuleDestinationsPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.DestinationSummaries, out.NextToken, nil
		},
		func(x iottypes.TopicRuleDestinationSummary) *string { return x.Arn },
		func(x iottypes.TopicRuleDestinationSummary) *store.Resource {
			return iotListedRow(acct, region, scanID, TypeIoTTopicRuleDestination, x.Arn, x.Arn, x)
		})
	d := list.describe(ctx, st, acct, region, "iot:GetTopicRuleDestination", neverSkip,
		func(ctx context.Context, arn string) (*store.Resource, error) {
			out, derr := client.GetTopicRuleDestination(ctx, &iot.GetTopicRuleDestinationInput{Arn: &arn})
			if derr != nil {
				return nil, derr
			}
			if out.TopicRuleDestination == nil {
				return nil, nil
			}
			outARN := sv(out.TopicRuleDestination.Arn)
			if outARN == "" {
				return nil, nil
			}
			name := outARN
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTTopicRuleDestination,
				NativeID:       outARN,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStore(st, acct, region, p, list, d)
}
