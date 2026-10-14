package aws

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/securityagent"
	satypes "github.com/aws/aws-sdk-go-v2/service/securityagent/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/store"
)

var (
	saDenied   = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: securityagent:X"}
	saBadInput = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	saGone     = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "gone"}
	saConflict = &smithy.GenericAPIError{Code: "ConflictException", Message: "conflict"}
	saCreated  = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
)

const (
	saSpace1ARN = "arn:aws:securityagent:us-east-1:123456789012:agent-space/s1"
	saSpace2ARN = "arn:aws:securityagent:us-east-1:123456789012:agent-space/s2"
	saPack1ARN  = "arn:aws:securityagent:us-east-1:123456789012:security-requirement-pack/pk1"
	saPack2ARN  = "arn:aws:securityagent:us-east-1:123456789012:security-requirement-pack/pk2"
	// saActorSecret stands in for an AWS_INTERNAL authentication value, whose
	// content is undocumented.
	saActorSecret = "internal-credential-material"
)

var (
	saSpaces = []childParent{{id: "s1", arn: saSpace1ARN}, {id: "s2", arn: saSpace2ARN}}
	saPacks  = []childParent{{id: "pk1", arn: saPack1ARN}, {id: "pk2", arn: saPack2ARN}}
)

// saAssets is the assets block of every full definition in these tests: one
// actor whose authentication value must be redacted.
func saAssets() *satypes.Assets {
	return &satypes.Assets{Actors: []satypes.Actor{{
		Identifier:     ptrStr("tester"),
		Authentication: &satypes.Authentication{ProviderType: satypes.AuthenticationProviderTypeAwsInternal, Value: ptrStr(saActorSecret)},
	}}}
}

// assertSAActorRedacted fails unless r's actor authentication value is the
// redaction placeholder.
func assertSAActorRedacted(t *testing.T, r store.Resource) {
	t.Helper()
	var attrs struct {
		Assets satypes.Assets
	}
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs of %s: %v", r.NativeID, err)
	}
	if len(attrs.Assets.Actors) != 1 || sv(attrs.Assets.Actors[0].Authentication.Value) != redact.Placeholder {
		t.Errorf("%s actor authentication not redacted: %s", r.NativeID, r.AttributesJSON)
	}
}

func saClient(handlers map[string]func(in any) (any, error)) *securityagent.Client {
	return securityagent.NewFromConfig(opStubConfig(handlers, saDenied))
}

// seedSASpaces stores the agent space rows so contains edges have a parent.
func seedSASpaces(t *testing.T, st *store.Store) {
	t.Helper()
	for _, s := range saSpaces {
		upsertTestResource(t, st, "aws", testAccountID, TypeSecurityAgentAgentSpace, s.arn, testRegion, "{}")
	}
}

func saAttrs(t *testing.T, r store.Resource) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &m); err != nil {
		t.Fatalf("attrs of %s: %v", r.NativeID, err)
	}
	return m
}

// batchCalls records the id list of every BatchGet call, keyed by space.
type batchCalls struct {
	mu  sync.Mutex
	ids map[string][][]string
}

func (b *batchCalls) add(space string, ids []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ids == nil {
		b.ids = map[string][][]string{}
	}
	b.ids[space] = append(b.ids[space], ids)
}

// saPentestHandlers lists s1's pentests on two pages ([p1], then [p2 and an
// element with no id]) and s2's on one ([p3]). BatchGetPentests answers each
// listed id with a full pentest titled "full-<id>" carrying a service role and
// a custom header, except p2, which it reports not found. Its answer for s2
// also carries p9, which was never listed. batchErr, when set, is what
// BatchGetPentests returns instead.
func saPentestHandlers(lists *parentCalls, gets *batchCalls, batchErr error) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListPentests": func(in any) (any, error) {
			req := in.(*securityagent.ListPentestsInput)
			space := sv(req.AgentSpaceId)
			lists.add(space)
			switch {
			case space == "s2":
				return &securityagent.ListPentestsOutput{PentestSummaries: []satypes.PentestSummary{
					{PentestId: ptrStr("p3"), Title: ptrStr("sum-p3"), CreatedAt: &saCreated},
				}}, nil
			case req.NextToken == nil:
				return &securityagent.ListPentestsOutput{PentestSummaries: []satypes.PentestSummary{
					{PentestId: ptrStr("p1"), Title: ptrStr("sum-p1"), CreatedAt: &saCreated},
				}, NextToken: ptrStr("page2")}, nil
			default:
				return &securityagent.ListPentestsOutput{PentestSummaries: []satypes.PentestSummary{
					{PentestId: ptrStr("p2"), Title: ptrStr("sum-p2")},
					{Title: ptrStr("no-id")},
				}}, nil
			}
		},
		"BatchGetPentests": func(in any) (any, error) {
			req := in.(*securityagent.BatchGetPentestsInput)
			gets.add(sv(req.AgentSpaceId), req.PentestIds)
			if batchErr != nil {
				return nil, batchErr
			}
			out := &securityagent.BatchGetPentestsOutput{}
			ids := req.PentestIds
			if sv(req.AgentSpaceId) == "s2" {
				ids = append(slices.Clone(ids), "p9")
			}
			for _, id := range ids {
				if id == "p2" {
					out.NotFound = append(out.NotFound, id)
					continue
				}
				out.Pentests = append(out.Pentests, satypes.Pentest{
					PentestId: ptrStr(id), Title: ptrStr("full-" + id), CreatedAt: &saCreated, Assets: saAssets(),
					ServiceRole: ptrStr("arn:aws:iam::123456789012:role/pentest"),
					NetworkTrafficConfig: &satypes.NetworkTrafficConfig{CustomHeaders: []satypes.CustomHeader{
						{Name: ptrStr("Authorization"), Value: ptrStr("Bearer s3cret")},
					}},
				})
			}
			return out, nil
		},
	}
}

// Every listed pentest is stored from its full BatchGet element, under its
// agent space: BatchGet runs once per list page with that page's ids, an id
// it reports not found is dropped, an id it returns that was never listed
// stores nothing, and the custom header and actor credential are redacted.
func TestScanSAPentests_StoresFullDefinitionsUnderSpaces(t *testing.T) {
	st := newTestStore(t)
	seedSASpaces(t, st)
	var lists parentCalls
	var gets batchCalls
	client := saClient(saPentestHandlers(&lists, &gets, nil))

	total, _, err := scanSAPentests(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saSpaces)
	if err != nil {
		t.Fatalf("scanSAPentests: %v", err)
	}
	if got := lists.distinct(); !slices.Equal(got, []string{"s1", "s2"}) {
		t.Errorf("listed spaces %v, want [s1 s2]", got)
	}
	wantGets := map[string][][]string{"s1": {{"p1"}, {"p2"}}, "s2": {{"p3"}}}
	for space, want := range wantGets {
		if got := gets.ids[space]; !slices.EqualFunc(got, want, slices.Equal) {
			t.Errorf("BatchGetPentests ids for %s = %v, want %v", space, got, want)
		}
	}
	rows := rowsByNativeID(t, st, TypeSecurityAgentPentest)
	if total != 2 || len(rows) != 2 {
		t.Fatalf("total = %d, rows = %v; want p1 and p3", total, rows)
	}
	if _, ok := rows[saSpace2ARN+"/pentest/p9"]; ok {
		t.Errorf("unlisted p9 stored")
	}
	p1, ok := rows[saSpace1ARN+"/pentest/p1"]
	if !ok {
		t.Fatalf("p1 missing; rows %v", rows)
	}
	if _, ok := rows[saSpace2ARN+"/pentest/p3"]; !ok {
		t.Errorf("p3 missing; rows %v", rows)
	}
	if sv(p1.Name) != "full-p1" || sv(p1.Region) != testRegion || sv(p1.CreatedAt) != "2026-09-01T12:00:00Z" {
		t.Errorf("p1 name/region/created = %q/%q/%q", sv(p1.Name), sv(p1.Region), sv(p1.CreatedAt))
	}
	attrs := saAttrs(t, p1)
	if attrs["ServiceRole"] != "arn:aws:iam::123456789012:role/pentest" {
		t.Errorf("ServiceRole = %v", attrs["ServiceRole"])
	}
	if strings.Contains(p1.AttributesJSON, "s3cret") || !strings.Contains(p1.AttributesJSON, redact.Placeholder) {
		t.Errorf("custom header value not redacted: %s", p1.AttributesJSON)
	}
	assertSAActorRedacted(t, p1)
	assertContainsNative(t, st, saSpace1ARN, saSpace1ARN+"/pentest/p1")
	assertContainsNative(t, st, saSpace2ARN, saSpace2ARN+"/pentest/p3")
}

// A BatchGet the service refuses still stores every listed pentest, from its
// summary, and warns once across both spaces and all three pages: a role
// without the read action (AccessDenied), a page larger than the undocumented
// batch cap (ValidationException), and a not-found the op does not model.
func TestScanSAPentests_BatchGetFailureKeepsSummaries(t *testing.T) {
	for _, batchErr := range []error{saDenied, saBadInput, saGone} {
		t.Run(batchErr.(*smithy.GenericAPIError).Code, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "securityagent:BatchGetPentests")
			var lists parentCalls
			var gets batchCalls
			client := saClient(saPentestHandlers(&lists, &gets, batchErr))

			total, _, err := scanSAPentests(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saSpaces)
			if err != nil {
				t.Fatalf("scanSAPentests: %v", err)
			}
			if *warns != 1 {
				t.Errorf("BatchGetPentests warnings = %d, want 1", *warns)
			}
			rows := rowsByNativeID(t, st, TypeSecurityAgentPentest)
			if total != 3 || len(rows) != 3 {
				t.Fatalf("total = %d, rows = %d; want p1, p2, p3", total, len(rows))
			}
			if p2 := rows[saSpace1ARN+"/pentest/p2"]; sv(p2.Name) != "sum-p2" {
				t.Errorf("p2 name = %q, want the summary title", sv(p2.Name))
			}
		})
	}
}

func TestScanSAPentests_Errors(t *testing.T) {
	t.Run("batch get other error propagates", func(t *testing.T) {
		st := newTestStore(t)
		var lists parentCalls
		var gets batchCalls
		client := saClient(saPentestHandlers(&lists, &gets, saConflict))
		_, _, err := scanSAPentests(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saSpaces)
		assertAPICode(t, err, "ConflictException")
	})
	t.Run("vanished space warns once and siblings continue", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "securityagent:ListPentests")
		var lists parentCalls
		var gets batchCalls
		h := saPentestHandlers(&lists, &gets, nil)
		list := h["ListPentests"]
		h["ListPentests"] = func(in any) (any, error) {
			if sv(in.(*securityagent.ListPentestsInput).AgentSpaceId) == "s1" {
				return nil, saGone
			}
			return list(in)
		}
		total, _, err := scanSAPentests(context.Background(), saClient(h), newTestAccount(testAccountID), testRegion, st, testScanID, saSpaces)
		if err != nil || *warns != 1 {
			t.Fatalf("err = %v, warnings = %d; want nil, 1", err, *warns)
		}
		rows := rowsByNativeID(t, st, TypeSecurityAgentPentest)
		if _, ok := rows[saSpace2ARN+"/pentest/p3"]; total != 1 || len(rows) != 1 || !ok {
			t.Errorf("total = %d, rows = %v; want only s2's p3", total, rows)
		}
	})
	t.Run("list denied warns once", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "securityagent:ListPentests")
		client := saClient(nil)
		total, _, err := scanSAPentests(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saSpaces)
		if err != nil || total != 0 || *warns != 1 {
			t.Errorf("err = %v, total = %d, warnings = %d; want nil, 0, 1", err, total, *warns)
		}
	})
	t.Run("list error propagates", func(t *testing.T) {
		st := newTestStore(t)
		client := saClient(map[string]func(in any) (any, error){
			"ListPentests": func(any) (any, error) { return nil, saConflict },
		})
		_, _, err := scanSAPentests(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saSpaces)
		assertAPICode(t, err, "ConflictException")
	})
	t.Run("empty space calls no batch get", func(t *testing.T) {
		st := newTestStore(t)
		client := saClient(map[string]func(in any) (any, error){
			"ListPentests": func(any) (any, error) { return &securityagent.ListPentestsOutput{}, nil },
		})
		total, _, err := scanSAPentests(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saSpaces)
		if err != nil || total != 0 {
			t.Errorf("err = %v, total = %d; want nil, 0", err, total)
		}
	})
}

// Code reviews and threat models go through the same engine as pentests;
// this pins each one's list and BatchGet wiring, NativeID kind, parent and
// actor credential redaction.
func TestScanSACodeReviewsAndThreatModels(t *testing.T) {
	role := ptrStr("arn:aws:iam::123456789012:role/agent")
	cases := []struct {
		name     string
		handlers map[string]func(in any) (any, error)
		scan     func(context.Context, securityAgentAPI, *account, string, *store.Store, string, []childParent) (int, int, error)
		typ      string
		native   string
	}{
		{
			name: "code reviews",
			handlers: map[string]func(in any) (any, error){
				"ListCodeReviews": func(in any) (any, error) {
					space := sv(in.(*securityagent.ListCodeReviewsInput).AgentSpaceId)
					return &securityagent.ListCodeReviewsOutput{CodeReviewSummaries: []satypes.CodeReviewSummary{{CodeReviewId: ptrStr("cr-" + space), Title: ptrStr("sum")}}}, nil
				},
				"BatchGetCodeReviews": func(in any) (any, error) {
					id := in.(*securityagent.BatchGetCodeReviewsInput).CodeReviewIds[0]
					return &securityagent.BatchGetCodeReviewsOutput{CodeReviews: []satypes.CodeReview{{CodeReviewId: &id, Title: ptrStr("full"), ServiceRole: role, CreatedAt: &saCreated, Assets: saAssets()}}}, nil
				},
			},
			scan:   scanSACodeReviews,
			typ:    TypeSecurityAgentCodeReview,
			native: saSpace1ARN + "/code-review/cr-s1",
		},
		{
			name: "threat models",
			handlers: map[string]func(in any) (any, error){
				"ListThreatModels": func(in any) (any, error) {
					space := sv(in.(*securityagent.ListThreatModelsInput).AgentSpaceId)
					return &securityagent.ListThreatModelsOutput{ThreatModelSummaries: []satypes.ThreatModelSummary{{ThreatModelId: ptrStr("tm-" + space), Title: ptrStr("sum")}}}, nil
				},
				"BatchGetThreatModels": func(in any) (any, error) {
					id := in.(*securityagent.BatchGetThreatModelsInput).ThreatModelIds[0]
					return &securityagent.BatchGetThreatModelsOutput{ThreatModels: []satypes.ThreatModel{{ThreatModelId: &id, Title: ptrStr("full"), ServiceRole: role, CreatedAt: &saCreated, Assets: saAssets()}}}, nil
				},
			},
			scan:   scanSAThreatModels,
			typ:    TypeSecurityAgentThreatModel,
			native: saSpace1ARN + "/threat-model/tm-s1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			seedSASpaces(t, st)
			total, _, err := tc.scan(context.Background(), saClient(tc.handlers), newTestAccount(testAccountID), testRegion, st, testScanID, saSpaces)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			rows := rowsByNativeID(t, st, tc.typ)
			r, ok := rows[tc.native]
			if total != 2 || len(rows) != 2 || !ok {
				t.Fatalf("total = %d, rows = %v; want 2 including %s", total, rows, tc.native)
			}
			if sv(r.Name) != "full" || sv(r.CreatedAt) != "2026-09-01T12:00:00Z" || saAttrs(t, r)["ServiceRole"] != *role {
				t.Errorf("row not built from the full element: name %q, created %q, attrs %s", sv(r.Name), sv(r.CreatedAt), r.AttributesJSON)
			}
			assertSAActorRedacted(t, r)
			assertContainsNative(t, st, saSpace1ARN, tc.native)
		})
	}
}

// saMembershipHandler lists, for (s1, a1), two pages ([m1], then [m2 and an
// element with no id]); every other (space, application) pair holds one
// membership "m-<space>-<app>". fail maps a "space/app" pair, or a whole
// space, to the error it returns.
// Every call's "space/app" pair is recorded.
func saMembershipHandler(calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	user := func(id, name string) satypes.MembershipSummary {
		return satypes.MembershipSummary{
			MembershipId: ptrStr(id), MemberType: satypes.MembershipTypeUser, CreatedAt: &saCreated,
			Config:   &satypes.MembershipConfigMemberUser{Value: satypes.UserConfig{Role: satypes.UserRoleMember}},
			Metadata: &satypes.MemberMetadataMemberUser{Value: satypes.UserMetadata{Username: ptrStr(name), Email: ptrStr(name + "@example.com")}},
		}
	}
	return func(in any) (any, error) {
		req := in.(*securityagent.ListMembershipsInput)
		space, app := sv(req.AgentSpaceId), sv(req.ApplicationId)
		calls.add(space + "/" + app)
		if err := cmp.Or(fail[space+"/"+app], fail[space]); err != nil {
			return nil, err
		}
		switch {
		case space != "s1" || app != "a1":
			return &securityagent.ListMembershipsOutput{MembershipSummaries: []satypes.MembershipSummary{user("m-"+space+"-"+app, "u")}}, nil
		case req.NextToken == nil:
			return &securityagent.ListMembershipsOutput{MembershipSummaries: []satypes.MembershipSummary{user("m1", "alice")}, NextToken: ptrStr("page2")}, nil
		default:
			return &securityagent.ListMembershipsOutput{MembershipSummaries: []satypes.MembershipSummary{user("m2", "bob"), {MemberType: satypes.MembershipTypeUser}}}, nil
		}
	}
}

func TestScanSAMemberships_StoresEveryApplicationsMembersUnderSpaces(t *testing.T) {
	st := newTestStore(t)
	seedSASpaces(t, st)
	var calls parentCalls
	client := saClient(map[string]func(in any) (any, error){"ListMemberships": saMembershipHandler(&calls, nil)})

	total, _, err := scanSAMemberships(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saSpaces, []string{"a1", "a2"})
	if err != nil {
		t.Fatalf("scanSAMemberships: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"s1/a1", "s1/a2", "s2/a1", "s2/a2"}) {
		t.Errorf("listed pairs %v, want every space × application", got)
	}
	rows := rowsByNativeID(t, st, TypeSecurityAgentMembership)
	// s1/a1: m1, m2; one each for s1/a2, s2/a1, s2/a2.
	if total != 5 || len(rows) != 5 {
		t.Fatalf("total = %d, rows = %d; want 5", total, len(rows))
	}
	m1, ok := rows[saSpace1ARN+"/application/a1/membership/m1"]
	if !ok {
		t.Fatalf("m1 missing; rows %v", rows)
	}
	if sv(m1.Name) != "alice" || sv(m1.Region) != testRegion || sv(m1.CreatedAt) != "2026-09-01T12:00:00Z" {
		t.Errorf("m1 name/region/created = %q/%q/%q", sv(m1.Name), sv(m1.Region), sv(m1.CreatedAt))
	}
	attrs := saAttrs(t, m1)
	cfg, _ := attrs["Config"].(map[string]any)
	user, _ := cfg["User"].(map[string]any)
	if user["Role"] != "MEMBER" {
		t.Errorf("Config = %v, want User.Role MEMBER", attrs["Config"])
	}
	assertContainsNative(t, st, saSpace1ARN, saSpace1ARN+"/application/a1/membership/m1")
	assertContainsNative(t, st, saSpace2ARN, saSpace2ARN+"/application/a2/membership/m-s2-a2")
}

func TestScanSAMemberships_Errors(t *testing.T) {
	t.Run("denied warns once", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "securityagent:ListMemberships")
		var calls parentCalls
		client := saClient(map[string]func(in any) (any, error){"ListMemberships": saMembershipHandler(&calls, map[string]error{"s1": saDenied, "s2": saDenied})})
		total, _, err := scanSAMemberships(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saSpaces, []string{"a1"})
		if err != nil || total != 0 || *warns != 1 {
			t.Errorf("err = %v, total = %d, warnings = %d; want nil, 0, 1", err, total, *warns)
		}
	})
	// A failed pair drops only itself: s1's other application still lands.
	for _, pairErr := range []error{saDenied, saGone} {
		t.Run("pair "+pairErr.(*smithy.GenericAPIError).Code+" warns once", func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "securityagent:ListMemberships")
			var calls parentCalls
			client := saClient(map[string]func(in any) (any, error){"ListMemberships": saMembershipHandler(&calls, map[string]error{"s1/a2": pairErr, "s2": pairErr})})
			total, _, err := scanSAMemberships(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saSpaces, []string{"a1", "a2"})
			if err != nil || *warns != 1 {
				t.Fatalf("err = %v, warnings = %d; want nil, 1", err, *warns)
			}
			rows := rowsByNativeID(t, st, TypeSecurityAgentMembership)
			if _, ok := rows[saSpace1ARN+"/application/a1/membership/m2"]; total != 2 || len(rows) != 2 || !ok {
				t.Errorf("total = %d, rows = %v; want s1/a1's m1 and m2", total, rows)
			}
		})
	}
	t.Run("other error propagates", func(t *testing.T) {
		st := newTestStore(t)
		var calls parentCalls
		client := saClient(map[string]func(in any) (any, error){"ListMemberships": saMembershipHandler(&calls, map[string]error{"s2": saConflict})})
		_, _, err := scanSAMemberships(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saSpaces, []string{"a1"})
		assertAPICode(t, err, "ConflictException")
	})
	t.Run("no applications lists nothing", func(t *testing.T) {
		st := newTestStore(t)
		var calls parentCalls
		client := saClient(map[string]func(in any) (any, error){"ListMemberships": saMembershipHandler(&calls, nil)})
		total, _, err := scanSAMemberships(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saSpaces, nil)
		if err != nil || total != 0 || len(calls.distinct()) != 0 {
			t.Errorf("err = %v, total = %d, calls = %v; want nil, 0, none", err, total, calls.distinct())
		}
	})
}

// saIntegratedResourceHandler lists s1's resources on two pages (a GitHub
// repository; then a Confluence page, an unknown union member and a resource
// with no integration id) and s2's on one (a GitLab repository). fail maps a
// space to the error it returns.
func saIntegratedResourceHandler(calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*securityagent.ListIntegratedResourcesInput)
		space := sv(req.AgentSpaceId)
		calls.add(space)
		if err := fail[space]; err != nil {
			return nil, err
		}
		switch {
		case space == "s2":
			return &securityagent.ListIntegratedResourcesOutput{IntegratedResourceSummaries: []satypes.IntegratedResourceSummary{{
				IntegrationId: ptrStr("i2"),
				Resource:      &satypes.IntegratedResourceMetadataMemberGitlabRepository{Value: satypes.GitLabRepositoryMetadata{Name: ptrStr("lab"), Namespace: ptrStr("grp"), ProviderResourceId: ptrStr("77")}},
			}}}, nil
		case req.NextToken == nil:
			return &securityagent.ListIntegratedResourcesOutput{IntegratedResourceSummaries: []satypes.IntegratedResourceSummary{{
				IntegrationId: ptrStr("i1"),
				Resource:      &satypes.IntegratedResourceMetadataMemberGithubRepository{Value: satypes.GitHubRepositoryMetadata{Name: ptrStr("repo"), Owner: ptrStr("org"), ProviderResourceId: ptrStr("42")}},
				Capabilities:  &satypes.ProviderResourceCapabilitiesMemberGithub{Value: satypes.GitHubResourceCapabilities{LeaveComments: sdkaws.Bool(true)}},
			}}, NextToken: ptrStr("page2")}, nil
		default:
			return &securityagent.ListIntegratedResourcesOutput{IntegratedResourceSummaries: []satypes.IntegratedResourceSummary{
				{IntegrationId: ptrStr("i1"), Resource: &satypes.IntegratedResourceMetadataMemberConfluenceDocument{Value: satypes.ConfluenceDocumentMetadata{Name: ptrStr("doc"), PageId: ptrStr("9"), SpaceKey: ptrStr("SP"), ProviderResourceId: ptrStr("c9")}}},
				{IntegrationId: ptrStr("i1"), Resource: &satypes.UnknownUnionMember{Tag: "newKind", Value: []byte("{}")}},
				{Resource: &satypes.IntegratedResourceMetadataMemberGithubRepository{Value: satypes.GitHubRepositoryMetadata{ProviderResourceId: ptrStr("43")}}},
			}}, nil
		}
	}
}

func TestScanSAIntegratedResources_StoresKnownResourcesUnderSpaces(t *testing.T) {
	st := newTestStore(t)
	seedSASpaces(t, st)
	var calls parentCalls
	client := saClient(map[string]func(in any) (any, error){"ListIntegratedResources": saIntegratedResourceHandler(&calls, nil)})

	total, _, err := scanSAIntegratedResources(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saSpaces)
	if err != nil {
		t.Fatalf("scanSAIntegratedResources: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeSecurityAgentIntegratedResource)
	gh := saSpace1ARN + "/integrated-resource/i1/42"
	want := []string{gh, saSpace1ARN + "/integrated-resource/i1/c9", saSpace2ARN + "/integrated-resource/i2/77"}
	if total != 3 || len(rows) != 3 {
		t.Fatalf("total = %d, rows = %v; want %v", total, rows, want)
	}
	for _, id := range want {
		if _, ok := rows[id]; !ok {
			t.Errorf("%s missing", id)
		}
	}
	r := rows[gh]
	if sv(r.Name) != "repo" || sv(r.Region) != testRegion {
		t.Errorf("github row name/region = %q/%q", sv(r.Name), sv(r.Region))
	}
	attrs := saAttrs(t, r)
	res, _ := attrs["Resource"].(map[string]any)
	repo, _ := res["GithubRepository"].(map[string]any)
	caps, _ := attrs["Capabilities"].(map[string]any)
	github, _ := caps["Github"].(map[string]any)
	if repo["Owner"] != "org" || github["LeaveComments"] != true {
		t.Errorf("unions not relabelled: %s", r.AttributesJSON)
	}
	assertContainsNative(t, st, saSpace1ARN, gh)
	assertContainsNative(t, st, saSpace2ARN, saSpace2ARN+"/integrated-resource/i2/77")
}

func TestScanSAIntegratedResources_Errors(t *testing.T) {
	t.Run("space gone is skipped silently", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "securityagent:ListIntegratedResources")
		var calls parentCalls
		client := saClient(map[string]func(in any) (any, error){"ListIntegratedResources": saIntegratedResourceHandler(&calls, map[string]error{"s1": saGone})})
		total, _, err := scanSAIntegratedResources(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saSpaces)
		if err != nil || total != 1 || *warns != 0 {
			t.Errorf("err = %v, total = %d, warnings = %d; want nil, s2's 1, 0", err, total, *warns)
		}
	})
	t.Run("denied warns once", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "securityagent:ListIntegratedResources")
		client := saClient(nil)
		total, _, err := scanSAIntegratedResources(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saSpaces)
		if err != nil || total != 0 || *warns != 1 {
			t.Errorf("err = %v, total = %d, warnings = %d; want nil, 0, 1", err, total, *warns)
		}
	})
	t.Run("other error propagates", func(t *testing.T) {
		st := newTestStore(t)
		var calls parentCalls
		client := saClient(map[string]func(in any) (any, error){"ListIntegratedResources": saIntegratedResourceHandler(&calls, map[string]error{"s2": saBadInput})})
		_, _, err := scanSAIntegratedResources(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saSpaces)
		assertAPICode(t, err, "ValidationException")
	})
}

// saTopLevelHandlers answers every op the service scan makes: agent spaces s1
// and s2 on two pages, application a1, one row for each region-wide phase,
// and the per-space handlers above.
func saTopLevelHandlers() map[string]func(in any) (any, error) {
	var lists, members, resources parentCalls
	var gets batchCalls
	h := saPentestHandlers(&lists, &gets, nil)
	h["ListAgentSpaces"] = func(in any) (any, error) {
		if in.(*securityagent.ListAgentSpacesInput).NextToken == nil {
			return &securityagent.ListAgentSpacesOutput{AgentSpaceSummaries: []satypes.AgentSpaceSummary{{AgentSpaceId: ptrStr("s1"), Name: ptrStr("one")}}, NextToken: ptrStr("p2")}, nil
		}
		return &securityagent.ListAgentSpacesOutput{AgentSpaceSummaries: []satypes.AgentSpaceSummary{{AgentSpaceId: ptrStr("s2"), Name: ptrStr("two")}}}, nil
	}
	h["ListApplications"] = func(any) (any, error) {
		return &securityagent.ListApplicationsOutput{ApplicationSummaries: []satypes.ApplicationSummary{{ApplicationId: ptrStr("a1")}}}, nil
	}
	h["ListTargetDomains"] = func(any) (any, error) {
		return &securityagent.ListTargetDomainsOutput{TargetDomainSummaries: []satypes.TargetDomainSummary{{TargetDomainId: ptrStr("td1")}}}, nil
	}
	h["ListIntegrations"] = func(any) (any, error) {
		return &securityagent.ListIntegrationsOutput{IntegrationSummaries: []satypes.IntegrationSummary{{IntegrationId: ptrStr("i1")}}}, nil
	}
	h["ListPrivateConnections"] = func(any) (any, error) {
		return &securityagent.ListPrivateConnectionsOutput{PrivateConnections: []satypes.PrivateConnectionSummary{{Name: ptrStr("pc1")}}}, nil
	}
	h["ListSecurityRequirementPacks"] = func(any) (any, error) {
		return &securityagent.ListSecurityRequirementPacksOutput{SecurityRequirementPackSummaries: []satypes.SecurityRequirementPackSummary{{PackId: ptrStr("pk1"), ManagementType: satypes.ManagementTypeCustomerManaged}}}, nil
	}
	h["ListSecurityRequirements"] = func(any) (any, error) {
		return &securityagent.ListSecurityRequirementsOutput{SecurityRequirementSummaries: []satypes.SecurityRequirementSummary{{Name: ptrStr("req1")}}}, nil
	}
	h["BatchGetSecurityRequirements"] = func(any) (any, error) {
		return &securityagent.BatchGetSecurityRequirementsOutput{SecurityRequirements: []satypes.BatchGetSecurityRequirementResult{{Name: ptrStr("req1")}}}, nil
	}
	h["ListCodeReviews"] = func(in any) (any, error) {
		space := sv(in.(*securityagent.ListCodeReviewsInput).AgentSpaceId)
		return &securityagent.ListCodeReviewsOutput{CodeReviewSummaries: []satypes.CodeReviewSummary{{CodeReviewId: ptrStr("cr-" + space)}}}, nil
	}
	h["BatchGetCodeReviews"] = func(in any) (any, error) {
		id := in.(*securityagent.BatchGetCodeReviewsInput).CodeReviewIds[0]
		return &securityagent.BatchGetCodeReviewsOutput{CodeReviews: []satypes.CodeReview{{CodeReviewId: &id}}}, nil
	}
	h["ListThreatModels"] = func(in any) (any, error) {
		space := sv(in.(*securityagent.ListThreatModelsInput).AgentSpaceId)
		return &securityagent.ListThreatModelsOutput{ThreatModelSummaries: []satypes.ThreatModelSummary{{ThreatModelId: ptrStr("tm-" + space)}}}, nil
	}
	h["BatchGetThreatModels"] = func(in any) (any, error) {
		id := in.(*securityagent.BatchGetThreatModelsInput).ThreatModelIds[0]
		return &securityagent.BatchGetThreatModelsOutput{ThreatModels: []satypes.ThreatModel{{ThreatModelId: &id}}}, nil
	}
	h["ListMemberships"] = saMembershipHandler(&members, nil)
	h["ListIntegratedResources"] = saIntegratedResourceHandler(&resources, nil)
	return h
}

// The service scan hands the agent spaces and applications it listed to every
// per-space phase, and the customer-managed packs to the requirement phase.
func TestScanSecurityAgent_StoresSpaceChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(saTopLevelHandlers(), saDenied)

	total, _, err := scanSecurityAgent(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanSecurityAgent: %v", err)
	}
	// 2 spaces, 1 app, 4 region-wide rows, 2 pentests, 2 code reviews,
	// 2 threat models, 3 memberships (s1/a1: 2, s2/a1: 1), 3 integrated
	// resources, 1 requirement of pack pk1.
	if total != 20 {
		t.Errorf("total = %d, want 20", total)
	}
	assertContainsNative(t, st, saPack1ARN, saPack1ARN+"/security-requirement/req1")
	for _, c := range []struct{ typ, native string }{
		{TypeSecurityAgentPentest, saSpace2ARN + "/pentest/p3"},
		{TypeSecurityAgentCodeReview, saSpace2ARN + "/code-review/cr-s2"},
		{TypeSecurityAgentThreatModel, saSpace2ARN + "/threat-model/tm-s2"},
		{TypeSecurityAgentMembership, saSpace2ARN + "/application/a1/membership/m-s2-a1"},
		{TypeSecurityAgentIntegratedResource, saSpace2ARN + "/integrated-resource/i2/77"},
	} {
		assertContainsNative(t, st, saSpace2ARN, c.native)
		if _, ok := rowsByNativeID(t, st, c.typ)[c.native]; !ok {
			t.Errorf("%s %s missing", c.typ, c.native)
		}
	}
}

// Phases are independent: a hard error in an existing phase does not stop the
// new per-space phases, a hard error in every new phase does not stop the
// existing ones, and all errors reach the caller.
func TestScanSecurityAgent_PhaseErrorsAreIndependent(t *testing.T) {
	newOps := []string{"ListCodeReviews", "ListIntegratedResources", "ListMemberships", "ListSecurityRequirements", "ListThreatModels"} // sorted, as parentCalls.distinct returns
	t.Run("new phases fail", func(t *testing.T) {
		st := newTestStore(t)
		acct := newTestAccount(testAccountID)
		h := saTopLevelHandlers()
		var failed parentCalls
		for _, op := range newOps {
			h[op] = func(any) (any, error) {
				failed.add(op)
				return nil, saBadInput
			}
		}
		acct.cfg = opStubConfig(h, saDenied)
		_, _, err := scanSecurityAgent(context.Background(), acct, testRegion, st, testScanID)
		assertAPICode(t, err, "ValidationException")
		if got := failed.distinct(); !slices.Equal(got, newOps) {
			t.Errorf("failing new ops called %v, want all of %v", got, newOps)
		}
		for _, typ := range []string{TypeSecurityAgentAgentSpace, TypeSecurityAgentApplication, TypeSecurityAgentPentest, TypeSecurityAgentTargetDomain, TypeSecurityAgentIntegration, TypeSecurityAgentPrivateConnection, TypeSecurityAgentSecurityRequirementPack} {
			if len(rowsByNativeID(t, st, typ)) == 0 {
				t.Errorf("no %s rows after new phases failed", typ)
			}
		}
	})
	t.Run("existing phase fails", func(t *testing.T) {
		st := newTestStore(t)
		acct := newTestAccount(testAccountID)
		h := saTopLevelHandlers()
		h["ListTargetDomains"] = func(any) (any, error) { return nil, saBadInput }
		acct.cfg = opStubConfig(h, saDenied)
		_, _, err := scanSecurityAgent(context.Background(), acct, testRegion, st, testScanID)
		assertAPICode(t, err, "ValidationException")
		for _, typ := range []string{TypeSecurityAgentCodeReview, TypeSecurityAgentThreatModel, TypeSecurityAgentMembership, TypeSecurityAgentIntegratedResource, TypeSecurityAgentSecurityRequirementPack, TypeSecurityAgentSecurityRequirement} {
			if len(rowsByNativeID(t, st, typ)) == 0 {
				t.Errorf("no %s rows after ListTargetDomains failed", typ)
			}
		}
	})
}

// Only customer-managed packs are handed to the requirement phase; an
// AWS-managed pack is stored as provider-managed and its requirements are not
// listed.
func TestScanSASecurityRequirementPacks_ReturnsCustomerManaged(t *testing.T) {
	st := newTestStore(t)
	client := saClient(map[string]func(in any) (any, error){
		"ListSecurityRequirementPacks": func(any) (any, error) {
			return &securityagent.ListSecurityRequirementPacksOutput{SecurityRequirementPackSummaries: []satypes.SecurityRequirementPackSummary{
				{PackId: ptrStr("pk1"), ManagementType: satypes.ManagementTypeCustomerManaged},
				{PackId: ptrStr("aws1"), ManagementType: satypes.ManagementTypeAwsManaged},
				{PackId: ptrStr("pk2"), ManagementType: satypes.ManagementTypeCustomerManaged},
			}}, nil
		},
	})
	packs, total, _, err := scanSASecurityRequirementPacks(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanSASecurityRequirementPacks: %v", err)
	}
	if !slices.Equal(packs, saPacks) || total != 3 {
		t.Errorf("packs = %v, total = %d; want %v, 3", packs, total, saPacks)
	}
	if r := rowsByNativeID(t, st, TypeSecurityAgentSecurityRequirementPack)["arn:aws:securityagent:us-east-1:123456789012:security-requirement-pack/aws1"]; !r.ManagedByProvider {
		t.Errorf("AWS-managed pack not flagged provider-managed")
	}
}

// saRequirementHandlers lists pk1's requirements on two pages ([r1], then [r2
// and an element with no name]) and pk2's on one ([r3]).
// BatchGetSecurityRequirements answers each name with a full requirement
// carrying its evaluation criteria, except r2, which it reports as a per-item
// error. fail maps a pack to the error its list call returns; batchErr, when
// set, is what BatchGetSecurityRequirements returns instead.
func saRequirementHandlers(fail map[string]error, batchErr error) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListSecurityRequirements": func(in any) (any, error) {
			req := in.(*securityagent.ListSecurityRequirementsInput)
			pack := sv(req.PackId)
			if err := fail[pack]; err != nil {
				return nil, err
			}
			switch {
			case pack == "pk2":
				return &securityagent.ListSecurityRequirementsOutput{SecurityRequirementSummaries: []satypes.SecurityRequirementSummary{{Name: ptrStr("r3")}}}, nil
			case req.NextToken == nil:
				return &securityagent.ListSecurityRequirementsOutput{SecurityRequirementSummaries: []satypes.SecurityRequirementSummary{{Name: ptrStr("r1"), CreatedAt: &saCreated}}, NextToken: ptrStr("page2")}, nil
			default:
				return &securityagent.ListSecurityRequirementsOutput{SecurityRequirementSummaries: []satypes.SecurityRequirementSummary{{Name: ptrStr("r2")}, {Description: ptrStr("no-name")}}}, nil
			}
		},
		"BatchGetSecurityRequirements": func(in any) (any, error) {
			if batchErr != nil {
				return nil, batchErr
			}
			out := &securityagent.BatchGetSecurityRequirementsOutput{}
			for _, name := range in.(*securityagent.BatchGetSecurityRequirementsInput).SecurityRequirementNames {
				if name == "r2" {
					out.Errors = append(out.Errors, satypes.BatchSecurityRequirementError{SecurityRequirementName: ptrStr(name), Code: ptrStr("InternalError"), Message: ptrStr("x")})
					continue
				}
				out.SecurityRequirements = append(out.SecurityRequirements, satypes.BatchGetSecurityRequirementResult{Name: ptrStr(name), Evaluation: ptrStr("check " + name), CreatedAt: &saCreated})
			}
			return out, nil
		},
	}
}

func seedSAPacks(t *testing.T, st *store.Store) {
	t.Helper()
	for _, p := range saPacks {
		upsertTestResource(t, st, "aws", testAccountID, TypeSecurityAgentSecurityRequirementPack, p.arn, testRegion, "{}")
	}
}

// Every listed requirement lands under its pack, from the full element where
// BatchGet returns one; a per-item BatchGet error warns once and keeps that
// requirement's summary.
func TestScanSASecurityRequirements_StoresUnderPacks(t *testing.T) {
	st := newTestStore(t)
	seedSAPacks(t, st)
	warns := countOpWarnings(st, "securityagent:BatchGetSecurityRequirements")
	client := saClient(saRequirementHandlers(nil, nil))

	total, _, err := scanSASecurityRequirements(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saPacks)
	if err != nil {
		t.Fatalf("scanSASecurityRequirements: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeSecurityAgentSecurityRequirement)
	if total != 3 || len(rows) != 3 || *warns != 1 {
		t.Fatalf("total = %d, rows = %v, warnings = %d; want r1, r2, r3 and 1 warning", total, rows, *warns)
	}
	r1 := rows[saPack1ARN+"/security-requirement/r1"]
	if sv(r1.Name) != "r1" || sv(r1.Region) != testRegion || sv(r1.CreatedAt) != "2026-09-01T12:00:00Z" || saAttrs(t, r1)["Evaluation"] != "check r1" {
		t.Errorf("r1 not built from the full element: name %q, created %q, attrs %s", sv(r1.Name), sv(r1.CreatedAt), r1.AttributesJSON)
	}
	r2, ok := rows[saPack1ARN+"/security-requirement/r2"]
	if _, full := saAttrs(t, r2)["Evaluation"]; !ok || full {
		t.Errorf("r2 = %v; want its listed summary", r2)
	}
	assertContainsNative(t, st, saPack1ARN, saPack1ARN+"/security-requirement/r1")
	assertContainsNative(t, st, saPack2ARN, saPack2ARN+"/security-requirement/r3")
}

func TestScanSASecurityRequirements_Errors(t *testing.T) {
	// Both ops model ResourceNotFoundException: a pack deleted after listing
	// is skipped silently and the other pack continues.
	t.Run("pack gone on list is skipped silently", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "securityagent:ListSecurityRequirements")
		client := saClient(saRequirementHandlers(map[string]error{"pk1": saGone}, nil))
		total, _, err := scanSASecurityRequirements(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saPacks)
		if err != nil || total != 1 || *warns != 0 {
			t.Errorf("err = %v, total = %d, warnings = %d; want nil, pk2's 1, 0", err, total, *warns)
		}
	})
	t.Run("pack gone on batch get is skipped silently", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "securityagent:BatchGetSecurityRequirements")
		client := saClient(saRequirementHandlers(nil, saGone))
		total, _, err := scanSASecurityRequirements(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saPacks)
		if err != nil || total != 0 || *warns != 0 {
			t.Errorf("err = %v, total = %d, warnings = %d; want nil, 0, 0", err, total, *warns)
		}
	})
	t.Run("batch get rejected keeps summaries", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "securityagent:BatchGetSecurityRequirements")
		client := saClient(saRequirementHandlers(nil, saBadInput))
		total, _, err := scanSASecurityRequirements(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saPacks)
		if err != nil || total != 3 || *warns != 1 {
			t.Errorf("err = %v, total = %d, warnings = %d; want nil, 3, 1", err, total, *warns)
		}
	})
	t.Run("list denied warns once", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "securityagent:ListSecurityRequirements")
		total, _, err := scanSASecurityRequirements(context.Background(), saClient(nil), newTestAccount(testAccountID), testRegion, st, testScanID, saPacks)
		if err != nil || total != 0 || *warns != 1 {
			t.Errorf("err = %v, total = %d, warnings = %d; want nil, 0, 1", err, total, *warns)
		}
	})
	t.Run("other error propagates", func(t *testing.T) {
		st := newTestStore(t)
		client := saClient(saRequirementHandlers(map[string]error{"pk2": saConflict}, nil))
		_, _, err := scanSASecurityRequirements(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saPacks)
		assertAPICode(t, err, "ConflictException")
	})
}
