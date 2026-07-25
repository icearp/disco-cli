package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/apprunner"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAppRunnerService, Service: "apprunner", Upstream: "AWS::AppRunner::Service"})
	registerType(restype.Descriptor{Type: TypeAppRunnerVPCConnector, Service: "apprunner", Upstream: "AWS::AppRunner::VpcConnector"})
	registerType(restype.Descriptor{Type: TypeAppRunnerAutoScalingConfiguration, Service: "apprunner", Leaf: true})
	registerType(restype.Descriptor{Type: TypeAppRunnerObservabilityConfiguration, Service: "apprunner", Leaf: true})
	registerType(restype.Descriptor{Type: TypeAppRunnerVpcIngressConnection, Service: "apprunner"})
	registerType(restype.Descriptor{Type: TypeAppRunnerConnection, Service: "apprunner", Leaf: true})
	registerService(serviceEntry{
		name: "aws:apprunner",
		fn:   scanAppRunner,
	})
}

// apprunnerAPI is the narrow set of App Runner operations called by the
// scanAppRunner sub-phases.
type apprunnerAPI interface {
	ListServices(context.Context, *apprunner.ListServicesInput, ...func(*apprunner.Options)) (*apprunner.ListServicesOutput, error)
	DescribeService(context.Context, *apprunner.DescribeServiceInput, ...func(*apprunner.Options)) (*apprunner.DescribeServiceOutput, error)
	ListVpcConnectors(context.Context, *apprunner.ListVpcConnectorsInput, ...func(*apprunner.Options)) (*apprunner.ListVpcConnectorsOutput, error)
	ListAutoScalingConfigurations(context.Context, *apprunner.ListAutoScalingConfigurationsInput, ...func(*apprunner.Options)) (*apprunner.ListAutoScalingConfigurationsOutput, error)
	ListObservabilityConfigurations(context.Context, *apprunner.ListObservabilityConfigurationsInput, ...func(*apprunner.Options)) (*apprunner.ListObservabilityConfigurationsOutput, error)
	ListVpcIngressConnections(context.Context, *apprunner.ListVpcIngressConnectionsInput, ...func(*apprunner.Options)) (*apprunner.ListVpcIngressConnectionsOutput, error)
	ListConnections(context.Context, *apprunner.ListConnectionsInput, ...func(*apprunner.Options)) (*apprunner.ListConnectionsOutput, error)
}

// scanAppRunner discovers App Runner resources in one region across phases:
// ListServices (paginator, skeleton) → fan-out DescribeService for full body
// (NetworkConfiguration, SourceConfiguration, EncryptionConfiguration,
// InstanceConfiguration); ListVpcConnectors (full body — Subnets,
// SecurityGroups); auto-scaling configs; observability configs; VPC ingress
// connections; source-provider connections. Per-phase + per-item AccessDenied
// tolerated. Custom domains deferred (per-service sub-resource, limited graph
// value beyond the service rows).
func scanAppRunner(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := apprunner.NewFromConfig(acct.cfg, func(o *apprunner.Options) { o.Region = region })

	{
		t, i, ferr := scanAppRunnerServices(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanAppRunnerVPCConnectors(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanAppRunnerAutoScalingConfigs(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanAppRunnerObservabilityConfigs(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanAppRunnerVpcIngressConnections(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanAppRunnerConnections(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	return total, inserted, nil
}

// scanAppRunnerConnections lists source-code provider connections (GitHub /
// Bitbucket links, account-scoped per region). NativeID = ConnectionArn.
func scanAppRunnerConnections(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := apprunner.NewListConnectionsPaginator(client, &apprunner.ListConnectionsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "apprunner:ListConnections", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("apprunner:ListConnections: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(out.ConnectionSummaryList))
		for _, c := range out.ConnectionSummaryList {
			arn := sv(c.ConnectionArn)
			if arn == "" {
				continue
			}
			name := sv(c.ConnectionName)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAppRunnerConnection,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				CreatedAt:      tp(c.CreatedAt),
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert apprunner connections: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// scanAppRunnerAutoScalingConfigs lists auto-scaling configuration revisions
// (account-scoped per region). NativeID = AutoScalingConfigurationArn.
func scanAppRunnerAutoScalingConfigs(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := apprunner.NewListAutoScalingConfigurationsPaginator(client, &apprunner.ListAutoScalingConfigurationsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "apprunner:ListAutoScalingConfigurations", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("apprunner:ListAutoScalingConfigurations: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(out.AutoScalingConfigurationSummaryList))
		for _, c := range out.AutoScalingConfigurationSummaryList {
			arn := sv(c.AutoScalingConfigurationArn)
			if arn == "" {
				continue
			}
			name := sv(c.AutoScalingConfigurationName)
			batch = append(batch, &store.Resource{
				Provider:    "aws",
				AccountID:   acct.ID,
				AccountName: &acct.Name,
				Type:        TypeAppRunnerAutoScalingConfiguration,
				NativeID:    arn,
				Name:        &name,
				Region:      &region,
				CreatedAt:   tp(c.CreatedAt),
				// Account-scoped AWS-supplied default revision (one per account, named
				// "DefaultConfiguration"). Customer can promote any revision via
				// UpdateDefaultAutoScalingConfiguration — flag tracks current SDK truth.
				ManagedByProvider: c.IsDefault != nil && *c.IsDefault,
				AttributesJSON:    mustJSON(c),
				DiscoveredBy:      scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert apprunner autoscaling-configs: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// scanAppRunnerObservabilityConfigs lists observability config revisions
// (account-scoped per region). NativeID = ObservabilityConfigurationArn.
func scanAppRunnerObservabilityConfigs(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := apprunner.NewListObservabilityConfigurationsPaginator(client, &apprunner.ListObservabilityConfigurationsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "apprunner:ListObservabilityConfigurations", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("apprunner:ListObservabilityConfigurations: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(out.ObservabilityConfigurationSummaryList))
		for _, c := range out.ObservabilityConfigurationSummaryList {
			arn := sv(c.ObservabilityConfigurationArn)
			if arn == "" {
				continue
			}
			name := sv(c.ObservabilityConfigurationName)
			batch = append(batch, &store.Resource{
				Provider:    "aws",
				AccountID:   acct.ID,
				AccountName: &acct.Name,
				Type:        TypeAppRunnerObservabilityConfiguration,
				NativeID:    arn,
				Name:        &name,
				Region:      &region,
				// Account-scoped AWS-supplied default named "DefaultConfiguration"
				// (no IsDefault field on this summary type — name is the only signal).
				ManagedByProvider: name == "DefaultConfiguration",
				AttributesJSON:    mustJSON(c),
				DiscoveredBy:      scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert apprunner observability-configs: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// scanAppRunnerVpcIngressConnections lists VPC ingress connections
// (account-scoped per region). NativeID = VpcIngressConnectionArn.
// VpcIngressConnectionSummary carries the linked ServiceArn for resolver edge.
func scanAppRunnerVpcIngressConnections(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := apprunner.NewListVpcIngressConnectionsPaginator(client, &apprunner.ListVpcIngressConnectionsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "apprunner:ListVpcIngressConnections", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("apprunner:ListVpcIngressConnections: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(out.VpcIngressConnectionSummaryList))
		for _, c := range out.VpcIngressConnectionSummaryList {
			arn := sv(c.VpcIngressConnectionArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAppRunnerVpcIngressConnection,
				NativeID:       arn,
				Name:           &arn,
				Region:         &region,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert apprunner vpc-ingress-connections: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

func scanAppRunnerServices(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := apprunner.NewListServicesPaginator(client, &apprunner.ListServicesInput{})
	var arns []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "apprunner:ListServices", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("apprunner:ListServices: %w", perr)
		}
		for _, s := range out.ServiceSummaryList {
			if s.ServiceArn != nil {
				arns = append(arns, *s.ServiceArn)
			}
		}
	}
	if len(arns) == 0 {
		return 0, 0, nil
	}

	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, arn := range arns {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.DescribeService(gctx, &apprunner.DescribeServiceInput{ServiceArn: &arn})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("apprunner:DescribeService %s: %w", arn, derr)
			}
			if out.Service == nil {
				return nil
			}
			name := sv(out.Service.ServiceName)
			status := string(out.Service.Status)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAppRunnerService,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(out.Service),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert apprunner services: %w", uerr)
	}
	return len(batch), n, nil
}

func scanAppRunnerVPCConnectors(ctx context.Context, client apprunnerAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := apprunner.NewListVpcConnectorsPaginator(client, &apprunner.ListVpcConnectorsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "apprunner:ListVpcConnectors", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("apprunner:ListVpcConnectors: %w", perr)
		}
		for _, c := range out.VpcConnectors {
			arn := sv(c.VpcConnectorArn)
			if arn == "" {
				continue
			}
			name := sv(c.VpcConnectorName)
			status := string(c.Status)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAppRunnerVPCConnector,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert apprunner vpc-connectors: %w", uerr)
	}
	return len(batch), n, nil
}
