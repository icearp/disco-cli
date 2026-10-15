package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/athena"
	athenatypes "github.com/aws/aws-sdk-go-v2/service/athena/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAthenaWorkgroup, Service: "athena"})
	registerType(restype.Descriptor{Type: TypeAthenaDataCatalog, Service: "athena"})
	registerType(restype.Descriptor{Type: TypeAthenaCapacityReservation, Service: "athena"})
	registerType(restype.Descriptor{Type: TypeAthenaNamedQuery, Service: "athena"})
	registerType(restype.Descriptor{Type: TypeAthenaPreparedStatement, Service: "athena"})
	registerType(restype.Descriptor{Type: TypeAthenaNotebook, Service: "athena"})
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
	ListNotebookMetadata(context.Context, *athena.ListNotebookMetadataInput, ...func(*athena.Options)) (*athena.ListNotebookMetadataOutput, error)
}

func athenaCapacityReservationARN(region, accountID, name string) string {
	return fmt.Sprintf("arn:aws:athena:%s:%s:capacity-reservation/%s", region, accountID, name)
}

// scanAthena discovers Athena workgroups, data catalogs, capacity reservations,
// each workgroup's saved (named) queries + prepared statements, and the
// notebooks of each Apache Spark enabled workgroup, per region.
// Workgroup/catalog phases List (paginator, name-only) → fan-out Get for the
// full body (errgroup + fanoutMed); the saved-query phases reuse the workgroup
// names to drive per-workgroup List + BatchGet.
//
// The older phases stop at their first hard error. The notebook phase runs
// whatever they returned, over the Spark workgroups stored before any failure,
// so its failure costs no other phase its rows and theirs does not skip it.
// The older phases' transient error is demoted to a warning, since one
// transient member would make the dispatcher read the whole join as transient
// (the notebook phase already warns on its own); the counts are summed.
func scanAthena(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := athena.NewFromConfig(acct.cfg, func(o *athena.Options) { o.Region = region })
	total, inserted, sparkWorkGroups, err := scanAthenaOlderPhases(ctx, client, acct, region, st, scanID)
	t, i, nerr := scanAthenaNotebooks(ctx, client, acct, region, st, scanID, sparkWorkGroups)
	return total + t, inserted + i, errors.Join(demoteTransient(st, "athena", acct, region, err), nerr)
}

// scanAthenaOlderPhases runs the phases that predate notebooks, stopping at
// the first hard error. It also returns the Spark enabled workgroups stored.
func scanAthenaOlderPhases(ctx context.Context, client athenaAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, sparkWorkGroups []childParent, err error) {
	var wgNames []string
	{
		t, i, names, spark, ferr := scanAthenaWorkGroups(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, nil, ferr
		}
		wgNames, sparkWorkGroups = names, spark
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanAthenaDataCatalogs(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, sparkWorkGroups, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanAthenaCapacityReservations(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, sparkWorkGroups, ferr
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
			return total, inserted, sparkWorkGroups, ferr
		}
		total += t
		inserted += i
	}

	return total, inserted, sparkWorkGroups, nil
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

// isAthenaSparkWorkGroup reports whether wg runs Apache Spark rather than
// Athena SQL. Notebooks exist only in Spark enabled workgroups, and the
// effective engine version is the workgroup field that tells the two apart: a
// Spark workgroup's names Spark ("PySpark engine version 3"), a SQL
// workgroup's does not ("Athena engine version 3").
func isAthenaSparkWorkGroup(wg *athenatypes.WorkGroup) bool {
	if wg.Configuration == nil || wg.Configuration.EngineVersion == nil {
		return false
	}
	return strings.Contains(sv(wg.Configuration.EngineVersion.EffectiveEngineVersion), "Spark")
}

// scanAthenaWorkGroups stores every workgroup and returns the names listed
// and the Spark enabled workgroups stored.
func scanAthenaWorkGroups(ctx context.Context, client athenaAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, names []string, spark []childParent, err error) {
	pager := athena.NewListWorkGroupsPaginator(client, &athena.ListWorkGroupsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "athena:ListWorkGroups", acct.ID, region, perr)
				return 0, 0, nil, nil, nil
			}
			return 0, 0, nil, nil, fmt.Errorf("athena:ListWorkGroups: %w", perr)
		}
		for _, w := range out.WorkGroups {
			if w.Name != nil {
				names = append(names, *w.Name)
			}
		}
	}
	if len(names) == 0 {
		return 0, 0, nil, nil, nil
	}

	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range names {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, names, nil, err
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
			if isAthenaSparkWorkGroup(out.WorkGroup) {
				spark = append(spark, childParent{id: name, arn: arn})
			}
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, names, nil, werr
	}
	if len(batch) == 0 {
		return 0, 0, names, nil, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, names, nil, fmt.Errorf("upsert athena workgroups: %w", uerr)
	}
	return len(batch), n, names, spark, nil
}

const athenaNotebookOp = "athena:ListNotebookMetadata"

// athenaNotebookPageLimit is the model's cap on ListNotebookMetadata
// MaxResults (1-50).
const athenaNotebookPageLimit = 50

var (
	// errAthenaWorkGroupGone marks a workgroup deleted after it was listed.
	errAthenaWorkGroupGone = errors.New("athena workgroup deleted since listing")
	// errAthenaWorkGroupDisabled marks a workgroup whose notebooks were not
	// listed because the workgroup is disabled.
	errAthenaWorkGroupDisabled = errors.New("athena notebooks not listed: the workgroup is disabled")
)

// isAthenaNotFound matches Athena's not-found answer, an InvalidRequestException
// saying "... not found" (observed on GetDataCatalog; Athena's read ops model
// no not-found error).
func isAthenaNotFound(err error) bool {
	return isAPIErrorWithMessage(err, "InvalidRequestException", "not found")
}

// explainAthenaNotebookRefusal asks GetWorkGroup why the first
// ListNotebookMetadata page of workGroup was refused with
// InvalidRequestException, the op's only modeled client error, which also
// covers a malformed request. It returns errAthenaWorkGroupGone when the
// workgroup no longer exists, errAthenaWorkGroupDisabled (wrapping refusal)
// when it is disabled, and otherwise refusal itself.
func explainAthenaNotebookRefusal(ctx context.Context, client athenaAPI, workGroup string, refusal error) error {
	out, err := client.GetWorkGroup(ctx, &athena.GetWorkGroupInput{WorkGroup: &workGroup})
	switch {
	case isAthenaNotFound(err):
		return errAthenaWorkGroupGone
	case err != nil:
		// %v, not %w: the refusal is the error being reported, and a transient
		// GetWorkGroup failure must not make it read as transient.
		return fmt.Errorf("%w (athena:GetWorkGroup could not explain it: %v)", refusal, err)
	case out.WorkGroup != nil && out.WorkGroup.State == athenatypes.WorkGroupStateDisabled:
		return fmt.Errorf("%w: %w", errAthenaWorkGroupDisabled, refusal)
	default:
		return refusal
	}
}

// scanAthenaNotebooks lists the notebooks of every Spark enabled workgroup and
// links each under its workgroup. A workgroup whose first page is refused is
// checked with GetWorkGroup: one deleted since listing is skipped silently,
// a disabled one is skipped with one warning per op (that response was never
// observed live), and any other refusal fails the phase. A workgroup whose
// call still fails transiently after the SDK's retries is skipped, keeping its
// siblings' rows, and the skips are reported in one warning.
func scanAthenaNotebooks(ctx context.Context, client athenaAPI, acct *account, region string, st *store.Store, scanID string, workGroups []childParent) (int, int, error) {
	var (
		transient    transientTally
		disabledOnce sync.Once
	)
	skipWorkGroup := func(err error) bool {
		switch {
		case errors.Is(err, errAthenaWorkGroupGone):
			return true
		case errors.Is(err, errAthenaWorkGroupDisabled):
			disabledOnce.Do(func() { _ = skipIfAccessDenied(st, athenaNotebookOp, acct.ID, region, err) })
			return true
		default:
			return false
		}
	}
	total, inserted, err := childFanOut(ctx, st, acct, region, athenaNotebookOp, workGroups, skipWorkGroup,
		func(ctx context.Context, wg childParent) ([]*store.Resource, error) {
			rows, err := listAthenaNotebooks(ctx, client, acct, region, scanID, wg)
			if isTransientNetworkError(err) {
				transient.skipped(wg.id, err)
				return nil, nil
			}
			return rows, err
		})
	transient.warnSkipped(st, athenaNotebookOp, acct, region)
	return total, inserted, err
}

// listAthenaNotebooks lists the notebook metadata of one workgroup; the
// notebook content (ExportNotebook) is not read. A notebook has no ARN, so its
// NativeID is {workgroupARN}/notebook/{NotebookId}.
func listAthenaNotebooks(ctx context.Context, client athenaAPI, acct *account, region, scanID string, wg childParent) ([]*store.Resource, error) {
	var rows []*store.Resource
	in := &athena.ListNotebookMetadataInput{WorkGroup: &wg.id, MaxResults: sdkaws.Int32(athenaNotebookPageLimit)}
	for {
		out, err := client.ListNotebookMetadata(ctx, in)
		if err != nil {
			// A later page is requested only after the first succeeded, so its
			// InvalidRequestException (e.g. a rejected NextToken) needs no
			// explaining.
			if in.NextToken == nil && isAPIErrorCode(err, "InvalidRequestException") {
				return nil, explainAthenaNotebookRefusal(ctx, client, wg.id, err)
			}
			return nil, err
		}
		for _, n := range out.NotebookMetadataList {
			id := sv(n.NotebookId)
			if id == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAthenaNotebook, NativeID: wg.arn + "/notebook/" + id,
				Name: n.Name, Region: &region, CreatedAt: tp(n.CreationTime),
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
		// ListNotebookMetadata has no paginator; a repeated token would loop
		// forever.
		if next := sv(out.NextToken); next == "" || next == sv(in.NextToken) {
			break
		}
		in.NextToken = out.NextToken
	}
	return rows, nil
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
