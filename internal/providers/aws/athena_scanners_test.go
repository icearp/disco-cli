package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/athena"
	athenatypes "github.com/aws/aws-sdk-go-v2/service/athena/types"
	"github.com/icearp/disco-cli/store"
)

const (
	athenaTestSQLWG    = "primary"
	athenaTestSparkWG1 = "spark-a"
	athenaTestSparkWG2 = "spark-b"
	athenaTestNamedQID = "11111111-1111-1111-1111-111111111111"
	athenaTestNB1ID    = "22222222-2222-2222-2222-222222222221"
	athenaTestNB2ID    = "22222222-2222-2222-2222-222222222222"
	athenaTestNB3ID    = "22222222-2222-2222-2222-222222222223"

	athenaTestSparkWGARN1 = "arn:aws:athena:us-east-1:123456789012:workgroup/spark-a"
	athenaTestSparkWGARN2 = "arn:aws:athena:us-east-1:123456789012:workgroup/spark-b"
	athenaTestNB1         = athenaTestSparkWGARN1 + "/notebook/" + athenaTestNB1ID
	athenaTestNB2         = athenaTestSparkWGARN1 + "/notebook/" + athenaTestNB2ID
	athenaTestNB3         = athenaTestSparkWGARN2 + "/notebook/" + athenaTestNB3ID
	athenaTestNamedQuery  = "arn:aws:athena:us-east-1:123456789012:workgroup/primary/named-query/" + athenaTestNamedQID
)

var (
	athenaDenied = apiErr("AccessDeniedException", "User: arn:aws:sts::123456789012:assumed-role/disco/scan is not "+
		"authorized to perform: athena:ListNotebookMetadata")
	athenaRefused  = apiErr("InvalidRequestException", "notebooks cannot be listed")
	athenaBadInput = apiErr("ValidationException", "bad input")
)

// athenaEngine is a workgroup whose effective engine version is engine.
func athenaEngine(name, engine string) *athenatypes.WorkGroup {
	return &athenatypes.WorkGroup{
		Name:          ptrStr(name),
		State:         athenatypes.WorkGroupStateEnabled,
		Configuration: &athenatypes.WorkGroupConfiguration{EngineVersion: &athenatypes.EngineVersion{EffectiveEngineVersion: ptrStr(engine)}},
	}
}

// athenaTopLevelHandlers serves one SQL workgroup holding one named query and
// two Spark workgroups: spark-a lists its notebooks over two pages (nb1; then
// nb2 plus one without an id), spark-b one notebook. Every ListNotebookMetadata
// call records its workgroup in nbCalls.
func athenaTopLevelHandlers(nbCalls *parentCalls) map[string]func(in any) (any, error) {
	created := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	engines := map[string]string{
		athenaTestSQLWG:    "Athena engine version 3",
		athenaTestSparkWG1: "PySpark engine version 3",
		athenaTestSparkWG2: "PySpark engine version 3",
	}
	return map[string]func(in any) (any, error){
		"ListTagsForResource": func(any) (any, error) { return &athena.ListTagsForResourceOutput{}, nil },
		"ListWorkGroups": func(any) (any, error) {
			return &athena.ListWorkGroupsOutput{WorkGroups: []athenatypes.WorkGroupSummary{
				{Name: ptrStr(athenaTestSQLWG)}, {Name: ptrStr(athenaTestSparkWG1)}, {Name: ptrStr(athenaTestSparkWG2)},
			}}, nil
		},
		"GetWorkGroup": func(in any) (any, error) {
			name := sv(in.(*athena.GetWorkGroupInput).WorkGroup)
			return &athena.GetWorkGroupOutput{WorkGroup: athenaEngine(name, engines[name])}, nil
		},
		"ListDataCatalogs":         func(any) (any, error) { return &athena.ListDataCatalogsOutput{}, nil },
		"ListCapacityReservations": func(any) (any, error) { return &athena.ListCapacityReservationsOutput{}, nil },
		"ListNamedQueries": func(in any) (any, error) {
			if sv(in.(*athena.ListNamedQueriesInput).WorkGroup) != athenaTestSQLWG {
				return &athena.ListNamedQueriesOutput{}, nil
			}
			return &athena.ListNamedQueriesOutput{NamedQueryIds: []string{athenaTestNamedQID}}, nil
		},
		"BatchGetNamedQuery": func(any) (any, error) {
			return &athena.BatchGetNamedQueryOutput{NamedQueries: []athenatypes.NamedQuery{{
				NamedQueryId: ptrStr(athenaTestNamedQID), Name: ptrStr("daily"), WorkGroup: ptrStr(athenaTestSQLWG),
			}}}, nil
		},
		"ListPreparedStatements": func(any) (any, error) { return &athena.ListPreparedStatementsOutput{}, nil },
		"ListNotebookMetadata": func(in any) (any, error) {
			p := in.(*athena.ListNotebookMetadataInput)
			wg := sv(p.WorkGroup)
			nbCalls.add(wg)
			if got := p.MaxResults; got == nil || *got != 50 {
				return nil, fmt.Errorf("MaxResults = %v, want 50", got)
			}
			switch {
			case wg == athenaTestSparkWG2:
				return &athena.ListNotebookMetadataOutput{NotebookMetadataList: []athenatypes.NotebookMetadata{
					{NotebookId: ptrStr(athenaTestNB3ID), Name: ptrStr("nb3"), WorkGroup: ptrStr(wg), Type: athenatypes.NotebookTypeIpynb},
				}}, nil
			case p.NextToken == nil:
				return &athena.ListNotebookMetadataOutput{NextToken: ptrStr("p2"), NotebookMetadataList: []athenatypes.NotebookMetadata{{
					NotebookId: ptrStr(athenaTestNB1ID), Name: ptrStr("explore"), WorkGroup: ptrStr(wg),
					Type: athenatypes.NotebookTypeIpynb, CreationTime: &created, LastModifiedTime: &created,
				}}}, nil
			default:
				return &athena.ListNotebookMetadataOutput{NotebookMetadataList: []athenatypes.NotebookMetadata{
					{NotebookId: ptrStr(athenaTestNB2ID), Name: ptrStr("nb2"), WorkGroup: ptrStr(wg), Type: athenatypes.NotebookTypeIpynb},
					{Name: ptrStr("no-id")},
				}}, nil
			}
		},
	}
}

func scanAthenaTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, athenaDenied)
	total, err := scanAthena(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanAthena_StoresNotebooksOfSparkWorkGroups(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var nbCalls parentCalls
	total, err := scanAthenaTopLevel(t, st, athenaTopLevelHandlers(&nbCalls))
	if err != nil {
		t.Fatalf("scanAthena: %v", err)
	}
	// 3 workgroups + 1 named query + 3 notebooks
	if total != 7 {
		t.Errorf("total = %d, want 7", total)
	}
	if got := nbCalls.distinct(); !slices.Equal(got, []string{athenaTestSparkWG1, athenaTestSparkWG2}) {
		t.Errorf("notebooks listed for %v, want only the Spark workgroups [%s %s]", got, athenaTestSparkWG1, athenaTestSparkWG2)
	}

	nbs := rowsByNativeID(t, st, TypeAthenaNotebook)
	if got := nativeIDs(nbs); !slices.Equal(got, []string{athenaTestNB1, athenaTestNB2, athenaTestNB3}) {
		t.Fatalf("notebook rows = %v, want [%s %s %s]", got, athenaTestNB1, athenaTestNB2, athenaTestNB3)
	}
	nb1 := nbs[athenaTestNB1]
	if sv(nb1.Name) != "explore" || sv(nb1.Region) != testRegion || nb1.Type != TypeAthenaNotebook {
		t.Errorf("nb1 name/region/type = %q/%q/%q, want explore/%s/%s", sv(nb1.Name), sv(nb1.Region), nb1.Type, testRegion, TypeAthenaNotebook)
	}
	if got := sv(nb1.CreatedAt); got != "2026-03-01T10:00:00Z" {
		t.Errorf("nb1 CreatedAt = %q, want 2026-03-01T10:00:00Z", got)
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(nb1.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs %s: %v", nb1.AttributesJSON, err)
	}
	if attrs["Type"] != "IPYNB" || attrs["WorkGroup"] != athenaTestSparkWG1 {
		t.Errorf("nb1 attrs Type/WorkGroup = %v/%v, want IPYNB/%s", attrs["Type"], attrs["WorkGroup"], athenaTestSparkWG1)
	}
	assertContainsNative(t, st, athenaTestSparkWGARN1, athenaTestNB1)
	assertContainsNative(t, st, athenaTestSparkWGARN1, athenaTestNB2)
	assertContainsNative(t, st, athenaTestSparkWGARN2, athenaTestNB3)
	assertContainsEdgesDeclared(t, st)
}

func TestScanAthena_NoSparkWorkGroupsOrNotebooks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		engine    string
		wantCalls []string
	}{
		{"only SQL workgroups", "Athena engine version 3", nil},
		{"Spark workgroups without notebooks", "PySpark engine version 3", []string{athenaTestSparkWG1, athenaTestSparkWG2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var nbCalls parentCalls
			h := athenaTopLevelHandlers(&nbCalls)
			h["GetWorkGroup"] = func(in any) (any, error) {
				name := sv(in.(*athena.GetWorkGroupInput).WorkGroup)
				engine := "Athena engine version 3"
				if name != athenaTestSQLWG {
					engine = tc.engine
				}
				return &athena.GetWorkGroupOutput{WorkGroup: athenaEngine(name, engine)}, nil
			}
			h["ListNotebookMetadata"] = func(in any) (any, error) {
				nbCalls.add(sv(in.(*athena.ListNotebookMetadataInput).WorkGroup))
				return &athena.ListNotebookMetadataOutput{}, nil
			}
			total, err := scanAthenaTopLevel(t, st, h)
			if err != nil {
				t.Fatalf("scanAthena: %v", err)
			}
			if total != 4 {
				t.Errorf("total = %d, want 4 (3 workgroups + 1 named query)", total)
			}
			if got := nbCalls.distinct(); !slices.Equal(got, tc.wantCalls) {
				t.Errorf("notebooks listed for %v, want %v", got, tc.wantCalls)
			}
			if n := len(rowsByNativeID(t, st, TypeAthenaNotebook)); n != 0 {
				t.Errorf("notebook rows = %d, want 0", n)
			}
		})
	}
}

// A failed workgroup drops only its own notebooks. A first-page
// InvalidRequestException is checked with GetWorkGroup: deleted since listing
// is silent, disabled warns once however many workgroups hit it, and still
// enabled (or unexplained) fails the scan. A denial or a transient error after
// retries warns once; any other error, or an InvalidRequestException on a later
// page, fails the scan. Every case runs after every older phase stored its
// rows.
func TestScanAthena_FailedNotebookWorkGroup(t *testing.T) {
	t.Parallel()
	gone := func(string) (any, error) { return nil, apiErr("InvalidRequestException", "WorkGroup is not found.") }
	disabled := func(name string) (any, error) {
		wg := athenaEngine(name, "PySpark engine version 3")
		wg.State = athenatypes.WorkGroupStateDisabled
		return &athena.GetWorkGroupOutput{WorkGroup: wg}, nil
	}
	enabled := func(name string) (any, error) {
		return &athena.GetWorkGroupOutput{WorkGroup: athenaEngine(name, "PySpark engine version 3")}, nil
	}
	// A network error, so a wrapping that kept it would make the refusal read as transient.
	unreachableConfirm := func(string) (any, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	}
	for _, tc := range []struct {
		name      string
		failWGs   []string
		onPage2   bool
		err       error
		confirm   func(name string) (any, error) // GetWorkGroup after the scan's own; nil means it must not be called
		wantCode  string                         // "" means the scan returns nil
		wantNBs   []string
		wantWarns int
	}{
		{"workgroup gone", []string{athenaTestSparkWG2}, false, athenaRefused, gone, "", []string{athenaTestNB1, athenaTestNB2}, 0},
		{"workgroup disabled", []string{athenaTestSparkWG2}, false, athenaRefused, disabled, "", []string{athenaTestNB1, athenaTestNB2}, 1},
		{"every workgroup disabled", []string{athenaTestSparkWG1, athenaTestSparkWG2}, false, athenaRefused, disabled, "", nil, 1},
		{"refused while enabled", []string{athenaTestSparkWG2}, false, athenaRefused, enabled, "InvalidRequestException", nil, 0},
		{"refused on every workgroup", []string{athenaTestSparkWG1, athenaTestSparkWG2}, false, athenaRefused, enabled, "InvalidRequestException", nil, 0},
		{"refusal not explained", []string{athenaTestSparkWG2}, false, athenaRefused, unreachableConfirm, "InvalidRequestException", nil, 0},
		{"denied on every workgroup", []string{athenaTestSparkWG1, athenaTestSparkWG2}, false, athenaDenied, nil, "", nil, 1},
		{"throttled workgroup", []string{athenaTestSparkWG2}, false, apiErr("TooManyRequestsException", "slow down"), nil, "", []string{athenaTestNB1, athenaTestNB2}, 1},
		{"refused on a later page", []string{athenaTestSparkWG1}, true, athenaRefused, nil, "InvalidRequestException", nil, 0},
		{"other error", []string{athenaTestSparkWG2}, false, athenaBadInput, nil, "ValidationException", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "athena:ListNotebookMetadata")
			var nbCalls, wgCalls parentCalls
			h := athenaTopLevelHandlers(&nbCalls)
			nextNB := h["ListNotebookMetadata"]
			h["ListNotebookMetadata"] = func(in any) (any, error) {
				p := in.(*athena.ListNotebookMetadataInput)
				if slices.Contains(tc.failWGs, sv(p.WorkGroup)) && (p.NextToken != nil) == tc.onPage2 {
					return nil, tc.err
				}
				return nextNB(in)
			}
			nextWG := h["GetWorkGroup"]
			h["GetWorkGroup"] = func(in any) (any, error) {
				name := sv(in.(*athena.GetWorkGroupInput).WorkGroup)
				wgCalls.add(name)
				if athenaCallCount(&wgCalls, name) == 1 {
					return nextWG(in)
				}
				if tc.confirm == nil {
					t.Errorf("GetWorkGroup %s called to explain a refusal, want no call", name)
					return nil, fmt.Errorf("unexpected GetWorkGroup %s", name)
				}
				return tc.confirm(name)
			}
			_, err := scanAthenaTopLevel(t, st, h)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
				if isTransientNetworkError(err) {
					t.Errorf("err = %v reads as transient, want a hard error", err)
				}
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeAthenaNotebook)); !slices.Equal(got, tc.wantNBs) {
				t.Errorf("notebook rows = %v, want %v", got, tc.wantNBs)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			if _, ok := rowsByNativeID(t, st, TypeAthenaNamedQuery)[athenaTestNamedQuery]; !ok {
				t.Errorf("named query %s missing; the notebook phase must run after every older phase", athenaTestNamedQuery)
			}
		})
	}
}

// athenaCallCount counts the calls recorded for id.
func athenaCallCount(c *parentCalls, id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, got := range c.ids {
		if got == id {
			n++
		}
	}
	return n
}

// A hard error in one phase still lets every later phase run, the
// saved-query and notebook phases over the workgroups stored, and is
// returned.
func TestScanAthena_PhaseErrorStillRunsLaterPhases(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var nbCalls parentCalls
	h := athenaTopLevelHandlers(&nbCalls)
	h["ListDataCatalogs"] = func(any) (any, error) { return nil, athenaBadInput }
	total, err := scanAthenaTopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	if got := nativeIDs(rowsByNativeID(t, st, TypeAthenaNotebook)); !slices.Equal(got, []string{athenaTestNB1, athenaTestNB2, athenaTestNB3}) {
		t.Errorf("notebook rows = %v, want all three", got)
	}
	if _, ok := rowsByNativeID(t, st, TypeAthenaNamedQuery)[athenaTestNamedQuery]; !ok {
		t.Errorf("named query %s missing; the phases after the failed catalogs must run", athenaTestNamedQuery)
	}
	// 3 workgroups + 1 named query + 3 notebooks.
	if total != 7 {
		t.Errorf("total = %d, want 7", total)
	}
}

// A transient error in one phase is demoted to a warning under its op so
// that the notebook phase's hard error still reaches the dispatcher as a
// hard error, not hidden inside a join the dispatcher would read as
// transient.
func TestScanAthena_PhaseTransientDoesNotMaskHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, athenaListDataCatalogsOp)
	var nbCalls parentCalls
	h := athenaTopLevelHandlers(&nbCalls)
	h["ListDataCatalogs"] = func(any) (any, error) { return nil, apiErr("TooManyRequestsException", "slow down") }
	h["ListNotebookMetadata"] = func(any) (any, error) { return nil, athenaBadInput }
	_, err := scanAthenaTopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	if isTransientNetworkError(err) {
		t.Errorf("err = %v reads as transient; the hard notebook error must not be masked", err)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d, want 1 for the demoted transient", athenaListDataCatalogsOp, *warns)
	}
}

// spark-b's engine is read from its listed summary until a GetWorkGroup body
// is read, whose engine then decides: a denied or empty body keeps the
// summary's verdict, so a Spark summary still gets its notebooks listed and a
// summary without an engine does not; a body naming the SQL engine overrides
// a Spark summary. A denied body warns once under GetWorkGroup, and spark-b
// is stored in every case.
func TestScanAthena_SparkWorkGroupFromSummaryUntilBodyRead(t *testing.T) {
	t.Parallel()
	const pySpark, sql = "PySpark engine version 3", "Athena engine version 3"
	for _, c := range []struct {
		name          string
		summaryEngine string
		get           func(in any) (any, error)
		wantListed    []string
		wantDenials   int
	}{
		{
			"denied body, Spark summary", pySpark, func(any) (any, error) { return nil, athenaDenied },
			[]string{athenaTestSparkWG1, athenaTestSparkWG2},
			1,
		},
		{
			"denied body, summary without engine", "", func(any) (any, error) { return nil, athenaDenied },
			[]string{athenaTestSparkWG1},
			1,
		},
		{
			"empty body, Spark summary", pySpark, func(any) (any, error) { return &athena.GetWorkGroupOutput{}, nil },
			[]string{athenaTestSparkWG1, athenaTestSparkWG2},
			0,
		},
		{"SQL body, Spark summary", pySpark, func(any) (any, error) {
			return &athena.GetWorkGroupOutput{WorkGroup: athenaEngine(athenaTestSparkWG2, sql)}, nil
		}, []string{athenaTestSparkWG1}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, athenaGetWorkGroupOp)
			var nbCalls parentCalls
			h := athenaTopLevelHandlers(&nbCalls)
			get := h["GetWorkGroup"]
			h["GetWorkGroup"] = func(in any) (any, error) {
				if sv(in.(*athena.GetWorkGroupInput).WorkGroup) == athenaTestSparkWG2 {
					return c.get(in)
				}
				return get(in)
			}
			list := h["ListWorkGroups"]
			h["ListWorkGroups"] = func(in any) (any, error) {
				out, err := list(in)
				if c.summaryEngine != "" {
					wgs := out.(*athena.ListWorkGroupsOutput).WorkGroups
					wgs[2].EngineVersion = &athenatypes.EngineVersion{EffectiveEngineVersion: ptrStr(c.summaryEngine)}
				}
				return out, err
			}
			total, err := scanAthenaTopLevel(t, st, h)
			if err != nil {
				t.Fatalf("scanAthena: %v", err)
			}
			if got := nbCalls.distinct(); !slices.Equal(got, c.wantListed) {
				t.Errorf("notebooks listed for %v, want %v", got, c.wantListed)
			}
			if _, ok := rowsByNativeID(t, st, TypeAthenaWorkgroup)[athenaTestSparkWGARN2]; !ok {
				t.Errorf("workgroup %s not stored; a failed body read must keep its summary", athenaTestSparkWGARN2)
			}
			// 3 workgroups + 1 named query + spark-a's 2 notebooks, + spark-b's 1.
			want := 6
			if len(c.wantListed) == 2 {
				want = 7
			}
			if total != want || *warns != c.wantDenials {
				t.Errorf("total, %s warnings = %d, %d; want %d, %d", athenaGetWorkGroupOp, total, *warns, want, c.wantDenials)
			}
		})
	}
}

func TestIsAthenaSparkWorkGroup_EngineVersion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		v    *athenatypes.EngineVersion
		want bool
	}{
		{"PySpark engine", &athenatypes.EngineVersion{EffectiveEngineVersion: ptrStr("PySpark engine version 3")}, true},
		{"Athena SQL engine", &athenatypes.EngineVersion{EffectiveEngineVersion: ptrStr("Athena engine version 3")}, false},
		{"only the selected engine names Spark", &athenatypes.EngineVersion{SelectedEngineVersion: ptrStr("PySpark engine version 3")}, false},
		{"no engine version", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAthenaSparkWorkGroup(tc.v); got != tc.want {
				t.Errorf("isAthenaSparkWorkGroup = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestListAthenaNotebooks_FailsOnRepeatedToken(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	cfg := opStubConfig(map[string]func(in any) (any, error){
		"ListNotebookMetadata": func(any) (any, error) {
			n := calls.Add(1)
			if n > 2 {
				return nil, errors.New("called again after a repeated token")
			}
			return &athena.ListNotebookMetadataOutput{NextToken: ptrStr("same"), NotebookMetadataList: []athenatypes.NotebookMetadata{
				{NotebookId: ptrStr(fmt.Sprintf("nb%d", n))},
			}}, nil
		},
	}, athenaDenied)
	client := athena.NewFromConfig(cfg)
	rows, err := listAthenaNotebooks(context.Background(), client, newTestAccount(testAccountID), testRegion, testScanID,
		childParent{id: athenaTestSparkWG1, arn: athenaTestSparkWGARN1})
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("calls = %d, want 2", got)
	}
	want := []string{athenaTestSparkWGARN1 + "/notebook/nb1", athenaTestSparkWGARN1 + "/notebook/nb2"}
	if got := resourceNativeIDs(rows); !slices.Equal(got, want) {
		t.Errorf("rows = %v, want both pages' rows %v", got, want)
	}
}

// athenaTagRow is one row of the tag fixture (athenaTagHandlers): its type,
// listed name and ARN kind. Names ending "-t" carry two tags, the others
// none.
type athenaTagRow struct{ typ, kind, name string }

var athenaTagRows = []athenaTagRow{
	{TypeAthenaWorkgroup, "workgroup", "wg-t"},
	{TypeAthenaWorkgroup, "workgroup", "wg-b"},
	{TypeAthenaDataCatalog, "datacatalog", "AwsDataCatalog"},
	{TypeAthenaDataCatalog, "datacatalog", "cat-t"},
	{TypeAthenaDataCatalog, "datacatalog", "cat-b"},
	{TypeAthenaCapacityReservation, "capacity-reservation", "cr-t"},
	{TypeAthenaCapacityReservation, "capacity-reservation", "cr-b"},
}

// athenaTagARN is the commercial ARN of a fixture row, built independently
// of the scanner's builders.
func athenaTagARN(kind, name string) string {
	return partitionARN("aws", "athena", testRegion, kind+"/"+name)
}

// athenaTagCalls records the stubbed calls and answers ListTagsForResource
// from tags and errs, keyed by ARN, one tag per page.
type athenaTagCalls struct {
	*bacCalls
	catalogGets parentCalls
}

// newAthenaTagCalls tags each "-t" fixture row of partition p, listed in
// region, with its type and a second key.
func newAthenaTagCalls(p, region string) *athenaTagCalls {
	c := &athenaTagCalls{bacCalls: newBACCalls()}
	for _, r := range athenaTagRows {
		if strings.HasSuffix(r.name, "-t") {
			c.tags[partitionARN(p, "athena", region, r.kind+"/"+r.name)] = map[string]string{"type": r.typ, "env": "prod"}
		}
	}
	return c
}

// listTags answers one tag a page, in key order, so a reader that stops
// after the first page loses the rest. tagARNs records each first page.
func (c *athenaTagCalls) listTags(in any) (any, error) {
	p := in.(*athena.ListTagsForResourceInput)
	arn := sv(p.ResourceARN)
	c.called("ListTagsForResource", in)
	c.mu.Lock()
	if p.NextToken == nil {
		c.tagARNs = append(c.tagARNs, arn)
	}
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	keys := slices.Sorted(maps.Keys(tags))
	i := 0
	if p.NextToken != nil {
		i, _ = strconv.Atoi(*p.NextToken)
	}
	out := &athena.ListTagsForResourceOutput{}
	if i < len(keys) {
		k, v := keys[i], tags[keys[i]]
		out.Tags = []athenatypes.Tag{{Key: &k, Value: &v}}
	}
	if i+1 < len(keys) {
		out.NextToken = ptrStr(strconv.Itoa(i + 1))
	}
	return out, nil
}

// athenaTagCreated is every fixture element's CreationTime.
var athenaTagCreated = time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)

// athenaTagHandlers answers every op the scan calls with the fixture
// (athenaTagRows), each listing over two pages, AwsDataCatalog on the first
// page of catalogs. No workgroup runs Spark and none holds a saved query.
// GetDataCatalog refuses AwsDataCatalog as Athena does.
func athenaTagHandlers(c *athenaTagCalls) map[string]func(any) (any, error) {
	firstPage := func(in any) bool { return bacField[*string](in, "NextToken") == nil }
	return map[string]func(any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListWorkGroups": c.answer("ListWorkGroups", func(in any) any {
			if firstPage(in) {
				return &athena.ListWorkGroupsOutput{NextToken: ptrStr("p2"), WorkGroups: []athenatypes.WorkGroupSummary{{
					Name: ptrStr("wg-t"), State: athenatypes.WorkGroupStateEnabled, CreationTime: &athenaTagCreated, Description: ptrStr("listed"),
				}}}
			}
			return &athena.ListWorkGroupsOutput{WorkGroups: []athenatypes.WorkGroupSummary{{
				Name: ptrStr("wg-b"), State: athenatypes.WorkGroupStateDisabled, Description: ptrStr("listed"),
			}}}
		}),
		"GetWorkGroup": c.answer("GetWorkGroup", func(in any) any {
			name := sv(in.(*athena.GetWorkGroupInput).WorkGroup)
			wg := athenaEngine(name, "Athena engine version 3")
			wg.Description = ptrStr("detail " + name)
			return &athena.GetWorkGroupOutput{WorkGroup: wg}
		}),
		"ListDataCatalogs": c.answer("ListDataCatalogs", func(in any) any {
			if firstPage(in) {
				return &athena.ListDataCatalogsOutput{NextToken: ptrStr("p2"), DataCatalogsSummary: []athenatypes.DataCatalogSummary{
					{CatalogName: ptrStr("AwsDataCatalog"), Type: athenatypes.DataCatalogTypeGlue},
					{CatalogName: ptrStr("cat-t"), Type: athenatypes.DataCatalogTypeLambda, Status: athenatypes.DataCatalogStatusCreateComplete},
				}}
			}
			return &athena.ListDataCatalogsOutput{DataCatalogsSummary: []athenatypes.DataCatalogSummary{
				{CatalogName: ptrStr("cat-b"), Type: athenatypes.DataCatalogTypeHive},
			}}
		}),
		"GetDataCatalog": func(in any) (any, error) {
			c.called("GetDataCatalog", in)
			name := sv(in.(*athena.GetDataCatalogInput).Name)
			c.catalogGets.add(name)
			if name == "AwsDataCatalog" {
				return nil, apiErr("InvalidRequestException", "Catalog AwsDataCatalog was not found.")
			}
			return &athena.GetDataCatalogOutput{DataCatalog: &athenatypes.DataCatalog{
				Name: &name, Type: athenatypes.DataCatalogTypeLambda, Description: ptrStr("detail " + name),
			}}, nil
		},
		"ListCapacityReservations": c.answer("ListCapacityReservations", func(in any) any {
			if firstPage(in) {
				return &athena.ListCapacityReservationsOutput{NextToken: ptrStr("p2"), CapacityReservations: []athenatypes.CapacityReservation{{
					Name: ptrStr("cr-t"), Status: athenatypes.CapacityReservationStatusActive, CreationTime: &athenaTagCreated, TargetDpus: sdkaws.Int32(24),
				}}}
			}
			return &athena.ListCapacityReservationsOutput{CapacityReservations: []athenatypes.CapacityReservation{{
				Name: ptrStr("cr-b"), Status: athenatypes.CapacityReservationStatusPending, CreationTime: &athenaTagCreated, TargetDpus: sdkaws.Int32(8),
			}}}
		}),
		"ListNamedQueries":       c.answer("ListNamedQueries", func(any) any { return &athena.ListNamedQueriesOutput{} }),
		"ListPreparedStatements": c.answer("ListPreparedStatements", func(any) any { return &athena.ListPreparedStatementsOutput{} }),
	}
}

// athenaTagScan runs the scan's real entry point against handlers; every op
// they do not answer is denied.
func athenaTagScan(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, athenaDenied)
	return scanAthena(ctx, acct, testRegion, st, testScanID)
}

func athenaTagRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// athenaAttr is the string at key of r's attributes.
func athenaAttr(t *testing.T, r store.Resource, key string) string {
	t.Helper()
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("%s attrs %s: %v", r.NativeID, r.AttributesJSON, err)
	}
	got, _ := attrs[key].(string)
	return got
}

// Every taggable type stores the tags ListTagsForResource returns for its
// ARN, read over every page, and nil for a resource without tags; every
// stored row's ARN but AwsDataCatalog's is sent to the tag op. AwsDataCatalog
// is stored as its summary, ManagedByProvider, with no GetDataCatalog or tag
// read. Both pages of every listing are read with the model's page cap, the
// tag op with 75, and the scan reports no error or warning. Each row keeps
// the base scan's Name and attributes (the Get body for workgroups and
// catalogs, the element for capacity reservations); Status and CreatedAt come
// from the listed element, nil where it has none.
func TestScanAthena_StoresTagsOfEveryType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newAthenaTagCalls("aws", testRegion)

	counted, upserts := countUpserts(st)
	total, err := athenaTagScan(context.Background(), t, counted, athenaTagHandlers(calls))
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 7 || inserted != 7 || *warns != 0 {
		t.Errorf("total, inserted, warnings = %d, %d, %d; want 7, 7, 0", total, inserted, *warns)
	}
	var wantTagARNs []string
	for _, w := range athenaTagRows {
		arn := athenaTagARN(w.kind, w.name)
		r, ok := athenaTagRowOf(t, st, w.typ, arn)
		if !ok {
			t.Errorf("%s %s not stored", w.typ, arn)
			continue
		}
		if sv(r.Name) != w.name || sv(r.Region) != testRegion {
			t.Errorf("%s Name, Region = %q, %q; want %q, %q", arn, sv(r.Name), sv(r.Region), w.name, testRegion)
		}
		if w.name != "AwsDataCatalog" {
			wantTagARNs = append(wantTagARNs, arn)
		}
		if strings.HasSuffix(w.name, "-t") {
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": w.typ, "env": "prod"})
		} else if r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
		}
		if managed := w.name == "AwsDataCatalog"; r.ManagedByProvider != managed {
			t.Errorf("%s ManagedByProvider = %v; want %v", arn, r.ManagedByProvider, managed)
		}
	}
	got := slices.Sorted(slices.Values(calls.tagARNs))
	slices.Sort(wantTagARNs)
	if !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantTagARNs)
	}
	if got := calls.catalogGets.distinct(); !slices.Equal(got, []string{"cat-b", "cat-t"}) {
		t.Errorf("GetDataCatalog names = %v; want [cat-b cat-t]", got)
	}
	for op, want := range map[string][]int32{
		"ListWorkGroups": {50, 50}, "ListDataCatalogs": {50, 50}, "ListCapacityReservations": {50, 50},
		"ListTagsForResource": {75, 75, 75, 75, 75, 75, 75, 75, 75},
	} {
		if got := calls.maxResults[op]; !slices.Equal(got, want) {
			t.Errorf("%s MaxResults = %v; want %v", op, got, want)
		}
	}

	wgT, _ := athenaTagRowOf(t, st, TypeAthenaWorkgroup, athenaTagARN("workgroup", "wg-t"))
	wgB, _ := athenaTagRowOf(t, st, TypeAthenaWorkgroup, athenaTagARN("workgroup", "wg-b"))
	if sv(wgT.Status) != "ENABLED" || sv(wgB.Status) != "DISABLED" || sv(wgT.CreatedAt) != "2026-04-01T09:00:00Z" || wgB.CreatedAt != nil {
		t.Errorf("workgroup Status = %q, %q, CreatedAt %q, %v; want ENABLED, DISABLED, 2026-04-01T09:00:00Z, nil",
			sv(wgT.Status), sv(wgB.Status), sv(wgT.CreatedAt), wgB.CreatedAt)
	}
	if got := athenaAttr(t, wgT, "Description"); got != "detail wg-t" {
		t.Errorf("wg-t attrs Description = %q; want the GetWorkGroup body's", got)
	}
	catT, _ := athenaTagRowOf(t, st, TypeAthenaDataCatalog, athenaTagARN("datacatalog", "cat-t"))
	glue, _ := athenaTagRowOf(t, st, TypeAthenaDataCatalog, athenaTagARN("datacatalog", "AwsDataCatalog"))
	if got := athenaAttr(t, catT, "Description"); got != "detail cat-t" || sv(catT.Status) != "CREATE_COMPLETE" || catT.CreatedAt != nil {
		t.Errorf("cat-t attrs Description = %q, Status %q, CreatedAt %v; want the GetDataCatalog body's, CREATE_COMPLETE, nil", got, sv(catT.Status), catT.CreatedAt)
	}
	if got := athenaAttr(t, glue, "CatalogName"); got != "AwsDataCatalog" || athenaAttr(t, glue, "Type") != "GLUE" || glue.Status != nil {
		t.Errorf("AwsDataCatalog attrs = %s, Status %v; want its DataCatalogSummary, nil", glue.AttributesJSON, glue.Status)
	}
	crT, _ := athenaTagRowOf(t, st, TypeAthenaCapacityReservation, athenaTagARN("capacity-reservation", "cr-t"))
	crB, _ := athenaTagRowOf(t, st, TypeAthenaCapacityReservation, athenaTagARN("capacity-reservation", "cr-b"))
	if sv(crT.Status) != "ACTIVE" || sv(crB.Status) != "PENDING" || sv(crT.CreatedAt) != "2026-04-01T09:00:00Z" {
		t.Errorf("capacity reservation Status = %q, %q, CreatedAt %q; want ACTIVE, PENDING, 2026-04-01T09:00:00Z", sv(crT.Status), sv(crB.Status), sv(crT.CreatedAt))
	}
	if !strings.Contains(crT.AttributesJSON, `"TargetDpus":24`) {
		t.Errorf("cr-t attrs = %s; want the element verbatim", crT.AttributesJSON)
	}
}

// An aws-us-gov account scanned in us-gov-west-1 sends ARNs of its own
// partition and region to the tag op, and stores the rows under them.
func TestScanAthena_GovCloudARNsReachTagOp(t *testing.T) {
	t.Parallel()
	const govRegion = "us-gov-west-1"
	st := newTestStore(t)
	calls := newAthenaTagCalls("aws-us-gov", govRegion)
	acct := partitionAccount("aws-us-gov")
	acct.cfg = opStubConfig(athenaTagHandlers(calls), athenaDenied)

	total, err := scanAthena(context.Background(), acct, govRegion, st, testScanID)
	if err != nil || total != 7 {
		t.Fatalf("scan = %d, %v; want 7, nil", total, err)
	}
	const (
		govCR = "arn:aws-us-gov:athena:us-gov-west-1:123456789012:capacity-reservation/cr-t"
		govDC = "arn:aws-us-gov:athena:us-gov-west-1:123456789012:datacatalog/cat-t"
		govWG = "arn:aws-us-gov:athena:us-gov-west-1:123456789012:workgroup/wg-t"
	)
	want := []string{
		"arn:aws-us-gov:athena:us-gov-west-1:123456789012:capacity-reservation/cr-b", govCR,
		"arn:aws-us-gov:athena:us-gov-west-1:123456789012:datacatalog/cat-b", govDC,
		"arn:aws-us-gov:athena:us-gov-west-1:123456789012:workgroup/wg-b", govWG,
	}
	if got := slices.Sorted(slices.Values(calls.tagARNs)); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
	}
	for _, c := range []struct{ typ, arn string }{
		{TypeAthenaWorkgroup, govWG}, {TypeAthenaDataCatalog, govDC}, {TypeAthenaCapacityReservation, govCR},
	} {
		r, ok := athenaTagRowOf(t, st, c.typ, c.arn)
		if !ok {
			t.Fatalf("%s %s not stored", c.typ, c.arn)
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"type": c.typ, "env": "prod"})
		if sv(r.Region) != govRegion {
			t.Errorf("%s Region = %q; want %s", c.arn, sv(r.Region), govRegion)
		}
	}
}

// Every listing returning nothing stores nothing, reads no tag or body and
// reports nothing.
func TestScanAthena_EmptyListings(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newAthenaTagCalls("aws", testRegion)
	h := athenaTagHandlers(calls)
	h["ListWorkGroups"] = calls.answer("ListWorkGroups", func(any) any { return &athena.ListWorkGroupsOutput{} })
	h["ListDataCatalogs"] = calls.answer("ListDataCatalogs", func(any) any { return &athena.ListDataCatalogsOutput{} })
	h["ListCapacityReservations"] = calls.answer("ListCapacityReservations", func(any) any { return &athena.ListCapacityReservationsOutput{} })

	counted, upserts := countUpserts(st)
	total, err := athenaTagScan(context.Background(), t, counted, h)
	inserted := upserts()
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if *warns != 0 || len(calls.tagARNs) != 0 || calls.count("GetWorkGroup") != 0 || calls.count("GetDataCatalog") != 0 || calls.count("ListNamedQueries") != 0 {
		t.Errorf("warnings, tag reads, GetWorkGroup, GetDataCatalog, ListNamedQueries = %d, %d, %d, %d, %d; want all 0",
			*warns, len(calls.tagARNs), calls.count("GetWorkGroup"), calls.count("GetDataCatalog"), calls.count("ListNamedQueries"))
	}
}

// A rescan reports every stored row in its total, not only the rows the store
// newly wrote. On a first scan into an empty store every row is new, so only a
// rescan catches a total taken from the store's written count.
func TestScanAthena_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newAthenaTagCalls("aws", testRegion)
	if _, err := athenaTagScan(context.Background(), t, st, athenaTagHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, err := athenaTagScan(context.Background(), t, st, athenaTagHandlers(calls))
	if err != nil || total != 7 {
		t.Errorf("rescan = %d, %v; want 7, nil", total, err)
	}
}

// A failed tag read keeps the row without tags unless the resource is gone:
// a denial warns once under athena:ListTagsForResource, a throttle after
// retries is a transient error, any other code a hard one;
// ResourceNotFoundException drops the row. The same holds for each type.
func TestScanAthena_TagReadFailures(t *testing.T) {
	t.Parallel()
	for _, target := range []athenaTagRow{athenaTagRows[0], athenaTagRows[3], athenaTagRows[5]} {
		t.Run(target.typ, func(t *testing.T) {
			arn := athenaTagARN(target.kind, target.name)
			for _, c := range []struct {
				name      string
				err       error
				kept      bool
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", athenaDenied, true, 0, 1},
				{"not found", apiErr("ResourceNotFoundException", "gone"), false, 0, 0},
				{"other error", apiErr("InvalidRequestException", "refused"), true, outcomeError, 0},
				{"transient", apiErr("TooManyRequestsException", "slow down"), true, outcomeTransient, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, athenaTagsOp)
					calls := newAthenaTagCalls("aws", testRegion)
					calls.errs[arn] = c.err

					total, err := athenaTagScan(context.Background(), t, st, athenaTagHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.outcome == outcomeError {
						assertAPICode(t, err, "InvalidRequestException")
					}
					r, ok := athenaTagRowOf(t, st, target.typ, arn)
					if ok != c.kept {
						t.Fatalf("row stored = %v; want %v", ok, c.kept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					want := 7
					if !c.kept {
						want = 6
					}
					if total != want {
						t.Errorf("total = %d; want %d", total, want)
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", athenaTagsOp, *warnings, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
				})
			}
		})
	}
}

// Denied tag reads in several listings are one warning for the scan.
func TestScanAthena_TagDenialsWarnOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warnings := bacWarnings(st, athenaTagsOp)
	calls := newAthenaTagCalls("aws", testRegion)
	calls.errs[athenaTagARN("workgroup", "wg-t")] = athenaDenied
	calls.errs[athenaTagARN("capacity-reservation", "cr-b")] = athenaDenied

	if _, err := athenaTagScan(context.Background(), t, st, athenaTagHandlers(calls)); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if len(*warnings) != 1 {
		t.Errorf("%s warnings = %v; want one", athenaTagsOp, *warnings)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanAthena_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, athenaTagsOp)
	calls := newAthenaTagCalls("aws", testRegion)
	calls.errs[athenaTagARN("workgroup", "wg-t")] = apiErr("TooManyRequestsException", "slow down")
	calls.errs[athenaTagARN("datacatalog", "cat-b")] = apiErr("InvalidRequestException", "refused")

	_, err := athenaTagScan(context.Background(), t, st, athenaTagHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", athenaTagsOp, *warns)
	}
}

// A failed GetWorkGroup or GetDataCatalog keeps the listed summary as the
// row, its tags still read: a denial warns once under the Get op, a
// throttle after retries is a transient error, and any other code, the
// InvalidRequestException a resource deleted since the listing gets
// included (neither op models a not-found), is a hard one.
func TestScanAthena_BodyReadFailureKeepsSummary(t *testing.T) {
	t.Parallel()
	for _, target := range []struct {
		row      athenaTagRow
		op, attr string
		nameOf   func(in any) string
	}{
		{athenaTagRows[0], "GetWorkGroup", "Name", func(in any) string { return sv(in.(*athena.GetWorkGroupInput).WorkGroup) }},
		{athenaTagRows[3], "GetDataCatalog", "CatalogName", func(in any) string { return sv(in.(*athena.GetDataCatalogInput).Name) }},
	} {
		t.Run(target.op, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", athenaDenied, 0, 1},
				{"gone or refused", apiErr("InvalidRequestException", "was not found"), outcomeError, 0},
				{"transient", apiErr("TooManyRequestsException", "slow down"), outcomeTransient, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warns := countOpWarnings(st, "athena:"+target.op)
					calls := newAthenaTagCalls("aws", testRegion)
					h := athenaTagHandlers(calls)
					get := h[target.op]
					h[target.op] = func(in any) (any, error) {
						if target.nameOf(in) == target.row.name {
							return nil, c.err
						}
						return get(in)
					}

					total, err := athenaTagScan(context.Background(), t, st, h)
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.outcome == outcomeError {
						assertAPICode(t, err, "InvalidRequestException")
					}
					arn := athenaTagARN(target.row.kind, target.row.name)
					r, ok := athenaTagRowOf(t, st, target.row.typ, arn)
					if !ok {
						t.Fatalf("%s not stored; want its summary kept", arn)
					}
					if got := athenaAttr(t, r, target.attr); got != target.row.name || athenaAttr(t, r, "Description") == "detail "+target.row.name {
						t.Errorf("%s attrs = %s; want the listed summary", arn, r.AttributesJSON)
					}
					assertTagsMap(t, r.TagsJSON, map[string]string{"type": target.row.typ, "env": "prod"})
					if total != 7 || *warns != c.wantWarns {
						t.Errorf("total, athena:%s warnings = %d, %d; want 7, %d", target.op, total, *warns, c.wantWarns)
					}
				})
			}
		})
	}
}

// A GetWorkGroup or GetDataCatalog answer without a body keeps the listed
// summary as the row's attributes, with no error or warning.
func TestScanAthena_EmptyBodyKeepsSummary(t *testing.T) {
	t.Parallel()
	for _, target := range []struct {
		row      athenaTagRow
		op, attr string
		nameOf   func(in any) string
		empty    any
	}{
		{athenaTagRows[0], "GetWorkGroup", "Name", func(in any) string { return sv(in.(*athena.GetWorkGroupInput).WorkGroup) }, &athena.GetWorkGroupOutput{}},
		{athenaTagRows[3], "GetDataCatalog", "CatalogName", func(in any) string { return sv(in.(*athena.GetDataCatalogInput).Name) }, &athena.GetDataCatalogOutput{}},
	} {
		t.Run(target.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			calls := newAthenaTagCalls("aws", testRegion)
			h := athenaTagHandlers(calls)
			get := h[target.op]
			h[target.op] = func(in any) (any, error) {
				if target.nameOf(in) == target.row.name {
					return target.empty, nil
				}
				return get(in)
			}

			total, err := athenaTagScan(context.Background(), t, st, h)
			if err != nil || total != 7 || *warns != 0 {
				t.Fatalf("scan = %d, %v, %d warnings; want 7, nil, 0", total, err, *warns)
			}
			arn := athenaTagARN(target.row.kind, target.row.name)
			r, ok := athenaTagRowOf(t, st, target.row.typ, arn)
			if !ok {
				t.Fatalf("%s not stored; want its summary kept", arn)
			}
			if got := athenaAttr(t, r, target.attr); got != target.row.name {
				t.Errorf("%s attrs = %s; want the listed summary", arn, r.AttributesJSON)
			}
		})
	}
}

// A workgroup or catalog deleted between its listing and its Get fails the
// Get with the InvalidRequestException returned as a hard error, and its row
// is dropped when the tag read then answers ResourceNotFoundException.
func TestScanAthena_DeletedAfterListingDropsRow(t *testing.T) {
	t.Parallel()
	for _, target := range []struct {
		row    athenaTagRow
		op     string
		nameOf func(in any) string
	}{
		{athenaTagRows[0], "GetWorkGroup", func(in any) string { return sv(in.(*athena.GetWorkGroupInput).WorkGroup) }},
		{athenaTagRows[3], "GetDataCatalog", func(in any) string { return sv(in.(*athena.GetDataCatalogInput).Name) }},
	} {
		t.Run(target.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newAthenaTagCalls("aws", testRegion)
			arn := athenaTagARN(target.row.kind, target.row.name)
			calls.errs[arn] = apiErr("ResourceNotFoundException", "gone")
			h := athenaTagHandlers(calls)
			get := h[target.op]
			h[target.op] = func(in any) (any, error) {
				if target.nameOf(in) == target.row.name {
					return nil, apiErr("InvalidRequestException", "was not found")
				}
				return get(in)
			}

			total, err := athenaTagScan(context.Background(), t, st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidRequestException")
			if _, ok := athenaTagRowOf(t, st, target.row.typ, arn); ok || total != 6 {
				t.Errorf("%s stored = %v, total = %d; want false, 6", arn, ok, total)
			}
		})
	}
}

// A saved-query phase's batch read that stays throttled beside a hard error
// of another phase is demoted to one warning under the batch op, not under
// the phase's listing op.
func TestScanAthena_SavedQueryBatchThrottleWarnsUnderItsOp(t *testing.T) {
	t.Parallel()
	throttled := apiErr("TooManyRequestsException", "slow down")
	for _, c := range []struct{ listOp, batchOp string }{
		{"ListNamedQueries", "BatchGetNamedQuery"},
		{"ListPreparedStatements", "BatchGetPreparedStatement"},
	} {
		t.Run(c.batchOp, func(t *testing.T) {
			st := newTestStore(t)
			batchWarns := countOpWarnings(st, "athena:"+c.batchOp)
			listWarns := countOpWarnings(st, "athena:"+c.listOp)
			var nbCalls parentCalls
			h := athenaTopLevelHandlers(&nbCalls)
			h["ListDataCatalogs"] = func(any) (any, error) { return nil, athenaBadInput }
			h["ListPreparedStatements"] = func(in any) (any, error) {
				if sv(in.(*athena.ListPreparedStatementsInput).WorkGroup) != athenaTestSQLWG {
					return &athena.ListPreparedStatementsOutput{}, nil
				}
				return &athena.ListPreparedStatementsOutput{PreparedStatements: []athenatypes.PreparedStatementSummary{{StatementName: ptrStr("stmt")}}}, nil
			}
			h["BatchGetPreparedStatement"] = func(any) (any, error) { return &athena.BatchGetPreparedStatementOutput{}, nil }
			h[c.batchOp] = func(any) (any, error) { return nil, throttled }

			_, err := scanAthenaTopLevel(t, st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if *batchWarns != 1 || *listWarns != 0 {
				t.Errorf("athena:%s, athena:%s warnings = %d, %d; want 1, 0", c.batchOp, c.listOp, *batchWarns, *listWarns)
			}
		})
	}
}

// A listing's failed page keeps the pages read before it, and no listing's
// failure stops the phases after it: the saved-query listing reads the
// workgroups stored.
func TestScanAthena_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newAthenaTagCalls("aws", testRegion)
	h := athenaTagHandlers(calls)
	h["ListWorkGroups"] = failOnCall(2, apiErr("InvalidRequestException", "page 2"), h["ListWorkGroups"])
	h["ListDataCatalogs"] = func(any) (any, error) { return nil, athenaBadInput }

	total, err := athenaTagScan(context.Background(), t, st, h)
	assertGlueOutcome(t, err, outcomeError)
	if got := ecsErrCodes(err); !slices.Equal(got, []string{"InvalidRequestException", "ValidationException"}) {
		t.Errorf("error codes = %v; want the workgroup page's and the catalog listing's", got)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeAthenaWorkgroup)); !slices.Equal(got, []string{athenaTagARN("workgroup", "wg-t")}) {
		t.Errorf("workgroup rows = %v; want page 1's wg-t", got)
	}
	if n := len(rowsByNativeID(t, st, TypeAthenaCapacityReservation)); n != 2 {
		t.Errorf("capacity reservation rows = %d; want 2", n)
	}
	if total != 3 || calls.count("ListNamedQueries") != 1 {
		t.Errorf("total, ListNamedQueries calls = %d, %d; want 3, 1", total, calls.count("ListNamedQueries"))
	}
}

// A listing whose service repeats its NextToken keeps both pages and fails
// with errRepeatedNextToken instead of looping.
func TestScanAthena_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newAthenaTagCalls("aws", testRegion)
	h := athenaTagHandlers(calls)
	pages := h["ListCapacityReservations"]
	h["ListCapacityReservations"] = func(in any) (any, error) {
		out, err := pages(in)
		out.(*athena.ListCapacityReservationsOutput).NextToken = ptrStr("same")
		return out, err
	}

	total, err := athenaTagScan(context.Background(), t, st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if n := calls.count("ListCapacityReservations"); n != 2 || total != 7 {
		t.Errorf("ListCapacityReservations calls, total = %d, %d; want 2, 7", n, total)
	}
}

// A denied listing warns once under its own op and costs no other phase its
// rows; a denied workgroup listing leaves the saved-query listings no
// workgroup to read.
func TestScanAthena_DeniedListingWarnsUnderItsOp(t *testing.T) {
	t.Parallel()
	for op, lost := range map[string]int{"ListWorkGroups": 2, "ListDataCatalogs": 3, "ListCapacityReservations": 2} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, "athena:"+op)
			calls := newAthenaTagCalls("aws", testRegion)
			h := athenaTagHandlers(calls)
			h[op] = func(any) (any, error) { return nil, athenaDenied }

			total, err := athenaTagScan(context.Background(), t, st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || *opWarns != 1 {
				t.Errorf("warnings = %d (athena:%s: %d); want 1 under the op", *warns, op, *opWarns)
			}
			if stored := countStoredRows(t, st); total != 7-lost || stored != 7-lost {
				t.Errorf("total = %d, stored rows = %d; want %d", total, stored, 7-lost)
			}
			wantNQ := 2
			if op == "ListWorkGroups" {
				wantNQ = 0
			}
			if n := calls.count("ListNamedQueries"); n != wantNQ {
				t.Errorf("ListNamedQueries calls = %d; want %d", n, wantNQ)
			}
		})
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanAthena_FailedWriteStopsWithoutWarning(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeAthenaWorkgroup)
	calls := newAthenaTagCalls("aws", testRegion)
	calls.errs[athenaTagARN("workgroup", "wg-t")] = athenaDenied

	_, err := athenaTagScan(context.Background(), t, st, athenaTagHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, op := range []string{"ListDataCatalogs", "ListCapacityReservations", "ListNamedQueries"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write", op, n)
		}
	}
	if n := calls.count("ListWorkGroups"); n != 1 {
		t.Errorf("ListWorkGroups calls = %d; want 1, no page after the failed write", n)
	}
}

// A listed element without a name is not stored and is warned about once
// under its listing's op; its siblings are stored.
func TestScanAthena_NamelessElementsWarn(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newAthenaTagCalls("aws", testRegion)
	h := athenaTagHandlers(calls)
	h["ListWorkGroups"] = calls.answer("ListWorkGroups", func(any) any {
		return &athena.ListWorkGroupsOutput{WorkGroups: []athenatypes.WorkGroupSummary{{Name: ptrStr("wg-t")}, {}, {}}}
	})
	h["ListDataCatalogs"] = calls.answer("ListDataCatalogs", func(any) any {
		return &athena.ListDataCatalogsOutput{DataCatalogsSummary: []athenatypes.DataCatalogSummary{{CatalogName: ptrStr("cat-t")}, {}}}
	})
	h["ListCapacityReservations"] = calls.answer("ListCapacityReservations", func(any) any {
		return &athena.ListCapacityReservationsOutput{CapacityReservations: []athenatypes.CapacityReservation{{Name: ptrStr("cr-t")}, {}}}
	})
	warns := map[string]*int{}
	for _, op := range []string{athenaListWorkGroupsOp, athenaListDataCatalogsOp, athenaListCapacityReservationsOp} {
		warns[op] = countOpWarnings(st, op)
	}

	total, err := athenaTagScan(context.Background(), t, st, h)
	if err != nil || total != 3 {
		t.Fatalf("scan = %d, %v; want 3, nil", total, err)
	}
	for op, n := range warns {
		if *n != 1 {
			t.Errorf("%s warnings = %d; want 1", op, *n)
		}
	}
	if calls.count("GetWorkGroup") != 1 || calls.count("GetDataCatalog") != 1 {
		t.Errorf("GetWorkGroup, GetDataCatalog calls = %d, %d; want 1, 1", calls.count("GetWorkGroup"), calls.count("GetDataCatalog"))
	}
}

// Once ctx ends no further phase starts, and the stop is reported once,
// naming the phases not run.
func TestScanAthena_ContextEndStopsOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newAthenaTagCalls("aws", testRegion)
	h := athenaTagHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	catalogs := h["ListDataCatalogs"]
	h["ListDataCatalogs"] = func(in any) (any, error) {
		if in.(*athena.ListDataCatalogsInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return catalogs(in)
	}

	_, err := athenaTagScan(ctx, t, st, h)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
	}
	if n := calls.count("ListCapacityReservations"); n != 0 {
		t.Errorf("ListCapacityReservations called %d times after ctx ended", n)
	}
	if n := len(rowsByNativeID(t, st, TypeAthenaDataCatalog)); n != 2 {
		t.Errorf("data catalog rows = %d; want page 1's 2", n)
	}
}

// A region without Athena (NXDOMAIN on the workgroup listing, nothing
// stored) is unavailable, and no other listing is made. An NXDOMAIN after
// rows were stored is a failure of this scan, and the counts are kept.
func TestScanAthena_Unreachable(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "athena.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("endpoint absent", func(t *testing.T) {
		st := newTestStore(t)
		calls := newAthenaTagCalls("aws", testRegion)
		h := athenaTagHandlers(calls)
		h["ListWorkGroups"] = func(any) (any, error) { return nil, nx }
		total, err := athenaTagScan(context.Background(), t, st, h)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 || calls.count("ListDataCatalogs") != 0 || calls.count("ListCapacityReservations") != 0 {
			t.Errorf("total, ListDataCatalogs, ListCapacityReservations calls = %d, %d, %d; want 0, 0, 0",
				total, calls.count("ListDataCatalogs"), calls.count("ListCapacityReservations"))
		}
	})
	t.Run("tag read after rows were listed", func(t *testing.T) {
		st := newTestStore(t)
		calls := newAthenaTagCalls("aws", testRegion)
		calls.errs[athenaTagARN("capacity-reservation", "cr-t")] = nx
		total, err := athenaTagScan(context.Background(), t, st, athenaTagHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
		if total != 7 {
			t.Errorf("total = %d; want 7", total)
		}
	})
}
