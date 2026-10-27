package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awsmw "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	sesv2types "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/aws/smithy-go"
	smithymw "github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/icearp/disco-cli/store"
)

const (
	sesTestDomain    = "example.com"
	sesTestAddress   = "ops@example.org"
	sesTestCertARN1  = "arn:aws:acm:us-east-1:123456789012:certificate/11111111-1111-1111-1111-111111111111"
	sesTestCertARN2  = "arn:aws:acm:us-east-1:123456789012:certificate/22222222-2222-2222-2222-222222222222"
	sesTestDomainARN = "arn:aws:ses:us-east-1:123456789012:identity/" + sesTestDomain
	sesTestAddrARN   = "arn:aws:ses:us-east-1:123456789012:identity/" + sesTestAddress
	sesTestAlice     = sesTestDomainARN + "/certificate/alice@example.com"
	sesTestBob       = sesTestDomainARN + "/certificate/bob@example.com"
	sesTestOps       = sesTestAddrARN + "/certificate/" + sesTestAddress
)

var (
	sesDenied   = apiErr("AccessDeniedException", "User: arn:aws:sts::123456789012:assumed-role/disco/scan is not authorized to perform: ses:ListEmailIdentityCertificates")
	sesBadInput = apiErr("BadRequestException", "bad input")
	// sesNotFound404 is a NotFoundException with the HTTP 404 it arrives with,
	// which isOpNotDeployed alone would read as a region gap.
	sesNotFound404 = &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 404}},
		Err:      apiErr("NotFoundException", "identity not found"),
	}
	// sesGatewayNotFound is the gateway's NotFoundException for a region that
	// does not offer the operation (isServiceNotAvailableInRegion).
	sesGatewayNotFound = apiErr("NotFoundException", "Unable to determine service/operation name to be authorized")
)

// sesTestIdentities are the parents the identity phase hands the certificate
// phase for the two identities sesCertHandlers lists.
var sesTestIdentities = []sesIdentity{
	{childParent: childParent{id: sesTestDomain, arn: sesTestDomainARN}},
	{childParent: childParent{id: sesTestAddress, arn: sesTestAddrARN}, emailAddress: true},
}

// sesCertHandlers serves two identities: the domain lists its certificates
// over two pages (alice; then bob plus one without a FromAddress), the address
// one certificate without a FromAddress. Every ListEmailIdentityCertificates call records its
// identity in calls; errFor, when it returns non-nil for an identity, fails
// that identity's calls.
func sesCertHandlers(calls *parentCalls, errFor func(identity string) error) map[string]func(in any) (any, error) {
	expiry := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	return map[string]func(in any) (any, error){
		"ListEmailIdentities": func(any) (any, error) {
			return &sesv2.ListEmailIdentitiesOutput{EmailIdentities: []sesv2types.IdentityInfo{
				{IdentityName: ptrStr(sesTestDomain), IdentityType: sesv2types.IdentityTypeDomain},
				{IdentityName: ptrStr(sesTestAddress), IdentityType: sesv2types.IdentityTypeEmailAddress},
			}}, nil
		},
		"GetEmailIdentity": func(in any) (any, error) {
			identityType := sesv2types.IdentityTypeDomain
			if sv(in.(*sesv2.GetEmailIdentityInput).EmailIdentity) == sesTestAddress {
				identityType = sesv2types.IdentityTypeEmailAddress
			}
			return &sesv2.GetEmailIdentityOutput{IdentityType: identityType, VerificationStatus: sesv2types.VerificationStatusSuccess}, nil
		},
		"ListEmailIdentityCertificates": func(in any) (any, error) {
			p := in.(*sesv2.ListEmailIdentityCertificatesInput)
			identity := sv(p.EmailIdentity)
			calls.add(identity)
			if err := errFor(identity); err != nil {
				return nil, err
			}
			switch {
			case identity == sesTestAddress:
				return &sesv2.ListEmailIdentityCertificatesOutput{Certificates: []sesv2types.IdentityCertificate{
					// No FromAddress: an email-address identity's association applies to the identity.
					{CertificateArn: ptrStr(sesTestCertARN2), Status: sesv2types.IdentityCertificateStatusProvisioning},
				}}, nil
			case p.NextToken == nil:
				return &sesv2.ListEmailIdentityCertificatesOutput{NextToken: ptrStr("p2"), Certificates: []sesv2types.IdentityCertificate{{
					FromAddress: ptrStr("alice@example.com"), CertificateArn: ptrStr(sesTestCertARN1),
					Status: sesv2types.IdentityCertificateStatusActive, CertificateExpiryTime: &expiry,
				}}}, nil
			default:
				return &sesv2.ListEmailIdentityCertificatesOutput{Certificates: []sesv2types.IdentityCertificate{
					{FromAddress: ptrStr("bob@example.com"), CertificateArn: ptrStr(sesTestCertARN1)},
					{CertificateArn: ptrStr(sesTestCertARN1)},
				}}, nil
			}
		},
	}
}

func noSESCertErr(string) error { return nil }

// opLog records the name of every SDK call a config makes, in order.
type opLog struct {
	mu  sync.Mutex
	ops []string
}

func (l *opLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.ops)
}

// recordOps is an API option that logs every call's operation name; it goes
// ahead of the config's stubs, which short-circuit the stack.
func recordOps(log *opLog) func(*smithymw.Stack) error {
	return func(s *smithymw.Stack) error {
		return s.Initialize.Add(smithymw.InitializeMiddlewareFunc("discoRecordOp",
			func(ctx context.Context, in smithymw.InitializeInput, next smithymw.InitializeHandler) (smithymw.InitializeOutput, smithymw.Metadata, error) {
				log.mu.Lock()
				log.ops = append(log.ops, awsmw.GetOperationName(ctx))
				log.mu.Unlock()
				return next.HandleInitialize(ctx, in)
			}), smithymw.After)
	}
}

// scanSESTopLevel runs scanSES with every op handlers lacks denied.
func scanSESTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error), log *opLog) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, sesDenied)
	acct.cfg.APIOptions = append([]func(*smithymw.Stack) error{recordOps(log)}, acct.cfg.APIOptions...)
	return scanSES(context.Background(), acct, testRegion, st, testScanID)
}

func scanSESCerts(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error), identities []sesIdentity) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	client := sesv2.NewFromConfig(opStubConfig(handlers, sesDenied))
	return scanSESEmailIdentityCertificates(context.Background(), client, acct, testRegion, st, testScanID, identities)
}

// scanSES lists certificates for every stored identity, after every older
// phase, and stores each under its identity.
func TestScanSES_StoresCertificatesOfEveryIdentityLast(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, sesCertificateOp)
	var calls parentCalls
	var log opLog
	total, inserted, err := scanSESTopLevel(t, st, sesCertHandlers(&calls, noSESCertErr), &log)
	if err != nil {
		t.Fatalf("scanSES: %v", err)
	}
	// 2 identities + 3 certificates; every other phase is denied.
	if total != 5 || inserted != 5 {
		t.Errorf("total, inserted = %d, %d; want 5, 5", total, inserted)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{sesTestDomain, sesTestAddress}) {
		t.Errorf("certificates listed for %v, want [%s %s]", got, sesTestDomain, sesTestAddress)
	}
	ops := log.snapshot()
	first := slices.Index(ops, "ListEmailIdentityCertificates")
	if first < 0 || slices.ContainsFunc(ops[first:], func(op string) bool { return op != "ListEmailIdentityCertificates" }) {
		t.Errorf("call order %v: every certificate call must follow every other phase's calls", ops)
	}

	rows := rowsByNativeID(t, st, TypeSESEmailIdentityCertificate)
	if got := nativeIDs(rows); !slices.Equal(got, []string{sesTestAlice, sesTestBob, sesTestOps}) {
		t.Fatalf("certificate rows = %v, want [%s %s %s]", got, sesTestAlice, sesTestBob, sesTestOps)
	}
	alice := rows[sesTestAlice]
	if alice.Type != TypeSESEmailIdentityCertificate || sv(alice.Name) != "alice@example.com" || sv(alice.Region) != testRegion || sv(alice.Status) != "ACTIVE" {
		t.Errorf("alice type/name/region/status = %q/%q/%q/%q, want %s/alice@example.com/%s/ACTIVE",
			alice.Type, sv(alice.Name), sv(alice.Region), sv(alice.Status), TypeSESEmailIdentityCertificate, testRegion)
	}
	if bob := rows[sesTestBob]; bob.Status != nil {
		t.Errorf("bob status = %q, want unset for an empty Status", sv(bob.Status))
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(alice.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs %s: %v", alice.AttributesJSON, err)
	}
	if attrs["CertificateArn"] != sesTestCertARN1 || attrs["CertificateExpiryTime"] != "2027-01-02T03:04:05Z" {
		t.Errorf("alice attrs CertificateArn/CertificateExpiryTime = %v/%v, want %s/2027-01-02T03:04:05Z",
			attrs["CertificateArn"], attrs["CertificateExpiryTime"], sesTestCertARN1)
	}
	assertContainsNative(t, st, sesTestDomainARN, sesTestAlice)
	assertContainsNative(t, st, sesTestDomainARN, sesTestBob)
	assertContainsNative(t, st, sesTestAddrARN, sesTestOps)
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the association without a FromAddress", *warns)
	}
}

// A hard certificate error is returned after every older phase stored its
// rows, and reaches the dispatcher as a hard error.
func TestScanSES_CertificateErrorKeepsOlderPhases(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	var log opLog
	h := sesCertHandlers(&calls, func(string) error { return sesBadInput })
	total, _, err := scanSESTopLevel(t, st, h, &log)
	assertAPICode(t, err, "BadRequestException")
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v, want outcomeError", got)
	}
	if total != 2 || len(rowsByNativeID(t, st, TypeSESEmailIdentity)) != 2 {
		t.Errorf("total = %d, identity rows = %d; want both identities kept", total, len(rowsByNativeID(t, st, TypeSESEmailIdentity)))
	}
}

// Each per-identity failure is dropped or returned per its kind, and a
// dropped identity costs its sibling none of its rows.
func TestScanSESEmailIdentityCertificates_ErrorLadder(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failed    []string
		err       error
		wantCode  string
		wantRows  []string
		wantWarns int
	}{
		{"identity deleted since listing", []string{sesTestAddress}, apiErr("NotFoundException", "gone"), "", []string{sesTestAlice, sesTestBob}, 1},
		{"first identity deleted, as the 404 the service sends", []string{sesTestDomain}, sesNotFound404, "", []string{sesTestOps}, 0},
		{"op not deployed in region", []string{sesTestDomain, sesTestAddress}, apiErr("UnknownOperationException", "unknown"), "", nil, 0},
		// One warning for the refused sibling, one for the domain's association without a FromAddress.
		{"op refused after the first identity succeeded", []string{sesTestAddress}, apiErr("UnknownOperationException", "unknown"), "", []string{sesTestAlice, sesTestBob}, 2},
		{"gateway NotFound is a region gap", []string{sesTestDomain, sesTestAddress}, sesGatewayNotFound, "", nil, 0},
		{"denied warns once", []string{sesTestDomain, sesTestAddress}, sesDenied, "", nil, 1},
		{"denied identity keeps sibling", []string{sesTestDomain}, sesDenied, "", []string{sesTestOps}, 1},
		{"throttled identity keeps sibling", []string{sesTestAddress}, apiErr("TooManyRequestsException", "slow down"), "", []string{sesTestAlice, sesTestBob}, 2},
		{"bad request fails the phase", []string{sesTestAddress}, sesBadInput, "BadRequestException", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			seedParentRows(t, st, TypeSESEmailIdentity, sesTestDomainARN, sesTestAddrARN)
			warns := countOpWarnings(st, sesCertificateOp)
			var calls parentCalls
			h := sesCertHandlers(&calls, func(identity string) error {
				if slices.Contains(tc.failed, identity) {
					return tc.err
				}
				return nil
			})
			total, _, err := scanSESCerts(t, st, h, sesTestIdentities)
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
				if isTransientNetworkError(err) {
					t.Errorf("err = %v reads as transient, want a hard error", err)
				}
				// A failed phase reports none of its skip tallies.
				if *warns != tc.wantWarns {
					t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeSESEmailIdentityCertificate)); !slices.Equal(got, tc.wantRows) {
				t.Errorf("rows = %v, want %v", got, tc.wantRows)
			}
			if total != len(tc.wantRows) {
				t.Errorf("total = %d, want %d", total, len(tc.wantRows))
			}
			// The domain's association without a FromAddress warns too
			// whenever the domain is listed.
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
		})
	}
}

func TestScanSESEmailIdentityCertificates_Empty(t *testing.T) {
	for _, tc := range []struct {
		name       string
		identities []sesIdentity
		wantCalls  int
	}{
		{"no identities", nil, 0},
		{"identity without certificates", sesTestIdentities[:1], 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			h := map[string]func(in any) (any, error){
				"ListEmailIdentityCertificates": func(in any) (any, error) {
					calls.add(sv(in.(*sesv2.ListEmailIdentityCertificatesInput).EmailIdentity))
					return &sesv2.ListEmailIdentityCertificatesOutput{}, nil
				},
			}
			total, inserted, err := scanSESCerts(t, st, h, tc.identities)
			if err != nil || total != 0 || inserted != 0 {
				t.Errorf("total, inserted, err = %d, %d, %v; want 0, 0, nil", total, inserted, err)
			}
			if len(calls.ids) != tc.wantCalls {
				t.Errorf("calls = %v, want %d", calls.ids, tc.wantCalls)
			}
		})
	}
}

// A service that repeats its NextToken ends the identity's listing instead of
// looping; the stub fails after a few calls so a regression cannot hang.
func TestScanSESEmailIdentityCertificates_StopsOnRepeatedToken(t *testing.T) {
	st := newTestStore(t)
	seedParentRows(t, st, TypeSESEmailIdentity, sesTestDomainARN)
	var n int
	h := map[string]func(in any) (any, error){
		"ListEmailIdentityCertificates": func(any) (any, error) {
			n++
			if n > 5 {
				return nil, errors.New("pagination did not stop on a repeated token")
			}
			return &sesv2.ListEmailIdentityCertificatesOutput{NextToken: ptrStr("same"), Certificates: []sesv2types.IdentityCertificate{
				{FromAddress: ptrStr(fmt.Sprintf("user%d@example.com", n))},
			}}, nil
		},
	}
	total, _, err := scanSESCerts(t, st, h, sesTestIdentities[:1])
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if n != 2 || total != 2 {
		t.Errorf("calls, total = %d, %d; want 2, 2", n, total)
	}
}

// An address with a DEPROVISIONING association and its replacement stores the
// replacement once, whichever the service lists first.
func TestScanSESEmailIdentityCertificates_ReplacementBeatsDeprovisioning(t *testing.T) {
	old := sesv2types.IdentityCertificate{FromAddress: ptrStr("alice@example.com"), CertificateArn: ptrStr(sesTestCertARN1), Status: sesv2types.IdentityCertificateStatusDeprovisioning}
	replacement := sesv2types.IdentityCertificate{FromAddress: ptrStr("alice@example.com"), CertificateArn: ptrStr(sesTestCertARN2), Status: sesv2types.IdentityCertificateStatusActive}
	for _, tc := range []struct {
		name  string
		pages [][]sesv2types.IdentityCertificate
	}{
		{"deprovisioning first", [][]sesv2types.IdentityCertificate{{old}, {replacement}}},
		{"replacement first", [][]sesv2types.IdentityCertificate{{replacement, old}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			seedParentRows(t, st, TypeSESEmailIdentity, sesTestDomainARN)
			warns := countOpWarnings(st, sesCertificateOp)
			t.Cleanup(func() {
				if *warns != 0 {
					t.Errorf("warnings = %d, want 0: a DEPROVISIONING duplicate is documented", *warns)
				}
			})
			h := map[string]func(in any) (any, error){
				"ListEmailIdentityCertificates": func(in any) (any, error) {
					page := 0
					if in.(*sesv2.ListEmailIdentityCertificatesInput).NextToken != nil {
						page = 1
					}
					out := &sesv2.ListEmailIdentityCertificatesOutput{Certificates: tc.pages[page]}
					if page+1 < len(tc.pages) {
						out.NextToken = ptrStr("p2")
					}
					return out, nil
				},
			}
			total, inserted, err := scanSESCerts(t, st, h, sesTestIdentities[:1])
			if err != nil || total != 1 || inserted != 1 {
				t.Fatalf("total, inserted, err = %d, %d, %v; want 1, 1, nil", total, inserted, err)
			}
			rows := rowsByNativeID(t, st, TypeSESEmailIdentityCertificate)
			alice, ok := rows[sesTestAlice]
			if len(rows) != 1 || !ok {
				t.Fatalf("rows = %v, want only %s", nativeIDs(rows), sesTestAlice)
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(alice.AttributesJSON), &attrs); err != nil {
				t.Fatalf("attrs %s: %v", alice.AttributesJSON, err)
			}
			if sv(alice.Status) != "ACTIVE" || attrs["CertificateArn"] != sesTestCertARN2 {
				t.Errorf("status/CertificateArn = %q/%v, want ACTIVE/%s", sv(alice.Status), attrs["CertificateArn"], sesTestCertARN2)
			}
		})
	}
}

// An association without a FromAddress applies to the identity itself when
// the identity is an email address; under a domain it is skipped with a
// warning.
func TestScanSESEmailIdentityCertificates_MissingFromAddress(t *testing.T) {
	for _, tc := range []struct {
		name      string
		identity  sesIdentity
		wantRows  []string
		wantWarns int
	}{
		{"email-address identity", sesTestIdentities[1], []string{sesTestOps}, 0},
		{"domain identity", sesTestIdentities[0], nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			seedParentRows(t, st, TypeSESEmailIdentity, tc.identity.arn)
			warns := countOpWarnings(st, sesCertificateOp)
			h := map[string]func(in any) (any, error){
				"ListEmailIdentityCertificates": func(any) (any, error) {
					return &sesv2.ListEmailIdentityCertificatesOutput{Certificates: []sesv2types.IdentityCertificate{
						{CertificateArn: ptrStr(sesTestCertARN1), Status: sesv2types.IdentityCertificateStatusActive},
					}}, nil
				},
			}
			total, _, err := scanSESCerts(t, st, h, []sesIdentity{tc.identity})
			if err != nil || total != len(tc.wantRows) {
				t.Fatalf("total, err = %d, %v; want %d, nil", total, err, len(tc.wantRows))
			}
			rows := rowsByNativeID(t, st, TypeSESEmailIdentityCertificate)
			if got := nativeIDs(rows); !slices.Equal(got, tc.wantRows) {
				t.Errorf("rows = %v, want %v", got, tc.wantRows)
			}
			if r, ok := rows[sesTestOps]; ok && sv(r.Name) != sesTestAddress {
				t.Errorf("name = %q, want %s", sv(r.Name), sesTestAddress)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
		})
	}
}

// An association skipped on an early page is not reported when a later page
// of the same identity fails: the identity stores nothing.
func TestScanSESEmailIdentityCertificates_FailedIdentityTalliesNothing(t *testing.T) {
	st := newTestStore(t)
	seedParentRows(t, st, TypeSESEmailIdentity, sesTestDomainARN)
	warns := countOpWarnings(st, sesCertificateOp)
	h := map[string]func(in any) (any, error){
		"ListEmailIdentityCertificates": func(in any) (any, error) {
			if in.(*sesv2.ListEmailIdentityCertificatesInput).NextToken != nil {
				return nil, sesDenied
			}
			return &sesv2.ListEmailIdentityCertificatesOutput{NextToken: ptrStr("p2"), Certificates: []sesv2types.IdentityCertificate{
				{CertificateArn: ptrStr(sesTestCertARN1)},
			}}, nil
		},
	}
	total, _, err := scanSESCerts(t, st, h, sesTestIdentities[:1])
	if err != nil || total != 0 {
		t.Fatalf("total, err = %d, %v; want 0, nil", total, err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 (the denial only)", *warns)
	}
}

// Two associations for one address, neither DEPROVISIONING, keep the first
// listed and are reported in one warning.
func TestScanSESEmailIdentityCertificates_UndocumentedDuplicateWarns(t *testing.T) {
	st := newTestStore(t)
	seedParentRows(t, st, TypeSESEmailIdentity, sesTestDomainARN)
	warns := countOpWarnings(st, sesCertificateOp)
	h := map[string]func(in any) (any, error){
		"ListEmailIdentityCertificates": func(any) (any, error) {
			return &sesv2.ListEmailIdentityCertificatesOutput{Certificates: []sesv2types.IdentityCertificate{
				{FromAddress: ptrStr("alice@example.com"), CertificateArn: ptrStr(sesTestCertARN1), Status: sesv2types.IdentityCertificateStatusActive},
				{FromAddress: ptrStr("alice@example.com"), CertificateArn: ptrStr(sesTestCertARN2), Status: sesv2types.IdentityCertificateStatusProvisioning},
			}}, nil
		},
	}
	total, inserted, err := scanSESCerts(t, st, h, sesTestIdentities[:1])
	if err != nil || total != 1 || inserted != 1 {
		t.Fatalf("total, inserted, err = %d, %d, %v; want 1, 1, nil", total, inserted, err)
	}
	alice, ok := rowsByNativeID(t, st, TypeSESEmailIdentityCertificate)[sesTestAlice]
	if !ok || sv(alice.Status) != "ACTIVE" {
		t.Errorf("alice found/status = %v/%q, want the first listed (ACTIVE)", ok, sv(alice.Status))
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1", *warns)
	}
}

// sesTestThird is a third domain identity, listed after the two fixtures.
var sesTestThird = sesIdentity{childParent: childParent{id: "third.example", arn: "arn:aws:ses:us-east-1:123456789012:identity/third.example"}}

var sesNotDeployed = apiErr("UnknownOperationException", "unknown operation")

// How a not-deployed refusal is reported depends on whether any identity
// listed successfully: with none the region does not serve the op and the
// phase is silent; with some, the refused identities are skipped in one
// warning and the others keep their rows.
func TestScanSESEmailIdentityCertificates_NotDeployed(t *testing.T) {
	for _, tc := range []struct {
		name string
		// errFor fails identities; nil leaves an identity's sesCertHandlers
		// listing, including page 2 of the domain.
		errFor    func(identity string, page2 bool) error
		wantRows  []string
		wantWarns int // all from this op: not-deployed, missing FromAddress, transient
		onceEach  bool
	}{
		{
			name:     "every identity not deployed",
			errFor:   func(string, bool) error { return sesNotDeployed },
			onceEach: true,
		},
		{
			// The domain's association without a FromAddress warns too.
			name: "one answers, one not deployed",
			errFor: func(id string, _ bool) error {
				if id == sesTestDomain {
					return nil
				}
				return sesNotDeployed
			},
			wantRows: []string{sesTestAlice, sesTestBob}, wantWarns: 2,
		},
		{
			// The gateway's NotFoundException is a refusal, not a deleted identity.
			name: "one answers, the rest refused by the gateway",
			errFor: func(id string, _ bool) error {
				if id == sesTestDomain {
					return nil
				}
				return sesGatewayNotFound
			},
			wantRows: []string{sesTestAlice, sesTestBob}, wantWarns: 2,
		},
		{
			name: "one answers, another refused on page 2",
			errFor: func(id string, page2 bool) error {
				if id == sesTestDomain && page2 {
					return sesNotDeployed
				}
				if id == sesTestThird.id {
					return apiErr("NotFoundException", "identity not found")
				}
				return nil
			},
			wantRows: []string{sesTestOps}, wantWarns: 1,
		},
		{
			name: "one deleted, the rest not deployed",
			errFor: func(id string, _ bool) error {
				if id == sesTestDomain {
					return apiErr("NotFoundException", "identity not found")
				}
				return sesNotDeployed
			},
		},
		{
			// Only the transient warning: no identity answered.
			name: "one throttled, the rest not deployed",
			errFor: func(id string, _ bool) error {
				if id == sesTestDomain {
					return apiErr("TooManyRequestsException", "slow down")
				}
				return sesNotDeployed
			},
			wantWarns: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			seedParentRows(t, st, TypeSESEmailIdentity, sesTestDomainARN, sesTestAddrARN, sesTestThird.arn)
			warns := countOpWarnings(st, sesCertificateOp)
			var calls parentCalls
			h := sesCertHandlers(&calls, func(string) error { return nil })
			listPage := h["ListEmailIdentityCertificates"]
			h["ListEmailIdentityCertificates"] = func(in any) (any, error) {
				p := in.(*sesv2.ListEmailIdentityCertificatesInput)
				id := sv(p.EmailIdentity)
				if err := tc.errFor(id, p.NextToken != nil); err != nil {
					calls.add(id)
					return nil, err
				}
				if id == sesTestThird.id {
					calls.add(id)
					return &sesv2.ListEmailIdentityCertificatesOutput{}, nil
				}
				return listPage(in)
			}
			identities := append(slices.Clone(sesTestIdentities), sesTestThird)
			total, inserted, err := scanSESCerts(t, st, h, identities)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeSESEmailIdentityCertificate)); !slices.Equal(got, tc.wantRows) {
				t.Errorf("rows = %v, want %v", got, tc.wantRows)
			}
			if total != len(tc.wantRows) || inserted != len(tc.wantRows) {
				t.Errorf("total, inserted = %d, %d; want %d", total, inserted, len(tc.wantRows))
			}
			if got := calls.distinct(); len(got) != len(identities) {
				t.Errorf("identities called = %v, want all %d", got, len(identities))
			}
			if tc.onceEach && len(calls.ids) != len(identities) {
				t.Errorf("calls = %v, want each identity once", calls.ids)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
		})
	}
}

// TestSESCertificateErrors_ReadCapitalisedGatewayMessage guards
// errorMessageKeyRepair for SES certificate errors.
//
// SES's gateway sends its message under "Message". From v1.75.0 (CHANGELOG
// "Enable schema-based (de)serialization") sesv2 reads only "message" for a
// modeled error, so without the repair a gateway NotFoundException would carry
// no message and read as a deleted identity. The client is built from the
// account config, as a scan's clients are; a client built without
// baseLoadOptions fails this test.
func TestSESCertificateErrors_ReadCapitalisedGatewayMessage(t *testing.T) {
	const gateway = "Unable to determine service/operation name to be authorized"
	cases := []struct {
		name, code, message string
		status              int
		wantGap             bool
	}{
		{"gateway AccessDenied", "AccessDeniedException", gateway, http.StatusForbidden, true},
		{"gateway NotFound", "NotFoundException", gateway, http.StatusNotFound, true},
		{"modeled NotFound", "NotFoundException", "Email identity " + sesTestDomain + " does not exist.", http.StatusNotFound, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Amzn-ErrorType", tc.code)
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(map[string]string{"Message": tc.message})
			}))
			defer srv.Close()
			client := sesv2.NewFromConfig(accountConfigForTest(t), func(o *sesv2.Options) {
				o.BaseEndpoint = sdkaws.String(srv.URL)
			})

			_, err := client.ListEmailIdentityCertificates(context.Background(),
				&sesv2.ListEmailIdentityCertificatesInput{EmailIdentity: sdkaws.String(sesTestDomain)})

			assertAPICode(t, err, tc.code)
			var ae smithy.APIError
			errors.As(err, &ae)
			if ae.ErrorMessage() != tc.message {
				t.Errorf("ErrorMessage() = %q, want %q", ae.ErrorMessage(), tc.message)
			}
			if got := isServiceNotAvailableInRegion(err); got != tc.wantGap {
				t.Errorf("isServiceNotAvailableInRegion = %v, want %v", got, tc.wantGap)
			}
			if got := isSESCertificateOpNotDeployed(err); got != tc.wantGap {
				t.Errorf("isSESCertificateOpNotDeployed = %v, want %v", got, tc.wantGap)
			}
			if got := isSESIdentityDeleted(err); got == tc.wantGap {
				t.Errorf("isSESIdentityDeleted = %v, want %v", got, !tc.wantGap)
			}
		})
	}
}
