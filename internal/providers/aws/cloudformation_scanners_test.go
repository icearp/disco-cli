package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/icearp/disco-cli/store"
)

const (
	cfnTestStackARN  = "arn:aws:cloudformation:us-east-1:123456789012:stack/app/11111111-2222-3333-4444-555555555555"
	cfnTestTaggedSet = "arn:aws:cloudformation:us-east-1:123456789012:stackset/tagged:aaaa"
	cfnTestPlainSet  = "arn:aws:cloudformation:us-east-1:123456789012:stackset/plain:bbbb"
	cfnTestTypeARN   = "arn:aws:cloudformation:us-east-1:123456789012:type/resource/Acme-Widget-Thing"
	cfnTestInstance1 = "arn:aws:cloudformation:us-west-2:123456789012:stack/StackSet-tagged-1/1"
	cfnTestInstance2 = "arn:aws:cloudformation:eu-west-1:210987654321:stack/StackSet-tagged-2/2"
)

var cfnTestDenied = apiErr("AccessDenied", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: cloudformation:Describe")

// cfnInputs records the inputs of the stack-set ops. Handlers run on fan-out
// goroutines, so it is locked.
type cfnInputs struct {
	mu        sync.Mutex
	sets      []cloudformation.ListStackSetsInput
	describes []cloudformation.DescribeStackSetInput
	instances []cloudformation.ListStackInstancesInput
}

// cfnHandlers answers every op the service scan makes: one stack, two pages
// of stack sets (the tagged set and a summary without a name, then the plain
// set), two pages of the tagged set's instances, none for the plain set, and
// one private registry type.
func cfnHandlers(rec *cfnInputs) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListStacks": func(any) (any, error) {
			return &cloudformation.ListStacksOutput{StackSummaries: []cfntypes.StackSummary{
				{StackId: ptrStr(cfnTestStackARN), StackName: ptrStr("app"), StackStatus: cfntypes.StackStatusCreateComplete},
			}}, nil
		},
		"ListStackResources": func(any) (any, error) { return &cloudformation.ListStackResourcesOutput{}, nil },
		"DescribeStacks": func(in any) (any, error) {
			return &cloudformation.DescribeStacksOutput{Stacks: []cfntypes.Stack{{
				StackId: in.(*cloudformation.DescribeStacksInput).StackName, Tags: []cfntypes.Tag{{Key: ptrStr("team"), Value: ptrStr("infra")}},
			}}}, nil
		},
		"ListStackSets": func(in any) (any, error) {
			req := in.(*cloudformation.ListStackSetsInput)
			rec.mu.Lock()
			rec.sets = append(rec.sets, *req)
			rec.mu.Unlock()
			if req.NextToken == nil {
				return &cloudformation.ListStackSetsOutput{
					Summaries: []cfntypes.StackSetSummary{{StackSetName: ptrStr("tagged")}, {StackSetId: ptrStr("nameless:cccc")}},
					NextToken: ptrStr("t2"),
				}, nil
			}
			return &cloudformation.ListStackSetsOutput{Summaries: []cfntypes.StackSetSummary{{StackSetName: ptrStr("plain")}}}, nil
		},
		"DescribeStackSet": func(in any) (any, error) {
			req := in.(*cloudformation.DescribeStackSetInput)
			rec.mu.Lock()
			rec.describes = append(rec.describes, *req)
			rec.mu.Unlock()
			if sv(req.StackSetName) == "tagged" {
				return &cloudformation.DescribeStackSetOutput{StackSet: &cfntypes.StackSet{
					StackSetName: ptrStr("tagged"), StackSetARN: ptrStr(cfnTestTaggedSet), Description: ptrStr("baseline"),
					Status: cfntypes.StackSetStatusActive,
					Tags:   []cfntypes.Tag{{Key: ptrStr("env"), Value: ptrStr("prod")}, {Key: ptrStr("owner"), Value: ptrStr("platform")}},
				}}, nil
			}
			return &cloudformation.DescribeStackSetOutput{StackSet: &cfntypes.StackSet{
				StackSetName: req.StackSetName, StackSetARN: ptrStr(cfnTestPlainSet),
			}}, nil
		},
		"ListStackInstances": func(in any) (any, error) {
			req := in.(*cloudformation.ListStackInstancesInput)
			rec.mu.Lock()
			rec.instances = append(rec.instances, *req)
			rec.mu.Unlock()
			if sv(req.StackSetName) != "tagged" {
				return &cloudformation.ListStackInstancesOutput{}, nil
			}
			if req.NextToken == nil {
				return &cloudformation.ListStackInstancesOutput{
					Summaries: []cfntypes.StackInstanceSummary{{StackId: ptrStr(cfnTestInstance1), Account: ptrStr(testAccountID), Region: ptrStr("us-west-2")}},
					NextToken: ptrStr("i2"),
				}, nil
			}
			return &cloudformation.ListStackInstancesOutput{
				Summaries: []cfntypes.StackInstanceSummary{{StackId: ptrStr(cfnTestInstance2), Account: ptrStr("210987654321"), Region: ptrStr("eu-west-1")}},
			}, nil
		},
		"ListGeneratedTemplates": func(any) (any, error) { return &cloudformation.ListGeneratedTemplatesOutput{}, nil },
		"ListResourceScans":      func(any) (any, error) { return &cloudformation.ListResourceScansOutput{}, nil },
		"ListTypes": func(any) (any, error) {
			return &cloudformation.ListTypesOutput{TypeSummaries: []cfntypes.TypeSummary{
				{TypeArn: ptrStr(cfnTestTypeARN), TypeName: ptrStr("Acme::Widget::Thing"), Type: cfntypes.RegistryTypeResource},
			}}, nil
		},
	}
}

func scanCFNWith(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, cfnTestDenied)
	total, _, err := scanCloudFormation(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func cfnStackSetAttrsOf(t *testing.T, r store.Resource) stackSetWithInstances {
	t.Helper()
	var a stackSetWithInstances
	if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
		t.Fatalf("decode %s attrs: %v", r.NativeID, err)
	}
	return a
}

// Stack sets store the DescribeStackSet body with its Tags and every page of
// instances; every listing reads at the page cap with no filter, and a
// summary without a name warns once.
func TestScanCloudFormation_StackSetsStoreTags(t *testing.T) {
	st := newTestStore(t)
	listWarns := countOpWarnings(st, cfnListStackSetsOp)
	rec := &cfnInputs{}
	total, err := scanCFNWith(t, st, cfnHandlers(rec))
	if err != nil {
		t.Fatalf("scanCloudFormation: %v", err)
	}
	// 1 stack + 2 stack sets + 1 registry type
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}
	if *listWarns != 1 {
		t.Errorf("%s warnings = %d, want 1 for the summary without a name", cfnListStackSetsOp, *listWarns)
	}

	rows := rowsByNativeID(t, st, TypeCloudFormationStackSet)
	if len(rows) != 2 {
		t.Fatalf("stack-set rows = %v, want tagged and plain", rows)
	}
	tagged := rows[cfnTestTaggedSet]
	if sv(tagged.Name) != "tagged" || sv(tagged.Region) != testRegion {
		t.Errorf("tagged name, region = %q, %q; want tagged, %s", sv(tagged.Name), sv(tagged.Region), testRegion)
	}
	a := cfnStackSetAttrsOf(t, tagged)
	if a.StackSet == nil || sv(a.StackSet.Description) != "baseline" || len(a.StackSet.Tags) != 2 {
		t.Errorf("tagged attrs = %s; want the DescribeStackSet body verbatim, Tags included", tagged.AttributesJSON)
	}
	if len(a.Instances) != 2 || sv(a.Instances[0].StackId) != cfnTestInstance1 || sv(a.Instances[1].StackId) != cfnTestInstance2 {
		t.Errorf("tagged instances = %+v, want both pages", a.Instances)
	}
	assertTagsMap(t, tagged.TagsJSON, map[string]string{"env": "prod", "owner": "platform"})
	plain := rows[cfnTestPlainSet]
	if sv(plain.Name) != "plain" || plain.TagsJSON != nil || len(cfnStackSetAttrsOf(t, plain).Instances) != 0 {
		t.Errorf("plain name, tags, attrs = %q, %v, %s; want plain, nil, no instances", sv(plain.Name), plain.TagsJSON, plain.AttributesJSON)
	}

	if len(rec.sets) != 2 {
		t.Fatalf("ListStackSets calls = %d, want 2", len(rec.sets))
	}
	for i, wantToken := range []string{"", "t2"} {
		in := rec.sets[i]
		if sv(in.NextToken) != wantToken || sdkaws.ToInt32(in.MaxResults) != cfnPageCap || in.Status != cfntypes.StackSetStatusActive || in.CallAs != "" {
			t.Errorf("ListStackSets call %d = %+v; want NextToken %q, MaxResults %d, Status ACTIVE, no CallAs", i, in, wantToken, cfnPageCap)
		}
	}
	for _, in := range rec.describes {
		if in.CallAs != "" {
			t.Errorf("DescribeStackSet %s CallAs = %q, want unset", sv(in.StackSetName), in.CallAs)
		}
	}
	var taggedTokens []string
	for _, in := range rec.instances {
		if sdkaws.ToInt32(in.MaxResults) != cfnPageCap || in.Filters != nil || in.StackInstanceAccount != nil || in.StackInstanceRegion != nil || in.CallAs != "" {
			t.Errorf("ListStackInstances input = %+v; want MaxResults %d and no filter", in, cfnPageCap)
		}
		if sv(in.StackSetName) == "tagged" {
			taggedTokens = append(taggedTokens, sv(in.NextToken))
		}
	}
	if len(taggedTokens) != 2 || taggedTokens[0] != "" || taggedTokens[1] != "i2" {
		t.Errorf("tagged ListStackInstances tokens = %q, want [\"\" \"i2\"]", taggedTokens)
	}
}

// One set's failure costs only that set: a set deleted since it was listed
// is skipped silently, a denial warns once per op, any other error is
// returned after the plain set is stored. A ListStackInstances failure (on
// its second page) other than not-found keeps the set with the first page's
// instance.
func TestScanCloudFormation_StackSetOutcomes(t *testing.T) {
	notFound := apiErr("StackSetNotFoundException", "gone")
	hard := apiErr("ValidationError", "bad")
	for _, c := range []struct {
		name                     string
		describeErr, instanceErr error
		describeOut              *cloudformation.DescribeStackSetOutput
		wantErr                  error // nil: the scan returns nil
		wantTagged               bool  // the tagged set is stored (with the first instance page)
		describeWarns, instWarns int
		skipWarns                int // DescribeStackSet warnings for a set described without what its row needs
	}{
		{name: "describe not found", describeErr: notFound},
		{name: "describe denied", describeErr: cfnTestDenied, describeWarns: 1},
		{name: "describe other error", describeErr: hard, wantErr: hard},
		{name: "described without a StackSet", describeOut: &cloudformation.DescribeStackSetOutput{}, skipWarns: 1},
		{name: "described without an ARN", describeOut: &cloudformation.DescribeStackSetOutput{StackSet: &cfntypes.StackSet{StackSetName: ptrStr("tagged")}}, skipWarns: 1},
		{name: "instances not found", instanceErr: notFound},
		{name: "instances denied", instanceErr: cfnTestDenied, wantTagged: true, instWarns: 1},
		{name: "instances other error", instanceErr: hard, wantErr: hard, wantTagged: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			describeWarns := countOpWarnings(st, cfnDescribeStackSetOp)
			instWarns := countOpWarnings(st, cfnListStackInstancesOp)
			h := cfnHandlers(&cfnInputs{})
			describe, instances := h["DescribeStackSet"], h["ListStackInstances"]
			h["DescribeStackSet"] = func(in any) (any, error) {
				if sv(in.(*cloudformation.DescribeStackSetInput).StackSetName) == "tagged" {
					if c.describeErr != nil {
						return nil, c.describeErr
					}
					if c.describeOut != nil {
						return c.describeOut, nil
					}
				}
				return describe(in)
			}
			h["ListStackInstances"] = func(in any) (any, error) {
				req := in.(*cloudformation.ListStackInstancesInput)
				if sv(req.StackSetName) == "tagged" && req.NextToken != nil && c.instanceErr != nil {
					return nil, c.instanceErr
				}
				return instances(in)
			}

			_, err := scanCFNWith(t, st, h)
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			rows := rowsByNativeID(t, st, TypeCloudFormationStackSet)
			if _, ok := rows[cfnTestPlainSet]; !ok {
				t.Error("plain set missing: one set's failure cost a sibling")
			}
			tagged, ok := rows[cfnTestTaggedSet]
			if ok != c.wantTagged {
				t.Errorf("tagged set stored = %v, want %v", ok, c.wantTagged)
			}
			if ok {
				if inst := cfnStackSetAttrsOf(t, tagged).Instances; len(inst) != 1 || sv(inst[0].StackId) != cfnTestInstance1 {
					t.Errorf("tagged instances = %s, want the first page's", tagged.AttributesJSON)
				}
			}
			if *describeWarns != c.describeWarns+c.skipWarns || *instWarns != c.instWarns {
				t.Errorf("warnings: %s %d, %s %d; want %d, %d", cfnDescribeStackSetOp, *describeWarns, cfnListStackInstancesOp, *instWarns, c.describeWarns+c.skipWarns, c.instWarns)
			}
		})
	}
}

// A failed later ListStackSets page keeps the sets read before it: a denial
// warns, and any other error is returned, the ValidationError of an account
// where StackSets is not active included, since the op models no error.
func TestScanCloudFormation_StackSetListErrorKeepsEarlierPages(t *testing.T) {
	notActive := apiErr("ValidationError", "StackSets is not active in this account")
	other := apiErr("InvalidOperationException", "bad")
	for _, c := range []struct {
		name      string
		err       error
		wantErr   error // nil: the scan returns nil
		wantWarns int   // the summary without a name, plus the denial
	}{
		{"denied", cfnTestDenied, nil, 2},
		{"not active", notActive, notActive, 1},
		{"other error", other, other, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			listWarns := countOpWarnings(st, cfnListStackSetsOp)
			h := cfnHandlers(&cfnInputs{})
			list := h["ListStackSets"]
			h["ListStackSets"] = func(in any) (any, error) {
				if in.(*cloudformation.ListStackSetsInput).NextToken != nil {
					return nil, c.err
				}
				return list(in)
			}
			_, err := scanCFNWith(t, st, h)
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			rows := rowsByNativeID(t, st, TypeCloudFormationStackSet)
			if _, ok := rows[cfnTestTaggedSet]; !ok || len(rows) != 1 {
				t.Errorf("stack-set rows = %v, want only the first page's tagged set", rows)
			}
			if *listWarns != c.wantWarns {
				t.Errorf("%s warnings = %d, want %d", cfnListStackSetsOp, *listWarns, c.wantWarns)
			}
		})
	}
}

func TestScanCloudFormation_StackSetsStopOnRepeatedToken(t *testing.T) {
	st := newTestStore(t)
	h := cfnHandlers(&cfnInputs{})
	list := h["ListStackSets"]
	h["ListStackSets"] = func(in any) (any, error) {
		out, err := list(in)
		if err == nil {
			out.(*cloudformation.ListStackSetsOutput).NextToken = ptrStr("same")
		}
		return out, err
	}
	_, err := scanCFNWith(t, st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	if n := len(rowsByNativeID(t, st, TypeCloudFormationStackSet)); n != 2 {
		t.Errorf("stack-set rows = %d, want 2: the pages read are kept", n)
	}
}

// Every phase runs whatever another phase returns: one phase's hard error is
// returned and the other phases' rows are stored.
func TestScanCloudFormation_PhaseErrorDoesNotStopOtherPhases(t *testing.T) {
	for _, c := range []struct {
		op        string
		wantTotal int
	}{
		{"ListStacks", 3},
		{"ListStackSets", 2},
		{"ListGeneratedTemplates", 4},
		{"ListResourceScans", 4},
		{"ListTypes", 3},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			h := cfnHandlers(&cfnInputs{})
			h[c.op] = func(any) (any, error) { return nil, apiErr("InvalidOperationException", "boom") }
			total, err := scanCFNWith(t, st, h)
			if !isAPIErrorCode(err, "InvalidOperationException") {
				t.Fatalf("err = %v, want the %s phase's error", err, c.op)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d, want %d", total, c.wantTotal)
			}
			for typ, failed := range map[string]bool{
				TypeCloudFormationStack:    c.op == "ListStacks",
				TypeCloudFormationStackSet: c.op == "ListStackSets",
				TypeCloudFormationType:     c.op == "ListTypes",
			} {
				if n := len(rowsByNativeID(t, st, typ)); (n == 0) != failed {
					t.Errorf("%s rows = %d with the %s phase failing", typ, n, c.op)
				}
			}
		})
	}
}

// A transient error in one phase is demoted to a warning under its op beside
// another phase's hard error, which is returned.
func TestScanCloudFormation_TransientPhaseDoesNotMaskHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, cfnListStacksOp)
	h := cfnHandlers(&cfnInputs{})
	h["ListStacks"] = func(any) (any, error) { return nil, apiErr("Throttling", "Rate exceeded") }
	h["ListTypes"] = func(any) (any, error) { return nil, apiErr("InvalidOperationException", "boom") }
	_, err := scanCFNWith(t, st, h)
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if *warns != 1 {
		t.Errorf("cloudformation:ListStacks warnings = %d, want 1 for the demoted transient", *warns)
	}
}

// A store write failure stops the later phases, and the stack-set phase warns
// about nothing it did not store.
func TestScanCloudFormation_StoreWriteStopsLaterPhases(t *testing.T) {
	for _, failing := range []string{"ListStacks", "ListStackSets"} {
		t.Run(failing, func(t *testing.T) {
			st, closeStore := openClosingStore(t)
			var warnings []store.ScanWarning
			st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
			h := cfnHandlers(&cfnInputs{})
			// Denials the failing phase would warn about after a successful
			// write: a stack's resources, or a listed set's description and a
			// second ListStackSets page.
			if failing == "ListStacks" {
				h["ListStackResources"] = func(any) (any, error) { return nil, cfnTestDenied }
			}
			list, describe := h["ListStackSets"], h["DescribeStackSet"]
			h["ListStackSets"] = func(in any) (any, error) {
				if in.(*cloudformation.ListStackSetsInput).NextToken != nil {
					return nil, cfnTestDenied
				}
				out, err := list(in)
				page := out.(*cloudformation.ListStackSetsOutput)
				page.Summaries = append(page.Summaries, cfntypes.StackSetSummary{StackSetName: ptrStr("denied")})
				return page, err
			}
			h["DescribeStackSet"] = func(in any) (any, error) {
				if sv(in.(*cloudformation.DescribeStackSetInput).StackSetName) == "denied" {
					return nil, cfnTestDenied
				}
				return describe(in)
			}
			var (
				mu     sync.Mutex
				called []string
			)
			for op, next := range h {
				h[op] = func(in any) (any, error) {
					mu.Lock()
					called = append(called, op)
					mu.Unlock()
					if op == failing {
						closeStore()
					}
					return next(in)
				}
			}
			// A denial the phase would warn about after a successful write.
			h["ListStackInstances"] = func(any) (any, error) { return nil, cfnTestDenied }
			_, err := scanCFNWith(t, st, h)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v, want ErrStoreWrite", err)
			}
			for _, op := range called {
				if op == "ListGeneratedTemplates" || op == "ListTypes" || (failing == "ListStacks" && op == "ListStackSets") {
					t.Errorf("%s ran after the %s write failed; ops %v", op, failing, called)
				}
			}
			for _, w := range warnings {
				if w.Service != "store" {
					t.Errorf("warning %+v after the failed write, want none", w)
				}
			}
		})
	}
}

const cfnTestStackARN2 = "arn:aws:cloudformation:us-east-1:123456789012:stack/api/66666666-7777-8888-9999-000000000000"

// cfnStackInputs records the inputs of the stack ops.
type cfnStackInputs struct {
	mu        sync.Mutex
	lists     []cloudformation.ListStacksInput
	resources []cloudformation.ListStackResourcesInput
	describes []cloudformation.DescribeStacksInput
}

// cfnTwoStacks lists stack app and a summary without a StackId on the first
// page, then stack api; app has two pages of resources and api none.
// DescribeStacks tags app and leaves api untagged.
func cfnTwoStacks(h map[string]func(in any) (any, error), rec *cfnStackInputs) {
	h["ListStacks"] = func(in any) (any, error) {
		req := in.(*cloudformation.ListStacksInput)
		rec.mu.Lock()
		rec.lists = append(rec.lists, *req)
		rec.mu.Unlock()
		if req.NextToken == nil {
			return &cloudformation.ListStacksOutput{NextToken: ptrStr("s2"), StackSummaries: []cfntypes.StackSummary{
				{StackId: ptrStr(cfnTestStackARN), StackName: ptrStr("app")}, {StackName: ptrStr("idless")},
			}}, nil
		}
		return &cloudformation.ListStacksOutput{StackSummaries: []cfntypes.StackSummary{{StackId: ptrStr(cfnTestStackARN2), StackName: ptrStr("api")}}}, nil
	}
	h["ListStackResources"] = func(in any) (any, error) {
		req := in.(*cloudformation.ListStackResourcesInput)
		rec.mu.Lock()
		rec.resources = append(rec.resources, *req)
		rec.mu.Unlock()
		switch {
		case sv(req.StackName) != cfnTestStackARN:
			return &cloudformation.ListStackResourcesOutput{}, nil
		case req.NextToken == nil:
			return &cloudformation.ListStackResourcesOutput{NextToken: ptrStr("r2"), StackResourceSummaries: []cfntypes.StackResourceSummary{{LogicalResourceId: ptrStr("Queue")}}}, nil
		}
		return &cloudformation.ListStackResourcesOutput{StackResourceSummaries: []cfntypes.StackResourceSummary{{LogicalResourceId: ptrStr("Topic")}}}, nil
	}
	h["DescribeStacks"] = func(in any) (any, error) {
		req := in.(*cloudformation.DescribeStacksInput)
		rec.mu.Lock()
		rec.describes = append(rec.describes, *req)
		rec.mu.Unlock()
		s := cfntypes.Stack{StackId: req.StackName}
		if sv(req.StackName) == cfnTestStackARN {
			s.Tags = []cfntypes.Tag{{Key: ptrStr("env"), Value: ptrStr("prod")}}
		}
		return &cloudformation.DescribeStacksOutput{Stacks: []cfntypes.Stack{s}}, nil
	}
}

func cfnStackAttrsOf(t *testing.T, r store.Resource) stackWithResources {
	t.Helper()
	var a stackWithResources
	if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
		t.Fatalf("decode %s attrs: %v", r.NativeID, err)
	}
	return a
}

// Stacks store every page of their resources and the Tags DescribeStacks
// returns; ListStacks reads every page with the active-status filter, and a
// summary without a StackId warns once.
func TestScanCloudFormation_StacksStoreResourcesAndTags(t *testing.T) {
	st := newTestStore(t)
	listWarns := countOpWarnings(st, cfnListStacksOp)
	rec := &cfnStackInputs{}
	h := cfnHandlers(&cfnInputs{})
	cfnTwoStacks(h, rec)
	if _, err := scanCFNWith(t, st, h); err != nil {
		t.Fatalf("scanCloudFormation: %v", err)
	}
	if *listWarns != 1 {
		t.Errorf("%s warnings = %d, want 1 for the summary without a StackId", cfnListStacksOp, *listWarns)
	}
	rows := rowsByNativeID(t, st, TypeCloudFormationStack)
	if len(rows) != 2 {
		t.Fatalf("stack rows = %v, want app and api", rows)
	}
	app := rows[cfnTestStackARN]
	a := cfnStackAttrsOf(t, app)
	if sv(app.Name) != "app" || sv(app.Region) != testRegion || a.Stack == nil || sv(a.Stack.StackName) != "app" {
		t.Errorf("app name, region, attrs = %q, %q, %s; want app, %s, its summary", sv(app.Name), sv(app.Region), app.AttributesJSON, testRegion)
	}
	if len(a.Resources) != 2 || sv(a.Resources[0].LogicalResourceId) != "Queue" || sv(a.Resources[1].LogicalResourceId) != "Topic" {
		t.Errorf("app resources = %+v, want both pages", a.Resources)
	}
	assertTagsMap(t, app.TagsJSON, map[string]string{"env": "prod"})
	if api := rows[cfnTestStackARN2]; sv(api.Name) != "api" || api.TagsJSON != nil || len(cfnStackAttrsOf(t, api).Resources) != 0 {
		t.Errorf("api name, tags, attrs = %q, %v, %s; want api, nil, no resources", sv(api.Name), api.TagsJSON, api.AttributesJSON)
	}

	if len(rec.lists) != 2 || sv(rec.lists[0].NextToken) != "" || sv(rec.lists[1].NextToken) != "s2" {
		t.Fatalf("ListStacks calls = %+v, want tokens \"\" and s2", rec.lists)
	}
	for _, in := range rec.lists {
		if !slices.Equal(in.StackStatusFilter, activeStackStatuses) {
			t.Errorf("ListStacks StackStatusFilter = %v, want activeStackStatuses", in.StackStatusFilter)
		}
	}
	var appTokens []string
	for _, in := range rec.resources {
		if sv(in.StackName) == cfnTestStackARN {
			appTokens = append(appTokens, sv(in.NextToken))
		}
	}
	if !slices.Equal(appTokens, []string{"", "r2"}) {
		t.Errorf("app ListStackResources tokens = %q, want [\"\" \"r2\"]", appTokens)
	}
	var described []string
	for _, in := range rec.describes {
		described = append(described, sv(in.StackName))
	}
	slices.Sort(described)
	if !slices.Equal(described, []string{cfnTestStackARN2, cfnTestStackARN}) { // sorted: api before app
		t.Errorf("DescribeStacks stack names = %q, want each stack's id", described)
	}
}

// One stack's failed read never drops it or its sibling: a denial stores it
// without the denied part and warns once, any other error stores it with what
// was read and is returned. ListStackResources fails on its second page.
func TestScanCloudFormation_StackOutcomes(t *testing.T) {
	gone := apiErr("ValidationError", "Stack with id app does not exist")
	hard := apiErr("InvalidOperationException", "bad")
	for _, c := range []struct {
		name                     string
		resourcesErr, describErr error
		emptyDescribe            bool
		wantErr                  error // nil: the scan returns nil
		wantResources            int
		wantTags                 bool
		resourceWarns, descWarns int
	}{
		{name: "resources denied", resourcesErr: cfnTestDenied, wantResources: 1, wantTags: true, resourceWarns: 1},
		{name: "resources not modeled ValidationError", resourcesErr: gone, wantErr: gone, wantResources: 1, wantTags: true},
		{name: "resources other error", resourcesErr: hard, wantErr: hard, wantResources: 1, wantTags: true},
		{name: "describe denied", describErr: cfnTestDenied, wantResources: 2, descWarns: 1},
		{name: "describe other error", describErr: hard, wantErr: hard, wantResources: 2},
		{name: "describe answers no stack", emptyDescribe: true, wantErr: errCFNStackNotDescribed, wantResources: 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			resourceWarns := countOpWarnings(st, cfnListStackResourcesOp)
			descWarns := countOpWarnings(st, cfnDescribeStacksOp)
			h := cfnHandlers(&cfnInputs{})
			cfnTwoStacks(h, &cfnStackInputs{})
			resources, describe := h["ListStackResources"], h["DescribeStacks"]
			h["ListStackResources"] = func(in any) (any, error) {
				req := in.(*cloudformation.ListStackResourcesInput)
				if sv(req.StackName) == cfnTestStackARN && req.NextToken != nil && c.resourcesErr != nil {
					return nil, c.resourcesErr
				}
				return resources(in)
			}
			h["DescribeStacks"] = func(in any) (any, error) {
				if sv(in.(*cloudformation.DescribeStacksInput).StackName) == cfnTestStackARN {
					if c.describErr != nil {
						return nil, c.describErr
					}
					if c.emptyDescribe {
						return &cloudformation.DescribeStacksOutput{}, nil
					}
				}
				return describe(in)
			}
			_, err := scanCFNWith(t, st, h)
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			rows := rowsByNativeID(t, st, TypeCloudFormationStack)
			if _, ok := rows[cfnTestStackARN2]; !ok {
				t.Error("api stack missing: one stack's failure cost a sibling")
			}
			app, ok := rows[cfnTestStackARN]
			if !ok {
				t.Fatal("app stack missing; want it stored with what was read")
			}
			if n := len(cfnStackAttrsOf(t, app).Resources); n != c.wantResources {
				t.Errorf("app resources = %d, want %d", n, c.wantResources)
			}
			if (app.TagsJSON != nil) != c.wantTags {
				t.Errorf("app tags = %v, want tags %v", app.TagsJSON, c.wantTags)
			}
			if *resourceWarns != c.resourceWarns || *descWarns != c.descWarns {
				t.Errorf("warnings: %s %d, %s %d; want %d, %d", cfnListStackResourcesOp, *resourceWarns, cfnDescribeStacksOp, *descWarns, c.resourceWarns, c.descWarns)
			}
		})
	}
}

// A failed later ListStacks page keeps the stacks read before it: a denial
// warns, any other error, a repeated token included, is returned.
func TestScanCloudFormation_StackListErrorKeepsEarlierPages(t *testing.T) {
	other := apiErr("InvalidOperationException", "bad")
	for _, c := range []struct {
		name      string
		page2     func(out *cloudformation.ListStacksOutput) (any, error)
		wantErr   error // nil: the scan returns nil
		wantWarns int   // the summary without a StackId, plus the denial
	}{
		{"denied", func(*cloudformation.ListStacksOutput) (any, error) { return nil, cfnTestDenied }, nil, 2},
		{"other error", func(*cloudformation.ListStacksOutput) (any, error) { return nil, other }, other, 1},
		{"repeated token", func(out *cloudformation.ListStacksOutput) (any, error) {
			out.StackSummaries, out.NextToken = nil, ptrStr("s2")
			return out, nil
		}, errRepeatedNextToken, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			listWarns := countOpWarnings(st, cfnListStacksOp)
			h := cfnHandlers(&cfnInputs{})
			cfnTwoStacks(h, &cfnStackInputs{})
			list := h["ListStacks"]
			h["ListStacks"] = func(in any) (any, error) {
				out, err := list(in)
				if in.(*cloudformation.ListStacksInput).NextToken != nil {
					return c.page2(out.(*cloudformation.ListStacksOutput))
				}
				return out, err
			}
			_, err := scanCFNWith(t, st, h)
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			rows := rowsByNativeID(t, st, TypeCloudFormationStack)
			if _, ok := rows[cfnTestStackARN]; !ok || len(rows) != 1 {
				t.Errorf("stack rows = %v, want only the first page's app", rows)
			}
			if *listWarns != c.wantWarns {
				t.Errorf("%s warnings = %d, want %d", cfnListStacksOp, *listWarns, c.wantWarns)
			}
		})
	}
}

// The generated-template, resource-scan and registry-type listings read every
// page at the page cap, warn once about an element without its id, and keep
// the pages read before a failed page.
func TestScanCloudFormation_ListPhasesKeepPagesAndWarnEmptyIDs(t *testing.T) {
	typeARN := func(n string) *string {
		return ptrStr("arn:aws:cloudformation:us-east-1:123456789012:type/resource/Acme-" + n)
	}
	for _, c := range []struct {
		op, label, typ string
		// answer returns the page for its input's token: the first holds a
		// stored element and one without its id, the second one stored element.
		answer func(t *testing.T, in any) (token *string, maxResults *int32, out any)
		first  string // NativeID of the first page's stored element
		// status is the first element's Status column; the second element
		// reports none, so its column is unset.
		status, second string
	}{
		{"ListGeneratedTemplates", cfnListGeneratedTemplatesOp, TypeCloudFormationGeneratedTemplate, func(_ *testing.T, in any) (*string, *int32, any) {
			req := in.(*cloudformation.ListGeneratedTemplatesInput)
			if req.NextToken == nil {
				return req.NextToken, req.MaxResults, &cloudformation.ListGeneratedTemplatesOutput{NextToken: ptrStr("n2"), Summaries: []cfntypes.TemplateSummary{
					{GeneratedTemplateId: ptrStr("gt-a"), GeneratedTemplateName: ptrStr("a"), Status: cfntypes.GeneratedTemplateStatusComplete}, {GeneratedTemplateName: ptrStr("idless")},
				}}
			}
			return req.NextToken, req.MaxResults, &cloudformation.ListGeneratedTemplatesOutput{Summaries: []cfntypes.TemplateSummary{{GeneratedTemplateId: ptrStr("gt-b")}}}
		}, "gt-a", "COMPLETE", "gt-b"},
		{"ListResourceScans", cfnListResourceScansOp, TypeCloudFormationResourceScan, func(t *testing.T, in any) (*string, *int32, any) {
			req := in.(*cloudformation.ListResourceScansInput)
			if req.ScanTypeFilter != "" {
				t.Errorf("ListResourceScans ScanTypeFilter = %q, want unset", req.ScanTypeFilter)
			}
			if req.NextToken == nil {
				return req.NextToken, req.MaxResults, &cloudformation.ListResourceScansOutput{NextToken: ptrStr("n2"), ResourceScanSummaries: []cfntypes.ResourceScanSummary{
					{ResourceScanId: ptrStr("scan-a"), Status: cfntypes.ResourceScanStatusComplete}, {Status: cfntypes.ResourceScanStatusFailed},
				}}
			}
			return req.NextToken, req.MaxResults, &cloudformation.ListResourceScansOutput{ResourceScanSummaries: []cfntypes.ResourceScanSummary{{ResourceScanId: ptrStr("scan-b")}}}
		}, "scan-a", "COMPLETE", "scan-b"},
		{"ListTypes", cfnListTypesOp, TypeCloudFormationType, func(t *testing.T, in any) (*string, *int32, any) {
			req := in.(*cloudformation.ListTypesInput)
			if req.Visibility != cfntypes.VisibilityPrivate || req.Type != "" || req.Filters != nil {
				t.Errorf("ListTypes input = %+v, want Visibility PRIVATE and no other filter", req)
			}
			if req.NextToken == nil {
				return req.NextToken, req.MaxResults, &cloudformation.ListTypesOutput{NextToken: ptrStr("n2"), TypeSummaries: []cfntypes.TypeSummary{
					{TypeArn: typeARN("A"), TypeName: ptrStr("Acme::A"), Type: cfntypes.RegistryTypeResource}, {TypeName: ptrStr("Acme::Arnless")},
				}}
			}
			return req.NextToken, req.MaxResults, &cloudformation.ListTypesOutput{TypeSummaries: []cfntypes.TypeSummary{{TypeArn: typeARN("B"), Type: cfntypes.RegistryTypeModule}}}
		}, sv(typeARN("A")), "", sv(typeARN("B"))},
	} {
		for _, failPage2 := range []bool{false, true} {
			t.Run(c.op+map[bool]string{false: "/all pages", true: "/second page fails"}[failPage2], func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, c.label)
				h := cfnHandlers(&cfnInputs{})
				var tokens []string
				h[c.op] = func(in any) (any, error) {
					token, maxResults, out := c.answer(t, in)
					tokens = append(tokens, sv(token))
					if sdkaws.ToInt32(maxResults) != cfnPageCap {
						t.Errorf("%s MaxResults = %d, want %d", c.op, sdkaws.ToInt32(maxResults), cfnPageCap)
					}
					if failPage2 && token != nil {
						return nil, apiErr("InvalidOperationException", "bad")
					}
					return out, nil
				}
				_, err := scanCFNWith(t, st, h)
				if failPage2 != isAPIErrorCode(err, "InvalidOperationException") || (!failPage2 && err != nil) {
					t.Fatalf("err = %v with the second page failing %v", err, failPage2)
				}
				if !slices.Equal(tokens, []string{"", "n2"}) {
					t.Errorf("tokens sent = %q, want [\"\" \"n2\"]", tokens)
				}
				rows := rowsByNativeID(t, st, c.typ)
				wantRows := map[bool]int{false: 2, true: 1}[failPage2]
				if _, ok := rows[c.first]; !ok || len(rows) != wantRows {
					t.Errorf("%s rows = %v, want %d with %s", c.typ, rows, wantRows, c.first)
				}
				if *warns != 1 {
					t.Errorf("%s warnings = %d, want 1 for the element without its id", c.label, *warns)
				}
				if got := sv(rows[c.first].Status); got != c.status {
					t.Errorf("%s Status = %q, want %q", c.first, got, c.status)
				}
				if r, ok := rows[c.second]; ok && r.Status != nil {
					t.Errorf("%s Status = %q, want unset", c.second, *r.Status)
				}
			})
		}
	}
}

// Once ctx is done no listed stack is read: each is stored from its summary
// alone, and the phase returns ctx's error.
func TestScanCloudFormationStacks_StopsWhenContextDone(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := cfnHandlers(&cfnInputs{})
	list := h["ListStacks"]
	h["ListStacks"] = func(in any) (any, error) {
		cancel()
		return list(in)
	}
	client := cloudformation.NewFromConfig(opStubConfig(h, cfnTestDenied))
	total, _, err := scanCloudFormationStacks(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	app, ok := rowsByNativeID(t, st, TypeCloudFormationStack)[cfnTestStackARN]
	if !ok || total != 1 {
		t.Fatalf("total = %d, app stored = %v; want the unread stack stored", total, ok)
	}
	a := cfnStackAttrsOf(t, app)
	if sv(app.Name) != "app" || a.Stack == nil || sv(a.Stack.StackName) != "app" || a.Resources != nil || app.TagsJSON != nil {
		t.Errorf("app = name %q, attrs %s, tags %v; want its summary, no resources and no tags", sv(app.Name), app.AttributesJSON, app.TagsJSON)
	}
}

// Once ctx is done no listed stack set is described, and the phase says so:
// a set's summary carries no ARN, so it cannot be stored from the listing.
func TestScanCloudFormationStackSets_StopsWhenContextDone(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := cfnHandlers(&cfnInputs{})
	list := h["ListStackSets"]
	h["ListStackSets"] = func(in any) (any, error) {
		cancel()
		out, err := list(in)
		out.(*cloudformation.ListStackSetsOutput).NextToken = nil
		return out, err
	}
	client := cloudformation.NewFromConfig(opStubConfig(h, cfnTestDenied))
	total, _, err := scanCloudFormationStackSets(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, context.Canceled) || total != 0 {
		t.Errorf("total, err = %d, %v; want 0, context.Canceled", total, err)
	}
}
