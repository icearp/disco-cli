package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/proton"
)

func init() {
	registerService(serviceEntry{
		name: "aws:proton",
		fn:   scanProton,
		emits: []coverage.TypeDecl{
			{Service: "proton", DiscoType: TypeProtonEnvironmentAccountConnection},
			{Service: "proton", DiscoType: TypeProtonEnvironmentTemplate},
			{Service: "proton", DiscoType: TypeProtonServiceTemplate},
		},
	})
}

type protonAPI interface {
	ListEnvironmentAccountConnections(context.Context, *proton.ListEnvironmentAccountConnectionsInput, ...func(*proton.Options)) (*proton.ListEnvironmentAccountConnectionsOutput, error)
	ListEnvironmentTemplates(context.Context, *proton.ListEnvironmentTemplatesInput, ...func(*proton.Options)) (*proton.ListEnvironmentTemplatesOutput, error)
	ListServiceTemplates(context.Context, *proton.ListServiceTemplatesInput, ...func(*proton.Options)) (*proton.ListServiceTemplatesOutput, error)
}

// scanProton discovers Proton environment account connections, environment
// templates, and service templates. ARNs native on every type.
func scanProton(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := proton.NewFromConfig(acct.cfg, func(o *proton.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanProtonAccountConnections(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanProtonEnvTemplates(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanProtonServiceTemplates(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanProtonAccountConnections(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := proton.NewListEnvironmentAccountConnectionsPaginator(client, &proton.ListEnvironmentAccountConnectionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "proton:ListEnvironmentAccountConnections", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("proton:ListEnvironmentAccountConnections: %w", err)
		}
		for _, c := range out.EnvironmentAccountConnections {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.EnvironmentName)
			if label == "" {
				label = sv(c.Id)
			}
			status := string(c.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeProtonEnvironmentAccountConnection, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "proton environment-account-connections")
}

func scanProtonEnvTemplates(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := proton.NewListEnvironmentTemplatesPaginator(client, &proton.ListEnvironmentTemplatesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "proton:ListEnvironmentTemplates", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("proton:ListEnvironmentTemplates: %w", err)
		}
		for _, t := range out.Templates {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeProtonEnvironmentTemplate, NativeID: arn,
				Name: t.Name, Region: &region,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "proton environment-templates")
}

func scanProtonServiceTemplates(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := proton.NewListServiceTemplatesPaginator(client, &proton.ListServiceTemplatesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "proton:ListServiceTemplates", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("proton:ListServiceTemplates: %w", err)
		}
		for _, t := range out.Templates {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeProtonServiceTemplate, NativeID: arn,
				Name: t.Name, Region: &region,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "proton service-templates")
}
