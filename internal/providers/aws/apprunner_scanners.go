package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/apprunner"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerService(serviceEntry{
		name: "aws:apprunner",
		fn:   scanAppRunner,
		emits: []coverage.TypeDecl{
			{Service: "apprunner", DiscoType: TypeAppRunnerService},
			{Service: "apprunner", DiscoType: TypeAppRunnerVPCConnector},
		},
	})
}

// apprunnerAPI is the narrow set of App Runner operations called by the
// scanAppRunner sub-phases.
type apprunnerAPI interface {
	ListServices(context.Context, *apprunner.ListServicesInput, ...func(*apprunner.Options)) (*apprunner.ListServicesOutput, error)
	DescribeService(context.Context, *apprunner.DescribeServiceInput, ...func(*apprunner.Options)) (*apprunner.DescribeServiceOutput, error)
	ListVpcConnectors(context.Context, *apprunner.ListVpcConnectorsInput, ...func(*apprunner.Options)) (*apprunner.ListVpcConnectorsOutput, error)
}

// scanAppRunner discovers App Runner services and VPC connectors in one
// region. Two phases. Phase 1: ListServices (paginator, skeleton) →
// fan-out DescribeService for full body (NetworkConfiguration,
// SourceConfiguration, EncryptionConfiguration, InstanceConfiguration).
// Phase 2: ListVpcConnectors (paginator, full body — Subnets, SecurityGroups).
// Per-phase + per-item AccessDenied tolerated. Auto-scaling configurations,
// observability configurations, custom domains, and connections deferred —
// each is a separate sub-resource group with limited graph value beyond
// the service rows themselves.
func scanAppRunner(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := apprunner.NewFromConfig(acct.cfg, func(o *apprunner.Options) { o.Region = region })

	if t, i, ferr := scanAppRunnerServices(ctx, client, acct, region, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	if t, i, ferr := scanAppRunnerVPCConnectors(ctx, client, acct, region, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
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
