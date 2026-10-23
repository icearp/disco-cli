package aws

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/databrew"
	databrewtypes "github.com/aws/aws-sdk-go-v2/service/databrew/types"
	"github.com/icearp/disco-cli/store"
)

var dbTestTags = map[string]string{"env": "prod"}

// dbElem is one listed DataBrew element; a nil arn is an element without a
// ResourceArn.
type dbElem struct {
	arn, name *string
	tags      map[string]string
}

// dbPage is one page of a listing: its elements and next as its NextToken
// ("" for the last page), or err instead.
type dbPage struct {
	elems []dbElem
	next  string
	err   error
}

// dbPhase is one DataBrew listing: the SDK op the stub answers, the op label
// the scanner warns under, the type it stores, how to build one page of its
// output, and its phase function.
type dbPhase struct {
	sdkOp, label, typ string
	output            func(elems []dbElem, next *string) any
	scan              func(context.Context, databrewAPI, *account, string, *store.Store, string) (int, int, error)
}

var dbPhases = []dbPhase{
	{"ListDatasets", dataBrewListDatasetsOp, TypeDataBrewDataset, func(es []dbElem, next *string) any {
		out := &databrew.ListDatasetsOutput{NextToken: next}
		for _, e := range es {
			out.Datasets = append(out.Datasets, databrewtypes.Dataset{ResourceArn: e.arn, Name: e.name, Tags: e.tags})
		}
		return out
	}, scanDataBrewDatasets},
	{"ListJobs", dataBrewListJobsOp, TypeDataBrewJob, func(es []dbElem, next *string) any {
		out := &databrew.ListJobsOutput{NextToken: next}
		for _, e := range es {
			out.Jobs = append(out.Jobs, databrewtypes.Job{ResourceArn: e.arn, Name: e.name, Tags: e.tags})
		}
		return out
	}, scanDataBrewJobs},
	{"ListProjects", dataBrewListProjectsOp, TypeDataBrewProject, func(es []dbElem, next *string) any {
		out := &databrew.ListProjectsOutput{NextToken: next}
		for _, e := range es {
			out.Projects = append(out.Projects, databrewtypes.Project{ResourceArn: e.arn, Name: e.name, Tags: e.tags})
		}
		return out
	}, scanDataBrewProjects},
	{"ListRecipes", dataBrewListRecipesOp, TypeDataBrewRecipe, func(es []dbElem, next *string) any {
		out := &databrew.ListRecipesOutput{NextToken: next}
		for _, e := range es {
			out.Recipes = append(out.Recipes, databrewtypes.Recipe{ResourceArn: e.arn, Name: e.name, Tags: e.tags})
		}
		return out
	}, scanDataBrewRecipes},
	{"ListRulesets", dataBrewListRulesetsOp, TypeDataBrewRuleset, func(es []dbElem, next *string) any {
		out := &databrew.ListRulesetsOutput{NextToken: next}
		for _, e := range es {
			out.Rulesets = append(out.Rulesets, databrewtypes.RulesetItem{ResourceArn: e.arn, Name: e.name, Tags: e.tags})
		}
		return out
	}, scanDataBrewRulesets},
	{"ListSchedules", dataBrewListSchedulesOp, TypeDataBrewSchedule, func(es []dbElem, next *string) any {
		out := &databrew.ListSchedulesOutput{NextToken: next}
		for _, e := range es {
			out.Schedules = append(out.Schedules, databrewtypes.Schedule{ResourceArn: e.arn, Name: e.name, Tags: e.tags})
		}
		return out
	}, scanDataBrewSchedules},
}

// dbCheckInput fails the test unless the request asks for MaxResults 100 (the
// model's cap) and leaves every filter (DatasetName, ProjectName,
// RecipeVersion, TargetArn, JobName) unset, since a filter would narrow the
// listing. NextToken is checked by the page lookup.
func dbCheckInput(t *testing.T, op string, in any) {
	req := reflect.ValueOf(in).Elem()
	for i := range req.NumField() {
		name, f := req.Type().Field(i).Name, req.Field(i)
		switch {
		case name == "NextToken" || f.Kind() != reflect.Pointer:
		case name == "MaxResults":
			if f.IsNil() || f.Elem().Int() != 100 {
				t.Errorf("%s MaxResults = %v, want 100", op, f.Interface())
			}
		case !f.IsNil():
			t.Errorf("%s %s = %v, want unset", op, name, f.Elem().Interface())
		}
	}
}

// dbHandler serves p's pages: a call without NextToken gets page 0 and one
// with "t<i>" page i (nmPageIndex), so a token not forwarded fails the test.
func (p dbPhase) dbHandler(t *testing.T, pages []dbPage) func(in any) (any, error) {
	return func(in any) (any, error) {
		dbCheckInput(t, p.sdkOp, in)
		token := reflect.ValueOf(in).Elem().FieldByName("NextToken").Interface().(*string)
		idx, err := nmPageIndex(t, p.sdkOp, sv(token), len(pages))
		if err != nil {
			return nil, err
		}
		pg := pages[idx]
		if pg.err != nil {
			return nil, pg.err
		}
		var next *string
		if pg.next != "" {
			next = ptrStr(pg.next)
		}
		return p.output(pg.elems, next), nil
	}
}

func dbARN(typ, n string) string { return "arn:" + typ + "/" + n }

// dbTwoPages serves p as two pages: "<typ>/1", named "name-1" and tagged, then
// "<typ>/2", named "name-2" and untagged.
func (p dbPhase) dbTwoPages(t *testing.T) func(in any) (any, error) {
	return p.dbHandler(t, []dbPage{
		{elems: []dbElem{{ptrStr(dbARN(p.typ, "1")), ptrStr("name-1"), dbTestTags}}, next: "t1"},
		{elems: []dbElem{{ptrStr(dbARN(p.typ, "2")), ptrStr("name-2"), nil}}},
	})
}

func dbAllHandlers(t *testing.T) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){}
	for _, p := range dbPhases {
		h[p.sdkOp] = p.dbTwoPages(t)
	}
	return h
}

func dbScanAll(t *testing.T, st *store.Store, h map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	client := databrew.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
	total, _, err := scanDataBrewAll(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

// TestScanDataBrewAll_StoresRows pins, for every type, that both pages are
// stored under the element's ResourceArn with its Name and Region, that the
// tagged element's TagsJSON is its tags and the untagged one's is nil.
func TestScanDataBrewAll_StoresRows(t *testing.T) {
	st := newTestStore(t)
	total, err := dbScanAll(t, st, dbAllHandlers(t))
	if err != nil {
		t.Fatalf("scanDataBrewAll: %v", err)
	}
	if total != 12 {
		t.Errorf("total = %d, want 12", total)
	}
	for _, p := range dbPhases {
		t.Run(p.typ, func(t *testing.T) {
			rows := rowsByNativeID(t, st, p.typ)
			if len(rows) != 2 {
				t.Errorf("stored %d rows, want 2", len(rows))
			}
			for _, w := range []struct {
				n    string
				tags map[string]string
			}{{"1", dbTestTags}, {"2", nil}} {
				arn := dbARN(p.typ, w.n)
				r, ok := rows[arn]
				if !ok {
					t.Fatalf("%s not stored", arn)
				}
				if got := sv(r.Name); got != "name-"+w.n {
					t.Errorf("%s Name = %q, want %q", arn, got, "name-"+w.n)
				}
				var attrs struct{ Name, ResourceArn string }
				if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
					t.Fatalf("%s attributes %q: %v", arn, r.AttributesJSON, err)
				}
				if attrs.Name != "name-"+w.n || attrs.ResourceArn != arn {
					t.Errorf("%s attributes Name, ResourceArn = %q, %q; want the listed element's", arn, attrs.Name, attrs.ResourceArn)
				}
				if got := sv(r.Region); got != testRegion {
					t.Errorf("%s Region = %q, want %q", arn, got, testRegion)
				}
				if w.tags == nil {
					if r.TagsJSON != nil {
						t.Errorf("%s TagsJSON = %s, want nil", arn, *r.TagsJSON)
					}
					continue
				}
				assertTagsMap(t, r.TagsJSON, w.tags)
			}
		})
	}
}

// TestScanDataBrewPhases_ListOutcomes pins each listing's error handling:
// rows read before a failed page are kept, a denial warns with the
// account/region scope instead of failing, and an element without a
// ResourceArn is warned about while its siblings are stored.
func TestScanDataBrewPhases_ListOutcomes(t *testing.T) {
	bad := apiErr("ValidationException", "bad")
	denied := apiErr("AccessDeniedException", "not authorized to perform: databrew:List")
	one := func(n string) []dbElem { return []dbElem{{arn: ptrStr("arn:" + n), name: ptrStr(n)}} }
	for _, p := range dbPhases {
		for _, c := range []struct {
			name      string
			pages     []dbPage
			wantErr   error
			wantARNs  []string
			wantWarns int
		}{
			{"later page fails keeps rows read", []dbPage{{elems: one("a1"), next: "t1"}, {err: bad}}, bad, []string{"arn:a1"}, 0},
			{"repeated token keeps rows read", []dbPage{{elems: one("a1"), next: "t1"}, {elems: one("a2"), next: "t1"}}, errRepeatedNextToken, []string{"arn:a1", "arn:a2"}, 0},
			{"denied warns", []dbPage{{err: denied}}, nil, nil, 1},
			{"denied after a page keeps rows and warns", []dbPage{{elems: one("a1"), next: "t1"}, {err: denied}}, nil, []string{"arn:a1"}, 1},
			{"empty ARN warns", []dbPage{{elems: append([]dbElem{{name: ptrStr("no-arn")}}, one("a1")...)}}, nil, []string{"arn:a1"}, 1},
			{"empty ARN and Name warns with a key", []dbPage{{elems: append([]dbElem{{}}, one("a1")...)}}, nil, []string{"arn:a1"}, 1},
		} {
			t.Run(p.sdkOp+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				var scopes []string
				st.OnWarn = func(w store.ScanWarning) {
					if w.Service == p.label {
						scopes = append(scopes, w.Scope)
						if strings.HasSuffix(w.Message, "first ") {
							t.Errorf("warning %q names no first element", w.Message)
						}
					}
				}
				client := databrew.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){p.sdkOp: p.dbHandler(t, c.pages)}, errors.New("unexpected op")))
				total, _, err := p.scan(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
				if c.wantErr == nil && err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if c.wantErr != nil && !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want %v", err, c.wantErr)
				}
				if total != len(c.wantARNs) {
					t.Errorf("total = %d, want %d", total, len(c.wantARNs))
				}
				rows := rowsByNativeID(t, st, p.typ)
				if len(rows) != len(c.wantARNs) {
					t.Errorf("stored %d rows, want %v", len(rows), c.wantARNs)
				}
				for _, a := range c.wantARNs {
					if _, ok := rows[a]; !ok {
						t.Errorf("%s not stored", a)
					}
				}
				if len(scopes) != c.wantWarns {
					t.Errorf("warnings = %d, want %d", len(scopes), c.wantWarns)
				}
				for _, s := range scopes {
					if want := testAccountID + "/" + testRegion; s != want {
						t.Errorf("warning scope = %q, want %q", s, want)
					}
				}
			})
		}
	}
}

// TestScanDataBrewDatasets_WriteFailsWarnsNothing pins that a failed write
// returns ErrStoreWrite and reports no empty-ARN warning, since none could
// say the rows read were stored.
func TestScanDataBrewDatasets_WriteFailsWarnsNothing(t *testing.T) {
	p := dbPhases[0]
	st, closeStore := openClosingStore(t)
	warns := countOpWarnings(st, p.label)
	serve := p.dbHandler(t, []dbPage{{elems: []dbElem{{name: ptrStr("no-arn")}, {arn: ptrStr("arn:a1")}}}})
	h := map[string]func(in any) (any, error){p.sdkOp: func(in any) (any, error) {
		closeStore()
		return serve(in)
	}}
	client := databrew.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
	_, _, err := p.scan(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d, want 0", *warns)
	}
}

// TestScanDataBrewAll_PhaseErrorRunsOtherPhases pins that a failed listing
// is returned without stopping the other listings, whichever phase fails.
func TestScanDataBrewAll_PhaseErrorRunsOtherPhases(t *testing.T) {
	bad := apiErr("ValidationException", "bad")
	for _, failed := range dbPhases {
		t.Run(failed.sdkOp, func(t *testing.T) {
			h := dbAllHandlers(t)
			h[failed.sdkOp] = func(any) (any, error) { return nil, bad }
			st := newTestStore(t)
			total, err := dbScanAll(t, st, h)
			if !errors.Is(err, bad) {
				t.Fatalf("err = %v, want the %s error", err, failed.sdkOp)
			}
			if total != 10 {
				t.Errorf("total = %d, want 10", total)
			}
			for _, p := range dbPhases {
				if p.typ != failed.typ && len(rowsByNativeID(t, st, p.typ)) != 2 {
					t.Errorf("%s not stored after %s failed", p.typ, failed.sdkOp)
				}
			}
		})
	}
}

// TestScanDataBrewAll_StoreWriteStops pins that a failed write stops the
// scan: no listing after it calls AWS.
func TestScanDataBrewAll_StoreWriteStops(t *testing.T) {
	calls := map[string]int{}
	h := dbAllHandlers(t)
	for op, f := range h {
		h[op] = func(in any) (any, error) {
			calls[op]++
			return f(in)
		}
	}
	st, closeStore := openClosingStore(t)
	serve := h["ListDatasets"]
	h["ListDatasets"] = func(in any) (any, error) {
		closeStore()
		return serve(in)
	}
	if _, err := dbScanAll(t, st, h); !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want ErrStoreWrite", err)
	}
	for _, p := range dbPhases[1:] {
		if calls[p.sdkOp] != 0 {
			t.Errorf("%s called %d times after the failed write, want 0", p.sdkOp, calls[p.sdkOp])
		}
	}
}

// TestScanDataBrewAll_TransientBesideHardError pins, per phase, that a
// throttled listing beside a hard-failing one is demoted to one warning under
// that listing's op label and the scan classified as an error.
func TestScanDataBrewAll_TransientBesideHardError(t *testing.T) {
	for i, throttled := range dbPhases {
		hard := dbPhases[(i+1)%len(dbPhases)]
		t.Run(throttled.sdkOp, func(t *testing.T) {
			h := dbAllHandlers(t)
			h[throttled.sdkOp] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
			h[hard.sdkOp] = func(any) (any, error) { return nil, apiErr("ValidationException", "bad") }
			st := newTestStore(t)
			warns := countOpWarnings(st, throttled.label)
			_, err := dbScanAll(t, st, h)
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			if *warns != 1 {
				t.Errorf("transient warnings under %s = %d, want 1", throttled.label, *warns)
			}
		})
	}
}
