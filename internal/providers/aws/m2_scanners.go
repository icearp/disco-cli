package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/m2"
)

func init() {
	registerService(serviceEntry{
		name: "aws:m2",
		fn:   scanM2,
		emits: []coverage.TypeDecl{
			{Service: "m2", DiscoType: TypeM2Application, Leaf: true},
			{Service: "m2", DiscoType: TypeM2Environment, Leaf: true},
			{Service: "m2", DiscoType: TypeM2Deployment},
		},
	})
}

type m2API interface {
	ListApplications(context.Context, *m2.ListApplicationsInput, ...func(*m2.Options)) (*m2.ListApplicationsOutput, error)
	ListEnvironments(context.Context, *m2.ListEnvironmentsInput, ...func(*m2.Options)) (*m2.ListEnvironmentsOutput, error)
	ListDeployments(context.Context, *m2.ListDeploymentsInput, ...func(*m2.Options)) (*m2.ListDeploymentsOutput, error)
}

// scanM2 discovers M2 (Mainframe Modernization) applications, environments,
// and per-application deployments. Application/Environment ARNs native;
// deployments synthesize ARN as parent application ARN + path.
func scanM2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := m2.NewFromConfig(acct.cfg, func(o *m2.Options) { o.Region = region })

	apps, t, i, ferr := scanM2Applications(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanM2Environments(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, a := range apps {
		t, i, ferr = scanM2Deployments(ctx, client, acct, region, st, scanID, a.id, a.arn)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

type m2App struct{ id, arn string }

func scanM2Applications(ctx context.Context, client m2API, acct *account, region string, st *store.Store, scanID string) ([]m2App, int, int, error) {
	pager := m2.NewListApplicationsPaginator(client, &m2.ListApplicationsInput{})
	var batch []*store.Resource
	var apps []m2App
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "m2:ListApplications", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("m2:ListApplications: %w", err)
		}
		for _, a := range out.Applications {
			arn := sv(a.ApplicationArn)
			id := sv(a.ApplicationId)
			if arn == "" || id == "" {
				continue
			}
			apps = append(apps, m2App{id, arn})
			status := string(a.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeM2Application, NativeID: arn,
				Name: a.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "m2 applications")
	return apps, t, i, err
}

func scanM2Environments(ctx context.Context, client m2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := m2.NewListEnvironmentsPaginator(client, &m2.ListEnvironmentsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "m2:ListEnvironments", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("m2:ListEnvironments: %w", err)
		}
		for _, e := range out.Environments {
			arn := sv(e.EnvironmentArn)
			if arn == "" {
				continue
			}
			status := string(e.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeM2Environment, NativeID: arn,
				Name: e.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "m2 environments")
}

func scanM2Deployments(ctx context.Context, client m2API, acct *account, region string, st *store.Store, scanID string, appID, appARN string) (int, int, error) {
	aid := appID
	pager := m2.NewListDeploymentsPaginator(client, &m2.ListDeploymentsInput{ApplicationId: &aid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "m2:ListDeployments", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("m2:ListDeployments: %w", err)
		}
		for _, d := range out.Deployments {
			did := sv(d.DeploymentId)
			if did == "" {
				continue
			}
			arn := appARN + "/deployment/" + did
			label := did
			status := string(d.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeM2Deployment, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "m2 deployments")
}
