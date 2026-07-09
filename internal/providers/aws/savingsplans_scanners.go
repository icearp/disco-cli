package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/savingsplans"
)

// Savings Plans is a global service — the endpoint resolves only via us-east-1.
const savingsPlansRegion = "us-east-1"

func init() {
	registerType(restype.Descriptor{Type: TypeSavingsPlansSavingsPlan, Service: "savingsplans", Leaf: true})
	registerService(serviceEntry{
		name:   "aws:savingsplans",
		global: true,
		fn: func(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
			client := savingsplans.NewFromConfig(acct.cfg, func(o *savingsplans.Options) { o.Region = savingsPlansRegion })
			return scanSavingsPlans(ctx, client, acct, st, scanID)
		},
	})
}

// savingsPlansAPI is the narrow surface scanSavingsPlans uses. DescribeSavingsPlans
// has no paginator — drive NextToken manually.
type savingsPlansAPI interface {
	DescribeSavingsPlans(context.Context, *savingsplans.DescribeSavingsPlansInput, ...func(*savingsplans.Options)) (*savingsplans.DescribeSavingsPlansOutput, error)
}

func scanSavingsPlans(ctx context.Context, client savingsPlansAPI, acct *account, st *store.Store, scanID string) (total, inserted int, err error) {
	region := savingsPlansRegion
	var batch []*store.Resource
	token := ""
	for {
		in := &savingsplans.DescribeSavingsPlansInput{MaxResults: sdkaws.Int32(100)}
		if token != "" {
			in.NextToken = &token
		}
		out, derr := client.DescribeSavingsPlans(ctx, in)
		if derr != nil {
			if isAccessDenied(derr) {
				return total, inserted, skipIfAccessDenied(st, "savingsplans:DescribeSavingsPlans", acct.ID, region, derr)
			}
			return total, inserted, fmt.Errorf("savingsplans:DescribeSavingsPlans: %w", derr)
		}
		for _, p := range out.SavingsPlans {
			arn := sv(p.SavingsPlanArn)
			if arn == "" {
				continue
			}
			label := sv(p.SavingsPlanId)
			status := string(p.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSavingsPlansSavingsPlan, NativeID: arn,
				Name: &label, Region: regionGlobal, Status: &status,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = *out.NextToken
	}
	return upsertBatch(st, batch, "savings plans")
}
