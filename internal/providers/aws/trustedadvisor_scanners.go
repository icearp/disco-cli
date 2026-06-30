package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/trustedadvisor"
)

func init() {
	registerService(serviceEntry{
		name:   "aws:trustedadvisor",
		global: true,
		fn:     scanTrustedAdvisor,
		emits: []coverage.TypeDecl{
			{Service: "trustedadvisor", DiscoType: TypeTrustedAdvisorChecks, Leaf: true},
		},
	})
}

type trustedAdvisorAPI interface {
	ListChecks(context.Context, *trustedadvisor.ListChecksInput, ...func(*trustedadvisor.Options)) (*trustedadvisor.ListChecksOutput, error)
}

// scanTrustedAdvisor enumerates the AWS-published Trusted Advisor check
// catalog. The catalog is AWS-owned (ManagedByProvider). ListChecks requires
// Business/Enterprise Support; accounts without it return AccessDenied, which
// is tolerated as a silent skip.
func scanTrustedAdvisor(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-east-1"
	client := trustedadvisor.NewFromConfig(acct.cfg, func(o *trustedadvisor.Options) { o.Region = region })
	return scanTrustedAdvisorChecks(ctx, client, acct, region, st, scanID)
}

func scanTrustedAdvisorChecks(ctx context.Context, client trustedAdvisorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := trustedadvisor.NewListChecksPaginator(client, &trustedadvisor.ListChecksInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "trustedadvisor:ListChecks", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("trustedadvisor:ListChecks: %w", perr)
		}
		for _, c := range out.CheckSummaries {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTrustedAdvisorChecks, NativeID: arn,
				Name: c.Name, Region: &region,
				ManagedByProvider: true,
				AttributesJSON:    mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "trustedadvisor checks")
}
