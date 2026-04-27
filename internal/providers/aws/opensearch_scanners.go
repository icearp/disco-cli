package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() { registerService(serviceEntry{name: "aws:opensearch", fn: scanOpenSearch}) }

// scanOpenSearch discovers OpenSearch (and legacy Elasticsearch — same SDK)
// domains in one region. ListDomainNames returns name-only entries; full
// edge-bearing body lives on DescribeDomain. Per-item AccessDenied
// tolerated. Outbound connections (cross-cluster), package associations,
// and reserved instances deferred — narrow value relative to the core
// domain edges.
func scanOpenSearch(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := opensearch.NewFromConfig(acct.cfg, func(o *opensearch.Options) { o.Region = region })

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
