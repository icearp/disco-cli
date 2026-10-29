package aws

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net"
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
)

var (
	saSpaces = []childParent{{id: "s1", arn: saSpace1ARN}, {id: "s2", arn: saSpace2ARN}}
	saPacks  = []childParent{{id: "pk1", arn: saPack1ARN}, {id: "pk2", arn: saPack2ARN}}
)

// saActorCases are the actors of every full definition in these tests and
// the authentication value the store must hold for each. The value is
// treated as a credential whatever its provider type, so only references
// and booleans are kept; a plain number or word is replaced too.
var saActorCases = []struct {
	provider satypes.AuthenticationProviderType
	value    string
	want     string
}{
	{satypes.AuthenticationProviderTypeAwsInternal, "internal-credential-material", redact.PlaceholderKeyName},
	{satypes.AuthenticationProviderTypeAwsInternal, "hunter2", redact.PlaceholderKeyName},
	{satypes.AuthenticationProviderTypeAwsInternal, "4821", redact.PlaceholderKeyName},
	{satypes.AuthenticationProviderTypeAwsInternal, "AKIAIOSFODNN7EXAMPLE", redact.PlaceholderValueShape},
	{satypes.AuthenticationProviderTypeAwsInternal, "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", redact.PlaceholderValueShape},
	{satypes.AuthenticationProviderTypeAwsInternal, "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----", redact.PlaceholderValueShape},
	{satypes.AuthenticationProviderTypeAwsInternal, "https://tester:pw-actor@app.example.com/login", redact.PlaceholderValueShape},
	{satypes.AuthenticationProviderTypeAwsInternal, "q2Wx8zPl0vB7nR4tY6uK3mHs", redact.PlaceholderKeyName},
	{satypes.AuthenticationProviderTypeSecretsManager, "arn:aws:secretsmanager:us-east-1:123456789012:secret:tester-AbCdEf", "arn:aws:secretsmanager:us-east-1:123456789012:secret:tester-AbCdEf"},
	{satypes.AuthenticationProviderTypeAwsLambda, "arn:aws:lambda:us-east-1:123456789012:function:login", "arn:aws:lambda:us-east-1:123456789012:function:login"},
	{satypes.AuthenticationProviderTypeAwsIamRole, "arn:aws:iam::123456789012:role/tester", "arn:aws:iam::123456789012:role/tester"},
	{satypes.AuthenticationProviderTypeAwsInternal, "{{resolve:ssm-secure:/pentest/tester:1}}", "{{resolve:ssm-secure:/pentest/tester:1}}"},
	{satypes.AuthenticationProviderTypeAwsInternal, "true", "true"},
	{satypes.AuthenticationProviderTypeAwsInternal, "i-0123456789abcdef0", redact.PlaceholderKeyName},
}

// saAssets is the assets block of every full definition in these tests: one
// actor per saActorCases entry.
func saAssets() *satypes.Assets {
	actors := make([]satypes.Actor, 0, len(saActorCases))
	for _, c := range saActorCases {
		actors = append(actors, satypes.Actor{
			Identifier:     ptrStr("tester"),
			Authentication: &satypes.Authentication{ProviderType: c.provider, Value: ptrStr(c.value)},
		})
	}
	return &satypes.Assets{Actors: actors}
}

// assertSAActorRedacted fails unless every actor of r holds its case's
// authentication value and no replaced value appears anywhere in r.
func assertSAActorRedacted(t *testing.T, r store.Resource) {
	t.Helper()
	var attrs struct {
		Assets satypes.Assets
	}
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs of %s: %v", r.NativeID, err)
	}
	if len(attrs.Assets.Actors) != len(saActorCases) {
		t.Fatalf("%s stored %d actors, want %d", r.NativeID, len(attrs.Assets.Actors), len(saActorCases))
	}
	for i, c := range saActorCases {
		got := attrs.Assets.Actors[i].Authentication
		if got == nil || got.ProviderType != c.provider || sv(got.Value) != c.want {
			t.Errorf("%s actor %d (%s %q) authentication = %s, want value %q", r.NativeID, i, c.provider, c.value, mustJSON(got), c.want)
		}
		if c.want != c.value && strings.Contains(r.AttributesJSON, jsonText(t, c.value)) {
			t.Errorf("%s stored attributes still contain %q", r.NativeID, c.value)
		}
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
	h["ListTagsForResource"] = func(any) (any, error) { return &securityagent.ListTagsForResourceOutput{}, nil }
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
		"ListTagsForResource": func(any) (any, error) { return &securityagent.ListTagsForResourceOutput{}, nil },
	})
	var p phaseTotals
	packs, failed := scanSASecurityRequirementPacks(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, saTags(client), &p)
	if failed || len(p.errs) != 0 {
		t.Fatalf("scanSASecurityRequirementPacks failed = %v, errs = %v", failed, p.errs)
	}
	if !slices.Equal(packs, saPacks) || p.total != 3 {
		t.Errorf("packs = %v, total = %d; want %v, 3", packs, p.total, saPacks)
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

// The tag fixture: each region-wide listing serves a "t" element on its first
// page and a "b" element on its second; the pack listing's second page also
// holds the AWS-managed pack aws-pk. Every "t" resource has tags: the private
// connection's come on its listed element, every other type's from
// ListTagsForResource. The per-parent listings find nothing.
const (
	saTagSpaceT  = "arn:aws:securityagent:us-east-1:123456789012:agent-space/s-t"
	saTagSpaceB  = "arn:aws:securityagent:us-east-1:123456789012:agent-space/s-b"
	saTagAppT    = "arn:aws:securityagent:us-east-1:123456789012:application/a-t"
	saTagAppB    = "arn:aws:securityagent:us-east-1:123456789012:application/a-b"
	saTagDomainT = "arn:aws:securityagent:us-east-1:123456789012:target-domain/td-t"
	saTagDomainB = "arn:aws:securityagent:us-east-1:123456789012:target-domain/td-b"
	saTagIntegT  = "arn:aws:securityagent:us-east-1:123456789012:integration/i-t"
	saTagIntegB  = "arn:aws:securityagent:us-east-1:123456789012:integration/i-b"
	saTagConnT   = "arn:aws:securityagent:us-east-1:123456789012:private-connection/pc-t"
	saTagConnB   = "arn:aws:securityagent:us-east-1:123456789012:private-connection/pc-b"
	saTagPackT   = "arn:aws:securityagent:us-east-1:123456789012:security-requirement-pack/pk-t"
	saTagPackB   = "arn:aws:securityagent:us-east-1:123456789012:security-requirement-pack/pk-b"
	saTagPackAWS = "arn:aws:securityagent:us-east-1:123456789012:security-requirement-pack/aws-pk"
	// saTagRows is every row the fixture stores: 2 of each of the six
	// region-wide types plus the AWS-managed pack.
	saTagRows = 13
)

var (
	saThrottled = apiErr("ThrottlingException", "slow down")
	// saTagReadARNs is every ARN the fixture sends ListTagsForResource: not
	// the private connections, whose element carries their tags, nor the
	// AWS-managed pack.
	saTagReadARNs = []string{saTagSpaceT, saTagSpaceB, saTagAppT, saTagAppB, saTagDomainT, saTagDomainB, saTagIntegT, saTagIntegB, saTagPackT, saTagPackB}
)

func saTagSpace(n string) satypes.AgentSpaceSummary {
	s := satypes.AgentSpaceSummary{AgentSpaceId: ptrStr("s-" + n), Name: ptrStr("space " + n)}
	if n == "t" {
		s.CreatedAt = &saCreated
	}
	return s
}

func saTagApp(n string) satypes.ApplicationSummary {
	return satypes.ApplicationSummary{ApplicationId: ptrStr("a-" + n), ApplicationName: ptrStr("app " + n), Domain: ptrStr(n + ".app.example")}
}

func saTagDomain(n string) satypes.TargetDomainSummary {
	d := satypes.TargetDomainSummary{TargetDomainId: ptrStr("td-" + n), DomainName: ptrStr(n + ".example.com")}
	if n == "t" {
		d.VerificationStatus = satypes.TargetDomainStatusVerified
	}
	return d
}

func saTagIntegration(n string) satypes.IntegrationSummary {
	return satypes.IntegrationSummary{IntegrationId: ptrStr("i-" + n), DisplayName: ptrStr("integration " + n), Provider: satypes.ProviderGithub}
}

func saTagConnection(n string) satypes.PrivateConnectionSummary {
	c := satypes.PrivateConnectionSummary{Name: ptrStr("pc-" + n), Type: satypes.PrivateConnectionTypeServiceManaged}
	if n == "t" {
		c.Status = satypes.PrivateConnectionStatusActive
		c.Tags = map[string]string{"type": TypeSecurityAgentPrivateConnection}
	}
	return c
}

func saTagPack(id string, mt satypes.ManagementType) satypes.SecurityRequirementPackSummary {
	p := satypes.SecurityRequirementPackSummary{PackId: ptrStr(id), Name: ptrStr("pack " + id), ManagementType: mt}
	if id != "pk-b" {
		p.Status = satypes.SecurityRequirementPackStatusEnabled
		p.CreatedAt = &saCreated
	}
	return p
}

// saTagWant is one row the fixture stores, as the scan must store it.
type saTagWant struct {
	typ, arn, name  string
	status, created *string
	managed         bool
	attrs           string
	tags            map[string]string
}

func saTagWants() []saTagWant {
	created := ptrStr("2026-09-01T12:00:00Z")
	tagged := func(typ string) map[string]string { return map[string]string{"type": typ} }
	return []saTagWant{
		{TypeSecurityAgentAgentSpace, saTagSpaceT, "space t", nil, created, false, mustJSON(saTagSpace("t")), tagged(TypeSecurityAgentAgentSpace)},
		{TypeSecurityAgentAgentSpace, saTagSpaceB, "space b", nil, nil, false, mustJSON(saTagSpace("b")), nil},
		{TypeSecurityAgentApplication, saTagAppT, "app t", nil, nil, false, mustJSON(saTagApp("t")), tagged(TypeSecurityAgentApplication)},
		{TypeSecurityAgentApplication, saTagAppB, "app b", nil, nil, false, mustJSON(saTagApp("b")), nil},
		{TypeSecurityAgentTargetDomain, saTagDomainT, "t.example.com", ptrStr("VERIFIED"), nil, false, mustJSON(saTagDomain("t")), tagged(TypeSecurityAgentTargetDomain)},
		{TypeSecurityAgentTargetDomain, saTagDomainB, "b.example.com", nil, nil, false, mustJSON(saTagDomain("b")), nil},
		{TypeSecurityAgentIntegration, saTagIntegT, "integration t", nil, nil, false, mustJSON(saTagIntegration("t")), tagged(TypeSecurityAgentIntegration)},
		{TypeSecurityAgentIntegration, saTagIntegB, "integration b", nil, nil, false, mustJSON(saTagIntegration("b")), nil},
		{TypeSecurityAgentPrivateConnection, saTagConnT, "pc-t", ptrStr("ACTIVE"), nil, false, mustJSON(saTagConnection("t")), tagged(TypeSecurityAgentPrivateConnection)},
		{TypeSecurityAgentPrivateConnection, saTagConnB, "pc-b", nil, nil, false, mustJSON(saTagConnection("b")), nil},
		{TypeSecurityAgentSecurityRequirementPack, saTagPackT, "pack pk-t", ptrStr("ENABLED"), created, false, mustJSON(saTagPack("pk-t", satypes.ManagementTypeCustomerManaged)), tagged(TypeSecurityAgentSecurityRequirementPack)},
		{TypeSecurityAgentSecurityRequirementPack, saTagPackB, "pack pk-b", nil, nil, false, mustJSON(saTagPack("pk-b", satypes.ManagementTypeCustomerManaged)), nil},
		{TypeSecurityAgentSecurityRequirementPack, saTagPackAWS, "pack aws-pk", ptrStr("ENABLED"), created, true, mustJSON(saTagPack("aws-pk", satypes.ManagementTypeAwsManaged)), nil},
	}
}

// saTagCalls records the stubbed calls, the parent each per-parent listing
// was sent, and answers ListTagsForResource from tags and errs, keyed by ARN.
type saTagCalls struct {
	*bacCalls
	parents map[string]*parentCalls
}

func newSATagCalls() *saTagCalls {
	c := &saTagCalls{bacCalls: newBACCalls(), parents: map[string]*parentCalls{}}
	for _, w := range saTagWants() {
		if w.tags != nil && w.typ != TypeSecurityAgentPrivateConnection {
			c.tags[w.arn] = w.tags
		}
	}
	return c
}

func (c *saTagCalls) listTags(in any) (any, error) {
	c.called("ListTagsForResource", in)
	arn := sv(in.(*securityagent.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &securityagent.ListTagsForResourceOutput{Tags: tags}, nil
}

// twoPages answers op with "t" on the first page and "b" on the second.
func (c *saTagCalls) twoPages(op string, out func(n string, next *string) any) func(any) (any, error) {
	return c.answer(op, func(in any) any {
		if bacField[*string](in, "NextToken") == nil {
			return out("t", ptrStr("p2"))
		}
		return out("b", nil)
	})
}

// noChildren answers a per-parent listing with out(), recording the parent
// named by field (and, for memberships, the application). out builds a new
// output per call: the SDK writes ResultMetadata into it, and the parents'
// calls run concurrently.
func (c *saTagCalls) noChildren(op, field string, out func() any) func(any) (any, error) {
	c.parents[op] = &parentCalls{}
	return func(in any) (any, error) {
		c.called(op, in)
		parent := sv(bacField[*string](in, field))
		if app := bacField[*string](in, "ApplicationId"); app != nil {
			parent += "/" + *app
		}
		c.parents[op].add(parent)
		return out(), nil
	}
}

func saTagHandlers(c *saTagCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListAgentSpaces": c.twoPages("ListAgentSpaces", func(n string, next *string) any {
			return &securityagent.ListAgentSpacesOutput{AgentSpaceSummaries: []satypes.AgentSpaceSummary{saTagSpace(n)}, NextToken: next}
		}),
		"ListApplications": c.twoPages("ListApplications", func(n string, next *string) any {
			return &securityagent.ListApplicationsOutput{ApplicationSummaries: []satypes.ApplicationSummary{saTagApp(n)}, NextToken: next}
		}),
		"ListTargetDomains": c.twoPages("ListTargetDomains", func(n string, next *string) any {
			return &securityagent.ListTargetDomainsOutput{TargetDomainSummaries: []satypes.TargetDomainSummary{saTagDomain(n)}, NextToken: next}
		}),
		"ListIntegrations": c.twoPages("ListIntegrations", func(n string, next *string) any {
			return &securityagent.ListIntegrationsOutput{IntegrationSummaries: []satypes.IntegrationSummary{saTagIntegration(n)}, NextToken: next}
		}),
		"ListPrivateConnections": c.twoPages("ListPrivateConnections", func(n string, next *string) any {
			return &securityagent.ListPrivateConnectionsOutput{PrivateConnections: []satypes.PrivateConnectionSummary{saTagConnection(n)}, NextToken: next}
		}),
		"ListSecurityRequirementPacks": c.twoPages("ListSecurityRequirementPacks", func(n string, next *string) any {
			packs := []satypes.SecurityRequirementPackSummary{saTagPack("pk-"+n, satypes.ManagementTypeCustomerManaged)}
			if n == "b" {
				packs = append(packs, saTagPack("aws-pk", satypes.ManagementTypeAwsManaged))
			}
			return &securityagent.ListSecurityRequirementPacksOutput{SecurityRequirementPackSummaries: packs, NextToken: next}
		}),
		"ListPentests":             c.noChildren("ListPentests", "AgentSpaceId", func() any { return &securityagent.ListPentestsOutput{} }),
		"ListCodeReviews":          c.noChildren("ListCodeReviews", "AgentSpaceId", func() any { return &securityagent.ListCodeReviewsOutput{} }),
		"ListThreatModels":         c.noChildren("ListThreatModels", "AgentSpaceId", func() any { return &securityagent.ListThreatModelsOutput{} }),
		"ListMemberships":          c.noChildren("ListMemberships", "AgentSpaceId", func() any { return &securityagent.ListMembershipsOutput{} }),
		"ListIntegratedResources":  c.noChildren("ListIntegratedResources", "AgentSpaceId", func() any { return &securityagent.ListIntegratedResourcesOutput{} }),
		"ListSecurityRequirements": c.noChildren("ListSecurityRequirements", "PackId", func() any { return &securityagent.ListSecurityRequirementsOutput{} }),
	}
}

// saTagScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func saTagScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, saDenied)
	return scanSecurityAgent(ctx, acct, testRegion, st, testScanID)
}

func saRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

func saSamePtr(a, b *string) bool { return (a == nil) == (b == nil) && sv(a) == sv(b) }

// Every row keeps the base scan's identity, Name, Status and attributes and
// gains the element's CreatedAt where it has one. The private connection
// stores the tags its element carries; every other type the tags
// ListTagsForResource returns for its ARN, built from the listed id, nil for
// a resource without tags. The AWS-managed pack is not read and stored
// without tags. Every listing page is read without a page size (the model
// gives no range), and each per-parent listing is sent each stored parent;
// only customer-managed packs have their requirements listed.
func TestScanSecurityAgent_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newSATagCalls()

	total, inserted, err := saTagScan(context.Background(), st, saTagHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != saTagRows || inserted != saTagRows || countStoredRows(t, st) != saTagRows {
		t.Errorf("total, inserted, stored = %d, %d, %d; want %d", total, inserted, countStoredRows(t, st), saTagRows)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, want := range saTagWants() {
		r, ok := saRowOf(t, st, want.typ, want.arn)
		if !ok {
			t.Errorf("%s %s not stored", want.typ, want.arn)
			continue
		}
		if want.tags != nil {
			assertTagsMap(t, r.TagsJSON, want.tags)
		} else if r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %s; want nil", want.arn, *r.TagsJSON)
		}
		if sv(r.Name) != want.name || sv(r.Region) != testRegion || r.ManagedByProvider != want.managed {
			t.Errorf("%s Name, Region, managed = %v, %v, %v; want %s, %s, %v", want.arn, sv(r.Name), sv(r.Region), r.ManagedByProvider, want.name, testRegion, want.managed)
		}
		if !saSamePtr(r.Status, want.status) || !saSamePtr(r.CreatedAt, want.created) {
			t.Errorf("%s Status, CreatedAt = %v, %v; want %v, %v", want.arn, r.Status, r.CreatedAt, want.status, want.created)
		}
		if r.AttributesJSON != want.attrs {
			t.Errorf("%s attributes = %s; want %s", want.arn, r.AttributesJSON, want.attrs)
		}
	}
	if got, want := slices.Sorted(slices.Values(calls.tagARNs)), slices.Sorted(slices.Values(saTagReadARNs)); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
	}
	for _, op := range []string{"ListAgentSpaces", "ListApplications", "ListTargetDomains", "ListIntegrations", "ListPrivateConnections", "ListSecurityRequirementPacks"} {
		if n, caps := calls.count(op), calls.maxResults[op]; n != 2 || len(caps) != 0 {
			t.Errorf("%s calls, MaxResults = %d, %v; want 2, none sent", op, n, caps)
		}
	}
	spaces := []string{"s-b", "s-t"}
	for op, want := range map[string][]string{
		"ListPentests":             spaces,
		"ListCodeReviews":          spaces,
		"ListThreatModels":         spaces,
		"ListIntegratedResources":  spaces,
		"ListMemberships":          {"s-b/a-b", "s-b/a-t", "s-t/a-b", "s-t/a-t"},
		"ListSecurityRequirements": {"pk-b", "pk-t"},
	} {
		if got := calls.parents[op].distinct(); !slices.Equal(got, want) {
			t.Errorf("%s parents = %v; want %v", op, got, want)
		}
	}
}

// An aws-us-gov account scanned in us-gov-west-1 sends the tag op the ARNs
// built in the account's partition and region.
func TestScanSecurityAgent_GovCloudARNsReachTagOp(t *testing.T) {
	const (
		govRegion = "us-gov-west-1"
		space     = "arn:aws-us-gov:securityagent:us-gov-west-1:123456789012:agent-space/s-gov"
		app       = "arn:aws-us-gov:securityagent:us-gov-west-1:123456789012:application/a-gov"
		domain    = "arn:aws-us-gov:securityagent:us-gov-west-1:123456789012:target-domain/td-gov"
		integ     = "arn:aws-us-gov:securityagent:us-gov-west-1:123456789012:integration/i-gov"
		pack      = "arn:aws-us-gov:securityagent:us-gov-west-1:123456789012:security-requirement-pack/pk-gov"
	)
	st := newTestStore(t)
	calls := newSATagCalls()
	h := saTagHandlers(calls)
	h["ListAgentSpaces"] = calls.answer("ListAgentSpaces", func(any) any {
		return &securityagent.ListAgentSpacesOutput{AgentSpaceSummaries: []satypes.AgentSpaceSummary{{AgentSpaceId: ptrStr("s-gov")}}}
	})
	h["ListApplications"] = calls.answer("ListApplications", func(any) any {
		return &securityagent.ListApplicationsOutput{ApplicationSummaries: []satypes.ApplicationSummary{{ApplicationId: ptrStr("a-gov")}}}
	})
	h["ListTargetDomains"] = calls.answer("ListTargetDomains", func(any) any {
		return &securityagent.ListTargetDomainsOutput{TargetDomainSummaries: []satypes.TargetDomainSummary{{TargetDomainId: ptrStr("td-gov")}}}
	})
	h["ListIntegrations"] = calls.answer("ListIntegrations", func(any) any {
		return &securityagent.ListIntegrationsOutput{IntegrationSummaries: []satypes.IntegrationSummary{{IntegrationId: ptrStr("i-gov")}}}
	})
	h["ListPrivateConnections"] = calls.answer("ListPrivateConnections", func(any) any { return &securityagent.ListPrivateConnectionsOutput{} })
	h["ListSecurityRequirementPacks"] = calls.answer("ListSecurityRequirementPacks", func(any) any {
		return &securityagent.ListSecurityRequirementPacksOutput{SecurityRequirementPackSummaries: []satypes.SecurityRequirementPackSummary{{PackId: ptrStr("pk-gov"), ManagementType: satypes.ManagementTypeCustomerManaged}}}
	})
	want := []string{app, space, integ, pack, domain}
	for _, arn := range want {
		calls.tags[arn] = map[string]string{"env": "gov"}
	}
	acct := partitionAccount("aws-us-gov")
	acct.cfg = opStubConfig(h, saDenied)

	total, _, err := scanSecurityAgent(context.Background(), acct, govRegion, st, testScanID)
	if err != nil || total != len(want) {
		t.Fatalf("scan = %d, %v; want %d, nil", total, err, len(want))
	}
	if got, want := slices.Sorted(slices.Values(calls.tagARNs)), slices.Sorted(slices.Values(want)); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
	}
	for _, c := range []struct{ typ, arn string }{
		{TypeSecurityAgentAgentSpace, space},
		{TypeSecurityAgentApplication, app},
		{TypeSecurityAgentTargetDomain, domain},
		{TypeSecurityAgentIntegration, integ},
		{TypeSecurityAgentSecurityRequirementPack, pack},
	} {
		r, ok := saRowOf(t, st, c.typ, c.arn)
		if !ok {
			t.Fatalf("%s %s not stored", c.typ, c.arn)
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"env": "gov"})
		if sv(r.Region) != govRegion {
			t.Errorf("%s Region = %v; want %s", c.arn, sv(r.Region), govRegion)
		}
	}
}

// Empty listings store nothing, make no per-parent listing, read no tags and
// report nothing.
func TestScanSecurityAgent_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newSATagCalls()
	h := saTagHandlers(calls)
	for op, out := range map[string]any{
		"ListAgentSpaces":              &securityagent.ListAgentSpacesOutput{},
		"ListApplications":             &securityagent.ListApplicationsOutput{},
		"ListTargetDomains":            &securityagent.ListTargetDomainsOutput{},
		"ListIntegrations":             &securityagent.ListIntegrationsOutput{},
		"ListPrivateConnections":       &securityagent.ListPrivateConnectionsOutput{},
		"ListSecurityRequirementPacks": &securityagent.ListSecurityRequirementPacksOutput{},
	} {
		h[op] = calls.answer(op, func(any) any { return out })
	}

	total, inserted, err := saTagScan(context.Background(), st, h)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if *warns != 0 || len(calls.tagARNs) != 0 {
		t.Errorf("warnings, tag reads = %d, %v; want 0, none", *warns, calls.tagARNs)
	}
	for op := range calls.parents {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times with no parent", op, n)
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanSecurityAgent_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := saTagScan(context.Background(), st, saTagHandlers(newSATagCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := saTagScan(context.Background(), st, saTagHandlers(newSATagCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != saTagRows || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, saTagRows)
	}
}

// A failed tag read keeps the row without tags: a denial warns once under
// securityagent:ListTagsForResource, a throttle after retries is a transient
// error, and any other code a hard one. The op models no not-found, so a
// ResourceNotFoundException is a hard error too and the row is kept.
func TestScanSecurityAgent_TagReadFailures(t *testing.T) {
	for _, target := range []struct{ typ, arn string }{
		{TypeSecurityAgentAgentSpace, saTagSpaceT},
		{TypeSecurityAgentApplication, saTagAppT},
		{TypeSecurityAgentTargetDomain, saTagDomainT},
		{TypeSecurityAgentIntegration, saTagIntegT},
		{TypeSecurityAgentSecurityRequirementPack, saTagPackT},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				code      string
				wantWarns int
			}{
				{"denied", saDenied, 0, "", 1},
				{"not found", saGone, outcomeError, "ResourceNotFoundException", 0},
				{"other error", saConflict, outcomeError, "ConflictException", 0},
				{"transient", saThrottled, outcomeTransient, "ThrottlingException", 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, saTagsOp)
					calls := newSATagCalls()
					calls.errs[target.arn] = c.err

					total, _, err := saTagScan(context.Background(), st, saTagHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
						assertAPICode(t, err, c.code)
					}
					r, ok := saRowOf(t, st, target.typ, target.arn)
					if !ok {
						t.Fatal("row not stored; want it kept")
					}
					if r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if stored := countStoredRows(t, st); total != saTagRows || stored != saTagRows {
						t.Errorf("total, stored = %d, %d; want %d", total, stored, saTagRows)
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", saTagsOp, *warnings, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
				})
			}
		})
	}
}

// Denied tag reads in several phases are one warning for the scan.
func TestScanSecurityAgent_TagDenialsWarnOnce(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, saTagsOp)
	calls := newSATagCalls()
	denied := []struct{ typ, arn string }{
		{TypeSecurityAgentAgentSpace, saTagSpaceT},
		{TypeSecurityAgentApplication, saTagAppB},
		{TypeSecurityAgentTargetDomain, saTagDomainB},
		{TypeSecurityAgentIntegration, saTagIntegB},
		{TypeSecurityAgentSecurityRequirementPack, saTagPackT},
	}
	for _, d := range denied {
		calls.errs[d.arn] = saDenied
	}

	total, _, err := saTagScan(context.Background(), st, saTagHandlers(calls))
	if err != nil || total != saTagRows {
		t.Fatalf("scan = %d, %v; want %d, nil", total, err, saTagRows)
	}
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion {
		t.Errorf("%s warnings = %v; want one, scoped %s/%s", saTagsOp, *warnings, testAccountID, testRegion)
	}
	for _, d := range denied {
		if r, ok := saRowOf(t, st, d.typ, d.arn); !ok || r.TagsJSON != nil {
			t.Errorf("%s stored %v, TagsJSON %v; want it kept without tags", d.arn, ok, r.TagsJSON)
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanSecurityAgent_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, saTagsOp)
	calls := newSATagCalls()
	calls.errs[saTagSpaceT] = saThrottled
	calls.errs[saTagPackB] = saConflict

	_, _, err := saTagScan(context.Background(), st, saTagHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ConflictException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", saTagsOp, *warns)
	}
}

// saPhaseOps is every listing of the scan, in the order it runs them.
var saPhaseOps = []string{
	"ListAgentSpaces", "ListApplications", "ListPentests", "ListTargetDomains", "ListIntegrations", "ListPrivateConnections",
	"ListSecurityRequirementPacks", "ListCodeReviews", "ListThreatModels", "ListMemberships", "ListIntegratedResources", "ListSecurityRequirements",
}

// A failed write of any region-wide type stops the scan before any later
// listing and sends no warning about the rows it lost, their denied tag
// reads included.
func TestScanSecurityAgent_FailedWriteStopsWithoutWarning(t *testing.T) {
	for _, c := range []struct {
		typ, op, deniedARN string
	}{
		{TypeSecurityAgentAgentSpace, "ListAgentSpaces", saTagSpaceT},
		{TypeSecurityAgentApplication, "ListApplications", saTagAppT},
		{TypeSecurityAgentTargetDomain, "ListTargetDomains", saTagDomainT},
		{TypeSecurityAgentIntegration, "ListIntegrations", saTagIntegT},
		{TypeSecurityAgentPrivateConnection, "ListPrivateConnections", ""},
		{TypeSecurityAgentSecurityRequirementPack, "ListSecurityRequirementPacks", saTagPackT},
	} {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			refuseStoreWrites(t, st, c.typ)
			calls := newSATagCalls()
			if c.deniedARN != "" {
				calls.errs[c.deniedARN] = saDenied
			}
			h := saTagHandlers(calls)
			if c.typ == TypeSecurityAgentPrivateConnection {
				// A nameless element would warn once its listing's rows are stored.
				h["ListPrivateConnections"] = calls.answer("ListPrivateConnections", func(any) any {
					return &securityagent.ListPrivateConnectionsOutput{PrivateConnections: []satypes.PrivateConnectionSummary{saTagConnection("t"), {}}}
				})
			}

			_, _, err := saTagScan(context.Background(), st, h)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
			for _, op := range saPhaseOps[slices.Index(saPhaseOps, c.op)+1:] {
				if n := calls.count(op); n != 0 {
					t.Errorf("%s called %d times after the failed write", op, n)
				}
			}
		})
	}
}

// A listing's failed page keeps the pages read before it, and its rows are
// still parents; no listing's failure stops the phases after it.
func TestScanSecurityAgent_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newSATagCalls()
	h := saTagHandlers(calls)
	pageTwo := apiErr("InvalidPaginationTokenException", "page 2")
	for _, op := range []string{"ListAgentSpaces", "ListApplications", "ListTargetDomains", "ListIntegrations", "ListPrivateConnections", "ListSecurityRequirementPacks"} {
		h[op] = failOnCall(2, pageTwo, h[op])
	}

	total, _, err := saTagScan(context.Background(), st, h)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidPaginationTokenException")
	if n := len(bacTopMembers(err)); n != 6 {
		t.Errorf("error members = %d; want one per failed listing, 6 (err %v)", n, err)
	}
	var want []string
	for _, w := range saTagWants() {
		if strings.HasSuffix(w.arn, "-t") {
			want = append(want, w.arn)
		}
	}
	if stored := countStoredRows(t, st); total != len(want) || stored != len(want) {
		t.Errorf("total, stored = %d, %d; want %d", total, stored, len(want))
	}
	for _, w := range saTagWants() {
		if _, ok := saRowOf(t, st, w.typ, w.arn); ok != slices.Contains(want, w.arn) {
			t.Errorf("%s stored = %v; want %v", w.arn, ok, slices.Contains(want, w.arn))
		}
	}
	for op, want := range map[string][]string{
		"ListThreatModels":         {"s-t"},
		"ListMemberships":          {"s-t/a-t"},
		"ListSecurityRequirements": {"pk-t"},
	} {
		if got := calls.parents[op].distinct(); !slices.Equal(got, want) {
			t.Errorf("%s parents = %v; want %v", op, got, want)
		}
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanSecurityAgent_RepeatedTokenKeepsPages(t *testing.T) {
	// out builds a new output per call: the SDK writes ResultMetadata into it.
	for _, c := range []struct {
		op, typ, arn string
		out          func() any
	}{
		{"ListAgentSpaces", TypeSecurityAgentAgentSpace, saTagSpaceT, func() any {
			return &securityagent.ListAgentSpacesOutput{AgentSpaceSummaries: []satypes.AgentSpaceSummary{saTagSpace("t")}, NextToken: ptrStr("same")}
		}},
		{"ListApplications", TypeSecurityAgentApplication, saTagAppT, func() any {
			return &securityagent.ListApplicationsOutput{ApplicationSummaries: []satypes.ApplicationSummary{saTagApp("t")}, NextToken: ptrStr("same")}
		}},
		{"ListTargetDomains", TypeSecurityAgentTargetDomain, saTagDomainT, func() any {
			return &securityagent.ListTargetDomainsOutput{TargetDomainSummaries: []satypes.TargetDomainSummary{saTagDomain("t")}, NextToken: ptrStr("same")}
		}},
		{"ListIntegrations", TypeSecurityAgentIntegration, saTagIntegT, func() any {
			return &securityagent.ListIntegrationsOutput{IntegrationSummaries: []satypes.IntegrationSummary{saTagIntegration("t")}, NextToken: ptrStr("same")}
		}},
		{"ListPrivateConnections", TypeSecurityAgentPrivateConnection, saTagConnT, func() any {
			return &securityagent.ListPrivateConnectionsOutput{PrivateConnections: []satypes.PrivateConnectionSummary{saTagConnection("t")}, NextToken: ptrStr("same")}
		}},
		{"ListSecurityRequirementPacks", TypeSecurityAgentSecurityRequirementPack, saTagPackT, func() any {
			return &securityagent.ListSecurityRequirementPacksOutput{SecurityRequirementPackSummaries: []satypes.SecurityRequirementPackSummary{saTagPack("pk-t", satypes.ManagementTypeCustomerManaged)}, NextToken: ptrStr("same")}
		}},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			h := saTagHandlers(newSATagCalls())
			h[c.op] = func(any) (any, error) { return c.out(), nil }

			_, _, err := saTagScan(context.Background(), st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if _, ok := saRowOf(t, st, c.typ, c.arn); !ok {
				t.Errorf("%s from the pages read missing", c.arn)
			}
		})
	}
}

// Elements listed without their identifier are not stored or read and are
// warned about once per listing, under a key that is never empty.
func TestScanSecurityAgent_UnstorableItemsWarn(t *testing.T) {
	for _, c := range []struct {
		name, op, key string
		out           any
	}{
		{"agent space without id", "ListAgentSpaces", "first space x", &securityagent.ListAgentSpacesOutput{AgentSpaceSummaries: []satypes.AgentSpaceSummary{{Name: ptrStr("space x")}}}},
		{"application without id", "ListApplications", "first (an element with no identifier)", &securityagent.ListApplicationsOutput{ApplicationSummaries: []satypes.ApplicationSummary{{}}}},
		{"target domain without id", "ListTargetDomains", "first x.example.com", &securityagent.ListTargetDomainsOutput{TargetDomainSummaries: []satypes.TargetDomainSummary{{DomainName: ptrStr("x.example.com")}}}},
		{"integration without id", "ListIntegrations", "first integration x", &securityagent.ListIntegrationsOutput{IntegrationSummaries: []satypes.IntegrationSummary{{DisplayName: ptrStr("integration x")}}}},
		{"private connection without name", "ListPrivateConnections", "first (an element with no identifier)", &securityagent.ListPrivateConnectionsOutput{PrivateConnections: []satypes.PrivateConnectionSummary{{Status: satypes.PrivateConnectionStatusActive}}}},
		{"pack without id", "ListSecurityRequirementPacks", "first pack x", &securityagent.ListSecurityRequirementPacksOutput{SecurityRequirementPackSummaries: []satypes.SecurityRequirementPackSummary{{Name: ptrStr("pack x")}}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			warnings := bacWarnings(st, "securityagent:"+c.op)
			calls := newSATagCalls()
			h := saTagHandlers(calls)
			h[c.op] = func(any) (any, error) { return c.out, nil }

			total, _, err := saTagScan(context.Background(), st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || len(*warnings) != 1 || !strings.HasSuffix((*warnings)[0].Message, c.key) {
				t.Errorf("warnings = %d, under the op %v; want one ending %q", *warns, *warnings, c.key)
			}
			lost := 2
			if c.op == "ListSecurityRequirementPacks" {
				lost = 3
			}
			if want := saTagRows - lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
		})
	}
}

// A denied listing warns once under its own op and costs no other phase its
// rows; denied agent spaces leave the per-space phases no parent.
func TestScanSecurityAgent_DeniedListingWarnsUnderItsOp(t *testing.T) {
	for _, c := range []struct {
		op   string
		lost int
	}{
		{"ListAgentSpaces", 2},
		{"ListApplications", 2},
		{"ListTargetDomains", 2},
		{"ListIntegrations", 2},
		{"ListPrivateConnections", 2},
		{"ListSecurityRequirementPacks", 3},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, "securityagent:"+c.op)
			calls := newSATagCalls()
			h := saTagHandlers(calls)
			h[c.op] = func(any) (any, error) { return nil, saDenied }

			total, _, err := saTagScan(context.Background(), st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || *opWarns != 1 {
				t.Errorf("warnings = %d (securityagent:%s: %d); want 1 under the op", *warns, c.op, *opWarns)
			}
			want := saTagRows - c.lost
			if stored := countStoredRows(t, st); total != want || stored != want {
				t.Errorf("total = %d, stored rows = %d; want %d", total, stored, want)
			}
			if c.op == "ListAgentSpaces" && calls.count("ListPentests")+calls.count("ListIntegratedResources") != 0 {
				t.Error("per-space listings called with no agent space read")
			}
		})
	}
}

// Once ctx ends no further phase starts, and the stop is reported once.
func TestScanSecurityAgent_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newSATagCalls()
	h := saTagHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h["ListTargetDomains"] = func(any) (any, error) {
		cancel()
		return nil, context.Canceled
	}

	_, _, err := saTagScan(ctx, st, h)
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
	for _, op := range saPhaseOps[slices.Index(saPhaseOps, "ListTargetDomains")+1:] {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// A region without Security Agent (NXDOMAIN on the first listing, nothing
// stored) is unavailable and makes no other listing. An NXDOMAIN after rows
// were stored is a failure of this scan: the counts are kept.
func TestScanSecurityAgent_Unreachable(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "securityagent.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("endpoint absent", func(t *testing.T) {
		st := newTestStore(t)
		calls := newSATagCalls()
		h := saTagHandlers(calls)
		h["ListAgentSpaces"] = func(any) (any, error) { return nil, nx }
		total, _, err := saTagScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 || calls.count("ListApplications") != 0 {
			t.Errorf("total, ListApplications calls = %d, %d; want 0, 0", total, calls.count("ListApplications"))
		}
	})
	t.Run("tag read after rows were listed", func(t *testing.T) {
		st := newTestStore(t)
		calls := newSATagCalls()
		calls.errs[saTagIntegB] = nx
		total, _, err := saTagScan(context.Background(), st, saTagHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
		if total != saTagRows {
			t.Errorf("total = %d; want %d", total, saTagRows)
		}
	})
}
