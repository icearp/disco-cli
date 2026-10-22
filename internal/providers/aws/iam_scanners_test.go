package aws

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	iamDenied  = &smithy.GenericAPIError{Code: "AccessDenied", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: iam:List"}
	iamGone    = &smithy.GenericAPIError{Code: "NoSuchEntity", Message: "The user with name gone cannot be found."}
	iamFailure = &smithy.GenericAPIError{Code: "ServiceFailure", Message: "boom"}
	iamTestDay = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
)

func iamTestUserARN(name string) string { return "arn:aws:iam::" + testAccountID + ":user/" + name }

func iamTestUsers(names ...string) []childParent {
	out := make([]childParent, len(names))
	for i, n := range names {
		out[i] = childParent{id: n, arn: iamTestUserARN(n)}
	}
	return out
}

// iamCredCase is one per-user credential phase: how to read its input, build a
// page of its output, and run it.
type iamCredCase struct {
	op, typ, kind string
	input         func(in any) (user string, marker *string)
	// page builds one output page; an "" id is an element with no id, and an
	// id ending in "-1" carries Status Active and a create date.
	page func(user string, next *string, ids ...string) any
	run  func(ctx context.Context, client iamAPI, users []childParent, st *store.Store) (int, int, error)
}

func iamElemStatus(id string) (iamtypes.StatusType, *time.Time) {
	if strings.HasSuffix(id, "-1") {
		return iamtypes.StatusTypeActive, &iamTestDay
	}
	return "", nil
}

const iamTestCertBody = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"

var iamCredCases = []iamCredCase{
	{
		op: "ListServiceSpecificCredentials", typ: TypeIAMServiceSpecificCredential, kind: "service-specific-credential",
		input: func(in any) (string, *string) {
			i := in.(*iam.ListServiceSpecificCredentialsInput)
			return sv(i.UserName), i.Marker
		},
		page: func(user string, next *string, ids ...string) any {
			out := &iam.ListServiceSpecificCredentialsOutput{Marker: next, IsTruncated: next != nil}
			for _, id := range ids {
				status, created := iamElemStatus(id)
				out.ServiceSpecificCredentials = append(out.ServiceSpecificCredentials, iamtypes.ServiceSpecificCredentialMetadata{
					ServiceSpecificCredentialId: nonEmptyPtr(id), UserName: &user, Status: status, CreateDate: created,
					ServiceName: ptrStr("codecommit.amazonaws.com"),
				})
			}
			return out
		},
		run: func(ctx context.Context, client iamAPI, users []childParent, st *store.Store) (int, int, error) {
			return scanIAMServiceSpecificCredentials(ctx, client, users, newTestAccount(testAccountID), st, testScanID)
		},
	},
	{
		op: "ListSigningCertificates", typ: TypeIAMSigningCertificate, kind: "signing-certificate",
		input: func(in any) (string, *string) {
			i := in.(*iam.ListSigningCertificatesInput)
			return sv(i.UserName), i.Marker
		},
		page: func(user string, next *string, ids ...string) any {
			out := &iam.ListSigningCertificatesOutput{Marker: next, IsTruncated: next != nil}
			for _, id := range ids {
				status, created := iamElemStatus(id)
				out.Certificates = append(out.Certificates, iamtypes.SigningCertificate{
					CertificateId: nonEmptyPtr(id), UserName: &user, Status: status, UploadDate: created,
					CertificateBody: ptrStr(iamTestCertBody),
				})
			}
			return out
		},
		run: func(ctx context.Context, client iamAPI, users []childParent, st *store.Store) (int, int, error) {
			return scanIAMSigningCertificates(ctx, client, users, newTestAccount(testAccountID), st, testScanID)
		},
	},
	{
		op: "ListSSHPublicKeys", typ: TypeIAMSSHPublicKey, kind: "ssh-public-key",
		input: func(in any) (string, *string) {
			i := in.(*iam.ListSSHPublicKeysInput)
			return sv(i.UserName), i.Marker
		},
		page: func(user string, next *string, ids ...string) any {
			out := &iam.ListSSHPublicKeysOutput{Marker: next, IsTruncated: next != nil}
			for _, id := range ids {
				status, created := iamElemStatus(id)
				out.SSHPublicKeys = append(out.SSHPublicKeys, iamtypes.SSHPublicKeyMetadata{
					SSHPublicKeyId: nonEmptyPtr(id), UserName: &user, Status: status, UploadDate: created,
				})
			}
			return out
		},
		run: func(ctx context.Context, client iamAPI, users []childParent, st *store.Store) (int, int, error) {
			return scanIAMSSHPublicKeys(ctx, client, users, newTestAccount(testAccountID), st, testScanID)
		},
	},
}

// handler serves two pages per user ({user}-1; then {user}-2 and an element
// with no id) and fails the users in fail with their error.
func (c iamCredCase) handler(fail map[string]error, calls *parentCalls) func(in any) (any, error) {
	return func(in any) (any, error) {
		user, marker := c.input(in)
		calls.add(user)
		if err := fail[user]; err != nil {
			return nil, err
		}
		if marker == nil {
			return c.page(user, ptrStr("m2"), user+"-1"), nil
		}
		return c.page(user, nil, user+"-2", ""), nil
	}
}

func (c iamCredCase) nativeID(user, id string) string {
	return iamTestUserARN(user) + "/" + c.kind + "/" + id
}

// scan seeds the users' rows, as the GAAD phase stores them, then runs the phase.
func (c iamCredCase) scan(t *testing.T, st *store.Store, users []childParent, h func(in any) (any, error)) (int, error) {
	t.Helper()
	for _, u := range users {
		upsertTestResource(t, st, "aws", testAccountID, TypeIAMUser, u.arn, "global", "{}")
	}
	client := iam.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){c.op: h}, iamDenied))
	total, _, err := c.run(context.Background(), client, users, st)
	return total, err
}

func TestScanIAMUserCredentials_StoresEveryPageOfEveryUser(t *testing.T) {
	for _, c := range iamCredCases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "iam:"+c.op)
			var calls parentCalls
			total, err := c.scan(t, st, iamTestUsers("alice", "bob"), c.handler(nil, &calls))
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			// Each user's second page holds an element with no id: not
			// stored, and reported in one warning for both users.
			if *warns != 1 {
				t.Errorf("iam:%s warnings = %d, want 1 for the id-less elements", c.op, *warns)
			}
			want := []string{c.nativeID("alice", "alice-1"), c.nativeID("alice", "alice-2"), c.nativeID("bob", "bob-1"), c.nativeID("bob", "bob-2")}
			rows := rowsByNativeID(t, st, c.typ)
			if got := nativeIDs(rows); total != 4 || !slices.Equal(got, want) {
				t.Fatalf("total = %d, rows = %v; want %v", total, got, want)
			}
			if got := calls.distinct(); !slices.Equal(got, []string{"alice", "bob"}) {
				t.Errorf("users queried = %v, want [alice bob]", got)
			}
			first := rows[c.nativeID("alice", "alice-1")]
			if sv(first.Name) != "alice-1" || sv(first.Status) != "Active" || sv(first.Region) != "global" ||
				sv(first.CreatedAt) != iamTestDay.Format(time.RFC3339) || !jsonHasString(t, first.AttributesJSON, "UserName", "alice") {
				t.Errorf("row name/status/region/created = %q/%q/%q/%q, attrs = %s", sv(first.Name), sv(first.Status), sv(first.Region), sv(first.CreatedAt), first.AttributesJSON)
			}
			if second := rows[c.nativeID("alice", "alice-2")]; second.Status != nil {
				t.Errorf("alice-2 status = %q, want unset for an absent Status", sv(second.Status))
			}
			assertContainsNative(t, st, iamTestUserARN("bob"), c.nativeID("bob", "bob-2"))
		})
	}
}

func TestScanIAMUserCredentials_Empty(t *testing.T) {
	for _, c := range iamCredCases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			total, err := c.scan(t, st, iamTestUsers("alice"), func(in any) (any, error) { return c.page("alice", nil), nil })
			if err != nil || total != 0 || len(rowsByNativeID(t, st, c.typ)) != 0 {
				t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
			}
		})
	}
}

// A user deleted mid-scan is skipped silently; a denial warns once for the op
// and drops only the denied users; any other error fails the phase. The
// served users' id-less elements add one warning to each count.
func TestScanIAMUserCredentials_PerUserErrors(t *testing.T) {
	for _, c := range iamCredCases {
		t.Run(c.op+"/gone", func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "iam:"+c.op)
			var calls parentCalls
			total, err := c.scan(t, st, iamTestUsers("alice", "gone"), c.handler(map[string]error{"gone": iamGone}, &calls))
			if err != nil || *warns != 1 || total != 2 {
				t.Errorf("total = %d, warnings = %d, err = %v; want alice's 2 rows, 1 warning, nil", total, *warns, err)
			}
			if got := calls.distinct(); !slices.Equal(got, []string{"alice", "gone"}) {
				t.Errorf("users queried = %v, want [alice gone]", got)
			}
		})
		t.Run(c.op+"/denied", func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "iam:"+c.op)
			var calls parentCalls
			fail := map[string]error{"alice": iamDenied, "bob": iamDenied}
			total, err := c.scan(t, st, iamTestUsers("alice", "bob", "carol"), c.handler(fail, &calls))
			if err != nil || *warns != 2 {
				t.Errorf("warnings = %d, err = %v; want 2 (the denial, carol's id-less element), nil", *warns, err)
			}
			want := []string{c.nativeID("carol", "carol-1"), c.nativeID("carol", "carol-2")}
			if got := nativeIDs(rowsByNativeID(t, st, c.typ)); total != 2 || !slices.Equal(got, want) {
				t.Errorf("total = %d, rows = %v; want %v", total, got, want)
			}
		})
		t.Run(c.op+"/other", func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			_, err := c.scan(t, st, iamTestUsers("alice", "bob"), c.handler(map[string]error{"bob": iamFailure}, &calls))
			assertAPICode(t, err, "ServiceFailure")
		})
	}
}

// ListServiceSpecificCredentials has no SDK paginator: a page that repeats the
// marker it was sent ends the loop instead of spinning.
func TestScanIAMServiceSpecificCredentials_RepeatedMarkerStops(t *testing.T) {
	st := newTestStore(t)
	c := iamCredCases[0]
	var calls atomic.Int32
	total, err := c.scan(t, st, iamTestUsers("alice"), func(any) (any, error) {
		n := calls.Add(1)
		if n > 3 {
			return nil, iamFailure
		}
		return c.page("alice", ptrStr("same"), "alice-"+strconv.Itoa(int(n))), nil
	})
	if err != nil || total != 2 || calls.Load() != 2 {
		t.Errorf("total = %d, calls = %d, err = %v; want 2 rows from 2 calls, nil", total, calls.Load(), err)
	}
}

// The signing certificate body is a public certificate and is stored verbatim.
func TestScanIAMSigningCertificates_KeepsCertificateBody(t *testing.T) {
	st := newTestStore(t)
	c := iamCredCases[1]
	var calls parentCalls
	if _, err := c.scan(t, st, iamTestUsers("alice"), c.handler(nil, &calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	row := rowsByNativeID(t, st, TypeIAMSigningCertificate)[c.nativeID("alice", "alice-1")]
	if !jsonHasString(t, row.AttributesJSON, "CertificateBody", iamTestCertBody) {
		t.Errorf("attrs = %s, want CertificateBody kept", row.AttributesJSON)
	}
}

// iamStaleUserQueried is what a per-user credential handler returns when asked
// about the user only an older scan stored.
var iamStaleUserQueried = &smithy.GenericAPIError{Code: "StaleUserQueried", Message: "per-user credential op called for a user this scan did not store"}

// iamTopLevelHandlers serves GAAD with users alice and bob, one access key per
// user, and every per-user credential op; every other IAM op is denied. An op
// named in fail returns that error instead.
func iamTopLevelHandlers(fail map[string]error) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"GetAccountAuthorizationDetails": func(any) (any, error) {
			var users []iamtypes.UserDetail
			for _, u := range []string{"alice", "bob"} {
				users = append(users, iamtypes.UserDetail{UserName: ptrStr(u), Arn: ptrStr(iamTestUserARN(u))})
			}
			return &iam.GetAccountAuthorizationDetailsOutput{UserDetailList: users}, nil
		},
		"ListAccessKeys": func(in any) (any, error) {
			user := sv(in.(*iam.ListAccessKeysInput).UserName)
			return &iam.ListAccessKeysOutput{AccessKeyMetadata: []iamtypes.AccessKeyMetadata{{AccessKeyId: ptrStr("AKIA" + user), UserName: &user}}}, nil
		},
	}
	for _, c := range iamCredCases {
		var calls parentCalls
		h[c.op] = c.handler(map[string]error{"stale": iamStaleUserQueried}, &calls)
	}
	for op, err := range fail {
		h[op] = func(any) (any, error) { return nil, err }
	}
	return h
}

// scanIAMTopLevel runs scanIAM over a store that also holds user "stale", which
// only an older scan stored.
func scanIAMTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) error {
	t.Helper()
	insertTestScan(t, st, "older-scan", "completed")
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: testAccountID, Type: TypeIAMUser, NativeID: iamTestUserARN("stale"),
		Name: ptrStr("stale"), Region: regionGlobal, AttributesJSON: "{}", DiscoveredBy: "older-scan",
	}); err != nil {
		t.Fatalf("seed stale user: %v", err)
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, iamDenied)
	_, _, err := scanIAM(context.Background(), acct, st, testScanID)
	return err
}

// scanIAM lists each user's credentials of all three kinds, for the users this
// scan stored only. Every handler returns the same ids for a user, so the rows
// of one kind would merge into another's if their NativeIDs could coincide.
func TestScanIAM_StoresPerUserCredentialsApartPerKind(t *testing.T) {
	st := newTestStore(t)
	if err := scanIAMTopLevel(t, st, iamTopLevelHandlers(nil)); err != nil {
		t.Fatalf("scanIAM: %v", err)
	}
	for _, c := range iamCredCases {
		want := []string{c.nativeID("alice", "alice-1"), c.nativeID("alice", "alice-2"), c.nativeID("bob", "bob-1"), c.nativeID("bob", "bob-2")}
		if got := nativeIDs(rowsByNativeID(t, st, c.typ)); !slices.Equal(got, want) {
			t.Errorf("%s rows = %v, want %v", c.typ, got, want)
		}
		assertContainsNative(t, st, iamTestUserARN("alice"), c.nativeID("alice", "alice-1"))
	}
}

// Each new phase runs after the access-key phase and whatever it or its
// siblings return; the failed phase's error surfaces from scanIAM.
func TestScanIAM_CredentialPhaseFailureKeepsOtherPhases(t *testing.T) {
	failedOps := []string{"ListAccessKeys"}
	for _, c := range iamCredCases {
		failedOps = append(failedOps, c.op)
	}
	for _, failed := range failedOps {
		t.Run(failed, func(t *testing.T) {
			st := newTestStore(t)
			err := scanIAMTopLevel(t, st, iamTopLevelHandlers(map[string]error{failed: iamFailure}))
			assertAPICode(t, err, "ServiceFailure")
			if failed != "ListAccessKeys" {
				keys := rowsByNativeID(t, st, TypeIAMAccessKey)
				for _, u := range []string{"alice", "bob"} {
					if _, ok := keys[iamTestUserARN(u)+"/access-key/AKIA"+u]; !ok {
						t.Errorf("%s's access key missing despite %s failing", u, failed)
					}
				}
			}
			for _, c := range iamCredCases {
				if c.op == failed {
					continue
				}
				if n := len(rowsByNativeID(t, st, c.typ)); n != 4 {
					t.Errorf("%s rows = %d, want 4 despite %s failing", c.typ, n, failed)
				}
			}
		})
	}
}

const iamTestBoundaryARN = "arn:aws:iam::" + testAccountID + ":policy/partner-boundary"

func iamTestDelegationNativeID(id string) string {
	return "arn:aws:iam::" + testAccountID + ":delegation-request/" + id
}

// iamTestDelegationPage builds one ListDelegationRequests page. Request "dr-1"
// carries a state, a create date and a permission boundary; "" is a request
// with no id.
func iamTestDelegationPage(next *string, ids ...string) *iam.ListDelegationRequestsOutput {
	out := &iam.ListDelegationRequestsOutput{Marker: next, IsTruncated: next != nil}
	for _, id := range ids {
		r := iamtypes.DelegationRequest{DelegationRequestId: nonEmptyPtr(id), RequestorName: ptrStr("Partner Co")}
		if id == "dr-1" {
			r.State = iamtypes.StateTypeAccepted
			r.CreateDate = &iamTestDay
			r.RolePermissionRestrictionArns = []string{iamTestBoundaryARN}
		}
		out.DelegationRequests = append(out.DelegationRequests, r)
	}
	return out
}

// iamDelegationHandler serves two pages (dr-1 and an id-less request; then
// dr-2 and another id-less one) and records each call's input.
func iamDelegationHandler(inputs *[]iam.ListDelegationRequestsInput) func(in any) (any, error) {
	return func(in any) (any, error) {
		i := in.(*iam.ListDelegationRequestsInput)
		*inputs = append(*inputs, *i)
		if i.Marker == nil {
			return iamTestDelegationPage(ptrStr("m2"), "dr-1", ""), nil
		}
		return iamTestDelegationPage(nil, "dr-2", ""), nil
	}
}

func scanIAMDelegationWith(t *testing.T, st *store.Store, h func(in any) (any, error)) (int, int, error) {
	t.Helper()
	client := iam.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){"ListDelegationRequests": h}, iamDenied))
	return scanIAMDelegationRequests(context.Background(), client, newTestAccount(testAccountID), st, testScanID)
}

func TestScanIAMDelegationRequests_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "iam:ListDelegationRequests")
	var inputs []iam.ListDelegationRequestsInput
	total, inserted, err := scanIAMDelegationWith(t, st, iamDelegationHandler(&inputs))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the request with no id", *warns)
	}
	rows := rowsByNativeID(t, st, TypeIAMDelegationRequest)
	want := []string{iamTestDelegationNativeID("dr-1"), iamTestDelegationNativeID("dr-2")}
	if got := nativeIDs(rows); total != 2 || inserted != 2 || !slices.Equal(got, want) {
		t.Fatalf("total/inserted = %d/%d, rows = %v; want 2/2, %v", total, inserted, got, want)
	}
	if len(inputs) != 2 || sv(inputs[1].Marker) != "m2" {
		t.Errorf("calls = %+v, want a second call with Marker m2", inputs)
	}
	for _, in := range inputs {
		if in.MaxItems == nil || *in.MaxItems != 1000 {
			t.Errorf("MaxItems = %v, want 1000 (the model's cap)", in.MaxItems)
		}
	}
	first := rows[iamTestDelegationNativeID("dr-1")]
	if sv(first.Name) != "dr-1" || sv(first.Status) != "ACCEPTED" || sv(first.Region) != "global" ||
		sv(first.CreatedAt) != iamTestDay.Format(time.RFC3339) || !jsonHasString(t, first.AttributesJSON, "RequestorName", "Partner Co") {
		t.Errorf("row name/status/region/created = %q/%q/%q/%q, attrs = %s", sv(first.Name), sv(first.Status), sv(first.Region), sv(first.CreatedAt), first.AttributesJSON)
	}
	if second := rows[iamTestDelegationNativeID("dr-2")]; second.Status != nil {
		t.Errorf("dr-2 status = %q, want unset for an absent State", sv(second.Status))
	}
}

func TestScanIAMDelegationRequests_Empty(t *testing.T) {
	st := newTestStore(t)
	total, _, err := scanIAMDelegationWith(t, st, func(any) (any, error) { return iamTestDelegationPage(nil), nil })
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeIAMDelegationRequest)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

func TestScanIAMDelegationRequests_AccessDeniedWarns(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "iam:ListDelegationRequests")
	total, _, err := scanIAMDelegationWith(t, st, func(any) (any, error) { return nil, iamDenied })
	if err != nil || total != 0 || *warns != 1 {
		t.Errorf("total = %d, warnings = %d, err = %v; want 0, 1, nil", total, *warns, err)
	}
}

// Any other error fails the phase, NoSuchEntity included: the model lists it,
// but an unfiltered listing names no entity that could be missing. A failure on
// a later page keeps the rows already stored.
func TestScanIAMDelegationRequests_OtherErrorsFail(t *testing.T) {
	for _, failure := range []*smithy.GenericAPIError{iamFailure, iamGone} {
		t.Run(failure.Code+"/first-page", func(t *testing.T) {
			st := newTestStore(t)
			_, _, err := scanIAMDelegationWith(t, st, func(any) (any, error) { return nil, failure })
			assertAPICode(t, err, failure.Code)
		})
		t.Run(failure.Code+"/second-page", func(t *testing.T) {
			st := newTestStore(t)
			total, _, err := scanIAMDelegationWith(t, st, func(in any) (any, error) {
				if in.(*iam.ListDelegationRequestsInput).Marker == nil {
					return iamTestDelegationPage(ptrStr("m2"), "dr-1"), nil
				}
				return nil, failure
			})
			assertAPICode(t, err, failure.Code)
			if total != 1 || len(rowsByNativeID(t, st, TypeIAMDelegationRequest)) != 1 {
				t.Errorf("total = %d; want page one's row kept", total)
			}
		})
	}
}

// ListDelegationRequests has no SDK paginator: a page repeating the marker it
// was sent fails the phase rather than looping or passing as complete.
func TestScanIAMDelegationRequests_RepeatedMarkerFails(t *testing.T) {
	st := newTestStore(t)
	var calls atomic.Int32
	total, _, err := scanIAMDelegationWith(t, st, func(any) (any, error) {
		n := calls.Add(1)
		if n > 3 {
			return nil, iamFailure
		}
		return iamTestDelegationPage(ptrStr("same"), "dr-"+strconv.Itoa(int(n))), nil
	})
	if !errors.Is(err, errRepeatedNextToken) || total != 2 || calls.Load() != 2 {
		t.Errorf("total = %d, calls = %d, err = %v; want 2 rows from 2 calls, errRepeatedNextToken", total, calls.Load(), err)
	}
}

// The delegation-request phase depends on no other phase: a phase-1 failure
// does not cost it its rows, and its failure costs no other phase its rows.
func TestScanIAM_DelegationRequestsRunLastAndFailAlone(t *testing.T) {
	t.Run("stored despite a phase-1 failure", func(t *testing.T) {
		st := newTestStore(t)
		handlers := iamTopLevelHandlers(map[string]error{"GetAccountAuthorizationDetails": iamFailure})
		var inputs []iam.ListDelegationRequestsInput
		handlers["ListDelegationRequests"] = iamDelegationHandler(&inputs)
		err := scanIAMTopLevel(t, st, handlers)
		if got := classifyServiceError(err); got != outcomeError {
			t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
		}
		assertAPICode(t, err, "ServiceFailure")
		if n := len(rowsByNativeID(t, st, TypeIAMDelegationRequest)); n != 2 {
			t.Errorf("delegation request rows = %d, want 2", n)
		}
	})
	t.Run("its failure keeps the other phases", func(t *testing.T) {
		st := newTestStore(t)
		err := scanIAMTopLevel(t, st, iamTopLevelHandlers(map[string]error{"ListDelegationRequests": iamFailure}))
		assertAPICode(t, err, "ServiceFailure")
		keys := rowsByNativeID(t, st, TypeIAMAccessKey)
		for _, u := range []string{"alice", "bob"} {
			if _, ok := keys[iamTestUserARN(u)+"/access-key/AKIA"+u]; !ok {
				t.Errorf("%s's access key missing", u)
			}
		}
		for _, c := range iamCredCases {
			if n := len(rowsByNativeID(t, st, c.typ)); n != 4 {
				t.Errorf("%s rows = %d, want 4", c.typ, n)
			}
		}
	})
}

// A transient error in one phase is demoted to a warning, so another phase's
// hard error still classifies the service as failed, whichever side throttles.
func TestScanIAM_TransientPhaseDoesNotMaskHardFailure(t *testing.T) {
	throttled := &smithy.GenericAPIError{Code: "Throttling", Message: "Rate exceeded"}
	for _, c := range []struct{ transientOp, hardOp, warnedAs string }{
		{"ListAccessKeys", "ListDelegationRequests", "iam:ListAccessKeys"},
		{"ListDelegationRequests", "ListSSHPublicKeys", "iam:ListDelegationRequests"},
		// A phase-1 error names no member op, so it warns under the service.
		{"GetAccountAuthorizationDetails", "ListDelegationRequests", "iam"},
	} {
		t.Run(c.transientOp, func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, c.warnedAs)
			err := scanIAMTopLevel(t, st, iamTopLevelHandlers(map[string]error{
				c.transientOp: throttled,
				c.hardOp:      iamFailure,
			}))
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			assertAPICode(t, err, "ServiceFailure")
			if *warned != 1 {
				t.Errorf("%s transient warnings = %d, want 1", c.transientOp, *warned)
			}
		})
	}
}
