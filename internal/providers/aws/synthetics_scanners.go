package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/synthetics"
	syntheticstypes "github.com/aws/aws-sdk-go-v2/service/synthetics/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSyntheticsCanary, Service: "synthetics"})
	registerType(restype.Descriptor{Type: TypeSyntheticsGroup, Service: "synthetics"})
	registerService(serviceEntry{
		name: "aws:synthetics",
		fn:   scanSynthetics,
	})
}

type syntheticsAPI interface {
	DescribeCanaries(context.Context, *synthetics.DescribeCanariesInput, ...func(*synthetics.Options)) (*synthetics.DescribeCanariesOutput, error)
	ListGroups(context.Context, *synthetics.ListGroupsInput, ...func(*synthetics.Options)) (*synthetics.ListGroupsOutput, error)
}

const (
	synDescribeCanariesOp = "synthetics:DescribeCanaries"
	synListGroupsOp       = "synthetics:ListGroups"
	// synCanaryPageSize is DescribeCanaries' MaxResults cap (MaxCanaryResults).
	synCanaryPageSize int32 = 20
	// synGroupPageSize is ListGroups' MaxResults cap (MaxGroupResults).
	synGroupPageSize int32 = 20
)

// scanSynthetics discovers CloudWatch Synthetics canaries and groups. Both
// phases run whatever the other returned and their counts are summed; only a
// store write failure stops the scan.
func scanSynthetics(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := synthetics.NewFromConfig(acct.cfg, func(o *synthetics.Options) { o.Region = region })
	var phases phaseTotals
	t, i, err := scanSyntheticsCanaries(ctx, client, acct, region, st, scanID)
	if phases.addPhase(synDescribeCanariesOp, t, i, err) {
		return phases.result(st, acct, region)
	}
	t, i, err = scanSyntheticsGroups(ctx, client, acct, region, st, scanID)
	phases.addPhase(synListGroupsOp, t, i, err)
	return phases.result(st, acct, region)
}

// scanSyntheticsCanaries stores every canary DescribeCanaries lists, those
// read before a failed page included, each with the tags its element carries.
// Canary has no native ARN, so the NativeID is synthesized as
// arn:{p}:synthetics:{r}:{a}:canary:{name}; a canary listed without a name is
// not stored and is warned about under its id. A denial is warned about once;
// any other error is returned after the rows are stored.
func scanSyntheticsCanaries(ctx context.Context, client syntheticsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, synDescribeCanariesOp,
		func(ctx context.Context, token *string) ([]syntheticstypes.Canary, *string, error) {
			out, err := client.DescribeCanaries(ctx, &synthetics.DescribeCanariesInput{
				NextToken: token, MaxResults: sdkaws.Int32(synCanaryPageSize),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Canaries, out.NextToken, nil
		},
		func(c syntheticstypes.Canary) (*store.Resource, string, string) {
			name := sv(c.Name)
			if name == "" {
				return nil, "canaries have an empty Name", sv(c.Id)
			}
			var status *string
			if c.Status != nil {
				status = nonEmptyPtr(string(c.Status.State))
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSyntheticsCanary, NativeID: arnFor(acct.partition(), "synthetics", region, acct.ID, "canary:"+name),
				Name: &name, Region: &region, Status: status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
				TagsJSON: mapTagsJSON(c.Tags),
			}, "", ""
		})
}

// scanSyntheticsGroups stores every group ListGroups lists, those read before
// a failed page included. A group listed without an ARN is not stored and is
// warned about under its id. A denial is warned about once; any other error is
// returned after the rows are stored.
func scanSyntheticsGroups(ctx context.Context, client syntheticsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, synListGroupsOp,
		func(ctx context.Context, token *string) ([]syntheticstypes.GroupSummary, *string, error) {
			out, err := client.ListGroups(ctx, &synthetics.ListGroupsInput{
				NextToken: token, MaxResults: sdkaws.Int32(synGroupPageSize),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Groups, out.NextToken, nil
		},
		func(g syntheticstypes.GroupSummary) (*store.Resource, string, string) {
			arn := sv(g.Arn)
			if arn == "" {
				return nil, "groups have an empty Arn", sv(g.Id)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSyntheticsGroup, NativeID: arn,
				Name: g.Name, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			}, "", ""
		})
}
