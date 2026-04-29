package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/athena"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerService(serviceEntry{
		name: "aws:athena",
		fn:   scanAthena,
		emits: []coverage.TypeDecl{
			{Service: "athena", DiscoType: TypeAthenaWorkgroup},
			{Service: "athena", DiscoType: TypeAthenaDataCatalog},
		},
	})
}

// athenaAPI is the narrow set of Athena operations called by the scanAthena
// sub-phases.
type athenaAPI interface {
	ListWorkGroups(context.Context, *athena.ListWorkGroupsInput, ...func(*athena.Options)) (*athena.ListWorkGroupsOutput, error)
	GetWorkGroup(context.Context, *athena.GetWorkGroupInput, ...func(*athena.Options)) (*athena.GetWorkGroupOutput, error)
	ListDataCatalogs(context.Context, *athena.ListDataCatalogsInput, ...func(*athena.Options)) (*athena.ListDataCatalogsOutput, error)
	GetDataCatalog(context.Context, *athena.GetDataCatalogInput, ...func(*athena.Options)) (*athena.GetDataCatalogOutput, error)
}

// scanAthena discovers Athena workgroups and data catalogs in one region.
// Two phases run sequentially. Each phase: List (paginator, name-only) →
// fan-out Get for full body (errgroup + fanoutMed). Named queries +
// prepared statements deferred — saved-SQL artefacts, not graph nodes.
func scanAthena(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := athena.NewFromConfig(acct.cfg, func(o *athena.Options) { o.Region = region })

	{
		t, i, ferr := scanAthenaWorkGroups(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanAthenaDataCatalogs(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	return total, inserted, nil
}

func athenaWorkGroupARN(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:athena:%s:%s:workgroup/%s", region, accountID, name)
}

func athenaDataCatalogARN(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:athena:%s:%s:datacatalog/%s", region, accountID, name)
}

func scanAthenaWorkGroups(ctx context.Context, client athenaAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := athena.NewListWorkGroupsPaginator(client, &athena.ListWorkGroupsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "athena:ListWorkGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("athena:ListWorkGroups: %w", perr)
		}
		for _, w := range out.WorkGroups {
			if w.Name != nil {
				names = append(names, *w.Name)
			}
		}
	}
	if len(names) == 0 {
		return 0, 0, nil
	}

	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range names {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.GetWorkGroup(gctx, &athena.GetWorkGroupInput{WorkGroup: &name})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("athena:GetWorkGroup %s: %w", name, derr)
			}
			if out.WorkGroup == nil {
				return nil
			}
			arn := athenaWorkGroupARN(region, acct.ID, name)
			status := string(out.WorkGroup.State)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAthenaWorkgroup,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(out.WorkGroup),
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
		return 0, 0, fmt.Errorf("upsert athena workgroups: %w", uerr)
	}
	return len(batch), n, nil
}

func scanAthenaDataCatalogs(ctx context.Context, client athenaAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := athena.NewListDataCatalogsPaginator(client, &athena.ListDataCatalogsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "athena:ListDataCatalogs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("athena:ListDataCatalogs: %w", perr)
		}
		for _, d := range out.DataCatalogsSummary {
			if d.CatalogName != nil {
				names = append(names, *d.CatalogName)
			}
		}
	}
	if len(names) == 0 {
		return 0, 0, nil
	}

	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range names {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.GetDataCatalog(gctx, &athena.GetDataCatalogInput{Name: &name})
			if derr != nil {
				if isAccessDenied(derr) || isAPIErrorCode(derr, "InvalidRequestException") {
					// AwsDataCatalog (the implicit Glue Data Catalog) is
					// returned by ListDataCatalogs but rejected by
					// GetDataCatalog with InvalidRequestException — it has
					// no per-catalog config to fetch. Silent skip preserves
					// totals from sibling catalogs.
					return nil
				}
				return fmt.Errorf("athena:GetDataCatalog %s: %w", name, derr)
			}
			if out.DataCatalog == nil {
				return nil
			}
			arn := athenaDataCatalogARN(region, acct.ID, name)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAthenaDataCatalog,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(out.DataCatalog),
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
		return 0, 0, fmt.Errorf("upsert athena data catalogs: %w", uerr)
	}
	return len(batch), n, nil
}
