package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/apprunner"
	apprunnertypes "github.com/aws/aws-sdk-go-v2/service/apprunner/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/store"
)

var appRunnerDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: apprunner:List")

func appRunnerServiceARN(name string) string {
	return fmt.Sprintf("arn:aws:apprunner:%s:%s:service/%s/0123456789abcdef", testRegion, testAccountID, name)
}

// appRunnerServices stores a service row for each name and returns them as parents.
func appRunnerServices(t *testing.T, st *store.Store, names ...string) []childParent {
	t.Helper()
	services := make([]childParent, len(names))
	for i, name := range names {
		arn := appRunnerServiceARN(name)
		services[i] = childParent{id: arn, arn: arn}
		upsertTestResource(t, st, "aws", testAccountID, TypeAppRunnerService, arn, testRegion, "{}")
	}
	return services
}

func appRunnerDomain(name string) apprunnertypes.CustomDomain {
	return apprunnertypes.CustomDomain{
		DomainName: sdkaws.String(name), EnableWWWSubdomain: sdkaws.Bool(true),
		Status: apprunnertypes.CustomDomainAssociationStatusActive,
	}
}

// appRunnerDomainsHandler answers DescribeCustomDomains from pages keyed by
// service ARN: the first page of a service carries NextToken "p2" when it has
// a second. errs fails a service's call with the given error; a "/p2" key
// fails only its second page. A call whose MaxResults is not the model cap
// fails, so the page size is pinned.
func appRunnerDomainsHandler(calls *parentCalls, pages map[string][][]apprunnertypes.CustomDomain, errs map[string]error) func(any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*apprunner.DescribeCustomDomainsInput)
		if sdkaws.ToInt32(req.MaxResults) != appRunnerCustomDomainPageLimit {
			return nil, fmt.Errorf("MaxResults = %v; want %d", req.MaxResults, appRunnerCustomDomainPageLimit)
		}
		arn := sdkaws.ToString(req.ServiceArn)
		calls.add(arn)
		page := 0
		if sdkaws.ToString(req.NextToken) == "p2" {
			page = 1
			if err := errs[arn+"/p2"]; err != nil {
				return nil, err
			}
		} else if err := errs[arn]; err != nil {
			return nil, err
		}
		out := &apprunner.DescribeCustomDomainsOutput{ServiceArn: &arn, DNSTarget: sdkaws.String("abc.us-east-1.awsapprunner.com")}
		if page < len(pages[arn]) {
			out.CustomDomains = pages[arn][page]
		}
		if page == 0 && len(pages[arn]) > 1 {
			out.NextToken = sdkaws.String("p2")
		}
		return out, nil
	}
}

func newAppRunnerTestClient(handlers map[string]func(in any) (any, error)) *apprunner.Client {
	return apprunner.NewFromConfig(opStubConfig(handlers, appRunnerDenied))
}

func TestScanAppRunnerCustomDomains_PagesAndFields(t *testing.T) {
	st := newTestStore(t)
	svc1, svc2, svc3 := appRunnerServiceARN("svc1"), appRunnerServiceARN("svc2"), appRunnerServiceARN("svc3")
	first := appRunnerDomain("example.com")
	first.Status = apprunnertypes.CustomDomainAssociationStatusPendingCertificateDnsValidation
	first.CertificateValidationRecords = []apprunnertypes.CertificateValidationRecord{{
		Name: sdkaws.String("_x.example.com."), Type: sdkaws.String("CNAME"), Value: sdkaws.String("_y.acm-validations.aws."),
	}}
	var calls parentCalls
	client := newAppRunnerTestClient(map[string]func(any) (any, error){
		"DescribeCustomDomains": appRunnerDomainsHandler(&calls, map[string][][]apprunnertypes.CustomDomain{
			svc1: {{first, {}}, {appRunnerDomain("*.example.com")}},
			svc2: {{appRunnerDomain("login.example.org")}},
		}, nil),
	})

	total, _, err := scanAppRunnerCustomDomains(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, appRunnerServices(t, st, "svc1", "svc2", "svc3"))
	if err != nil {
		t.Fatalf("scanAppRunnerCustomDomains: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3", total)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{svc1, svc2, svc3}) {
		t.Errorf("services queried = %v; want all three", got)
	}
	rows := rowsByNativeID(t, st, TypeAppRunnerCustomDomain)
	if len(rows) != 3 {
		t.Fatalf("rows = %d; want 3 (the nameless element skipped): %v", len(rows), rows)
	}
	firstID := svc1 + "/custom-domain/example.com"
	for _, want := range []struct{ parent, id string }{
		{svc1, firstID},
		{svc1, svc1 + "/custom-domain/*.example.com"},
		{svc2, svc2 + "/custom-domain/login.example.org"},
	} {
		r, ok := rows[want.id]
		if !ok {
			t.Errorf("missing custom domain %s", want.id)
			continue
		}
		if r.Type != TypeAppRunnerCustomDomain || sv(r.Region) != testRegion {
			t.Errorf("%s: Type %q Region %q", want.id, r.Type, sv(r.Region))
		}
		assertContainsNative(t, st, want.parent, want.id)
	}
	r := rows[firstID]
	if sv(r.Name) != "example.com" || sv(r.Status) != "PENDING_CERTIFICATE_DNS_VALIDATION" {
		t.Errorf("Name, Status = %q, %q; want example.com, PENDING_CERTIFICATE_DNS_VALIDATION", sv(r.Name), sv(r.Status))
	}
	for _, want := range []string{`"EnableWWWSubdomain":true`, `"Value":"_y.acm-validations.aws."`} {
		if !strings.Contains(r.AttributesJSON, want) {
			t.Errorf("attrs = %s; want %s", r.AttributesJSON, want)
		}
	}
	if strings.Contains(r.AttributesJSON, "DNSTarget") {
		t.Errorf("attrs = %s; the service-level DNSTarget must not be on a domain row", r.AttributesJSON)
	}
}

func TestScanAppRunnerCustomDomains_Empty(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	client := newAppRunnerTestClient(map[string]func(any) (any, error){
		"DescribeCustomDomains": appRunnerDomainsHandler(&calls, nil, nil),
	})
	total, _, err := scanAppRunnerCustomDomains(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, appRunnerServices(t, st, "svc1"))
	if err != nil || total != 0 {
		t.Errorf("total, err = %d, %v; want 0, nil", total, err)
	}
	if n := len(rowsByNativeID(t, st, TypeAppRunnerCustomDomain)); n != 0 {
		t.Errorf("rows = %d; want 0", n)
	}
}

// Each per-service error is skipped or returned by its code. Two of three
// services fail, so a skip that warns must warn once for both.
func TestScanAppRunnerCustomDomains_PerServiceErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantErr   string
		wantWarns int
	}{
		{name: "denied warns once", err: appRunnerDenied, wantWarns: 1},
		{name: "throttled warns once", err: apiErr("ThrottlingException", "Rate exceeded"), wantWarns: 1},
		{name: "invalid request warns once", err: apiErr("InvalidRequestException", "service not ready"), wantWarns: 1},
		{name: "service deleted since listing skipped silently", err: apiErr("ResourceNotFoundException", "gone")},
		{name: "other code fails the phase", err: apiErr("ConflictException", "bad"), wantErr: "ConflictException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, appRunnerDescribeCustomDomainsOp)
			svc1, svc2, svc3 := appRunnerServiceARN("svc1"), appRunnerServiceARN("svc2"), appRunnerServiceARN("svc3")
			var calls parentCalls
			client := newAppRunnerTestClient(map[string]func(any) (any, error){
				"DescribeCustomDomains": appRunnerDomainsHandler(&calls, map[string][][]apprunnertypes.CustomDomain{
					svc3: {{appRunnerDomain("example.com")}},
				}, map[string]error{svc1: tc.err, svc2: tc.err}),
			})
			total, _, err := scanAppRunnerCustomDomains(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, appRunnerServices(t, st, "svc1", "svc2", "svc3"))
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
			if _, ok := rowsByNativeID(t, st, TypeAppRunnerCustomDomain)[svc3+"/custom-domain/example.com"]; !ok || total != 1 {
				t.Errorf("total = %d, sibling row stored = %v; want 1, true", total, ok)
			}
		})
	}
}

// A skipped service keeps the rows its earlier pages stored.
func TestScanAppRunnerCustomDomains_SkipKeepsEarlierPages(t *testing.T) {
	st := newTestStore(t)
	svc1 := appRunnerServiceARN("svc1")
	var calls parentCalls
	client := newAppRunnerTestClient(map[string]func(any) (any, error){
		"DescribeCustomDomains": appRunnerDomainsHandler(&calls, map[string][][]apprunnertypes.CustomDomain{
			svc1: {{appRunnerDomain("a.example.com")}, {appRunnerDomain("b.example.com")}},
		}, map[string]error{svc1 + "/p2": apiErr("InvalidRequestException", "x")}),
	})
	total, _, err := scanAppRunnerCustomDomains(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, appRunnerServices(t, st, "svc1"))
	if err != nil || total != 1 {
		t.Errorf("total, err = %d, %v; want 1, nil", total, err)
	}
	if _, ok := rowsByNativeID(t, st, TypeAppRunnerCustomDomain)[svc1+"/custom-domain/a.example.com"]; !ok {
		t.Error("first-page row not kept")
	}
}

// The listing stops when the service repeats the token it was sent, rather
// than looping on it.
func TestScanAppRunnerCustomDomains_RepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	var n atomic.Int64
	client := newAppRunnerTestClient(map[string]func(any) (any, error){
		"DescribeCustomDomains": func(any) (any, error) {
			if n.Add(1) > 5 {
				return nil, apiErr("ConflictException", "looped")
			}
			return &apprunner.DescribeCustomDomainsOutput{NextToken: sdkaws.String("same")}, nil
		},
	})
	_, _, err := scanAppRunnerCustomDomains(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, appRunnerServices(t, st, "svc1"))
	if err != nil || n.Load() != 2 {
		t.Errorf("err, calls = %v, %d; want nil, 2", err, n.Load())
	}
}

// appRunnerTestTotal is the rows of the fixture's scan: each of the six
// tagged listings stores two, one per page; no service has a custom domain.
const appRunnerTestTotal = 6 * 2

var (
	appRunnerThrottle = apiErr("ThrottlingException", "Rate exceeded")
	// appRunnerInvalidState is a code ListTagsForResource models that is
	// neither a not-found nor a denial.
	appRunnerInvalidState = apiErr("InvalidStateException", "busy")
	appRunnerCreatedAt    = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
)

// appRunnerKind is one listing whose rows have their tags read: its list op,
// the type it stores, the Service Reference resource type of its ARNs, the
// MaxResults it must send, the Status and CreatedAt its rows carry ("" for
// none), a fragment of the stored attributes, and page, which
// builds the op's output holding one element under arn named name, with
// next.
type appRunnerKind struct {
	op, typ, arnType string
	maxResults       int32
	status           string
	createdAt        string
	attr             func(arn string) string
	page             func(arn, name, next *string) any
}

func (k appRunnerKind) name(suffix string) string { return k.arnType + "-" + suffix }

// arn is the row's ARN in the Service Reference's format for k's type.
func (k appRunnerKind) arn(suffix string) string {
	rest := k.name(suffix) + "/0123456789abcdef"
	switch k.arnType {
	case "vpcconnector", "autoscalingconfiguration", "observabilityconfiguration":
		rest = k.name(suffix) + "/1/0123456789abcdef"
	}
	return "arn:aws:apprunner:" + testRegion + ":" + testAccountID + ":" + k.arnType + "/" + rest
}

// rowName is the Name a row of k is stored under: a VPC ingress connection
// has no name and is named by its ARN.
func (k appRunnerKind) rowName(suffix string) string {
	if k.typ == TypeAppRunnerVpcIngressConnection {
		return k.arn(suffix)
	}
	return k.name(suffix)
}

func appRunnerKinds() []appRunnerKind {
	arnAttr := func(field string) func(string) string {
		return func(arn string) string { return `"` + field + `":"` + arn + `"` }
	}
	return []appRunnerKind{
		{
			op: "ListServices", typ: TypeAppRunnerService, arnType: "service", maxResults: 20, status: "RUNNING",
			// The DescribeService body, not the ServiceSummary.
			attr: func(string) string { return `"Cpu":"1 vCPU"` },
			page: func(arn, name, next *string) any {
				return &apprunner.ListServicesOutput{NextToken: next, ServiceSummaryList: []apprunnertypes.ServiceSummary{{
					ServiceArn: arn, ServiceName: name, Status: apprunnertypes.ServiceStatusRunning,
				}}}
			},
		},
		{
			op: "ListVpcConnectors", typ: TypeAppRunnerVPCConnector, arnType: "vpcconnector", maxResults: 100, status: "ACTIVE",
			attr: arnAttr("VpcConnectorArn"),
			page: func(arn, name, next *string) any {
				return &apprunner.ListVpcConnectorsOutput{NextToken: next, VpcConnectors: []apprunnertypes.VpcConnector{{
					VpcConnectorArn: arn, VpcConnectorName: name, Status: apprunnertypes.VpcConnectorStatusActive, Subnets: []string{"subnet-1"},
				}}}
			},
		},
		{
			op: "ListAutoScalingConfigurations", typ: TypeAppRunnerAutoScalingConfiguration, arnType: "autoscalingconfiguration", maxResults: 100,
			status: "ACTIVE", createdAt: "2026-01-02T03:04:05Z",
			attr: arnAttr("AutoScalingConfigurationArn"),
			page: func(arn, name, next *string) any {
				return &apprunner.ListAutoScalingConfigurationsOutput{NextToken: next, AutoScalingConfigurationSummaryList: []apprunnertypes.AutoScalingConfigurationSummary{{
					AutoScalingConfigurationArn: arn, AutoScalingConfigurationName: name, Status: apprunnertypes.AutoScalingConfigurationStatusActive,
					CreatedAt: &appRunnerCreatedAt,
				}}}
			},
		},
		{
			op: "ListObservabilityConfigurations", typ: TypeAppRunnerObservabilityConfiguration, arnType: "observabilityconfiguration", maxResults: 100,
			attr: arnAttr("ObservabilityConfigurationArn"),
			page: func(arn, name, next *string) any {
				return &apprunner.ListObservabilityConfigurationsOutput{NextToken: next, ObservabilityConfigurationSummaryList: []apprunnertypes.ObservabilityConfigurationSummary{{
					ObservabilityConfigurationArn: arn, ObservabilityConfigurationName: name,
				}}}
			},
		},
		{
			op: "ListVpcIngressConnections", typ: TypeAppRunnerVpcIngressConnection, arnType: "vpcingressconnection", maxResults: 100,
			attr: arnAttr("VpcIngressConnectionArn"),
			page: func(arn, _, next *string) any {
				return &apprunner.ListVpcIngressConnectionsOutput{NextToken: next, VpcIngressConnectionSummaryList: []apprunnertypes.VpcIngressConnectionSummary{{
					VpcIngressConnectionArn: arn, ServiceArn: sdkaws.String(appRunnerServiceARN("svc1")),
				}}}
			},
		},
		{
			op: "ListConnections", typ: TypeAppRunnerConnection, arnType: "connection", maxResults: 100, createdAt: "2026-01-02T03:04:05Z",
			attr: arnAttr("ConnectionArn"),
			page: func(arn, name, next *string) any {
				return &apprunner.ListConnectionsOutput{NextToken: next, ConnectionSummaryList: []apprunnertypes.ConnectionSummary{{
					ConnectionArn: arn, ConnectionName: name, CreatedAt: &appRunnerCreatedAt,
				}}}
			},
		},
	}
}

func appRunnerKindOf(t *testing.T, op string) appRunnerKind {
	t.Helper()
	for _, k := range appRunnerKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no tagged App Runner listing", op)
	return appRunnerKind{}
}

// appRunnerCalls records the calls of a scan and answers ListTagsForResource
// with tags, or errs, by ARN.
type appRunnerCalls struct {
	mu      sync.Mutex
	inputs  map[string][]any
	tagARNs []string
	tags    map[string]map[string]string
	errs    map[string]error
}

// newAppRunnerCalls tags the "tagged" row of every kind with its type.
func newAppRunnerCalls() *appRunnerCalls {
	c := &appRunnerCalls{inputs: map[string][]any{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range appRunnerKinds() {
		c.tags[k.arn("tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

func (c *appRunnerCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inputs[op] = append(c.inputs[op], in)
}

func (c *appRunnerCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.inputs[op])
}

func (c *appRunnerCalls) listTags(in any) (any, error) {
	arn := sv(in.(*apprunner.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var list []apprunnertypes.Tag
	for k, v := range tags {
		list = append(list, apprunnertypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &apprunner.ListTagsForResourceOutput{Tags: list}, nil
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second.
func (c *appRunnerCalls) twoPages(k appRunnerKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("t2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		arn, name := k.arn(suffix), k.name(suffix)
		return k.page(&arn, &name, next), nil
	}
}

// appRunnerServiceBody is the DescribeService body of the service arn: it
// carries the listed name and status and an InstanceConfiguration the
// summary lacks.
func appRunnerServiceBody(arn string) *apprunnertypes.Service {
	_, rest, _ := strings.Cut(arn, ":service/")
	name, _, _ := strings.Cut(rest, "/")
	return &apprunnertypes.Service{
		ServiceArn: &arn, ServiceName: &name, Status: apprunnertypes.ServiceStatusRunning,
		InstanceConfiguration: &apprunnertypes.InstanceConfiguration{Cpu: sdkaws.String("1 vCPU")},
	}
}

// describeService answers DescribeService with appRunnerServiceBody.
func (c *appRunnerCalls) describeService(in any) (any, error) {
	c.record("DescribeService", in)
	return &apprunner.DescribeServiceOutput{Service: appRunnerServiceBody(sv(in.(*apprunner.DescribeServiceInput).ServiceArn))}, nil
}

// appRunnerHandlers answers every App Runner op the scan calls: each tagged
// kind in two pages, and no custom domains.
func appRunnerHandlers(c *appRunnerCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"ListTagsForResource": c.listTags,
		"DescribeService":     c.describeService,
		"DescribeCustomDomains": func(in any) (any, error) {
			c.record("DescribeCustomDomains", in)
			return &apprunner.DescribeCustomDomainsOutput{}, nil
		},
	}
	for _, k := range appRunnerKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// appRunnerScan runs the scan's real entry point against handlers; every op
// they do not answer is denied.
func appRunnerScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, appRunnerDenied)
	return scanAppRunner(ctx, acct, testRegion, st, testScanID)
}

// appRunnerTagReadARNs is the ARN of every row whose tags the fixture's scan
// reads: all of them.
func appRunnerTagReadARNs() []string {
	var arns []string
	for _, k := range appRunnerKinds() {
		arns = append(arns, k.arn("tagged"), k.arn("bare"))
	}
	slices.Sort(arns)
	return arns
}

// Every tagged listing stores both its pages, each row under its ARN with
// its Name, Status, CreatedAt and attributes (a service's from its
// DescribeService body), unmanaged, and the tags ListTagsForResource returns
// for that ARN, nil for a resource without tags. Exactly those ARNs are sent
// to the tag op; each listing sends its model's page cap. No error, no
// warning.
func TestScanAppRunner_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newAppRunnerCalls()

	total, inserted, err := appRunnerScan(context.Background(), st, appRunnerHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != appRunnerTestTotal || inserted != appRunnerTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, appRunnerTestTotal, appRunnerTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range appRunnerKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %v; want 2", k.typ, nativeIDs(rows))
		}
		for _, suffix := range []string{"tagged", "bare"} {
			arn := k.arn(suffix)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			wantStatus := (*string)(nil)
			if k.status != "" {
				wantStatus = &k.status
			}
			if sv(r.Name) != k.rowName(suffix) || sv(r.Region) != testRegion || !reflect.DeepEqual(r.Status, wantStatus) || r.ManagedByProvider {
				t.Errorf("%s Name, Region, Status, Managed = %q, %q, %v, %v; want %q, %q, %v, false",
					arn, sv(r.Name), sv(r.Region), r.Status, r.ManagedByProvider, k.rowName(suffix), testRegion, wantStatus)
			}
			if (r.CreatedAt == nil) != (k.createdAt == "") || sv(r.CreatedAt) != k.createdAt {
				t.Errorf("%s CreatedAt = %v; want %q", arn, r.CreatedAt, k.createdAt)
			}
			if !strings.Contains(r.AttributesJSON, k.attr(arn)) {
				t.Errorf("%s attrs = %s; want %s", arn, r.AttributesJSON, k.attr(arn))
			}
			if k.typ == TypeAppRunnerService {
				assertJSONEqual(t, arn+" attrs (the DescribeService body itself)", r.AttributesJSON, mustJSON(appRunnerServiceBody(arn)))
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		for _, in := range calls.inputs[k.op] {
			if n := bacField[*int32](in, "MaxResults"); n == nil || *n != k.maxResults {
				t.Errorf("%s MaxResults = %v; want %d", k.op, n, k.maxResults)
			}
		}
		if n := calls.count(k.op); n != 2 {
			t.Errorf("%s calls = %d; want 2", k.op, n)
		}
	}
	if got := slices.Sorted(slices.Values(calls.tagARNs)); !slices.Equal(got, appRunnerTagReadARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, appRunnerTagReadARNs())
	}
	svcKind := appRunnerKindOf(t, "ListServices")
	if got := calls.count("DescribeCustomDomains"); got != 2 {
		t.Errorf("DescribeCustomDomains calls = %d; want 2, one per stored service (%s, %s)", got, svcKind.arn("tagged"), svcKind.arn("bare"))
	}
}

// Every listing empty: nothing stored, no tag read, no error, no warning.
func TestScanAppRunner_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newAppRunnerCalls()
	handlers := appRunnerHandlers(calls)
	handlers["ListServices"] = func(any) (any, error) { return &apprunner.ListServicesOutput{}, nil }
	handlers["ListVpcConnectors"] = func(any) (any, error) { return &apprunner.ListVpcConnectorsOutput{}, nil }
	handlers["ListAutoScalingConfigurations"] = func(any) (any, error) { return &apprunner.ListAutoScalingConfigurationsOutput{}, nil }
	handlers["ListObservabilityConfigurations"] = func(any) (any, error) { return &apprunner.ListObservabilityConfigurationsOutput{}, nil }
	handlers["ListVpcIngressConnections"] = func(any) (any, error) { return &apprunner.ListVpcIngressConnectionsOutput{}, nil }
	handlers["ListConnections"] = func(any) (any, error) { return &apprunner.ListConnectionsOutput{}, nil }
	total, inserted, err := appRunnerScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Errorf("total, inserted, err = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if n := calls.count("ListTagsForResource"); n != 0 {
		t.Errorf("ListTagsForResource calls = %d; want 0", n)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanAppRunner_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := appRunnerScan(context.Background(), st, appRunnerHandlers(newAppRunnerCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := appRunnerScan(context.Background(), st, appRunnerHandlers(newAppRunnerCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != appRunnerTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, appRunnerTestTotal)
	}
}

// A failed tag read: ResourceNotFoundException drops the row silently; a
// denial keeps the row untagged and warns once under the tag op; a throttle
// keeps it and is transient; any other modeled code keeps it and is an error.
func TestScanAppRunner_TagReadFailures(t *testing.T) {
	type tagCase struct {
		name      string
		arn, typ  string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		wantWarns int
	}
	var cases []tagCase
	for _, k := range appRunnerKinds() {
		arn := k.arn("tagged")
		cases = append(cases,
			tagCase{k.typ + "/not found", arn, k.typ, apiErr("ResourceNotFoundException", "gone"), -1, "", appRunnerTestTotal - 1, false, 0},
			tagCase{k.typ + "/denied", arn, k.typ, appRunnerDenied, -1, "", appRunnerTestTotal, true, 1},
			tagCase{k.typ + "/transient", arn, k.typ, appRunnerThrottle, outcomeTransient, "ThrottlingException", appRunnerTestTotal, true, 0},
			tagCase{k.typ + "/other error", arn, k.typ, appRunnerInvalidState, outcomeError, "InvalidStateException", appRunnerTestTotal, true, 0},
		)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newAppRunnerCalls()
			calls.errs[c.arn] = c.err

			total, _, err := appRunnerScan(context.Background(), st, appRunnerHandlers(calls))
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
			if len(w) != c.wantWarns {
				t.Fatalf("warnings = %v; want %d", w, c.wantWarns)
			}
			for _, x := range w {
				if x.Service != appRunnerTagsOp || x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, c.arn) {
					t.Errorf("warning %+v; want one under %s in %s/%s naming %s", x, appRunnerTagsOp, testAccountID, testRegion, c.arn)
				}
			}
		})
	}
}

// A service whose tag read finds it gone is not stored, so its custom
// domains are not listed.
func TestScanAppRunner_ServiceGoneAtTagReadHasNoDomainCall(t *testing.T) {
	st := newTestStore(t)
	calls := newAppRunnerCalls()
	gone := appRunnerKindOf(t, "ListServices").arn("tagged")
	calls.errs[gone] = apiErr("ResourceNotFoundException", "gone")

	if _, _, err := appRunnerScan(context.Background(), st, appRunnerHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, in := range calls.inputs["DescribeCustomDomains"] {
		if arn := sv(in.(*apprunner.DescribeCustomDomainsInput).ServiceArn); arn == gone {
			t.Errorf("custom domains of %s listed; it was not stored", gone)
		}
	}
	if n := calls.count("DescribeCustomDomains"); n != 1 {
		t.Errorf("DescribeCustomDomains calls = %d; want 1", n)
	}
}

// Denied tag reads in two listings send one warning for the whole scan,
// under the tag op, naming the first denied row; the rows are kept untagged.
func TestScanAppRunner_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, appRunnerTagsOp)
	calls := newAppRunnerCalls()
	ops := []string{"ListVpcConnectors", "ListConnections"}
	for _, op := range ops {
		k := appRunnerKindOf(t, op)
		calls.errs[k.arn("tagged")] = appRunnerDenied
		calls.errs[k.arn("bare")] = appRunnerDenied
	}

	total, _, err := appRunnerScan(context.Background(), st, appRunnerHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != appRunnerTestTotal {
		t.Errorf("total = %d; want %d", total, appRunnerTestTotal)
	}
	first := appRunnerKindOf(t, "ListVpcConnectors").arn("tagged")
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion || !strings.Contains((*warnings)[0].Message, first) {
		t.Fatalf("%s warnings = %v; want 1 in %s/%s naming %s", appRunnerTagsOp, *warnings, testAccountID, testRegion, first)
	}
	for _, op := range ops {
		k := appRunnerKindOf(t, op)
		for _, arn := range []string{k.arn("tagged"), k.arn("bare")} {
			if r, ok := rowsByNativeID(t, st, k.typ)[arn]; !ok || r.TagsJSON != nil {
				t.Errorf("%s stored %v; want stored untagged", arn, ok)
			}
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to one
// warning, so the hard error decides the outcome.
func TestScanAppRunner_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, appRunnerTagsOp)
	calls := newAppRunnerCalls()
	calls.errs[appRunnerKindOf(t, "ListServices").arn("tagged")] = appRunnerThrottle
	calls.errs[appRunnerKindOf(t, "ListConnections").arn("bare")] = appRunnerInvalidState

	_, _, err := appRunnerScan(context.Background(), st, appRunnerHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidStateException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", appRunnerTagsOp, *warns)
	}
}

// A failed write stops the scan: no later phase runs and no warning is sent
// about rows the write lost; the phases before it keep their rows.
func TestScanAppRunner_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeAppRunnerAutoScalingConfiguration)
	calls := newAppRunnerCalls()
	k := appRunnerKindOf(t, "ListAutoScalingConfigurations")
	calls.errs[k.arn("tagged")] = appRunnerDenied

	total, _, err := appRunnerScan(context.Background(), st, appRunnerHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count(k.op); n != 1 {
		t.Errorf("%s called %d times; want 1, none after the failed write", k.op, n)
	}
	for _, op := range []string{"ListObservabilityConfigurations", "ListVpcIngressConnections", "ListConnections", "DescribeCustomDomains"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write; want 0", op, n)
		}
	}
	// Services and VPC connectors (2 each) ran before it.
	if total != 2*2 {
		t.Errorf("total = %d; want %d", total, 2*2)
	}
}

// A listing's failed page keeps the page read before it, and its error does
// not stop the later listings or the custom-domain phase.
func TestScanAppRunner_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, op := range []string{"ListServices", "ListVpcConnectors"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newAppRunnerCalls()
			handlers := appRunnerHandlers(calls)
			k := appRunnerKindOf(t, op)
			handlers[k.op] = failOnCall(2, appRunnerInvalidState, handlers[k.op])

			total, _, err := appRunnerScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidStateException")
			if total != appRunnerTestTotal-1 {
				t.Errorf("total = %d; want %d", total, appRunnerTestTotal-1)
			}
			if _, ok := rowsByNativeID(t, st, k.typ)[k.arn("tagged")]; !ok {
				t.Errorf("%s from the page before the failure missing", k.typ)
			}
			if got := len(rowsByNativeID(t, st, TypeAppRunnerConnection)); got != 2 {
				t.Errorf("connections (the last listing) = %d; want 2", got)
			}
			if calls.count("DescribeCustomDomains") == 0 {
				t.Error("custom-domain phase did not run")
			}
		})
	}
}

// A repeated token ends a listing as an error, keeping its pages.
func TestScanAppRunner_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := appRunnerHandlers(newAppRunnerCalls())
	k := appRunnerKindOf(t, "ListObservabilityConfigurations")
	handlers[k.op] = func(in any) (any, error) {
		suffix := "tagged"
		if bacField[*string](in, "NextToken") != nil {
			suffix = "bare"
		}
		arn, name := k.arn(suffix), k.name(suffix)
		return k.page(&arn, &name, sdkaws.String("same")), nil
	}
	total, _, err := appRunnerScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != appRunnerTestTotal {
		t.Errorf("total = %d; want %d", total, appRunnerTestTotal)
	}
}

// An element without an ARN is not stored, is not sent to the tag op, and is
// warned about once under its listing, named by its name or by the fallback
// text when it has none.
func TestScanAppRunner_ElementWithoutARNWarns(t *testing.T) {
	for _, c := range []struct{ op, key string }{
		{"ListServices", "nameless-arn"},
		{"ListVpcConnectors", "nameless-arn"},
		{"ListAutoScalingConfigurations", "nameless-arn"},
		{"ListObservabilityConfigurations", "nameless-arn"},
		{"ListVpcIngressConnections", noIdentifier},
		{"ListConnections", "nameless-arn"},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newAppRunnerCalls()
			handlers := appRunnerHandlers(calls)
			k := appRunnerKindOf(t, c.op)
			handlers[c.op] = func(any) (any, error) { return k.page(nil, sdkaws.String("nameless-arn"), nil), nil }

			total, _, err := appRunnerScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := appRunnerTestTotal - 2; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			w := warns()
			if len(w) != 1 {
				t.Fatalf("warnings = %v; want 1", w)
			}
			if w[0].Service != "apprunner:"+c.op || !strings.Contains(w[0].Message, c.key) || w[0].Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning %+v; want one under apprunner:%s naming %q in %s/%s", w[0], c.op, c.key, testAccountID, testRegion)
			}
			if slices.Contains(calls.tagARNs, "") {
				t.Error("an empty ARN was sent to ListTagsForResource")
			}
		})
	}
}

// Rows of the aws-us-gov and aws-cn partitions reach the tag op under their
// listed ARNs unchanged.
func TestScanAppRunner_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newAppRunnerCalls()
			handlers := appRunnerHandlers(calls)
			arn := "arn:" + c.partition + ":apprunner:" + c.region + ":" + testAccountID + ":connection/gh/abc"
			calls.tags[arn] = map[string]string{"k": "v"}
			handlers["ListConnections"] = func(any) (any, error) {
				return &apprunner.ListConnectionsOutput{ConnectionSummaryList: []apprunnertypes.ConnectionSummary{{ConnectionArn: &arn, ConnectionName: sdkaws.String("gh")}}}, nil
			}
			acct := newTestAccount(testAccountID)
			acct.Partition = c.partition
			acct.cfg = opStubConfig(handlers, appRunnerDenied)
			if _, _, err := scanAppRunner(context.Background(), acct, c.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if !slices.Contains(calls.tagARNs, arn) {
				t.Errorf("ListTagsForResource ARNs %v lack %s", calls.tagARNs, arn)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeAppRunnerConnection)[arn].TagsJSON, map[string]string{"k": "v"})
		})
	}
}

// The AWS-owned DefaultConfiguration observability configuration is stored
// managed, untagged, and never sent to the tag op. The DefaultConfiguration
// auto scaling configuration is customer-managed: unmanaged, tags read.
func TestScanAppRunner_DefaultConfigurations(t *testing.T) {
	st := newTestStore(t)
	calls := newAppRunnerCalls()
	handlers := appRunnerHandlers(calls)
	obs := "arn:aws:apprunner:" + testRegion + ":" + testAccountID + ":observabilityconfiguration/DefaultConfiguration/1/0123"
	asc := "arn:aws:apprunner:" + testRegion + ":" + testAccountID + ":autoscalingconfiguration/DefaultConfiguration/1/0123"
	handlers["ListObservabilityConfigurations"] = func(any) (any, error) {
		return &apprunner.ListObservabilityConfigurationsOutput{ObservabilityConfigurationSummaryList: []apprunnertypes.ObservabilityConfigurationSummary{{
			ObservabilityConfigurationArn: &obs, ObservabilityConfigurationName: sdkaws.String("DefaultConfiguration"),
		}}}, nil
	}
	handlers["ListAutoScalingConfigurations"] = func(any) (any, error) {
		return &apprunner.ListAutoScalingConfigurationsOutput{AutoScalingConfigurationSummaryList: []apprunnertypes.AutoScalingConfigurationSummary{{
			AutoScalingConfigurationArn: &asc, AutoScalingConfigurationName: sdkaws.String("DefaultConfiguration"), IsDefault: sdkaws.Bool(true),
		}}}, nil
	}
	calls.tags[asc] = map[string]string{"owner": "ops"}

	if _, _, err := appRunnerScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	o, ok := rowsByNativeID(t, st, TypeAppRunnerObservabilityConfiguration)[obs]
	if !ok || !o.ManagedByProvider || o.TagsJSON != nil {
		t.Errorf("%s stored %v, managed %v, TagsJSON %v; want stored managed and untagged", obs, ok, o.ManagedByProvider, o.TagsJSON)
	}
	if slices.Contains(calls.tagARNs, obs) {
		t.Errorf("%s sent to ListTagsForResource", obs)
	}
	a := rowsByNativeID(t, st, TypeAppRunnerAutoScalingConfiguration)[asc]
	if a.ManagedByProvider {
		t.Error("DefaultConfiguration auto scaling configuration is managed")
	}
	assertTagsMap(t, a.TagsJSON, map[string]string{"owner": "ops"})
}

// An INACTIVE (deleted) VPC connector or auto scaling configuration revision
// is stored untagged without a tag read, even when the read would report it
// gone; an ACTIVE one is read.
func TestScanAppRunner_InactiveRevisionNotRead(t *testing.T) {
	for _, c := range []struct {
		op   string
		page func(inactive, active string) any
	}{
		{"ListVpcConnectors", func(inactive, active string) any {
			return &apprunner.ListVpcConnectorsOutput{VpcConnectors: []apprunnertypes.VpcConnector{
				{VpcConnectorArn: &inactive, VpcConnectorName: sdkaws.String("old"), Status: apprunnertypes.VpcConnectorStatusInactive},
				{VpcConnectorArn: &active, VpcConnectorName: sdkaws.String("new"), Status: apprunnertypes.VpcConnectorStatusActive},
			}}
		}},
		{"ListAutoScalingConfigurations", func(inactive, active string) any {
			return &apprunner.ListAutoScalingConfigurationsOutput{AutoScalingConfigurationSummaryList: []apprunnertypes.AutoScalingConfigurationSummary{
				{AutoScalingConfigurationArn: &inactive, AutoScalingConfigurationName: sdkaws.String("old"), Status: apprunnertypes.AutoScalingConfigurationStatusInactive},
				{AutoScalingConfigurationArn: &active, AutoScalingConfigurationName: sdkaws.String("new"), Status: apprunnertypes.AutoScalingConfigurationStatusActive},
			}}
		}},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newAppRunnerCalls()
			handlers := appRunnerHandlers(calls)
			k := appRunnerKindOf(t, c.op)
			inactive, active := k.arn("inactive"), k.arn("tagged")
			calls.errs[inactive] = apiErr("ResourceNotFoundException", "gone")
			handlers[k.op] = func(any) (any, error) { return c.page(inactive, active), nil }

			if _, _, err := appRunnerScan(context.Background(), st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			rows := rowsByNativeID(t, st, k.typ)
			if r, ok := rows[inactive]; !ok || r.TagsJSON != nil || sv(r.Status) != "INACTIVE" {
				t.Errorf("%s stored %v, %+v; want stored INACTIVE and untagged", inactive, ok, r)
			}
			if slices.Contains(calls.tagARNs, inactive) {
				t.Errorf("%s sent to ListTagsForResource", inactive)
			}
			assertTagsMap(t, rows[active].TagsJSON, map[string]string{"type": k.typ})
		})
	}
}

// A service's runtime environment variables and commands, which the model
// marks sensitive, are stored redacted where they hold a secret, in both the
// image and the code repository configuration; safe values are kept. The
// tagged service carries a credential in every command and the bare service
// none, so each command path proves both its redaction and what it keeps.
func TestScanAppRunner_ServiceSecretsRedacted(t *testing.T) {
	st := newTestStore(t)
	calls := newAppRunnerCalls()
	handlers := appRunnerHandlers(calls)
	k := appRunnerKindOf(t, "ListServices")
	secretSvc, safeSvc := k.arn("tagged"), k.arn("bare")
	// APP_SIGNING's key is not sensitive and its value has no credential
	// shape, so only RedactUnlessSafe (not RedactIfSensitive) withholds it.
	vars := map[string]string{"DB_PASSWORD": "hunter2", "FEATURE_X": "true", "APP_SIGNING": "9f8e7d6c5b4a3f2e1d0c"}
	type commands struct{ imageStart, codeStart, build string }
	secret := commands{
		imageStart: "node app.js --db postgres://app:hunter2@db.internal/app",
		codeStart:  "npm start -- --db postgres://app:hunter2@db.internal/app",
		build:      "pip install --index-url https://u:token@host/simple",
	}
	safe := commands{imageStart: "npm start", codeStart: "node app.js", build: "npm ci"}
	byService := map[string]commands{secretSvc: secret, safeSvc: safe}
	handlers["DescribeService"] = func(in any) (any, error) {
		arn := sv(in.(*apprunner.DescribeServiceInput).ServiceArn)
		c := byService[arn]
		body := appRunnerServiceBody(arn)
		body.SourceConfiguration = &apprunnertypes.SourceConfiguration{
			ImageRepository: &apprunnertypes.ImageRepository{ImageConfiguration: &apprunnertypes.ImageConfiguration{
				RuntimeEnvironmentVariables: vars, StartCommand: &c.imageStart, Port: sdkaws.String("8080"),
			}},
			CodeRepository: &apprunnertypes.CodeRepository{CodeConfiguration: &apprunnertypes.CodeConfiguration{
				CodeConfigurationValues: &apprunnertypes.CodeConfigurationValues{
					RuntimeEnvironmentVariables: vars, StartCommand: &c.codeStart, BuildCommand: &c.build, Runtime: apprunnertypes.RuntimeNodejs18,
				},
			}},
		}
		return &apprunner.DescribeServiceOutput{Service: body}, nil
	}

	if _, _, err := appRunnerScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeAppRunnerService)
	redacted := commands{redact.PlaceholderValueShape, redact.PlaceholderValueShape, redact.PlaceholderValueShape}
	for arn, want := range map[string]commands{secretSvc: redacted, safeSvc: safe} {
		r, ok := rows[arn]
		if !ok {
			t.Fatalf("%s not stored", arn)
		}
		var got apprunnertypes.Service
		if err := json.Unmarshal([]byte(r.AttributesJSON), &got); err != nil {
			t.Fatalf("%s attrs: %v", arn, err)
		}
		img := got.SourceConfiguration.ImageRepository.ImageConfiguration
		code := got.SourceConfiguration.CodeRepository.CodeConfiguration.CodeConfigurationValues
		if gotCmds := (commands{sv(img.StartCommand), sv(code.StartCommand), sv(code.BuildCommand)}); gotCmds != want {
			t.Errorf("%s image StartCommand, code StartCommand, BuildCommand = %+v; want %+v", arn, gotCmds, want)
		}
		wantVars := map[string]string{
			"DB_PASSWORD": redact.PlaceholderKeyName, "FEATURE_X": "true", "APP_SIGNING": redact.PlaceholderRule,
		}
		if !reflect.DeepEqual(img.RuntimeEnvironmentVariables, wantVars) || !reflect.DeepEqual(code.RuntimeEnvironmentVariables, wantVars) {
			t.Errorf("%s RuntimeEnvironmentVariables = %v, %v; want %v", arn, img.RuntimeEnvironmentVariables, code.RuntimeEnvironmentVariables, wantVars)
		}
		if sv(img.Port) != "8080" || code.Runtime != apprunnertypes.RuntimeNodejs18 {
			t.Errorf("%s Port, Runtime = %q, %q; want 8080, NODEJS_18 kept", arn, sv(img.Port), code.Runtime)
		}
	}
}

// An NXDOMAIN on the first listing, nothing stored, leaves the region
// unavailable and runs no later phase; one after rows were stored is a
// failure of this scan that keeps their counts.
func TestScanAppRunner_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "apprunner.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		calls := newAppRunnerCalls()
		handlers := appRunnerHandlers(calls)
		handlers["ListServices"] = func(any) (any, error) { return nil, nx }
		total, _, err := appRunnerScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 {
			t.Errorf("total = %d; want 0", total)
		}
		if n := calls.count("ListConnections"); n != 0 {
			t.Errorf("ListConnections calls = %d; want 0", n)
		}
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
	t.Run("after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		handlers := appRunnerHandlers(newAppRunnerCalls())
		handlers["ListConnections"] = func(any) (any, error) { return nil, nx }
		total, _, err := appRunnerScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != appRunnerTestTotal-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, appRunnerTestTotal-2)
		}
	})
}

// A failed DescribeService: ResourceNotFoundException drops the service
// silently with no tag read and no domain call; any other failure keeps the
// ServiceSummary as the row, its tags read and its domains listed: a denial
// warns once under DescribeService, a throttle is transient, a response
// without its Service and any other error are errors.
func TestScanAppRunner_DescribeServiceFailures(t *testing.T) {
	k := appRunnerKindOf(t, "ListServices")
	failing := k.arn("tagged")
	for _, c := range []struct {
		name      string
		answer    func(in any) (any, error)
		outcome   serviceOutcome // -1: no error
		code      string         // "" with an outcome: errAppRunnerNoServiceBody
		kept      bool
		wantWarns int
	}{
		{"not found", func(any) (any, error) { return nil, apiErr("ResourceNotFoundException", "gone") }, -1, "", false, 0},
		{"denied", func(any) (any, error) { return nil, appRunnerDenied }, -1, "", true, 1},
		{"throttled", func(any) (any, error) { return nil, appRunnerThrottle }, outcomeTransient, "ThrottlingException", true, 0},
		{"no body", func(any) (any, error) { return &apprunner.DescribeServiceOutput{}, nil }, outcomeError, "", true, 0},
		{"other error", func(any) (any, error) { return nil, apiErr("InvalidRequestException", "bad") }, outcomeError, "InvalidRequestException", true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newAppRunnerCalls()
			handlers := appRunnerHandlers(calls)
			handlers["DescribeService"] = func(in any) (any, error) {
				if sv(in.(*apprunner.DescribeServiceInput).ServiceArn) == failing {
					return c.answer(in)
				}
				return calls.describeService(in)
			}

			total, _, err := appRunnerScan(context.Background(), st, handlers)
			switch {
			case c.outcome < 0:
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			case c.code == "":
				assertGlueOutcome(t, err, c.outcome)
				if !errors.Is(err, errAppRunnerNoServiceBody) {
					t.Errorf("err = %v; want errAppRunnerNoServiceBody", err)
				}
			default:
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			r, ok := rowsByNativeID(t, st, TypeAppRunnerService)[failing]
			if ok != c.kept {
				t.Fatalf("row stored = %v; want %v", ok, c.kept)
			}
			wantTotal, wantDomainCalls := appRunnerTestTotal, 2
			if !c.kept {
				wantTotal, wantDomainCalls = appRunnerTestTotal-1, 1
				if slices.Contains(calls.tagARNs, failing) {
					t.Errorf("%s sent to ListTagsForResource", failing)
				}
			} else {
				if sv(r.Name) != k.name("tagged") || sv(r.Status) != "RUNNING" || strings.Contains(r.AttributesJSON, "InstanceConfiguration") || !strings.Contains(r.AttributesJSON, `"ServiceArn":"`+failing+`"`) {
					t.Errorf("row %+v; want the ServiceSummary named %s, RUNNING", r, k.name("tagged"))
				}
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeAppRunnerService})
			}
			if total != wantTotal {
				t.Errorf("total = %d; want %d", total, wantTotal)
			}
			if n := calls.count("DescribeCustomDomains"); n != wantDomainCalls {
				t.Errorf("DescribeCustomDomains calls = %d; want %d", n, wantDomainCalls)
			}
			w := warns()
			if len(w) != c.wantWarns {
				t.Fatalf("warnings = %v; want %d", w, c.wantWarns)
			}
			for _, x := range w {
				if x.Service != appRunnerDescribeServiceOp || !strings.Contains(x.Message, failing) {
					t.Errorf("warning %+v; want one under %s naming %s", x, appRunnerDescribeServiceOp, failing)
				}
			}
		})
	}
}

// With more services than the fan-out bound and one hard DescribeService
// failure, every other service is still described and stored: one failure
// cancels no sibling.
func TestScanAppRunner_OneDescribeFailureCancelsNoSibling(t *testing.T) {
	st := newTestStore(t)
	calls := newAppRunnerCalls()
	handlers := appRunnerHandlers(calls)
	k := appRunnerKindOf(t, "ListServices")
	var arns []string
	for i := range fanoutMed * 2 {
		arns = append(arns, k.arn(fmt.Sprintf("s%d", i)))
	}
	handlers["ListServices"] = func(any) (any, error) {
		var sums []apprunnertypes.ServiceSummary
		for _, arn := range arns {
			sums = append(sums, apprunnertypes.ServiceSummary{ServiceArn: sdkaws.String(arn), ServiceName: sdkaws.String("x")})
		}
		return &apprunner.ListServicesOutput{ServiceSummaryList: sums}, nil
	}
	handlers["DescribeService"] = func(in any) (any, error) {
		if sv(in.(*apprunner.DescribeServiceInput).ServiceArn) == arns[0] {
			return nil, apiErr("ConflictException", "bad")
		}
		return calls.describeService(in)
	}

	_, _, err := appRunnerScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ConflictException")
	if errors.Is(err, context.Canceled) {
		t.Errorf("err = %v; want no cancellation", err)
	}
	rows := rowsByNativeID(t, st, TypeAppRunnerService)
	if len(rows) != len(arns) {
		t.Fatalf("service rows = %d; want %d", len(rows), len(arns))
	}
	for _, arn := range arns[1:] {
		if !strings.Contains(rows[arn].AttributesJSON, `"Cpu":"1 vCPU"`) {
			t.Errorf("%s attrs = %s; want its DescribeService body", arn, rows[arn].AttributesJSON)
		}
	}
}

// A transient earlier-phase error is demoted to a warning under its op, so
// the custom-domain phase's hard error still reaches the dispatcher as a
// hard error, and the service rows stay.
func TestScanAppRunner_TransientDoesNotMaskCustomDomainError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "apprunner:ListVpcConnectors")
	handlers := appRunnerHandlers(newAppRunnerCalls())
	handlers["ListVpcConnectors"] = func(any) (any, error) { return nil, appRunnerThrottle }
	handlers["DescribeCustomDomains"] = func(any) (any, error) { return nil, apiErr("ConflictException", "bad") }

	_, _, err := appRunnerScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ConflictException")
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1 for the demoted transient", *warns)
	}
	if n := len(rowsByNativeID(t, st, TypeAppRunnerService)); n != 2 {
		t.Errorf("service rows = %d; want 2", n)
	}
}

// The custom-domain phase runs after every other phase, over the services
// stored this scan, and links each domain under its service.
func TestScanAppRunner_CustomDomainsAfterEveryPhase(t *testing.T) {
	st := newTestStore(t)
	calls := newAppRunnerCalls()
	handlers := appRunnerHandlers(calls)
	last := appRunnerKindOf(t, "ListConnections").arn("bare")
	handlers["DescribeCustomDomains"] = func(in any) (any, error) {
		calls.record("DescribeCustomDomains", in)
		if _, err := st.GetResource(store.ResourceID("aws", testAccountID, last)); err != nil {
			return nil, fmt.Errorf("%s not stored before the custom domains: %w", last, err)
		}
		return &apprunner.DescribeCustomDomainsOutput{CustomDomains: []apprunnertypes.CustomDomain{appRunnerDomain("example.com")}}, nil
	}

	total, _, err := appRunnerScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != appRunnerTestTotal+2 {
		t.Errorf("total = %d; want %d", total, appRunnerTestTotal+2)
	}
	k := appRunnerKindOf(t, "ListServices")
	for _, arn := range []string{k.arn("tagged"), k.arn("bare")} {
		assertContainsNative(t, st, arn, arn+"/custom-domain/example.com")
	}
}
