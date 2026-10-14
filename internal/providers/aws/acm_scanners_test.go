package aws

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/acm"
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	acmDenied   = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: acm:List"}
	acmBadInput = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	acmGone     = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "not found"}
)

const (
	acmTestCert = "arn:aws:acm:us-east-1:123456789012:certificate/c1"
	acmTestEP1  = "arn:aws:acm:us-east-1:123456789012:acme-endpoint/ep1"
	acmTestEP2  = "arn:aws:acm:us-east-1:123456789012:acme-endpoint/ep2"
	acmTestEAB1 = acmTestEP1 + "/acme-external-account-binding/eab1"
	acmTestEAB2 = acmTestEP1 + "/acme-external-account-binding/eab2"
	acmTestDV1  = acmTestEP1 + "/acme-domain-validation/dv1"
	acmTestDV2  = acmTestEP1 + "/acme-domain-validation/dv2"
	acmTestURL1 = "https://acme.example/acct/1"
	acmTestURL2 = "https://acme.example/acct/2"
	acmTestRole = "arn:aws:iam::123456789012:role/acme-clients"
)

var acmTestCreated = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// acmTestAccountID is the expected ACME account NativeID, written out rather
// than built with acmAcmeAccountNativeID so the test pins the shape.
func acmTestAccountID(endpointARN, url string) string {
	return endpointARN + "/acme-account/" + url
}

// acmChildCalls records the endpoint of every call to each endpoint-child op.
type acmChildCalls struct{ bindings, accounts, validations parentCalls }

// acmTopLevelHandlers serves one certificate and the account config, then
// ACME endpoints over two pages (ep1; then ep2 and an element with no ARN).
// Under ep1, each child op returns two pages: one child, then another child
// and an element with no id. Under ep2, only ListAcmeAccounts returns a row:
// an account with ep1's first AccountUrl.
func acmTopLevelHandlers(calls *acmChildCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListCertificates": func(any) (any, error) {
			return &acm.ListCertificatesOutput{CertificateSummaryList: []acmtypes.CertificateSummary{{CertificateArn: ptrStr(acmTestCert)}}}, nil
		},
		"DescribeCertificate": func(in any) (any, error) {
			return &acm.DescribeCertificateOutput{Certificate: &acmtypes.CertificateDetail{CertificateArn: in.(*acm.DescribeCertificateInput).CertificateArn, DomainName: ptrStr("example.com")}}, nil
		},
		"ListTagsForCertificate": func(any) (any, error) { return &acm.ListTagsForCertificateOutput{}, nil },
		"GetAccountConfiguration": func(any) (any, error) {
			return &acm.GetAccountConfigurationOutput{}, nil
		},
		"ListAcmeEndpoints": func(in any) (any, error) {
			if in.(*acm.ListAcmeEndpointsInput).NextToken == nil {
				return &acm.ListAcmeEndpointsOutput{NextToken: ptrStr("p2"), AcmeEndpoints: []acmtypes.AcmeEndpointSummary{{
					AcmeEndpointArn: ptrStr(acmTestEP1), Status: acmtypes.AcmeEndpointStatusActive, CreatedAt: &acmTestCreated,
					EndpointUrl: ptrStr("https://acme.example/ep1"),
					CertificateAuthority: &acmtypes.CertificateAuthorityMemberPublicCertificateAuthority{Value: acmtypes.PublicCertificateAuthority{
						AllowedKeyAlgorithms: []acmtypes.PublicKeyAlgorithm{acmtypes.PublicKeyAlgorithmRsa2048},
					}},
				}}}, nil
			}
			return &acm.ListAcmeEndpointsOutput{AcmeEndpoints: []acmtypes.AcmeEndpointSummary{
				{
					AcmeEndpointArn:      ptrStr(acmTestEP2),
					CertificateAuthority: &acmtypes.UnknownUnionMember{Tag: "futureCertificateAuthority", Value: []byte(`{"x":1}`)},
				},
				{EndpointUrl: ptrStr("https://acme.example/no-arn")},
			}}, nil
		},
		"ListAcmeExternalAccountBindings": func(in any) (any, error) {
			p := in.(*acm.ListAcmeExternalAccountBindingsInput)
			ep := sv(p.AcmeEndpointArn)
			calls.bindings.add(ep)
			switch {
			case ep != acmTestEP1:
				return &acm.ListAcmeExternalAccountBindingsOutput{}, nil
			case p.NextToken == nil:
				return &acm.ListAcmeExternalAccountBindingsOutput{NextToken: ptrStr("p2"), ExternalAccountBindings: []acmtypes.AcmeExternalAccountBindingSummary{
					{
						AcmeExternalAccountBindingArn: ptrStr(acmTestEAB1), AcmeEndpointArn: ptrStr(acmTestEP1), RoleArn: ptrStr(acmTestRole),
						CreatedAt: &acmTestCreated, ExpiresAt: &acmTestCreated,
					},
				}}, nil
			default:
				return &acm.ListAcmeExternalAccountBindingsOutput{ExternalAccountBindings: []acmtypes.AcmeExternalAccountBindingSummary{
					{AcmeExternalAccountBindingArn: ptrStr(acmTestEAB2), AcmeEndpointArn: ptrStr(acmTestEP1), RevokedAt: &acmTestCreated, ExpiresAt: &acmTestCreated},
					{RoleArn: ptrStr(acmTestRole)},
				}}, nil
			}
		},
		"ListAcmeAccounts": func(in any) (any, error) {
			p := in.(*acm.ListAcmeAccountsInput)
			ep := sv(p.AcmeEndpointArn)
			calls.accounts.add(ep)
			switch {
			case ep != acmTestEP1:
				return &acm.ListAcmeAccountsOutput{AcmeAccounts: []acmtypes.AcmeAccountSummary{{AccountUrl: ptrStr(acmTestURL1)}}}, nil
			case p.NextToken == nil:
				return &acm.ListAcmeAccountsOutput{NextToken: ptrStr("p2"), AcmeAccounts: []acmtypes.AcmeAccountSummary{{
					AccountUrl: ptrStr(acmTestURL1), AcmeExternalAccountBindingArn: ptrStr(acmTestEAB1), Status: acmtypes.AcmeAccountStatusValid,
					CreatedAt: &acmTestCreated, PublicKeyThumbprint: ptrStr("thumb-1"),
				}}}, nil
			default:
				return &acm.ListAcmeAccountsOutput{AcmeAccounts: []acmtypes.AcmeAccountSummary{
					{AccountUrl: ptrStr(acmTestURL2)},
					{PublicKeyThumbprint: ptrStr("no-url")},
				}}, nil
			}
		},
		"ListAcmeDomainValidations": func(in any) (any, error) {
			p := in.(*acm.ListAcmeDomainValidationsInput)
			ep := sv(p.AcmeEndpointArn)
			calls.validations.add(ep)
			switch {
			case ep != acmTestEP1:
				return &acm.ListAcmeDomainValidationsOutput{}, nil
			case p.NextToken == nil:
				return &acm.ListAcmeDomainValidationsOutput{NextToken: ptrStr("p2"), AcmeDomainValidations: []acmtypes.AcmeDomainValidationSummary{{
					AcmeDomainValidationArn: ptrStr(acmTestDV1), DomainName: ptrStr("example.com"), Status: acmtypes.AcmeDomainValidationStatusValid,
					CreatedAt:            &acmTestCreated,
					PrevalidationDetails: &acmtypes.PrevalidationDetailsMemberDnsPrevalidation{Value: acmtypes.DnsPrevalidationDetails{HostedZoneId: ptrStr("Z1")}},
				}}}, nil
			default:
				return &acm.ListAcmeDomainValidationsOutput{AcmeDomainValidations: []acmtypes.AcmeDomainValidationSummary{
					{
						AcmeDomainValidationArn: ptrStr(acmTestDV2),
						PrevalidationDetails:    &acmtypes.UnknownUnionMember{Tag: "futurePrevalidation", Value: []byte(`{"y":2}`)},
					},
					{DomainName: ptrStr("no-arn.example")},
				}}, nil
			}
		},
	}
}

func scanACMTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, acmDenied)
	total, _, err := scanACM(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

// unionMembers decodes the object under key in attrs into its member keys.
func unionMembers(t *testing.T, attrs, key string) map[string]json.RawMessage {
	t.Helper()
	var top map[string]json.RawMessage
	var members map[string]json.RawMessage
	if err := json.Unmarshal([]byte(attrs), &top); err != nil {
		t.Fatalf("attrs %s: %v", attrs, err)
	}
	if err := json.Unmarshal(top[key], &members); err != nil {
		t.Fatalf("attrs %s key %s: %v", attrs, key, err)
	}
	return members
}

func assertUnknownMember(t *testing.T, members map[string]json.RawMessage, tag, value string) {
	t.Helper()
	var member acmtypes.UnknownUnionMember
	if err := json.Unmarshal(members["UnknownMember"], &member); err != nil || len(members) != 1 || member.Tag != tag || string(member.Value) != value {
		t.Errorf("union = %s (decode err %v), want only UnknownMember %s %s", members, err, tag, value)
	}
}

func TestScanACM_StoresAcmeEndpointsAndChildren(t *testing.T) {
	st := newTestStore(t)
	var calls acmChildCalls
	total, err := scanACMTopLevel(t, st, acmTopLevelHandlers(&calls))
	if err != nil {
		t.Fatalf("scanACM: %v", err)
	}
	// certificate + account config + 2 endpoints + 2 bindings + 3 accounts + 2 validations
	if total != 11 {
		t.Errorf("total = %d, want 11", total)
	}
	for op, c := range map[string]*parentCalls{"bindings": &calls.bindings, "accounts": &calls.accounts, "validations": &calls.validations} {
		if got := c.distinct(); !slices.Equal(got, []string{acmTestEP1, acmTestEP2}) {
			t.Errorf("%s listed for %v, want [%s %s]", op, got, acmTestEP1, acmTestEP2)
		}
	}

	eps := rowsByNativeID(t, st, TypeACMAcmeEndpoint)
	if got := nativeIDs(eps); !slices.Equal(got, []string{acmTestEP1, acmTestEP2}) {
		t.Fatalf("endpoint rows = %v, want [%s %s]", got, acmTestEP1, acmTestEP2)
	}
	ep1 := eps[acmTestEP1]
	if sv(ep1.Status) != "ACTIVE" || sv(ep1.Region) != testRegion || sv(ep1.CreatedAt) != "2026-01-02T03:04:05Z" ||
		!jsonHasString(t, ep1.AttributesJSON, "EndpointUrl", "https://acme.example/ep1") {
		t.Errorf("ep1 status/region/created = %q/%q/%q, attrs = %s", sv(ep1.Status), sv(ep1.Region), sv(ep1.CreatedAt), ep1.AttributesJSON)
	}
	if eps[acmTestEP2].Status != nil {
		t.Errorf("ep2 status = %q, want unset for an absent Status", sv(eps[acmTestEP2].Status))
	}
	ca := unionMembers(t, ep1.AttributesJSON, "CertificateAuthority")
	if got := string(ca["PublicCertificateAuthority"]); got != `{"AllowedKeyAlgorithms":["RSA_2048"]}` || len(ca) != 1 {
		t.Errorf("ep1 CertificateAuthority = %s", ep1.AttributesJSON)
	}
	assertUnknownMember(t, unionMembers(t, eps[acmTestEP2].AttributesJSON, "CertificateAuthority"), "futureCertificateAuthority", `{"x":1}`)

	eabs := rowsByNativeID(t, st, TypeACMAcmeExternalAccountBinding)
	if got := nativeIDs(eabs); !slices.Equal(got, []string{acmTestEAB1, acmTestEAB2}) {
		t.Fatalf("binding rows = %v, want [%s %s]", got, acmTestEAB1, acmTestEAB2)
	}
	eab1 := eabs[acmTestEAB1]
	if sv(eab1.Region) != testRegion || sv(eab1.CreatedAt) != "2026-01-02T03:04:05Z" || !jsonHasString(t, eab1.AttributesJSON, "RoleArn", acmTestRole) {
		t.Errorf("eab1 region/created = %q/%q, attrs = %s", sv(eab1.Region), sv(eab1.CreatedAt), eab1.AttributesJSON)
	}
	// Both bindings expired (ExpiresAt is in the past); only eab2 was revoked.
	if eab1.Status != nil || sv(eabs[acmTestEAB2].Status) != "revoked" {
		t.Errorf("binding status eab1/eab2 = %q/%q, want unset/revoked", sv(eab1.Status), sv(eabs[acmTestEAB2].Status))
	}

	acct1, acct2 := acmTestAccountID(acmTestEP1, acmTestURL1), acmTestAccountID(acmTestEP1, acmTestURL2)
	acct1EP2 := acmTestAccountID(acmTestEP2, acmTestURL1)
	accounts := rowsByNativeID(t, st, TypeACMAcmeAccount)
	want := []string{acct1, acct2, acct1EP2}
	sort.Strings(want)
	if got := nativeIDs(accounts); !slices.Equal(got, want) {
		t.Fatalf("account rows = %v, want %v", got, want)
	}
	a1 := accounts[acct1]
	if sv(a1.Status) != "VALID" || sv(a1.Region) != testRegion || sv(a1.CreatedAt) != "2026-01-02T03:04:05Z" ||
		!jsonHasString(t, a1.AttributesJSON, "AcmeExternalAccountBindingArn", acmTestEAB1) || !jsonHasString(t, a1.AttributesJSON, "PublicKeyThumbprint", "thumb-1") {
		t.Errorf("account1 status/region/created = %q/%q/%q, attrs = %s", sv(a1.Status), sv(a1.Region), sv(a1.CreatedAt), a1.AttributesJSON)
	}
	if accounts[acct2].Status != nil {
		t.Errorf("account2 status = %q, want unset for an absent Status", sv(accounts[acct2].Status))
	}

	dvs := rowsByNativeID(t, st, TypeACMAcmeDomainValidation)
	if got := nativeIDs(dvs); !slices.Equal(got, []string{acmTestDV1, acmTestDV2}) {
		t.Fatalf("validation rows = %v, want [%s %s]", got, acmTestDV1, acmTestDV2)
	}
	dv1 := dvs[acmTestDV1]
	if sv(dv1.Name) != "example.com" || sv(dv1.Status) != "VALID" || sv(dv1.Region) != testRegion || sv(dv1.CreatedAt) != "2026-01-02T03:04:05Z" {
		t.Errorf("dv1 name/status/region/created = %q/%q/%q/%q", sv(dv1.Name), sv(dv1.Status), sv(dv1.Region), sv(dv1.CreatedAt))
	}
	if dvs[acmTestDV2].Status != nil {
		t.Errorf("dv2 status = %q, want unset for an absent Status", sv(dvs[acmTestDV2].Status))
	}
	pre := unionMembers(t, dv1.AttributesJSON, "PrevalidationDetails")
	if got := string(pre["DnsPrevalidation"]); got != `{"DomainScope":null,"HostedZoneId":"Z1","ResourceRecord":null}` || len(pre) != 1 {
		t.Errorf("dv1 PrevalidationDetails = %s", dv1.AttributesJSON)
	}
	assertUnknownMember(t, unionMembers(t, dvs[acmTestDV2].AttributesJSON, "PrevalidationDetails"), "futurePrevalidation", `{"y":2}`)

	for _, pair := range [][2]string{
		{acmTestEP1, acmTestEAB1},
		{acmTestEP1, acmTestEAB2},
		{acmTestEP1, acct1},
		{acmTestEP1, acct2},
		{acmTestEP2, acct1EP2},
		{acmTestEP1, acmTestDV1},
		{acmTestEP1, acmTestDV2},
	} {
		assertContainsNative(t, st, pair[0], pair[1])
	}
	assertContainsEdgesDeclared(t, st)
}

// One AccountUrl under two endpoints is two accounts; the endpoint ARN in the
// NativeID keeps their identities apart.
func TestScanACM_SameAccountURLUnderTwoEndpointsKeepsTwoIdentities(t *testing.T) {
	st := newTestStore(t)
	var calls acmChildCalls
	if _, err := scanACMTopLevel(t, st, acmTopLevelHandlers(&calls)); err != nil {
		t.Fatalf("scanACM: %v", err)
	}
	accounts := rowsByNativeID(t, st, TypeACMAcmeAccount)
	onEP1, onEP2 := accounts[acmTestAccountID(acmTestEP1, acmTestURL1)], accounts[acmTestAccountID(acmTestEP2, acmTestURL1)]
	if onEP1.ID == "" || onEP2.ID == "" || onEP1.ID == onEP2.ID || sv(onEP1.Status) != "VALID" || onEP2.Status != nil {
		t.Errorf("ep1 account = %s/%q, ep2 account = %s/%q; want two rows with their own status", onEP1.ID, sv(onEP1.Status), onEP2.ID, sv(onEP2.Status))
	}
}

func TestScanACM_NoAcmeEndpointsNoChildCalls(t *testing.T) {
	st := newTestStore(t)
	var calls acmChildCalls
	h := acmTopLevelHandlers(&calls)
	h["ListAcmeEndpoints"] = func(any) (any, error) { return &acm.ListAcmeEndpointsOutput{}, nil }
	total, err := scanACMTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scanACM: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if n := len(calls.bindings.distinct()) + len(calls.accounts.distinct()) + len(calls.validations.distinct()); n != 0 {
		t.Errorf("child calls = %d, want none", n)
	}
	for _, typ := range []string{TypeACMAcmeEndpoint, TypeACMAcmeExternalAccountBinding, TypeACMAcmeAccount, TypeACMAcmeDomainValidation} {
		if n := len(rowsByNativeID(t, st, typ)); n != 0 {
			t.Errorf("%s rows = %d, want 0", typ, n)
		}
	}
}

// A denial on the endpoints' second page warns once and keeps page one's
// endpoint and its children; any other error fails the scan.
func TestScanACM_FailedAcmeEndpointsPage(t *testing.T) {
	failPageTwo := func(next func(in any) (any, error), err error) func(in any) (any, error) {
		return func(in any) (any, error) {
			if in.(*acm.ListAcmeEndpointsInput).NextToken != nil {
				return nil, err
			}
			return next(in)
		}
	}
	t.Run("denied", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "acm:ListAcmeEndpoints")
		var calls acmChildCalls
		h := acmTopLevelHandlers(&calls)
		h["ListAcmeEndpoints"] = failPageTwo(h["ListAcmeEndpoints"], acmDenied)
		total, err := scanACMTopLevel(t, st, h)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if *warns != 1 {
			t.Errorf("warnings = %d, want 1", *warns)
		}
		if got := nativeIDs(rowsByNativeID(t, st, TypeACMAcmeEndpoint)); !slices.Equal(got, []string{acmTestEP1}) {
			t.Errorf("endpoint rows = %v, want [%s]", got, acmTestEP1)
		}
		if got := calls.bindings.distinct(); !slices.Equal(got, []string{acmTestEP1}) {
			t.Errorf("bindings listed for %v, want [%s]", got, acmTestEP1)
		}
		// certificate + account config + 1 endpoint + 2 bindings + 2 accounts + 2 validations
		if total != 9 {
			t.Errorf("total = %d, want 9", total)
		}
	})
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		var calls acmChildCalls
		h := acmTopLevelHandlers(&calls)
		h["ListAcmeEndpoints"] = failPageTwo(h["ListAcmeEndpoints"], acmBadInput)
		_, err := scanACMTopLevel(t, st, h)
		assertAPICode(t, err, "ValidationException")
		if n := len(rowsByNativeID(t, st, TypeACMAcmeEndpoint)); n != 0 {
			t.Errorf("endpoint rows = %d, want 0", n)
		}
	})
}

// A failed endpoint drops only its own children of that op: deleted since
// listing is silent, a denial warns once however many endpoints hit it, and
// any other error fails the scan.
func TestScanACM_FailedChildEndpoint(t *testing.T) {
	failEndpoints := func(next func(in any) (any, error), err error) func(in any) (any, error) {
		return func(in any) (any, error) {
			var ep string
			switch p := in.(type) {
			case *acm.ListAcmeExternalAccountBindingsInput:
				ep = sv(p.AcmeEndpointArn)
			case *acm.ListAcmeAccountsInput:
				ep = sv(p.AcmeEndpointArn)
			case *acm.ListAcmeDomainValidationsInput:
				ep = sv(p.AcmeEndpointArn)
			}
			if ep == acmTestEP1 || ep == acmTestEP2 {
				return nil, err
			}
			return next(in)
		}
	}
	for _, tc := range []struct {
		op, childType string
		lost          int
	}{
		{"ListAcmeExternalAccountBindings", TypeACMAcmeExternalAccountBinding, 2},
		{"ListAcmeAccounts", TypeACMAcmeAccount, 3},
		{"ListAcmeDomainValidations", TypeACMAcmeDomainValidation, 2},
	} {
		for _, ec := range []struct {
			name     string
			err      error
			wantWarn int
		}{
			{"endpoint gone", acmGone, 0},
			{"denied", acmDenied, 1},
		} {
			t.Run(tc.op+"/"+ec.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "acm:"+tc.op)
				var calls acmChildCalls
				h := acmTopLevelHandlers(&calls)
				h[tc.op] = failEndpoints(h[tc.op], ec.err)
				total, err := scanACMTopLevel(t, st, h)
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if *warns != ec.wantWarn {
					t.Errorf("warnings = %d, want %d", *warns, ec.wantWarn)
				}
				if n := len(rowsByNativeID(t, st, tc.childType)); n != 0 || total != 11-tc.lost {
					t.Errorf("%s rows = %d, total = %d; want 0 and %d", tc.childType, n, total, 11-tc.lost)
				}
			})
		}
		t.Run(tc.op+"/other", func(t *testing.T) {
			st := newTestStore(t)
			var calls acmChildCalls
			h := acmTopLevelHandlers(&calls)
			h[tc.op] = failEndpoints(h[tc.op], acmBadInput)
			_, err := scanACMTopLevel(t, st, h)
			assertAPICode(t, err, "ValidationException")
		})
	}
}

// Each ACME op runs after the certificate and account-config phases, its hard
// error reaches the caller, and the returned total keeps the rows the earlier
// phases stored.
func TestScanACM_AcmePhaseErrorAfterExistingPhases(t *testing.T) {
	for _, tc := range []struct {
		op        string
		wantTotal int
	}{
		{"ListAcmeEndpoints", 2},               // certificate + account config
		{"ListAcmeExternalAccountBindings", 4}, // + 2 endpoints
		{"ListAcmeAccounts", 6},                // + 2 bindings
		{"ListAcmeDomainValidations", 9},       // + 3 accounts
	} {
		op := tc.op
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			var calls acmChildCalls
			h := acmTopLevelHandlers(&calls)
			var failed atomic.Bool
			h[op] = func(any) (any, error) {
				failed.Store(true)
				return nil, acmBadInput
			}
			total, err := scanACMTopLevel(t, st, h)
			if !failed.Load() {
				t.Fatalf("%s never called", op)
			}
			assertAPICode(t, err, "ValidationException")
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
			if len(rowsByNativeID(t, st, TypeACMCertificate)) != 1 || len(rowsByNativeID(t, st, TypeACMAccount)) != 1 {
				t.Errorf("certificate/account rows missing: %s ran before a pre-existing phase", op)
			}
		})
	}
}

// A phase that fails after storing rows still counts them: the certificate on
// page one is in the total returned with page two's error.
func TestScanACM_FailedPhaseKeepsItsPartialCount(t *testing.T) {
	st := newTestStore(t)
	var calls acmChildCalls
	h := acmTopLevelHandlers(&calls)
	certs := h["ListCertificates"]
	h["ListCertificates"] = func(in any) (any, error) {
		if in.(*acm.ListCertificatesInput).NextToken != nil {
			return nil, acmBadInput
		}
		out, err := certs(in)
		out.(*acm.ListCertificatesOutput).NextToken = ptrStr("p2")
		return out, err
	}
	total, err := scanACMTopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	if total != 1 || len(rowsByNativeID(t, st, TypeACMCertificate)) != 1 {
		t.Errorf("total = %d, certificate rows = %d; want 1 and 1", total, len(rowsByNativeID(t, st, TypeACMCertificate)))
	}
}

// The client is pinned to the scanned region, not the config's default.
func TestScanACM_CallsGoToScannedRegion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var calls acmChildCalls
	var regions parentCalls
	cfg := opStubConfig(acmTopLevelHandlers(&calls), acmDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	if _, _, err := scanACM(context.Background(), acct, "eu-west-1", st, testScanID); err != nil {
		t.Fatalf("scanACM: %v", err)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
	for _, typ := range []string{TypeACMAcmeExternalAccountBinding, TypeACMAcmeAccount, TypeACMAcmeDomainValidation} {
		rows := rowsByNativeID(t, st, typ)
		if len(rows) == 0 {
			t.Errorf("%s: no rows stored", typ)
		}
		for id, r := range rows {
			if sv(r.Region) != "eu-west-1" {
				t.Errorf("%s region = %q, want eu-west-1", id, sv(r.Region))
			}
		}
	}
}
