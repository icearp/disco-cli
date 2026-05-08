package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/appintegrations"
)

func init() {
	registerService(serviceEntry{
		name: "aws:appintegrations",
		fn:   scanAppIntegrations,
		emits: []coverage.TypeDecl{
			{Service: "appintegrations", DiscoType: TypeAppIntegrationsApplication, Leaf: true},
			{Service: "appintegrations", DiscoType: TypeAppIntegrationsDataIntegration, Leaf: true},
			{Service: "appintegrations", DiscoType: TypeAppIntegrationsEventIntegration},
		},
	})
}

type appintegrationsAPI interface {
	ListApplications(context.Context, *appintegrations.ListApplicationsInput, ...func(*appintegrations.Options)) (*appintegrations.ListApplicationsOutput, error)
	ListDataIntegrations(context.Context, *appintegrations.ListDataIntegrationsInput, ...func(*appintegrations.Options)) (*appintegrations.ListDataIntegrationsOutput, error)
	ListEventIntegrations(context.Context, *appintegrations.ListEventIntegrationsInput, ...func(*appintegrations.Options)) (*appintegrations.ListEventIntegrationsOutput, error)
}

func scanAppIntegrations(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := appintegrations.NewFromConfig(acct.cfg, func(o *appintegrations.Options) { o.Region = region })
	a, ai, err := scanAppIntegrationsApplications(ctx, client, acct, region, st, scanID)
	if err != nil {
		return a, ai, err
	}
	d, di, err := scanAppIntegrationsDataIntegrations(ctx, client, acct, region, st, scanID)
	if err != nil {
		return a + d, ai + di, err
	}
	e, ei, err := scanAppIntegrationsEventIntegrations(ctx, client, acct, region, st, scanID)
	if err != nil {
		return a + d + e, ai + di + ei, err
	}
	return a + d + e, ai + di + ei, nil
}

func scanAppIntegrationsApplications(ctx context.Context, client appintegrationsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := appintegrations.NewListApplicationsPaginator(client, &appintegrations.ListApplicationsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "appintegrations:ListApplications", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("appintegrations:ListApplications: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.Applications))
		for _, a := range page.Applications {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			name := sv(a.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAppIntegrationsApplication,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				CreatedAt:      tp(a.CreatedTime),
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert appintegrations applications: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

func scanAppIntegrationsDataIntegrations(ctx context.Context, client appintegrationsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := appintegrations.NewListDataIntegrationsPaginator(client, &appintegrations.ListDataIntegrationsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "appintegrations:ListDataIntegrations", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("appintegrations:ListDataIntegrations: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.DataIntegrations))
		for _, d := range page.DataIntegrations {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			name := sv(d.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAppIntegrationsDataIntegration,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(d),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert appintegrations data-integrations: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

func scanAppIntegrationsEventIntegrations(ctx context.Context, client appintegrationsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := appintegrations.NewListEventIntegrationsPaginator(client, &appintegrations.ListEventIntegrationsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "appintegrations:ListEventIntegrations", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("appintegrations:ListEventIntegrations: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.EventIntegrations))
		for _, e := range page.EventIntegrations {
			arn := sv(e.EventIntegrationArn)
			if arn == "" {
				continue
			}
			name := sv(e.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAppIntegrationsEventIntegration,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(e),
				TagsJSON:       mapTagsJSON(e.Tags),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert appintegrations event-integrations: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
