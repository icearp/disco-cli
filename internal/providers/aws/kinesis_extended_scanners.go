package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/icearp/disco-cli/store"
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

// scanKinesisChannels stores every channel in the region, keyed by the ARN the
// summary returns. A channel delivers records from Kinesis data streams to S3
// or S3 Tables; its summary names the source streams but not the destination,
// which only DescribeChannel returns. ChannelStatus has no deleted state, so a
// DELETING channel is stored.
//
// ListChannels arrived in SDK v1.50.0, long after the stream API it shares an
// endpoint with, so a region may serve streams without it. On the first call,
// the shapes an unrouted operation takes are a feature gap, skipped silently;
// see isKinesisChannelsNotDeployed. Once a page has succeeded the op is
// deployed: a later error of any shape except a denial is returned, after the
// rows listed so far are stored.
func scanKinesisChannels(ctx context.Context, client kinesisAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "kinesis:ListChannels"
	// The model allows MaxResults up to 10000, but the op doc caps a page at 100.
	pager := kinesis.NewListChannelsPaginator(client, &kinesis.ListChannelsInput{},
		func(o *kinesis.ListChannelsPaginatorOptions) { o.Limit = 100 })
	var batch []*store.Resource
	var listErr error
	listed := false
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			switch {
			case !listed && isKinesisChannelsNotDeployed(perr):
				return 0, 0, nil
			case isAccessDenied(perr):
				_ = skipIfAccessDenied(st, op, acct.ID, region, perr)
			default:
				listErr = fmt.Errorf("%s: %w", op, perr)
			}
			break
		}
		listed = true
		for _, c := range out.ChannelSummaries {
			arn := sv(c.ChannelARN)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeKinesisChannel, NativeID: arn,
				Name: c.ChannelName, Status: nonEmptyPtr(string(c.ChannelStatus)), Region: &region,
				CreatedAt: tp(c.ChannelCreationTimestamp), AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	t, i, uerr := upsertBatch(st, batch, "kinesis channels")
	return t, i, errors.Join(listErr, uerr)
}

// isKinesisChannelsNotDeployed reports the shapes an unrouted ListChannels call
// takes on an endpoint that predates it: UnknownOperationException, the
// gateway's "Unable to determine service/operation name", or an HTTP 404.
// ListChannels does not model UnknownOperationException, but an awsJson
// endpoint answers an unknown X-Amz-Target with it and the SDK surfaces it as
// a generic API error carrying that code. The op models no not-found error,
// so a 404 on its unfiltered first call means the op is absent.
func isKinesisChannelsNotDeployed(err error) bool {
	return isAPIErrorCode(err, "UnknownOperationException") || isServiceNotAvailableInRegion(err) || isHTTP404(err)
}
