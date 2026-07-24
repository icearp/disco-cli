package aws

import (
	"context"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/aiops"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAIOpsInvestigationGroup, Service: "aiops", Upstream: "AWS::AIOps::InvestigationGroup"})
	registerService(serviceEntry{
		name: "aws:aiops",
		fn:   scanAIOps,
	})
}

// aiopsAPI is the narrow surface scanAIOpsInvestigationGroups uses:
// ListInvestigationGroups returns only Arn + Name; GetInvestigationGroup
// supplies the full body the resolver needs (KMS key, IAM role, SNS
// channels, cross-account configs).
type aiopsAPI interface {
	ListInvestigationGroups(context.Context, *aiops.ListInvestigationGroupsInput, ...func(*aiops.Options)) (*aiops.ListInvestigationGroupsOutput, error)
	GetInvestigationGroup(context.Context, *aiops.GetInvestigationGroupInput, ...func(*aiops.Options)) (*aiops.GetInvestigationGroupOutput, error)
}

func scanAIOps(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := aiops.NewFromConfig(acct.cfg, func(o *aiops.Options) { o.Region = region })
	return scanAIOpsInvestigationGroups(ctx, client, acct, region, st, scanID)
}

func scanAIOpsInvestigationGroups(ctx context.Context, client aiopsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	p := aiops.NewListInvestigationGroupsPaginator(client, &aiops.ListInvestigationGroupsInput{})
	return pageScanConcurrent(ctx, "aiops:ListInvestigationGroups", acct, region, st,
		p.HasMorePages,
		func(c context.Context) (*aiops.ListInvestigationGroupsOutput, error) { return p.NextPage(c) },
		func(o *aiops.ListInvestigationGroupsOutput) []string {
			out := make([]string, 0, len(o.InvestigationGroups))
			for _, g := range o.InvestigationGroups {
				out = append(out, sv(g.Arn))
			}
			return out
		},
		func(gctx context.Context, arn string) (*store.Resource, error) {
			if arn == "" {
				return nil, nil
			}
			out, err := client.GetInvestigationGroup(gctx, &aiops.GetInvestigationGroupInput{Identifier: &arn})
			if err != nil {
				if isAccessDenied(err) {
					return nil, nil
				}
				return nil, err
			}
			name := sv(out.Name)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAIOpsInvestigationGroup,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		}, fanoutMed)
}
