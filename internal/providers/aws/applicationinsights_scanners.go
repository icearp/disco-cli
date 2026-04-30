package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/applicationinsights"
)

func init() {
	registerService(serviceEntry{
		name: "aws:applicationinsights",
		fn:   scanApplicationInsights,
		emits: []coverage.TypeDecl{
			{Service: "applicationinsights", DiscoType: TypeApplicationInsightsApplication},
		},
	})
}

type applicationInsightsAPI interface {
	ListApplications(context.Context, *applicationinsights.ListApplicationsInput, ...func(*applicationinsights.Options)) (*applicationinsights.ListApplicationsOutput, error)
}

// applicationInsightsAppNativeID synthesizes a NativeID for ApplicationInsights
// applications. The SDK exposes no application ARN — identity is the
// resource-group name. Shape mirrors the standard AWS service-resource form.
func applicationInsightsAppNativeID(region, acct, rgName string) string {
	return fmt.Sprintf("arn:aws:applicationinsights:%s:%s:application/resource-group/%s", region, acct, rgName)
}

func scanApplicationInsights(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := applicationinsights.NewFromConfig(acct.cfg, func(o *applicationinsights.Options) { o.Region = region })
	return scanApplicationInsightsApplications(ctx, client, acct, region, st, scanID)
}

func scanApplicationInsightsApplications(ctx context.Context, client applicationInsightsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := applicationinsights.NewListApplicationsPaginator(client, &applicationinsights.ListApplicationsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "applicationinsights:ListApplications", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("applicationinsights:ListApplications: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.ApplicationInfoList))
		for _, a := range page.ApplicationInfoList {
			rg := sv(a.ResourceGroupName)
			if rg == "" {
				continue
			}
			arn := applicationInsightsAppNativeID(region, acct.ID, rg)
			name := rg
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeApplicationInsightsApplication,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert applicationinsights apps: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
