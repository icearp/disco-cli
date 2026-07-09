package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/braket"
)

func init() {
	registerType(restype.Descriptor{Type: TypeBraketSpendingLimit, Service: "braket", Leaf: true})
	registerService(serviceEntry{
		name: "aws:braket",
		fn:   scanBraket,
	})
}

// braketAPI is the narrow surface scanBraket uses. SearchSpendingLimits is
// paginator-native and returns SpendingLimitSummary entries that already
// carry SpendingLimitArn — no Get fan-out needed.
type braketAPI interface {
	SearchSpendingLimits(context.Context, *braket.SearchSpendingLimitsInput, ...func(*braket.Options)) (*braket.SearchSpendingLimitsOutput, error)
}

func scanBraket(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := braket.NewFromConfig(acct.cfg, func(o *braket.Options) { o.Region = region })
	return scanBraketSpendingLimits(ctx, client, acct, region, st, scanID)
}

func scanBraketSpendingLimits(ctx context.Context, client braketAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := braket.NewSearchSpendingLimitsPaginator(client, &braket.SearchSpendingLimitsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "braket:SearchSpendingLimits", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("braket:SearchSpendingLimits: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(out.SpendingLimits))
		for _, sl := range out.SpendingLimits {
			arn := sv(sl.SpendingLimitArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBraketSpendingLimit,
				NativeID:       arn,
				Name:           &arn,
				Region:         &region,
				CreatedAt:      tp(sl.CreatedAt),
				AttributesJSON: mustJSON(sl),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, uerr := st.UpsertResources(batch)
			if uerr != nil {
				return total, inserted, fmt.Errorf("upsert braket spending limits: %w", uerr)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
