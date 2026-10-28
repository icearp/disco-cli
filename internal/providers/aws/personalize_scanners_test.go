package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/personalize"
	pztypes "github.com/aws/aws-sdk-go-v2/service/personalize/types"
	"github.com/icearp/disco-cli/store"
)

var (
	pzDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: personalize:List")
	pzInvalid  = apiErr("InvalidInputException", "bad")
	pzThrottle = apiErr("ThrottlingException", "Rate exceeded")
	pzNotFound = apiErr("ResourceNotFoundException", "gone")
)

// pzListing is one listing of the fixture: its op, the type and ARN kind of
// its rows, whether its rows are tag-read, whether its element carries a
// name and a status, its element for an ARN and a name, and its output for
// one page of elements.
type pzListing struct {
	op, typ, kind     string
	tagged            bool
	hasName, hasState bool
	elem              func(arn, name *string) any
	out               func(next *string, elems ...any) any
}

// pzActive is the status of every fixture element that carries one.
var pzActive = sdkaws.String("ACTIVE")

// pzRecipeKind marks the recipe listing, whose ARNs carry neither region
// nor account.
const pzRecipeKind = "recipe"

func pzElems[T any](elems []any) []T {
	out := make([]T, 0, len(elems))
	for _, e := range elems {
		out = append(out, e.(T))
	}
	return out
}

var pzListings = []pzListing{
	{
		"ListDatasetGroups", TypePersonalizeDatasetGroup, "dataset-group", true, true, true,
		func(arn, name *string) any {
			return pztypes.DatasetGroupSummary{DatasetGroupArn: arn, Name: name, Status: pzActive, Domain: pztypes.DomainEcommerce}
		},
		func(next *string, e ...any) any {
			return &personalize.ListDatasetGroupsOutput{NextToken: next, DatasetGroups: pzElems[pztypes.DatasetGroupSummary](e)}
		},
	},
	{
		"ListDatasets", TypePersonalizeDataset, "dataset", true, true, true,
		func(arn, name *string) any {
			return pztypes.DatasetSummary{DatasetArn: arn, Name: name, Status: pzActive, DatasetType: sdkaws.String("Interactions")}
		},
		func(next *string, e ...any) any {
			return &personalize.ListDatasetsOutput{NextToken: next, Datasets: pzElems[pztypes.DatasetSummary](e)}
		},
	},
	{
		"ListSchemas", TypePersonalizeSchema, "schema", false, true, false,
		func(arn, name *string) any {
			return pztypes.DatasetSchemaSummary{SchemaArn: arn, Name: name, Domain: pztypes.DomainVideoOnDemand}
		},
		func(next *string, e ...any) any {
			return &personalize.ListSchemasOutput{NextToken: next, Schemas: pzElems[pztypes.DatasetSchemaSummary](e)}
		},
	},
	{
		"ListSolutions", TypePersonalizeSolution, "solution", true, true, true,
		func(arn, name *string) any {
			return pztypes.SolutionSummary{SolutionArn: arn, Name: name, Status: pzActive, RecipeArn: sdkaws.String("recipe-ref")}
		},
		func(next *string, e ...any) any {
			return &personalize.ListSolutionsOutput{NextToken: next, Solutions: pzElems[pztypes.SolutionSummary](e)}
		},
	},
	{
		"ListCampaigns", TypePersonalizeCampaign, "campaign", true, true, true,
		func(arn, name *string) any {
			return pztypes.CampaignSummary{CampaignArn: arn, Name: name, Status: pzActive}
		},
		func(next *string, e ...any) any {
			return &personalize.ListCampaignsOutput{NextToken: next, Campaigns: pzElems[pztypes.CampaignSummary](e)}
		},
	},
	{
		"ListEventTrackers", TypePersonalizeEventTracker, "event-tracker", true, true, true,
		func(arn, name *string) any {
			return pztypes.EventTrackerSummary{EventTrackerArn: arn, Name: name, Status: pzActive}
		},
		func(next *string, e ...any) any {
			return &personalize.ListEventTrackersOutput{NextToken: next, EventTrackers: pzElems[pztypes.EventTrackerSummary](e)}
		},
	},
	{
		"ListFilters", TypePersonalizeFilter, "filter", true, true, true,
		func(arn, name *string) any {
			return pztypes.FilterSummary{FilterArn: arn, Name: name, Status: pzActive, DatasetGroupArn: sdkaws.String("dg-ref")}
		},
		func(next *string, e ...any) any {
			return &personalize.ListFiltersOutput{NextToken: next, Filters: pzElems[pztypes.FilterSummary](e)}
		},
	},
	{
		"ListMetricAttributions", TypePersonalizeMetricAttribution, "metric-attribution", false, true, true,
		func(arn, name *string) any {
			return pztypes.MetricAttributionSummary{MetricAttributionArn: arn, Name: name, Status: pzActive}
		},
		func(next *string, e ...any) any {
			return &personalize.ListMetricAttributionsOutput{NextToken: next, MetricAttributions: pzElems[pztypes.MetricAttributionSummary](e)}
		},
	},
	{
		"ListRecommenders", TypePersonalizeRecommender, "recommender", true, true, true,
		func(arn, name *string) any {
			return pztypes.RecommenderSummary{RecommenderArn: arn, Name: name, Status: pzActive, DatasetGroupArn: sdkaws.String("dg-ref")}
		},
		func(next *string, e ...any) any {
			return &personalize.ListRecommendersOutput{NextToken: next, Recommenders: pzElems[pztypes.RecommenderSummary](e)}
		},
	},
	{
		"ListRecipes", TypePersonalizeRecipe, pzRecipeKind, false, true, true,
		func(arn, name *string) any {
			return pztypes.RecipeSummary{RecipeArn: arn, Name: name, Status: pzActive}
		},
		func(next *string, e ...any) any {
			return &personalize.ListRecipesOutput{NextToken: next, Recipes: pzElems[pztypes.RecipeSummary](e)}
		},
	},
	{
		"ListSolutionVersions", TypePersonalizeSolutionVersion, "solution/sol-a", true, false, true,
		func(arn, _ *string) any {
			return pztypes.SolutionVersionSummary{SolutionVersionArn: arn, Status: pzActive, TrainingMode: pztypes.TrainingModeFull}
		},
		func(next *string, e ...any) any {
			return &personalize.ListSolutionVersionsOutput{NextToken: next, SolutionVersions: pzElems[pztypes.SolutionVersionSummary](e)}
		},
	},
}

// pzTestTotal is every listing's two pages of one element each.
const pzTestTotal = 11 * 2

// pzName is the name of fixture element id in l, nil when l's element has
// none.
func pzName(l pzListing, id string) *string {
	if !l.hasName {
		return nil
	}
	return sdkaws.String("name-" + id)
}

func pzARN(kind, id string) string {
	return pzARNIn("aws", testRegion, kind, id)
}

func pzARNIn(partition, region, kind, id string) string {
	if kind == pzRecipeKind {
		return "arn:" + partition + ":personalize:::recipe/aws-" + id
	}
	return "arn:" + partition + ":personalize:" + region + ":" + testAccountID + ":" + kind + "/" + id
}

// pzListingOf is the fixture listing of op.
func pzListingOf(t *testing.T, op string) pzListing {
	t.Helper()
	for _, l := range pzListings {
		if l.op == op {
			return l
		}
	}
	t.Fatalf("%s is not a fixture listing", op)
	return pzListing{}
}

// pzCalls records a scan's calls and answers its tag reads, by ARN, with
// tags or errors.
type pzCalls struct {
	mu         sync.Mutex
	partition  string
	region     string
	maxResults map[string][]int32
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

// newPZCalls tags each tagged listing's first-page element with its type.
func newPZCalls() *pzCalls {
	c := &pzCalls{
		partition: "aws", region: testRegion,
		maxResults: map[string][]int32{}, tags: map[string]map[string]string{}, errs: map[string]error{},
	}
	for _, l := range pzListings {
		if l.tagged {
			c.tags[pzARN(l.kind, "x1")] = map[string]string{"type": l.typ}
		}
	}
	return c
}

func (c *pzCalls) arn(kind, id string) string { return pzARNIn(c.partition, c.region, kind, id) }

func (c *pzCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var n int32
	if p := bacField[*int32](in, "MaxResults"); p != nil {
		n = *p
	}
	c.maxResults[op] = append(c.maxResults[op], n)
}

func (c *pzCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.maxResults[op])
}

func (c *pzCalls) readARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

// pages answers l's listing: the first page holds element x1 and names a
// next page, which holds x2 and ends the listing.
func (c *pzCalls) pages(l pzListing) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(l.op, in)
		first := bacField[*string](in, "NextToken") == nil
		var next *string
		if first {
			next = sdkaws.String("p2")
		}
		id := pick(first, "x1", "x2")
		return l.out(next, l.elem(sdkaws.String(c.arn(l.kind, id)), pzName(l, id))), nil
	}
}

func (c *pzCalls) listTags(in any) (any, error) {
	arn := sv(in.(*personalize.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	var tags []pztypes.Tag
	for k, v := range c.tags[arn] {
		tags = append(tags, pztypes.Tag{TagKey: sdkaws.String(k), TagValue: sdkaws.String(v)})
	}
	return &personalize.ListTagsForResourceOutput{Tags: tags}, nil
}

func pzHandlers(c *pzCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.listTags}
	for _, l := range pzListings {
		h[l.op] = c.pages(l)
	}
	return h
}

// pzScan runs the scan's real entry point against handlers; every op they do
// not answer is denied.
func pzScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, pzDenied)
	return scanPersonalize(ctx, acct, testRegion, st, testScanID)
}

func pzDecode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("attributes %s: %v", s, err)
	}
	return m
}

// pzStoredElem is elem as l's row stores it: verbatim, less the recipe's
// volatile LastUpdatedDateTime, which the store drops.
func pzStoredElem(t *testing.T, l pzListing, elem any) map[string]any {
	t.Helper()
	m := pzDecode(t, mustJSON(elem))
	if l.typ == TypePersonalizeRecipe {
		delete(m, "LastUpdatedDateTime")
	}
	return m
}

func pzWarningOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// Every listing stores both its pages, each row with its listed name and
// status, no CreatedAt, and the listed element verbatim as its attributes.
// A taggable type's row carries the tags its read returns (nil for a
// resource without tags); ListTagsForResource is sent exactly the ARN of
// every taggable row and never a schema, metric attribution or recipe.
// Only recipes are provider-managed. Every listing page is sent MaxResults
// 100. No error, no warning.
func TestScanPersonalize_StoresTagsOfEveryTaggableType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newPZCalls()

	total, inserted, err := pzScan(context.Background(), st, pzHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != pzTestTotal || inserted != pzTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, pzTestTotal, pzTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	var wantARNs []string
	for _, l := range pzListings {
		rows := rowsByNativeID(t, st, l.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %d; want 2", l.typ, len(rows))
		}
		for _, id := range []string{"x1", "x2"} {
			arn := pzARN(l.kind, id)
			if l.tagged {
				wantARNs = append(wantARNs, arn)
			}
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", l.typ, arn)
				continue
			}
			if got, want := sv(r.Name), sv(pzName(l, id)); got != want || (r.Name == nil) != !l.hasName {
				t.Errorf("%s Name = %v; want %q (set: %v)", arn, r.Name, want, l.hasName)
			}
			if wantStatus := pick(l.hasState, "ACTIVE", ""); sv(r.Status) != wantStatus || (r.Status == nil) != !l.hasState {
				t.Errorf("%s Status = %v; want %q", arn, r.Status, wantStatus)
			}
			if sv(r.Region) != testRegion || r.CreatedAt != nil {
				t.Errorf("%s Region, CreatedAt = %q, %v; want %q, nil", arn, sv(r.Region), r.CreatedAt, testRegion)
			}
			if r.ManagedByProvider != (l.typ == TypePersonalizeRecipe) {
				t.Errorf("%s ManagedByProvider = %v", arn, r.ManagedByProvider)
			}
			if got, want := pzDecode(t, r.AttributesJSON), pzStoredElem(t, l, l.elem(sdkaws.String(arn), pzName(l, id))); !reflect.DeepEqual(got, want) {
				t.Errorf("%s attributes = %v\nwant %v", arn, got, want)
			}
			if l.tagged && id == "x1" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": l.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
	}
	slices.Sort(wantARNs)
	if got := calls.readARNs(); !slices.Equal(got, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, wantARNs)
	}
	for _, l := range pzListings {
		want := []int32{100, 100}
		if got := calls.maxResults[l.op]; !slices.Equal(got, want) {
			t.Errorf("%s MaxResults = %v; want %v", l.op, got, want)
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanPersonalize_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := pzScan(context.Background(), st, pzHandlers(newPZCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := pzScan(context.Background(), st, pzHandlers(newPZCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != pzTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, pzTestTotal)
	}
}

// A failed tag read. ResourceNotFoundException, which the op models, drops
// the row silently. A denial keeps the row untagged and warns once under
// the tag op; a throttle keeps it and is transient; any other error keeps it
// and is an error.
func TestScanPersonalize_TagReadFailures(t *testing.T) {
	for _, x := range []struct{ kind, typ string }{
		{"dataset-group", TypePersonalizeDatasetGroup},
		{"event-tracker", TypePersonalizeEventTracker},
		{"solution/sol-a", TypePersonalizeSolutionVersion},
	} {
		for _, c := range []struct {
			name    string
			err     error
			outcome serviceOutcome // -1: no error
			code    string
			lost    int
			warnOp  string
		}{
			{"not found", pzNotFound, -1, "", 1, ""},
			{"denied", pzDenied, -1, "", 0, pzTagsOp},
			{"transient", pzThrottle, outcomeTransient, "ThrottlingException", 0, ""},
			{"other error", pzInvalid, outcomeError, "InvalidInputException", 0, ""},
		} {
			t.Run(x.typ+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newPZCalls()
				arn := pzARN(x.kind, "x1")
				calls.errs[arn] = c.err

				total, _, err := pzScan(context.Background(), st, pzHandlers(calls))
				if c.outcome < 0 {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, c.outcome)
					assertAPICode(t, err, c.code)
				}
				if want := pzTestTotal - c.lost; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				r, ok := rowsByNativeID(t, st, x.typ)[arn]
				if ok != (c.lost == 0) {
					t.Fatalf("row stored = %v; want %v", ok, c.lost == 0)
				}
				if ok && r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				w := warns()
				var want []string
				if c.warnOp != "" {
					want = []string{c.warnOp}
				}
				if got := pzWarningOps(w); !slices.Equal(got, want) {
					t.Fatalf("warnings = %v; want %v", w, want)
				}
				for _, x := range w {
					if x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, arn) {
						t.Errorf("warning Scope %q, message %q; want %s/%s naming %s", x.Scope, x.Message, testAccountID, testRegion, arn)
					}
				}
			})
		}
	}
}

// Denied tag reads in several phases send one warning for the whole scan;
// the rows are kept.
func TestScanPersonalize_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newPZCalls()
	for _, kind := range []string{"dataset", "filter", "solution/sol-a"} {
		calls.errs[pzARN(kind, "x2")] = pzDenied
	}

	total, _, err := pzScan(context.Background(), st, pzHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != pzTestTotal {
		t.Errorf("total = %d; want %d", total, pzTestTotal)
	}
	if got := pzWarningOps(warns()); !slices.Equal(got, []string{pzTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, pzTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanPersonalize_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, pzTagsOp)
	calls := newPZCalls()
	calls.errs[pzARN("campaign", "x1")] = pzThrottle
	calls.errs[pzARN("recommender", "x2")] = pzInvalid

	_, _, err := pzScan(context.Background(), st, pzHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidInputException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", pzTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored.
func TestScanPersonalize_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypePersonalizeSolution)
	calls := newPZCalls()
	calls.errs[pzARN("solution", "x1")] = pzDenied

	total, _, err := pzScan(context.Background(), st, pzHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count("ListSolutions"); n != 1 {
		t.Errorf("ListSolutions called %d times; want 1, none after the failed write", n)
	}
	if n := calls.count("ListCampaigns"); n != 0 {
		t.Errorf("ListCampaigns called %d times; want 0 after the failed write", n)
	}
	if total != 6 {
		t.Errorf("total = %d; want 6 (the dataset groups, datasets and schemas)", total)
	}
}

// A listing's failed later page keeps the page read before it, and its
// error does not stop the later phases.
func TestScanPersonalize_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, op := range []string{"ListDatasetGroups", "ListSchemas", "ListRecipes"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newPZCalls()
			handlers := pzHandlers(calls)
			handlers[op] = failOnCall(2, pzInvalid, handlers[op])
			l := pzListingOf(t, op)

			total, _, err := pzScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidInputException")
			if total != pzTestTotal-1 {
				t.Errorf("total = %d; want %d", total, pzTestTotal-1)
			}
			if _, ok := rowsByNativeID(t, st, l.typ)[pzARN(l.kind, "x1")]; !ok {
				t.Errorf("%s row from the page before the failure missing", l.typ)
			}
			if got := len(rowsByNativeID(t, st, TypePersonalizeSolutionVersion)); got != 2 {
				t.Errorf("solution versions (the last phase) = %d; want 2", got)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanPersonalize_RepeatedTokenKeepsPages(t *testing.T) {
	for _, op := range []string{"ListEventTrackers", "ListMetricAttributions"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			handlers := pzHandlers(newPZCalls())
			l := pzListingOf(t, op)
			handlers[op] = func(in any) (any, error) {
				id := pick(bacField[*string](in, "NextToken") == nil, "x1", "x2")
				return l.out(sdkaws.String("same"), l.elem(sdkaws.String(pzARN(l.kind, id)), pzName(l, id))), nil
			}

			total, _, err := pzScan(context.Background(), st, handlers)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != pzTestTotal {
				t.Errorf("total = %d; want %d", total, pzTestTotal)
			}
		})
	}
}

// An element without an ARN is not stored, is not sent to the tag op, and
// is warned about once under its listing, keyed by its name or by the
// fallback text when it has none.
func TestScanPersonalize_ElementWithoutARN(t *testing.T) {
	for _, c := range []struct{ op, key string }{
		{"ListFilters", "name-orphan"},
		{"ListMetricAttributions", "name-orphan"},
		{"ListSolutionVersions", "(an element with no name)"},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newPZCalls()
			handlers := pzHandlers(calls)
			l := pzListingOf(t, c.op)
			handlers[c.op] = func(in any) (any, error) {
				calls.record(c.op, in)
				return l.out(nil, l.elem(nil, pzName(l, "orphan"))), nil
			}

			total, _, err := pzScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != pzTestTotal-2 {
				t.Errorf("total = %d; want %d", total, pzTestTotal-2)
			}
			w := warns()
			if len(w) != 1 || w[0].Service != "personalize:"+c.op || !strings.Contains(w[0].Message, c.key) {
				t.Fatalf("warnings = %v; want one under personalize:%s naming %s", w, c.op, c.key)
			}
			if got, want := calls.readARNs(), pzTaggedARNsExcept(calls, c.op); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
		})
	}
}

// pzTaggedARNsExcept is the sorted ARNs of every tagged listing's fixture
// rows but op's.
func pzTaggedARNsExcept(c *pzCalls, op string) []string {
	var arns []string
	for _, l := range pzListings {
		if l.tagged && l.op != op {
			arns = append(arns, c.arn(l.kind, "x1"), c.arn(l.kind, "x2"))
		}
	}
	slices.Sort(arns)
	return arns
}

// A listing that returns no elements stores no rows of its type and sends
// nothing to the tag op, without an error or a warning; every other listing
// is stored whole.
func TestScanPersonalize_EmptyListing(t *testing.T) {
	for _, op := range []string{"ListSolutionVersions", "ListRecipes"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newPZCalls()
			handlers := pzHandlers(calls)
			l := pzListingOf(t, op)
			handlers[op] = func(in any) (any, error) {
				calls.record(op, in)
				return l.out(nil), nil
			}

			total, inserted, err := pzScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != pzTestTotal-2 || inserted != pzTestTotal-2 {
				t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, pzTestTotal-2, pzTestTotal-2)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			for _, o := range pzListings {
				want := 2
				if o.op == op {
					want = 0
				}
				if got := len(rowsByNativeID(t, st, o.typ)); got != want {
					t.Errorf("%s rows = %d; want %d", o.typ, got, want)
				}
			}
			if got, want := calls.readARNs(), pzTaggedARNsExcept(calls, op); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
		})
	}
}

// A GovCloud or China account's listed ARNs reach the tag op unchanged.
func TestScanPersonalize_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newPZCalls()
			calls.partition, calls.region = pf.partition, pf.region
			var want []string
			for _, l := range pzListings {
				if l.tagged {
					want = append(want, calls.arn(l.kind, "x1"), calls.arn(l.kind, "x2"))
				}
			}
			slices.Sort(want)
			ev := partitionARN(pf.partition, "personalize", pf.region, "event-tracker/x1")
			calls.tags[ev] = map[string]string{"env": "prod"}
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(pzHandlers(calls), pzDenied)

			if _, _, err := scanPersonalize(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := calls.readARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypePersonalizeEventTracker)[ev].TagsJSON, map[string]string{"env": "prod"})
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanPersonalize_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newPZCalls()
	handlers := pzHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListCampaigns"]
	handlers["ListCampaigns"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := pzScan(ctx, st, handlers)
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
	if n := calls.count("ListEventTrackers"); n != 0 {
		t.Errorf("ListEventTrackers called %d times after ctx ended", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanPersonalize_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "personalize.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := pzHandlers(newPZCalls())
		handlers["ListFilters"] = func(any) (any, error) { return nil, nx }
		total, _, err := pzScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != pzTestTotal-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, pzTestTotal-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newPZCalls()
		calls.errs[pzARN("filter", "x2")] = nx
		_, _, err := pzScan(context.Background(), st, pzHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanPersonalize(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

// A listing denied on its first page warns once under its op and stores
// nothing; every other listing is stored.
func TestScanPersonalize_FirstPageDenial(t *testing.T) {
	for _, l := range pzListings {
		t.Run(l.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			handlers := pzHandlers(newPZCalls())
			handlers[l.op] = func(any) (any, error) { return nil, pzDenied }

			total, _, err := pzScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != pzTestTotal-2 {
				t.Errorf("total = %d; want %d", total, pzTestTotal-2)
			}
			for _, o := range pzListings {
				want := 2
				if o.op == l.op {
					want = 0
				}
				if got := len(rowsByNativeID(t, st, o.typ)); got != want {
					t.Errorf("%s rows = %d; want %d", o.typ, got, want)
				}
			}
			w := warns()
			if got := pzWarningOps(w); !slices.Equal(got, []string{"personalize:" + l.op}) {
				t.Fatalf("warnings = %v; want one under personalize:%s", w, l.op)
			}
			if w[0].Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning Scope = %q; want %s/%s", w[0].Scope, testAccountID, testRegion)
			}
		})
	}
}
