package aws

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/applicationinsights"
	aitypes "github.com/aws/aws-sdk-go-v2/service/applicationinsights/types"
	"github.com/icearp/disco-cli/store"
)

const (
	aiTestInstanceARN = "arn:aws:ec2:us-east-1:123456789012:instance/i-0abc"
	aiTestW1          = "w-11111111-1111-1111-1111-111111111111"
	aiTestW2          = "w-22222222-2222-2222-2222-222222222222"
	aiTestW3          = "w-33333333-3333-3333-3333-333333333333"

	aiTestAppA = "arn:aws:applicationinsights:us-east-1:123456789012:application/resource-group/rg-a"
	aiTestAppB = "arn:aws:applicationinsights:us-east-1:123456789012:application/resource-group/rg-b"

	aiTestCompWeb  = aiTestAppA + "/component/web"
	aiTestCompInst = aiTestAppA + "/component/" + aiTestInstanceARN
	aiTestCompDB   = aiTestAppB + "/component/db"

	aiTestWL1 = aiTestAppA + "/workload/" + aiTestW1
	aiTestWL2 = aiTestAppA + "/workload/" + aiTestW2
	aiTestWL3 = aiTestAppB + "/workload/" + aiTestW3

	aiTestSet1 = aiTestAppA + "/log-pattern-set/set1"
	aiTestSet2 = aiTestAppA + "/log-pattern-set/set2"
	aiTestSetB = aiTestAppB + "/log-pattern-set/setb"

	aiTestP1 = aiTestSet1 + "/log-pattern/p1"
	aiTestP2 = aiTestSet1 + "/log-pattern/p2"
	aiTestP3 = aiTestSet2 + "/log-pattern/p3"
)

var (
	aiDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: applicationinsights:List")
	aiBadInput = apiErr("ValidationException", "bad")
	aiGone     = apiErr("ResourceNotFoundException", "gone")
	aiThrottle = apiErr("ThrottlingException", "slow down")
)

// aiCalls records, per op, the parent key of every child call.
type aiCalls map[string]*parentCalls

func (c aiCalls) add(op, key string) { c[op].add(key) }

func newAICalls() aiCalls {
	c := aiCalls{}
	for _, op := range []string{"ListComponents", "DescribeComponent", "ListWorkloads", "DescribeWorkload", "ListLogPatternSets", "ListLogPatterns"} {
		c[op] = &parentCalls{}
	}
	return c
}

// aiPageCapErr fails a child List call whose MaxResults is not the model cap.
func aiPageCapErr(maxResults *int32) error {
	if sdkaws.ToInt32(maxResults) != appInsightsPageLimit {
		return apiErr("ValidationException", "MaxResults must be between 1 and 40")
	}
	return nil
}

// aiHandlers serves two applications over two pages (rg-a; then rg-b and a
// nameless element). rg-a has components web and an EC2-instance-ARN-named one
// over two pages (plus a nameless element), rg-b has db; web has workloads w1
// and w2 over two pages, db has w3. rg-a has log pattern sets set1 and set2
// over two pages (plus an empty name), rg-b has setb; set1 has patterns p1 and
// p2 over two pages (plus a nameless one), set2 has p3, setb none. Every child
// call is recorded in calls under its parent key; an unknown parent is
// ResourceNotFoundException.
func aiHandlers(calls aiCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListApplications": func(in any) (any, error) {
			if in.(*applicationinsights.ListApplicationsInput).NextToken == nil {
				return &applicationinsights.ListApplicationsOutput{
					ApplicationInfoList: []aitypes.ApplicationInfo{{ResourceGroupName: sdkaws.String("rg-a")}},
					NextToken:           sdkaws.String("p2"),
				}, nil
			}
			return &applicationinsights.ListApplicationsOutput{ApplicationInfoList: []aitypes.ApplicationInfo{
				{ResourceGroupName: sdkaws.String("rg-b")}, {},
			}}, nil
		},
		"ListComponents": func(in any) (any, error) {
			p := in.(*applicationinsights.ListComponentsInput)
			rg := sdkaws.ToString(p.ResourceGroupName)
			calls.add("ListComponents", rg)
			if err := aiPageCapErr(p.MaxResults); err != nil {
				return nil, err
			}
			switch {
			case rg == "rg-a" && p.NextToken == nil:
				return &applicationinsights.ListComponentsOutput{
					ApplicationComponentList: []aitypes.ApplicationComponent{{ComponentName: sdkaws.String("web")}},
					NextToken:                sdkaws.String("p2"),
				}, nil
			case rg == "rg-a":
				return &applicationinsights.ListComponentsOutput{ApplicationComponentList: []aitypes.ApplicationComponent{
					{ComponentName: sdkaws.String(aiTestInstanceARN)}, {},
				}}, nil
			case rg == "rg-b":
				return &applicationinsights.ListComponentsOutput{ApplicationComponentList: []aitypes.ApplicationComponent{{ComponentName: sdkaws.String("db")}}}, nil
			}
			return nil, aiGone
		},
		"DescribeComponent": func(in any) (any, error) {
			p := in.(*applicationinsights.DescribeComponentInput)
			name := sdkaws.ToString(p.ComponentName)
			calls.add("DescribeComponent", sdkaws.ToString(p.ResourceGroupName)+"/"+name)
			return &applicationinsights.DescribeComponentOutput{
				ApplicationComponent: &aitypes.ApplicationComponent{ComponentName: sdkaws.String(name), Monitor: sdkaws.Bool(true), Tier: aitypes.TierDotNetWeb},
				ResourceList:         []string{"arn:aws:ec2:us-east-1:123456789012:instance/i-z-" + name, "arn:aws:ec2:us-east-1:123456789012:instance/i-a-" + name},
			}, nil
		},
		"ListWorkloads": func(in any) (any, error) {
			p := in.(*applicationinsights.ListWorkloadsInput)
			key := sdkaws.ToString(p.ResourceGroupName) + "/" + sdkaws.ToString(p.ComponentName)
			calls.add("ListWorkloads", key)
			if err := aiPageCapErr(p.MaxResults); err != nil {
				return nil, err
			}
			switch {
			case key == "rg-a/web" && p.NextToken == nil:
				return &applicationinsights.ListWorkloadsOutput{
					WorkloadList: []aitypes.Workload{{WorkloadId: sdkaws.String(aiTestW1), WorkloadName: sdkaws.String("sql1"), MissingWorkloadConfig: sdkaws.Bool(true)}},
					NextToken:    sdkaws.String("p2"),
				}, nil
			case key == "rg-a/web":
				return &applicationinsights.ListWorkloadsOutput{WorkloadList: []aitypes.Workload{{WorkloadId: sdkaws.String(aiTestW2)}, {WorkloadName: sdkaws.String("no-id")}}}, nil
			case key == "rg-b/db":
				return &applicationinsights.ListWorkloadsOutput{WorkloadList: []aitypes.Workload{{WorkloadId: sdkaws.String(aiTestW3)}}}, nil
			case key == "rg-a/"+aiTestInstanceARN:
				return &applicationinsights.ListWorkloadsOutput{}, nil
			}
			return nil, aiGone
		},
		"DescribeWorkload": func(in any) (any, error) {
			p := in.(*applicationinsights.DescribeWorkloadInput)
			id := sdkaws.ToString(p.WorkloadId)
			calls.add("DescribeWorkload", id)
			return &applicationinsights.DescribeWorkloadOutput{
				WorkloadId: sdkaws.String(id),
				WorkloadConfiguration: &aitypes.WorkloadConfiguration{
					WorkloadName: sdkaws.String("cfg-" + id), Tier: aitypes.TierSqlServer, Configuration: sdkaws.String(`{"SQLServerWorkload":{}}`),
				},
			}, nil
		},
		"ListLogPatternSets": func(in any) (any, error) {
			p := in.(*applicationinsights.ListLogPatternSetsInput)
			rg := sdkaws.ToString(p.ResourceGroupName)
			calls.add("ListLogPatternSets", rg)
			if err := aiPageCapErr(p.MaxResults); err != nil {
				return nil, err
			}
			switch {
			case rg == "rg-a" && p.NextToken == nil:
				return &applicationinsights.ListLogPatternSetsOutput{LogPatternSets: []string{"set1"}, NextToken: sdkaws.String("p2")}, nil
			case rg == "rg-a":
				return &applicationinsights.ListLogPatternSetsOutput{LogPatternSets: []string{"set2", ""}}, nil
			case rg == "rg-b":
				return &applicationinsights.ListLogPatternSetsOutput{LogPatternSets: []string{"setb"}}, nil
			}
			return nil, aiGone
		},
		"ListLogPatterns": func(in any) (any, error) {
			p := in.(*applicationinsights.ListLogPatternsInput)
			key := sdkaws.ToString(p.ResourceGroupName) + "/" + sdkaws.ToString(p.PatternSetName)
			calls.add("ListLogPatterns", key)
			if err := aiPageCapErr(p.MaxResults); err != nil {
				return nil, err
			}
			pattern := func(set, name string) aitypes.LogPattern {
				return aitypes.LogPattern{PatternSetName: sdkaws.String(set), PatternName: sdkaws.String(name), Pattern: sdkaws.String(".*ERROR.*"), Rank: 250000}
			}
			switch {
			case key == "rg-a/set1" && p.NextToken == nil:
				return &applicationinsights.ListLogPatternsOutput{LogPatterns: []aitypes.LogPattern{pattern("set1", "p1")}, NextToken: sdkaws.String("p2")}, nil
			case key == "rg-a/set1":
				return &applicationinsights.ListLogPatternsOutput{LogPatterns: []aitypes.LogPattern{pattern("set1", "p2"), {PatternSetName: sdkaws.String("set1")}}}, nil
			case key == "rg-a/set2":
				return &applicationinsights.ListLogPatternsOutput{LogPatterns: []aitypes.LogPattern{pattern("set2", "p3")}}, nil
			case key == "rg-b/setb":
				return &applicationinsights.ListLogPatternsOutput{}, nil
			}
			return nil, aiGone
		},
	}
}

func scanAITopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, aiDenied)
	total, _, err := scanApplicationInsights(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanApplicationInsights_StoresChildren(t *testing.T) {
	st := newTestStore(t)
	calls := newAICalls()
	total, err := scanAITopLevel(t, st, aiHandlers(calls))
	if err != nil {
		t.Fatalf("scanApplicationInsights: %v", err)
	}
	// 2 apps + 3 components + 3 workloads + 3 sets + 3 patterns
	if total != 14 {
		t.Errorf("total = %d, want 14", total)
	}
	for _, tc := range []struct {
		typ  string
		want []string
	}{
		{TypeApplicationInsightsApplication, []string{aiTestAppA, aiTestAppB}},
		{TypeApplicationInsightsComponent, []string{aiTestCompWeb, aiTestCompInst, aiTestCompDB}},
		{TypeApplicationInsightsWorkload, []string{aiTestWL1, aiTestWL2, aiTestWL3}},
		{TypeApplicationInsightsLogPatternSet, []string{aiTestSet1, aiTestSet2, aiTestSetB}},
		{TypeApplicationInsightsLogPattern, []string{aiTestP1, aiTestP2, aiTestP3}},
	} {
		want := slices.Sorted(slices.Values(tc.want))
		if got := nativeIDs(rowsByNativeID(t, st, tc.typ)); !slices.Equal(got, want) {
			t.Errorf("%s rows = %v, want %v", tc.typ, got, want)
		}
	}
	for op, want := range map[string][]string{
		"ListComponents":     {"rg-a", "rg-b"},
		"DescribeComponent":  {"rg-a/" + aiTestInstanceARN, "rg-a/web", "rg-b/db"},
		"ListWorkloads":      {"rg-a/" + aiTestInstanceARN, "rg-a/web", "rg-b/db"},
		"DescribeWorkload":   {aiTestW1, aiTestW2, aiTestW3},
		"ListLogPatternSets": {"rg-a", "rg-b"},
		"ListLogPatterns":    {"rg-a/set1", "rg-a/set2", "rg-b/setb"},
	} {
		if got := calls[op].distinct(); !slices.Equal(got, want) {
			t.Errorf("%s parents = %v, want %v", op, got, want)
		}
	}
	for _, pair := range [][2]string{
		{aiTestAppA, aiTestCompWeb},
		{aiTestAppA, aiTestCompInst},
		{aiTestAppB, aiTestCompDB},
		{aiTestCompWeb, aiTestWL1},
		{aiTestCompWeb, aiTestWL2},
		{aiTestCompDB, aiTestWL3},
		{aiTestAppA, aiTestSet1},
		{aiTestAppA, aiTestSet2},
		{aiTestAppB, aiTestSetB},
		{aiTestSet1, aiTestP1},
		{aiTestSet1, aiTestP2},
		{aiTestSet2, aiTestP3},
	} {
		assertContainsNative(t, st, pair[0], pair[1])
	}
}

func TestScanApplicationInsights_ChildRowContents(t *testing.T) {
	st := newTestStore(t)
	if _, err := scanAITopLevel(t, st, aiHandlers(newAICalls())); err != nil {
		t.Fatalf("scanApplicationInsights: %v", err)
	}
	for _, tc := range []struct {
		typ, nativeID, name string
		attrKey, attrWant   string
	}{
		{TypeApplicationInsightsComponent, aiTestCompInst, aiTestInstanceARN, "ResourceList", `["arn:aws:ec2:us-east-1:123456789012:instance/i-a-` + aiTestInstanceARN + `","arn:aws:ec2:us-east-1:123456789012:instance/i-z-` + aiTestInstanceARN + `"]`},
		{TypeApplicationInsightsWorkload, aiTestWL1, "sql1", "MissingWorkloadConfig", "true"},
		{TypeApplicationInsightsWorkload, aiTestWL1, "sql1", "WorkloadId", `"` + aiTestW1 + `"`},
		{TypeApplicationInsightsWorkload, aiTestWL1, "sql1", "WorkloadConfiguration", mustJSON(aitypes.WorkloadConfiguration{
			WorkloadName: sdkaws.String("cfg-" + aiTestW1), Tier: aitypes.TierSqlServer, Configuration: sdkaws.String(`{"SQLServerWorkload":{}}`),
		})},
		{TypeApplicationInsightsLogPatternSet, aiTestSet2, "set2", "PatternSetName", `"set2"`},
		{TypeApplicationInsightsLogPattern, aiTestP3, "p3", "Pattern", `".*ERROR.*"`},
	} {
		r, ok := rowsByNativeID(t, st, tc.typ)[tc.nativeID]
		if !ok {
			t.Errorf("%s %s missing", tc.typ, tc.nativeID)
			continue
		}
		if r.Type != tc.typ || sv(r.Region) != testRegion || sv(r.Name) != tc.name {
			t.Errorf("%s row = type %q region %q name %q; want name %q", tc.nativeID, r.Type, sv(r.Region), sv(r.Name), tc.name)
		}
		var attrs map[string]json.RawMessage
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			t.Fatalf("%s attrs: %v", tc.nativeID, err)
		}
		if got := string(attrs[tc.attrKey]); got != tc.attrWant {
			t.Errorf("%s attrs[%s] = %s, want %s", tc.nativeID, tc.attrKey, got, tc.attrWant)
		}
	}
	// A workload listed without a name gets no Name.
	if r := rowsByNativeID(t, st, TypeApplicationInsightsWorkload)[aiTestWL2]; r.Name != nil {
		t.Errorf("unnamed workload Name = %q, want nil", sv(r.Name))
	}
}

func TestScanApplicationInsights_NoChildren(t *testing.T) {
	st := newTestStore(t)
	calls := newAICalls()
	h := aiHandlers(calls)
	h["ListComponents"] = func(in any) (any, error) {
		calls.add("ListComponents", sdkaws.ToString(in.(*applicationinsights.ListComponentsInput).ResourceGroupName))
		return &applicationinsights.ListComponentsOutput{}, nil
	}
	h["ListLogPatternSets"] = func(in any) (any, error) {
		calls.add("ListLogPatternSets", sdkaws.ToString(in.(*applicationinsights.ListLogPatternSetsInput).ResourceGroupName))
		return &applicationinsights.ListLogPatternSetsOutput{}, nil
	}
	total, err := scanAITopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scanApplicationInsights: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2 (the applications)", total)
	}
	for _, typ := range []string{TypeApplicationInsightsComponent, TypeApplicationInsightsWorkload, TypeApplicationInsightsLogPatternSet, TypeApplicationInsightsLogPattern} {
		if n := len(rowsByNativeID(t, st, typ)); n != 0 {
			t.Errorf("%s rows = %d, want 0", typ, n)
		}
	}
	for _, op := range []string{"ListComponents", "ListLogPatternSets"} {
		if got := calls[op].distinct(); !slices.Equal(got, []string{"rg-a", "rg-b"}) {
			t.Errorf("%s parents = %v, want both applications", op, got)
		}
	}
}

// aiFailKey extracts the parent key aiHandlers records for op's input.
func aiFailKey(op string, in any) string {
	switch p := in.(type) {
	case *applicationinsights.ListComponentsInput:
		return sdkaws.ToString(p.ResourceGroupName)
	case *applicationinsights.DescribeComponentInput:
		return sdkaws.ToString(p.ResourceGroupName) + "/" + sdkaws.ToString(p.ComponentName)
	case *applicationinsights.ListWorkloadsInput:
		return sdkaws.ToString(p.ResourceGroupName) + "/" + sdkaws.ToString(p.ComponentName)
	case *applicationinsights.DescribeWorkloadInput:
		return sdkaws.ToString(p.WorkloadId)
	case *applicationinsights.ListLogPatternSetsInput:
		return sdkaws.ToString(p.ResourceGroupName)
	case *applicationinsights.ListLogPatternsInput:
		return sdkaws.ToString(p.ResourceGroupName) + "/" + sdkaws.ToString(p.PatternSetName)
	}
	panic("aiFailKey: unexpected input for " + op)
}

// One parent of a child call that was deleted since listing, denied, or
// failing transiently after retries drops only that parent's children: deleted
// is silent, a denial warns once however many parents hit it, and transient
// errors warn once. Any other error fails the phase (storing none of its rows)
// and is returned, while the other child family and the applications are
// still stored.
func TestScanApplicationInsights_FailedChildParent(t *testing.T) {
	type kind struct {
		name      string
		err       error
		all       bool // fail every parent, not only key
		wantWarns int
	}
	gone := kind{"parent gone", aiGone, false, 0}
	denied := kind{"denied everywhere", aiDenied, true, 1}
	throttled := kind{"throttled everywhere", aiThrottle, true, 1}
	other := kind{"other error", aiBadInput, false, 0}
	type phase struct {
		op, label, key, typ string
		wantGone            []string
		// sibling is a row of the other child family that must survive a hard error.
		siblingTyp, sibling string
	}
	for _, ph := range []phase{
		{"ListComponents", appInsightsListComponentsOp, "rg-a", TypeApplicationInsightsComponent, []string{aiTestCompDB}, TypeApplicationInsightsLogPattern, aiTestP1},
		{"DescribeComponent", appInsightsDescribeComponentOp, "rg-a/web", TypeApplicationInsightsComponent, []string{aiTestCompInst, aiTestCompDB}, TypeApplicationInsightsLogPattern, aiTestP1},
		{"ListWorkloads", appInsightsListWorkloadsOp, "rg-a/web", TypeApplicationInsightsWorkload, []string{aiTestWL3}, TypeApplicationInsightsLogPattern, aiTestP1},
		{"DescribeWorkload", appInsightsDescribeWorkloadOp, aiTestW1, TypeApplicationInsightsWorkload, []string{aiTestWL2, aiTestWL3}, TypeApplicationInsightsLogPattern, aiTestP1},
		{"ListLogPatternSets", appInsightsListLogPatternSetsOp, "rg-a", TypeApplicationInsightsLogPatternSet, []string{aiTestSetB}, TypeApplicationInsightsWorkload, aiTestWL1},
		{"ListLogPatterns", appInsightsListLogPatternsOp, "rg-a/set1", TypeApplicationInsightsLogPattern, []string{aiTestP3}, TypeApplicationInsightsWorkload, aiTestWL1},
	} {
		for _, k := range []kind{gone, denied, throttled, other} {
			t.Run(ph.op+"/"+k.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, ph.label)
				h := aiHandlers(newAICalls())
				next := h[ph.op]
				h[ph.op] = func(in any) (any, error) {
					if k.all || aiFailKey(ph.op, in) == ph.key {
						return nil, k.err
					}
					return next(in)
				}
				_, err := scanAITopLevel(t, st, h)
				var wantRows []string
				switch k {
				case other:
					assertAPICode(t, err, "ValidationException")
					if isTransientNetworkError(err) {
						t.Errorf("err = %v reads as transient, want a hard error", err)
					}
					if _, ok := rowsByNativeID(t, st, ph.siblingTyp)[ph.sibling]; !ok {
						t.Errorf("%s %s missing; a hard error in %s must not stop the other child family", ph.siblingTyp, ph.sibling, ph.op)
					}
				case gone:
					wantRows = ph.wantGone
					fallthrough
				default:
					if err != nil {
						t.Fatalf("err = %v, want nil", err)
					}
				}
				if got := nativeIDs(rowsByNativeID(t, st, ph.typ)); !slices.Equal(got, wantRows) {
					t.Errorf("%s rows = %v, want %v", ph.typ, got, wantRows)
				}
				if *warns != k.wantWarns {
					t.Errorf("%s warnings = %d, want %d", ph.label, *warns, k.wantWarns)
				}
				if n := len(rowsByNativeID(t, st, TypeApplicationInsightsApplication)); n != 2 {
					t.Errorf("applications = %d, want 2", n)
				}
			})
		}
	}
}

// The workload phase fans out only over components that were stored: a
// component that vanished between ListComponents and DescribeComponent gets no
// ListWorkloads call, so no workload is stored under a missing parent row.
func TestScanApplicationInsights_WorkloadsOnlyUnderStoredComponents(t *testing.T) {
	st := newTestStore(t)
	calls := newAICalls()
	h := aiHandlers(calls)
	next := h["DescribeComponent"]
	h["DescribeComponent"] = func(in any) (any, error) {
		if sdkaws.ToString(in.(*applicationinsights.DescribeComponentInput).ComponentName) == "web" {
			return nil, aiGone
		}
		return next(in)
	}
	if _, err := scanAITopLevel(t, st, h); err != nil {
		t.Fatalf("scanApplicationInsights: %v", err)
	}
	if got := calls["ListWorkloads"].distinct(); !slices.Equal(got, []string{"rg-a/" + aiTestInstanceARN, "rg-b/db"}) {
		t.Errorf("ListWorkloads parents = %v, want only the stored components", got)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeApplicationInsightsWorkload)); !slices.Equal(got, []string{aiTestWL3}) {
		t.Errorf("workload rows = %v, want only db's", got)
	}
}

// A hard error on the second ListApplications page still lets the child
// phases run over the application stored from the first page, and is returned.
func TestScanApplicationInsights_ApplicationsErrorStillRunsChildren(t *testing.T) {
	st := newTestStore(t)
	h := aiHandlers(newAICalls())
	next := h["ListApplications"]
	h["ListApplications"] = func(in any) (any, error) {
		if in.(*applicationinsights.ListApplicationsInput).NextToken != nil {
			return nil, aiBadInput
		}
		return next(in)
	}
	total, err := scanAITopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	// app A + components web, inst + workloads w1, w2 + sets 1, 2 + patterns p1, p2, p3
	if total != 10 {
		t.Errorf("total = %d, want 10", total)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeApplicationInsightsComponent)); !slices.Equal(got, []string{aiTestCompInst, aiTestCompWeb}) {
		t.Errorf("component rows = %v, want rg-a's", got)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeApplicationInsightsLogPattern)); !slices.Equal(got, []string{aiTestP1, aiTestP2, aiTestP3}) {
		t.Errorf("pattern rows = %v, want rg-a's", got)
	}
}

// A transient ListApplications error is demoted to a warning so that a child
// phase's hard error still reaches the dispatcher as a hard error.
func TestScanApplicationInsights_ApplicationsTransientDoesNotMaskHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "applicationinsights:ListApplications")
	h := aiHandlers(newAICalls())
	next := h["ListApplications"]
	h["ListApplications"] = func(in any) (any, error) {
		if in.(*applicationinsights.ListApplicationsInput).NextToken != nil {
			return nil, aiThrottle
		}
		return next(in)
	}
	h["ListLogPatterns"] = func(any) (any, error) { return nil, aiBadInput }
	_, err := scanAITopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	if isTransientNetworkError(err) {
		t.Errorf("err = %v reads as transient; the hard pattern error must not be masked", err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
	}
}

func TestScanApplicationInsights_ApplicationsDenied(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "applicationinsights:ListApplications")
	calls := newAICalls()
	h := aiHandlers(calls)
	h["ListApplications"] = func(any) (any, error) { return nil, aiDenied }
	total, err := scanAITopLevel(t, st, h)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if total != 0 || *warns != 1 {
		t.Errorf("total = %d, warnings = %d; want 0 and 1", total, *warns)
	}
	if got := calls["ListComponents"].distinct(); len(got) != 0 {
		t.Errorf("ListComponents parents = %v, want none", got)
	}
}

// The two child families run concurrently: ListComponents waits for the
// pattern family's ListLogPatternSets call, which a sequential run would only
// make after the whole component family returned.
func TestScanApplicationInsights_FamiliesRunConcurrently(t *testing.T) {
	st := newTestStore(t)
	h := aiHandlers(newAICalls())
	setsCalled := make(chan struct{})
	var once sync.Once
	nextSets, nextComps := h["ListLogPatternSets"], h["ListComponents"]
	h["ListLogPatternSets"] = func(in any) (any, error) {
		once.Do(func() { close(setsCalled) })
		return nextSets(in)
	}
	h["ListComponents"] = func(in any) (any, error) {
		select {
		case <-setsCalled:
			return nextComps(in)
		case <-time.After(10 * time.Second):
			return nil, aiBadInput
		}
	}
	if _, err := scanAITopLevel(t, st, h); err != nil {
		t.Fatalf("scanApplicationInsights: %v; the component family ran before the pattern family started", err)
	}
	if n := len(rowsByNativeID(t, st, TypeApplicationInsightsComponent)); n != 3 {
		t.Errorf("component rows = %d, want 3", n)
	}
}
