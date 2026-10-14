package aws

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

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

// waListCase drives one of the four parent list phases: page one holds an
// owned item, page two a foreign one and an ARN-less one.
type waListCase struct {
	op, typ   string
	handler   func(in any) (any, error)
	ownedARN  string
	otherARN  string
	allParent func(*waParents) []childParent
	owned     func(*waParents) []childParent
	run       func(context.Context, wellArchitectedAPI, *store.Store, *waParents) (int, error)
}

func waListCases() []waListCase {
	acct := newTestAccount(testAccountID)
	return []waListCase{
		{
			op: "ListWorkloads", typ: TypeWellArchitectedWorkload,
			ownedARN: waARN("workload", "w1"), otherARN: waARN("workload", "w2"),
			handler: func(in any) (any, error) {
				if in.(*wellarchitected.ListWorkloadsInput).NextToken == nil {
					return &wellarchitected.ListWorkloadsOutput{NextToken: ptrStr("p2"), WorkloadSummaries: []watypes.WorkloadSummary{
						{WorkloadArn: ptrStr(waARN("workload", "w1")), WorkloadId: ptrStr("w1"), Owner: ptrStr(testAccountID)},
					}}, nil
				}
				return &wellarchitected.ListWorkloadsOutput{WorkloadSummaries: []watypes.WorkloadSummary{
					{WorkloadArn: ptrStr(waARN("workload", "w2")), WorkloadId: ptrStr("w2"), Owner: ptrStr(waForeignOwner)},
					{WorkloadId: ptrStr("no-arn"), Owner: ptrStr(testAccountID)},
				}}, nil
			},
			allParent: func(p *waParents) []childParent { return p.workloads },
			owned:     func(p *waParents) []childParent { return p.ownedWorkloads },
			run: func(ctx context.Context, c wellArchitectedAPI, st *store.Store, p *waParents) (int, error) {
				total, _, err := scanWAWorkloads(ctx, c, acct, testRegion, st, testScanID, p)
				return total, err
			},
		},
		{
			op: "ListLenses", typ: TypeWellArchitectedLens,
			ownedARN: waARN("lens", "custom1"), otherARN: "arn:aws:wellarchitected::aws:lens/serverless",
			handler: func(in any) (any, error) {
				if in.(*wellarchitected.ListLensesInput).NextToken == nil {
					return &wellarchitected.ListLensesOutput{NextToken: ptrStr("p2"), LensSummaries: []watypes.LensSummary{
						{LensArn: ptrStr(waARN("lens", "custom1")), LensType: watypes.LensTypeCustomSelf, Owner: ptrStr(testAccountID)},
					}}, nil
				}
				return &wellarchitected.ListLensesOutput{LensSummaries: []watypes.LensSummary{
					{LensArn: ptrStr("arn:aws:wellarchitected::aws:lens/serverless"), LensType: watypes.LensTypeAwsOfficial, Owner: ptrStr("AWS")},
					{LensType: watypes.LensTypeCustomSelf},
				}}, nil
			},
			owned: func(p *waParents) []childParent { return p.ownedLenses },
			run: func(ctx context.Context, c wellArchitectedAPI, st *store.Store, p *waParents) (int, error) {
				total, _, err := scanWALenses(ctx, c, acct, testRegion, st, testScanID, p)
				return total, err
			},
		},
		{
			op: "ListProfiles", typ: TypeWellArchitectedProfile,
			ownedARN: waARN("profile", "pr1"), otherARN: waARN("profile", "pr2"),
			handler: func(in any) (any, error) {
				if in.(*wellarchitected.ListProfilesInput).NextToken == nil {
					return &wellarchitected.ListProfilesOutput{NextToken: ptrStr("p2"), ProfileSummaries: []watypes.ProfileSummary{
						{ProfileArn: ptrStr(waARN("profile", "pr1")), Owner: ptrStr(testAccountID)},
					}}, nil
				}
				return &wellarchitected.ListProfilesOutput{ProfileSummaries: []watypes.ProfileSummary{
					{ProfileArn: ptrStr(waARN("profile", "pr2")), Owner: ptrStr(waForeignOwner)},
					{Owner: ptrStr(testAccountID)},
				}}, nil
			},
			owned: func(p *waParents) []childParent { return p.ownedProfiles },
			run: func(ctx context.Context, c wellArchitectedAPI, st *store.Store, p *waParents) (int, error) {
				total, _, err := scanWAProfiles(ctx, c, acct, testRegion, st, testScanID, p)
				return total, err
			},
		},
		{
			op: "ListReviewTemplates", typ: TypeWellArchitectedReviewTemplate,
			ownedARN: waARN("review-template", "t1"), otherARN: waARN("review-template", "t2"),
			handler: func(in any) (any, error) {
				if in.(*wellarchitected.ListReviewTemplatesInput).NextToken == nil {
					return &wellarchitected.ListReviewTemplatesOutput{NextToken: ptrStr("p2"), ReviewTemplates: []watypes.ReviewTemplateSummary{
						{TemplateArn: ptrStr(waARN("review-template", "t1")), Owner: ptrStr(testAccountID)},
					}}, nil
				}
				return &wellarchitected.ListReviewTemplatesOutput{ReviewTemplates: []watypes.ReviewTemplateSummary{
					{TemplateArn: ptrStr(waARN("review-template", "t2")), Owner: ptrStr(waForeignOwner)},
					{Owner: ptrStr(testAccountID)},
				}}, nil
			},
			owned: func(p *waParents) []childParent { return p.ownedTemplates },
			run: func(ctx context.Context, c wellArchitectedAPI, st *store.Store, p *waParents) (int, error) {
				total, _, err := scanWAReviewTemplates(ctx, c, acct, testRegion, st, testScanID, p)
				return total, err
			},
		},
	}
}

func TestScanWAListPhases_StoreEveryPageAndHandOwnedParents(t *testing.T) {
	for _, c := range waListCases() {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			var p waParents
			total, err := c.run(context.Background(), waStubClient(map[string]func(any) (any, error){c.op: c.handler}), st, &p)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			rows := rowsByNativeID(t, st, c.typ)
			_, okOwned := rows[c.ownedARN]
			_, okOther := rows[c.otherARN]
			if total != 2 || len(rows) != 2 || !okOwned || !okOther {
				t.Fatalf("total = %d, rows = %v, want %s and %s", total, rows, c.ownedARN, c.otherARN)
			}
			if got := c.owned(&p); len(got) != 1 || got[0].arn != c.ownedARN {
				t.Errorf("owned parents = %v, want only %s", got, c.ownedARN)
			}
			if c.allParent != nil {
				want := []childParent{{id: "w1", arn: waARN("workload", "w1")}, {id: "w2", arn: waARN("workload", "w2")}}
				if got := c.allParent(&p); !slices.Equal(got, want) {
					t.Errorf("all workloads = %v, want %v", got, want)
				}
			}
		})
	}
}

// A denial on a later page drops the phase's rows, so it must hand no parents
// either: a child under a parent with no stored row has nowhere to hang.
func TestScanWAListPhases_DeniedPageHandsNoParents(t *testing.T) {
	for _, c := range waListCases() {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "wellarchitected:"+c.op)
			first := true
			client := waStubClient(map[string]func(any) (any, error){c.op: func(in any) (any, error) {
				if first {
					first = false
					return c.handler(in)
				}
				return nil, waDenied
			}})
			var p waParents
			total, err := c.run(context.Background(), client, st, &p)
			if err != nil || total != 0 || *warns != 1 {
				t.Fatalf("total = %d, err = %v, warnings = %d; want 0, nil, 1", total, err, *warns)
			}
			if len(p.workloads)+len(p.ownedWorkloads)+len(p.ownedLenses)+len(p.ownedProfiles)+len(p.ownedTemplates) != 0 {
				t.Errorf("parents = %+v, want none", p)
			}
		})
	}
}

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
	}
}

func TestScanWellArchitected_ChildPhasesStoreRowsUnderParents(t *testing.T) {
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
