package aws

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/athena"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAthenaWorkgroup, Service: "athena", Upstream: "AWS::Athena::WorkGroup"})
	registerType(restype.Descriptor{Type: TypeAthenaDataCatalog, Service: "athena", Upstream: "AWS::Athena::DataCatalog"})
	registerType(restype.Descriptor{Type: TypeAthenaCapacityReservation, Service: "athena", Leaf: true})
	registerType(restype.Descriptor{Type: TypeAthenaNamedQuery, Service: "athena"})
	registerType(restype.Descriptor{Type: TypeAthenaPreparedStatement, Service: "athena"})
	registerService(serviceEntry{
		name: "aws:athena",
		fn:   scanAthena,
	})
}

// athenaAPI is the narrow set of Athena operations called by the scanAthena
// sub-phases.
type athenaAPI interface {
	ListWorkGroups(context.Context, *athena.ListWorkGroupsInput, ...func(*athena.Options)) (*athena.ListWorkGroupsOutput, error)
	GetWorkGroup(context.Context, *athena.GetWorkGroupInput, ...func(*athena.Options)) (*athena.GetWorkGroupOutput, error)
	ListDataCatalogs(context.Context, *athena.ListDataCatalogsInput, ...func(*athena.Options)) (*athena.ListDataCatalogsOutput, error)
	GetDataCatalog(context.Context, *athena.GetDataCatalogInput, ...func(*athena.Options)) (*athena.GetDataCatalogOutput, error)
	ListCapacityReservations(context.Context, *athena.ListCapacityReservationsInput, ...func(*athena.Options)) (*athena.ListCapacityReservationsOutput, error)
	ListNamedQueries(context.Context, *athena.ListNamedQueriesInput, ...func(*athena.Options)) (*athena.ListNamedQueriesOutput, error)
	BatchGetNamedQuery(context.Context, *athena.BatchGetNamedQueryInput, ...func(*athena.Options)) (*athena.BatchGetNamedQueryOutput, error)
	ListPreparedStatements(context.Context, *athena.ListPreparedStatementsInput, ...func(*athena.Options)) (*athena.ListPreparedStatementsOutput, error)
	BatchGetPreparedStatement(context.Context, *athena.BatchGetPreparedStatementInput, ...func(*athena.Options)) (*athena.BatchGetPreparedStatementOutput, error)
}

func athenaCapacityReservationARN(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:athena:%s:%s:capacity-reservation/%s", region, accountID, name)
}

// scanAthena discovers Athena workgroups, data catalogs, capacity reservations,
// and each workgroup's saved (named) queries + prepared statements, per region.
// Workgroup/catalog phases List (paginator, name-only) → fan-out Get for the
// full body (errgroup + fanoutMed); the saved-query phases reuse the workgroup
// names to drive per-workgroup List + BatchGet.
func scanAthena(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := athena.NewFromConfig(acct.cfg, func(o *athena.Options) { o.Region = region })

	var wgNames []string
	{
		t, i, names, ferr := scanAthenaWorkGroups(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		wgNames = names
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

	{
		t, i, ferr := scanAthenaCapacityReservations(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	// Saved-SQL artefacts run last: a named-query / prepared-statement fetch
	// error must not blank the higher-value workgroup / catalog / capacity rows.
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanAthenaNamedQueries(ctx, client, acct, region, st, scanID, wgNames)
		},
		func() (int, int, error) {
			return scanAthenaPreparedStatements(ctx, client, acct, region, st, scanID, wgNames)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	return total, inserted, nil
}

// scanAthenaCapacityReservations enumerates Athena capacity reservations
// (account-scoped per region). NativeID synthesized via
// athenaCapacityReservationARN since ListCapacityReservations does not
// surface an ARN field.
func scanAthenaCapacityReservations(ctx context.Context, client athenaAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := athena.NewListCapacityReservationsPaginator(client, &athena.ListCapacityReservationsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "athena:ListCapacityReservations", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("athena:ListCapacityReservations: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(out.CapacityReservations))
		for _, c := range out.CapacityReservations {
			cname := sv(c.Name)
			if cname == "" {
				continue
			}
			arn := athenaCapacityReservationARN(region, acct.ID, cname)
			status := string(c.Status)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAthenaCapacityReservation,
				NativeID:       arn,
				Name:           &cname,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(c.CreationTime),
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert athena capacity-reservations: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

func athenaWorkGroupARN(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:athena:%s:%s:workgroup/%s", region, accountID, name)
}

func athenaDataCatalogARN(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:athena:%s:%s:datacatalog/%s", region, accountID, name)
}

func scanAthenaWorkGroups(ctx context.Context, client athenaAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, names []string, err error) {
	pager := athena.NewListWorkGroupsPaginator(client, &athena.ListWorkGroupsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "athena:ListWorkGroups", acct.ID, region, perr)
				return 0, 0, nil, nil
			}
			return 0, 0, nil, fmt.Errorf("athena:ListWorkGroups: %w", perr)
		}
		for _, w := range out.WorkGroups {
			if w.Name != nil {
				names = append(names, *w.Name)
			}
		}
	}
	if len(names) == 0 {
		return 0, 0, nil, nil
	}

	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range names {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, names, err
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
				Provider:    "aws",
				AccountID:   acct.ID,
				AccountName: &acct.Name,
				Type:        TypeAthenaWorkgroup,
				NativeID:    arn,
				Name:        &name,
				Region:      &region,
				Status:      &status,
				// AWS-supplied default workgroup, named "primary"; auto-created per region.
				ManagedByProvider: name == "primary",
				AttributesJSON:    mustJSON(out.WorkGroup),
				DiscoveredBy:      scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, names, werr
	}
	if len(batch) == 0 {
		return 0, 0, names, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, names, fmt.Errorf("upsert athena workgroups: %w", uerr)
	}
	return len(batch), n, names, nil
}

// scanAthenaNamedQueries lists each workgroup's saved (named) queries and
// batch-fetches their bodies. Named queries carry no AWS-issued ARN; synthesize
// {workgroupARN}/named-query/{id}.
func scanAthenaNamedQueries(ctx context.Context, client athenaAPI, acct *account, region string, st *store.Store, scanID string, workgroups []string) (total, inserted int, err error) {
	var batch []*store.Resource
	for _, wg := range workgroups {
		var ids []string
		pager := athena.NewListNamedQueriesPaginator(client, &athena.ListNamedQueriesInput{WorkGroup: &wg})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "athena:ListNamedQueries", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("athena:ListNamedQueries: %w", perr)
			}
			ids = append(ids, out.NamedQueryIds...)
		}
		for chunk := range slices.Chunk(ids, 50) {
			out, derr := client.BatchGetNamedQuery(ctx, &athena.BatchGetNamedQueryInput{NamedQueryIds: chunk})
			if derr != nil {
				if isAccessDenied(derr) {
					_ = skipIfAccessDenied(st, "athena:BatchGetNamedQuery", acct.ID, region, derr)
					break
				}
				return 0, 0, fmt.Errorf("athena:BatchGetNamedQuery: %w", derr)
			}
			for _, q := range out.NamedQueries {
				id := sv(q.NamedQueryId)
				if id == "" {
					continue
				}
				arn := athenaWorkGroupARN(region, acct.ID, wg) + "/named-query/" + id
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAthenaNamedQuery, NativeID: arn,
					Name: q.Name, Region: &region,
					AttributesJSON: mustJSON(q), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "athena named-queries")
}

// scanAthenaPreparedStatements lists each workgroup's prepared statements and
// batch-fetches their bodies. NativeID {workgroupARN}/prepared-statement/{name}.
func scanAthenaPreparedStatements(ctx context.Context, client athenaAPI, acct *account, region string, st *store.Store, scanID string, workgroups []string) (total, inserted int, err error) {
	var batch []*store.Resource
	for _, wg := range workgroups {
		var names []string
		pager := athena.NewListPreparedStatementsPaginator(client, &athena.ListPreparedStatementsInput{WorkGroup: &wg})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "athena:ListPreparedStatements", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("athena:ListPreparedStatements: %w", perr)
			}
			for _, s := range out.PreparedStatements {
				if s.StatementName != nil {
					names = append(names, *s.StatementName)
				}
			}
		}
		// BatchGetPreparedStatement accepts up to 256 names/call (vs 50 for
		// BatchGetNamedQuery).
		for chunk := range slices.Chunk(names, 256) {
			out, derr := client.BatchGetPreparedStatement(ctx, &athena.BatchGetPreparedStatementInput{
				PreparedStatementNames: chunk, WorkGroup: &wg,
			})
			if derr != nil {
				if isAccessDenied(derr) {
					_ = skipIfAccessDenied(st, "athena:BatchGetPreparedStatement", acct.ID, region, derr)
					break
				}
				return 0, 0, fmt.Errorf("athena:BatchGetPreparedStatement: %w", derr)
			}
			for _, s := range out.PreparedStatements {
				name := sv(s.StatementName)
				if name == "" {
					continue
				}
				arn := athenaWorkGroupARN(region, acct.ID, wg) + "/prepared-statement/" + name
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAthenaPreparedStatement, NativeID: arn,
					Name: s.StatementName, Region: &region,
					AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "athena prepared-statements")
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
					// GetDataCatalog with InvalidRequestException — no
					// per-catalog config to fetch. Skip preserves sibling
					// totals.
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
