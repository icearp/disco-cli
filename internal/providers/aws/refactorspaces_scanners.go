package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/migrationhubrefactorspaces"
)

func init() {
	registerService(serviceEntry{
		name: "aws:refactor-spaces",
		fn:   scanRefactorSpaces,
		emits: []coverage.TypeDecl{
			{Service: "refactor-spaces", DiscoType: TypeRefactorSpacesEnvironment, Leaf: true},
			{Service: "refactor-spaces", DiscoType: TypeRefactorSpacesApplication},
			{Service: "refactor-spaces", DiscoType: TypeRefactorSpacesService},
			{Service: "refactor-spaces", DiscoType: TypeRefactorSpacesRoute},
		},
	})
}

type refactorSpacesAPI interface {
	ListEnvironments(context.Context, *migrationhubrefactorspaces.ListEnvironmentsInput, ...func(*migrationhubrefactorspaces.Options)) (*migrationhubrefactorspaces.ListEnvironmentsOutput, error)
	ListApplications(context.Context, *migrationhubrefactorspaces.ListApplicationsInput, ...func(*migrationhubrefactorspaces.Options)) (*migrationhubrefactorspaces.ListApplicationsOutput, error)
	ListServices(context.Context, *migrationhubrefactorspaces.ListServicesInput, ...func(*migrationhubrefactorspaces.Options)) (*migrationhubrefactorspaces.ListServicesOutput, error)
	ListRoutes(context.Context, *migrationhubrefactorspaces.ListRoutesInput, ...func(*migrationhubrefactorspaces.Options)) (*migrationhubrefactorspaces.ListRoutesOutput, error)
}

// scanRefactorSpaces discovers Migration Hub Refactor Spaces environments,
// per-environment applications, and per-(env, app) services and routes.
func scanRefactorSpaces(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := migrationhubrefactorspaces.NewFromConfig(acct.cfg, func(o *migrationhubrefactorspaces.Options) { o.Region = region })

	envIDs, t, i, ferr := scanRSEnvironments(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	type appRef struct{ env, app string }
	var apps []appRef
	for _, e := range envIDs {
		refs, tt, ii, aerr := scanRSApplications(ctx, client, acct, region, st, scanID, e)
		if aerr != nil {
			return total, inserted, aerr
		}
		total += tt
		inserted += ii
		for _, r := range refs {
			apps = append(apps, appRef{e, r})
		}
	}

	for _, a := range apps {
		t, i, ferr = scanRSServices(ctx, client, acct, region, st, scanID, a.env, a.app)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanRSRoutes(ctx, client, acct, region, st, scanID, a.env, a.app)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanRSEnvironments(ctx context.Context, client refactorSpacesAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := migrationhubrefactorspaces.NewListEnvironmentsPaginator(client, &migrationhubrefactorspaces.ListEnvironmentsInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "migrationhubrefactorspaces:ListEnvironments", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("migrationhubrefactorspaces:ListEnvironments: %w", err)
		}
		for _, e := range out.EnvironmentSummaryList {
			arn := sv(e.Arn)
			if arn == "" {
				continue
			}
			if id := sv(e.EnvironmentId); id != "" {
				ids = append(ids, id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRefactorSpacesEnvironment, NativeID: arn,
				Name: e.Name, Region: &region,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "refactor-spaces environments")
	return ids, t, i, err
}

func scanRSApplications(ctx context.Context, client refactorSpacesAPI, acct *account, region string, st *store.Store, scanID string, envID string) ([]string, int, int, error) {
	eid := envID
	pager := migrationhubrefactorspaces.NewListApplicationsPaginator(client, &migrationhubrefactorspaces.ListApplicationsInput{EnvironmentIdentifier: &eid})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "migrationhubrefactorspaces:ListApplications", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("migrationhubrefactorspaces:ListApplications: %w", err)
		}
		for _, a := range out.ApplicationSummaryList {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			if id := sv(a.ApplicationId); id != "" {
				ids = append(ids, id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRefactorSpacesApplication, NativeID: arn,
				Name: a.Name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "refactor-spaces applications")
	return ids, t, i, err
}

func scanRSServices(ctx context.Context, client refactorSpacesAPI, acct *account, region string, st *store.Store, scanID string, envID, appID string) (int, int, error) {
	eid, aid := envID, appID
	pager := migrationhubrefactorspaces.NewListServicesPaginator(client, &migrationhubrefactorspaces.ListServicesInput{EnvironmentIdentifier: &eid, ApplicationIdentifier: &aid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "migrationhubrefactorspaces:ListServices", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("migrationhubrefactorspaces:ListServices: %w", err)
		}
		for _, s := range out.ServiceSummaryList {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRefactorSpacesService, NativeID: arn,
				Name: s.Name, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "refactor-spaces services")
}

func scanRSRoutes(ctx context.Context, client refactorSpacesAPI, acct *account, region string, st *store.Store, scanID string, envID, appID string) (int, int, error) {
	eid, aid := envID, appID
	pager := migrationhubrefactorspaces.NewListRoutesPaginator(client, &migrationhubrefactorspaces.ListRoutesInput{EnvironmentIdentifier: &eid, ApplicationIdentifier: &aid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "migrationhubrefactorspaces:ListRoutes", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("migrationhubrefactorspaces:ListRoutes: %w", err)
		}
		for _, r := range out.RouteSummaryList {
			arn := sv(r.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRefactorSpacesRoute, NativeID: arn,
				Region:         &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "refactor-spaces routes")
}
