package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/drs"
	drstypes "github.com/aws/aws-sdk-go-v2/service/drs/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	drsDenied    = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: drs:List"}
	drsBadInput  = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	drsGone      = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "not found"}
	drsThrottled = apiErr("ThrottlingException", "Rate exceeded")
)

func drsTestARN(kind, id string) string { return drsARN(testRegion, testAccountID, kind, id) }

func drsActionNativeID(parentARN, actionID string) string {
	return parentARN + "/launch-action/" + actionID
}

func nativeIDs(rows map[string]store.Resource) []string {
	out := make([]string, 0, len(rows))
	for id := range rows {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// drsHasCode reports whether any member of err carries the API error code.
func drsHasCode(err error, code string) bool {
	return err != nil && !everyMember(err, func(m error) bool { return !isAPIErrorCode(m, code) })
}

// drsPinPage fails a stub call whose NextToken or MaxResults is not the one
// the page it serves needs: no token on page one, "p2" after it, and
// maxResults (nil for an op whose model sets no cap).
func drsPinPage(token *string, maxResults *int32, want *int32) error {
	if t := sv(token); t != "" && t != "p2" {
		return fmt.Errorf("unexpected NextToken %q", t)
	}
	if (maxResults == nil) != (want == nil) || (want != nil && *maxResults != *want) {
		return fmt.Errorf("MaxResults = %v, want %v", maxResults, want)
	}
	return nil
}

var drsMax = ptrInt32(drsPageMax)

// drsPlanSummary is the list summary of plan id.
func drsPlanSummary(id string) drstypes.RecoveryPlanSummary {
	return drstypes.RecoveryPlanSummary{RecoveryPlanArn: ptrStr(drsTestARN("recovery-plan", id)), Name: ptrStr("summary-" + id), Status: drstypes.RecoveryPlanStatusActive}
}

// drsPlan is the full plan GetRecoveryPlan returns for id: plan1 is active
// and tagged, plan2 has no status and no tags.
func drsPlan(id string) *drstypes.RecoveryPlan {
	p := &drstypes.RecoveryPlan{RecoveryPlanArn: ptrStr(drsTestARN("recovery-plan", id)), Name: ptrStr("name-" + id), Description: ptrStr("desc")}
	if id == "plan1" {
		p.Status, p.Tags = drstypes.RecoveryPlanStatusActive, map[string]string{"env": "dr"}
	}
	return p
}

// drsPlanHandlers serves ListRecoveryPlans over two pages (plan1; then plan2
// and an element with no ARN, or fail) and GetRecoveryPlan for any plan, with
// the plan in getFail returning that error.
func drsPlanHandlers(listFail error, getFail map[string]error) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListRecoveryPlans": func(in any) (any, error) {
			p := in.(*drs.ListRecoveryPlansInput)
			if err := drsPinPage(p.NextToken, p.MaxResults, drsMax); err != nil {
				return nil, err
			}
			if p.NextToken == nil {
				return &drs.ListRecoveryPlansOutput{RecoveryPlans: []drstypes.RecoveryPlanSummary{drsPlanSummary("plan1")}, NextToken: ptrStr("p2")}, nil
			}
			if listFail != nil {
				return nil, listFail
			}
			return &drs.ListRecoveryPlansOutput{RecoveryPlans: []drstypes.RecoveryPlanSummary{drsPlanSummary("plan2"), {Name: ptrStr("no-arn")}}}, nil
		},
		"GetRecoveryPlan": func(in any) (any, error) {
			arn := sv(in.(*drs.GetRecoveryPlanInput).RecoveryPlanArn)
			if err := getFail[arn]; err != nil {
				return nil, err
			}
			return &drs.GetRecoveryPlanOutput{RecoveryPlan: drsPlan(strings.TrimPrefix(arn, drsTestARN("recovery-plan", "")))}, nil
		},
	}
}

func scanDRSPlans(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, []childParent, error) {
	t.Helper()
	client := drs.NewFromConfig(opStubConfig(handlers, drsDenied))
	total, _, parents, err := scanDRSRecoveryPlans(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, parents, err
}

func parentIDs(ps []childParent) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.id)
	}
	sort.Strings(out)
	return out
}

// assertDRSRow checks a stored row's name, status ("" for unset), attributes
// and tags (nil wantTags: no tags stored).
func assertDRSRow(t *testing.T, row store.Resource, name, status string, attrs any, wantTags map[string]string) {
	t.Helper()
	if sv(row.Name) != name || sv(row.Status) != status || (status == "") != (row.Status == nil) || sv(row.Region) != testRegion || row.AttributesJSON != mustJSON(attrs) {
		t.Errorf("%s: name/status/region = %q/%q/%q, attrs = %s; want %q/%q/%q, %s",
			row.NativeID, sv(row.Name), sv(row.Status), sv(row.Region), row.AttributesJSON, name, status, testRegion, mustJSON(attrs))
	}
	if wantTags == nil {
		if row.TagsJSON != nil {
			t.Errorf("%s: tags = %s, want none", row.NativeID, *row.TagsJSON)
		}
		return
	}
	assertTagsMap(t, row.TagsJSON, wantTags)
}

func TestScanDRSRecoveryPlans_StoresEveryPageSkipsARNless(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, drsRecoveryPlansOp)
	total, parents, err := scanDRSPlans(t, st, drsPlanHandlers(nil, nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	plan1, plan2 := drsTestARN("recovery-plan", "plan1"), drsTestARN("recovery-plan", "plan2")
	rows := rowsByNativeID(t, st, TypeDRSRecoveryPlanResource)
	if want := []string{plan1, plan2}; total != 2 || !slices.Equal(nativeIDs(rows), want) || !slices.Equal(parentIDs(parents), want) {
		t.Fatalf("total = %d, rows = %v, parents = %v; want %v", total, nativeIDs(rows), parentIDs(parents), want)
	}
	assertDRSRow(t, rows[plan1], "name-plan1", "ACTIVE", drsPlan("plan1"), map[string]string{"env": "dr"})
	assertDRSRow(t, rows[plan2], "name-plan2", "", drsPlan("plan2"), nil)
	if *warns != 1 {
		t.Errorf("%s warnings = %d, want 1 for the plan with no ARN", drsRecoveryPlansOp, *warns)
	}
}

func TestScanDRSRecoveryPlans_Empty(t *testing.T) {
	st := newTestStore(t)
	var gets atomic.Int32
	total, parents, err := scanDRSPlans(t, st, map[string]func(in any) (any, error){
		"ListRecoveryPlans": func(any) (any, error) { return &drs.ListRecoveryPlansOutput{}, nil },
		"GetRecoveryPlan": func(any) (any, error) {
			gets.Add(1)
			return &drs.GetRecoveryPlanOutput{}, nil
		},
	})
	if err != nil || total != 0 || len(parents) != 0 || gets.Load() != 0 || len(rowsByNativeID(t, st, TypeDRSRecoveryPlanResource)) != 0 {
		t.Errorf("total = %d, parents = %v, gets = %d, err = %v; want nothing, nil", total, parents, gets.Load(), err)
	}
}

// A failed page two keeps page one's plan: a denial warns once, any other
// error is returned after the plan is stored.
func TestScanDRSRecoveryPlans_FailedListPage(t *testing.T) {
	want := []string{drsTestARN("recovery-plan", "plan1")}
	for _, tc := range []struct {
		name     string
		err      error
		wantCode string
		wantWarn int
	}{
		{"denied", drsDenied, "", 1},
		{"other", drsBadInput, "ValidationException", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, drsRecoveryPlansOp)
			total, parents, err := scanDRSPlans(t, st, drsPlanHandlers(tc.err, nil))
			if tc.wantCode == "" && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
			}
			if *warns != tc.wantWarn {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarn)
			}
			if total != 1 || !slices.Equal(nativeIDs(rowsByNativeID(t, st, TypeDRSRecoveryPlanResource)), want) || !slices.Equal(parentIDs(parents), want) {
				t.Errorf("total = %d, parents = %v; want page one's plan1 stored", total, parentIDs(parents))
			}
		})
	}
}

// A plan GetRecoveryPlan no longer finds is not stored. A plan whose read
// fails any other way is stored from its list summary, without tags, and
// stays a parent of the step phase: a denial warns once, a transient error is
// returned as transient, any other error is returned.
func TestScanDRSRecoveryPlans_FailedGet(t *testing.T) {
	plan1, plan2 := drsTestARN("recovery-plan", "plan1"), drsTestARN("recovery-plan", "plan2")
	t.Run("plan gone", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, drsGetRecoveryPlanOp)
		total, parents, err := scanDRSPlans(t, st, drsPlanHandlers(nil, map[string]error{plan1: drsGone}))
		want := []string{plan2}
		if err != nil || *warns != 0 || total != 1 || !slices.Equal(nativeIDs(rowsByNativeID(t, st, TypeDRSRecoveryPlanResource)), want) || !slices.Equal(parentIDs(parents), want) {
			t.Errorf("err = %v, warnings = %d, total = %d, parents = %v; want nil, 0, only plan2", err, *warns, total, parentIDs(parents))
		}
	})
	for _, tc := range []struct {
		name     string
		err      error
		wantWarn int
		check    func(error) bool
	}{
		{"denied", drsDenied, 1, func(err error) bool { return err == nil }},
		{"transient", drsThrottled, 0, isTransientNetworkError},
		{"other", drsBadInput, 0, func(err error) bool { return isAPIErrorCode(err, "ValidationException") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, drsGetRecoveryPlanOp)
			total, parents, err := scanDRSPlans(t, st, drsPlanHandlers(nil, map[string]error{plan1: tc.err}))
			if !tc.check(err) {
				t.Errorf("err = %v", err)
			}
			if *warns != tc.wantWarn {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarn)
			}
			want := []string{plan1, plan2}
			rows := rowsByNativeID(t, st, TypeDRSRecoveryPlanResource)
			if total != 2 || !slices.Equal(nativeIDs(rows), want) || !slices.Equal(parentIDs(parents), want) {
				t.Fatalf("total = %d, rows = %v, parents = %v; want %v", total, nativeIDs(rows), parentIDs(parents), want)
			}
			assertDRSRow(t, rows[plan1], "summary-plan1", "ACTIVE", drsPlanSummary("plan1"), nil)
			assertDRSRow(t, rows[plan2], "name-plan2", "", drsPlan("plan2"), nil)
		})
	}
	t.Run("empty response", func(t *testing.T) {
		st := newTestStore(t)
		h := drsPlanHandlers(nil, nil)
		get := h["GetRecoveryPlan"]
		h["GetRecoveryPlan"] = func(in any) (any, error) {
			if sv(in.(*drs.GetRecoveryPlanInput).RecoveryPlanArn) == plan1 {
				return &drs.GetRecoveryPlanOutput{}, nil
			}
			return get(in)
		}
		_, _, err := scanDRSPlans(t, st, h)
		if err == nil {
			t.Errorf("err = nil, want an error for the plan the response left out")
		}
		rows := rowsByNativeID(t, st, TypeDRSRecoveryPlanResource)
		if len(rows) != 2 {
			t.Fatalf("rows = %v, want plan1 and plan2", nativeIDs(rows))
		}
		assertDRSRow(t, rows[plan1], "summary-plan1", "ACTIVE", drsPlanSummary("plan1"), nil)
	})
}

// drsListedFixture is one listed phase's three elements: one tagged on page
// one, and on page two one without tags and one without an ARN.
type drsListedFixture struct {
	op, typ                       string
	tagged, plain                 any
	taggedName, plainName, status string
}

func drsListedFixtures() []drsListedFixture {
	tags := map[string]string{"team": "dr"}
	ss := drstypes.SourceServer{Arn: ptrStr(drsTestARN("source-server", "s-tag")), SourceServerID: ptrStr("s-tag"), Tags: tags}
	ri := drstypes.RecoveryInstance{Arn: ptrStr(drsTestARN("recovery-instance", "ri-tag")), RecoveryInstanceID: ptrStr("ri-tag"), Ec2InstanceState: drstypes.EC2InstanceStateRunning, Tags: tags}
	sn := drstypes.SourceNetwork{Arn: ptrStr(drsTestARN("source-network", "sn-tag")), SourceNetworkID: ptrStr("sn-tag"), Tags: tags}
	rt := drstypes.ReplicationConfigurationTemplate{
		Arn: ptrStr(drsTestARN("replication-configuration-template", "rct-tag")), ReplicationConfigurationTemplateID: ptrStr("rct-tag"),
		Tags: tags, StagingAreaTags: map[string]string{"staging": "yes"},
	}
	lt := drstypes.LaunchConfigurationTemplate{Arn: ptrStr(drsTestARN("launch-configuration-template", "lct-tag")), LaunchConfigurationTemplateID: ptrStr("lct-tag"), Tags: tags}
	return []drsListedFixture{
		{
			op: "DescribeSourceServers", typ: TypeDRSSourceServerResource, tagged: ss, taggedName: "s-tag",
			plain:     drstypes.SourceServer{Arn: ptrStr(drsTestARN("source-server", "s-plain")), SourceServerID: ptrStr("s-plain")},
			plainName: "s-plain",
		},
		{
			op: "DescribeRecoveryInstances", typ: TypeDRSRecoveryInstanceResource, tagged: ri, taggedName: "ri-tag", status: "RUNNING",
			plain:     drstypes.RecoveryInstance{Arn: ptrStr(drsTestARN("recovery-instance", "ri-plain")), RecoveryInstanceID: ptrStr("ri-plain")},
			plainName: "ri-plain",
		},
		{
			op: "DescribeSourceNetworks", typ: TypeDRSSourceNetworkResource, tagged: sn, taggedName: "sn-tag",
			plain:     drstypes.SourceNetwork{Arn: ptrStr(drsTestARN("source-network", "sn-plain")), SourceNetworkID: ptrStr("sn-plain")},
			plainName: "sn-plain",
		},
		{
			op: "DescribeReplicationConfigurationTemplates", typ: TypeDRSReplicationConfigurationTemplateResource, tagged: rt, taggedName: "rct-tag",
			plain: drstypes.ReplicationConfigurationTemplate{
				Arn: ptrStr(drsTestARN("replication-configuration-template", "rct-plain")), ReplicationConfigurationTemplateID: ptrStr("rct-plain"),
			},
			plainName: "rct-plain",
		},
		{
			op: "DescribeLaunchConfigurationTemplates", typ: TypeDRSLaunchConfigurationTemplateResource, tagged: lt, taggedName: "lct-tag",
			plain: drstypes.LaunchConfigurationTemplate{
				Arn: ptrStr(drsTestARN("launch-configuration-template", "lct-plain")), LaunchConfigurationTemplateID: ptrStr("lct-plain"),
			},
			plainName: "lct-plain",
		},
	}
}

// drsListedHandlers serves the five listed phases over two pages per
// drsListedFixtures, page two failing with fail[op] when set. Each handler
// pins the request: no filter, the documented MaxResults cap (only
// DescribeLaunchConfigurationTemplates models one), and the page token.
// ListLaunchActions answers every parent with no actions, recording it.
func drsListedHandlers(fail map[string]error, actionCalls *parentCalls) map[string]func(in any) (any, error) {
	fx := map[string]drsListedFixture{}
	for _, f := range drsListedFixtures() {
		fx[f.op] = f
	}
	page := func(op string, token *string, maxResults, wantMax *int32, filtered bool) (items []any, next *string, err error) {
		if err := drsPinPage(token, maxResults, wantMax); err != nil {
			return nil, nil, err
		}
		if filtered {
			return nil, nil, fmt.Errorf("%s: unexpected filter", op)
		}
		if token == nil {
			return []any{fx[op].tagged}, ptrStr("p2"), nil
		}
		if err := fail[op]; err != nil {
			return nil, nil, err
		}
		return []any{fx[op].plain, nil}, nil, nil
	}
	return map[string]func(in any) (any, error){
		"DescribeSourceServers": func(in any) (any, error) {
			p := in.(*drs.DescribeSourceServersInput)
			items, next, err := page("DescribeSourceServers", p.NextToken, p.MaxResults, nil, p.Filters != nil)
			if err != nil {
				return nil, err
			}
			out := &drs.DescribeSourceServersOutput{NextToken: next}
			for _, it := range items {
				if it == nil {
					// No ARN; and a server with an ARN but no id, stored without
					// its launch actions or replication configuration.
					out.Items = append(out.Items, drstypes.SourceServer{SourceServerID: ptrStr("s-noarn")},
						drstypes.SourceServer{Arn: ptrStr(drsTestARN("source-server", "s-noid"))})
					continue
				}
				out.Items = append(out.Items, it.(drstypes.SourceServer))
			}
			return out, nil
		},
		"DescribeRecoveryInstances": func(in any) (any, error) {
			p := in.(*drs.DescribeRecoveryInstancesInput)
			items, next, err := page("DescribeRecoveryInstances", p.NextToken, p.MaxResults, nil, p.Filters != nil)
			if err != nil {
				return nil, err
			}
			out := &drs.DescribeRecoveryInstancesOutput{NextToken: next}
			for _, it := range items {
				ri, _ := it.(drstypes.RecoveryInstance)
				out.Items = append(out.Items, ri)
			}
			return out, nil
		},
		"DescribeSourceNetworks": func(in any) (any, error) {
			p := in.(*drs.DescribeSourceNetworksInput)
			items, next, err := page("DescribeSourceNetworks", p.NextToken, p.MaxResults, nil, p.Filters != nil)
			if err != nil {
				return nil, err
			}
			out := &drs.DescribeSourceNetworksOutput{NextToken: next}
			for _, it := range items {
				sn, _ := it.(drstypes.SourceNetwork)
				out.Items = append(out.Items, sn)
			}
			return out, nil
		},
		"DescribeReplicationConfigurationTemplates": func(in any) (any, error) {
			p := in.(*drs.DescribeReplicationConfigurationTemplatesInput)
			items, next, err := page("DescribeReplicationConfigurationTemplates", p.NextToken, p.MaxResults, nil, p.ReplicationConfigurationTemplateIDs != nil)
			if err != nil {
				return nil, err
			}
			out := &drs.DescribeReplicationConfigurationTemplatesOutput{NextToken: next}
			for _, it := range items {
				rt, _ := it.(drstypes.ReplicationConfigurationTemplate)
				out.Items = append(out.Items, rt)
			}
			return out, nil
		},
		"DescribeLaunchConfigurationTemplates": func(in any) (any, error) {
			p := in.(*drs.DescribeLaunchConfigurationTemplatesInput)
			items, next, err := page("DescribeLaunchConfigurationTemplates", p.NextToken, p.MaxResults, drsMax, p.LaunchConfigurationTemplateIDs != nil)
			if err != nil {
				return nil, err
			}
			out := &drs.DescribeLaunchConfigurationTemplatesOutput{NextToken: next}
			for _, it := range items {
				lt, _ := it.(drstypes.LaunchConfigurationTemplate)
				out.Items = append(out.Items, lt)
			}
			return out, nil
		},
		"ListLaunchActions": func(in any) (any, error) {
			actionCalls.add(sv(in.(*drs.ListLaunchActionsInput).ResourceId))
			return &drs.ListLaunchActionsOutput{}, nil
		},
	}
}

// Every listed phase stores each element under its ARN, named by its id,
// with its attributes verbatim and its own tags; an element with no ARN is
// warned about once and not stored. A source server with no id is stored,
// warned about, and given no child calls.
func TestScanDRS_ListedPhasesStoreTags(t *testing.T) {
	st := newTestStore(t)
	warns := map[string]*int{}
	for _, f := range drsListedFixtures() {
		warns[f.op] = countOpWarnings(st, "drs:"+f.op)
	}
	var actionCalls parentCalls
	if err := scanDRSTopLevel(t, st, drsListedHandlers(nil, &actionCalls)); err != nil {
		t.Fatalf("scanDRS: %v", err)
	}
	for _, f := range drsListedFixtures() {
		t.Run(f.op, func(t *testing.T) {
			rows := rowsByNativeID(t, st, f.typ)
			byName := map[string]store.Resource{}
			for _, r := range rows {
				byName[sv(r.Name)] = r
			}
			wantRows, wantWarns := 2, 1
			if f.op == "DescribeSourceServers" {
				wantRows, wantWarns = 3, 2
			}
			if len(rows) != wantRows {
				t.Fatalf("rows = %v, want %d", nativeIDs(rows), wantRows)
			}
			assertDRSRow(t, byName[f.taggedName], f.taggedName, f.status, f.tagged, map[string]string{"team": "dr"})
			assertDRSRow(t, byName[f.plainName], f.plainName, "", f.plain, nil)
			if *warns[f.op] != wantWarns {
				t.Errorf("warnings = %d, want %d", *warns[f.op], wantWarns)
			}
		})
	}
	if got := actionCalls.distinct(); !slices.Equal(got, []string{"lct-plain", "lct-tag", "s-plain", "s-tag"}) {
		t.Errorf("launch actions listed for %v, want the templates and the servers with an id", got)
	}
}

// A failed page two keeps page one's row in every listed phase: a denial
// warns once, any other error is returned after storing.
func TestScanDRS_ListedPhaseFailedPageKeepsPageOne(t *testing.T) {
	for _, f := range drsListedFixtures() {
		for _, tc := range []struct {
			name     string
			err      error
			wantCode string
		}{
			{"denied", drsDenied, ""},
			{"other", drsBadInput, "ValidationException"},
		} {
			t.Run(f.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "drs:"+f.op)
				var actionCalls parentCalls
				err := scanDRSTopLevel(t, st, drsListedHandlers(map[string]error{f.op: tc.err}, &actionCalls))
				if tc.wantCode == "" && err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if tc.wantCode != "" && !drsHasCode(err, tc.wantCode) {
					t.Fatalf("err = %v, want %s", err, tc.wantCode)
				}
				if tc.wantCode == "" && *warns != 1 {
					t.Errorf("warnings = %d, want 1 for the denial", *warns)
				}
				rows := rowsByNativeID(t, st, f.typ)
				if len(rows) != 1 {
					t.Fatalf("rows = %v, want page one's row only", nativeIDs(rows))
				}
				for _, r := range rows {
					assertDRSRow(t, r, f.taggedName, f.status, f.tagged, map[string]string{"team": "dr"})
				}
			})
		}
	}
}

// drsTopLevelHandlers answers source servers s-1 and s-2, launch template
// lct-1 (the last pre-existing phase), the plan handlers, and:
//   - ListRecoveryPlanSteps: plan1 over two pages (a server step on s-1; a
//     wait step, a step with a union member newer than the SDK, and an element
//     with no ARN), plan2 none.
//   - ListLaunchActions: lct-1 over two pages (a1; a2 and an element with no
//     id), s-1 the same id a1, s-2 none.
//
// Each list records the parent it was called for. Every other op is denied and
// warn-skipped by its phase.
func drsTopLevelHandlers(stepCalls, actionCalls *parentCalls) map[string]func(in any) (any, error) {
	h := drsPlanHandlers(nil, nil)
	h["DescribeSourceServers"] = func(any) (any, error) {
		return &drs.DescribeSourceServersOutput{Items: []drstypes.SourceServer{
			{Arn: ptrStr(drsTestARN("source-server", "s-1")), SourceServerID: ptrStr("s-1")},
			{Arn: ptrStr(drsTestARN("source-server", "s-2")), SourceServerID: ptrStr("s-2")},
		}}, nil
	}
	h["DescribeLaunchConfigurationTemplates"] = func(any) (any, error) {
		return &drs.DescribeLaunchConfigurationTemplatesOutput{Items: []drstypes.LaunchConfigurationTemplate{
			{Arn: ptrStr(drsTestARN("launch-configuration-template", "lct-1")), LaunchConfigurationTemplateID: ptrStr("lct-1")},
		}}, nil
	}
	h["ListRecoveryPlanSteps"] = func(in any) (any, error) {
		p := in.(*drs.ListRecoveryPlanStepsInput)
		if err := drsPinPage(p.NextToken, p.MaxResults, drsMax); err != nil {
			return nil, err
		}
		plan := sv(p.RecoveryPlanArn)
		stepCalls.add(plan)
		if plan != drsTestARN("recovery-plan", "plan1") {
			return &drs.ListRecoveryPlanStepsOutput{}, nil
		}
		if p.NextToken == nil {
			return &drs.ListRecoveryPlanStepsOutput{NextToken: ptrStr("p2"), RecoveryPlanSteps: []drstypes.RecoveryPlanStep{{
				RecoveryPlanStepArn: ptrStr(drsTestARN("recovery-plan-step", "step1")), StepName: ptrStr("servers"),
				Configuration: &drstypes.RecoveryPlanStepConfigurationMemberServerStepConfiguration{Value: drstypes.ServerStepConfiguration{
					Servers: []drstypes.RecoveryPlanServer{{ServerArn: ptrStr(drsTestARN("source-server", "s-1"))}},
				}},
			}}}, nil
		}
		return &drs.ListRecoveryPlanStepsOutput{RecoveryPlanSteps: []drstypes.RecoveryPlanStep{
			{
				RecoveryPlanStepArn: ptrStr(drsTestARN("recovery-plan-step", "step2")), StepName: ptrStr("wait"),
				Configuration: &drstypes.RecoveryPlanStepConfigurationMemberWaitStepConfiguration{Value: drstypes.WaitStepConfiguration{WaitDurationMinutes: ptrInt32(5)}},
			},
			{
				RecoveryPlanStepArn: ptrStr(drsTestARN("recovery-plan-step", "step3")), StepName: ptrStr("future"),
				Configuration: &drstypes.UnknownUnionMember{Tag: "futureStepConfiguration", Value: []byte(`{"x":1}`)},
			},
			{StepName: ptrStr("no-arn")},
		}}, nil
	}
	h["ListLaunchActions"] = func(in any) (any, error) {
		p := in.(*drs.ListLaunchActionsInput)
		if err := drsPinPage(p.NextToken, p.MaxResults, drsMax); err != nil {
			return nil, err
		}
		if p.Filters != nil {
			return nil, errors.New("unexpected launch action filter")
		}
		id := sv(p.ResourceId)
		actionCalls.add(id)
		switch {
		case id == "s-1":
			return &drs.ListLaunchActionsOutput{Items: []drstypes.LaunchAction{{ActionId: ptrStr("a1"), Name: ptrStr("server-a1")}}}, nil
		case id != "lct-1":
			return &drs.ListLaunchActionsOutput{}, nil
		case p.NextToken == nil:
			return &drs.ListLaunchActionsOutput{NextToken: ptrStr("p2"), Items: []drstypes.LaunchAction{{ActionId: ptrStr("a1"), Name: ptrStr("template-a1"), Category: drstypes.LaunchActionCategorySecurity}}}, nil
		default:
			return &drs.ListLaunchActionsOutput{Items: []drstypes.LaunchAction{{ActionId: ptrStr("a2"), Name: ptrStr("template-a2")}, {Name: ptrStr("no-id")}}}, nil
		}
	}
	return h
}

func ptrInt32(v int32) *int32 { return &v }

func scanDRSTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) error {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, drsDenied)
	_, _, err := scanDRS(context.Background(), acct, testRegion, st, testScanID)
	return err
}

func TestScanDRS_StoresRecoveryPlanStepsUnderTheirPlan(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, drsRecoveryPlanStepsOp)
	var stepCalls, actionCalls parentCalls
	if err := scanDRSTopLevel(t, st, drsTopLevelHandlers(&stepCalls, &actionCalls)); err != nil {
		t.Fatalf("scanDRS: %v", err)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d, want 1 for the step with no ARN", drsRecoveryPlanStepsOp, *warns)
	}
	plan1, plan2 := drsTestARN("recovery-plan", "plan1"), drsTestARN("recovery-plan", "plan2")
	if got := stepCalls.distinct(); !slices.Equal(got, []string{plan1, plan2}) {
		t.Errorf("steps listed for %v, want [%s %s]", got, plan1, plan2)
	}
	step1, step2, step3 := drsTestARN("recovery-plan-step", "step1"), drsTestARN("recovery-plan-step", "step2"), drsTestARN("recovery-plan-step", "step3")
	rows := rowsByNativeID(t, st, TypeDRSRecoveryPlanStep)
	if got := nativeIDs(rows); !slices.Equal(got, []string{step1, step2, step3}) {
		t.Fatalf("step rows = %v, want [%s %s %s]", got, step1, step2, step3)
	}
	assertContainsNative(t, st, plan1, step1)
	assertContainsNative(t, st, plan1, step2)
	assertContainsNative(t, st, plan1, step3)

	var server, wait, unknown struct {
		Configuration map[string]json.RawMessage
	}
	if err := json.Unmarshal([]byte(rows[step1].AttributesJSON), &server); err != nil {
		t.Fatalf("step1 attrs: %v", err)
	}
	if err := json.Unmarshal([]byte(rows[step2].AttributesJSON), &wait); err != nil {
		t.Fatalf("step2 attrs: %v", err)
	}
	if err := json.Unmarshal([]byte(rows[step3].AttributesJSON), &unknown); err != nil {
		t.Fatalf("step3 attrs: %v", err)
	}
	var member drstypes.UnknownUnionMember
	if err := json.Unmarshal(unknown.Configuration["UnknownMember"], &member); err != nil || len(unknown.Configuration) != 1 ||
		member.Tag != "futureStepConfiguration" || string(member.Value) != `{"x":1}` {
		t.Errorf("unknown-member step Configuration = %s (decode err %v)", rows[step3].AttributesJSON, err)
	}
	if got := string(server.Configuration["ServerStepConfiguration"]); got != `{"Servers":[{"ServerArn":"`+drsTestARN("source-server", "s-1")+`","ImpactLevel":""}]}` || len(server.Configuration) != 1 {
		t.Errorf("server step Configuration = %s", rows[step1].AttributesJSON)
	}
	if got := string(wait.Configuration["WaitStepConfiguration"]); got != `{"WaitDurationMinutes":5}` || len(wait.Configuration) != 1 {
		t.Errorf("wait step Configuration = %s", rows[step2].AttributesJSON)
	}
	if sv(rows[step1].Name) != "servers" || sv(rows[step1].Region) != testRegion || rows[step1].TagsJSON != nil {
		t.Errorf("step1 name/region/tags = %q/%q/%v", sv(rows[step1].Name), sv(rows[step1].Region), rows[step1].TagsJSON)
	}
}

func TestScanDRS_StoresLaunchActionsUnderTemplatesAndServers(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, drsLaunchActionsOp)
	var stepCalls, actionCalls parentCalls
	if err := scanDRSTopLevel(t, st, drsTopLevelHandlers(&stepCalls, &actionCalls)); err != nil {
		t.Fatalf("scanDRS: %v", err)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d, want 1 for the action with no id", drsLaunchActionsOp, *warns)
	}
	if got := actionCalls.distinct(); !slices.Equal(got, []string{"lct-1", "s-1", "s-2"}) {
		t.Errorf("launch actions listed for %v, want [lct-1 s-1 s-2]", got)
	}
	lct, s1 := drsTestARN("launch-configuration-template", "lct-1"), drsTestARN("source-server", "s-1")
	tA1, tA2, sA1 := drsActionNativeID(lct, "a1"), drsActionNativeID(lct, "a2"), drsActionNativeID(s1, "a1")
	rows := rowsByNativeID(t, st, TypeDRSLaunchAction)
	want := []string{tA1, tA2, sA1}
	sort.Strings(want)
	if got := nativeIDs(rows); !slices.Equal(got, want) {
		t.Fatalf("launch action rows = %v, want %v", got, want)
	}
	assertContainsNative(t, st, lct, tA1)
	assertContainsNative(t, st, lct, tA2)
	assertContainsNative(t, st, s1, sA1)
	row := rows[tA1]
	wantAttrs := drstypes.LaunchAction{ActionId: ptrStr("a1"), Name: ptrStr("template-a1"), Category: drstypes.LaunchActionCategorySecurity}
	if sv(row.Name) != "template-a1" || sv(row.Region) != testRegion || row.AttributesJSON != mustJSON(wantAttrs) || row.TagsJSON != nil {
		t.Errorf("row name/region/tags = %q/%q/%v, attrs = %s", sv(row.Name), sv(row.Region), row.TagsJSON, row.AttributesJSON)
	}
}

// One action id under a template and under a source server is two actions;
// the parent ARN in the NativeID keeps their identities apart.
func TestScanDRS_SameActionIDUnderTwoParentsKeepsTwoIdentities(t *testing.T) {
	st := newTestStore(t)
	var stepCalls, actionCalls parentCalls
	if err := scanDRSTopLevel(t, st, drsTopLevelHandlers(&stepCalls, &actionCalls)); err != nil {
		t.Fatalf("scanDRS: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeDRSLaunchAction)
	tpl := rows[drsActionNativeID(drsTestARN("launch-configuration-template", "lct-1"), "a1")]
	srv := rows[drsActionNativeID(drsTestARN("source-server", "s-1"), "a1")]
	if tpl.ID == "" || srv.ID == "" || tpl.ID == srv.ID || sv(tpl.Name) != "template-a1" || sv(srv.Name) != "server-a1" {
		t.Errorf("template a1 = %s/%q, server a1 = %s/%q; want two rows with their own names", tpl.ID, sv(tpl.Name), srv.ID, sv(srv.Name))
	}
}

func TestScanDRS_NoParentsNoChildCalls(t *testing.T) {
	st := newTestStore(t)
	var stepCalls, actionCalls parentCalls
	h := drsTopLevelHandlers(&stepCalls, &actionCalls)
	delete(h, "DescribeSourceServers")
	delete(h, "DescribeLaunchConfigurationTemplates")
	h["ListRecoveryPlans"] = func(any) (any, error) { return &drs.ListRecoveryPlansOutput{}, nil }
	if err := scanDRSTopLevel(t, st, h); err != nil {
		t.Fatalf("scanDRS: %v", err)
	}
	if len(stepCalls.distinct()) != 0 || len(actionCalls.distinct()) != 0 {
		t.Errorf("step calls = %v, action calls = %v; want none", stepCalls.distinct(), actionCalls.distinct())
	}
	if n := len(rowsByNativeID(t, st, TypeDRSRecoveryPlanStep)) + len(rowsByNativeID(t, st, TypeDRSLaunchAction)); n != 0 {
		t.Errorf("child rows = %d, want 0", n)
	}
}

// A failed parent drops only itself: deleted since listing is silent, a
// denial warns once however many parents hit it, and any other error is
// returned after the other parents' rows are stored. Both plans fail in the
// step case; the launch-action case keeps the healthy s-1's action.
func TestScanDRS_FailedChildParent(t *testing.T) {
	for _, tc := range []struct {
		op, bad1, bad2, childType string
		want                      []string
	}{
		{
			op: "ListRecoveryPlanSteps", bad1: drsTestARN("recovery-plan", "plan1"), bad2: drsTestARN("recovery-plan", "plan2"),
			childType: TypeDRSRecoveryPlanStep, want: []string{},
		},
		{
			op: "ListLaunchActions", bad1: "lct-1", bad2: "s-2",
			childType: TypeDRSLaunchAction, want: []string{drsActionNativeID(drsTestARN("source-server", "s-1"), "a1")},
		},
	} {
		for _, ec := range []struct {
			name     string
			err      error
			wantWarn int
		}{
			{"parent gone", drsGone, 0},
			{"denied", drsDenied, 1},
		} {
			t.Run(tc.op+"/"+ec.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "drs:"+tc.op)
				var stepCalls, actionCalls parentCalls
				h := drsTopLevelHandlers(&stepCalls, &actionCalls)
				h[tc.op] = failParents(h[tc.op], ec.err, tc.bad1, tc.bad2)
				if err := scanDRSTopLevel(t, st, h); err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if *warns != ec.wantWarn {
					t.Errorf("warnings = %d, want %d", *warns, ec.wantWarn)
				}
				if got := nativeIDs(rowsByNativeID(t, st, tc.childType)); !slices.Equal(got, tc.want) {
					t.Errorf("rows = %v, want %v", got, tc.want)
				}
			})
		}
		t.Run(tc.op+"/other", func(t *testing.T) {
			st := newTestStore(t)
			var stepCalls, actionCalls parentCalls
			h := drsTopLevelHandlers(&stepCalls, &actionCalls)
			h[tc.op] = failParents(h[tc.op], drsBadInput, tc.bad1, tc.bad2)
			assertAPICode(t, scanDRSTopLevel(t, st, h), "ValidationException")
			if got := nativeIDs(rowsByNativeID(t, st, tc.childType)); !slices.Equal(got, tc.want) {
				t.Errorf("rows = %v, want %v", got, tc.want)
			}
		})
	}
}

// failParents wraps a child-list handler so the parents bad1 and bad2 fail
// with err.
func failParents(next func(in any) (any, error), err error, bad1, bad2 string) func(in any) (any, error) {
	return func(in any) (any, error) {
		var id string
		switch p := in.(type) {
		case *drs.ListRecoveryPlanStepsInput:
			id = sv(p.RecoveryPlanArn)
		case *drs.ListLaunchActionsInput:
			id = sv(p.ResourceId)
		}
		if id == bad1 || id == bad2 {
			return nil, err
		}
		return next(in)
	}
}

func TestScanDRS_UninitializedAccountDisabled(t *testing.T) {
	st := newTestStore(t)
	var stepCalls, actionCalls parentCalls
	h := drsTopLevelHandlers(&stepCalls, &actionCalls)
	h["DescribeSourceServers"] = func(any) (any, error) {
		return nil, apiErr("UninitializedAccountException", "Account not initialized")
	}
	if err := scanDRSTopLevel(t, st, h); !errors.Is(err, errServiceDisabled) {
		t.Fatalf("err = %v; want service-disabled sentinel", err)
	}
}

// The client is pinned to the scanned region, not the config's default.
func TestScanDRS_CallsGoToScannedRegion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var stepCalls, actionCalls, regions parentCalls
	cfg := opStubConfig(drsTopLevelHandlers(&stepCalls, &actionCalls), drsDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	if _, _, err := scanDRS(context.Background(), acct, "eu-west-1", st, testScanID); err != nil {
		t.Fatalf("scanDRS: %v", err)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
	if row, ok := rowsByNativeID(t, st, TypeDRSRecoveryPlanResource)[drsTestARN("recovery-plan", "plan1")]; !ok || sv(row.Region) != "eu-west-1" {
		t.Errorf("plan row = %+v (found %v), want region eu-west-1", row, ok)
	}
}

func drsReplicationConfigNativeID(serverID string) string {
	return drsTestARN("source-server", serverID) + "/replication-configuration"
}

var drsTestKeyARN = "arn:aws:kms:" + testRegion + ":" + testAccountID + ":key/k-drs"

// drsReplicationConfigHandler answers GetReplicationConfiguration for any
// server, recording the server id; s-1 carries the staging subnet, security
// group, EBS key, bandwidth, PIT policy, instance type and staging-area tags,
// and the server in fail returns err.
func drsReplicationConfigHandler(calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	return func(in any) (any, error) {
		id := sv(in.(*drs.GetReplicationConfigurationInput).SourceServerID)
		calls.add(id)
		if err := fail[id]; err != nil {
			return nil, err
		}
		out := &drs.GetReplicationConfigurationOutput{SourceServerID: &id, Name: ptrStr("cfg-" + id)}
		if id == "s-1" {
			out.StagingAreaSubnetId = ptrStr("subnet-1")
			out.ReplicationServersSecurityGroupsIDs = []string{"sg-1"}
			out.EbsEncryptionKeyArn = &drsTestKeyARN
			out.BandwidthThrottling = 100
			out.ReplicationServerInstanceType = ptrStr("t3.small")
			out.PitPolicy = []drstypes.PITPolicyRule{{Interval: ptrInt32(10), RetentionDuration: ptrInt32(60), Units: drstypes.PITPolicyRuleUnitsMinute}}
			out.StagingAreaTags = map[string]string{"team": "dr"}
		}
		return out, nil
	}
}

// scanDRSReplicationConfigs seeds source servers s-1 and s-2 and runs the
// replication-configuration phase over them.
func scanDRSReplicationConfigs(t *testing.T, st *store.Store, fail map[string]error) (total, inserted int, calls *parentCalls, err error) {
	t.Helper()
	calls = &parentCalls{}
	var servers []childParent
	for _, id := range []string{"s-1", "s-2"} {
		arn := drsTestARN("source-server", id)
		upsertTestResource(t, st, "aws", testAccountID, TypeDRSSourceServerResource, arn, testRegion, "{}")
		servers = append(servers, childParent{id: id, arn: arn})
	}
	client := drs.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
		"GetReplicationConfiguration": drsReplicationConfigHandler(calls, fail),
	}, drsDenied))
	total, inserted, err = scanDRSReplicationConfigurations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, servers)
	return total, inserted, calls, err
}

func TestScanDRSReplicationConfigurations_OneRowPerServer(t *testing.T) {
	st := newTestStore(t)
	total, inserted, calls, err := scanDRSReplicationConfigs(t, st, nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"s-1", "s-2"}) {
		t.Errorf("configurations read for %v, want [s-1 s-2]", got)
	}
	c1, c2 := drsReplicationConfigNativeID("s-1"), drsReplicationConfigNativeID("s-2")
	rows := rowsByNativeID(t, st, TypeDRSReplicationConfiguration)
	if total != 2 || inserted != 2 || !slices.Equal(nativeIDs(rows), []string{c1, c2}) {
		t.Fatalf("total/inserted = %d/%d, rows = %v; want 2/2 [%s %s]", total, inserted, nativeIDs(rows), c1, c2)
	}
	assertContainsNative(t, st, drsTestARN("source-server", "s-1"), c1)
	assertContainsNative(t, st, drsTestARN("source-server", "s-2"), c2)
	row := rows[c1]
	if sv(row.Name) != "cfg-s-1" || sv(row.Region) != testRegion || row.ManagedByProvider || row.TagsJSON != nil {
		t.Errorf("row name/region/managed/tags = %q/%q/%v/%v", sv(row.Name), sv(row.Region), row.ManagedByProvider, row.TagsJSON)
	}
	var attrs drs.GetReplicationConfigurationOutput
	if err := json.Unmarshal([]byte(row.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs %s: %v", row.AttributesJSON, err)
	}
	if sv(attrs.StagingAreaSubnetId) != "subnet-1" || !slices.Equal(attrs.ReplicationServersSecurityGroupsIDs, []string{"sg-1"}) ||
		sv(attrs.EbsEncryptionKeyArn) != drsTestKeyARN || attrs.BandwidthThrottling != 100 || sv(attrs.ReplicationServerInstanceType) != "t3.small" ||
		len(attrs.PitPolicy) != 1 || attrs.PitPolicy[0].Units != drstypes.PITPolicyRuleUnitsMinute || attrs.StagingAreaTags["team"] != "dr" {
		t.Errorf("attrs = %s", row.AttributesJSON)
	}
	if strings.Contains(row.AttributesJSON, "ResultMetadata") {
		t.Errorf("attrs carry the SDK's ResultMetadata envelope: %s", row.AttributesJSON)
	}
}

func TestScanDRSReplicationConfigurations_NoServersNoCalls(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := drs.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
		"GetReplicationConfiguration": drsReplicationConfigHandler(&calls, nil),
	}, drsDenied))
	total, inserted, err := scanDRSReplicationConfigurations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, nil)
	if err != nil || total != 0 || inserted != 0 || len(calls.distinct()) != 0 || len(rowsByNativeID(t, st, TypeDRSReplicationConfiguration)) != 0 {
		t.Errorf("total/inserted = %d/%d, calls = %v, err = %v; want nothing, nil", total, inserted, calls.distinct(), err)
	}
}

// A failed server drops only itself: deleted since listing is silent, a
// denial warns once however many servers hit it, and a transient or any other
// error is returned after the other servers' rows are stored: there is no
// listed row to keep in its place.
func TestScanDRSReplicationConfigurations_FailedServer(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fail     map[string]error
		wantWarn int
		want     []string
	}{
		{"server gone", map[string]error{"s-1": drsGone}, 0, []string{drsReplicationConfigNativeID("s-2")}},
		{"denied", map[string]error{"s-1": drsDenied, "s-2": drsDenied}, 1, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "drs:GetReplicationConfiguration")
			total, _, _, err := scanDRSReplicationConfigs(t, st, tc.fail)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if *warns != tc.wantWarn {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarn)
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeDRSReplicationConfiguration)); total != len(tc.want) || !slices.Equal(got, tc.want) {
				t.Errorf("total = %d, rows = %v; want %v", total, got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name  string
		err   error
		check func(error) bool
	}{
		{"transient", drsThrottled, isTransientNetworkError},
		{"other", drsBadInput, func(err error) bool { return isAPIErrorCode(err, "ValidationException") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, drsReplicationConfigOp)
			total, _, _, err := scanDRSReplicationConfigs(t, st, map[string]error{"s-1": tc.err})
			if !tc.check(err) {
				t.Errorf("err = %v", err)
			}
			want := []string{drsReplicationConfigNativeID("s-2")}
			if got := nativeIDs(rowsByNativeID(t, st, TypeDRSReplicationConfiguration)); total != 1 || !slices.Equal(got, want) || *warns != 0 {
				t.Errorf("total = %d, rows = %v, warnings = %d; want 1, %v, 0", total, got, *warns, want)
			}
			assertContainsNative(t, st, drsTestARN("source-server", "s-2"), want[0])
		})
	}
}

func TestScanDRS_StoresReplicationConfigurationsUnderTheirServers(t *testing.T) {
	st := newTestStore(t)
	var stepCalls, actionCalls, configCalls parentCalls
	h := drsTopLevelHandlers(&stepCalls, &actionCalls)
	h["GetReplicationConfiguration"] = drsReplicationConfigHandler(&configCalls, nil)
	if err := scanDRSTopLevel(t, st, h); err != nil {
		t.Fatalf("scanDRS: %v", err)
	}
	if got := configCalls.distinct(); !slices.Equal(got, []string{"s-1", "s-2"}) {
		t.Errorf("configurations read for %v, want [s-1 s-2]", got)
	}
	c1, c2 := drsReplicationConfigNativeID("s-1"), drsReplicationConfigNativeID("s-2")
	if got := nativeIDs(rowsByNativeID(t, st, TypeDRSReplicationConfiguration)); !slices.Equal(got, []string{c1, c2}) {
		t.Fatalf("configuration rows = %v, want [%s %s]", got, c1, c2)
	}
	assertContainsNative(t, st, drsTestARN("source-server", "s-1"), c1)
	assertContainsNative(t, st, drsTestARN("source-server", "s-2"), c2)
}

// UninitializedAccountException reads as the service disabled only while no
// phase has stored a row or failed otherwise; after either, it is returned as
// an error and the earlier phase's work is kept.
func TestScanDRS_UninitializedAccountAfterOtherWork(t *testing.T) {
	uninit := apiErr("UninitializedAccountException", "Account not initialized")
	for _, tc := range []struct {
		name        string
		servers     func(any) (any, error)
		wantServers int
		wantCode    string
	}{
		{"after a stored row", nil, 2, ""},
		{"after a failed phase", func(any) (any, error) { return nil, drsBadInput }, 0, "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var stepCalls, actionCalls parentCalls
			h := drsTopLevelHandlers(&stepCalls, &actionCalls)
			if tc.servers != nil {
				h["DescribeSourceServers"] = tc.servers
			}
			h["DescribeRecoveryInstances"] = func(any) (any, error) { return nil, uninit }
			err := scanDRSTopLevel(t, st, h)
			if errors.Is(err, errServiceDisabled) || !drsHasCode(err, "UninitializedAccountException") {
				t.Errorf("err = %v; want the UninitializedAccountException returned, not the disabled sentinel", err)
			}
			if tc.wantCode != "" && !drsHasCode(err, tc.wantCode) {
				t.Errorf("err = %v; want the earlier %s kept", err, tc.wantCode)
			}
			if n := len(rowsByNativeID(t, st, TypeDRSSourceServerResource)); n != tc.wantServers {
				t.Errorf("server rows = %d, want %d", n, tc.wantServers)
			}
		})
	}
	t.Run("after a denied phase", func(t *testing.T) {
		st := newTestStore(t)
		var stepCalls, actionCalls parentCalls
		h := drsTopLevelHandlers(&stepCalls, &actionCalls)
		delete(h, "DescribeSourceServers")
		h["DescribeRecoveryInstances"] = func(any) (any, error) { return nil, uninit }
		if err := scanDRSTopLevel(t, st, h); !errors.Is(err, errServiceDisabled) {
			t.Errorf("err = %v; want the disabled sentinel: a denial stores nothing and fails nothing", err)
		}
	})
}

// A phase's hard error does not stop the later phases: each early phase
// failing still leaves the recovery-plan steps (the latest child phase) and
// every other early phase's rows stored, and its error is returned.
func TestScanDRS_PhaseErrorRunsLaterPhases(t *testing.T) {
	for _, f := range drsListedFixtures() {
		t.Run(f.op, func(t *testing.T) {
			st := newTestStore(t)
			var actionCalls, stepCalls parentCalls
			h := drsListedHandlers(nil, &actionCalls)
			for op, fn := range drsTopLevelHandlers(&stepCalls, &parentCalls{}) {
				if _, ok := h[op]; !ok {
					h[op] = fn
				}
			}
			h[f.op] = func(any) (any, error) { return nil, drsBadInput }
			if err := scanDRSTopLevel(t, st, h); !drsHasCode(err, "ValidationException") {
				t.Fatalf("err = %v, want ValidationException", err)
			}
			if n := len(rowsByNativeID(t, st, TypeDRSRecoveryPlanStep)); n != 3 {
				t.Errorf("step rows = %d, want 3", n)
			}
			for _, other := range drsListedFixtures() {
				if other.op == f.op {
					continue
				}
				if len(rowsByNativeID(t, st, other.typ)) == 0 {
					t.Errorf("%s rows missing after %s failed", other.typ, f.op)
				}
			}
		})
	}
}

// A throttled earlier phase marks the scan partial (transient) when it is
// the only failure, and is demoted to one warning beside a later phase's
// hard error.
func TestScanDRS_TransientPhaseBesideHardError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hard      bool
		want      serviceOutcome
		wantWarns int
	}{
		{"alone", false, outcomeTransient, 0},
		{"beside a hard error", true, outcomeError, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, drsRecoveryInstancesOp)
			var stepCalls, actionCalls, configCalls parentCalls
			h := drsTopLevelHandlers(&stepCalls, &actionCalls)
			h["DescribeRecoveryInstances"] = func(any) (any, error) { return nil, drsThrottled }
			h["GetReplicationConfiguration"] = drsReplicationConfigHandler(&configCalls, nil)
			if tc.hard {
				h["GetReplicationConfiguration"] = drsReplicationConfigHandler(&configCalls, map[string]error{"s-1": drsBadInput})
			}
			err := scanDRSTopLevel(t, st, h)
			if got := classifyServiceError(err); got != tc.want {
				t.Errorf("classifyServiceError(%v) = %v, want %v", err, got, tc.want)
			}
			if *warns != tc.wantWarns {
				t.Errorf("%s warnings = %d, want %d", drsRecoveryInstancesOp, *warns, tc.wantWarns)
			}
		})
	}
}

// A hard error in any child or recovery-plan phase reaches the caller and
// costs no other phase its rows: only the failing phase, and the phase that
// reads its parents, store fewer. TestScanDRS_PhaseErrorRunsLaterPhases covers
// the listed phases.
func TestScanDRS_ChildPhaseErrorKeepsOtherPhases(t *testing.T) {
	all := map[string]int{
		TypeDRSLaunchAction: 3, TypeDRSRecoveryPlanResource: 2, TypeDRSRecoveryPlanStep: 3, TypeDRSReplicationConfiguration: 2,
	}
	for _, tc := range []struct {
		op   string
		less map[string]int
	}{
		{"ListLaunchActions", map[string]int{TypeDRSLaunchAction: 0}},
		{"ListRecoveryPlans", map[string]int{TypeDRSRecoveryPlanResource: 0, TypeDRSRecoveryPlanStep: 0}},
		{"GetRecoveryPlan", nil}, // every plan is kept from its summary
		{"ListRecoveryPlanSteps", map[string]int{TypeDRSRecoveryPlanStep: 0}},
		{"GetReplicationConfiguration", map[string]int{TypeDRSReplicationConfiguration: 0}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			var stepCalls, actionCalls, configCalls parentCalls
			h := drsTopLevelHandlers(&stepCalls, &actionCalls)
			h["GetReplicationConfiguration"] = drsReplicationConfigHandler(&configCalls, nil)
			h[tc.op] = func(any) (any, error) { return nil, drsBadInput }
			if err := scanDRSTopLevel(t, st, h); !drsHasCode(err, "ValidationException") {
				t.Fatalf("err = %v, want ValidationException", err)
			}
			for typ, n := range all {
				if less, ok := tc.less[typ]; ok {
					n = less
				}
				if got := len(rowsByNativeID(t, st, typ)); got != n {
					t.Errorf("%s rows = %d, want %d", typ, got, n)
				}
			}
		})
	}
}

// drsOverlap is a stub handler that reports whether two of its calls were in
// flight at once: the first call waits for a second, up to a deadline a
// sequential caller always hits.
type drsOverlap struct {
	inFlight atomic.Int32
	second   chan struct{}
	once     sync.Once
	seen     atomic.Bool
}

func newDRSOverlap() *drsOverlap { return &drsOverlap{second: make(chan struct{})} }

func (o *drsOverlap) wrap(next func(in any) (any, error)) func(in any) (any, error) {
	return func(in any) (any, error) {
		if o.inFlight.Add(1) >= 2 {
			o.seen.Store(true)
			o.once.Do(func() { close(o.second) })
		}
		select {
		case <-o.second:
		case <-time.After(5 * time.Second):
		}
		o.inFlight.Add(-1)
		return next(in)
	}
}

// The per-parent reads (GetRecoveryPlan, and every storeTokenEachParent op)
// run concurrently, so one slow parent does not hold up the others.
func TestScanDRS_PerParentReadsRunConcurrently(t *testing.T) {
	for _, op := range []string{"GetRecoveryPlan", "ListRecoveryPlanSteps", "ListLaunchActions", "GetReplicationConfiguration"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			var stepCalls, actionCalls, configCalls parentCalls
			h := drsTopLevelHandlers(&stepCalls, &actionCalls)
			h["GetReplicationConfiguration"] = drsReplicationConfigHandler(&configCalls, nil)
			overlap := newDRSOverlap()
			h[op] = overlap.wrap(h[op])
			if err := scanDRSTopLevel(t, st, h); err != nil {
				t.Fatalf("scanDRS: %v", err)
			}
			if !overlap.seen.Load() {
				t.Errorf("%s calls never overlapped; want the parents read concurrently", op)
			}
		})
	}
}

// Once ctx ends mid-way, the per-parent phases (storeTokenEachParent) read no
// further parent, keep the rows already read, and return one stop error; the
// parents never read add none. (The stub ignores ctx, so the reads in flight
// succeed here; with the real SDK they report their own errors.)
func TestScanDRSReplicationConfigurations_StopsOnContext(t *testing.T) {
	const n = 3 * fanoutMed
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var gets atomic.Int32
	client := drs.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
		"GetReplicationConfiguration": func(in any) (any, error) {
			gets.Add(1)
			cancel()
			return &drs.GetReplicationConfigurationOutput{SourceServerID: in.(*drs.GetReplicationConfigurationInput).SourceServerID}, nil
		},
	}, drsDenied))
	servers := make([]childParent, n)
	for i := range servers {
		id := fmt.Sprintf("s-%02d", i)
		servers[i] = childParent{id: id, arn: drsTestARN("source-server", id)}
	}
	st := newTestStore(t)
	total, _, err := scanDRSReplicationConfigurations(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID, servers)
	if !errors.Is(err, context.Canceled) || drsLeaves(err) != 1 {
		t.Errorf("err = %v (%d errors), want one context.Canceled", err, drsLeaves(err))
	}
	got := int(gets.Load())
	if got >= n || total != got || len(rowsByNativeID(t, st, TypeDRSReplicationConfiguration)) != got {
		t.Errorf("calls = %d, total = %d; want fewer than %d calls, each one's row stored", got, total, n)
	}
}

// drsLeaves counts the non-join errors in err's tree.
func drsLeaves(err error) int {
	if err == nil {
		return 0
	}
	members := joinedMembers(err)
	if len(members) == 0 {
		return 1
	}
	n := 0
	for _, m := range members {
		n += drsLeaves(m)
	}
	return n
}

// Once ctx ends mid-way, GetRecoveryPlan reads no further plan, the plans
// not read are stored from their summaries, and the stop comes back as one
// error; the plans never read add none. (The stub ignores ctx, so the reads in
// flight succeed here; with the real SDK they report their own errors.)
func TestScanDRSRecoveryPlans_StopsOnContext(t *testing.T) {
	const n = 3 * fanoutMed
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var gets atomic.Int32
	summaries := make([]drstypes.RecoveryPlanSummary, n)
	for i := range summaries {
		summaries[i] = drsPlanSummary(fmt.Sprintf("p%02d", i))
	}
	client := drs.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
		"ListRecoveryPlans": func(any) (any, error) { return &drs.ListRecoveryPlansOutput{RecoveryPlans: summaries}, nil },
		"GetRecoveryPlan": func(in any) (any, error) {
			gets.Add(1)
			cancel()
			arn := sv(in.(*drs.GetRecoveryPlanInput).RecoveryPlanArn)
			return &drs.GetRecoveryPlanOutput{RecoveryPlan: &drstypes.RecoveryPlan{RecoveryPlanArn: &arn, Name: ptrStr("full")}}, nil
		},
	}, drsDenied))
	st := newTestStore(t)
	total, _, _, err := scanDRSRecoveryPlans(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, context.Canceled) || drsLeaves(err) != 1 {
		t.Errorf("err = %v (%d errors), want one context.Canceled", err, drsLeaves(err))
	}
	if got := int(gets.Load()); got >= n {
		t.Errorf("GetRecoveryPlan calls = %d, want fewer than %d once ctx ended", got, n)
	}
	rows := rowsByNativeID(t, st, TypeDRSRecoveryPlanResource)
	if total != n || len(rows) != n {
		t.Fatalf("total = %d, rows = %d; want every listed plan stored", total, len(rows))
	}
	unread := rows[drsTestARN("recovery-plan", fmt.Sprintf("p%02d", n-1))]
	assertDRSRow(t, unread, fmt.Sprintf("summary-p%02d", n-1), "ACTIVE", summaries[n-1], nil)
}

// Every plan whose GetRecoveryPlan fails transiently is kept from its
// summary, and the failures come back as one transient error, not one per
// plan.
func TestScanDRSRecoveryPlans_TransientFailuresAreOneError(t *testing.T) {
	st := newTestStore(t)
	plan1, plan2 := drsTestARN("recovery-plan", "plan1"), drsTestARN("recovery-plan", "plan2")
	_, _, err := scanDRSPlans(t, st, drsPlanHandlers(nil, map[string]error{plan1: drsThrottled, plan2: drsThrottled}))
	if !isTransientNetworkError(err) || drsLeaves(err) != 1 {
		t.Errorf("err = %v (%d errors), want one transient error", err, drsLeaves(err))
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeDRSRecoveryPlanResource)); !slices.Equal(got, []string{plan1, plan2}) {
		t.Errorf("rows = %v, want both plans kept", got)
	}
}

// A failed contains-edge write is returned with the stored children's counts.
func TestScanDRSReplicationConfigurations_HierarchyWriteFails(t *testing.T) {
	st := newTestStore(t)
	server := childParent{id: "s-1", arn: drsTestARN("source-server", "s-1")}
	upsertTestResource(t, st, "aws", testAccountID, TypeDRSSourceServerResource, server.arn, testRegion, "{}")
	refuseClosureWrites(t, st)
	client := drs.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
		"GetReplicationConfiguration": drsReplicationConfigHandler(&parentCalls{}, nil),
	}, drsDenied))
	total, _, err := scanDRSReplicationConfigurations(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, []childParent{server})
	if !errors.Is(err, store.ErrStoreWrite) || total != 1 {
		t.Errorf("total = %d, err = %v; want 1 and the closure's store write failure", total, err)
	}
	if n := len(rowsByNativeID(t, st, TypeDRSReplicationConfiguration)); n != 1 {
		t.Errorf("configuration rows = %d, want 1: only the link fails", n)
	}
}
