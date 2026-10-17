package aws

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloud9"
	cloud9types "github.com/aws/aws-sdk-go-v2/service/cloud9/types"
	"github.com/icearp/disco-cli/store"
)

var cloud9Denied = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: cloud9:DescribeEnvironmentMemberships")

func cloud9EnvARN(id string) string {
	return fmt.Sprintf("arn:aws:cloud9:%s:%s:environment:%s", testRegion, testAccountID, id)
}

func cloud9UserARN(name string) string {
	return fmt.Sprintf("arn:aws:iam::%s:user/team/%s", testAccountID, name)
}

// cloud9Envs stores an environment row for each id and returns them as parents.
func cloud9Envs(t *testing.T, st *store.Store, ids ...string) []childParent {
	t.Helper()
	envs := make([]childParent, len(ids))
	for i, id := range ids {
		envs[i] = childParent{id: id, arn: cloud9EnvARN(id)}
		upsertTestResource(t, st, "aws", testAccountID, TypeCloud9EnvironmentEC2, cloud9EnvARN(id), testRegion, "{}")
	}
	return envs
}

func cloud9Member(envID, user string, perm cloud9types.Permissions) cloud9types.EnvironmentMember {
	return cloud9types.EnvironmentMember{
		EnvironmentId: sdkaws.String(envID), UserArn: sdkaws.String(cloud9UserARN(user)),
		UserId: sdkaws.String("AIDA" + strings.ToUpper(user)), Permissions: perm,
	}
}

// cloud9MembersHandler answers DescribeEnvironmentMemberships from pages keyed
// by environment id: the first page of an environment carries NextToken "p2"
// when it has a second. errs fails an environment's call with the given
// error; a "/p2" key fails only its second page. A call whose MaxResults is
// not the model cap, or that names no environment, fails.
func cloud9MembersHandler(calls *parentCalls, pages map[string][][]cloud9types.EnvironmentMember, errs map[string]error) func(any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*cloud9.DescribeEnvironmentMembershipsInput)
		if sdkaws.ToInt32(req.MaxResults) != cloud9MembershipPageLimit {
			return nil, fmt.Errorf("MaxResults = %v; want %d", req.MaxResults, cloud9MembershipPageLimit)
		}
		id := sdkaws.ToString(req.EnvironmentId)
		if id == "" {
			return nil, errors.New("EnvironmentId not set")
		}
		calls.add(id)
		page := 0
		if sdkaws.ToString(req.NextToken) == "p2" {
			page = 1
			if err := errs[id+"/p2"]; err != nil {
				return nil, err
			}
		} else if err := errs[id]; err != nil {
			return nil, err
		}
		out := &cloud9.DescribeEnvironmentMembershipsOutput{}
		if page < len(pages[id]) {
			out.Memberships = pages[id][page]
		}
		if page == 0 && len(pages[id]) > 1 {
			out.NextToken = sdkaws.String("p2")
		}
		return out, nil
	}
}

func newCloud9TestClient(handlers map[string]func(in any) (any, error)) *cloud9.Client {
	return cloud9.NewFromConfig(opStubConfig(handlers, cloud9Denied))
}

func TestScanCloud9Memberships_PagesAndFields(t *testing.T) {
	st := newTestStore(t)
	owner := cloud9Member("env1", "alice", cloud9types.PermissionsOwner)
	owner.LastAccess = sdkaws.Time(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	var calls parentCalls
	client := newCloud9TestClient(map[string]func(any) (any, error){
		"DescribeEnvironmentMemberships": cloud9MembersHandler(&calls, map[string][][]cloud9types.EnvironmentMember{
			"env1": {{owner, {EnvironmentId: sdkaws.String("env1")}}, {cloud9Member("env1", "bob", cloud9types.PermissionsReadWrite)}},
			"env2": {{cloud9Member("env2", "alice", cloud9types.PermissionsReadOnly)}},
		}, nil),
	})

	total, _, err := scanCloud9Memberships(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, cloud9Envs(t, st, "env1", "env2", "env3"))
	if err != nil {
		t.Fatalf("scanCloud9Memberships: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3", total)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{"env1", "env2", "env3"}) {
		t.Errorf("environments queried = %v; want all three", got)
	}
	rows := rowsByNativeID(t, st, TypeCloud9EnvironmentMembership)
	if len(rows) != 3 {
		t.Fatalf("rows = %d; want 3 (the element without UserArn skipped): %v", len(rows), rows)
	}
	ownerID := cloud9EnvARN("env1") + "/membership/" + cloud9UserARN("alice")
	for _, want := range []struct{ parent, id string }{
		{cloud9EnvARN("env1"), ownerID},
		{cloud9EnvARN("env1"), cloud9EnvARN("env1") + "/membership/" + cloud9UserARN("bob")},
		{cloud9EnvARN("env2"), cloud9EnvARN("env2") + "/membership/" + cloud9UserARN("alice")},
	} {
		r, ok := rows[want.id]
		if !ok {
			t.Errorf("missing membership %s", want.id)
			continue
		}
		if r.Type != TypeCloud9EnvironmentMembership || sv(r.Region) != testRegion {
			t.Errorf("%s: Type %q Region %q", want.id, r.Type, sv(r.Region))
		}
		assertContainsNative(t, st, want.parent, want.id)
	}
	r := rows[ownerID]
	if sv(r.Name) != cloud9UserARN("alice") {
		t.Errorf("Name = %q; want %q", sv(r.Name), cloud9UserARN("alice"))
	}
	for _, want := range []string{`"Permissions":"owner"`, `"UserId":"AIDAALICE"`, `"LastAccess":"2026-01-02T03:04:05Z"`} {
		if !strings.Contains(r.AttributesJSON, want) {
			t.Errorf("attrs = %s; want %s", r.AttributesJSON, want)
		}
	}
	assertContainsEdgesDeclared(t, st)
}

func TestScanCloud9Memberships_Empty(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := newCloud9TestClient(map[string]func(any) (any, error){
		"DescribeEnvironmentMemberships": cloud9MembersHandler(&calls, nil, nil),
	})
	total, _, err := scanCloud9Memberships(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, cloud9Envs(t, st, "env1"))
	if err != nil || total != 0 {
		t.Errorf("total, err = %d, %v; want 0, nil", total, err)
	}
	if n := len(rowsByNativeID(t, st, TypeCloud9EnvironmentMembership)); n != 0 {
		t.Errorf("rows = %d; want 0", n)
	}
}

// Each per-environment error is skipped or returned by its code. Two of three
// environments fail, so a skip that warns must warn once for both.
func TestScanCloud9Memberships_PerEnvironmentErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantErr   string
		wantWarns int
	}{
		{name: "denied warns once", err: cloud9Denied, wantWarns: 1},
		{name: "throttled warns once", err: apiErr("TooManyRequestsException", "Rate exceeded"), wantWarns: 1},
		{name: "bad request warns once", err: apiErr("BadRequestException", "environment not ready"), wantWarns: 1},
		{name: "environment deleted since listing skipped silently", err: apiErr("NotFoundException", "gone")},
		{name: "other code fails the phase", err: apiErr("ConflictException", "bad"), wantErr: "ConflictException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, cloud9DescribeMembershipsOp)
			var calls parentCalls
			client := newCloud9TestClient(map[string]func(any) (any, error){
				"DescribeEnvironmentMemberships": cloud9MembersHandler(&calls, map[string][][]cloud9types.EnvironmentMember{
					"env3": {{cloud9Member("env3", "alice", cloud9types.PermissionsOwner)}},
				}, map[string]error{"env1": tc.err, "env2": tc.err}),
			})
			total, _, err := scanCloud9Memberships(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, cloud9Envs(t, st, "env1", "env2", "env3"))
			if tc.wantErr != "" {
				if !isAPIErrorCode(err, tc.wantErr) || classifyServiceError(err) != outcomeError {
					t.Errorf("err = %v; want a hard %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
			id := cloud9EnvARN("env3") + "/membership/" + cloud9UserARN("alice")
			if _, ok := rowsByNativeID(t, st, TypeCloud9EnvironmentMembership)[id]; !ok || total != 1 {
				t.Errorf("total = %d, sibling row stored = %v; want 1, true", total, ok)
			}
		})
	}
}

// A skipped environment keeps the rows its earlier pages stored.
func TestScanCloud9Memberships_SkipKeepsEarlierPages(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := newCloud9TestClient(map[string]func(any) (any, error){
		"DescribeEnvironmentMemberships": cloud9MembersHandler(&calls, map[string][][]cloud9types.EnvironmentMember{
			"env1": {{cloud9Member("env1", "alice", cloud9types.PermissionsOwner)}, {cloud9Member("env1", "bob", cloud9types.PermissionsReadOnly)}},
		}, map[string]error{"env1/p2": apiErr("BadRequestException", "x")}),
	})
	total, _, err := scanCloud9Memberships(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, cloud9Envs(t, st, "env1"))
	if err != nil || total != 1 {
		t.Errorf("total, err = %d, %v; want 1, nil", total, err)
	}
	if _, ok := rowsByNativeID(t, st, TypeCloud9EnvironmentMembership)[cloud9EnvARN("env1")+"/membership/"+cloud9UserARN("alice")]; !ok {
		t.Error("first-page row not kept")
	}
}

// The listing stops when the service repeats the token it was sent, rather
// than looping on it.
func TestScanCloud9Memberships_RepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	var n atomic.Int64
	client := newCloud9TestClient(map[string]func(any) (any, error){
		"DescribeEnvironmentMemberships": func(any) (any, error) {
			if n.Add(1) > 5 {
				return nil, apiErr("ConflictException", "looped")
			}
			return &cloud9.DescribeEnvironmentMembershipsOutput{NextToken: sdkaws.String("same")}, nil
		},
	})
	_, _, err := scanCloud9Memberships(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, cloud9Envs(t, st, "env1"))
	if err != nil || n.Load() != 2 {
		t.Errorf("err, calls = %v, %d; want nil, 2", err, n.Load())
	}
}

// cloud9TopLevelHandlers answers the ops scanCloud9 calls: env1 is an EC2
// environment, env2 an SSH one (not stored), and each environment has one
// member. A membership call fails unless its environment row is already stored.
func cloud9TopLevelHandlers(st *store.Store, calls *parentCalls) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListEnvironments": func(any) (any, error) {
			return &cloud9.ListEnvironmentsOutput{EnvironmentIds: []string{"env1", "env2"}}, nil
		},
		"DescribeEnvironments": func(any) (any, error) {
			return &cloud9.DescribeEnvironmentsOutput{Environments: []cloud9types.Environment{
				{Id: sdkaws.String("env1"), Arn: sdkaws.String(cloud9EnvARN("env1")), Type: cloud9types.EnvironmentTypeEc2},
				{Id: sdkaws.String("env2"), Arn: sdkaws.String(cloud9EnvARN("env2")), Type: cloud9types.EnvironmentTypeSsh},
			}}, nil
		},
		"DescribeEnvironmentMemberships": func(in any) (any, error) {
			id := sdkaws.ToString(in.(*cloud9.DescribeEnvironmentMembershipsInput).EnvironmentId)
			if _, err := st.GetResource(store.ResourceID("aws", testAccountID, cloud9EnvARN(id))); err != nil {
				return nil, fmt.Errorf("environment %s not stored before its memberships: %w", id, err)
			}
			calls.add(id)
			return &cloud9.DescribeEnvironmentMembershipsOutput{Memberships: []cloud9types.EnvironmentMember{cloud9Member(id, "alice", cloud9types.PermissionsOwner)}}, nil
		},
	}
}

// The top-level scan lists the memberships of every stored environment, and
// only those, after the environment phase.
func TestScanCloud9_MembershipsOfStoredEnvironments(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(cloud9TopLevelHandlers(st, &calls), cloud9Denied)}

	total, _, err := scanCloud9(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloud9: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2 (1 environment + 1 membership)", total)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{"env1"}) {
		t.Errorf("environments queried = %v; want [env1]", got)
	}
	assertContainsNative(t, st, cloud9EnvARN("env1"), cloud9EnvARN("env1")+"/membership/"+cloud9UserARN("alice"))
}

// A membership phase error reaches the dispatcher as a hard error, and the
// environment rows stay.
func TestScanCloud9_MembershipErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	h := cloud9TopLevelHandlers(st, &calls)
	h["DescribeEnvironmentMemberships"] = func(any) (any, error) { return nil, apiErr("ConflictException", "bad") }
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, cloud9Denied)}

	_, _, err := scanCloud9(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "ConflictException") || classifyServiceError(err) != outcomeError {
		t.Errorf("err = %v; want a hard ConflictException", err)
	}
	if n := len(rowsByNativeID(t, st, TypeCloud9EnvironmentEC2)); n != 1 {
		t.Errorf("environment rows = %d; want 1", n)
	}
}

// A failed environment phase's error reaches the caller. This checks error
// propagation only: a failed environment phase returns no parents, so whether
// the membership phase is gated on it cannot be observed here.
func TestScanCloud9_EnvironmentErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	h := cloud9TopLevelHandlers(st, &calls)
	h["DescribeEnvironments"] = func(any) (any, error) { return nil, apiErr("ConflictException", "bad") }
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, cloud9Denied)}

	_, _, err := scanCloud9(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "ConflictException") {
		t.Errorf("err = %v; want the DescribeEnvironments ConflictException", err)
	}
}

// TestScanCloud9_ClosedToAccountExplicitMessage verifies the non-empty
// "does not have access to the Cloud9 service" AccessDeniedException (AWS's
// closed-to-new-customers signal for never-onboarded accounts) maps to the
// not-entitled sentinel — Cloud9 can't be self-enabled — with no warning.
func TestScanCloud9_ClosedToAccountExplicitMessage(t *testing.T) {
	st := newTestStore(t)
	warnings := 0
	st.OnWarn = func(_ store.ScanWarning) { warnings++ }

	stub := stubResponses(t, map[string][]stubCall{
		"ListEnvironments": {{Err: apiErr("AccessDeniedException",
			"This account does not have access to the Cloud9 service")}},
	})
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cfgWithStub(stub, testRegion)}

	_, _, err := scanCloud9(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, errServiceNotEntitled) {
		t.Fatalf("scanCloud9: got %v; want errServiceNotEntitled", err)
	}
	if warnings != 0 {
		t.Errorf("warnings=%d, want 0 (closed-state must not warn)", warnings)
	}
}

// TestScanCloud9_RealIAMDenialStillWarns confirms the message-disambiguated
// silent-skip doesn't swallow a genuine per-op IAM denial — those carry an
// action-identifying message and must still warn.
func TestScanCloud9_RealIAMDenialStillWarns(t *testing.T) {
	st := newTestStore(t)
	warnings := 0
	st.OnWarn = func(_ store.ScanWarning) { warnings++ }

	stub := stubResponses(t, map[string][]stubCall{
		"ListEnvironments": {{Err: apiErr("AccessDeniedException",
			"User: arn:aws:iam::123456789012:user/x is not authorized to perform: cloud9:ListEnvironments")}},
	})
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cfgWithStub(stub, testRegion)}

	total, inserted, err := scanCloud9(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloud9: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total=%d inserted=%d, want 0/0", total, inserted)
	}
	if warnings != 1 {
		t.Errorf("warnings=%d, want 1 (real IAM denial must warn)", warnings)
	}
}
