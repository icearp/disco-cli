package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerService(serviceEntry{
		name: "aws:opensearch",
		fn:   scanOpenSearch,
		emits: []coverage.TypeDecl{
			{Service: "opensearchservice", DiscoType: TypeOpenSearchDomain},
			{Service: "opensearchservice", DiscoType: TypeOpenSearchApplication, Leaf: true},
			{Service: "opensearchservice", DiscoType: TypeOpenSearchDataSource},
		},
	})
}

// opensearchAPI is the narrow set of OpenSearch operations called by
// scanOpenSearchDomains.
type opensearchAPI interface {
	ListDomainNames(context.Context, *opensearch.ListDomainNamesInput, ...func(*opensearch.Options)) (*opensearch.ListDomainNamesOutput, error)
	DescribeDomain(context.Context, *opensearch.DescribeDomainInput, ...func(*opensearch.Options)) (*opensearch.DescribeDomainOutput, error)
	ListApplications(context.Context, *opensearch.ListApplicationsInput, ...func(*opensearch.Options)) (*opensearch.ListApplicationsOutput, error)
	ListDataSources(context.Context, *opensearch.ListDataSourcesInput, ...func(*opensearch.Options)) (*opensearch.ListDataSourcesOutput, error)
}

// scanOpenSearch discovers OpenSearch (and legacy Elasticsearch — same SDK)
// domains in one region. ListDomainNames returns name-only entries; full
// edge-bearing body lives on DescribeDomain. Per-item AccessDenied
// tolerated. Outbound connections (cross-cluster), package associations,
// and reserved instances deferred — narrow value relative to the core
// domain edges.
func scanOpenSearch(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := opensearch.NewFromConfig(acct.cfg, func(o *opensearch.Options) { o.Region = region })
	t, i, ferr := scanOpenSearchDomains(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return t, i, ferr
	}
	t2, i2, ferr := scanOpenSearchApplications(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return t + t2, i + i2, ferr
	}
	t3, i3, ferr := scanOpenSearchDataSources(ctx, client, acct, region, st, scanID)
	return t + t2 + t3, i + i2 + i3, ferr
}

// scanOpenSearchDomains holds the testable scan body.
func scanOpenSearchDomains(ctx context.Context, client opensearchAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	listOut, lerr := client.ListDomainNames(ctx, &opensearch.ListDomainNamesInput{})
	if lerr != nil {
		if isAccessDenied(lerr) {
			_ = skipIfAccessDenied(st, "opensearch:ListDomainNames", acct.ID, region, lerr)
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("opensearch:ListDomainNames: %w", lerr)
	}
	if len(listOut.DomainNames) == 0 {
		return 0, 0, nil
	}

	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range listOut.DomainNames {
		name := sv(n.DomainName)
		if name == "" {
			continue
		}
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.DescribeDomain(gctx, &opensearch.DescribeDomainInput{DomainName: &name})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("opensearch:DescribeDomain %s: %w", name, derr)
			}
			if out.DomainStatus == nil {
				return nil
			}
			arn := sv(out.DomainStatus.ARN)
			if arn == "" {
				return nil
			}
			created := false
			if out.DomainStatus.Created != nil {
				created = *out.DomainStatus.Created
			}
			status := "creating"
			if created {
				status = "active"
			}
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeOpenSearchDomain,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(out.DomainStatus),
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
		return 0, 0, fmt.Errorf("upsert opensearch domains: %w", uerr)
	}
	return len(batch), n, nil
}
