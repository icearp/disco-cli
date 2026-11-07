package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"sort"
	"sync"
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
	acmThrottle = &smithy.GenericAPIError{Code: "ThrottlingException", Message: "Rate exceeded"}
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
	// acmTestTotal is every row of acmTopLevelHandlers: the certificate, the
	// account config, two endpoints, two bindings, three ACME accounts and two
	// domain validations.
	acmTestTotal = 11
)

var acmTestCreated = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// acmTestAccountID is the expected ACME account NativeID, written out rather
// than built with acmAcmeAccountNativeID so the test pins the shape.
func acmTestAccountID(endpointARN, url string) string {
	return endpointARN + "/acme-account/" + url
}

// acmChildCalls records the endpoint of every call to each endpoint-child op
// and the ARN of every tag read. tags, tagErrs and describeErrs, keyed by
// ARN, are set before the scan and only read by the handlers.
type acmChildCalls struct {
	bindings, accounts, validations parentCalls
	tagARNs, certTagARNs, described parentCalls
	tags                            map[string]map[string]string
	tagErrs, describeErrs           map[string]error
}

// acmTestTags is m as an ACM tag list.
func acmTestTags(m map[string]string) []acmtypes.Tag {
	var tags []acmtypes.Tag
	for k, v := range m {
		tags = append(tags, acmtypes.Tag{Key: ptrStr(k), Value: ptrStr(v)})
	}
	return tags
}

// acmTopLevelHandlers serves one certificate and the account config, then
// ACME endpoints over two pages (ep1; then ep2 and an element with no ARN).
// Under ep1, each child op returns two pages: one child, then another child
// and an element with no id. Under ep2, only ListAcmeAccounts returns a row:
// an account with ep1's first AccountUrl. Both tag ops answer calls.tags (no
// tags when unset) or calls.tagErrs.
func acmTopLevelHandlers(calls *acmChildCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListCertificates": func(any) (any, error) {
			return &acm.ListCertificatesOutput{CertificateSummaryList: []acmtypes.CertificateSummary{{
				CertificateArn: ptrStr(acmTestCert), DomainName: ptrStr("example.com"),
				Status: acmtypes.CertificateStatusIssued, CreatedAt: &acmTestCreated,
			}}}, nil
		},
		"DescribeCertificate": func(in any) (any, error) {
			arn := sv(in.(*acm.DescribeCertificateInput).CertificateArn)
			calls.described.add(arn)
			if err := calls.describeErrs[arn]; err != nil {
				return nil, err
			}
			return &acm.DescribeCertificateOutput{Certificate: &acmtypes.CertificateDetail{
				CertificateArn: &arn, DomainName: ptrStr("example.com"), Serial: ptrStr("01:02"),
			}}, nil
		},
		"ListTagsForCertificate": func(in any) (any, error) {
			arn := sv(in.(*acm.ListTagsForCertificateInput).CertificateArn)
			calls.certTagARNs.add(arn)
			if err := calls.tagErrs[arn]; err != nil {
				return nil, err
			}
			return &acm.ListTagsForCertificateOutput{Tags: acmTestTags(calls.tags[arn])}, nil
		},
		"ListTagsForResource": func(in any) (any, error) {
			arn := sv(in.(*acm.ListTagsForResourceInput).ResourceArn)
			calls.tagARNs.add(arn)
			if err := calls.tagErrs[arn]; err != nil {
				return nil, err
			}
			return &acm.ListTagsForResourceOutput{Tags: acmTestTags(calls.tags[arn])}, nil
		},
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

// acmWarningOps is the op of each warning, sorted.
func acmWarningOps(warns []store.ScanWarning) []string {
	ops := make([]string, 0, len(warns))
	for _, w := range warns {
		ops = append(ops, w.Service)
	}
	sort.Strings(ops)
	return ops
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
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls acmChildCalls
	total, err := scanACMTopLevel(t, st, acmTopLevelHandlers(&calls))
	if err != nil {
		t.Fatalf("scanACM: %v", err)
	}
	if total != acmTestTotal {
		t.Errorf("total = %d, want %d", total, acmTestTotal)
	}
	for op, c := range map[string]*parentCalls{"bindings": &calls.bindings, "accounts": &calls.accounts, "validations": &calls.validations} {
		if got := c.distinct(); !slices.Equal(got, []string{acmTestEP1, acmTestEP2}) {
			t.Errorf("%s listed for %v, want [%s %s]", op, got, acmTestEP1, acmTestEP2)
		}
	}
	// Each listing's element without an id is warned about once, not dropped silently.
	wantWarns := []string{"acm:ListAcmeAccounts", "acm:ListAcmeDomainValidations", "acm:ListAcmeEndpoints", "acm:ListAcmeExternalAccountBindings"}
	if got := acmWarningOps(warns()); !slices.Equal(got, wantWarns) {
		t.Errorf("warnings = %v, want %v", warns(), wantWarns)
	}

	cert := onlyRow(t, st, TypeACMCertificate)
	if cert.NativeID != acmTestCert || sv(cert.Name) != "example.com" || sv(cert.Status) != "ISSUED" || sv(cert.Region) != testRegion ||
		sv(cert.CreatedAt) != "2026-01-02T03:04:05Z" || !jsonHasString(t, cert.AttributesJSON, "Serial", "01:02") {
		t.Errorf("certificate %s name/status/region/created = %q/%q/%q/%q, attrs = %s; want the DescribeCertificate body",
			cert.NativeID, sv(cert.Name), sv(cert.Status), sv(cert.Region), sv(cert.CreatedAt), cert.AttributesJSON)
	}

	eps := rowsByNativeID(t, st, TypeACMAcmeEndpoint)
	if got := nativeIDs(eps); !slices.Equal(got, []string{acmTestEP1, acmTestEP2}) {
		t.Fatalf("endpoint rows = %v, want [%s %s]", got, acmTestEP1, acmTestEP2)
	}
	ep1 := eps[acmTestEP1]
	if sv(ep1.Status) != "ACTIVE" || sv(ep1.Region) != testRegion || sv(ep1.CreatedAt) != "2026-01-02T03:04:05Z" || ep1.Name != nil ||
		!jsonHasString(t, ep1.AttributesJSON, "EndpointUrl", "https://acme.example/ep1") {
		t.Errorf("ep1 name/status/region/created = %v/%q/%q/%q, attrs = %s", ep1.Name, sv(ep1.Status), sv(ep1.Region), sv(ep1.CreatedAt), ep1.AttributesJSON)
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
	if sv(eab1.Region) != testRegion || sv(eab1.CreatedAt) != "2026-01-02T03:04:05Z" || eab1.Name != nil || !jsonHasString(t, eab1.AttributesJSON, "RoleArn", acmTestRole) {
		t.Errorf("eab1 name/region/created = %v/%q/%q, attrs = %s", eab1.Name, sv(eab1.Region), sv(eab1.CreatedAt), eab1.AttributesJSON)
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
	if sv(a1.Status) != "VALID" || sv(a1.Region) != testRegion || sv(a1.CreatedAt) != "2026-01-02T03:04:05Z" || a1.Name != nil || a1.TagsJSON != nil ||
		!jsonHasString(t, a1.AttributesJSON, "AcmeExternalAccountBindingArn", acmTestEAB1) || !jsonHasString(t, a1.AttributesJSON, "PublicKeyThumbprint", "thumb-1") {
		t.Errorf("account1 name/status/region/created/tags = %v/%q/%q/%q/%v, attrs = %s", a1.Name, sv(a1.Status), sv(a1.Region), sv(a1.CreatedAt), a1.TagsJSON, a1.AttributesJSON)
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

// Every taggable row has its tags read with its own listed ARN: certificates
// through ListTagsForCertificate, the ACME endpoints, bindings and domain
// validations through ListTagsForResource. A row without tags stores nil.
// ACME accounts have no tag read.
func TestScanACM_StoresTags(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := acmChildCalls{tags: map[string]map[string]string{
		acmTestCert: {"env": "prod"},
		acmTestEP1:  {"team": "pki"},
		acmTestEAB1: {"client": "a"},
		acmTestDV1:  {"zone": "z1", "owner": "dns"},
	}}
	total, err := scanACMTopLevel(t, st, acmTopLevelHandlers(&calls))
	if err != nil {
		t.Fatalf("scanACM: %v", err)
	}
	if total != acmTestTotal {
		t.Errorf("total = %d, want %d", total, acmTestTotal)
	}
	wantARNs := []string{acmTestEP1, acmTestEP2, acmTestEAB1, acmTestEAB2, acmTestDV1, acmTestDV2}
	sort.Strings(wantARNs)
	if got := calls.tagARNs.ids; len(got) != len(wantARNs) || !slices.Equal(calls.tagARNs.distinct(), wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v, want each of %v once", got, wantARNs)
	}
	if got := calls.certTagARNs.ids; !slices.Equal(got, []string{acmTestCert}) {
		t.Errorf("ListTagsForCertificate ARNs = %v, want [%s]", got, acmTestCert)
	}
	for _, c := range []struct {
		typ, arn string
		want     map[string]string
	}{
		{TypeACMCertificate, acmTestCert, map[string]string{"env": "prod"}},
		{TypeACMAcmeEndpoint, acmTestEP1, map[string]string{"team": "pki"}},
		{TypeACMAcmeExternalAccountBinding, acmTestEAB1, map[string]string{"client": "a"}},
		{TypeACMAcmeDomainValidation, acmTestDV1, map[string]string{"zone": "z1", "owner": "dns"}},
	} {
		assertTagsMap(t, rowsByNativeID(t, st, c.typ)[c.arn].TagsJSON, c.want)
	}
	for _, c := range []struct{ typ, arn string }{
		{TypeACMAcmeEndpoint, acmTestEP2},
		{TypeACMAcmeExternalAccountBinding, acmTestEAB2},
		{TypeACMAcmeDomainValidation, acmTestDV2},
	} {
		r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
		if !ok || r.TagsJSON != nil {
			t.Errorf("%s stored %v with tags %v, want stored with nil tags", c.arn, ok, r.TagsJSON)
		}
	}
}

// A rescan reports every stored row in its total, not only the newly inserted
// ones. The dispatcher reports total for the service, and a first scan into an
// empty store has total == inserted, so only a rescan catches the two swapped.
func TestScanACM_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := acmChildCalls{tags: map[string]map[string]string{acmTestEP1: {"team": "pki"}}}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(acmTopLevelHandlers(&calls), acmDenied)
	if _, _, err := scanACM(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, _, err := scanACM(context.Background(), acct, testRegion, st, testScanID)
	if err != nil || total != acmTestTotal {
		t.Errorf("rescan total = %d, err = %v; want %d, nil", total, err, acmTestTotal)
	}
}

// A failed tag read. ResourceNotFoundException, which both tag ops model,
// drops a resource deleted since it was listed (an endpoint with the children
// it would have listed). A denial keeps the row untagged and warns once under
// the tag op with the scan's scope; a throttle keeps it and is transient; any
// other error keeps it and is an error.
func TestScanACM_TagReadFailures(t *testing.T) {
	t.Parallel()
	// ep1 and the two bindings, two accounts and two validations it lists.
	ep1AndChildren := 1 + 2 + 2 + 2
	for _, c := range []struct {
		name      string
		arn, typ  string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warnOp    string
	}{
		{"binding/not found", acmTestEAB1, TypeACMAcmeExternalAccountBinding, acmGone, -1, "", acmTestTotal - 1, false, ""},
		{"binding/denied", acmTestEAB1, TypeACMAcmeExternalAccountBinding, acmDenied, -1, "", acmTestTotal, true, acmTagsOp},
		{"binding/transient", acmTestEAB1, TypeACMAcmeExternalAccountBinding, acmThrottle, outcomeTransient, "ThrottlingException", acmTestTotal, true, ""},
		{"binding/other error", acmTestEAB1, TypeACMAcmeExternalAccountBinding, acmBadInput, outcomeError, "ValidationException", acmTestTotal, true, ""},
		{"validation/not found", acmTestDV2, TypeACMAcmeDomainValidation, acmGone, -1, "", acmTestTotal - 1, false, ""},
		{"validation/other error", acmTestDV2, TypeACMAcmeDomainValidation, acmBadInput, outcomeError, "ValidationException", acmTestTotal, true, ""},
		{"endpoint/not found", acmTestEP1, TypeACMAcmeEndpoint, acmGone, -1, "", acmTestTotal - ep1AndChildren, false, ""},
		{"endpoint/denied", acmTestEP1, TypeACMAcmeEndpoint, acmDenied, -1, "", acmTestTotal, true, acmTagsOp},
		{"endpoint/other error", acmTestEP1, TypeACMAcmeEndpoint, acmBadInput, outcomeError, "ValidationException", acmTestTotal, true, ""},
		{"certificate/not found", acmTestCert, TypeACMCertificate, acmGone, -1, "", acmTestTotal - 1, false, ""},
		{"certificate/denied", acmTestCert, TypeACMCertificate, acmDenied, -1, "", acmTestTotal, true, acmCertTagsOp},
		{"certificate/transient", acmTestCert, TypeACMCertificate, acmThrottle, outcomeTransient, "ThrottlingException", acmTestTotal, true, ""},
		{"certificate/other error", acmTestCert, TypeACMCertificate, acmBadInput, outcomeError, "ValidationException", acmTestTotal, true, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			calls := acmChildCalls{tagErrs: map[string]error{c.arn: c.err}}
			handlers := acmTopLevelHandlers(&calls)
			// Every element has an id, so the only warnings are the tag reads'.
			acmDropKeylessElements(handlers)
			warns := rhWarnRecorder(st)

			total, err := scanACMTopLevel(t, st, handlers)
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
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			var want []string
			if c.warnOp != "" {
				want = []string{c.warnOp}
			}
			if got := acmWarningOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// acmDropKeylessElements removes from each ACME listing's fixture the element
// without an id, so that a test sees only the warnings it provokes.
func acmDropKeylessElements(handlers map[string]func(in any) (any, error)) {
	endpoints := handlers["ListAcmeEndpoints"]
	handlers["ListAcmeEndpoints"] = func(in any) (any, error) {
		out, err := endpoints(in)
		if o, ok := out.(*acm.ListAcmeEndpointsOutput); ok {
			o.AcmeEndpoints = slices.DeleteFunc(o.AcmeEndpoints, func(e acmtypes.AcmeEndpointSummary) bool { return e.AcmeEndpointArn == nil })
		}
		return out, err
	}
	bindings := handlers["ListAcmeExternalAccountBindings"]
	handlers["ListAcmeExternalAccountBindings"] = func(in any) (any, error) {
		out, err := bindings(in)
		if o, ok := out.(*acm.ListAcmeExternalAccountBindingsOutput); ok {
			o.ExternalAccountBindings = slices.DeleteFunc(o.ExternalAccountBindings, func(b acmtypes.AcmeExternalAccountBindingSummary) bool {
				return b.AcmeExternalAccountBindingArn == nil
			})
		}
		return out, err
	}
	accounts := handlers["ListAcmeAccounts"]
	handlers["ListAcmeAccounts"] = func(in any) (any, error) {
		out, err := accounts(in)
		if o, ok := out.(*acm.ListAcmeAccountsOutput); ok {
			o.AcmeAccounts = slices.DeleteFunc(o.AcmeAccounts, func(a acmtypes.AcmeAccountSummary) bool { return a.AccountUrl == nil })
		}
		return out, err
	}
	validations := handlers["ListAcmeDomainValidations"]
	handlers["ListAcmeDomainValidations"] = func(in any) (any, error) {
		out, err := validations(in)
		if o, ok := out.(*acm.ListAcmeDomainValidationsOutput); ok {
			o.AcmeDomainValidations = slices.DeleteFunc(o.AcmeDomainValidations, func(v acmtypes.AcmeDomainValidationSummary) bool {
				return v.AcmeDomainValidationArn == nil
			})
		}
		return out, err
	}
}

// Denied ListTagsForResource reads in the endpoint phase and two child phases
// send one warning for the whole scan; the rows are kept.
func TestScanACM_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, acmTagsOp)
	calls := acmChildCalls{tagErrs: map[string]error{acmTestEP2: acmDenied, acmTestEAB2: acmDenied, acmTestDV1: acmDenied}}
	total, err := scanACMTopLevel(t, st, acmTopLevelHandlers(&calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != acmTestTotal {
		t.Errorf("total = %d; want %d", total, acmTestTotal)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", acmTagsOp, *warns)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanACM_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, acmTagsOp)
	calls := acmChildCalls{tagErrs: map[string]error{acmTestEP2: acmThrottle, acmTestDV1: acmBadInput}}
	_, err := scanACMTopLevel(t, st, acmTopLevelHandlers(&calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", acmTagsOp, *warns)
	}
}

// A failed DescribeCertificate. ResourceNotFoundException, which the op
// models, drops a certificate deleted since it was listed, with no tag read.
// A denial keeps the CertificateSummary as the row, tags read, and warns
// once; a throttle keeps it and is transient; any other error, and a
// response without its Certificate, keep it and are errors.
func TestScanACM_DescribeCertificateFailures(t *testing.T) {
	t.Parallel()
	noBody := errors.New("no body")
	for _, c := range []struct {
		name     string
		err      error
		outcome  serviceOutcome // -1: no error
		is       func(error) bool
		wantKept bool
		warnOp   string
	}{
		{"not found", acmGone, -1, nil, false, ""},
		{"denied", acmDenied, -1, nil, true, "acm:DescribeCertificate"},
		{"transient", acmThrottle, outcomeTransient, func(err error) bool { return isAPIErrorCode(err, "ThrottlingException") }, true, ""},
		{"other error", acmBadInput, outcomeError, func(err error) bool { return isAPIErrorCode(err, "ValidationException") }, true, ""},
		{"no Certificate", noBody, outcomeError, func(err error) bool { return errors.Is(err, errACMNoCertificate) }, true, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			calls := acmChildCalls{tags: map[string]map[string]string{acmTestCert: {"env": "prod"}}}
			handlers := acmTopLevelHandlers(&calls)
			acmDropKeylessElements(handlers)
			if c.err == noBody {
				handlers["DescribeCertificate"] = func(any) (any, error) { return &acm.DescribeCertificateOutput{}, nil }
			} else {
				calls.describeErrs = map[string]error{acmTestCert: c.err}
			}
			warns := rhWarnRecorder(st)

			total, err := scanACMTopLevel(t, st, handlers)
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				if !c.is(err) {
					t.Errorf("err = %v; want the DescribeCertificate failure", err)
				}
			}
			var want []string
			if c.warnOp != "" {
				want = []string{c.warnOp}
			}
			if got := acmWarningOps(warns()); !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", warns(), want)
			}
			cert, ok := rowsByNativeID(t, st, TypeACMCertificate)[acmTestCert]
			if !c.wantKept {
				if ok || total != acmTestTotal-1 || len(calls.certTagARNs.ids) != 0 {
					t.Errorf("stored = %v, total = %d, tag reads = %v; want dropped, %d, none", ok, total, calls.certTagARNs.ids, acmTestTotal-1)
				}
				return
			}
			if !ok || total != acmTestTotal {
				t.Fatalf("stored = %v, total = %d; want stored, %d", ok, total, acmTestTotal)
			}
			// The row is the summary: its own fields, not the describe body's Serial.
			if sv(cert.Name) != "example.com" || sv(cert.Status) != "ISSUED" || sv(cert.CreatedAt) != "2026-01-02T03:04:05Z" ||
				jsonHasString(t, cert.AttributesJSON, "Serial", "01:02") || !jsonHasString(t, cert.AttributesJSON, "CertificateArn", acmTestCert) {
				t.Errorf("certificate name/status/created = %q/%q/%q, attrs = %s; want the CertificateSummary", sv(cert.Name), sv(cert.Status), sv(cert.CreatedAt), cert.AttributesJSON)
			}
			assertTagsMap(t, cert.TagsJSON, map[string]string{"env": "prod"})
		})
	}
}

// A certificate listed without an ARN is warned about, not described or
// stored.
func TestScanACM_CertificateWithoutARN(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, "acm:ListCertificates")
	var calls acmChildCalls
	handlers := acmTopLevelHandlers(&calls)
	handlers["ListCertificates"] = func(any) (any, error) {
		return &acm.ListCertificatesOutput{CertificateSummaryList: []acmtypes.CertificateSummary{{DomainName: ptrStr("no-arn.example")}}}, nil
	}
	if _, err := scanACMTopLevel(t, st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n := len(rowsByNativeID(t, st, TypeACMCertificate)); n != 0 || *warns != 1 || len(calls.described.ids) != 0 {
		t.Errorf("rows = %d, warnings = %d, described = %v; want 0, 1, none", n, *warns, calls.described.ids)
	}
}

// Every listing asks for its model's page maximum.
func TestScanACM_PageCaps(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls acmChildCalls
	handlers := acmTopLevelHandlers(&calls)
	var (
		mu    sync.Mutex // guards sizes: the child listings run concurrently
		sizes = map[string][]int32{}
	)
	record := func(op string, size *int32) {
		mu.Lock()
		defer mu.Unlock()
		sizes[op] = append(sizes[op], sv32(size))
	}
	for op, size := range map[string]func(in any) *int32{
		"ListCertificates":                func(in any) *int32 { return in.(*acm.ListCertificatesInput).MaxItems },
		"ListAcmeEndpoints":               func(in any) *int32 { return in.(*acm.ListAcmeEndpointsInput).MaxResults },
		"ListAcmeExternalAccountBindings": func(in any) *int32 { return in.(*acm.ListAcmeExternalAccountBindingsInput).MaxResults },
		"ListAcmeAccounts":                func(in any) *int32 { return in.(*acm.ListAcmeAccountsInput).MaxResults },
		"ListAcmeDomainValidations":       func(in any) *int32 { return in.(*acm.ListAcmeDomainValidationsInput).MaxResults },
	} {
		next := handlers[op]
		handlers[op] = func(in any) (any, error) {
			record(op, size(in))
			return next(in)
		}
	}
	if _, err := scanACMTopLevel(t, st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for op, want := range map[string]int32{
		"ListCertificates": 1000, "ListAcmeEndpoints": 100, "ListAcmeExternalAccountBindings": 100,
		"ListAcmeAccounts": 100, "ListAcmeDomainValidations": 100,
	} {
		got := sizes[op]
		if len(got) == 0 || slices.ContainsFunc(got, func(n int32) bool { return n != want }) {
			t.Errorf("%s page sizes = %v; want every call %d", op, got, want)
		}
	}
}

// Every ListCertificates page asks for every key type and every key pair
// origin, since the default filter leaves out ECDSA, RSA_3072, RSA_4096 and
// ACME-issued certificates.
func TestScanACM_ListCertificatesFilters(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls acmChildCalls
	handlers := acmTopLevelHandlers(&calls)
	var inputs []*acm.ListCertificatesInput // the certificate phase runs before every other phase, one page at a time
	certs := handlers["ListCertificates"]
	handlers["ListCertificates"] = func(in any) (any, error) {
		p := in.(*acm.ListCertificatesInput)
		inputs = append(inputs, p)
		if p.NextToken == nil {
			return &acm.ListCertificatesOutput{NextToken: ptrStr("p2")}, nil
		}
		return certs(in)
	}
	if _, err := scanACMTopLevel(t, st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	wantOrigins := []acmtypes.CertificateKeyPairOrigin{"AWS_MANAGED", "ACME", "CUSTOMER_PROVIDED"}
	wantKeys := []acmtypes.KeyAlgorithm{"RSA_1024", "RSA_2048", "RSA_3072", "RSA_4096", "EC_prime256v1", "EC_secp384r1", "EC_secp521r1"}
	if len(inputs) != 2 {
		t.Fatalf("ListCertificates calls = %d; want 2", len(inputs))
	}
	for i, in := range inputs {
		if !slices.Equal(in.CertificateKeyPairOrigins, wantOrigins) {
			t.Errorf("page %d CertificateKeyPairOrigins = %v; want %v", i, in.CertificateKeyPairOrigins, wantOrigins)
		}
		if in.Includes == nil || !slices.Equal(in.Includes.KeyTypes, wantKeys) {
			t.Errorf("page %d Includes = %+v; want KeyTypes %v", i, in.Includes, wantKeys)
		}
	}
}

// A throttle of a phase that counts its own rows, beside a hard error of
// another phase, is demoted to one warning under that phase's own op.
func TestScanACM_CountedPhaseThrottleWarnsUnderItsOp(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"GetAccountConfiguration", "ListAcmeAccounts"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			var calls acmChildCalls
			h := acmTopLevelHandlers(&calls)
			acmDropKeylessElements(h)
			h[op] = func(any) (any, error) { return nil, acmThrottle }
			h["ListAcmeDomainValidations"] = func(any) (any, error) { return nil, acmBadInput }
			_, err := scanACMTopLevel(t, st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if got, want := acmWarningOps(warns()), []string{"acm:" + op}; !slices.Equal(got, want) {
				t.Errorf("warning ops = %v; want %v", got, want)
			}
		})
	}
}

// Rows of the aws-us-gov and aws-cn partitions reach both tag ops under
// their listed ARNs unchanged.
func TestScanACM_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			prefix := "arn:" + c.partition + ":acm:" + c.region + ":" + testAccountID + ":"
			cert, ep := prefix+"certificate/c-p", prefix+"acme-endpoint/ep-p"
			eab, dv := ep+"/acme-external-account-binding/eab-p", ep+"/acme-domain-validation/dv-p"
			calls := acmChildCalls{tags: map[string]map[string]string{cert: {"k": "c"}, ep: {"k": "e"}, eab: {"k": "b"}, dv: {"k": "d"}}}
			handlers := acmTopLevelHandlers(&calls)
			handlers["ListCertificates"] = func(any) (any, error) {
				return &acm.ListCertificatesOutput{CertificateSummaryList: []acmtypes.CertificateSummary{{CertificateArn: &cert}}}, nil
			}
			handlers["ListAcmeEndpoints"] = func(any) (any, error) {
				return &acm.ListAcmeEndpointsOutput{AcmeEndpoints: []acmtypes.AcmeEndpointSummary{{AcmeEndpointArn: &ep}}}, nil
			}
			handlers["ListAcmeExternalAccountBindings"] = func(any) (any, error) {
				return &acm.ListAcmeExternalAccountBindingsOutput{ExternalAccountBindings: []acmtypes.AcmeExternalAccountBindingSummary{{AcmeExternalAccountBindingArn: &eab}}}, nil
			}
			handlers["ListAcmeAccounts"] = func(any) (any, error) { return &acm.ListAcmeAccountsOutput{}, nil }
			handlers["ListAcmeDomainValidations"] = func(any) (any, error) {
				return &acm.ListAcmeDomainValidationsOutput{AcmeDomainValidations: []acmtypes.AcmeDomainValidationSummary{{AcmeDomainValidationArn: &dv}}}, nil
			}
			acct := partitionAccount(c.partition)
			acct.cfg = opStubConfig(handlers, acmDenied)
			if _, _, err := scanACM(context.Background(), acct, c.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			wantARNs := []string{ep, eab, dv}
			sort.Strings(wantARNs)
			if got := calls.tagARNs.distinct(); !slices.Equal(got, wantARNs) {
				t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantARNs)
			}
			if got := calls.certTagARNs.ids; !slices.Equal(got, []string{cert}) {
				t.Errorf("ListTagsForCertificate ARNs = %v; want [%s]", got, cert)
			}
			for typ, want := range map[string][2]string{
				TypeACMCertificate: {cert, "c"}, TypeACMAcmeEndpoint: {ep, "e"},
				TypeACMAcmeExternalAccountBinding: {eab, "b"}, TypeACMAcmeDomainValidation: {dv, "d"},
			} {
				assertTagsMap(t, rowsByNativeID(t, st, typ)[want[0]].TagsJSON, map[string]string{"k": want[1]})
			}
		})
	}
}

// One AccountUrl under two endpoints is two accounts; the endpoint ARN in the
// NativeID keeps their identities apart.
func TestScanACM_SameAccountURLUnderTwoEndpointsKeepsTwoIdentities(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	if n := len(calls.bindings.distinct()) + len(calls.accounts.distinct()) + len(calls.validations.distinct()) + len(calls.tagARNs.ids); n != 0 {
		t.Errorf("child and tag calls = %d, want none", n)
	}
	for _, typ := range []string{TypeACMAcmeEndpoint, TypeACMAcmeExternalAccountBinding, TypeACMAcmeAccount, TypeACMAcmeDomainValidation} {
		if n := len(rowsByNativeID(t, st, typ)); n != 0 {
			t.Errorf("%s rows = %d, want 0", typ, n)
		}
	}
}

// A failed page of the endpoints keeps page one's endpoint and its children:
// a denial warns once; any other error is returned after they are stored.
func TestScanACM_FailedAcmeEndpointsPage(t *testing.T) {
	t.Parallel()
	failPageTwo := func(next func(in any) (any, error), err error) func(in any) (any, error) {
		return func(in any) (any, error) {
			if in.(*acm.ListAcmeEndpointsInput).NextToken != nil {
				return nil, err
			}
			return next(in)
		}
	}
	for _, c := range []struct {
		name     string
		err      error
		wantWarn int
	}{
		{"denied", acmDenied, 1},
		{"other", acmBadInput, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "acm:ListAcmeEndpoints")
			var calls acmChildCalls
			h := acmTopLevelHandlers(&calls)
			h["ListAcmeEndpoints"] = failPageTwo(h["ListAcmeEndpoints"], c.err)
			total, err := scanACMTopLevel(t, st, h)
			if c.err == acmDenied {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, "ValidationException")
			}
			if *warns != c.wantWarn {
				t.Errorf("warnings = %d, want %d", *warns, c.wantWarn)
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
	}
}

// A repeated NextToken ends a listing as an error, keeping the pages read:
// the endpoints' and an endpoint's bindings'.
func TestScanACM_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	t.Run("endpoints", func(t *testing.T) {
		st := newTestStore(t)
		var calls acmChildCalls
		h := acmTopLevelHandlers(&calls)
		h["ListAcmeEndpoints"] = func(any) (any, error) {
			return &acm.ListAcmeEndpointsOutput{NextToken: ptrStr("same"), AcmeEndpoints: []acmtypes.AcmeEndpointSummary{{AcmeEndpointArn: ptrStr(acmTestEP1)}}}, nil
		}
		_, err := scanACMTopLevel(t, st, h)
		if !errors.Is(err, errRepeatedNextToken) {
			t.Fatalf("err = %v; want errRepeatedNextToken", err)
		}
		if got := nativeIDs(rowsByNativeID(t, st, TypeACMAcmeEndpoint)); !slices.Equal(got, []string{acmTestEP1}) {
			t.Errorf("endpoint rows = %v, want [%s]", got, acmTestEP1)
		}
	})
	t.Run("bindings", func(t *testing.T) {
		st := newTestStore(t)
		var calls acmChildCalls
		h := acmTopLevelHandlers(&calls)
		h["ListAcmeExternalAccountBindings"] = func(any) (any, error) {
			return &acm.ListAcmeExternalAccountBindingsOutput{NextToken: ptrStr("same"), ExternalAccountBindings: []acmtypes.AcmeExternalAccountBindingSummary{
				{AcmeExternalAccountBindingArn: ptrStr(acmTestEAB1)},
			}}, nil
		}
		_, err := scanACMTopLevel(t, st, h)
		if !errors.Is(err, errRepeatedNextToken) {
			t.Fatalf("err = %v; want errRepeatedNextToken", err)
		}
		if got := nativeIDs(rowsByNativeID(t, st, TypeACMAcmeExternalAccountBinding)); !slices.Equal(got, []string{acmTestEAB1}) {
			t.Errorf("binding rows = %v, want [%s]", got, acmTestEAB1)
		}
	})
}

// A failed endpoint drops only its own children of that op: deleted since
// listing is silent, a denial warns once however many endpoints hit it, and
// any other error fails the scan.
func TestScanACM_FailedChildEndpoint(t *testing.T) {
	t.Parallel()
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
				if n := len(rowsByNativeID(t, st, tc.childType)); n != 0 || total != acmTestTotal-tc.lost {
					t.Errorf("%s rows = %d, total = %d; want 0 and %d", tc.childType, n, total, acmTestTotal-tc.lost)
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

// One endpoint's failed child listing costs the other endpoint none of its
// children, and the failed endpoint keeps the pages it read before failing.
func TestScanACM_FailedEndpointKeepsSiblings(t *testing.T) {
	t.Parallel()
	t.Run("accounts", func(t *testing.T) {
		st := newTestStore(t)
		var calls acmChildCalls
		h := acmTopLevelHandlers(&calls)
		next := h["ListAcmeAccounts"]
		h["ListAcmeAccounts"] = func(in any) (any, error) {
			if sv(in.(*acm.ListAcmeAccountsInput).AcmeEndpointArn) == acmTestEP1 {
				return nil, acmBadInput
			}
			return next(in)
		}
		total, err := scanACMTopLevel(t, st, h)
		assertAPICode(t, err, "ValidationException")
		if got := nativeIDs(rowsByNativeID(t, st, TypeACMAcmeAccount)); !slices.Equal(got, []string{acmTestAccountID(acmTestEP2, acmTestURL1)}) {
			t.Errorf("account rows = %v, want ep2's", got)
		}
		assertContainsNative(t, st, acmTestEP2, acmTestAccountID(acmTestEP2, acmTestURL1))
		if want := acmTestTotal - 2; total != want {
			t.Errorf("total = %d, want %d", total, want)
		}
	})
	t.Run("bindings page two", func(t *testing.T) {
		st := newTestStore(t)
		var calls acmChildCalls
		h := acmTopLevelHandlers(&calls)
		next := h["ListAcmeExternalAccountBindings"]
		h["ListAcmeExternalAccountBindings"] = func(in any) (any, error) {
			if in.(*acm.ListAcmeExternalAccountBindingsInput).NextToken != nil {
				return nil, acmBadInput
			}
			return next(in)
		}
		total, err := scanACMTopLevel(t, st, h)
		assertAPICode(t, err, "ValidationException")
		if got := nativeIDs(rowsByNativeID(t, st, TypeACMAcmeExternalAccountBinding)); !slices.Equal(got, []string{acmTestEAB1}) {
			t.Errorf("binding rows = %v, want [%s]", got, acmTestEAB1)
		}
		assertContainsNative(t, st, acmTestEP1, acmTestEAB1)
		if want := acmTestTotal - 1; total != want {
			t.Errorf("total = %d, want %d", total, want)
		}
	})
}

// A hard error in any phase reaches the caller and costs no other phase its
// rows: every phase runs, those after it included.
func TestScanACM_PhaseErrorKeepsOtherPhases(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		op        string
		wantTotal int
	}{
		{"ListCertificates", acmTestTotal - 1},
		{"GetAccountConfiguration", acmTestTotal - 1},
		{"ListAcmeEndpoints", 2},                              // no endpoint, so no children
		{"ListAcmeExternalAccountBindings", acmTestTotal - 2}, // its 2 bindings
		{"ListAcmeAccounts", acmTestTotal - 3},                // its 3 accounts
		{"ListAcmeDomainValidations", acmTestTotal - 2},       // its 2 validations
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
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
		})
	}
}

// A failed later page of the certificates keeps page one's certificate in the
// store and the total, and the later phases still run.
func TestScanACM_FailedCertificatesPageKeepsItsRows(t *testing.T) {
	t.Parallel()
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
	if total != acmTestTotal || len(rowsByNativeID(t, st, TypeACMCertificate)) != 1 {
		t.Errorf("total = %d, certificate rows = %d; want %d and 1", total, len(rowsByNativeID(t, st, TypeACMCertificate)), acmTestTotal)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), and no later phase runs.
func TestScanACM_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeACMAcmeExternalAccountBinding)
	calls := acmChildCalls{tagErrs: map[string]error{acmTestEAB1: acmDenied}}
	h := acmTopLevelHandlers(&calls)
	acmDropKeylessElements(h)
	_, err := scanACMTopLevel(t, st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := len(calls.accounts.ids) + len(calls.validations.ids); n != 0 {
		t.Errorf("later phases made %d calls; want none after the failed write", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan; with nothing
// stored, the region does not serve ACM.
func TestScanACM_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "acm.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("child listing after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		var calls acmChildCalls
		h := acmTopLevelHandlers(&calls)
		h["ListAcmeDomainValidations"] = func(any) (any, error) { return nil, nx }
		total, err := scanACMTopLevel(t, st, h)
		assertGlueOutcome(t, err, outcomeError)
		if want := acmTestTotal - 2; total != want {
			t.Errorf("total = %d; want %d", total, want)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := acmChildCalls{tagErrs: map[string]error{acmTestEP1: nx}}
		_, err := scanACMTopLevel(t, st, acmTopLevelHandlers(&calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanACM(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

// The client is pinned to the scanned region, not the config's default.
func TestScanACM_CallsGoToScannedRegion(t *testing.T) {
	t.Parallel()
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
