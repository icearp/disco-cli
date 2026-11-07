package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/entityresolution"
	ertypes "github.com/aws/aws-sdk-go-v2/service/entityresolution/types"
	"github.com/icearp/disco-cli/store"
)

var (
	erTestIMWA = fmt.Sprintf("arn:aws:entityresolution:%s:%s:idmappingworkflow/imw-a", testRegion, testAccountID)
	erTestIMWB = fmt.Sprintf("arn:aws:entityresolution:%s:%s:idmappingworkflow/imw-b", testRegion, testAccountID)
	erTestNSA  = fmt.Sprintf("arn:aws:entityresolution:%s:%s:idnamespace/ns-a", testRegion, testAccountID)
	erTestNSB  = fmt.Sprintf("arn:aws:entityresolution:%s:%s:idnamespace/ns-b", testRegion, testAccountID)
	erTestMWA  = fmt.Sprintf("arn:aws:entityresolution:%s:%s:matchingworkflow/mw-a", testRegion, testAccountID)
	erTestMWB  = fmt.Sprintf("arn:aws:entityresolution:%s:%s:matchingworkflow/mw-b", testRegion, testAccountID)
	erTestSMA  = fmt.Sprintf("arn:aws:entityresolution:%s:%s:schemamapping/sm-a", testRegion, testAccountID)
	erTestSMB  = fmt.Sprintf("arn:aws:entityresolution:%s:%s:schemamapping/sm-b", testRegion, testAccountID)
	erTestRole = fmt.Sprintf("arn:aws:iam::%s:role/er-match", testAccountID)

	erTestCreated = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	erDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: entityresolution:List")
	erThrottle   = apiErr("ThrottlingException", "slow down")
	erValidation = apiErr("ValidationException", "bad input")
	erBadRequest = apiErr("InvalidRequestException", "bad request")
	erGone       = apiErr("ResourceNotFoundException", "gone")

	// erTestTags are the tags ListTagsForResource and GetMatchingWorkflow
	// return; every other resource has none.
	erTestTags = map[string]map[string]string{
		erTestIMWA: {"env": "prod"},
		erTestNSA:  {"team": "data"},
		erTestMWA:  {"owner": "ml"},
		erTestSMA:  {"app": "crm", "cost": "7"},
	}

	// erTestPolicies are the resources GetPolicy returns a policy for. It
	// answers ns-a with ResourceNotFoundException and every other resource
	// with no policy.
	erTestPolicies = map[string]string{
		erTestIMWA: `{"Statement":[{"Sid":"imw"}]}`,
		erTestMWA:  `{"Statement":[{"Sid":"mw"}]}`,
		erTestSMB:  `{"Statement":[{"Sid":"sm"}]}`,
	}

	erTestMWByName = map[string]string{"mw-a": erTestMWA, "mw-b": erTestMWB}
)

// erTestTotal is every row erHandlers yields: 2 ID mapping workflows, 2 ID
// namespaces, 2 matching workflows, 2 schema mappings and 3 policies.
const erTestTotal = 11

// erCalls records the calls erHandlers answers, and holds the tags and the
// errors its ops return per key on top of the fixtures.
type erCalls struct {
	// tagARNs is every ARN ListTagsForResource was sent, getNames every name
	// GetMatchingWorkflow was sent, policyARNs every ARN GetPolicy was sent,
	// lists every listing op called.
	tagARNs, getNames, policyARNs, lists parentCalls
	tags                                 map[string]map[string]string
	// tagErrs and policyErrs are keyed by ARN, getErrs by workflow name.
	tagErrs, getErrs, policyErrs map[string]error

	mu sync.Mutex
	// maxResults is the MaxResults of every listing call, per op.
	maxResults map[string][]int32
}

// listed records a listing call of op and reports whether it asks for the
// first page.
func (c *erCalls) listed(op string, in any) bool {
	c.lists.add(op)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxResults == nil {
		c.maxResults = map[string][]int32{}
	}
	c.maxResults[op] = append(c.maxResults[op], sdkaws.ToInt32(bacField[*int32](in, "MaxResults")))
	return bacField[*string](in, "NextToken") == nil
}

// tagsOf is arn's tags: c.tags, else erTestTags.
func (c *erCalls) tagsOf(arn string) map[string]string {
	if t, ok := c.tags[arn]; ok {
		return t
	}
	return erTestTags[arn]
}

// erHandlers answers a region with A of each listing on page 1 and B on page
// 2. The page-1 elements carry CreatedAt, the page-2 ones none.
func erHandlers(calls *erCalls) map[string]func(in any) (any, error) {
	page2 := "page-2"
	return map[string]func(in any) (any, error){
		"ListIdMappingWorkflows": func(in any) (any, error) {
			if calls.listed("ListIdMappingWorkflows", in) {
				return &entityresolution.ListIdMappingWorkflowsOutput{
					WorkflowSummaries: []ertypes.IdMappingWorkflowSummary{{WorkflowArn: &erTestIMWA, WorkflowName: sdkaws.String("imw-a"), CreatedAt: &erTestCreated, UpdatedAt: &erTestCreated}},
					NextToken:         &page2,
				}, nil
			}
			return &entityresolution.ListIdMappingWorkflowsOutput{WorkflowSummaries: []ertypes.IdMappingWorkflowSummary{{WorkflowArn: &erTestIMWB, WorkflowName: sdkaws.String("imw-b")}}}, nil
		},
		"ListIdNamespaces": func(in any) (any, error) {
			if calls.listed("ListIdNamespaces", in) {
				return &entityresolution.ListIdNamespacesOutput{
					IdNamespaceSummaries: []ertypes.IdNamespaceSummary{{IdNamespaceArn: &erTestNSA, IdNamespaceName: sdkaws.String("ns-a"), Type: ertypes.IdNamespaceTypeSource, Description: sdkaws.String("source ids"), CreatedAt: &erTestCreated}},
					NextToken:            &page2,
				}, nil
			}
			return &entityresolution.ListIdNamespacesOutput{IdNamespaceSummaries: []ertypes.IdNamespaceSummary{{IdNamespaceArn: &erTestNSB, IdNamespaceName: sdkaws.String("ns-b"), Type: ertypes.IdNamespaceTypeTarget}}}, nil
		},
		"ListMatchingWorkflows": func(in any) (any, error) {
			if calls.listed("ListMatchingWorkflows", in) {
				return &entityresolution.ListMatchingWorkflowsOutput{
					WorkflowSummaries: []ertypes.MatchingWorkflowSummary{{WorkflowArn: &erTestMWA, WorkflowName: sdkaws.String("mw-a"), ResolutionType: ertypes.ResolutionTypeRuleMatching, CreatedAt: &erTestCreated}},
					NextToken:         &page2,
				}, nil
			}
			return &entityresolution.ListMatchingWorkflowsOutput{WorkflowSummaries: []ertypes.MatchingWorkflowSummary{{WorkflowArn: &erTestMWB, WorkflowName: sdkaws.String("mw-b"), ResolutionType: ertypes.ResolutionTypeMlMatching}}}, nil
		},
		"GetMatchingWorkflow": func(in any) (any, error) {
			name := sv(in.(*entityresolution.GetMatchingWorkflowInput).WorkflowName)
			calls.getNames.add(name)
			if err, ok := calls.getErrs[name]; ok {
				return nil, err
			}
			arn := erTestMWByName[name]
			return &entityresolution.GetMatchingWorkflowOutput{
				WorkflowName: &name, WorkflowArn: &arn, RoleArn: &erTestRole,
				Description: sdkaws.String("matches " + name), Tags: calls.tagsOf(arn),
			}, nil
		},
		"ListSchemaMappings": func(in any) (any, error) {
			if calls.listed("ListSchemaMappings", in) {
				return &entityresolution.ListSchemaMappingsOutput{
					SchemaList: []ertypes.SchemaMappingSummary{{SchemaArn: &erTestSMA, SchemaName: sdkaws.String("sm-a"), HasWorkflows: sdkaws.Bool(true), CreatedAt: &erTestCreated}},
					NextToken:  &page2,
				}, nil
			}
			return &entityresolution.ListSchemaMappingsOutput{SchemaList: []ertypes.SchemaMappingSummary{{SchemaArn: &erTestSMB, SchemaName: sdkaws.String("sm-b"), HasWorkflows: sdkaws.Bool(false)}}}, nil
		},
		"GetPolicy": func(in any) (any, error) {
			arn := sv(in.(*entityresolution.GetPolicyInput).Arn)
			calls.policyARNs.add(arn)
			if err, ok := calls.policyErrs[arn]; ok {
				return nil, err
			}
			if arn == erTestNSA {
				return nil, erGone
			}
			out := &entityresolution.GetPolicyOutput{Arn: &arn, Token: sdkaws.String("tok-1")}
			if p, ok := erTestPolicies[arn]; ok {
				out.Policy = &p
			}
			return out, nil
		},
		"ListTagsForResource": func(in any) (any, error) {
			arn := sv(in.(*entityresolution.ListTagsForResourceInput).ResourceArn)
			calls.tagARNs.add(arn)
			if err, ok := calls.tagErrs[arn]; ok {
				return nil, err
			}
			return &entityresolution.ListTagsForResourceOutput{Tags: calls.tagsOf(arn)}, nil
		},
	}
}

// erScan runs the service scan entry point against handlers; an op without a
// handler is denied.
func erScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, erDenied)
	return scanEntityResolution(ctx, acct, testRegion, st, testScanID)
}

func erSorted(ids ...string) []string {
	sort.Strings(ids)
	return ids
}

// erListedTypes are the four listed types, whose rows are the policy reads'
// parents.
var erListedTypes = []string{TypeEntityResolutionIDMappingWorkflow, TypeEntityResolutionIDNamespace, TypeEntityResolutionMatchingWorkflow, TypeEntityResolutionSchemaMapping}

// erAllListed is every listed resource erHandlers yields, sorted.
func erAllListed() []string {
	return erSorted(erTestIMWA, erTestIMWB, erTestNSA, erTestNSB, erTestMWA, erTestMWB, erTestSMA, erTestSMB)
}

// erListTagged is the ARN of every row whose tags ListTagsForResource reads
// when every GetMatchingWorkflow succeeds: all but the matching workflows.
func erListTagged() []string {
	return erSorted(erTestIMWA, erTestIMWB, erTestNSA, erTestNSB, erTestSMA, erTestSMB)
}

// erPolicyIDs is the policy NativeID of each parent, sorted.
func erPolicyIDs(parents ...string) []string {
	ids := make([]string, len(parents))
	for i, p := range parents {
		ids[i] = p + "/policy"
	}
	return erSorted(ids...)
}

// erStoredIDs is the NativeID of every stored row of typs, sorted.
func erStoredIDs(t *testing.T, st *store.Store, typs ...string) []string {
	t.Helper()
	var ids []string
	for _, typ := range typs {
		for id := range rowsByNativeID(t, st, typ) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func erListedStored(t *testing.T, st *store.Store) []string {
	t.Helper()
	return erStoredIDs(t, st, erListedTypes...)
}

func erPoliciesStored(t *testing.T, st *store.Store) []string {
	t.Helper()
	return erStoredIDs(t, st, TypeEntityResolutionPolicyStatement)
}

// erPolicyOf decodes a policy row's attributes.
func erPolicyOf(a string) (entityresolution.GetPolicyOutput, bool) {
	var p entityresolution.GetPolicyOutput
	return p, json.Unmarshal([]byte(a), &p) == nil
}

// Every listed resource is stored from both its pages under its listed ARN,
// named by its name, with the listed CreatedAt (none on page 2), its
// attributes the listed element, except a matching workflow's, which are its
// GetMatchingWorkflow body; each has the tags its tag source returns (nil for
// none). ListTagsForResource is sent exactly the ARN of each ID mapping
// workflow, ID namespace and schema mapping, once, and never a matching
// workflow's, whose tags come from GetMatchingWorkflow (sent each name). Each
// stored resource has its policy read by ARN; those with one are stored
// under {ARN}/policy, named "policy". Every listing sends MaxResults 25. No
// error, no warning.
func TestScanEntityResolution_StoresEveryRow(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls erCalls
	total, inserted, err := erScan(context.Background(), st, erHandlers(&calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != erTestTotal || inserted != erTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, erTestTotal, erTestTotal)
	}
	if got := calls.tagARNs.distinct(); !slices.Equal(got, erListTagged()) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, erListTagged())
	}
	if n := len(calls.tagARNs.ids); n != len(erListTagged()) {
		t.Errorf("ListTagsForResource calls = %d; want one per row, %d", n, len(erListTagged()))
	}
	if got, want := calls.getNames.distinct(), []string{"mw-a", "mw-b"}; !slices.Equal(got, want) || len(calls.getNames.ids) != len(want) {
		t.Errorf("GetMatchingWorkflow names = %v; want each of %v once", calls.getNames.ids, want)
	}
	if got := calls.policyARNs.distinct(); !slices.Equal(got, erAllListed()) || len(calls.policyARNs.ids) != len(got) {
		t.Errorf("GetPolicy ARNs = %v; want each of %v once", calls.policyARNs.ids, erAllListed())
	}
	for _, op := range []string{"ListIdMappingWorkflows", "ListIdNamespaces", "ListMatchingWorkflows", "ListSchemaMappings"} {
		if got, want := calls.maxResults[op], []int32{25, 25}; !slices.Equal(got, want) {
			t.Errorf("%s MaxResults = %v; want %v (two pages)", op, got, want)
		}
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}

	created := "2026-03-04T05:06:07Z"
	for _, c := range []struct {
		typ, id, name, created string // created "": none
		tags                   map[string]string
		attr                   func(string) bool // whether the attributes are the expected body
	}{
		{TypeEntityResolutionIDMappingWorkflow, erTestIMWA, "imw-a", created, erTestTags[erTestIMWA], func(a string) bool {
			var w ertypes.IdMappingWorkflowSummary
			return json.Unmarshal([]byte(a), &w) == nil && sv(w.WorkflowArn) == erTestIMWA && w.UpdatedAt != nil
		}},
		{TypeEntityResolutionIDMappingWorkflow, erTestIMWB, "imw-b", "", nil, nil},
		{TypeEntityResolutionIDNamespace, erTestNSA, "ns-a", created, erTestTags[erTestNSA], func(a string) bool {
			var n ertypes.IdNamespaceSummary
			return json.Unmarshal([]byte(a), &n) == nil && n.Type == ertypes.IdNamespaceTypeSource && sv(n.Description) == "source ids"
		}},
		{TypeEntityResolutionIDNamespace, erTestNSB, "ns-b", "", nil, nil},
		{TypeEntityResolutionMatchingWorkflow, erTestMWA, "mw-a", created, erTestTags[erTestMWA], func(a string) bool {
			var g entityresolution.GetMatchingWorkflowOutput
			return json.Unmarshal([]byte(a), &g) == nil && sv(g.RoleArn) == erTestRole && sv(g.Description) == "matches mw-a"
		}},
		{TypeEntityResolutionMatchingWorkflow, erTestMWB, "mw-b", "", nil, func(a string) bool {
			var g entityresolution.GetMatchingWorkflowOutput
			return json.Unmarshal([]byte(a), &g) == nil && sv(g.RoleArn) == erTestRole && sv(g.Description) == "matches mw-b"
		}},
		{TypeEntityResolutionSchemaMapping, erTestSMA, "sm-a", created, erTestTags[erTestSMA], func(a string) bool {
			var s ertypes.SchemaMappingSummary
			return json.Unmarshal([]byte(a), &s) == nil && sdkaws.ToBool(s.HasWorkflows)
		}},
		{TypeEntityResolutionSchemaMapping, erTestSMB, "sm-b", "", nil, nil},
		{TypeEntityResolutionPolicyStatement, erTestIMWA + "/policy", "policy", "", nil, func(a string) bool {
			p, ok := erPolicyOf(a)
			return ok && sv(p.Policy) == erTestPolicies[erTestIMWA] && sv(p.Arn) == erTestIMWA
		}},
		{TypeEntityResolutionPolicyStatement, erTestMWA + "/policy", "policy", "", nil, func(a string) bool {
			p, ok := erPolicyOf(a)
			return ok && sv(p.Policy) == erTestPolicies[erTestMWA]
		}},
		{TypeEntityResolutionPolicyStatement, erTestSMB + "/policy", "policy", "", nil, func(a string) bool {
			p, ok := erPolicyOf(a)
			return ok && sv(p.Policy) == erTestPolicies[erTestSMB] && sv(p.Token) == "tok-1"
		}},
	} {
		t.Run(c.id, func(t *testing.T) {
			r, ok := rowsByNativeID(t, st, c.typ)[c.id]
			if !ok {
				t.Fatalf("%s %s not stored", c.typ, c.id)
			}
			if sv(r.Name) != c.name {
				t.Errorf("Name = %v; want %q", r.Name, c.name)
			}
			if r.Status != nil {
				t.Errorf("Status = %q; want none", *r.Status)
			}
			if sv(r.CreatedAt) != c.created || (c.created == "") != (r.CreatedAt == nil) {
				t.Errorf("CreatedAt = %v; want %q", r.CreatedAt, c.created)
			}
			if sv(r.Region) != testRegion {
				t.Errorf("Region = %q; want %q", sv(r.Region), testRegion)
			}
			if r.Provider != "aws" || r.AccountID != testAccountID || sv(r.AccountName) != newTestAccount(testAccountID).Name {
				t.Errorf("Provider, AccountID, AccountName = %q, %q, %q; want aws, %s, %q", r.Provider, r.AccountID, sv(r.AccountName), testAccountID, newTestAccount(testAccountID).Name)
			}
			if c.attr != nil && !c.attr(r.AttributesJSON) {
				t.Errorf("attributes = %s; want the expected body", r.AttributesJSON)
			}
			if c.tags == nil {
				if r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				return
			}
			assertTagsMap(t, r.TagsJSON, c.tags)
		})
	}
	if got, want := erPoliciesStored(t, st), erPolicyIDs(erTestIMWA, erTestMWA, erTestSMB); !slices.Equal(got, want) {
		t.Errorf("policies = %v; want %v", got, want)
	}
}

// A rescan reports every stored row in its total, not only the newly inserted
// ones. The dispatcher reports total for the service, and a first scan into an
// empty store has total == inserted, so only a rescan catches the two swapped.
func TestScanEntityResolution_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls erCalls
	if _, _, err := erScan(context.Background(), st, erHandlers(&calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, _, err := erScan(context.Background(), st, erHandlers(&calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != erTestTotal {
		t.Errorf("rescan total = %d; want %d", total, erTestTotal)
	}
}

// A region with nothing listed stores nothing, and reads no tag, workflow
// body or policy.
func TestScanEntityResolution_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls erCalls
	h := erHandlers(&calls)
	h["ListIdMappingWorkflows"] = func(any) (any, error) { return &entityresolution.ListIdMappingWorkflowsOutput{}, nil }
	h["ListIdNamespaces"] = func(any) (any, error) { return &entityresolution.ListIdNamespacesOutput{}, nil }
	h["ListMatchingWorkflows"] = func(any) (any, error) { return &entityresolution.ListMatchingWorkflowsOutput{}, nil }
	h["ListSchemaMappings"] = func(any) (any, error) { return &entityresolution.ListSchemaMappingsOutput{}, nil }
	total, inserted, err := erScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
	}
	if n := len(calls.tagARNs.ids) + len(calls.getNames.ids) + len(calls.policyARNs.ids); n != 0 {
		t.Errorf("tag, workflow and policy reads = %d; want none", n)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently, and has no policy read. A
// denial keeps the row untagged and warns once under the tag op with the
// scan's scope; a throttle keeps it and is transient; any other error (the
// modeled ValidationException included) keeps it and is an error.
func TestScanEntityResolution_TagReadFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name         string
		arn, typ     string
		err          error
		outcome      serviceOutcome // -1: no error
		code         string
		wantKept     bool
		warn         bool
		wantPolicies []string // parents of the policies stored
	}{
		{"ID mapping workflow/not found", erTestIMWA, TypeEntityResolutionIDMappingWorkflow, erGone, -1, "", false, false, []string{erTestMWA, erTestSMB}},
		{"ID namespace/not found", erTestNSB, TypeEntityResolutionIDNamespace, erGone, -1, "", false, false, []string{erTestIMWA, erTestMWA, erTestSMB}},
		{"schema mapping/not found", erTestSMB, TypeEntityResolutionSchemaMapping, erGone, -1, "", false, false, []string{erTestIMWA, erTestMWA}},
		{"ID namespace/denied", erTestNSA, TypeEntityResolutionIDNamespace, erDenied, -1, "", true, true, []string{erTestIMWA, erTestMWA, erTestSMB}},
		{"schema mapping/transient", erTestSMA, TypeEntityResolutionSchemaMapping, erThrottle, outcomeTransient, "ThrottlingException", true, false, []string{erTestIMWA, erTestMWA, erTestSMB}},
		{"ID mapping workflow/other error", erTestIMWB, TypeEntityResolutionIDMappingWorkflow, erValidation, outcomeError, "ValidationException", true, false, []string{erTestIMWA, erTestMWA, erTestSMB}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := erCalls{tagErrs: map[string]error{c.arn: c.err}}
			total, _, err := erScan(context.Background(), st, erHandlers(&calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			wantListed := 8
			if !c.wantKept {
				wantListed--
			}
			if want := wantListed + len(c.wantPolicies); total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			if got := slices.Contains(calls.policyARNs.distinct(), c.arn); got != c.wantKept {
				t.Errorf("GetPolicy sent %s = %v; want %v", c.arn, got, c.wantKept)
			}
			if got, want := erPoliciesStored(t, st), erPolicyIDs(c.wantPolicies...); !slices.Equal(got, want) {
				t.Errorf("policies = %v; want %v", got, want)
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{erListTagsForResourceOp}
			}
			if got := amWarnings(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// A failed GetMatchingWorkflow. ResourceNotFoundException is modeled, so a
// workflow deleted since it was listed is dropped silently: no tag or policy
// read. Any other failure keeps the list element as the row, reads its tags
// with ListTagsForResource instead, and reads its policy: a denial warns once
// under the Get's op, a throttle is transient, any other error is an error.
func TestScanEntityResolution_GetMatchingWorkflowFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		wf       string // the workflow whose Get fails
		err      error
		outcome  serviceOutcome // -1: no error
		code     string
		wantKept bool
		warns    []string
	}{
		{"not found", "mw-a", erGone, -1, "", false, nil},
		{"denied", "mw-a", erDenied, -1, "", true, []string{erGetMatchingWorkflowOp}},
		{"transient", "mw-b", erThrottle, outcomeTransient, "ThrottlingException", true, nil},
		{"other error", "mw-a", erBadRequest, outcomeError, "InvalidRequestException", true, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := erCalls{getErrs: map[string]error{c.wf: c.err}}
			total, _, err := erScan(context.Background(), st, erHandlers(&calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			arn := erTestMWByName[c.wf]
			wantTotal := erTestTotal
			wantTagged := erListTagged()
			if c.wantKept {
				wantTagged = erSorted(append(wantTagged, arn)...)
			} else {
				// The row and its policy (mw-a has one).
				wantTotal -= 2
			}
			if total != wantTotal {
				t.Errorf("total = %d; want %d", total, wantTotal)
			}
			if got := calls.tagARNs.distinct(); !slices.Equal(got, wantTagged) {
				t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantTagged)
			}
			if got := slices.Contains(calls.policyARNs.distinct(), arn); got != c.wantKept {
				t.Errorf("GetPolicy sent %s = %v; want %v", arn, got, c.wantKept)
			}
			r, ok := rowsByNativeID(t, st, TypeEntityResolutionMatchingWorkflow)[arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok {
				var s ertypes.MatchingWorkflowSummary
				var g entityresolution.GetMatchingWorkflowOutput
				if json.Unmarshal([]byte(r.AttributesJSON), &s) != nil || sv(s.WorkflowArn) != arn || s.ResolutionType == "" ||
					json.Unmarshal([]byte(r.AttributesJSON), &g) != nil || g.RoleArn != nil {
					t.Errorf("attributes = %s; want the list element", r.AttributesJSON)
				}
				if want := erTestTags[arn]; want == nil {
					if r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
				} else {
					assertTagsMap(t, r.TagsJSON, want)
				}
			}
			if got := amWarnings(warns()); !slices.Equal(got, c.warns) {
				t.Errorf("warnings = %v; want %v", got, c.warns)
			}
		})
	}
}

// A workflow whose Get and tag read are both denied is kept as its list
// element without tags, and each op warns once.
func TestScanEntityResolution_GetAndTagReadDenied(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := erCalls{getErrs: map[string]error{"mw-a": erDenied}, tagErrs: map[string]error{erTestMWA: erDenied}}
	total, _, err := erScan(context.Background(), st, erHandlers(&calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != erTestTotal {
		t.Errorf("total = %d; want %d", total, erTestTotal)
	}
	r, ok := rowsByNativeID(t, st, TypeEntityResolutionMatchingWorkflow)[erTestMWA]
	if !ok {
		t.Fatalf("%s not stored", erTestMWA)
	}
	if r.TagsJSON != nil {
		t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
	}
	if got, want := amWarnings(warns()), []string{erGetMatchingWorkflowOp, erListTagsForResourceOp}; !slices.Equal(got, want) {
		t.Errorf("warnings = %v; want %v", got, want)
	}
}

// Denied tag reads in every phase send one warning for the whole scan; the
// rows are kept.
func TestScanEntityResolution_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := erCalls{tagErrs: map[string]error{erTestIMWB: erDenied, erTestNSA: erDenied, erTestSMB: erDenied}}
	total, _, err := erScan(context.Background(), st, erHandlers(&calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != erTestTotal {
		t.Errorf("total = %d; want %d", total, erTestTotal)
	}
	if got := amWarnings(warns()); !slices.Equal(got, []string{erListTagsForResourceOp}) {
		t.Errorf("warnings = %v; want one under %s", got, erListTagsForResourceOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to one
// warning, so the hard error decides the outcome; a throttled
// GetMatchingWorkflow beside it is demoted under its own op.
func TestScanEntityResolution_ThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	tagWarns := countOpWarnings(st, erListTagsForResourceOp)
	getWarns := countOpWarnings(st, erGetMatchingWorkflowOp)
	calls := erCalls{
		tagErrs: map[string]error{erTestIMWA: erThrottle, erTestSMB: erValidation},
		getErrs: map[string]error{"mw-b": erThrottle},
	}
	_, _, err := erScan(context.Background(), st, erHandlers(&calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *tagWarns != 1 || *getWarns != 1 {
		t.Errorf("%s, %s warnings = %d, %d; want 1, 1", erListTagsForResourceOp, erGetMatchingWorkflowOp, *tagWarns, *getWarns)
	}
}

// A listing whose second page fails keeps its first page, and the later
// phases still run; a resource on the lost page has no policy read.
func TestScanEntityResolution_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		op   string
		lost string
	}{
		{"ListIdMappingWorkflows", erTestIMWB},
		{"ListIdNamespaces", erTestNSB},
		{"ListMatchingWorkflows", erTestMWB},
		{"ListSchemaMappings", erTestSMB},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			var calls erCalls
			h := erHandlers(&calls)
			list := h[tc.op]
			h[tc.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					return nil, erBadRequest
				}
				return list(in)
			}
			total, _, err := erScan(context.Background(), st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidRequestException")
			kept := slices.DeleteFunc(erAllListed(), func(id string) bool { return id == tc.lost })
			if got := erListedStored(t, st); !slices.Equal(got, kept) {
				t.Errorf("stored = %v; want %v", got, kept)
			}
			if got := calls.policyARNs.distinct(); !slices.Equal(got, kept) {
				t.Errorf("GetPolicy ARNs = %v; want %v", got, kept)
			}
			var withPolicy []string
			for _, id := range kept {
				if _, ok := erTestPolicies[id]; ok {
					withPolicy = append(withPolicy, id)
				}
			}
			if want := len(kept) + len(withPolicy); total != want {
				t.Errorf("total = %d, want %d", total, want)
			}
		})
	}
}

// A denied listing warns once under its op and is not an error; its first
// page, when it was read, is kept, and the later phases still run.
func TestScanEntityResolution_ListDeniedWarnsAndContinues(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		op   string
		page int // the page denied
		lost []string
	}{
		{"ListIdMappingWorkflows", 1, []string{erTestIMWA, erTestIMWB}},
		{"ListIdNamespaces", 2, []string{erTestNSB}},
		{"ListMatchingWorkflows", 2, []string{erTestMWB}},
		{"ListSchemaMappings", 1, []string{erTestSMA, erTestSMB}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			var calls erCalls
			h := erHandlers(&calls)
			list := h[tc.op]
			h[tc.op] = func(in any) (any, error) {
				if (bacField[*string](in, "NextToken") == nil) == (tc.page == 1) {
					return nil, erDenied
				}
				return list(in)
			}
			if _, _, err := erScan(context.Background(), st, h); err != nil {
				t.Fatalf("scan: %v", err)
			}
			kept := slices.DeleteFunc(erAllListed(), func(id string) bool { return slices.Contains(tc.lost, id) })
			if got := erListedStored(t, st); !slices.Equal(got, kept) {
				t.Errorf("stored = %v; want %v", got, kept)
			}
			if got := calls.policyARNs.distinct(); !slices.Equal(got, kept) {
				t.Errorf("GetPolicy ARNs = %v; want %v", got, kept)
			}
			if got, want := amWarnings(warns()), []string{"entityresolution:" + tc.op}; !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
}

// A policy read fails per resource: a denied one warns once for the op, one
// refused with ValidationException (modeled) warns once under the op, apart
// from the denial warning, a throttle is transient and any other error is an
// error. Every other resource's policy is stored.
func TestScanEntityResolution_PolicyReadErrors(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name         string
		errs         map[string]error
		outcome      serviceOutcome // -1: no error
		code         string
		warns        int
		wantPolicies []string
	}{
		{"denied", map[string]error{erTestIMWA: erDenied, erTestSMB: erDenied}, -1, "", 1, []string{erTestMWA}},
		{"validation", map[string]error{erTestMWA: erValidation, erTestNSB: erValidation}, -1, "", 1, []string{erTestIMWA, erTestSMB}},
		{"denied and validation", map[string]error{erTestIMWA: erDenied, erTestMWA: erValidation}, -1, "", 2, []string{erTestSMB}},
		{"transient", map[string]error{erTestSMB: erThrottle}, outcomeTransient, "ThrottlingException", 0, []string{erTestIMWA, erTestMWA}},
		{"other error", map[string]error{erTestIMWA: erBadRequest}, outcomeError, "InvalidRequestException", 0, []string{erTestMWA, erTestSMB}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := erCalls{policyErrs: c.errs}
			total, _, err := erScan(context.Background(), st, erHandlers(&calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if want := 8 + len(c.wantPolicies); total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if got, want := erPoliciesStored(t, st), erPolicyIDs(c.wantPolicies...); !slices.Equal(got, want) {
				t.Errorf("policies = %v; want %v", got, want)
			}
			want := slices.Repeat([]string{erGetPolicyOp}, c.warns)
			w := warns()
			if got := amWarnings(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// A throttled policy read beside a hard failure of the same op is demoted to
// one GetPolicy warning, so the hard error alone decides the outcome.
func TestScanEntityResolution_PolicyThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, erGetPolicyOp)
	calls := erCalls{policyErrs: map[string]error{erTestIMWA: erThrottle, erTestSMB: erBadRequest}}
	_, _, err := erScan(context.Background(), st, erHandlers(&calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidRequestException")
	if isAPIErrorCode(err, "ThrottlingException") {
		t.Errorf("err = %v; want the throttle demoted out of it", err)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", erGetPolicyOp, *warns)
	}
	if got, want := erPoliciesStored(t, st), erPolicyIDs(erTestMWA); !slices.Equal(got, want) {
		t.Errorf("policies = %v; want %v", got, want)
	}
}

// Every throttled policy read of a scan is one transient error, however many
// resources it covers.
func TestScanEntityResolution_PolicyThrottlesAreOneError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := erCalls{policyErrs: map[string]error{erTestIMWA: erThrottle, erTestMWA: erThrottle, erTestSMB: erThrottle}}
	_, _, err := erScan(context.Background(), st, erHandlers(&calls))
	assertGlueOutcome(t, err, outcomeTransient)
	if m := joinedMembers(err); m != nil {
		t.Errorf("err has %d members; want one error (err %v)", len(m), err)
	}
	if got := erPoliciesStored(t, st); len(got) != 0 {
		t.Errorf("policies = %v; want none", got)
	}
}

// A failed write stops the scan in each phase: no warning about the rows it
// lost (a denied tag, workflow or policy read among them), no later phase
// runs, and the earlier phases' rows stay counted.
func TestScanEntityResolution_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		typ                       string
		tagErrs, getErrs, polErrs map[string]error
		laterLists                []string // listings that must not be called
		wantTotal                 int
	}{
		{TypeEntityResolutionIDMappingWorkflow, map[string]error{erTestIMWA: erDenied}, nil, nil, []string{"ListIdNamespaces", "ListMatchingWorkflows", "ListSchemaMappings"}, 0},
		{TypeEntityResolutionIDNamespace, map[string]error{erTestNSA: erDenied}, nil, nil, []string{"ListMatchingWorkflows", "ListSchemaMappings"}, 2},
		{TypeEntityResolutionMatchingWorkflow, nil, map[string]error{"mw-a": erDenied}, nil, []string{"ListSchemaMappings"}, 4},
		{TypeEntityResolutionSchemaMapping, map[string]error{erTestSMA: erDenied}, nil, nil, nil, 6},
		{TypeEntityResolutionPolicyStatement, nil, nil, map[string]error{erTestNSB: erDenied}, nil, 8},
	} {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			refuseStoreWrites(t, st, c.typ)
			calls := erCalls{tagErrs: c.tagErrs, getErrs: c.getErrs, policyErrs: c.polErrs}
			total, _, err := erScan(context.Background(), st, erHandlers(&calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			for _, op := range c.laterLists {
				if slices.Contains(calls.lists.distinct(), op) {
					t.Errorf("%s called after the failed write", op)
				}
			}
			if ran := len(calls.policyARNs.ids) > 0; ran != (c.typ == TypeEntityResolutionPolicyStatement) {
				t.Errorf("GetPolicy called = %v; want only when the policy write fails", ran)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
		})
	}
}

// Each listing writes a page before it reads the next: the first page's row
// is stored when the second page is requested.
func TestScanEntityResolution_WritesEachPageBeforeTheNext(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls erCalls
	h := erHandlers(&calls)
	pageOne := map[string]string{"ListIdMappingWorkflows": erTestIMWA, "ListIdNamespaces": erTestNSA, "ListMatchingWorkflows": erTestMWA, "ListSchemaMappings": erTestSMA}
	lookups := map[string]error{} // per listing: GetResource of its page-1 row, when page 2 was requested
	for op, arn := range pageOne {
		list := h[op]
		h[op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				_, lookups[op] = st.GetResource(store.ResourceID("aws", testAccountID, arn))
			}
			return list(in)
		}
	}
	if _, _, err := erScan(context.Background(), st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for op, arn := range pageOne {
		err, asked := lookups[op]
		if !asked {
			t.Errorf("%s: page 2 never requested", op)
			continue
		}
		if err != nil {
			t.Errorf("%s: %s not stored before page 2 was read: %v", op, arn, err)
		}
	}
}

// A repeated NextToken ends each listing as an error, keeping its pages; the
// later phases still run.
func TestScanEntityResolution_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"ListIdMappingWorkflows", "ListIdNamespaces", "ListMatchingWorkflows", "ListSchemaMappings"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			var calls erCalls
			h := erHandlers(&calls)
			twoPages := h[op]
			h[op] = func(in any) (any, error) {
				out, err := twoPages(in)
				switch o := out.(type) {
				case *entityresolution.ListIdMappingWorkflowsOutput:
					o.NextToken = sdkaws.String("same")
				case *entityresolution.ListIdNamespacesOutput:
					o.NextToken = sdkaws.String("same")
				case *entityresolution.ListMatchingWorkflowsOutput:
					o.NextToken = sdkaws.String("same")
				case *entityresolution.ListSchemaMappingsOutput:
					o.NextToken = sdkaws.String("same")
				}
				return out, err
			}
			total, _, err := erScan(context.Background(), st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != erTestTotal {
				t.Errorf("total = %d; want %d", total, erTestTotal)
			}
			if got := erListedStored(t, st); !slices.Equal(got, erAllListed()) {
				t.Errorf("stored = %v; want %v", got, erAllListed())
			}
			if got := calls.maxResults[op]; len(got) != 2 {
				t.Errorf("%s calls = %d; want 2", op, len(got))
			}
		})
	}
}

// A listed element without an ARN is not stored, and each listing warns once
// under its op; its siblings are stored and have their tags read. A matching
// workflow with an ARN but no name has no GetMatchingWorkflow call: it is
// stored as its list element, its tags read with ListTagsForResource.
func TestScanEntityResolution_ElementWithoutIdentifier(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls erCalls
	h := erHandlers(&calls)
	h["ListIdMappingWorkflows"] = func(any) (any, error) {
		return &entityresolution.ListIdMappingWorkflowsOutput{WorkflowSummaries: []ertypes.IdMappingWorkflowSummary{{WorkflowName: sdkaws.String("no-arn")}, {WorkflowArn: &erTestIMWA}}}, nil
	}
	h["ListIdNamespaces"] = func(any) (any, error) {
		return &entityresolution.ListIdNamespacesOutput{IdNamespaceSummaries: []ertypes.IdNamespaceSummary{{}, {IdNamespaceArn: &erTestNSA}}}, nil
	}
	h["ListMatchingWorkflows"] = func(any) (any, error) {
		return &entityresolution.ListMatchingWorkflowsOutput{WorkflowSummaries: []ertypes.MatchingWorkflowSummary{{WorkflowName: sdkaws.String("no-arn")}, {WorkflowArn: &erTestMWA, ResolutionType: ertypes.ResolutionTypeRuleMatching}}}, nil
	}
	h["ListSchemaMappings"] = func(any) (any, error) {
		return &entityresolution.ListSchemaMappingsOutput{SchemaList: []ertypes.SchemaMappingSummary{{SchemaName: sdkaws.String("no-arn")}, {SchemaArn: &erTestSMA}}}, nil
	}
	if _, _, err := erScan(context.Background(), st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := erSorted(erTestIMWA, erTestNSA, erTestMWA, erTestSMA)
	if got := erListedStored(t, st); !slices.Equal(got, want) {
		t.Errorf("stored = %v; want %v", got, want)
	}
	if got := calls.tagARNs.distinct(); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
	}
	if n := len(calls.getNames.ids); n != 0 {
		t.Errorf("GetMatchingWorkflow calls = %v; want none", calls.getNames.ids)
	}
	mw := rowsByNativeID(t, st, TypeEntityResolutionMatchingWorkflow)[erTestMWA]
	assertTagsMap(t, mw.TagsJSON, erTestTags[erTestMWA])
	var s ertypes.MatchingWorkflowSummary
	if json.Unmarshal([]byte(mw.AttributesJSON), &s) != nil || s.ResolutionType != ertypes.ResolutionTypeRuleMatching {
		t.Errorf("attributes = %s; want the list element", mw.AttributesJSON)
	}
	w := warns()
	wantOps := []string{erListIDMappingWorkflowsOp, erListIDNamespacesOp, erListMatchingWorkflowsOp, erListSchemaMappingsOp}
	if got := amWarnings(w); !slices.Equal(got, wantOps) {
		t.Fatalf("warnings = %v; want %v", w, wantOps)
	}
}

// A GovCloud or China ARN reaches the tag op and GetPolicy unchanged, and a
// policy's NativeID is that ARN plus /policy. The matching workflow's Get is
// denied here, so its ARN reaches the tag op too.
func TestScanEntityResolution_PartitionARNs(t *testing.T) {
	t.Parallel()
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			arns := map[string]string{
				"ListIdMappingWorkflows": partitionARN(pf.partition, "entityresolution", pf.region, "idmappingworkflow/imw-gov"),
				"ListIdNamespaces":       partitionARN(pf.partition, "entityresolution", pf.region, "idnamespace/ns-gov"),
				"ListMatchingWorkflows":  partitionARN(pf.partition, "entityresolution", pf.region, "matchingworkflow/mw-gov"),
				"ListSchemaMappings":     partitionARN(pf.partition, "entityresolution", pf.region, "schemamapping/sm-gov"),
			}
			calls := erCalls{tags: map[string]map[string]string{}, getErrs: map[string]error{"mw-gov": erDenied}}
			for _, arn := range arns {
				calls.tags[arn] = map[string]string{"env": pf.partition}
			}
			h := erHandlers(&calls)
			h["ListIdMappingWorkflows"] = func(any) (any, error) {
				return &entityresolution.ListIdMappingWorkflowsOutput{WorkflowSummaries: []ertypes.IdMappingWorkflowSummary{{WorkflowArn: sdkaws.String(arns["ListIdMappingWorkflows"])}}}, nil
			}
			h["ListIdNamespaces"] = func(any) (any, error) {
				return &entityresolution.ListIdNamespacesOutput{IdNamespaceSummaries: []ertypes.IdNamespaceSummary{{IdNamespaceArn: sdkaws.String(arns["ListIdNamespaces"])}}}, nil
			}
			h["ListMatchingWorkflows"] = func(any) (any, error) {
				return &entityresolution.ListMatchingWorkflowsOutput{WorkflowSummaries: []ertypes.MatchingWorkflowSummary{{WorkflowArn: sdkaws.String(arns["ListMatchingWorkflows"]), WorkflowName: sdkaws.String("mw-gov")}}}, nil
			}
			h["ListSchemaMappings"] = func(any) (any, error) {
				return &entityresolution.ListSchemaMappingsOutput{SchemaList: []ertypes.SchemaMappingSummary{{SchemaArn: sdkaws.String(arns["ListSchemaMappings"])}}}, nil
			}
			getPolicy := h["GetPolicy"]
			h["GetPolicy"] = func(in any) (any, error) {
				out, err := getPolicy(in)
				if o, ok := out.(*entityresolution.GetPolicyOutput); ok && sv(o.Arn) == arns["ListSchemaMappings"] {
					o.Policy = sdkaws.String(`{"Statement":[]}`)
				}
				return out, err
			}
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(h, erDenied)
			if _, _, err := scanEntityResolution(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			want := erSorted(arns["ListIdMappingWorkflows"], arns["ListIdNamespaces"], arns["ListMatchingWorkflows"], arns["ListSchemaMappings"])
			if got := calls.tagARNs.distinct(); !slices.Equal(got, want) {
				t.Fatalf("ListTagsForResource ARNs = %v; want %v", got, want)
			}
			if got := calls.policyARNs.distinct(); !slices.Equal(got, want) {
				t.Errorf("GetPolicy ARNs = %v; want %v", got, want)
			}
			for typ, arn := range map[string]string{
				TypeEntityResolutionIDMappingWorkflow: arns["ListIdMappingWorkflows"],
				TypeEntityResolutionIDNamespace:       arns["ListIdNamespaces"],
				TypeEntityResolutionMatchingWorkflow:  arns["ListMatchingWorkflows"],
				TypeEntityResolutionSchemaMapping:     arns["ListSchemaMappings"],
			} {
				assertTagsMap(t, rowsByNativeID(t, st, typ)[arn].TagsJSON, map[string]string{"env": pf.partition})
			}
			if got, want := erPoliciesStored(t, st), []string{arns["ListSchemaMappings"] + "/policy"}; !slices.Equal(got, want) {
				t.Errorf("policies = %v; want %v", got, want)
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanEntityResolution_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls erCalls
	h := erHandlers(&calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := h["ListIdNamespaces"]
	h["ListIdNamespaces"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}
	_, _, err := erScan(ctx, st, h)
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
	for _, op := range []string{"ListMatchingWorkflows", "ListSchemaMappings"} {
		if slices.Contains(calls.lists.distinct(), op) {
			t.Errorf("%s called after ctx ended", op)
		}
	}
	if n := len(calls.policyARNs.ids); n != 0 {
		t.Errorf("GetPolicy calls = %d after ctx ended; want none", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanEntityResolution_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "entityresolution.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		var calls erCalls
		h := erHandlers(&calls)
		h["ListIdNamespaces"] = func(any) (any, error) { return nil, nx }
		total, _, err := erScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		if want := erTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := erCalls{tagErrs: map[string]error{erTestSMB: nx}}
		_, _, err := erScan(context.Background(), st, erHandlers(&calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	// The NXDOMAIN is a hard error of its own, not tallied with a throttled
	// read, which is demoted beside it.
	t.Run("policy read after the listings", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, erGetPolicyOp)
		calls := erCalls{policyErrs: map[string]error{erTestIMWA: erThrottle, erTestIMWB: nx}}
		_, _, err := erScan(context.Background(), st, erHandlers(&calls))
		assertGlueOutcome(t, err, outcomeError)
		if *warns != 1 {
			t.Errorf("%s warnings = %d; want 1", erGetPolicyOp, *warns)
		}
	})
	// Flattening the NXDOMAIN leaves another policy read's error its type.
	t.Run("policy read beside another hard error", func(t *testing.T) {
		st := newTestStore(t)
		calls := erCalls{policyErrs: map[string]error{erTestIMWB: nx, erTestSMB: erBadRequest}}
		_, _, err := erScan(context.Background(), st, erHandlers(&calls))
		assertGlueOutcome(t, err, outcomeError)
		assertAPICode(t, err, "InvalidRequestException")
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanEntityResolution(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

// When ctx ends during the policy reads, the resources whose policy was not
// read make the stop part of the error, although every read that ran
// succeeded. Every GetPolicy call waits until fanoutMed have arrived, and the
// last of those ends ctx, so the reads not started are exactly the rest.
func TestScanEntityResolution_ContextEndDuringPolicyReads(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls erCalls
	h := erHandlers(&calls)
	var many []ertypes.IdMappingWorkflowSummary
	for i := range fanoutMed + 2 {
		many = append(many, ertypes.IdMappingWorkflowSummary{WorkflowArn: sdkaws.String(fmt.Sprintf("%s-%02d", erTestIMWA, i))})
	}
	h["ListIdMappingWorkflows"] = func(any) (any, error) {
		return &entityresolution.ListIdMappingWorkflowsOutput{WorkflowSummaries: many}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var (
		mu      sync.Mutex
		arrived int
		release = make(chan struct{})
	)
	getPolicy := h["GetPolicy"]
	h["GetPolicy"] = func(in any) (any, error) {
		mu.Lock()
		arrived++
		if arrived == fanoutMed {
			cancel()
			close(release)
		}
		mu.Unlock()
		<-release
		return getPolicy(in)
	}
	_, _, err := erScan(ctx, st, h)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	if n := len(calls.policyARNs.ids); n != fanoutMed {
		t.Errorf("GetPolicy calls = %d; want %d", n, fanoutMed)
	}
}
