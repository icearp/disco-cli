package aws

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	iottypes "github.com/aws/aws-sdk-go-v2/service/iot/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/icearp/disco-cli/store"
)

const iotSuppressionOp = "iot:ListAuditSuppressions"

var (
	iotDenied         = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: iot:List"}
	iotInvalidRequest = &smithy.GenericAPIError{Code: "InvalidRequestException", Message: "bad"}
	iotFamily         = &smithy.GenericAPIError{Code: "InternalFailureException", Message: "family"}
	iot404            = &smithyhttp.ResponseError{Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 404}}, Err: errors.New("no method found matching route")}

	iotCertSuppressionID   = "arn:aws:iot:us-east-1:123456789012:audit-suppression/DEVICE_CERTIFICATE_EXPIRING_CHECK/deviceCertificateId=c1"
	iotPolicySuppressionID = "arn:aws:iot:us-east-1:123456789012:audit-suppression/IOT_POLICY_OVERLY_PERMISSIVE_CHECK/" +
		"policyVersionIdentifier.policyName=p1&policyVersionIdentifier.policyVersionId=2"
)

// iotSuppressionPages serves two pages: a certificate suppression with every
// optional field, then a policy-version suppression plus two elements with no
// identity (nil checkName, nil resourceIdentifier). pageTwoErr, when set,
// replaces the second page. Every call's input is recorded.
func iotSuppressionPages(inputs *[]iot.ListAuditSuppressionsInput, mu *sync.Mutex, pageTwoErr error) func(in any) (any, error) {
	exp := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	return func(in any) (any, error) {
		req := in.(*iot.ListAuditSuppressionsInput)
		mu.Lock()
		*inputs = append(*inputs, *req)
		mu.Unlock()
		if req.NextToken == nil {
			return &iot.ListAuditSuppressionsOutput{Suppressions: []iottypes.AuditSuppression{{
				CheckName:          ptrStr("DEVICE_CERTIFICATE_EXPIRING_CHECK"),
				ResourceIdentifier: &iottypes.ResourceIdentifier{DeviceCertificateId: ptrStr("c1")},
				Description:        ptrStr("renewal scheduled"),
				ExpirationDate:     &exp,
			}}, NextToken: ptrStr("p2")}, nil
		}
		if pageTwoErr != nil {
			return nil, pageTwoErr
		}
		return &iot.ListAuditSuppressionsOutput{Suppressions: []iottypes.AuditSuppression{
			{
				CheckName: ptrStr("IOT_POLICY_OVERLY_PERMISSIVE_CHECK"),
				ResourceIdentifier: &iottypes.ResourceIdentifier{PolicyVersionIdentifier: &iottypes.PolicyVersionIdentifier{
					PolicyName: ptrStr("p1"), PolicyVersionId: ptrStr("2"),
				}},
				SuppressIndefinitely: sdkaws.Bool(true),
			},
			{ResourceIdentifier: &iottypes.ResourceIdentifier{DeviceCertificateId: ptrStr("c2")}},
			{CheckName: ptrStr("CA_CERTIFICATE_EXPIRING_CHECK")},
		}}, nil
	}
}

func scanIoTSuppressionsWith(t *testing.T, st *store.Store, handler func(in any) (any, error)) (int, error) {
	t.Helper()
	client := iot.NewFromConfig(opStubConfig(map[string]func(any) (any, error){"ListAuditSuppressions": handler}, iotDenied))
	total, _, err := scanIoTAuditSuppressions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

// Both pages are stored unfiltered at the documented page cap, and elements
// with no identity are skipped behind one warning.
func TestScanIoTAuditSuppressions_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, iotSuppressionOp)
	var (
		mu     sync.Mutex
		inputs []iot.ListAuditSuppressionsInput
	)
	total, err := scanIoTSuppressionsWith(t, st, iotSuppressionPages(&inputs, &mu, nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(inputs) != 2 {
		t.Fatalf("list calls = %d, want 2", len(inputs))
	}
	for _, in := range inputs {
		if in.CheckName != nil || in.ResourceIdentifier != nil {
			t.Errorf("list carried a filter: %+v", in)
		}
		if in.MaxResults == nil || *in.MaxResults != 250 {
			t.Errorf("MaxResults = %v, want 250", in.MaxResults)
		}
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the two unkeyed elements", *warns)
	}
	rows := rowsByNativeID(t, st, TypeIoTAuditSuppression)
	if got := slices.Sorted(maps.Keys(rows)); !slices.Equal(got, []string{iotCertSuppressionID, iotPolicySuppressionID}) {
		t.Fatalf("native ids = %v", got)
	}
	cert := rows[iotCertSuppressionID]
	if sv(cert.Region) != testRegion || sv(cert.Name) != "DEVICE_CERTIFICATE_EXPIRING_CHECK" || cert.ManagedByProvider {
		t.Errorf("cert row region=%q name=%q managed=%v", sv(cert.Region), sv(cert.Name), cert.ManagedByProvider)
	}
	if !strings.Contains(cert.AttributesJSON, `"ExpirationDate":"2026-12-01T00:00:00Z"`) {
		t.Errorf("attrs lack ExpirationDate: %s", cert.AttributesJSON)
	}
	if !strings.Contains(rows[iotPolicySuppressionID].AttributesJSON, `"SuppressIndefinitely":true`) {
		t.Errorf("attrs lack SuppressIndefinitely: %s", rows[iotPolicySuppressionID].AttributesJSON)
	}
}

func TestScanIoTAuditSuppressions_Empty(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, iotSuppressionOp)
	total, err := scanIoTSuppressionsWith(t, st, func(any) (any, error) { return &iot.ListAuditSuppressionsOutput{}, nil })
	if err != nil || total != 0 || *warns != 0 {
		t.Fatalf("total=%d err=%v warnings=%d, want 0, nil, 0", total, err, *warns)
	}
}

func TestScanIoTAuditSuppressions_Errors(t *testing.T) {
	cases := []struct {
		name       string
		firstErr   error // first page error; nil serves iotSuppressionPages
		pageTwoErr error
		wantTotal  int
		wantRows   int
		wantWarns  int
		wantErr    bool
	}{
		{name: "denied first page warns", firstErr: iotDenied, wantWarns: 1},
		{name: "denied later page keeps rows", pageTwoErr: iotDenied, wantTotal: 1, wantRows: 1, wantWarns: 1},
		{name: "404 first page is a region gap", firstErr: iot404},
		{name: "404 later page propagates", pageTwoErr: iot404, wantTotal: 1, wantRows: 1, wantErr: true},
		{name: "other error propagates", firstErr: iotInvalidRequest, wantErr: true},
		{name: "other error later page keeps rows", pageTwoErr: iotInvalidRequest, wantTotal: 1, wantRows: 1, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, iotSuppressionOp)
			var (
				mu     sync.Mutex
				inputs []iot.ListAuditSuppressionsInput
			)
			handler := iotSuppressionPages(&inputs, &mu, tc.pageTwoErr)
			if tc.firstErr != nil {
				handler = func(any) (any, error) { return nil, tc.firstErr }
			}
			total, err := scanIoTSuppressionsWith(t, st, handler)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			cause := tc.firstErr
			if cause == nil {
				cause = tc.pageTwoErr
			}
			if tc.wantErr && !errors.Is(err, cause) {
				t.Errorf("err %v does not wrap %v", err, cause)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
			if n := len(rowsByNativeID(t, st, TypeIoTAuditSuppression)); n != tc.wantRows {
				t.Errorf("rows = %d, want %d", n, tc.wantRows)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
		})
	}
}

func TestIoTResourceIdentifierKey(t *testing.T) {
	cases := []struct {
		name string
		ri   *iottypes.ResourceIdentifier
		want string
	}{
		{"nil", nil, ""},
		{"no member", &iottypes.ResourceIdentifier{}, ""},
		{"account", &iottypes.ResourceIdentifier{Account: ptrStr("123456789012")}, "account=123456789012"},
		{"ca certificate", &iottypes.ResourceIdentifier{CaCertificateId: ptrStr("ca1")}, "caCertificateId=ca1"},
		{"client id", &iottypes.ResourceIdentifier{ClientId: ptrStr("dev/1")}, "clientId=dev%2F1"},
		{"cognito pool", &iottypes.ResourceIdentifier{CognitoIdentityPoolId: ptrStr("us-east-1:abc")}, "cognitoIdentityPoolId=us-east-1%3Aabc"},
		{
			"device certificate arn", &iottypes.ResourceIdentifier{DeviceCertificateArn: ptrStr("arn:aws:iot:us-east-1:123456789012:cert/c1")},
			"deviceCertificateArn=arn%3Aaws%3Aiot%3Aus-east-1%3A123456789012%3Acert%2Fc1",
		},
		{"device certificate id", &iottypes.ResourceIdentifier{DeviceCertificateId: ptrStr("c1")}, "deviceCertificateId=c1"},
		{
			"iam role", &iottypes.ResourceIdentifier{IamRoleArn: ptrStr("arn:aws:iam::123456789012:role/r")},
			"iamRoleArn=arn%3Aaws%3Aiam%3A%3A123456789012%3Arole%2Fr",
		},
		{
			"role alias", &iottypes.ResourceIdentifier{RoleAliasArn: ptrStr("arn:aws:iot:us-east-1:123456789012:rolealias/a")},
			"roleAliasArn=arn%3Aaws%3Aiot%3Aus-east-1%3A123456789012%3Arolealias%2Fa",
		},
		{
			"policy version", &iottypes.ResourceIdentifier{PolicyVersionIdentifier: &iottypes.PolicyVersionIdentifier{PolicyName: ptrStr("p1"), PolicyVersionId: ptrStr("2")}},
			"policyVersionIdentifier.policyName=p1&policyVersionIdentifier.policyVersionId=2",
		},
		{"issuer certificate", &iottypes.ResourceIdentifier{IssuerCertificateIdentifier: &iottypes.IssuerCertificateIdentifier{
			IssuerCertificateSerialNumber: ptrStr("0a:01"), IssuerCertificateSubject: ptrStr("CN=x/O=y&z"), IssuerId: ptrStr("ff"),
		}}, "issuerCertificateIdentifier.issuerCertificateSerialNumber=0a%3A01&issuerCertificateIdentifier.issuerCertificateSubject=CN%3Dx%2FO%3Dy%26z&issuerCertificateIdentifier.issuerId=ff"},
		{"empty nested", &iottypes.ResourceIdentifier{PolicyVersionIdentifier: &iottypes.PolicyVersionIdentifier{}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := iotResourceIdentifierKey(tc.ri); got != tc.want {
				t.Errorf("key = %q, want %q", got, tc.want)
			}
		})
	}
}

// A check name containing "/" is escaped, so it cannot shift the boundary
// between the check and the identifier.
func TestIoTAuditSuppressionNativeID_EscapesCheckName(t *testing.T) {
	got := iotAuditSuppressionNativeID(testRegion, testAccountID, iottypes.AuditSuppression{
		CheckName:          ptrStr("A/B"),
		ResourceIdentifier: &iottypes.ResourceIdentifier{ClientId: ptrStr("c")},
	})
	if want := "arn:aws:iot:us-east-1:123456789012:audit-suppression/A%2FB/clientId=c"; got != want {
		t.Errorf("native id = %q, want %q", got, want)
	}
}

// The suppression phase runs after every IoT family has finished, and its
// error neither hides the families' rows nor is lost behind theirs.
func TestScanIoT_AuditSuppressionsRunLast(t *testing.T) {
	familyOps := []string{
		"DescribeAccountAuditConfiguration", "ListScheduledAudits", "ListMitigationActions", "ListSecurityProfiles",
		"ListCustomMetrics", "ListDimensions", "ListThings", "ListThingTypes", "ListThingGroups", "ListBillingGroups",
		"ListCertificates", "ListCACertificates", "ListPolicies", "ListRoleAliases", "ListAuthorizers",
		"ListDomainConfigurations", "ListCertificateProviders", "ListJobs", "ListJobTemplates", "ListCommands",
		"ListOTAUpdates", "ListStreams", "ListFleetMetrics", "ListProvisioningTemplates", "ListIndices",
		"ListPackages", "ListTopicRules", "ListTopicRuleDestinations",
	}
	var (
		mu         sync.Mutex
		called     = map[string]bool{}
		beforeMine []string
	)
	handlers := map[string]func(any) (any, error){}
	for _, op := range familyOps {
		handlers[op] = func(any) (any, error) {
			mu.Lock()
			called[op] = true
			mu.Unlock()
			if op == "DescribeAccountAuditConfiguration" {
				return &iot.DescribeAccountAuditConfigurationOutput{RoleArn: ptrStr("arn:aws:iam::123456789012:role/audit")}, nil
			}
			if op == "ListDimensions" {
				return nil, iotFamily
			}
			return nil, iotDenied
		}
	}
	var inputs []iot.ListAuditSuppressionsInput
	pages := iotSuppressionPages(&inputs, &mu, iotInvalidRequest)
	handlers["ListAuditSuppressions"] = func(in any) (any, error) {
		mu.Lock()
		if beforeMine == nil {
			beforeMine = slices.Sorted(maps.Keys(called))
		}
		mu.Unlock()
		return pages(in)
	}
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, iotDenied)

	total, _, err := scanIoT(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, iotFamily) || !errors.Is(err, iotInvalidRequest) {
		t.Fatalf("err = %v, want both the family and the suppression error", err)
	}
	if want := slices.Sorted(maps.Keys(called)); !slices.Equal(beforeMine, want) {
		t.Errorf("ops called before ListAuditSuppressions = %v, want every family op %v", beforeMine, want)
	}
	if len(rowsByNativeID(t, st, TypeIoTAccountAuditConfiguration)) != 1 {
		t.Error("family row missing")
	}
	if _, ok := rowsByNativeID(t, st, TypeIoTAuditSuppression)[iotCertSuppressionID]; !ok {
		t.Error("suppression row missing")
	}
	// One audit-configuration row plus the suppression stored before page two failed.
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
}

// Every exported member of the SDK's identifier structs is encoded, so an SDK
// bump that adds one fails here instead of letting suppressions share a key.
func TestIoTResourceIdentifierKey_EncodesEveryMember(t *testing.T) {
	var want []string
	ri := &iottypes.ResourceIdentifier{}
	fillStringMembers(t, reflect.ValueOf(ri).Elem(), "", &want)
	q, err := url.ParseQuery(iotResourceIdentifierKey(ri))
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	slices.Sort(want)
	if got := slices.Sorted(maps.Keys(q)); !slices.Equal(got, want) {
		t.Errorf("encoded members = %v, want %v", got, want)
	}
}

// fillStringMembers sets every exported *string field of v (recursing into
// struct pointers) and appends its wire name, lower-camel and dotted under
// its parent, to names.
func fillStringMembers(t *testing.T, v reflect.Value, prefix string, names *[]string) {
	t.Helper()
	for i := range v.NumField() {
		f := v.Type().Field(i)
		if !f.IsExported() {
			continue
		}
		name := prefix + strings.ToLower(f.Name[:1]) + f.Name[1:]
		switch {
		case f.Type == reflect.TypeFor[*string]():
			v.Field(i).Set(reflect.ValueOf(ptrStr("v")))
			*names = append(*names, name)
		case f.Type.Kind() == reflect.Pointer && f.Type.Elem().Kind() == reflect.Struct:
			v.Field(i).Set(reflect.New(f.Type.Elem()))
			fillStringMembers(t, v.Field(i).Elem(), name+".", names)
		default:
			t.Fatalf("member %s has unhandled type %s", name, f.Type)
		}
	}
}
