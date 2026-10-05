package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/migrationhuborchestrator"
	mhotypes "github.com/aws/aws-sdk-go-v2/service/migrationhuborchestrator/types"
	"github.com/icearp/disco-cli/store"
)

// stubMHO serves each op from pages keyed by "<Op>" (account-wide lists) or
// "<Op>:<parent key>" (children; key is the workflow/template id, plus
// "/<step group id>" for steps), token = page index. errs[key] replaces page
// errPage[key] of that key. Every call is recorded in queried as its key.
type stubMHO struct {
	t         *testing.T
	workflows [][]mhotypes.MigrationWorkflowSummary
	templates [][]mhotypes.TemplateSummary
	plugins   [][]mhotypes.PluginSummary
	wfGroups  map[string][][]mhotypes.WorkflowStepGroupSummary
	wfSteps   map[string][][]mhotypes.WorkflowStepSummary
	tplGroups map[string][][]mhotypes.TemplateStepGroupSummary
	tplSteps  map[string][][]mhotypes.TemplateStepSummary
	errs      map[string]error
	errPage   map[string]int

	mu      sync.Mutex
	queried []string
}

func mhoStubPage[T any](s *stubMHO, key string, pages [][]T, token *string) ([]T, *string, error) {
	s.mu.Lock()
	s.queried = append(s.queried, key)
	s.mu.Unlock()
	page := 0
	if token != nil {
		var err error
		if page, err = strconv.Atoi(*token); err != nil {
			s.t.Errorf("stub page token %q: %v", *token, err)
			return nil, nil, err
		}
	}
	if err := s.errs[key]; err != nil && page == s.errPage[key] {
		return nil, nil, err
	}
	return smPage(pages, token)
}

func (s *stubMHO) ListWorkflows(_ context.Context, in *migrationhuborchestrator.ListWorkflowsInput, _ ...func(*migrationhuborchestrator.Options)) (*migrationhuborchestrator.ListWorkflowsOutput, error) {
	items, next, err := mhoStubPage(s, "ListWorkflows", s.workflows, in.NextToken)
	if err != nil {
		return nil, err
	}
	return &migrationhuborchestrator.ListWorkflowsOutput{MigrationWorkflowSummary: items, NextToken: next}, nil
}

func (s *stubMHO) ListTemplates(_ context.Context, in *migrationhuborchestrator.ListTemplatesInput, _ ...func(*migrationhuborchestrator.Options)) (*migrationhuborchestrator.ListTemplatesOutput, error) {
	items, next, err := mhoStubPage(s, "ListTemplates", s.templates, in.NextToken)
	if err != nil {
		return nil, err
	}
	return &migrationhuborchestrator.ListTemplatesOutput{TemplateSummary: items, NextToken: next}, nil
}

func (s *stubMHO) ListPlugins(_ context.Context, in *migrationhuborchestrator.ListPluginsInput, _ ...func(*migrationhuborchestrator.Options)) (*migrationhuborchestrator.ListPluginsOutput, error) {
	items, next, err := mhoStubPage(s, "ListPlugins", s.plugins, in.NextToken)
	if err != nil {
		return nil, err
	}
	return &migrationhuborchestrator.ListPluginsOutput{Plugins: items, NextToken: next}, nil
}

func (s *stubMHO) ListWorkflowStepGroups(_ context.Context, in *migrationhuborchestrator.ListWorkflowStepGroupsInput, _ ...func(*migrationhuborchestrator.Options)) (*migrationhuborchestrator.ListWorkflowStepGroupsOutput, error) {
	key := sv(in.WorkflowId)
	items, next, err := mhoStubPage(s, "ListWorkflowStepGroups:"+key, s.wfGroups[key], in.NextToken)
	if err != nil {
		return nil, err
	}
	return &migrationhuborchestrator.ListWorkflowStepGroupsOutput{WorkflowStepGroupsSummary: items, NextToken: next}, nil
}

func (s *stubMHO) ListWorkflowSteps(_ context.Context, in *migrationhuborchestrator.ListWorkflowStepsInput, _ ...func(*migrationhuborchestrator.Options)) (*migrationhuborchestrator.ListWorkflowStepsOutput, error) {
	key := sv(in.WorkflowId) + "/" + sv(in.StepGroupId)
	items, next, err := mhoStubPage(s, "ListWorkflowSteps:"+key, s.wfSteps[key], in.NextToken)
	if err != nil {
		return nil, err
	}
	return &migrationhuborchestrator.ListWorkflowStepsOutput{WorkflowStepsSummary: items, NextToken: next}, nil
}

func (s *stubMHO) ListTemplateStepGroups(_ context.Context, in *migrationhuborchestrator.ListTemplateStepGroupsInput, _ ...func(*migrationhuborchestrator.Options)) (*migrationhuborchestrator.ListTemplateStepGroupsOutput, error) {
	key := sv(in.TemplateId)
	items, next, err := mhoStubPage(s, "ListTemplateStepGroups:"+key, s.tplGroups[key], in.NextToken)
	if err != nil {
		return nil, err
	}
	return &migrationhuborchestrator.ListTemplateStepGroupsOutput{TemplateStepGroupSummary: items, NextToken: next}, nil
}

func (s *stubMHO) ListTemplateSteps(_ context.Context, in *migrationhuborchestrator.ListTemplateStepsInput, _ ...func(*migrationhuborchestrator.Options)) (*migrationhuborchestrator.ListTemplateStepsOutput, error) {
	key := sv(in.TemplateId) + "/" + sv(in.StepGroupId)
	items, next, err := mhoStubPage(s, "ListTemplateSteps:"+key, s.tplSteps[key], in.NextToken)
	if err != nil {
		return nil, err
	}
	return &migrationhuborchestrator.ListTemplateStepsOutput{TemplateStepSummaryList: items, NextToken: next}, nil
}

func (s *stubMHO) calls(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, q := range s.queried {
		if q == key {
			n++
		}
	}
	return n
}

func mhoNID(kind, id string) string {
	return "arn:aws:migrationhub-orchestrator:" + testRegion + ":" + testAccountID + ":" + kind + "/" + id
}

// mhoTplARN is a template ARN as ListTemplates returns it. Its path differs
// from the arn:...:template/{Id} shape, so a scanner that synthesizes the
// template NativeID from Id instead of using the returned Arn fails.
func mhoTplARN(id string) string {
	return "arn:aws:migrationhub-orchestrator:" + testRegion + ":" + testAccountID + ":template/returned-" + id
}

func runMHO(t *testing.T, st *store.Store, stub *stubMHO) (int, error) {
	t.Helper()
	total, _, err := scanMHOWithClient(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

func mhoAttrs(t *testing.T, r store.Resource) map[string]any {
	t.Helper()
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs of %s: %v", r.NativeID, err)
	}
	return attrs
}

func mhoContains(t *testing.T, st *store.Store, parentNativeID, childNativeID string) {
	t.Helper()
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, parentNativeID), childNativeID)
}

// --- plugins ---------------------------------------------------------------

func TestScanMHOPlugins_TwoPagesStored(t *testing.T) {
	st := newTestStore(t)
	s := sdkaws.String
	stub := &stubMHO{t: t, plugins: [][]mhotypes.PluginSummary{
		{{PluginId: s("p-1"), Hostname: s("host-1"), IpAddress: s("10.0.0.1"), Status: mhotypes.PluginHealthPluginHealthy}, {Hostname: s("no id")}},
		{{PluginId: s("p-2")}},
	}}
	total, err := runMHO(t, st, stub)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2", total)
	}
	p1, p2 := mhoNID("plugin", "p-1"), mhoNID("plugin", "p-2")
	assertSMIDs(t, st, TypeMigrationHubOrchestratorPlugin, p1, p2)
	rows := smStoredRows(t, st, TypeMigrationHubOrchestratorPlugin)
	if got := sv(rows[p1].Name); got != "host-1" {
		t.Errorf("name = %q; want host-1", got)
	}
	if got := sv(rows[p1].Status); got != string(mhotypes.PluginHealthPluginHealthy) {
		t.Errorf("status = %q; want HEALTHY", got)
	}
	if rows[p2].Status != nil {
		t.Errorf("%s status = %q; want unset", p2, *rows[p2].Status)
	}
	if got := mhoAttrs(t, rows[p1])["IpAddress"]; got != "10.0.0.1" {
		t.Errorf("attrs IpAddress = %v; want 10.0.0.1", got)
	}
}

func TestScanMHOPlugins_ErrorShapes(t *testing.T) {
	cases := []struct {
		name            string
		err             error
		wantWarnings    int
		wantErrCode     string
		wantNotEntitled bool
	}{
		{name: "access denied warns", err: apiErr("AccessDeniedException", "User: x is not authorized to perform: y"), wantWarnings: 1},
		{name: "region gap silent", err: apiErr("ValidationException", "Unauthorized access denied")},
		{name: "closed to new customers", err: apiErr("AccessDeniedException", "This service is no longer open to new customers"), wantNotEntitled: true},
		{name: "other error propagates", err: apiErr("InternalServerException", "boom"), wantErrCode: "InternalServerException"},
		{name: "unrelated validation propagates", err: apiErr("ValidationException", "bad"), wantErrCode: "ValidationException"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countSMWarnings(st)
			stub := &stubMHO{
				t:       t,
				plugins: [][]mhotypes.PluginSummary{{{PluginId: sdkaws.String("p-1")}}},
				errs:    map[string]error{"ListPlugins": tc.err},
			}
			_, err := runMHO(t, st, stub)
			switch {
			case tc.wantNotEntitled:
				if !errors.Is(err, errServiceNotEntitled) {
					t.Fatalf("err = %v; want not-entitled sentinel", err)
				}
			case tc.wantErrCode != "":
				if !isAPIErrorCode(err, tc.wantErrCode) {
					t.Fatalf("err = %v; want wrapped %s", err, tc.wantErrCode)
				}
			case err != nil:
				t.Fatalf("scan: %v", err)
			}
			if *warnings != tc.wantWarnings {
				t.Errorf("warnings = %d; want %d", *warnings, tc.wantWarnings)
			}
			assertSMIDs(t, st, TypeMigrationHubOrchestratorPlugin)
		})
	}
}

// --- workflow and template children ---------------------------------------

// Every workflow's step groups are listed (both pages where there are two),
// every step group's steps are listed with its own workflow's id even when two
// workflows share a step-group id, rows missing their id are dropped, a
// workflow deleted mid-scan is skipped, and each child is contained by its
// parent.
func TestScanMHO_WorkflowChildren(t *testing.T) {
	st := newTestStore(t)
	warnings := countSMWarnings(st)
	s := sdkaws.String
	stub := &stubMHO{
		t:         t,
		workflows: [][]mhotypes.MigrationWorkflowSummary{{{Id: s("wf-1")}, {Id: s("wf-2")}, {Id: s("wf-gone")}}},
		wfGroups: map[string][][]mhotypes.WorkflowStepGroupSummary{
			"wf-1": {
				{{Id: s("sg-1"), Name: s("group one"), Status: mhotypes.StepGroupStatusInProgress, Owner: mhotypes.OwnerCustom}, {Name: s("no id")}},
				{{Id: s("sg-2")}},
			},
			"wf-2": {{{Id: s("sg-1")}}},
		},
		wfSteps: map[string][][]mhotypes.WorkflowStepSummary{
			"wf-1/sg-1": {
				{{StepId: s("st-1"), Name: s("step one"), Status: mhotypes.StepStatusCompleted, StepActionType: mhotypes.StepActionTypeManual}, {Name: s("no id")}},
				{{StepId: s("st-2")}},
			},
			"wf-2/sg-1": {{{StepId: s("st-3")}}},
		},
		errs: map[string]error{"ListWorkflowStepGroups:wf-gone": apiErr("ResourceNotFoundException", "workflow not found")},
	}
	total, err := runMHO(t, st, stub)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	wf1, wf2 := mhoNID("workflow", "wf-1"), mhoNID("workflow", "wf-2")
	sg1, sg2, sg3 := wf1+"/step-group/sg-1", wf1+"/step-group/sg-2", wf2+"/step-group/sg-1"
	st1, st2, st3 := sg1+"/step/st-1", sg1+"/step/st-2", sg3+"/step/st-3"
	if want := 3 + 3 + 3; total != want {
		t.Errorf("total = %d; want %d (workflows + step groups + steps)", total, want)
	}
	assertSMIDs(t, st, TypeMigrationHubOrchestratorWorkflowStepGroup, sg1, sg2, sg3)
	assertSMIDs(t, st, TypeMigrationHubOrchestratorWorkflowStep, st1, st2, st3)
	for key, want := range map[string]int{
		"ListWorkflowStepGroups:wf-1": 2, "ListWorkflowStepGroups:wf-2": 1, "ListWorkflowStepGroups:wf-gone": 1,
		"ListWorkflowSteps:wf-1/sg-1": 2, "ListWorkflowSteps:wf-1/sg-2": 1, "ListWorkflowSteps:wf-2/sg-1": 1,
	} {
		if got := stub.calls(key); got != want {
			t.Errorf("calls %s = %d; want %d", key, got, want)
		}
	}

	groups := smStoredRows(t, st, TypeMigrationHubOrchestratorWorkflowStepGroup)
	if got := sv(groups[sg1].Name); got != "group one" {
		t.Errorf("step group name = %q; want group one", got)
	}
	assertSMStatus(t, st, TypeMigrationHubOrchestratorWorkflowStepGroup, sg1, string(mhotypes.StepGroupStatusInProgress))
	assertSMStatus(t, st, TypeMigrationHubOrchestratorWorkflowStepGroup, sg2, "")
	if got := mhoAttrs(t, groups[sg1])["Owner"]; got != string(mhotypes.OwnerCustom) {
		t.Errorf("step group attrs Owner = %v; want CUSTOM", got)
	}
	steps := smStoredRows(t, st, TypeMigrationHubOrchestratorWorkflowStep)
	if got := sv(steps[st1].Name); got != "step one" {
		t.Errorf("step name = %q; want step one", got)
	}
	assertSMStatus(t, st, TypeMigrationHubOrchestratorWorkflowStep, st1, string(mhotypes.StepStatusCompleted))
	assertSMStatus(t, st, TypeMigrationHubOrchestratorWorkflowStep, st2, "")
	if got := mhoAttrs(t, steps[st1])["StepActionType"]; got != string(mhotypes.StepActionTypeManual) {
		t.Errorf("step attrs StepActionType = %v; want MANUAL", got)
	}

	mhoContains(t, st, wf1, sg1)
	mhoContains(t, st, wf1, sg2)
	mhoContains(t, st, wf2, sg3)
	mhoContains(t, st, sg1, st1)
	mhoContains(t, st, sg1, st2)
	mhoContains(t, st, sg3, st3)
	if *warnings != 0 {
		t.Errorf("warnings = %d; want 0", *warnings)
	}
}

// Template children hang off the Arn ListTemplates returned; a template
// summary missing its Arn (the NativeID) or its Id (what every child op
// takes) is neither stored nor listed under; steps are listed with their own
// template's id even when two templates share a step-group id; a template or
// step group deleted mid-scan is skipped.
func TestScanMHO_TemplateChildren(t *testing.T) {
	st := newTestStore(t)
	warnings := countSMWarnings(st)
	s := sdkaws.String
	tpl1 := mhoTplARN("tpl-1")
	stub := &stubMHO{
		t: t,
		templates: [][]mhotypes.TemplateSummary{{
			{Arn: s(tpl1), Id: s("tpl-1")},
			{Arn: s(mhoTplARN("tpl-2")), Id: s("tpl-2")},
			{Arn: s(mhoTplARN("tpl-noid"))},
			{Id: s("tpl-noarn")},
			{Arn: s(mhoTplARN("tpl-gone")), Id: s("tpl-gone")},
		}},
		tplGroups: map[string][][]mhotypes.TemplateStepGroupSummary{
			"tpl-1": {
				{{Id: s("sg-1"), Name: s("group one"), Next: []string{"sg-2"}}, {Name: s("no id")}},
				{{Id: s("sg-2")}, {Id: s("sg-gone")}},
			},
			"tpl-2": {{{Id: s("sg-1")}}},
		},
		tplSteps: map[string][][]mhotypes.TemplateStepSummary{
			"tpl-1/sg-1": {
				{{Id: s("st-1"), Name: s("step one"), TargetType: mhotypes.TargetTypeAll}, {Name: s("no id")}},
				{{Id: s("st-2")}},
			},
			"tpl-2/sg-1": {{{Id: s("st-3")}}},
		},
		errs: map[string]error{
			"ListTemplateStepGroups:tpl-gone": apiErr("ResourceNotFoundException", "template not found"),
			"ListTemplateSteps:tpl-1/sg-gone": apiErr("ResourceNotFoundException", "step group not found"),
		},
	}
	if _, err := runMHO(t, st, stub); err != nil {
		t.Fatalf("scan: %v", err)
	}
	tpl2 := mhoTplARN("tpl-2")
	assertSMIDs(t, st, TypeMigrationHubOrchestratorTemplate, tpl1, tpl2, mhoTplARN("tpl-gone"))
	sg1, sg2, sgGone, sg3 := tpl1+"/step-group/sg-1", tpl1+"/step-group/sg-2", tpl1+"/step-group/sg-gone", tpl2+"/step-group/sg-1"
	st1, st2, st3 := sg1+"/step/st-1", sg1+"/step/st-2", sg3+"/step/st-3"
	assertSMIDs(t, st, TypeMigrationHubOrchestratorTemplateStepGroup, sg1, sg2, sgGone, sg3)
	assertSMIDs(t, st, TypeMigrationHubOrchestratorTemplateStep, st1, st2, st3)
	for key, want := range map[string]int{
		"ListTemplateStepGroups:tpl-1": 2, "ListTemplateStepGroups:tpl-2": 1, "ListTemplateStepGroups:tpl-gone": 1,
		"ListTemplateSteps:tpl-1/sg-1": 2, "ListTemplateSteps:tpl-1/sg-2": 1, "ListTemplateSteps:tpl-1/sg-gone": 1,
		"ListTemplateSteps:tpl-2/sg-1": 1,
	} {
		if got := stub.calls(key); got != want {
			t.Errorf("calls %s = %d; want %d", key, got, want)
		}
	}
	for _, q := range stub.queried {
		if q == "ListTemplateStepGroups:" || q == "ListTemplateStepGroups:tpl-noarn" {
			t.Errorf("listed children of a template that was not stored: %v", stub.queried)
		}
	}

	groups := smStoredRows(t, st, TypeMigrationHubOrchestratorTemplateStepGroup)
	if got := sv(groups[sg1].Name); got != "group one" {
		t.Errorf("step group name = %q; want group one", got)
	}
	if _, ok := mhoAttrs(t, groups[sg1])["Next"]; !ok {
		t.Errorf("step group attrs missing Next: %s", groups[sg1].AttributesJSON)
	}
	assertSMStatus(t, st, TypeMigrationHubOrchestratorTemplateStepGroup, sg1, "")
	steps := smStoredRows(t, st, TypeMigrationHubOrchestratorTemplateStep)
	if got := sv(steps[st1].Name); got != "step one" {
		t.Errorf("step name = %q; want step one", got)
	}
	if got := mhoAttrs(t, steps[st1])["TargetType"]; got != string(mhotypes.TargetTypeAll) {
		t.Errorf("step attrs TargetType = %v; want ALL", got)
	}

	mhoContains(t, st, tpl1, sg1)
	mhoContains(t, st, tpl2, sg3)
	mhoContains(t, st, sg1, st1)
	mhoContains(t, st, sg1, st2)
	mhoContains(t, st, sg3, st3)
	if *warnings != 0 {
		t.Errorf("warnings = %d; want 0", *warnings)
	}
}

// mhoChildCase seeds parents "p-1", "p-2" and "p-3" for a single child op;
// only p-2 has children. keyOf maps a parent to its stub key suffix (after
// "<op>:"); rtype/want are the row p-2 yields.
type mhoChildCase struct {
	name  string
	op    string
	fill  func(*stubMHO)
	keyOf func(parent string) string
	rtype string
	want  string
}

func mhoChildCases() []mhoChildCase {
	s := sdkaws.String
	parents := []string{"p-1", "p-2", "p-3"}
	wfs := func() [][]mhotypes.MigrationWorkflowSummary {
		var out []mhotypes.MigrationWorkflowSummary
		for _, p := range parents {
			out = append(out, mhotypes.MigrationWorkflowSummary{Id: s(p)})
		}
		return [][]mhotypes.MigrationWorkflowSummary{out}
	}
	tpls := func() [][]mhotypes.TemplateSummary {
		var out []mhotypes.TemplateSummary
		for _, p := range parents {
			out = append(out, mhotypes.TemplateSummary{Arn: s(mhoTplARN(p)), Id: s(p)})
		}
		return [][]mhotypes.TemplateSummary{out}
	}
	return []mhoChildCase{
		{
			name: "workflow step groups", op: "ListWorkflowStepGroups",
			fill: func(st *stubMHO) {
				st.workflows = wfs()
				st.wfGroups = map[string][][]mhotypes.WorkflowStepGroupSummary{"p-2": {{{Id: s("sg")}}}}
			},
			keyOf: func(p string) string { return p },
			rtype: TypeMigrationHubOrchestratorWorkflowStepGroup, want: mhoNID("workflow", "p-2") + "/step-group/sg",
		},
		{
			name: "workflow steps", op: "ListWorkflowSteps",
			fill: func(st *stubMHO) {
				st.workflows = [][]mhotypes.MigrationWorkflowSummary{{{Id: s("wf")}}}
				var groups []mhotypes.WorkflowStepGroupSummary
				for _, p := range parents {
					groups = append(groups, mhotypes.WorkflowStepGroupSummary{Id: s(p)})
				}
				st.wfGroups = map[string][][]mhotypes.WorkflowStepGroupSummary{"wf": {groups}}
				st.wfSteps = map[string][][]mhotypes.WorkflowStepSummary{"wf/p-2": {{{StepId: s("step")}}}}
			},
			keyOf: func(p string) string { return "wf/" + p },
			rtype: TypeMigrationHubOrchestratorWorkflowStep, want: mhoNID("workflow", "wf") + "/step-group/p-2/step/step",
		},
		{
			name: "template step groups", op: "ListTemplateStepGroups",
			fill: func(st *stubMHO) {
				st.templates = tpls()
				st.tplGroups = map[string][][]mhotypes.TemplateStepGroupSummary{"p-2": {{{Id: s("sg")}}}}
			},
			keyOf: func(p string) string { return p },
			rtype: TypeMigrationHubOrchestratorTemplateStepGroup, want: mhoTplARN("p-2") + "/step-group/sg",
		},
		{
			name: "template steps", op: "ListTemplateSteps",
			fill: func(st *stubMHO) {
				st.templates = [][]mhotypes.TemplateSummary{{{Arn: s(mhoTplARN("tpl")), Id: s("tpl")}}}
				var groups []mhotypes.TemplateStepGroupSummary
				for _, p := range parents {
					groups = append(groups, mhotypes.TemplateStepGroupSummary{Id: s(p)})
				}
				st.tplGroups = map[string][][]mhotypes.TemplateStepGroupSummary{"tpl": {groups}}
				st.tplSteps = map[string][][]mhotypes.TemplateStepSummary{"tpl/p-2": {{{Id: s("step")}}}}
			},
			keyOf: func(p string) string { return "tpl/" + p },
			rtype: TypeMigrationHubOrchestratorTemplateStep, want: mhoTplARN("tpl") + "/step-group/p-2/step/step",
		},
	}
}

// Denied parents warn once per op in total; the other parent is still stored.
func TestScanMHO_ChildDeniedWarnsOnceKeepsSiblings(t *testing.T) {
	for _, tc := range mhoChildCases() {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countSMWarnings(st)
			denied := apiErr("AccessDeniedException", "User: x is not authorized to perform: y")
			stub := &stubMHO{t: t, errs: map[string]error{
				tc.op + ":" + tc.keyOf("p-1"): denied,
				tc.op + ":" + tc.keyOf("p-3"): denied,
			}}
			tc.fill(stub)
			if _, err := runMHO(t, st, stub); err != nil {
				t.Fatalf("scan: %v", err)
			}
			assertSMIDs(t, st, tc.rtype, tc.want)
			if *warnings != 1 {
				t.Errorf("warnings = %d; want 1", *warnings)
			}
		})
	}
}

// Any other error fails the scan, and the rows of the earlier phases are kept.
func TestScanMHO_ChildOtherErrorPropagates(t *testing.T) {
	for _, tc := range mhoChildCases() {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			stub := &stubMHO{t: t, errs: map[string]error{tc.op + ":" + tc.keyOf("p-1"): apiErr("InternalServerException", "boom")}}
			tc.fill(stub)
			_, err := runMHO(t, st, stub)
			if !isAPIErrorCode(err, "InternalServerException") {
				t.Fatalf("err = %v; want wrapped InternalServerException", err)
			}
			rows, lerr := st.ListResources(store.ResourceFilter{Types: []string{TypeMigrationHubOrchestratorWorkflow, TypeMigrationHubOrchestratorTemplate}})
			if lerr != nil {
				t.Fatalf("list: %v", lerr)
			}
			if len(rows) == 0 {
				t.Error("no workflow/template rows stored; the earlier phases must survive a child-phase error")
			}
		})
	}
}

// ListWorkflowSteps does not model ResourceNotFoundException, so it is not
// tolerated as a vanished parent.
func TestScanMHO_WorkflowStepsNotFoundPropagates(t *testing.T) {
	st := newTestStore(t)
	s := sdkaws.String
	stub := &stubMHO{
		t:         t,
		workflows: [][]mhotypes.MigrationWorkflowSummary{{{Id: s("wf")}}},
		wfGroups:  map[string][][]mhotypes.WorkflowStepGroupSummary{"wf": {{{Id: s("sg")}}}},
		errs:      map[string]error{"ListWorkflowSteps:wf/sg": apiErr("ResourceNotFoundException", "gone")},
	}
	if _, err := runMHO(t, st, stub); !isAPIErrorCode(err, "ResourceNotFoundException") {
		t.Fatalf("err = %v; want wrapped ResourceNotFoundException", err)
	}
}

// A denial on a parent's second page drops that parent's rows (childFanOut
// keeps a parent's rows only when its whole listing succeeded), and its step
// groups are not handed on to the steps phase.
func TestScanMHO_ChildDeniedOnSecondPageDropsParent(t *testing.T) {
	st := newTestStore(t)
	warnings := countSMWarnings(st)
	s := sdkaws.String
	stub := &stubMHO{
		t:         t,
		workflows: [][]mhotypes.MigrationWorkflowSummary{{{Id: s("wf")}}},
		wfGroups:  map[string][][]mhotypes.WorkflowStepGroupSummary{"wf": {{{Id: s("sg-1")}}, {{Id: s("sg-2")}}}},
		errs:      map[string]error{"ListWorkflowStepGroups:wf": apiErr("AccessDeniedException", "denied")},
		errPage:   map[string]int{"ListWorkflowStepGroups:wf": 1},
	}
	if _, err := runMHO(t, st, stub); err != nil {
		t.Fatalf("scan: %v", err)
	}
	assertSMIDs(t, st, TypeMigrationHubOrchestratorWorkflowStepGroup)
	if n := stub.calls("ListWorkflowSteps:wf/sg-1"); n != 0 {
		t.Errorf("ListWorkflowSteps called %d times for a group of a denied parent; want 0", n)
	}
	if *warnings != 1 {
		t.Errorf("warnings = %d; want 1", *warnings)
	}
}

// A workflow-steps failure (ListWorkflowSteps tolerates no vanished parent)
// must not cost the plugins or the template children: their phases run first.
func TestScanMHO_WorkflowStepsErrorKeepsTemplateChildren(t *testing.T) {
	st := newTestStore(t)
	s := sdkaws.String
	tpl := mhoTplARN("tpl")
	stub := &stubMHO{
		t:         t,
		workflows: [][]mhotypes.MigrationWorkflowSummary{{{Id: s("wf")}}},
		wfGroups:  map[string][][]mhotypes.WorkflowStepGroupSummary{"wf": {{{Id: s("sg")}}}},
		templates: [][]mhotypes.TemplateSummary{{{Arn: s(tpl), Id: s("tpl")}}},
		tplGroups: map[string][][]mhotypes.TemplateStepGroupSummary{"tpl": {{{Id: s("tsg")}}}},
		tplSteps:  map[string][][]mhotypes.TemplateStepSummary{"tpl/tsg": {{{Id: s("ts")}}}},
		plugins:   [][]mhotypes.PluginSummary{{{PluginId: s("p")}}},
		errs:      map[string]error{"ListWorkflowSteps:wf/sg": apiErr("ValidationException", "workflow gone")},
	}
	if _, err := runMHO(t, st, stub); !isAPIErrorCode(err, "ValidationException") {
		t.Fatalf("err = %v; want wrapped ValidationException", err)
	}
	assertSMIDs(t, st, TypeMigrationHubOrchestratorTemplateStepGroup, tpl+"/step-group/tsg")
	assertSMIDs(t, st, TypeMigrationHubOrchestratorTemplateStep, tpl+"/step-group/tsg/step/ts")
	assertSMIDs(t, st, TypeMigrationHubOrchestratorPlugin, mhoNID("plugin", "p"))
}

// With no workflows or templates no child op is called and nothing is stored.
func TestScanMHO_EmptyMakesNoChildCalls(t *testing.T) {
	st := newTestStore(t)
	stub := &stubMHO{t: t}
	total, err := runMHO(t, st, stub)
	if err != nil || total != 0 {
		t.Fatalf("scan = (%d, %v); want (0, nil)", total, err)
	}
	slices.Sort(stub.queried)
	if want := []string{"ListPlugins", "ListTemplates", "ListWorkflows"}; !slices.Equal(stub.queried, want) {
		t.Errorf("queried = %v; want %v", stub.queried, want)
	}
}

// TestScanMigrationHubOrchestrator_ChildrenContainedByParents runs the whole
// service against the real SDK client (its input validators included): every
// child phase runs after its parent phase and is contained by it.
func TestScanMigrationHubOrchestrator_ChildrenContainedByParents(t *testing.T) {
	st := newTestStore(t)
	s := sdkaws.String
	one := func(out any) []stubCall { return []stubCall{{Output: out}} }
	stub := stubResponses(t, map[string][]stubCall{
		"ListWorkflows": one(&migrationhuborchestrator.ListWorkflowsOutput{MigrationWorkflowSummary: []mhotypes.MigrationWorkflowSummary{{Id: s("wf")}}}),
		"ListTemplates": one(&migrationhuborchestrator.ListTemplatesOutput{TemplateSummary: []mhotypes.TemplateSummary{{Arn: s(mhoTplARN("tpl")), Id: s("tpl")}}}),
		"ListPlugins":   one(&migrationhuborchestrator.ListPluginsOutput{Plugins: []mhotypes.PluginSummary{{PluginId: s("p")}}}),
		"ListWorkflowStepGroups": one(&migrationhuborchestrator.ListWorkflowStepGroupsOutput{
			WorkflowStepGroupsSummary: []mhotypes.WorkflowStepGroupSummary{{Id: s("wsg")}},
		}),
		"ListWorkflowSteps": one(&migrationhuborchestrator.ListWorkflowStepsOutput{WorkflowStepsSummary: []mhotypes.WorkflowStepSummary{{StepId: s("ws")}}}),
		"ListTemplateStepGroups": one(&migrationhuborchestrator.ListTemplateStepGroupsOutput{
			TemplateStepGroupSummary: []mhotypes.TemplateStepGroupSummary{{Id: s("tsg")}},
		}),
		"ListTemplateSteps": one(&migrationhuborchestrator.ListTemplateStepsOutput{TemplateStepSummaryList: []mhotypes.TemplateStepSummary{{Id: s("ts")}}}),
	})
	acct := newTestAccount(testAccountID)
	acct.cfg = cloud9CfgWithStub(stub, testRegion)
	if _, _, err := scanMigrationHubOrchestrator(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	wf, tpl := mhoNID("workflow", "wf"), mhoTplARN("tpl")
	wsg, tsg := wf+"/step-group/wsg", tpl+"/step-group/tsg"
	assertSMIDs(t, st, TypeMigrationHubOrchestratorPlugin, mhoNID("plugin", "p"))
	mhoContains(t, st, wf, wsg)
	mhoContains(t, st, wsg, wsg+"/step/ws")
	mhoContains(t, st, tpl, tsg)
	mhoContains(t, st, tsg, tsg+"/step/ts")
}
