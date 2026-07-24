package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/iot"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTTopicRule, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTTopicRuleDestination, Service: "iot", Leaf: true})
}

type iotTopicAPI interface {
	ListTopicRules(context.Context, *iot.ListTopicRulesInput, ...func(*iot.Options)) (*iot.ListTopicRulesOutput, error)
	GetTopicRule(context.Context, *iot.GetTopicRuleInput, ...func(*iot.Options)) (*iot.GetTopicRuleOutput, error)
	ListTopicRuleDestinations(context.Context, *iot.ListTopicRuleDestinationsInput, ...func(*iot.Options)) (*iot.ListTopicRuleDestinationsOutput, error)
	GetTopicRuleDestination(context.Context, *iot.GetTopicRuleDestinationInput, ...func(*iot.Options)) (*iot.GetTopicRuleDestinationOutput, error)
}

func scanIoTTopic(ctx context.Context, client iotTopicAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	t1, i1, e1 := scanIoTTopicRules(ctx, client, acct, region, st, scanID)
	if e1 != nil {
		return 0, 0, e1
	}
	t2, i2, e2 := scanIoTTopicRuleDestinations(ctx, client, acct, region, st, scanID)
	if e2 != nil {
		return t1, i1, e2
	}
	return t1 + t2, i1 + i2, nil
}

func scanIoTTopicRules(ctx context.Context, client iotTopicAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListTopicRulesPaginator(client, &iot.ListTopicRulesInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListTopicRules", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListTopicRules: %w", perr)
		}
		for _, r := range out.Rules {
			if r.RuleName != nil {
				names = append(names, *r.RuleName)
			}
		}
	}
	return iotDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.GetTopicRule(gctx, &iot.GetTopicRuleInput{RuleName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:GetTopicRule %s: %w", name, derr)
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
	}, st, "iot topic rules")
}

func scanIoTTopicRuleDestinations(ctx context.Context, client iotTopicAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListTopicRuleDestinationsPaginator(client, &iot.ListTopicRuleDestinationsInput{})
	var arns []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListTopicRuleDestinations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListTopicRuleDestinations: %w", perr)
		}
		for _, d := range out.DestinationSummaries {
			if d.Arn != nil {
				arns = append(arns, *d.Arn)
			}
		}
	}
	return iotDescribeFanout(ctx, arns, fanoutMed, func(gctx context.Context, arn string) (*store.Resource, error) {
		out, derr := client.GetTopicRuleDestination(gctx, &iot.GetTopicRuleDestinationInput{Arn: &arn})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:GetTopicRuleDestination %s: %w", arn, derr)
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
	}, st, "iot topic rule destinations")
}
