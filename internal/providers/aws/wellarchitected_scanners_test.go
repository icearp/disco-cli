package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/wellarchitected"
	watypes "github.com/aws/aws-sdk-go-v2/service/wellarchitected/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

const (
	waTestARNPrefix = "arn:aws:wellarchitected:us-east-1:123456789012:"
	waForeignOwner  = "210987654321"
)

var (
	waDenied   = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: wellarchitected:List"}
	waGone     = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "not found"}
	waBadInput = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
)

func waStubClient(handlers map[string]func(in any) (any, error)) *wellarchitected.Client {
	return wellarchitected.NewFromConfig(opStubConfig(handlers, waDenied))
}

func waARN(kind, id string) string { return waTestARNPrefix + kind + "/" + id }

// waItem is one child element a stub page returns.
type waItem struct{ id, name, status string }

// waChildCase drives one child fan-out phase.
type waChildCase struct {
	op, typ, parentType, segment, idKey string
	parentID                            func(in any) (id string, next *string)
	page                                func(items []waItem, next *string) any
	run                                 func(context.Context, wellArchitectedAPI, []childParent, *store.Store) (int, error)
}

func waShare(it waItem) (shareID, sharedWith *string, status watypes.ShareStatus) {
	if it.id != "" {
		shareID = ptrStr(it.id)
	}
	return shareID, ptrStr(it.name), watypes.ShareStatus(it.status)
}

func waChildCases() []waChildCase {
	acct := newTestAccount(testAccountID)
	return []waChildCase{
		{
			op: "ListLensReviews", typ: TypeWellArchitectedLensReview, parentType: TypeWellArchitectedWorkload, segment: "lens-review", idKey: "LensAlias",
			parentID: func(in any) (string, *string) {
				r := in.(*wellarchitected.ListLensReviewsInput)
				return sv(r.WorkloadId), r.NextToken
			},
			page: func(items []waItem, next *string) any {
				out := &wellarchitected.ListLensReviewsOutput{NextToken: next}
				for _, it := range items {
					var alias *string
					if it.id != "" {
						alias = ptrStr(it.id)
					}
					out.LensReviewSummaries = append(out.LensReviewSummaries, watypes.LensReviewSummary{LensAlias: alias, LensName: ptrStr(it.name), LensStatus: watypes.LensStatus(it.status)})
				}
				return out
			},
			run: func(ctx context.Context, c wellArchitectedAPI, ps []childParent, st *store.Store) (int, error) {
				total, _, err := scanWALensReviews(ctx, c, ps, acct, testRegion, st, testScanID)
				return total, err
			},
		},
		{
			op: "ListWorkloadShares", typ: TypeWellArchitectedWorkloadShare, parentType: TypeWellArchitectedWorkload, segment: "share", idKey: "ShareId",
			parentID: func(in any) (string, *string) {
				r := in.(*wellarchitected.ListWorkloadSharesInput)
				return sv(r.WorkloadId), r.NextToken
			},
			page: func(items []waItem, next *string) any {
				out := &wellarchitected.ListWorkloadSharesOutput{NextToken: next}
				for _, it := range items {
					id, with, status := waShare(it)
					out.WorkloadShareSummaries = append(out.WorkloadShareSummaries, watypes.WorkloadShareSummary{ShareId: id, SharedWith: with, Status: status})
				}
				return out
			},
			run: func(ctx context.Context, c wellArchitectedAPI, ps []childParent, st *store.Store) (int, error) {
				total, _, err := scanWAWorkloadShares(ctx, c, ps, acct, testRegion, st, testScanID)
				return total, err
			},
		},
		{
			op: "ListLensShares", typ: TypeWellArchitectedLensShare, parentType: TypeWellArchitectedLens, segment: "share", idKey: "ShareId",
			parentID: func(in any) (string, *string) {
				r := in.(*wellarchitected.ListLensSharesInput)
				return sv(r.LensAlias), r.NextToken
			},
			page: func(items []waItem, next *string) any {
				out := &wellarchitected.ListLensSharesOutput{NextToken: next}
				for _, it := range items {
					id, with, status := waShare(it)
					out.LensShareSummaries = append(out.LensShareSummaries, watypes.LensShareSummary{ShareId: id, SharedWith: with, Status: status})
				}
				return out
			},
			run: func(ctx context.Context, c wellArchitectedAPI, ps []childParent, st *store.Store) (int, error) {
				total, _, err := scanWALensShares(ctx, c, ps, acct, testRegion, st, testScanID)
				return total, err
			},
		},
		{
			op: "ListProfileShares", typ: TypeWellArchitectedProfileShare, parentType: TypeWellArchitectedProfile, segment: "share", idKey: "ShareId",
			parentID: func(in any) (string, *string) {
				r := in.(*wellarchitected.ListProfileSharesInput)
				return sv(r.ProfileArn), r.NextToken
			},
			page: func(items []waItem, next *string) any {
				out := &wellarchitected.ListProfileSharesOutput{NextToken: next}
				for _, it := range items {
					id, with, status := waShare(it)
					out.ProfileShareSummaries = append(out.ProfileShareSummaries, watypes.ProfileShareSummary{ShareId: id, SharedWith: with, Status: status})
				}
				return out
			},
			run: func(ctx context.Context, c wellArchitectedAPI, ps []childParent, st *store.Store) (int, error) {
				total, _, err := scanWAProfileShares(ctx, c, ps, acct, testRegion, st, testScanID)
				return total, err
			},
		},
		{
			op: "ListTemplateShares", typ: TypeWellArchitectedTemplateShare, parentType: TypeWellArchitectedReviewTemplate, segment: "share", idKey: "ShareId",
			parentID: func(in any) (string, *string) {
				r := in.(*wellarchitected.ListTemplateSharesInput)
				return sv(r.TemplateArn), r.NextToken
			},
			page: func(items []waItem, next *string) any {
				out := &wellarchitected.ListTemplateSharesOutput{NextToken: next}
				for _, it := range items {
					id, with, status := waShare(it)
					out.TemplateShareSummaries = append(out.TemplateShareSummaries, watypes.TemplateShareSummary{ShareId: id, SharedWith: with, Status: status})
				}
				return out
			},
			run: func(ctx context.Context, c wellArchitectedAPI, ps []childParent, st *store.Store) (int, error) {
				total, _, err := scanWATemplateShares(ctx, c, ps, acct, testRegion, st, testScanID)
				return total, err
			},
		},
	}
}

// waChildHandler answers a child op: p1 over two pages, p2 with one item plus
// an id-less one; fail decides per parent whether to fail instead.
func waChildHandler(c waChildCase, calls *parentCalls, fail func(parent string) error) func(any) (any, error) {
	return func(in any) (any, error) {
		id, next := c.parentID(in)
		calls.add(id)
		if err := fail(id); err != nil {
			return nil, err
		}
		if id == "p1" && next == nil {
			return c.page([]waItem{{id: "c1", name: "111111111111", status: "ACCEPTED"}}, ptrStr("p2")), nil
		}
		return c.page([]waItem{{id: id + "-c2", name: "ou-ab12-cdef3456", status: "PENDING"}, {name: "no id"}}, nil), nil
	}
}

func noFail(string) error { return nil }

func seedWAParents(t *testing.T, st *store.Store, parentType string) []childParent {
	t.Helper()
	parents := []childParent{{id: "p1", arn: waARN("parent", "p1")}, {id: "p2", arn: waARN("parent", "p2")}}
	for _, p := range parents {
		upsertTestResource(t, st, "aws", testAccountID, parentType, p.arn, testRegion, "{}")
	}
	return parents
}

func waChildID(parent, segment, child string) string {
	return waARN("parent", parent) + "/" + segment + "/" + child
}

func TestWAChildPhases_ListEveryParentUnderItsParent(t *testing.T) {
	t.Parallel()
	for _, c := range waChildCases() {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			parents := seedWAParents(t, st, c.parentType)
			calls := &parentCalls{}
			total, err := c.run(context.Background(), waStubClient(map[string]func(any) (any, error){c.op: waChildHandler(c, calls, noFail)}), parents, st)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := calls.distinct(); !slices.Equal(got, []string{"p1", "p2"}) {
				t.Errorf("parents queried = %v, want [p1 p2]", got)
			}
			rows := rowsByNativeID(t, st, c.typ)
			if total != 3 || len(rows) != 3 {
				t.Fatalf("total = %d, rows = %d, want 3 and 3 (both p1 pages, id-less item skipped)", total, len(rows))
			}
			r, ok := rows[waChildID("p1", c.segment, "c1")]
			if !ok {
				t.Fatalf("no row for p1/c1; got %v", rows)
			}
			if r.Type != c.typ || sv(r.Region) != testRegion || sv(r.Name) != "111111111111" || sv(r.Status) != "ACCEPTED" {
				t.Errorf("row = type %q region %q name %q status %q", r.Type, sv(r.Region), sv(r.Name), sv(r.Status))
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs[c.idKey] != "c1" {
				t.Errorf("attrs = %s (%v), want %s c1", r.AttributesJSON, err, c.idKey)
			}
			assertContainsNative(t, st, waARN("parent", "p1"), waChildID("p1", c.segment, "c1"))
			assertContainsNative(t, st, waARN("parent", "p2"), waChildID("p2", c.segment, "p2-c2"))
		})
	}
}

func TestWAChildPhases_ParentErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		fail     func(parent string) error
		wantRows []string
		wantWarn int
		wantErr  bool
	}{
		{name: "empty", fail: noFail},
		{name: "parent gone skipped silently", fail: func(p string) error {
			if p == "p1" {
				return waGone
			}
			return nil
		}, wantRows: []string{"p2-c2"}},
		{name: "denied parent warns and keeps siblings", fail: func(p string) error {
			if p == "p1" {
				return waDenied
			}
			return nil
		}, wantRows: []string{"p2-c2"}, wantWarn: 1},
		{name: "denied everywhere warns once", fail: func(string) error { return waDenied }, wantWarn: 1},
		{name: "other error propagates", fail: func(p string) error {
			if p == "p2" {
				return waBadInput
			}
			return nil
		}, wantErr: true},
	}
	for _, c := range waChildCases() {
		for _, tc := range cases {
			t.Run(c.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				parents := seedWAParents(t, st, c.parentType)
				warns := countOpWarnings(st, "wellarchitected:"+c.op)
				handler := waChildHandler(c, &parentCalls{}, tc.fail)
				if tc.name == "empty" {
					handler = func(any) (any, error) { return c.page(nil, nil), nil }
				}
				total, err := c.run(context.Background(), waStubClient(map[string]func(any) (any, error){c.op: handler}), parents, st)
				if tc.wantErr != isAPIErrorCode(err, "ValidationException") || (!tc.wantErr && err != nil) {
					t.Fatalf("err = %v, want ValidationException: %v", err, tc.wantErr)
				}
				if *warns != tc.wantWarn {
					t.Errorf("warnings = %d, want %d", *warns, tc.wantWarn)
				}
				if tc.wantErr {
					return
				}
				rows := rowsByNativeID(t, st, c.typ)
				if total != len(tc.wantRows) || len(rows) != len(tc.wantRows) {
					t.Fatalf("total = %d, rows = %v, want %v", total, rows, tc.wantRows)
				}
				for _, id := range tc.wantRows {
					if _, ok := rows[waChildID("p2", c.segment, id)]; !ok {
						t.Errorf("no row for p2/%s; got %v", id, rows)
					}
				}
			})
		}
	}
}

// The SDK documents an AWS-official lens's LensAlias as either the bare alias
// or an account-less ARN; both must land on one row, while a custom lens's
// ARN, which names its owner account, is kept whole.
func TestScanWALensReviews_LensAliasFormsKeyOneNativeID(t *testing.T) {
	t.Parallel()
	workload := childParent{id: "w1", arn: waARN("workload", "w1")}
	customARN := waARN("lens", "0123abcd")
	cases := []struct {
		name    string
		aliases []string
		wantID  string
	}{
		{"official alias and account-less ARN", []string{"serverless", "arn:aws:wellarchitected:us-east-1::lens/serverless"}, workload.arn + "/lens-review/serverless"},
		{"custom lens ARN kept whole", []string{customARN}, workload.arn + "/lens-review/" + customARN},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			upsertTestResource(t, st, "aws", testAccountID, TypeWellArchitectedWorkload, workload.arn, testRegion, "{}")
			for _, alias := range c.aliases {
				client := waStubClient(map[string]func(any) (any, error){"ListLensReviews": func(any) (any, error) {
					return &wellarchitected.ListLensReviewsOutput{LensReviewSummaries: []watypes.LensReviewSummary{{LensAlias: ptrStr(alias)}}}, nil
				}})
				if _, _, err := scanWALensReviews(context.Background(), client, []childParent{workload}, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
					t.Fatalf("scan %q: %v", alias, err)
				}
			}
			rows := rowsByNativeID(t, st, TypeWellArchitectedLensReview)
			if _, ok := rows[c.wantID]; len(rows) != 1 || !ok {
				t.Errorf("aliases %q stored %v, want one row %s", c.aliases, rows, c.wantID)
			}
		})
	}
}

// waServiceHandlers answers every op of scanWellArchitected with one owned
// parent of each kind and one child under each.
func waServiceHandlers() map[string]func(any) (any, error) {
	share := func(id string) (shareID, sharedWith *string, status watypes.ShareStatus) {
		return ptrStr(id), ptrStr("111111111111"), watypes.ShareStatusAccepted
	}
	return map[string]func(any) (any, error){
		"ListWorkloads": func(any) (any, error) {
			return &wellarchitected.ListWorkloadsOutput{WorkloadSummaries: []watypes.WorkloadSummary{
				{WorkloadArn: ptrStr(waARN("workload", "w1")), WorkloadId: ptrStr("w1"), Owner: ptrStr(testAccountID)},
			}}, nil
		},
		"ListLenses": func(any) (any, error) {
			return &wellarchitected.ListLensesOutput{LensSummaries: []watypes.LensSummary{
				{LensArn: ptrStr(waARN("lens", "l1")), LensType: watypes.LensTypeCustomSelf, Owner: ptrStr(testAccountID)},
			}}, nil
		},
		"ListProfiles": func(any) (any, error) {
			return &wellarchitected.ListProfilesOutput{ProfileSummaries: []watypes.ProfileSummary{
				{ProfileArn: ptrStr(waARN("profile", "pr1")), Owner: ptrStr(testAccountID)},
			}}, nil
		},
		"ListReviewTemplates": func(any) (any, error) {
			return &wellarchitected.ListReviewTemplatesOutput{ReviewTemplates: []watypes.ReviewTemplateSummary{
				{TemplateArn: ptrStr(waARN("review-template", "t1")), Owner: ptrStr(testAccountID)},
			}}, nil
		},
		"ListLensReviews": func(any) (any, error) {
			return &wellarchitected.ListLensReviewsOutput{LensReviewSummaries: []watypes.LensReviewSummary{{LensAlias: ptrStr("wellarchitected")}}}, nil
		},
		"ListWorkloadShares": func(any) (any, error) {
			id, with, status := share("ws1")
			return &wellarchitected.ListWorkloadSharesOutput{WorkloadShareSummaries: []watypes.WorkloadShareSummary{{ShareId: id, SharedWith: with, Status: status}}}, nil
		},
		"ListLensShares": func(any) (any, error) {
			id, with, status := share("ls1")
			return &wellarchitected.ListLensSharesOutput{LensShareSummaries: []watypes.LensShareSummary{{ShareId: id, SharedWith: with, Status: status}}}, nil
		},
		"ListProfileShares": func(any) (any, error) {
			id, with, status := share("ps1")
			return &wellarchitected.ListProfileSharesOutput{ProfileShareSummaries: []watypes.ProfileShareSummary{{ShareId: id, SharedWith: with, Status: status}}}, nil
		},
		"ListTemplateShares": func(any) (any, error) {
			id, with, status := share("ts1")
			return &wellarchitected.ListTemplateSharesOutput{TemplateShareSummaries: []watypes.TemplateShareSummary{{ShareId: id, SharedWith: with, Status: status}}}, nil
		},
		"ListTagsForResource": func(any) (any, error) { return &wellarchitected.ListTagsForResourceOutput{}, nil },
	}
}

func TestScanWellArchitected_ChildPhasesStoreRowsUnderParents(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(waServiceHandlers(), waDenied)
	if _, _, err := scanWellArchitected(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanWellArchitected: %v", err)
	}
	for _, c := range []struct{ typ, parent, child string }{
		{TypeWellArchitectedLensReview, waARN("workload", "w1"), waARN("workload", "w1") + "/lens-review/wellarchitected"},
		{TypeWellArchitectedWorkloadShare, waARN("workload", "w1"), waARN("workload", "w1") + "/share/ws1"},
		{TypeWellArchitectedLensShare, waARN("lens", "l1"), waARN("lens", "l1") + "/share/ls1"},
		{TypeWellArchitectedProfileShare, waARN("profile", "pr1"), waARN("profile", "pr1") + "/share/ps1"},
		{TypeWellArchitectedTemplateShare, waARN("review-template", "t1"), waARN("review-template", "t1") + "/share/ts1"},
	} {
		if _, ok := rowsByNativeID(t, st, c.typ)[c.child]; !ok {
			t.Errorf("%s: no row %s", c.typ, c.child)
			continue
		}
		assertContainsNative(t, st, c.parent, c.child)
	}
}

// A failing child phase must not cost the rows of the list phases before it,
// and its error must reach the dispatcher.
func TestScanWellArchitected_ChildPhaseErrorAfterListPhases(t *testing.T) {
	t.Parallel()
	for _, failing := range []string{"ListLensReviews", "ListWorkloadShares", "ListLensShares", "ListProfileShares", "ListTemplateShares"} {
		t.Run(failing, func(t *testing.T) {
			st := newTestStore(t)
			h := waServiceHandlers()
			h[failing] = func(any) (any, error) { return nil, waBadInput }
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(h, waDenied)
			if _, _, err := scanWellArchitected(context.Background(), acct, testRegion, st, testScanID); !isAPIErrorCode(err, "ValidationException") {
				t.Fatalf("err = %v, want ValidationException", err)
			}
			if len(rowsByNativeID(t, st, TypeWellArchitectedReviewTemplate)) != 1 {
				t.Error("review-template row from the last list phase lost")
			}
		})
	}
}

const waMaxAgentProfileCalls = 5

func waAgentProfile(name string) watypes.AgentProfileSummary {
	return watypes.AgentProfileSummary{
		Arn: ptrStr(waARN("agent-profile", name)), Name: ptrStr(name),
		ExecutionRoleArn: ptrStr("arn:aws:iam::123456789012:role/" + name),
	}
}

type waAgentPageFn func() (*wellarchitected.ListAgentProfilesOutput, error)

// waAgentProfilePages answers ListAgentProfiles with pages[i] for call i, each
// page but the last carrying a NextToken. Calls past the last page or past
// waMaxAgentProfileCalls fail, so a pagination loop fails the test instead of
// hanging it.
func waAgentProfilePages(calls *int, pages ...waAgentPageFn) func(any) (any, error) {
	return func(any) (any, error) {
		*calls++
		if *calls > waMaxAgentProfileCalls || *calls > len(pages) {
			return nil, apiErr("TooManyCalls", "ListAgentProfiles called past its pages")
		}
		out, err := pages[*calls-1]()
		if err == nil && *calls < len(pages) {
			out.NextToken = ptrStr(fmt.Sprintf("t%d", *calls))
		}
		return out, err
	}
}

func waAgentPage(items ...watypes.AgentProfileSummary) waAgentPageFn {
	return func() (*wellarchitected.ListAgentProfilesOutput, error) {
		return &wellarchitected.ListAgentProfilesOutput{Items: items}, nil
	}
}

func waAgentFail(err error) waAgentPageFn {
	return func() (*wellarchitected.ListAgentProfilesOutput, error) { return nil, err }
}

func runWAAgentProfiles(t *testing.T, st *store.Store, handler func(any) (any, error)) (int, error) {
	t.Helper()
	client := waStubClient(map[string]func(any) (any, error){"ListAgentProfiles": handler})
	total, _, _, err := scanWAAgentProfiles(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

func TestScanWAAgentProfiles_TwoPagesStored(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	created := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	first := waAgentProfile("p1")
	first.CreatedAt = &created
	first.Pillars = []watypes.Pillar{watypes.PillarSecurity}
	first.Tags = []watypes.Tag{{Key: ptrStr("team"), Value: ptrStr("ops")}}
	var (
		calls      int
		maxResults []int32
	)
	pages := waAgentProfilePages(&calls, waAgentPage(first), waAgentPage(waAgentProfile("p2"), watypes.AgentProfileSummary{Name: ptrStr("no-arn")}))
	total, err := runWAAgentProfiles(t, st, func(in any) (any, error) {
		maxResults = append(maxResults, sdkaws.ToInt32(in.(*wellarchitected.ListAgentProfilesInput).MaxResults))
		return pages(in)
	})
	if err != nil || total != 2 {
		t.Fatalf("total = %d, err = %v; want 2, nil", total, err)
	}
	if !slices.Equal(maxResults, []int32{1000, 1000}) {
		t.Errorf("MaxResults per call = %v, want [1000 1000]", maxResults)
	}
	rows := rowsByNativeID(t, st, TypeWellArchitectedAgentProfile)
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want p1 and p2", rows)
	}
	if _, ok := rows[waARN("agent-profile", "p2")]; !ok {
		t.Errorf("no row for page-two profile p2")
	}
	r, ok := rows[waARN("agent-profile", "p1")]
	if !ok {
		t.Fatalf("no row for p1")
	}
	if r.Type != TypeWellArchitectedAgentProfile || sdkaws.ToString(r.Name) != "p1" || sdkaws.ToString(r.Region) != testRegion {
		t.Errorf("p1 type/name/region = %s/%s/%s, want %s/p1/%s", r.Type, sdkaws.ToString(r.Name), sdkaws.ToString(r.Region), TypeWellArchitectedAgentProfile, testRegion)
	}
	if got := sdkaws.ToString(r.CreatedAt); got != "2026-08-20T10:00:00Z" {
		t.Errorf("CreatedAt = %q, want 2026-08-20T10:00:00Z", got)
	}
	var tags map[string]string
	if err := json.Unmarshal([]byte(sdkaws.ToString(r.TagsJSON)), &tags); err != nil || tags["team"] != "ops" || len(tags) != 1 {
		t.Errorf("tags = %v (%v), want {team: ops}", tags, err)
	}
	var attrs watypes.AgentProfileSummary
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || sdkaws.ToString(attrs.ExecutionRoleArn) != "arn:aws:iam::123456789012:role/p1" || !slices.Equal(attrs.Pillars, []watypes.Pillar{watypes.PillarSecurity}) {
		t.Errorf("attributes = %+v (%v), want the SDK element", attrs, err)
	}
}

func TestScanWAAgentProfiles_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls int
	total, err := runWAAgentProfiles(t, st, waAgentProfilePages(&calls, waAgentPage()))
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeWellArchitectedAgentProfile)) != 0 {
		t.Fatalf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

// A token the service repeats must end the listing, not loop on it.
func TestScanWAAgentProfiles_RepeatedTokenStops(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls int
	total, err := runWAAgentProfiles(t, st, func(any) (any, error) {
		calls++
		if calls > waMaxAgentProfileCalls {
			return nil, apiErr("TooManyCalls", "repeated token followed")
		}
		return &wellarchitected.ListAgentProfilesOutput{NextToken: ptrStr("same"), Items: []watypes.AgentProfileSummary{waAgentProfile(fmt.Sprintf("p%d", calls))}}, nil
	})
	if err != nil || total != 2 || calls != 2 {
		t.Fatalf("total = %d, calls = %d, err = %v; want 2, 2, nil", total, calls, err)
	}
}

// Each failure is skipped, warned or returned by its code and page; rows read
// before a later failure are kept.
func TestScanWAAgentProfiles_ErrorLadder(t *testing.T) {
	t.Parallel()
	gap := apiErr("UnknownOperationException", "unknown operation")
	unknownOp := &watypes.ValidationException{Message: ptrStr("unknown operation"), Reason: watypes.ValidationExceptionReasonUnknownOperation}
	fieldInvalid := &watypes.ValidationException{Message: ptrStr("bad field"), Reason: watypes.ValidationExceptionReasonFieldValidationFailed}
	for _, tc := range []struct {
		name      string
		pages     []waAgentPageFn
		wantErr   string
		wantRows  int
		wantWarns int
	}{
		{name: "first-page denial warns", pages: []waAgentPageFn{waAgentFail(waDenied)}, wantWarns: 1},
		{name: "second-page denial warns and keeps page one", pages: []waAgentPageFn{waAgentPage(waAgentProfile("p1")), waAgentFail(waDenied)}, wantRows: 1, wantWarns: 1},
		{name: "first-page op gap is silent", pages: []waAgentPageFn{waAgentFail(gap)}},
		{name: "op gap after a page is returned", pages: []waAgentPageFn{waAgentPage(waAgentProfile("p1")), waAgentFail(gap)}, wantErr: "UnknownOperationException", wantRows: 1},
		{name: "other code is returned", pages: []waAgentPageFn{waAgentFail(waBadInput)}, wantErr: "ValidationException"},
		{name: "other code after a page keeps page one", pages: []waAgentPageFn{waAgentPage(waAgentProfile("p1")), waAgentFail(waBadInput)}, wantErr: "ValidationException", wantRows: 1},
		{name: "first-page unknown-operation validation is silent", pages: []waAgentPageFn{waAgentFail(unknownOp)}},
		{name: "unknown-operation validation after a page is returned", pages: []waAgentPageFn{waAgentPage(waAgentProfile("p1")), waAgentFail(unknownOp)}, wantErr: "ValidationException", wantRows: 1},
		{name: "first-page validation with another reason is returned", pages: []waAgentPageFn{waAgentFail(fieldInvalid)}, wantErr: "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "wellarchitected:ListAgentProfiles")
			var calls int
			total, err := runWAAgentProfiles(t, st, waAgentProfilePages(&calls, tc.pages...))
			if tc.wantErr == "" && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if tc.wantErr != "" && (!isAPIErrorCode(err, tc.wantErr) || classifyServiceError(err) != outcomeError) {
				t.Fatalf("err = %v, want a hard %s", err, tc.wantErr)
			}
			if rows := len(rowsByNativeID(t, st, TypeWellArchitectedAgentProfile)); total != tc.wantRows || rows != tc.wantRows {
				t.Errorf("total = %d, rows = %d; want %d", total, rows, tc.wantRows)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
		})
	}
}

func waAgentServiceHandlers() map[string]func(any) (any, error) {
	h := waServiceHandlers()
	h["ListAgentProfiles"] = func(any) (any, error) {
		return &wellarchitected.ListAgentProfilesOutput{Items: []watypes.AgentProfileSummary{waAgentProfile("ap1")}}, nil
	}
	h["ListAgentContexts"] = func(any) (any, error) {
		return &wellarchitected.ListAgentContextsOutput{Items: []watypes.ContextSummary{{Id: ptrStr("ctx1")}}}, nil
	}
	h["ListAgentGoals"] = func(any) (any, error) {
		return &wellarchitected.ListAgentGoalsOutput{Items: []watypes.GoalSummary{{Id: ptrStr("goal1")}}}, nil
	}
	return h
}

func TestScanWellArchitected_StoresAgentProfilesWithToolRows(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(waAgentServiceHandlers(), waDenied)
	total, inserted, err := scanWellArchitected(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanWellArchitected: %v", err)
	}
	// Four tool parents, five tool children, one agent profile, its context and its goal.
	if total != 12 || inserted != 12 {
		t.Errorf("total, inserted = %d, %d; want 12, 12", total, inserted)
	}
	if _, ok := rowsByNativeID(t, st, TypeWellArchitectedAgentProfile)[waARN("agent-profile", "ap1")]; !ok {
		t.Error("no agent profile row")
	}
	for typ, child := range map[string]string{
		TypeWellArchitectedAgentContext: waARN("agent-profile", "ap1") + "/context/ctx1",
		TypeWellArchitectedAgentGoal:    waARN("agent-profile", "ap1") + "/goal/goal1",
	} {
		if _, ok := rowsByNativeID(t, st, typ)[child]; !ok {
			t.Errorf("%s: no row %s", typ, child)
			continue
		}
		assertContainsNative(t, st, waARN("agent-profile", "ap1"), child)
	}
	if _, ok := rowsByNativeID(t, st, TypeWellArchitectedTemplateShare)[waARN("review-template", "t1")+"/share/ts1"]; !ok {
		t.Error("no template share row from the last tool phase")
	}
}

// A tool phase throttled after earlier phases stored rows is returned as
// transient, so the scan is partial, and the rows the earlier phases and the
// agent phase stored are still counted.
func TestScanWellArchitected_ThrottledToolPhaseKeepsCounts(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, "wellarchitected:ListTemplateShares")
	h := waAgentServiceHandlers()
	h["ListTemplateShares"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "Rate exceeded") }
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, waDenied)
	total, inserted, err := scanWellArchitected(context.Background(), acct, testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeTransient {
		t.Fatalf("classifyServiceError(%v) = %v, want outcomeTransient", err, got)
	}
	// Four tool parents and four of the five tool children, plus one agent
	// profile, its context and its goal.
	if total != 11 || inserted != 11 {
		t.Errorf("total, inserted = %d, %d; want 11, 11", total, inserted)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d, want 0", *warns)
	}
}

// A failed store write must not lose the listing error read before it.
func TestScanWAAgentProfiles_StoreWriteFailureKeepsListError(t *testing.T) {
	t.Parallel()
	// Not newTestStore: its cleanup queries the DB, which a closed store can't serve.
	st, err := store.Open(filepath.Join(t.TempDir(), "closing.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	insertTestScan(t, st, testScanID, "running")
	var calls int
	client := waStubClient(map[string]func(any) (any, error){"ListAgentProfiles": waAgentProfilePages(&calls,
		waAgentPage(waAgentProfile("p1")),
		func() (*wellarchitected.ListAgentProfilesOutput, error) {
			if err := st.Close(); err != nil {
				t.Errorf("close: %v", err)
			}
			return nil, waBadInput
		})})
	total, _, profiles, err := scanWAAgentProfiles(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) || !isAPIErrorCode(err, "ValidationException") || total != 0 {
		t.Fatalf("total = %d, err = %v; want 0 and both store.ErrStoreWrite and ValidationException", total, err)
	}
	// p1 was listed but never stored, so it is no parent of the child phases.
	if len(profiles) != 0 {
		t.Errorf("profiles = %v, want none after the failed store write", profiles)
	}
}

// The agent phase runs after every tool phase and whatever they returned; each
// side's hard error reaches the dispatcher without costing the other's rows,
// and a transient on either side is demoted to one warning so it cannot hide
// the other side's hard error.
func TestScanWellArchitected_AgentProfilesIndependentOfToolPhases(t *testing.T) {
	t.Parallel()
	throttled := apiErr("ThrottlingException", "Rate exceeded")
	for _, tc := range []struct {
		name                          string
		toolOp                        string
		toolErr, agentErr             error
		wantAgentRow                  bool
		wantToolWarns, wantAgentWarns int
	}{
		{name: "first tool phase fails", toolOp: "ListWorkloads", toolErr: waBadInput, wantAgentRow: true},
		{name: "last tool phase fails", toolOp: "ListTemplateShares", toolErr: waBadInput, wantAgentRow: true},
		{name: "agent fails after every tool phase", agentErr: waBadInput},
		{name: "tool throttled, agent fails", toolOp: "ListWorkloads", toolErr: throttled, agentErr: waBadInput, wantToolWarns: 1},
		{name: "agent throttled, tool fails", toolOp: "ListTemplateShares", toolErr: waBadInput, agentErr: throttled, wantAgentWarns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			toolWarns := countOpWarnings(st, "wellarchitected:"+tc.toolOp)
			h := waAgentServiceHandlers()
			if tc.toolOp != "" {
				h[tc.toolOp] = func(any) (any, error) { return nil, tc.toolErr }
			}
			if tc.agentErr != nil {
				h["ListAgentProfiles"] = func(any) (any, error) { return nil, tc.agentErr }
			}
			agentWarns := 0
			countTool := st.OnWarn
			st.OnWarn = func(w store.ScanWarning) {
				if w.Service == "wellarchitected:ListAgentProfiles" {
					agentWarns++
				}
				countTool(w)
			}
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(h, waDenied)
			_, _, err := scanWellArchitected(context.Background(), acct, testRegion, st, testScanID)
			if !isAPIErrorCode(err, "ValidationException") || classifyServiceError(err) != outcomeError {
				t.Fatalf("err = %v, want a hard ValidationException", err)
			}
			if _, ok := rowsByNativeID(t, st, TypeWellArchitectedAgentProfile)[waARN("agent-profile", "ap1")]; ok != tc.wantAgentRow {
				t.Errorf("agent profile row present = %v, want %v", ok, tc.wantAgentRow)
			}
			if *toolWarns != tc.wantToolWarns || agentWarns != tc.wantAgentWarns {
				t.Errorf("transient warnings tool/agent = %d/%d, want %d/%d", *toolWarns, agentWarns, tc.wantToolWarns, tc.wantAgentWarns)
			}
		})
	}
}

// waAgentChildCase drives one of the agent profile child phases.
type waAgentChildCase struct {
	op, typ, segment string
	// input returns the profile ARN, NextToken and MaxResults of a call.
	input func(in any) (profileARN string, next *string, maxResults int32)
	// page builds an output holding one item per id ("" for an id-less item).
	page func(ids []string, next *string) any
	// attrsOK checks the stored attributes carry the SDK element's content.
	attrsOK func(attrs string) bool
	run     func(context.Context, wellArchitectedAPI, []childParent, *store.Store) (int, int, error)
}

var waAgentChildCreated = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)

func waAgentChildCases() []waAgentChildCase {
	run := func(fn func(context.Context, wellArchitectedAPI, []childParent, *account, string, *store.Store, string) (int, int, error)) func(context.Context, wellArchitectedAPI, []childParent, *store.Store) (int, int, error) {
		return func(ctx context.Context, c wellArchitectedAPI, ps []childParent, st *store.Store) (int, int, error) {
			return fn(ctx, c, ps, newTestAccount(testAccountID), testRegion, st, testScanID)
		}
	}
	return []waAgentChildCase{
		{
			op: "ListAgentContexts", typ: TypeWellArchitectedAgentContext, segment: "context",
			input: func(in any) (string, *string, int32) {
				i := in.(*wellarchitected.ListAgentContextsInput)
				return sv(i.ProfileArn), i.NextToken, sdkaws.ToInt32(i.MaxResults)
			},
			page: func(ids []string, next *string) any {
				out := &wellarchitected.ListAgentContextsOutput{NextToken: next}
				for _, id := range ids {
					out.Items = append(out.Items, watypes.ContextSummary{
						Id: nonEmptyPtr(id), Title: ptrStr("title " + id), CreatedAt: &waAgentChildCreated,
						Content: &watypes.ContextContent{AccountIds: []string{"111111111111"}, Regions: []string{"eu-west-1"}},
					})
				}
				return out
			},
			attrsOK: func(attrs string) bool {
				var c watypes.ContextSummary
				return json.Unmarshal([]byte(attrs), &c) == nil && c.Content != nil &&
					slices.Equal(c.Content.AccountIds, []string{"111111111111"}) && slices.Equal(c.Content.Regions, []string{"eu-west-1"})
			},
			run: run(scanWAAgentContexts),
		},
		{
			op: "ListAgentGoals", typ: TypeWellArchitectedAgentGoal, segment: "goal",
			input: func(in any) (string, *string, int32) {
				i := in.(*wellarchitected.ListAgentGoalsInput)
				return sv(i.ProfileArn), i.NextToken, sdkaws.ToInt32(i.MaxResults)
			},
			page: func(ids []string, next *string) any {
				out := &wellarchitected.ListAgentGoalsOutput{NextToken: next}
				for _, id := range ids {
					out.Items = append(out.Items, watypes.GoalSummary{
						Id: nonEmptyPtr(id), Title: ptrStr("title " + id), CreatedAt: &waAgentChildCreated,
						Pillars: []watypes.Pillar{watypes.PillarCostOptimization},
					})
				}
				return out
			},
			attrsOK: func(attrs string) bool {
				var g watypes.GoalSummary
				return json.Unmarshal([]byte(attrs), &g) == nil && slices.Equal(g.Pillars, []watypes.Pillar{watypes.PillarCostOptimization})
			},
			run: run(scanWAAgentGoals),
		},
	}
}

// seedWAAgentProfiles stores profiles ap1 and ap2 and returns them as parents.
func seedWAAgentProfiles(t *testing.T, st *store.Store) []childParent {
	t.Helper()
	var parents []childParent
	for _, name := range []string{"ap1", "ap2"} {
		arn := waARN("agent-profile", name)
		upsertTestResource(t, st, "aws", testAccountID, TypeWellArchitectedAgentProfile, arn, testRegion, "{}")
		parents = append(parents, childParent{id: arn, arn: arn})
	}
	return parents
}

func waAgentChildID(profile, segment, id string) string {
	return waARN("agent-profile", profile) + "/" + segment + "/" + id
}

// waAgentChildCalls records each call's profile and MaxResults; handlers run
// on fan-out goroutines.
type waAgentChildCalls struct {
	parentCalls
	mu         sync.Mutex
	maxResults []int32
}

func (c *waAgentChildCalls) record(profile string, maxResults int32) {
	c.add(profile)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxResults = append(c.maxResults, maxResults)
}

// waAgentChildHandler answers ap1 with two pages (c1, then c2) and ap2 with
// one page (ap2-c1 and an id-less item). fail(profile, page) injects an error
// for a profile's page, numbered from 1.
func waAgentChildHandler(c waAgentChildCase, calls *waAgentChildCalls, fail func(profile string, page int) error) func(any) (any, error) {
	return func(in any) (any, error) {
		arn, next, maxResults := c.input(in)
		profile := strings.TrimPrefix(arn, waARN("agent-profile", ""))
		calls.record(profile, maxResults)
		page := 1
		if next != nil {
			page = 2
		}
		if err := fail(profile, page); err != nil {
			return nil, err
		}
		switch {
		case profile == "ap1" && page == 1:
			return c.page([]string{"c1"}, ptrStr("t1")), nil
		case profile == "ap1":
			return c.page([]string{"c2"}, nil), nil
		default:
			return c.page([]string{profile + "-c1", ""}, nil), nil
		}
	}
}

func noAgentFail(string, int) error { return nil }

func TestWAAgentChildPhases_TwoPagesStoredUnderProfiles(t *testing.T) {
	t.Parallel()
	for _, c := range waAgentChildCases() {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			parents := seedWAAgentProfiles(t, st)
			calls := &waAgentChildCalls{}
			client := waStubClient(map[string]func(any) (any, error){c.op: waAgentChildHandler(c, calls, noAgentFail)})
			total, inserted, err := c.run(context.Background(), client, parents, st)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := calls.distinct(); !slices.Equal(got, []string{"ap1", "ap2"}) {
				t.Errorf("profiles queried = %v, want [ap1 ap2]", got)
			}
			if !slices.Equal(calls.maxResults, []int32{1000, 1000, 1000}) {
				t.Errorf("MaxResults per call = %v, want [1000 1000 1000]", calls.maxResults)
			}
			if total != 3 || inserted != 3 {
				t.Errorf("total, inserted = %d, %d; want 3, 3 (both ap1 pages, id-less item skipped)", total, inserted)
			}
			rows := rowsByNativeID(t, st, c.typ)
			if len(rows) != 3 {
				t.Fatalf("rows = %v, want 3", rows)
			}
			if _, ok := rows[waAgentChildID("ap1", c.segment, "c2")]; !ok {
				t.Errorf("no row for page-two item ap1/c2; got %v", rows)
			}
			r, ok := rows[waAgentChildID("ap1", c.segment, "c1")]
			if !ok {
				t.Fatalf("no row for ap1/c1; got %v", rows)
			}
			if r.Type != c.typ || sv(r.Region) != testRegion || sv(r.Name) != "title c1" || sv(r.CreatedAt) != "2026-09-01T08:00:00Z" {
				t.Errorf("row = type %q region %q name %q createdAt %q", r.Type, sv(r.Region), sv(r.Name), sv(r.CreatedAt))
			}
			if !c.attrsOK(r.AttributesJSON) {
				t.Errorf("attributes = %s, want the SDK element", r.AttributesJSON)
			}
			assertContainsNative(t, st, waARN("agent-profile", "ap1"), waAgentChildID("ap1", c.segment, "c1"))
			assertContainsNative(t, st, waARN("agent-profile", "ap1"), waAgentChildID("ap1", c.segment, "c2"))
			assertContainsNative(t, st, waARN("agent-profile", "ap2"), waAgentChildID("ap2", c.segment, "ap2-c1"))
		})
	}
}

func TestWAAgentChildPhases_Empty(t *testing.T) {
	t.Parallel()
	for _, c := range waAgentChildCases() {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			parents := seedWAAgentProfiles(t, st)
			client := waStubClient(map[string]func(any) (any, error){c.op: func(any) (any, error) { return c.page(nil, nil), nil }})
			total, inserted, err := c.run(context.Background(), client, parents, st)
			if err != nil || total != 0 || inserted != 0 || len(rowsByNativeID(t, st, c.typ)) != 0 {
				t.Fatalf("total, inserted = %d, %d, err = %v; want 0 rows, nil", total, inserted, err)
			}
		})
	}
}

// A token the service repeats must end a profile's listing, not loop on it.
func TestWAAgentChildPhases_RepeatedTokenStops(t *testing.T) {
	t.Parallel()
	for _, c := range waAgentChildCases() {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			parents := seedWAAgentProfiles(t, st)[:1]
			calls := 0
			client := waStubClient(map[string]func(any) (any, error){c.op: func(any) (any, error) {
				calls++
				if calls > 5 {
					return nil, apiErr("TooManyCalls", "repeated token followed")
				}
				return c.page([]string{fmt.Sprintf("c%d", calls)}, ptrStr("same")), nil
			}})
			total, _, err := c.run(context.Background(), client, parents, st)
			if err != nil || total != 2 || calls != 2 {
				t.Fatalf("total = %d, calls = %d, err = %v; want 2, 2, nil", total, calls, err)
			}
		})
	}
}

// Each per-profile failure is skipped, warned or returned by its code and
// page; a skipped profile costs no sibling its rows.
func TestWAAgentChildPhases_ErrorLadder(t *testing.T) {
	t.Parallel()
	gap := apiErr("UnknownOperationException", "unknown operation")
	unknownOp := &watypes.ValidationException{Message: ptrStr("unknown operation"), Reason: watypes.ValidationExceptionReasonUnknownOperation}
	fieldInvalid := &watypes.ValidationException{Message: ptrStr("bad field"), Reason: watypes.ValidationExceptionReasonFieldValidationFailed}
	throttled := apiErr("ThrottlingException", "Rate exceeded")
	onAP1 := func(err error, page int) func(string, int) error {
		return func(profile string, p int) error {
			if profile == "ap1" && p == page {
				return err
			}
			return nil
		}
	}
	for _, tc := range []struct {
		name      string
		fail      func(profile string, page int) error
		wantErr   string
		wantAP2   bool
		wantWarns int
	}{
		{name: "profile gone skipped silently", fail: onAP1(waGone, 1), wantAP2: true},
		{name: "denied profile warns and keeps siblings", fail: onAP1(waDenied, 1), wantAP2: true, wantWarns: 1},
		{name: "denied everywhere warns once", fail: func(string, int) error { return waDenied }, wantWarns: 1},
		{name: "transient profile warns and keeps siblings", fail: onAP1(throttled, 1), wantAP2: true, wantWarns: 1},
		{name: "first-page op gap is silent", fail: onAP1(gap, 1), wantAP2: true},
		{name: "first-page unknown-operation validation is silent", fail: onAP1(unknownOp, 1), wantAP2: true},
		{name: "op gap after a page is returned", fail: onAP1(gap, 2), wantErr: "UnknownOperationException"},
		{name: "unknown-operation validation after a page is returned", fail: onAP1(unknownOp, 2), wantErr: "ValidationException"},
		{name: "validation with another reason is returned", fail: onAP1(fieldInvalid, 1), wantErr: "ValidationException"},
		// The op names the profile, so an untyped 404 may be about it, not an op gap.
		{name: "first-page untyped 404 is returned", fail: onAP1(respErrCode(404), 1), wantErr: "UnknownError"},
	} {
		for _, c := range waAgentChildCases() {
			t.Run(c.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				parents := seedWAAgentProfiles(t, st)
				warns := countOpWarnings(st, "wellarchitected:"+c.op)
				client := waStubClient(map[string]func(any) (any, error){c.op: waAgentChildHandler(c, &waAgentChildCalls{}, tc.fail)})
				total, inserted, err := c.run(context.Background(), client, parents, st)
				if tc.wantErr == "" && err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if tc.wantErr != "" && (!isAPIErrorCode(err, tc.wantErr) || classifyServiceError(err) != outcomeError) {
					t.Fatalf("err = %v, want a hard %s", err, tc.wantErr)
				}
				if *warns != tc.wantWarns {
					t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
				}
				want := 0
				if tc.wantAP2 {
					want = 1
				}
				rows := rowsByNativeID(t, st, c.typ)
				if total != want || inserted != want || len(rows) != want {
					t.Fatalf("total, inserted = %d, %d, rows = %v; want %d", total, inserted, rows, want)
				}
				if _, ok := rows[waAgentChildID("ap2", c.segment, "ap2-c1")]; ok != tc.wantAP2 {
					t.Errorf("ap2 row present = %v, want %v; got %v", ok, tc.wantAP2, rows)
				}
			})
		}
	}
}

// The context and goal phases fan out over exactly the profiles the profile
// phase stored: a page-two profile is listed, an ARN-less one is not, and a
// denied profile listing hands them none.
func TestScanWellArchitected_AgentChildrenListStoredProfiles(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		profiles     func(any) (any, error)
		wantProfiles []string
	}{
		{
			name: "two pages", profiles: waAgentProfilePages(new(int),
				waAgentPage(waAgentProfile("ap1")),
				waAgentPage(waAgentProfile("ap2"), watypes.AgentProfileSummary{Name: ptrStr("no-arn")})),
			wantProfiles: []string{"ap1", "ap2"},
		},
		{name: "profiles denied", profiles: func(any) (any, error) { return nil, waDenied }},
		{
			name: "page-two denial keeps page one", profiles: waAgentProfilePages(new(int),
				waAgentPage(waAgentProfile("ap1")), waAgentFail(waDenied)),
			wantProfiles: []string{"ap1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			h := waAgentServiceHandlers()
			h["ListAgentProfiles"] = tc.profiles
			calls := map[string]*waAgentChildCalls{}
			for _, c := range waAgentChildCases() {
				calls[c.op] = &waAgentChildCalls{}
				h[c.op] = waAgentChildHandler(c, calls[c.op], noAgentFail)
			}
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(h, waDenied)
			if _, _, err := scanWellArchitected(context.Background(), acct, testRegion, st, testScanID); err != nil {
				t.Fatalf("scanWellArchitected: %v", err)
			}
			for op, pc := range calls {
				if got := pc.distinct(); !slices.Equal(got, tc.wantProfiles) {
					t.Errorf("%s profiles queried = %v, want %v", op, got, tc.wantProfiles)
				}
			}
		})
	}
}

// The context and goal phases run after every earlier phase and whatever
// they returned; a hard error in either reaches the dispatcher without
// costing the tool, profile or sibling rows, and is not hidden by a transient
// error elsewhere.
func TestScanWellArchitected_AgentChildPhaseErrors(t *testing.T) {
	t.Parallel()
	throttled := apiErr("ThrottlingException", "Rate exceeded")
	for _, tc := range []struct {
		name, failing, sibling, sibType string
		throttleTool                    bool
		wantWarns                       int
	}{
		{name: "contexts fail", failing: "ListAgentContexts", sibling: "/goal/goal1", sibType: TypeWellArchitectedAgentGoal},
		{name: "goals fail", failing: "ListAgentGoals", sibling: "/context/ctx1", sibType: TypeWellArchitectedAgentContext},
		{name: "tool throttled, goals fail", failing: "ListAgentGoals", sibling: "/context/ctx1", sibType: TypeWellArchitectedAgentContext, throttleTool: true, wantWarns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "wellarchitected:ListTemplateShares")
			h := waAgentServiceHandlers()
			h[tc.failing] = func(any) (any, error) { return nil, waBadInput }
			if tc.throttleTool {
				h["ListTemplateShares"] = func(any) (any, error) { return nil, throttled }
			}
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfig(h, waDenied)
			_, _, err := scanWellArchitected(context.Background(), acct, testRegion, st, testScanID)
			if !isAPIErrorCode(err, "ValidationException") || classifyServiceError(err) != outcomeError {
				t.Fatalf("err = %v, want a hard ValidationException", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("tool transient warnings = %d, want %d", *warns, tc.wantWarns)
			}
			if len(rowsByNativeID(t, st, TypeWellArchitectedReviewTemplate)) != 1 {
				t.Error("review-template row from the tool phases lost")
			}
			if len(rowsByNativeID(t, st, TypeWellArchitectedAgentProfile)) != 1 {
				t.Error("agent profile row lost")
			}
			if _, ok := rowsByNativeID(t, st, tc.sibType)[waARN("agent-profile", "ap1")+tc.sibling]; !ok {
				t.Errorf("sibling %s row lost", tc.sibType)
			}
		})
	}
}

// waTotal is the rows waFixture's scan stores: 3 workloads, 4 lenses, 2
// profiles and 2 review templates.
const waTotal = 11

var waThrottle = apiErr("ThrottlingException", "Rate exceeded")

// waFixture answers every op of a Well-Architected scan and records its
// calls. Each tool listing has two pages: page one holds an item this account
// owns (w1, custom1, pr1, t1), page two one another account owns (w2, the
// shared-in lens shared1, pr2, t2). Page two of every tool listing also holds
// an element named "nameless" without an ARN (a CUSTOM_SELF one for
// ListLenses). Page two of ListWorkloads also holds w3, this account's but
// with no WorkloadId; page two of ListLenses also holds the AWS-official lens
// serverless and custom2, this account's with no LensType. w1, custom1, pr1
// and t1 are tagged {"id": <their id>}; the
// other rows have no tags. The child listings and ListAgentProfiles answer
// empty. errs fails a call: "<Op>/<key>", where key is "" or "p2" for the
// two pages of a tool listing, the ARN for ListTagsForResource, the parent
// key for a child listing and "" for ListAgentProfiles. Handlers run on
// fan-out goroutines, so mu guards the state they share.
type waFixture struct {
	partition, region string
	mu                sync.Mutex
	errs              map[string]error
	keys              map[string][]string
	maxResults        map[string][]int32
}

func newWAFixture(partition, region string) *waFixture {
	return &waFixture{partition: partition, region: region, errs: map[string]error{}, keys: map[string][]string{}, maxResults: map[string][]int32{}}
}

// arn is the fixture's ARN of a resource owner owns, built independently of
// the scanner.
func (f *waFixture) arn(owner, kind, id string) string {
	return "arn:" + f.partition + ":wellarchitected:" + f.region + ":" + owner + ":" + kind + "/" + id
}

func (f *waFixture) own(kind, id string) string     { return f.arn(testAccountID, kind, id) }
func (f *waFixture) foreign(kind, id string) string { return f.arn(waForeignOwner, kind, id) }

// official is the ARN form the SDK documents for an AWS-official lens.
func (f *waFixture) official() string {
	return "arn:" + f.partition + ":wellarchitected::aws:lens/serverless"
}

// call records op's call under key with in's MaxResults and returns the
// error errs holds for it.
func (f *waFixture) call(op, key string, in any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys[op] = append(f.keys[op], key)
	if n := bacField[*int32](in, "MaxResults"); n != nil {
		f.maxResults[op] = append(f.maxResults[op], *n)
	}
	return f.errs[op+"/"+key]
}

func (f *waFixture) called(op string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(slices.Values(f.keys[op]))
}

func (f *waFixture) pageCaps(op string) []int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.maxResults[op])
}

// waPage answers page one (token nil) with first and NextToken p2, and page
// two with second.
func waPage[Out any](token *string, first, second func(next *string) Out) Out {
	if token == nil {
		return first(ptrStr("p2"))
	}
	return second(nil)
}

func (f *waFixture) handlers() map[string]func(in any) (any, error) {
	child := func(op string, key func(in any) *string, out any) func(in any) (any, error) {
		return func(in any) (any, error) {
			return out, f.call(op, sv(key(in)), in)
		}
	}
	return map[string]func(in any) (any, error){
		"ListWorkloads": func(in any) (any, error) {
			req := in.(*wellarchitected.ListWorkloadsInput)
			if err := f.call("ListWorkloads", sv(req.NextToken), in); err != nil {
				return nil, err
			}
			return waPage(req.NextToken, func(next *string) *wellarchitected.ListWorkloadsOutput {
				return &wellarchitected.ListWorkloadsOutput{NextToken: next, WorkloadSummaries: []watypes.WorkloadSummary{
					{WorkloadArn: ptrStr(f.own("workload", "w1")), WorkloadId: ptrStr("w1"), WorkloadName: ptrStr("w-one"), Owner: ptrStr(testAccountID), ImprovementStatus: watypes.WorkloadImprovementStatusNotStarted},
				}}
			}, func(next *string) *wellarchitected.ListWorkloadsOutput {
				return &wellarchitected.ListWorkloadsOutput{NextToken: next, WorkloadSummaries: []watypes.WorkloadSummary{
					{WorkloadArn: ptrStr(f.foreign("workload", "w2")), WorkloadId: ptrStr("w2"), WorkloadName: ptrStr("w-two"), Owner: ptrStr(waForeignOwner)},
					{WorkloadArn: ptrStr(f.own("workload", "w3")), WorkloadName: ptrStr("w-three"), Owner: ptrStr(testAccountID)},
					{WorkloadName: ptrStr("nameless"), Owner: ptrStr(testAccountID)},
				}}
			}), nil
		},
		"ListLenses": func(in any) (any, error) {
			req := in.(*wellarchitected.ListLensesInput)
			if err := f.call("ListLenses", sv(req.NextToken), in); err != nil {
				return nil, err
			}
			return waPage(req.NextToken, func(next *string) *wellarchitected.ListLensesOutput {
				return &wellarchitected.ListLensesOutput{NextToken: next, LensSummaries: []watypes.LensSummary{
					{LensArn: ptrStr(f.own("lens", "custom1")), LensName: ptrStr("custom-one"), LensType: watypes.LensTypeCustomSelf, LensStatus: watypes.LensStatusCurrent, Owner: ptrStr(testAccountID)},
				}}
			}, func(next *string) *wellarchitected.ListLensesOutput {
				return &wellarchitected.ListLensesOutput{NextToken: next, LensSummaries: []watypes.LensSummary{
					{LensArn: ptrStr(f.official()), LensName: ptrStr("Serverless Lens"), LensType: watypes.LensTypeAwsOfficial, Owner: ptrStr("AWS")},
					{LensArn: ptrStr(f.foreign("lens", "shared1")), LensName: ptrStr("shared-one"), LensType: watypes.LensTypeCustomShared, Owner: ptrStr(waForeignOwner)},
					{LensArn: ptrStr(f.own("lens", "custom2")), LensName: ptrStr("custom-two"), Owner: ptrStr(testAccountID)},
					{LensName: ptrStr("nameless"), LensType: watypes.LensTypeCustomSelf, Owner: ptrStr(testAccountID)},
				}}
			}), nil
		},
		"ListProfiles": func(in any) (any, error) {
			req := in.(*wellarchitected.ListProfilesInput)
			if err := f.call("ListProfiles", sv(req.NextToken), in); err != nil {
				return nil, err
			}
			return waPage(req.NextToken, func(next *string) *wellarchitected.ListProfilesOutput {
				return &wellarchitected.ListProfilesOutput{NextToken: next, ProfileSummaries: []watypes.ProfileSummary{
					{ProfileArn: ptrStr(f.own("profile", "pr1")), ProfileName: ptrStr("profile-one"), ProfileVersion: ptrStr("v1"), Owner: ptrStr(testAccountID)},
				}}
			}, func(next *string) *wellarchitected.ListProfilesOutput {
				return &wellarchitected.ListProfilesOutput{NextToken: next, ProfileSummaries: []watypes.ProfileSummary{
					{ProfileArn: ptrStr(f.foreign("profile", "pr2")), ProfileName: ptrStr("profile-two"), Owner: ptrStr(waForeignOwner)},
					{ProfileName: ptrStr("nameless"), Owner: ptrStr(testAccountID)},
				}}
			}), nil
		},
		"ListReviewTemplates": func(in any) (any, error) {
			req := in.(*wellarchitected.ListReviewTemplatesInput)
			if err := f.call("ListReviewTemplates", sv(req.NextToken), in); err != nil {
				return nil, err
			}
			return waPage(req.NextToken, func(next *string) *wellarchitected.ListReviewTemplatesOutput {
				return &wellarchitected.ListReviewTemplatesOutput{NextToken: next, ReviewTemplates: []watypes.ReviewTemplateSummary{
					{TemplateArn: ptrStr(f.own("review-template", "t1")), TemplateName: ptrStr("template-one"), UpdateStatus: watypes.ReviewTemplateUpdateStatusCurrent, Owner: ptrStr(testAccountID)},
				}}
			}, func(next *string) *wellarchitected.ListReviewTemplatesOutput {
				return &wellarchitected.ListReviewTemplatesOutput{NextToken: next, ReviewTemplates: []watypes.ReviewTemplateSummary{
					{TemplateArn: ptrStr(f.foreign("review-template", "t2")), TemplateName: ptrStr("template-two"), Owner: ptrStr(waForeignOwner)},
					{TemplateName: ptrStr("nameless"), Owner: ptrStr(testAccountID)},
				}}
			}), nil
		},
		"ListTagsForResource": func(in any) (any, error) {
			arn := sv(in.(*wellarchitected.ListTagsForResourceInput).WorkloadArn)
			if err := f.call("ListTagsForResource", arn, in); err != nil {
				return nil, err
			}
			for _, r := range [][2]string{{"workload", "w1"}, {"lens", "custom1"}, {"profile", "pr1"}, {"review-template", "t1"}} {
				if arn == f.own(r[0], r[1]) {
					return &wellarchitected.ListTagsForResourceOutput{Tags: map[string]string{"id": r[1]}}, nil
				}
			}
			return &wellarchitected.ListTagsForResourceOutput{}, nil
		},
		"ListLensReviews":    child("ListLensReviews", func(in any) *string { return in.(*wellarchitected.ListLensReviewsInput).WorkloadId }, &wellarchitected.ListLensReviewsOutput{}),
		"ListWorkloadShares": child("ListWorkloadShares", func(in any) *string { return in.(*wellarchitected.ListWorkloadSharesInput).WorkloadId }, &wellarchitected.ListWorkloadSharesOutput{}),
		"ListLensShares":     child("ListLensShares", func(in any) *string { return in.(*wellarchitected.ListLensSharesInput).LensAlias }, &wellarchitected.ListLensSharesOutput{}),
		"ListProfileShares":  child("ListProfileShares", func(in any) *string { return in.(*wellarchitected.ListProfileSharesInput).ProfileArn }, &wellarchitected.ListProfileSharesOutput{}),
		"ListTemplateShares": child("ListTemplateShares", func(in any) *string { return in.(*wellarchitected.ListTemplateSharesInput).TemplateArn }, &wellarchitected.ListTemplateSharesOutput{}),
		"ListAgentProfiles":  child("ListAgentProfiles", func(any) *string { return nil }, &wellarchitected.ListAgentProfilesOutput{}),
	}
}

// taggedARNs is the ARN of every row whose tags the scan reads, sorted: every
// row but the AWS-official lens's. No element without an ARN is a row, so
// none sends the op an empty ARN.
func (f *waFixture) taggedARNs() []string {
	return slices.Sorted(slices.Values([]string{
		f.own("workload", "w1"), f.foreign("workload", "w2"), f.own("workload", "w3"),
		f.own("lens", "custom1"), f.foreign("lens", "shared1"), f.own("lens", "custom2"),
		f.own("profile", "pr1"), f.foreign("profile", "pr2"),
		f.own("review-template", "t1"), f.foreign("review-template", "t2"),
	}))
}

// scanWith runs the scan's real entry point against handlers in f's
// partition and region; every op handlers does not answer is denied.
func (f *waFixture) scanWith(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.Partition = f.partition
	acct.cfg = opStubConfig(handlers, waDenied)
	return scanWellArchitected(ctx, acct, f.region, st, testScanID)
}

func (f *waFixture) scan(ctx context.Context, st *store.Store) (int, int, error) {
	return f.scanWith(ctx, st, f.handlers())
}

// waListOps are the tool listings, each read in two pages.
var waListOps = []string{"ListWorkloads", "ListLenses", "ListProfiles", "ListReviewTemplates"}

// waFixtureWarnOps are the warnings f's scan sends unprompted, sorted: each
// tool listing's element without an ARN, and w3, stored without the
// WorkloadId its children are listed by.
var waFixtureWarnOps = []string{waListLensesOp, waListProfilesOp, waListReviewTemplatesOp, waListWorkloadsOp, waListWorkloadsOp}

// Every row is stored under the ARN its listing returned, with the tags
// ListTagsForResource returns (nil for none). The op is sent exactly those
// ARNs, the AWS-official lens's excepted, which is ManagedByProvider and
// costs no call; custom2, with no LensType, is read. Name comes from the list element and the attributes are
// the element; no row has a Status or CreatedAt. The stored workloads are
// the lens-review parents, and only what this account owns (Owner, or
// CUSTOM_SELF for a lens) is a share parent; w3, stored without a
// WorkloadId, is no parent and is warned about, as is each listing's element
// without an ARN. Every tool listing (ListWorkloads, ListLenses,
// ListProfiles, ListReviewTemplates) sends MaxResults 50.
func TestScanWA_StoresTagsOfTaggedTypes(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newWAFixture("aws", testRegion)

	total, inserted, err := f.scan(context.Background(), st)
	if err != nil || total != waTotal || inserted != waTotal {
		t.Fatalf("total, inserted, err = %d, %d, %v; want %d, %d, nil", total, inserted, err, waTotal, waTotal)
	}
	if got, want := f.called("ListTagsForResource"), f.taggedARNs(); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
	}
	for _, op := range waListOps {
		if got := f.pageCaps(op); !slices.Equal(got, []int32{50, 50}) {
			t.Errorf("%s MaxResults = %v; want [50 50]", op, got)
		}
	}
	for _, c := range []struct {
		typ, arn, name string
		tags           map[string]string
		managed        bool
	}{
		{TypeWellArchitectedWorkload, f.own("workload", "w1"), "w-one", map[string]string{"id": "w1"}, false},
		{TypeWellArchitectedWorkload, f.foreign("workload", "w2"), "w-two", nil, false},
		{TypeWellArchitectedWorkload, f.own("workload", "w3"), "w-three", nil, false},
		{TypeWellArchitectedLens, f.own("lens", "custom1"), "custom-one", map[string]string{"id": "custom1"}, false},
		{TypeWellArchitectedLens, f.official(), "Serverless Lens", nil, true},
		{TypeWellArchitectedLens, f.foreign("lens", "shared1"), "shared-one", nil, false},
		{TypeWellArchitectedLens, f.own("lens", "custom2"), "custom-two", nil, false},
		{TypeWellArchitectedProfile, f.own("profile", "pr1"), "profile-one", map[string]string{"id": "pr1"}, false},
		{TypeWellArchitectedProfile, f.foreign("profile", "pr2"), "profile-two", nil, false},
		{TypeWellArchitectedReviewTemplate, f.own("review-template", "t1"), "template-one", map[string]string{"id": "t1"}, false},
		{TypeWellArchitectedReviewTemplate, f.foreign("review-template", "t2"), "template-two", nil, false},
	} {
		r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
		if !ok {
			t.Errorf("%s: no row %s", c.typ, c.arn)
			continue
		}
		if sv(r.Name) != c.name || sv(r.Region) != testRegion || r.Status != nil || r.CreatedAt != nil || r.ManagedByProvider != c.managed {
			t.Errorf("%s name/region/status/created/managed = %q/%q/%v/%v/%v; want %q/%s/nil/nil/%v",
				c.arn, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, r.ManagedByProvider, c.name, testRegion, c.managed)
		}
		if c.tags == nil {
			if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", c.arn, *r.TagsJSON)
			}
		} else {
			assertTagsMap(t, r.TagsJSON, c.tags)
		}
	}
	for _, c := range []struct {
		typ, arn string
		into     any
		check    func(any) bool
	}{
		{TypeWellArchitectedWorkload, f.own("workload", "w1"), &watypes.WorkloadSummary{}, func(v any) bool {
			w := v.(*watypes.WorkloadSummary)
			return sv(w.WorkloadId) == "w1" && w.ImprovementStatus == watypes.WorkloadImprovementStatusNotStarted
		}},
		{TypeWellArchitectedLens, f.own("lens", "custom1"), &watypes.LensSummary{}, func(v any) bool {
			l := v.(*watypes.LensSummary)
			return l.LensType == watypes.LensTypeCustomSelf && l.LensStatus == watypes.LensStatusCurrent
		}},
		{TypeWellArchitectedProfile, f.own("profile", "pr1"), &watypes.ProfileSummary{}, func(v any) bool {
			return sv(v.(*watypes.ProfileSummary).ProfileVersion) == "v1"
		}},
		{TypeWellArchitectedReviewTemplate, f.own("review-template", "t1"), &watypes.ReviewTemplateSummary{}, func(v any) bool {
			return v.(*watypes.ReviewTemplateSummary).UpdateStatus == watypes.ReviewTemplateUpdateStatusCurrent
		}},
	} {
		if err := json.Unmarshal([]byte(rowsByNativeID(t, st, c.typ)[c.arn].AttributesJSON), c.into); err != nil || !c.check(c.into) {
			t.Errorf("%s attributes = %+v (%v); want the list element", c.arn, c.into, err)
		}
	}
	for op, want := range map[string][]string{
		"ListLensReviews":    {"w1", "w2"},
		"ListWorkloadShares": {"w1"},
		"ListLensShares":     {f.own("lens", "custom1")},
		"ListProfileShares":  {f.own("profile", "pr1")},
		"ListTemplateShares": {f.own("review-template", "t1")},
	} {
		if got := f.called(op); !slices.Equal(got, want) {
			t.Errorf("%s keys = %v; want %v", op, got, want)
		}
	}
	w := warns()
	if got := lxWarnOps(w); !slices.Equal(got, waFixtureWarnOps) {
		t.Errorf("warnings = %v; want ops %v", w, waFixtureWarnOps)
	}
}

// A rescan reports every stored row in its total, not only the newly inserted
// ones. The dispatcher reports total for the service, and a first scan into an
// empty store has total == inserted, so only a rescan catches the two swapped.
func TestScanWA_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := newWAFixture("aws", testRegion)
	if _, _, err := f.scan(context.Background(), st); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, _, err := f.scan(context.Background(), st)
	if err != nil || total != waTotal {
		t.Errorf("rescan total = %d, err = %v; want %d, nil", total, err, waTotal)
	}
}

// With every tool listing empty, no tag read and no child listing is made.
func TestScanWA_EmptyListings(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := newWAFixture("aws", testRegion)
	handlers := f.handlers()
	handlers["ListWorkloads"] = func(in any) (any, error) {
		return &wellarchitected.ListWorkloadsOutput{}, f.call("ListWorkloads", "", in)
	}
	handlers["ListLenses"] = func(in any) (any, error) {
		return &wellarchitected.ListLensesOutput{}, f.call("ListLenses", "", in)
	}
	handlers["ListProfiles"] = func(in any) (any, error) {
		return &wellarchitected.ListProfilesOutput{}, f.call("ListProfiles", "", in)
	}
	handlers["ListReviewTemplates"] = func(in any) (any, error) {
		return &wellarchitected.ListReviewTemplatesOutput{}, f.call("ListReviewTemplates", "", in)
	}

	total, _, err := f.scanWith(context.Background(), st, handlers)
	if err != nil || total != 0 {
		t.Errorf("total, err = %d, %v; want 0, nil", total, err)
	}
	for _, op := range waListOps {
		if got := f.called(op); len(got) != 1 {
			t.Errorf("%s calls = %v; want 1", op, got)
		}
	}
	for _, op := range []string{"ListTagsForResource", "ListLensReviews", "ListWorkloadShares", "ListLensShares", "ListProfileShares", "ListTemplateShares"} {
		if got := f.called(op); len(got) != 0 {
			t.Errorf("%s calls = %v; want none", op, got)
		}
	}
}

// A failed tag read of this account's resource: ResourceNotFoundException is
// modeled, so a resource deleted since it was listed is dropped silently and
// is no parent of a child listing; a denial keeps the row untagged and warns once under the tag
// op with the scan's scope; a throttle keeps it and is transient; any other
// error keeps it and is an error. A resource another account owns (Owner)
// whose read is denied or not found is kept untagged and silent.
func TestScanWA_TagReadFailures(t *testing.T) {
	t.Parallel()
	f0 := newWAFixture("aws", testRegion)
	for _, c := range []struct {
		name, arn, typ string
		err            error
		outcome        serviceOutcome // -1: no error
		code           string
		wantTotal      int
		extraWarn      bool
		children       map[string][]string // the keys of each child listing named
	}{
		{"workload/not found", f0.own("workload", "w1"), TypeWellArchitectedWorkload, waGone, -1, "", waTotal - 1, false, map[string][]string{"ListLensReviews": {"w2"}, "ListWorkloadShares": nil}},
		{"lens/not found", f0.own("lens", "custom1"), TypeWellArchitectedLens, waGone, -1, "", waTotal - 1, false, map[string][]string{"ListLensShares": nil}},
		{"profile/not found", f0.own("profile", "pr1"), TypeWellArchitectedProfile, waGone, -1, "", waTotal - 1, false, map[string][]string{"ListProfileShares": nil}},
		{"review template/not found", f0.own("review-template", "t1"), TypeWellArchitectedReviewTemplate, waGone, -1, "", waTotal - 1, false, map[string][]string{"ListTemplateShares": nil}},
		{"lens/denied", f0.own("lens", "custom1"), TypeWellArchitectedLens, waDenied, -1, "", waTotal, true, nil},
		{"profile/transient", f0.own("profile", "pr1"), TypeWellArchitectedProfile, waThrottle, outcomeTransient, "ThrottlingException", waTotal, false, nil},
		{"review template/other error", f0.own("review-template", "t1"), TypeWellArchitectedReviewTemplate, waBadInput, outcomeError, "ValidationException", waTotal, false, nil},
		{"shared workload/denied", f0.foreign("workload", "w2"), TypeWellArchitectedWorkload, waDenied, -1, "", waTotal, false, nil},
		{"shared lens/not found", f0.foreign("lens", "shared1"), TypeWellArchitectedLens, waGone, -1, "", waTotal, false, nil},
		{"shared profile/not found", f0.foreign("profile", "pr2"), TypeWellArchitectedProfile, waGone, -1, "", waTotal, false, nil},
		{"shared review template/denied", f0.foreign("review-template", "t2"), TypeWellArchitectedReviewTemplate, waDenied, -1, "", waTotal, false, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newWAFixture("aws", testRegion)
			f.errs["ListTagsForResource/"+c.arn] = c.err

			total, _, err := f.scan(context.Background(), st)
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			r, kept := rowsByNativeID(t, st, c.typ)[c.arn]
			if wantKept := c.wantTotal == waTotal; kept != wantKept {
				t.Fatalf("row stored = %v; want %v", kept, wantKept)
			}
			if kept && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			wantWarns := slices.Clone(waFixtureWarnOps)
			if c.extraWarn {
				wantWarns = slices.Sorted(slices.Values(append(wantWarns, waTagsOp)))
			}
			w := warns()
			if got := lxWarnOps(w); !slices.Equal(got, wantWarns) {
				t.Fatalf("warnings = %v; want ops %v", w, wantWarns)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
			for op, want := range c.children {
				if got := f.called(op); !slices.Equal(got, want) {
					t.Errorf("%s keys = %v; want %v, the dropped row no parent", op, got, want)
				}
			}
		})
	}
}

// Denied tag reads in every tool listing send one warning for the whole
// scan; the rows are kept.
func TestScanWA_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, waTagsOp)
	f := newWAFixture("aws", testRegion)
	for _, r := range [][2]string{{"workload", "w1"}, {"lens", "custom1"}, {"profile", "pr1"}, {"review-template", "t1"}} {
		f.errs["ListTagsForResource/"+f.own(r[0], r[1])] = waDenied
	}

	total, _, err := f.scan(context.Background(), st)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != waTotal || *warns != 1 {
		t.Errorf("total = %d, %s warnings = %d; want %d, 1", total, waTagsOp, *warns, waTotal)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanWA_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, waTagsOp)
	f := newWAFixture("aws", testRegion)
	f.errs["ListTagsForResource/"+f.own("workload", "w1")] = waThrottle
	f.errs["ListTagsForResource/"+f.own("review-template", "t1")] = waBadInput

	_, _, err := f.scan(context.Background(), st)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", waTagsOp, *warns)
	}
}

// A tool listing failing on its second page keeps its first page and every
// later phase runs: a hard error is returned, a denial warns once. A failing
// first page costs that listing only.
func TestScanWA_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, key string
		err       error
		wantTotal int
		wantWarns []string
	}{
		{"later page error", "ListWorkloads/p2", waBadInput, waTotal - 2, []string{waListLensesOp, waListProfilesOp, waListReviewTemplatesOp}},
		{"later page denial", "ListWorkloads/p2", waDenied, waTotal - 2, []string{waListLensesOp, waListProfilesOp, waListReviewTemplatesOp, waListWorkloadsOp}},
		{"first page error", "ListLenses/", waBadInput, waTotal - 4, []string{waListProfilesOp, waListReviewTemplatesOp, waListWorkloadsOp, waListWorkloadsOp}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newWAFixture("aws", testRegion)
			f.errs[c.key] = c.err

			total, _, err := f.scan(context.Background(), st)
			if c.err == waDenied {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "ValidationException")
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			if got := lxWarnOps(warns()); !slices.Equal(got, c.wantWarns) {
				t.Errorf("warning ops = %v; want %v", got, c.wantWarns)
			}
			if c.key == "ListWorkloads/p2" {
				if got := storedNativeIDs(t, st, TypeWellArchitectedWorkload); !slices.Equal(got, []string{f.own("workload", "w1")}) {
					t.Errorf("workload rows = %v; want page one's w1", got)
				}
				if got := f.called("ListLensReviews"); !slices.Equal(got, []string{"w1"}) {
					t.Errorf("ListLensReviews keys = %v; want [w1]", got)
				}
			}
			if got := len(rowsByNativeID(t, st, TypeWellArchitectedReviewTemplate)); got != 2 {
				t.Errorf("review template rows = %d; want 2 from the last tool listing", got)
			}
		})
	}
}

// A repeated NextToken ends the listing with errRepeatedNextToken after its
// first page is stored.
func TestScanWA_RepeatedToken(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := newWAFixture("aws", testRegion)
	handlers := f.handlers()
	handlers["ListReviewTemplates"] = func(in any) (any, error) {
		if err := f.call("ListReviewTemplates", "", in); err != nil {
			return nil, err
		}
		return &wellarchitected.ListReviewTemplatesOutput{NextToken: ptrStr("same"), ReviewTemplates: []watypes.ReviewTemplateSummary{
			{TemplateArn: ptrStr(f.own("review-template", "t1")), Owner: ptrStr(testAccountID)},
		}}, nil
	}

	_, _, err := f.scanWith(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if got := len(f.called("ListReviewTemplates")); got != 2 {
		t.Errorf("ListReviewTemplates calls = %d; want 2", got)
	}
	if got := storedNativeIDs(t, st, TypeWellArchitectedReviewTemplate); !slices.Equal(got, []string{f.own("review-template", "t1")}) {
		t.Errorf("review template rows = %v; want [t1]", got)
	}
}

func TestScanWA_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "wellarchitected.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		f := newWAFixture("aws", testRegion)
		f.errs["ListWorkloads/"] = nx

		total, _, err := f.scan(context.Background(), st)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 {
			t.Errorf("total = %d; want 0", total)
		}
		if got := f.called("ListLenses"); len(got) != 0 {
			t.Errorf("ListLenses calls = %v; want none once the region proved unavailable", got)
		}
	})
	t.Run("after workloads were stored", func(t *testing.T) {
		st := newTestStore(t)
		f := newWAFixture("aws", testRegion)
		f.errs["ListLenses/"] = nx

		total, _, err := f.scan(context.Background(), st)
		assertGlueOutcome(t, err, outcomeError)
		if want := waTotal - 4; total != want {
			t.Errorf("total = %d; want the %d rows stored but the lenses", total, want)
		}
	})
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing; the later phases are not run, and the rows
// stored before it are kept.
func TestScanWA_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := newWAFixture("aws", testRegion)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handlers := f.handlers()
	handlers["ListProfiles"] = func(any) (any, error) {
		cancel()
		return nil, context.Canceled
	}

	total, _, err := f.scanWith(ctx, st, handlers)
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
	if total != 7 {
		t.Errorf("total = %d; want the 7 workloads and lenses stored before ctx ended", total)
	}
	if got := f.called("ListReviewTemplates"); len(got) != 0 {
		t.Errorf("ListReviewTemplates calls = %v; want none after ctx ended", got)
	}
}

// A failed write stops the scan: no warning about the rows it lost (custom1's
// denied tag read among them), no later phase runs, and the rows of the
// phases before it stay stored.
func TestScanWA_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeWellArchitectedLens)
	f := newWAFixture("aws", testRegion)
	f.errs["ListTagsForResource/"+f.own("lens", "custom1")] = waDenied

	total, _, err := f.scan(context.Background(), st)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if got, want := lxWarnOps(warns()), []string{waListWorkloadsOp, waListWorkloadsOp}; !slices.Equal(got, want) {
		t.Errorf("warning ops = %v; want %v, those sent before the failed write", got, want)
	}
	for _, op := range []string{"ListProfiles", "ListReviewTemplates", "ListLensReviews", "ListAgentProfiles"} {
		if got := f.called(op); len(got) != 0 {
			t.Errorf("%s calls = %v; want none after the failed write", op, got)
		}
	}
	if n := len(rowsByNativeID(t, st, TypeWellArchitectedWorkload)); n != 3 || total != 3 {
		t.Errorf("workload rows, total = %d, %d; want 3, 3", n, total)
	}
}

// A workload stored without a WorkloadId on page one is not warned about
// when page two's write fails: no warning follows a failed write.
func TestScanWA_FailedWorkloadWriteSendsNoChildIDWarning(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newWAFixture("aws", testRegion)
	refused := f.own("workload", "w1")
	if _, err := st.DB().Exec(`CREATE TRIGGER refuse_w1 BEFORE INSERT ON resources
		WHEN NEW.native_id = '` + refused + `'
		BEGIN SELECT RAISE(ABORT, 'test: write refused'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	handlers := f.handlers()
	handlers["ListWorkloads"] = func(in any) (any, error) {
		req := in.(*wellarchitected.ListWorkloadsInput)
		if err := f.call("ListWorkloads", sv(req.NextToken), in); err != nil {
			return nil, err
		}
		return waPage(req.NextToken, func(next *string) *wellarchitected.ListWorkloadsOutput {
			return &wellarchitected.ListWorkloadsOutput{NextToken: next, WorkloadSummaries: []watypes.WorkloadSummary{
				{WorkloadArn: ptrStr(f.own("workload", "w3")), WorkloadName: ptrStr("w-three"), Owner: ptrStr(testAccountID)},
			}}
		}, func(next *string) *wellarchitected.ListWorkloadsOutput {
			return &wellarchitected.ListWorkloadsOutput{NextToken: next, WorkloadSummaries: []watypes.WorkloadSummary{
				{WorkloadArn: ptrStr(refused), WorkloadId: ptrStr("w1"), Owner: ptrStr(testAccountID)},
			}}
		}), nil
	}

	total, _, err := f.scanWith(context.Background(), st, handlers)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if got := storedNativeIDs(t, st, TypeWellArchitectedWorkload); total != 1 || !slices.Equal(got, []string{f.own("workload", "w3")}) {
		t.Errorf("total, workload rows = %d, %v; want 1, page one's w3", total, got)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none after the failed write", w)
	}
}

// A GovCloud or China ARN reaches the tag op unchanged and is the row's
// NativeID.
func TestScanWA_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	for _, c := range partitionFixtures {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			f := newWAFixture(c.partition, c.region)
			if _, _, err := f.scan(context.Background(), st); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got, want := f.called("ListTagsForResource"), f.taggedARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			for typ, r := range map[string][2]string{
				TypeWellArchitectedWorkload:       {"workload", "w1"},
				TypeWellArchitectedLens:           {"lens", "custom1"},
				TypeWellArchitectedProfile:        {"profile", "pr1"},
				TypeWellArchitectedReviewTemplate: {"review-template", "t1"},
			} {
				assertTagsMap(t, rowsByNativeID(t, st, typ)[f.own(r[0], r[1])].TagsJSON, map[string]string{"id": r[1]})
			}
		})
	}
}
