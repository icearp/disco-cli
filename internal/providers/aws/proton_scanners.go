package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/proton"
	protontypes "github.com/aws/aws-sdk-go-v2/service/proton/types"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeProtonComponent, Service: "proton"})
	registerType(restype.Descriptor{Type: TypeProtonDeployment, Service: "proton", Leaf: true})
	registerType(restype.Descriptor{Type: TypeProtonEnvironment, Service: "proton"})
	registerType(restype.Descriptor{Type: TypeProtonEnvironmentAccountConnection, Service: "proton", Leaf: true})
	registerType(restype.Descriptor{Type: TypeProtonEnvironmentTemplate, Service: "proton", Leaf: true})
	registerType(restype.Descriptor{Type: TypeProtonEnvironmentTemplateVersion, Service: "proton", Upstream: "AWS::proton::environment-template-version"})
	registerType(restype.Descriptor{Type: TypeProtonRepository, Service: "proton", Leaf: true})
	registerType(restype.Descriptor{Type: TypeProtonService, Service: "proton", Leaf: true})
	registerType(restype.Descriptor{Type: TypeProtonServiceInstance, Service: "proton", Upstream: "AWS::proton::service-instance"})
	registerType(restype.Descriptor{Type: TypeProtonServiceTemplate, Service: "proton", Leaf: true})
	registerType(restype.Descriptor{Type: TypeProtonServiceTemplateVersion, Service: "proton", Upstream: "AWS::proton::service-template-version"})
	registerService(serviceEntry{
		name: "aws:proton",
		fn:   scanProton,
	})
}

type protonAPI interface {
	ListEnvironmentAccountConnections(context.Context, *proton.ListEnvironmentAccountConnectionsInput, ...func(*proton.Options)) (*proton.ListEnvironmentAccountConnectionsOutput, error)
	ListEnvironmentTemplates(context.Context, *proton.ListEnvironmentTemplatesInput, ...func(*proton.Options)) (*proton.ListEnvironmentTemplatesOutput, error)
	ListServiceTemplates(context.Context, *proton.ListServiceTemplatesInput, ...func(*proton.Options)) (*proton.ListServiceTemplatesOutput, error)
	ListComponents(context.Context, *proton.ListComponentsInput, ...func(*proton.Options)) (*proton.ListComponentsOutput, error)
	ListDeployments(context.Context, *proton.ListDeploymentsInput, ...func(*proton.Options)) (*proton.ListDeploymentsOutput, error)
	ListEnvironments(context.Context, *proton.ListEnvironmentsInput, ...func(*proton.Options)) (*proton.ListEnvironmentsOutput, error)
	ListServices(context.Context, *proton.ListServicesInput, ...func(*proton.Options)) (*proton.ListServicesOutput, error)
	ListServiceInstances(context.Context, *proton.ListServiceInstancesInput, ...func(*proton.Options)) (*proton.ListServiceInstancesOutput, error)
	ListRepositories(context.Context, *proton.ListRepositoriesInput, ...func(*proton.Options)) (*proton.ListRepositoriesOutput, error)
	ListEnvironmentTemplateVersions(context.Context, *proton.ListEnvironmentTemplateVersionsInput, ...func(*proton.Options)) (*proton.ListEnvironmentTemplateVersionsOutput, error)
	ListServiceTemplateVersions(context.Context, *proton.ListServiceTemplateVersionsInput, ...func(*proton.Options)) (*proton.ListServiceTemplateVersionsOutput, error)
}

// scanProton discovers Proton account connections, templates, template
// versions, environments, services, service instances, components,
// deployments, and repositories. ARNs are native on every type; template
// versions need a TemplateName, so they fan out over names captured in the
// environment-/service-template phases.
func scanProton(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := proton.NewFromConfig(acct.cfg, func(o *proton.Options) { o.Region = region })

	add := func(t, i int, perr error) error {
		if perr != nil {
			return perr
		}
		total += t
		inserted += i
		return nil
	}

	if err = add(scanProtonAccountConnections(ctx, client, acct, region, st, scanID)); err != nil {
		return total, inserted, err
	}

	envTemplateNames, t, i, terr := scanProtonEnvTemplates(ctx, client, acct, region, st, scanID)
	if err = add(t, i, terr); err != nil {
		return total, inserted, err
	}
	svcTemplateNames, t, i, terr := scanProtonServiceTemplates(ctx, client, acct, region, st, scanID)
	if err = add(t, i, terr); err != nil {
		return total, inserted, err
	}

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanProtonEnvironments(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanProtonServices(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanProtonServiceInstances(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanProtonComponents(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanProtonDeployments(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanProtonRepositories(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanProtonEnvTemplateVersions(ctx, client, acct, region, st, scanID, envTemplateNames)
		},
		func() (int, int, error) {
			return scanProtonServiceTemplateVersions(ctx, client, acct, region, st, scanID, svcTemplateNames)
		},
	} {
		if err = add(phase()); err != nil {
			return total, inserted, err
		}
	}
	return total, inserted, nil
}

// scanProtonAccountConnections requires RequestedBy per call; iterate both
// enum values to capture connections from either side. Dedup by ARN — a
// connection normally appears under exactly one RequestedBy value, but this
// guards against AWS evolving the API.
func scanProtonAccountConnections(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	seen := map[string]struct{}{}
	var batch []*store.Resource
	for _, rb := range []protontypes.EnvironmentAccountConnectionRequesterAccountType{
		protontypes.EnvironmentAccountConnectionRequesterAccountTypeManagementAccount,
		protontypes.EnvironmentAccountConnectionRequesterAccountTypeEnvironmentAccount,
	} {
		pager := proton.NewListEnvironmentAccountConnectionsPaginator(client, &proton.ListEnvironmentAccountConnectionsInput{
			RequestedBy: rb,
		})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					return 0, 0, skipIfAccessDenied(st, "proton:ListEnvironmentAccountConnections", acct.ID, region, err)
				}
				return 0, 0, fmt.Errorf("proton:ListEnvironmentAccountConnections %s: %w", rb, err)
			}
			for _, c := range out.EnvironmentAccountConnections {
				arn := sv(c.Arn)
				if arn == "" {
					continue
				}
				if _, dup := seen[arn]; dup {
					continue
				}
				seen[arn] = struct{}{}
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
	}
	return upsertBatch(st, batch, "proton environment-account-connections")
}

func scanProtonEnvTemplates(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string) (names []string, total, inserted int, err error) {
	pager := proton.NewListEnvironmentTemplatesPaginator(client, &proton.ListEnvironmentTemplatesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return nil, 0, 0, skipIfAccessDenied(st, "proton:ListEnvironmentTemplates", acct.ID, region, perr)
			}
			return nil, 0, 0, fmt.Errorf("proton:ListEnvironmentTemplates: %w", perr)
		}
		for _, t := range out.Templates {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			if n := sv(t.Name); n != "" {
				names = append(names, n)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeProtonEnvironmentTemplate, NativeID: arn,
				Name: t.Name, Region: &region,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	total, inserted, err = upsertBatch(st, batch, "proton environment-templates")
	return names, total, inserted, err
}

func scanProtonServiceTemplates(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string) (names []string, total, inserted int, err error) {
	pager := proton.NewListServiceTemplatesPaginator(client, &proton.ListServiceTemplatesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return nil, 0, 0, skipIfAccessDenied(st, "proton:ListServiceTemplates", acct.ID, region, perr)
			}
			return nil, 0, 0, fmt.Errorf("proton:ListServiceTemplates: %w", perr)
		}
		for _, t := range out.Templates {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			if n := sv(t.Name); n != "" {
				names = append(names, n)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeProtonServiceTemplate, NativeID: arn,
				Name: t.Name, Region: &region,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	total, inserted, err = upsertBatch(st, batch, "proton service-templates")
	return names, total, inserted, err
}

func scanProtonEnvironments(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := proton.NewListEnvironmentsPaginator(client, &proton.ListEnvironmentsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "proton:ListEnvironments", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("proton:ListEnvironments: %w", err)
		}
		for _, e := range out.Environments {
			arn := sv(e.Arn)
			if arn == "" {
				continue
			}
			status := string(e.DeploymentStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeProtonEnvironment, NativeID: arn,
				Name: e.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "proton environments")
}

func scanProtonServices(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := proton.NewListServicesPaginator(client, &proton.ListServicesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "proton:ListServices", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("proton:ListServices: %w", err)
		}
		for _, s := range out.Services {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			status := string(s.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeProtonService, NativeID: arn,
				Name: s.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "proton services")
}

func scanProtonServiceInstances(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := proton.NewListServiceInstancesPaginator(client, &proton.ListServiceInstancesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "proton:ListServiceInstances", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("proton:ListServiceInstances: %w", err)
		}
		for _, si := range out.ServiceInstances {
			arn := sv(si.Arn)
			if arn == "" {
				continue
			}
			status := string(si.DeploymentStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeProtonServiceInstance, NativeID: arn,
				Name: si.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(si), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "proton service-instances")
}

func scanProtonComponents(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := proton.NewListComponentsPaginator(client, &proton.ListComponentsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "proton:ListComponents", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("proton:ListComponents: %w", err)
		}
		for _, c := range out.Components {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			status := string(c.DeploymentStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeProtonComponent, NativeID: arn,
				Name: c.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "proton components")
}

func scanProtonDeployments(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := proton.NewListDeploymentsPaginator(client, &proton.ListDeploymentsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "proton:ListDeployments", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("proton:ListDeployments: %w", err)
		}
		for _, d := range out.Deployments {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			label := sv(d.Id)
			status := string(d.DeploymentStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeProtonDeployment, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "proton deployments")
}

func scanProtonRepositories(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := proton.NewListRepositoriesPaginator(client, &proton.ListRepositoriesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "proton:ListRepositories", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("proton:ListRepositories: %w", err)
		}
		for _, r := range out.Repositories {
			arn := sv(r.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeProtonRepository, NativeID: arn,
				Name: r.Name, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "proton repositories")
}

// scanProtonEnvTemplateVersions fans out ListEnvironmentTemplateVersions over
// the environment-template names captured by scanProtonEnvTemplates — the op
// requires a TemplateName. Concurrency capped at fanoutMed.
func scanProtonEnvTemplateVersions(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string, templateNames []string) (int, int, error) {
	if len(templateNames) == 0 {
		return 0, 0, nil
	}
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range templateNames {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		tmpl := name
		g.Go(func() error {
			defer sem.Release(1)
			pager := proton.NewListEnvironmentTemplateVersionsPaginator(client, &proton.ListEnvironmentTemplateVersionsInput{
				TemplateName: &tmpl,
			})
			for pager.HasMorePages() {
				out, perr := pager.NextPage(gctx)
				if perr != nil {
					if isAccessDenied(perr) {
						_ = skipIfAccessDenied(st, "proton:ListEnvironmentTemplateVersions", acct.ID, region, perr)
						return nil
					}
					return fmt.Errorf("proton:ListEnvironmentTemplateVersions %s: %w", tmpl, perr)
				}
				for _, v := range out.TemplateVersions {
					arn := sv(v.Arn)
					if arn == "" {
						continue
					}
					label := sv(v.MajorVersion) + "." + sv(v.MinorVersion)
					status := string(v.Status)
					mu.Lock()
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeProtonEnvironmentTemplateVersion, NativeID: arn,
						Name: &label, Region: &region, Status: &status,
						AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
					})
					mu.Unlock()
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	return upsertBatch(st, batch, "proton environment-template-versions")
}

// scanProtonServiceTemplateVersions fans out ListServiceTemplateVersions over
// the service-template names captured by scanProtonServiceTemplates — the op
// requires a TemplateName. Concurrency capped at fanoutMed.
func scanProtonServiceTemplateVersions(ctx context.Context, client protonAPI, acct *account, region string, st *store.Store, scanID string, templateNames []string) (int, int, error) {
	if len(templateNames) == 0 {
		return 0, 0, nil
	}
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range templateNames {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		tmpl := name
		g.Go(func() error {
			defer sem.Release(1)
			pager := proton.NewListServiceTemplateVersionsPaginator(client, &proton.ListServiceTemplateVersionsInput{
				TemplateName: &tmpl,
			})
			for pager.HasMorePages() {
				out, perr := pager.NextPage(gctx)
				if perr != nil {
					if isAccessDenied(perr) {
						_ = skipIfAccessDenied(st, "proton:ListServiceTemplateVersions", acct.ID, region, perr)
						return nil
					}
					return fmt.Errorf("proton:ListServiceTemplateVersions %s: %w", tmpl, perr)
				}
				for _, v := range out.TemplateVersions {
					arn := sv(v.Arn)
					if arn == "" {
						continue
					}
					label := sv(v.MajorVersion) + "." + sv(v.MinorVersion)
					status := string(v.Status)
					mu.Lock()
					batch = append(batch, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeProtonServiceTemplateVersion, NativeID: arn,
						Name: &label, Region: &region, Status: &status,
						AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
					})
					mu.Unlock()
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	return upsertBatch(st, batch, "proton service-template-versions")
}
