package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
)

// scanKinesisExtended discovers stream consumers (per stream) and
// resource-based policies (per stream + per consumer). ResourcePolicy synth
// ARN: {targetArn}/policy.
func scanKinesisExtended(ctx context.Context, client kinesisAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	streamARNs, lerr := loadKinesisStreamARNs(ctx, client, acct, region, st)
	if lerr != nil {
		return 0, 0, lerr
	}

	consumerARNs, t, i, ferr := scanKinesisStreamConsumers(ctx, client, streamARNs, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	policyTargets := append(append([]string{}, streamARNs...), consumerARNs...)
	t, i, ferr = scanKinesisResourcePolicies(ctx, client, policyTargets, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func loadKinesisStreamARNs(ctx context.Context, client kinesisAPI, acct *account, region string, st *store.Store) ([]string, error) {
	var arns []string
	pager := kinesis.NewListStreamsPaginator(client, &kinesis.ListStreamsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "kinesis:ListStreams(extended)", acct.ID, region, err)
				return nil, nil
			}
			return nil, fmt.Errorf("kinesis:ListStreams(extended): %w", err)
		}
		for _, s := range page.StreamSummaries {
			if a := sv(s.StreamARN); a != "" {
				arns = append(arns, a)
			}
		}
	}
	return arns, nil
}

func scanKinesisStreamConsumers(ctx context.Context, client kinesisAPI, streamARNs []string, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var batch []*store.Resource
	var consumerARNs []string
	for _, arn := range streamARNs {
		streamArn := arn
		var nextToken *string
		for {
			out, err := client.ListStreamConsumers(ctx, &kinesis.ListStreamConsumersInput{
				StreamARN: &streamArn,
				NextToken: nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "kinesis:ListStreamConsumers", acct.ID, region, err)
					break
				}
				if isAPIErrorCode(err, "ResourceNotFoundException") {
					break
				}
				return nil, 0, 0, fmt.Errorf("kinesis:ListStreamConsumers s=%s: %w", streamArn, err)
			}
			for _, c := range out.Consumers {
				cArn := sv(c.ConsumerARN)
				if cArn == "" {
					continue
				}
				consumerARNs = append(consumerARNs, cArn)
				status := string(c.ConsumerStatus)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeKinesisStreamConsumer, NativeID: cArn,
					Name: c.ConsumerName, Region: &region, Status: &status,
					AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
	}
	t, i, err := upsertBatch(st, batch, "kinesis stream-consumers")
	return consumerARNs, t, i, err
}

func scanKinesisResourcePolicies(ctx context.Context, client kinesisAPI, targets []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, t := range targets {
		target := t
		out, err := client.GetResourcePolicy(ctx, &kinesis.GetResourcePolicyInput{ResourceARN: &target})
		if err != nil {
			if isAccessDenied(err) || isAPIErrorCode(err, "ResourceNotFoundException", "ResourcePolicyNotFoundException") {
				continue
			}
			return 0, 0, fmt.Errorf("kinesis:GetResourcePolicy %s: %w", target, err)
		}
		if sv(out.Policy) == "" {
			continue
		}
		arn := target + "/policy"
		label := target
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeKinesisResourcePolicy, NativeID: arn,
			Name: &label, Region: &region,
			AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "kinesis resource-policies")
}
