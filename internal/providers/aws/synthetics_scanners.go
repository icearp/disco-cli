package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/synthetics"
)

func init() {
	registerService(serviceEntry{
		name: "aws:synthetics",
		fn:   scanSynthetics,
		emits: []coverage.TypeDecl{
			{Service: "synthetics", DiscoType: TypeSyntheticsCanary},
			{Service: "synthetics", DiscoType: TypeSyntheticsGroup},
		},
	})
}

type syntheticsAPI interface {
	DescribeCanaries(context.Context, *synthetics.DescribeCanariesInput, ...func(*synthetics.Options)) (*synthetics.DescribeCanariesOutput, error)
	ListGroups(context.Context, *synthetics.ListGroupsInput, ...func(*synthetics.Options)) (*synthetics.ListGroupsOutput, error)
}

// scanSynthetics discovers CloudWatch Synthetics canaries and groups.
// Canary has no native ARN; synth as arn:aws:synthetics:{r}:{a}:canary:{name}.
func scanSynthetics(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := synthetics.NewFromConfig(acct.cfg, func(o *synthetics.Options) { o.Region = region })

	t, i, ferr := scanSyntheticsCanaries(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanSyntheticsGroups(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanSyntheticsCanaries(ctx context.Context, client syntheticsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.DescribeCanaries(ctx, &synthetics.DescribeCanariesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "synthetics:DescribeCanaries", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("synthetics:DescribeCanaries: %w", err)
		}
		for _, c := range out.Canaries {
			name := sv(c.Name)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:synthetics:%s:%s:canary:%s", region, acct.ID, name)
			var status string
			if c.Status != nil {
				status = string(c.Status.State)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSyntheticsCanary, NativeID: arn,
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "synthetics canaries")
}

func scanSyntheticsGroups(ctx context.Context, client syntheticsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListGroups(ctx, &synthetics.ListGroupsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "synthetics:ListGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("synthetics:ListGroups: %w", err)
		}
		for _, g := range out.Groups {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSyntheticsGroup, NativeID: arn,
				Name: g.Name, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "synthetics groups")
}
