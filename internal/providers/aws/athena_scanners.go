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
	ListTagsForResource(context.Context, *athena.ListTagsForResourceInput, ...func(*athena.Options)) (*athena.ListTagsForResourceOutput, error)
	ListNamedQueries(context.Context, *athena.ListNamedQueriesInput, ...func(*athena.Options)) (*athena.ListNamedQueriesOutput, error)
	BatchGetNamedQuery(context.Context, *athena.BatchGetNamedQueryInput, ...func(*athena.Options)) (*athena.BatchGetNamedQueryOutput, error)
	ListPreparedStatements(context.Context, *athena.ListPreparedStatementsInput, ...func(*athena.Options)) (*athena.ListPreparedStatementsOutput, error)
	BatchGetPreparedStatement(context.Context, *athena.BatchGetPreparedStatementInput, ...func(*athena.Options)) (*athena.BatchGetPreparedStatementOutput, error)
	ListNotebookMetadata(context.Context, *athena.ListNotebookMetadataInput, ...func(*athena.Options)) (*athena.ListNotebookMetadataOutput, error)
}

const (
	athenaListWorkGroupsOp           = "athena:ListWorkGroups"
	athenaGetWorkGroupOp             = "athena:GetWorkGroup"
	athenaListDataCatalogsOp         = "athena:ListDataCatalogs"
	athenaGetDataCatalogOp           = "athena:GetDataCatalog"
	athenaListCapacityReservationsOp = "athena:ListCapacityReservations"
	athenaTagsOp                     = "athena:ListTagsForResource"
	athenaListNamedQueriesOp         = "athena:ListNamedQueries"
	athenaBatchGetNamedQueryOp       = "athena:BatchGetNamedQuery"
	athenaListPreparedStatementsOp   = "athena:ListPreparedStatements"
	athenaBatchGetPreparedStmtOp     = "athena:BatchGetPreparedStatement"
)

// athenaListPageCap is the model's cap on MaxResults of ListWorkGroups,
// ListDataCatalogs and ListCapacityReservations (50 each).
const athenaListPageCap = 50

// athenaTagsPageSize is ListTagsForResource's MaxResults: the model sets a
// minimum of 75 and no maximum.
const athenaTagsPageSize = 75

// athenaGlueCatalog is the name ListDataCatalogs gives the account's AWS Glue
// Data Catalog.
const athenaGlueCatalog = "AwsDataCatalog"

func athenaCapacityReservationARN(partition, region, accountID, name string) string {
	return arnFor(partition, "athena", region, accountID, "capacity-reservation/"+name)
}

// scanAthena discovers Athena workgroups, data catalogs, capacity
// reservations, each workgroup's saved (named) queries and prepared
// statements, and the notebooks of each Apache Spark enabled workgroup, per
// region. Every listing runs as its own phase, one at a time: a phase's error
// does not stop the later ones; a failed write and ctx's end do; a region
// whose workgroup listing finds no endpoint runs no other phase
// (runHostPhases). The saved-query and notebook phases read the workgroups
// the workgroup phase stored. Workgroups, data catalogs and capacity
// reservations, the types Athena tags, have their tags read per row
// (athenaTags), and the tag denials of the whole scan are one warning.
func scanAthena(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := athena.NewFromConfig(acct.cfg, func(o *athena.Options) { o.Region = region })
	tags := athenaTags(client).sharingDenials()
	var workGroups athenaWorkGroupParents
	total, inserted, err := runHostPhases(ctx, st, acct, region, "athena", 0, []hostPhase{
		{op: athenaListWorkGroupsOp, run: func(p *phaseTotals) bool {
			return scanAthenaWorkGroups(ctx, client, acct, region, st, scanID, tags, &workGroups, p)
		}},
		{op: athenaListDataCatalogsOp, run: func(p *phaseTotals) bool {
			return scanAthenaDataCatalogs(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: athenaListCapacityReservationsOp, run: func(p *phaseTotals) bool {
			return scanAthenaCapacityReservations(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: athenaListNamedQueriesOp, run: func(p *phaseTotals) bool {
			return scanAthenaNamedQueries(ctx, client, acct, region, st, scanID, workGroups.names, p)
		}},
		{op: athenaListPreparedStatementsOp, run: func(p *phaseTotals) bool {
			return scanAthenaPreparedStatements(ctx, client, acct, region, st, scanID, workGroups.names, p)
		}},
		countedPhase(0, athenaNotebookOp, func() (int, int, error) {
			return scanAthenaNotebooks(ctx, client, acct, region, st, scanID, workGroups.spark)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// athenaTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN built from its listed name in the Service Reference's
// format for its type; the Service Reference lists workgroup, datacatalog
// and capacity-reservation under the op. Every page is read.
// ResourceNotFoundException, the op's only modeled not-found, drops a
// resource deleted after it was listed. The listings read here take no
// owner and return this account's own resources.
//
// A ManagedByProvider row (AwsDataCatalog) is not read: whether the op
// accepts its ARN is unverified, a refusal would fail every scan (every
// region lists it), and a not-found answer would drop the row. Such a row
// keeps the TagsJSON its builder set.
func athenaTags(client athenaAPI) rowTagReader {
	return rowTagReader{
		op:       athenaTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if r.ManagedByProvider {
				return r.TagsJSON, nil
			}
			arn := r.NativeID
			tags, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]athenatypes.Tag, *string, error) {
				out, err := client.ListTagsForResource(ctx, &athena.ListTagsForResourceInput{
					ResourceARN: &arn, MaxResults: sdkaws.Int32(athenaTagsPageSize), NextToken: token,
				})
				if err != nil {
					return nil, nil, err
				}
				return out.Tags, out.NextToken, nil
			})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(tags, func(t athenatypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// scanAthenaCapacityReservations stores the capacity reservations under the
// ARN built from each listed name (ListCapacityReservations returns none), a
// page at a time, the element verbatim as the attributes.
func scanAthenaCapacityReservations(ctx context.Context, client athenaAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, athenaListCapacityReservationsOp,
		func(ctx context.Context, token *string) ([]athenatypes.CapacityReservation, *string, error) {
			out, err := client.ListCapacityReservations(ctx, &athena.ListCapacityReservationsInput{
				MaxResults: sdkaws.Int32(athenaListPageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.CapacityReservations, out.NextToken, nil
		},
		func(c athenatypes.CapacityReservation) (*store.Resource, string, string) {
			name := sv(c.Name)
			if name == "" {
				return nil, "capacity reservations have no name", noIdentifier
			}
			status := string(c.Status)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAthenaCapacityReservation, NativeID: athenaCapacityReservationARN(acct.partition(), region, acct.ID, name),
				Name: &name, Region: &region, Status: &status, CreatedAt: tp(c.CreationTime),
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}, "", ""
		}, tags, p)
}

func athenaWorkGroupARN(partition, region, accountID, name string) string {
	return arnFor(partition, "athena", region, accountID, "workgroup/"+name)
}

func athenaDataCatalogARN(partition, region, accountID, name string) string {
	return arnFor(partition, "athena", region, accountID, "datacatalog/"+name)
}

// isAthenaSparkWorkGroup reports whether a workgroup whose engine version is
// v runs Apache Spark rather than Athena SQL. Notebooks exist only in Spark
// enabled workgroups, and the effective engine version is the workgroup field
// that tells the two apart: a Spark workgroup's names Spark ("PySpark engine
// version 3"), a SQL workgroup's does not ("Athena engine version 3"). Both
// WorkGroupSummary and the GetWorkGroup body carry it.
func isAthenaSparkWorkGroup(v *athenatypes.EngineVersion) bool {
	return v != nil && strings.Contains(sv(v.EffectiveEngineVersion), "Spark")
}

// athenaWorkGroupParents is what the workgroup phase hands the later phases:
// the names of the workgroups it stored, and the stored workgroups whose
// engine version names Spark.
type athenaWorkGroupParents struct {
	names []string
	spark []childParent
}

// scanAthenaWorkGroups stores the workgroups under the ARN built from each
// listed name, a page at a time, each row's attributes replaced with its
// GetWorkGroup body, and adds the stored ones to parents. GetWorkGroup
// models no not-found error, so any failed read keeps the WorkGroupSummary
// as the row, a denial warned about and any other error returned
// (rowDetailReader). A workgroup deleted after the listing fails that read
// with an error that is returned, and its row is then dropped when its tag
// read answers ResourceNotFoundException (athenaTags). Whether a workgroup
// runs Spark is read from its summary's engine version, or from its body's
// once the body is read.
func scanAthenaWorkGroups(ctx context.Context, client athenaAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, parents *athenaWorkGroupParents, p *phaseTotals) bool {
	var (
		mu    sync.Mutex
		spark = map[string]bool{}
	)
	steps := tokenPageSteps{
		details: rowDetailReader{
			op: athenaGetWorkGroupOp, what: "workgroup(s)",
			get: func(ctx context.Context, r *store.Resource) (any, error) {
				out, err := client.GetWorkGroup(ctx, &athena.GetWorkGroupInput{WorkGroup: r.Name})
				if err != nil {
					return nil, err
				}
				if out.WorkGroup == nil {
					return nil, nil
				}
				var engine *athenatypes.EngineVersion
				if out.WorkGroup.Configuration != nil {
					engine = out.WorkGroup.Configuration.EngineVersion
				}
				mu.Lock()
				spark[r.NativeID] = isAthenaSparkWorkGroup(engine)
				mu.Unlock()
				return out.WorkGroup, nil
			},
		},
		stored: func(rows []*store.Resource) {
			mu.Lock()
			defer mu.Unlock()
			for _, r := range rows {
				parents.names = append(parents.names, sv(r.Name))
				if spark[r.NativeID] {
					parents.spark = append(parents.spark, childParent{id: sv(r.Name), arn: r.NativeID})
				}
			}
		},
	}
	return storeTokenPagesTagsWith(ctx, st, acct, region, athenaListWorkGroupsOp,
		func(ctx context.Context, token *string) ([]athenatypes.WorkGroupSummary, *string, error) {
			out, err := client.ListWorkGroups(ctx, &athena.ListWorkGroupsInput{
				MaxResults: sdkaws.Int32(athenaListPageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.WorkGroups, out.NextToken, nil
		},
		func(w athenatypes.WorkGroupSummary) (*store.Resource, string, string) {
			name := sv(w.Name)
			if name == "" {
				return nil, "workgroups have no name", noIdentifier
			}
			arn := athenaWorkGroupARN(acct.partition(), region, acct.ID, name)
			mu.Lock()
			spark[arn] = isAthenaSparkWorkGroup(w.EngineVersion)
			mu.Unlock()
			status := string(w.State)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAthenaWorkgroup, NativeID: arn,
				Name: &name, Region: &region, Status: &status, CreatedAt: tp(w.CreationTime),
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			}, "", ""
		}, steps, tags, p)
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
// saying "... not found" (observed on GetDataCatalog; GetWorkGroup and
// GetDataCatalog model no not-found error).
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
	var disabledOnce sync.Once
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
	return childFanOutSkipTransient(ctx, st, acct, region, athenaNotebookOp, workGroups, skipWorkGroup, byParentID,
		func(ctx context.Context, wg childParent) ([]*store.Resource, error) {
			return listAthenaNotebooks(ctx, client, acct, region, scanID, wg)
		})
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
		if done, err := lastPage(in.NextToken, out.NextToken); done {
			return rows, err
		}
		in.NextToken = out.NextToken
	}
}

// scanAthenaNamedQueries lists each workgroup's saved (named) queries and
// batch-fetches their bodies, adding its counts and its error, under the op
// that failed, to p. Named queries carry no AWS-issued ARN; synthesize
// {workgroupARN}/named-query/{id}.
func scanAthenaNamedQueries(ctx context.Context, client athenaAPI, acct *account, region string, st *store.Store, scanID string, workgroups []string, p *phaseTotals) (storeWriteFailed bool) {
	var batch []*store.Resource
	for _, wg := range workgroups {
		var ids []string
		pager := athena.NewListNamedQueriesPaginator(client, &athena.ListNamedQueriesInput{WorkGroup: &wg})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, athenaListNamedQueriesOp, acct.ID, region, perr)
					break
				}
				p.errs.add(athenaListNamedQueriesOp, fmt.Errorf("%s: %w", athenaListNamedQueriesOp, perr))
				return false
			}
			ids = append(ids, out.NamedQueryIds...)
		}
		for chunk := range slices.Chunk(ids, 50) {
			out, derr := client.BatchGetNamedQuery(ctx, &athena.BatchGetNamedQueryInput{NamedQueryIds: chunk})
			if derr != nil {
				if isAccessDenied(derr) {
					_ = skipIfAccessDenied(st, athenaBatchGetNamedQueryOp, acct.ID, region, derr)
					break
				}
				p.errs.add(athenaBatchGetNamedQueryOp, fmt.Errorf("%s: %w", athenaBatchGetNamedQueryOp, derr))
				return false
			}
			for _, q := range out.NamedQueries {
				id := sv(q.NamedQueryId)
				if id == "" {
					continue
				}
				arn := athenaWorkGroupARN(acct.partition(), region, acct.ID, wg) + "/named-query/" + id
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAthenaNamedQuery, NativeID: arn,
					Name: q.Name, Region: &region,
					AttributesJSON: mustJSON(q), DiscoveredBy: scanID,
				})
			}
		}
	}
	total, inserted, err := upsertBatch(st, batch, "athena named-queries")
	return p.addPhase(athenaListNamedQueriesOp, total, inserted, err)
}

// scanAthenaPreparedStatements lists each workgroup's prepared statements and
// batch-fetches their bodies, adding its counts and its error, under the op
// that failed, to p. NativeID {workgroupARN}/prepared-statement/{name}.
func scanAthenaPreparedStatements(ctx context.Context, client athenaAPI, acct *account, region string, st *store.Store, scanID string, workgroups []string, p *phaseTotals) (storeWriteFailed bool) {
	var batch []*store.Resource
	for _, wg := range workgroups {
		var names []string
		pager := athena.NewListPreparedStatementsPaginator(client, &athena.ListPreparedStatementsInput{WorkGroup: &wg})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, athenaListPreparedStatementsOp, acct.ID, region, perr)
					break
				}
				p.errs.add(athenaListPreparedStatementsOp, fmt.Errorf("%s: %w", athenaListPreparedStatementsOp, perr))
				return false
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
					_ = skipIfAccessDenied(st, athenaBatchGetPreparedStmtOp, acct.ID, region, derr)
					break
				}
				p.errs.add(athenaBatchGetPreparedStmtOp, fmt.Errorf("%s: %w", athenaBatchGetPreparedStmtOp, derr))
				return false
			}
			for _, s := range out.PreparedStatements {
				name := sv(s.StatementName)
				if name == "" {
					continue
				}
				arn := athenaWorkGroupARN(acct.partition(), region, acct.ID, wg) + "/prepared-statement/" + name
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAthenaPreparedStatement, NativeID: arn,
					Name: s.StatementName, Region: &region,
					AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
				})
			}
		}
	}
	total, inserted, err := upsertBatch(st, batch, "athena prepared-statements")
	return p.addPhase(athenaListPreparedStatementsOp, total, inserted, err)
}

// scanAthenaDataCatalogs stores the data catalogs under the ARN built from
// each listed name, a page at a time, each row's attributes replaced with its
// GetDataCatalog body. GetDataCatalog models no not-found error, so a failed
// read keeps the DataCatalogSummary as the row, and a catalog deleted after
// the listing is dropped only when its tag read answers
// ResourceNotFoundException, as scanAthenaWorkGroups says.
//
// AwsDataCatalog, Athena's name for the account's AWS Glue Data Catalog, is
// listed without a CreateDataCatalog call and holds no Athena setting:
// GetDataCatalog rejects it with InvalidRequestException (aws/CLAUDE.md
// "List returns entry, Get rejects it"), which would fail every scan. Its
// row is its DataCatalogSummary, ManagedByProvider, and neither its body nor
// its tags are read (athenaTags).
func scanAthenaDataCatalogs(ctx context.Context, client athenaAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	steps := tokenPageSteps{details: rowDetailReader{
		op: athenaGetDataCatalogOp, what: "data catalog(s)",
		get: func(ctx context.Context, r *store.Resource) (any, error) {
			if r.ManagedByProvider {
				return nil, nil
			}
			out, err := client.GetDataCatalog(ctx, &athena.GetDataCatalogInput{Name: r.Name})
			if err != nil {
				return nil, err
			}
			if out.DataCatalog == nil {
				return nil, nil
			}
			return out.DataCatalog, nil
		},
	}}
	return storeTokenPagesTagsWith(ctx, st, acct, region, athenaListDataCatalogsOp,
		func(ctx context.Context, token *string) ([]athenatypes.DataCatalogSummary, *string, error) {
			out, err := client.ListDataCatalogs(ctx, &athena.ListDataCatalogsInput{
				MaxResults: sdkaws.Int32(athenaListPageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.DataCatalogsSummary, out.NextToken, nil
		},
		func(d athenatypes.DataCatalogSummary) (*store.Resource, string, string) {
			name := sv(d.CatalogName)
			if name == "" {
				return nil, "data catalogs have no name", noIdentifier
			}
			r := &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAthenaDataCatalog, NativeID: athenaDataCatalogARN(acct.partition(), region, acct.ID, name),
				Name: &name, Region: &region, ManagedByProvider: name == athenaGlueCatalog,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			}
			if s := string(d.Status); s != "" {
				r.Status = &s
			}
			return r, "", ""
		}, steps, tags, p)
}
