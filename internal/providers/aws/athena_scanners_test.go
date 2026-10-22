package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync/atomic"
	"testing"
	"time"

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
	total, _, err := scanAthena(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanAthena_StoresNotebooksOfSparkWorkGroups(t *testing.T) {
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

// A hard error in an older phase still lets the notebook phase run, over the
// Spark workgroups stored before the failure, and is returned.
func TestScanAthena_OlderPhaseErrorStillRunsNotebooks(t *testing.T) {
	st := newTestStore(t)
	var nbCalls parentCalls
	h := athenaTopLevelHandlers(&nbCalls)
	h["ListDataCatalogs"] = func(any) (any, error) { return nil, athenaBadInput }
	total, err := scanAthenaTopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	if got := nativeIDs(rowsByNativeID(t, st, TypeAthenaNotebook)); !slices.Equal(got, []string{athenaTestNB1, athenaTestNB2, athenaTestNB3}) {
		t.Errorf("notebook rows = %v, want all three", got)
	}
	// 3 workgroups + 3 notebooks; the phases after the failed catalogs store none.
	if total != 6 {
		t.Errorf("total = %d, want 6", total)
	}
}

// A transient error in an older phase is demoted to a warning so that the
// notebook phase's hard error still reaches the dispatcher as a hard error,
// not hidden inside a join the dispatcher would read as transient.
func TestScanAthena_OlderPhaseTransientDoesNotMaskHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "athena")
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
		t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
	}
}

func TestIsAthenaSparkWorkGroup_EngineVersion(t *testing.T) {
	for _, tc := range []struct {
		name string
		wg   *athenatypes.WorkGroup
		want bool
	}{
		{"PySpark engine", athenaEngine("s", "PySpark engine version 3"), true},
		{"Athena SQL engine", athenaEngine("q", "Athena engine version 3"), false},
		{"no configuration", &athenatypes.WorkGroup{Name: ptrStr("n")}, false},
		{"no engine version", &athenatypes.WorkGroup{Configuration: &athenatypes.WorkGroupConfiguration{}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAthenaSparkWorkGroup(tc.wg); got != tc.want {
				t.Errorf("isAthenaSparkWorkGroup = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestListAthenaNotebooks_FailsOnRepeatedToken(t *testing.T) {
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
