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
	"github.com/aws/aws-sdk-go-v2/service/apprunner"
	apprunnertypes "github.com/aws/aws-sdk-go-v2/service/apprunner/types"
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

// appRunnerConnectionARN is the connection the top-level handlers list, so a
// custom-domain call can check that the last earlier phase already ran.
var appRunnerConnectionARN = fmt.Sprintf("arn:aws:apprunner:%s:%s:connection/gh/abc", testRegion, testAccountID)

// appRunnerTopLevelHandlers answers the ops scanAppRunner calls with three
// services: svc3's DescribeService is denied, so it is not stored and one
// apprunner:DescribeService warning is recorded. Each
// stored service has one custom domain. Every other list op returns one
// connection and nothing else.
func appRunnerTopLevelHandlers(st *store.Store, calls *parentCalls) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListServices": func(any) (any, error) {
			var sums []apprunnertypes.ServiceSummary
			for _, name := range []string{"svc1", "svc2", "svc3"} {
				sums = append(sums, apprunnertypes.ServiceSummary{ServiceArn: sdkaws.String(appRunnerServiceARN(name)), ServiceName: sdkaws.String(name)})
			}
			return &apprunner.ListServicesOutput{ServiceSummaryList: sums}, nil
		},
		"DescribeService": func(in any) (any, error) {
			arn := sdkaws.ToString(in.(*apprunner.DescribeServiceInput).ServiceArn)
			if arn == appRunnerServiceARN("svc3") {
				return nil, appRunnerDenied
			}
			return &apprunner.DescribeServiceOutput{Service: &apprunnertypes.Service{ServiceArn: &arn, Status: apprunnertypes.ServiceStatusRunning}}, nil
		},
		"ListVpcConnectors":               func(any) (any, error) { return &apprunner.ListVpcConnectorsOutput{}, nil },
		"ListAutoScalingConfigurations":   func(any) (any, error) { return &apprunner.ListAutoScalingConfigurationsOutput{}, nil },
		"ListObservabilityConfigurations": func(any) (any, error) { return &apprunner.ListObservabilityConfigurationsOutput{}, nil },
		"ListVpcIngressConnections":       func(any) (any, error) { return &apprunner.ListVpcIngressConnectionsOutput{}, nil },
		"ListConnections": func(any) (any, error) {
			return &apprunner.ListConnectionsOutput{ConnectionSummaryList: []apprunnertypes.ConnectionSummary{{ConnectionArn: &appRunnerConnectionARN}}}, nil
		},
		"DescribeCustomDomains": func(in any) (any, error) {
			arn := sdkaws.ToString(in.(*apprunner.DescribeCustomDomainsInput).ServiceArn)
			// The service row must already be stored, and the last earlier
			// phase must already have run: the domain phase runs after all of
			// them.
			for _, id := range []string{arn, appRunnerConnectionARN} {
				if _, err := st.GetResource(store.ResourceID("aws", testAccountID, id)); err != nil {
					return nil, fmt.Errorf("%s not stored before the custom domains of %s: %w", id, arn, err)
				}
			}
			calls.add(arn)
			return &apprunner.DescribeCustomDomainsOutput{CustomDomains: []apprunnertypes.CustomDomain{appRunnerDomain("example.com")}}, nil
		},
	}
}

// The top-level scan lists the custom domains of every stored service, and
// only those, after every earlier phase.
func TestScanAppRunner_CustomDomainsOfStoredServices(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, appRunnerDescribeServiceOp)
	var calls parentCalls
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(appRunnerTopLevelHandlers(st, &calls), appRunnerDenied)}

	total, _, err := scanAppRunner(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d; want 5 (2 services + 1 connection + 2 custom domains)", total)
	}
	if *warns != 1 {
		t.Errorf("DescribeService warnings = %d; want 1 for svc3's denial", *warns)
	}
	svc1, svc2 := appRunnerServiceARN("svc1"), appRunnerServiceARN("svc2")
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{svc1, svc2}) {
		t.Errorf("services queried = %v; want [%s %s]", got, svc1, svc2)
	}
	for _, arn := range []string{svc1, svc2} {
		assertContainsNative(t, st, arn, arn+"/custom-domain/example.com")
	}
}

// A service deleted between ListServices and DescribeService is dropped
// silently: its siblings are stored and their custom domains listed.
func TestScanAppRunner_ServiceGoneBeforeDescribeSkipped(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, appRunnerDescribeServiceOp)
	var calls parentCalls
	h := appRunnerTopLevelHandlers(st, &calls)
	describe := h["DescribeService"]
	h["DescribeService"] = func(in any) (any, error) {
		if sdkaws.ToString(in.(*apprunner.DescribeServiceInput).ServiceArn) == appRunnerServiceARN("svc3") {
			return nil, apiErr("ResourceNotFoundException", "gone")
		}
		return describe(in)
	}
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, appRunnerDenied)}

	_, _, err := scanAppRunner(context.Background(), acct, testRegion, st, testScanID)
	if err != nil || *warns != 0 {
		t.Fatalf("err, warnings = %v, %d; want nil, 0", err, *warns)
	}
	svc1, svc2 := appRunnerServiceARN("svc1"), appRunnerServiceARN("svc2")
	services := rowsByNativeID(t, st, TypeAppRunnerService)
	if _, ok := services[appRunnerServiceARN("svc3")]; ok || len(services) != 2 {
		t.Errorf("service rows = %v; want svc1 and svc2 only", services)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{svc1, svc2}) {
		t.Errorf("services queried for custom domains = %v; want [%s %s]", got, svc1, svc2)
	}
}

// A DescribeService response without its model-required Service drops that
// service with one warning; its siblings are stored.
func TestScanAppRunner_ServiceWithoutBodyWarns(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, appRunnerDescribeServiceOp)
	var calls parentCalls
	h := appRunnerTopLevelHandlers(st, &calls)
	h["DescribeService"] = func(in any) (any, error) {
		arn := sdkaws.ToString(in.(*apprunner.DescribeServiceInput).ServiceArn)
		if arn == appRunnerServiceARN("svc1") {
			return &apprunner.DescribeServiceOutput{Service: &apprunnertypes.Service{ServiceArn: &arn}}, nil
		}
		return &apprunner.DescribeServiceOutput{}, nil
	}
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, appRunnerDenied)}

	_, _, err := scanAppRunner(context.Background(), acct, testRegion, st, testScanID)
	if err != nil || *warns != 1 {
		t.Fatalf("err, warnings = %v, %d; want nil, 1", err, *warns)
	}
	if services := rowsByNativeID(t, st, TypeAppRunnerService); len(services) != 1 {
		t.Errorf("service rows = %v; want svc1 only", services)
	}
}

// With more services than the fan-out bound and one hard DescribeService
// failure, the phase returns that failure, not a cancellation. Every other
// call holds its fan-out slot until the fan-out cancels it, so a loop that
// waits for a free slot on the cancelled context would see the cancellation
// first. The failing service is the first listed, so it is dispatched before
// the slots fill.
func TestScanAppRunner_ManyServicesOneFailureReturnsRealError(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	handlers := ignoreCtx(appRunnerTopLevelHandlers(st, &calls))
	failing := appRunnerServiceARN("svc0")
	handlers["ListServices"] = func(context.Context, any) (any, error) {
		var sums []apprunnertypes.ServiceSummary
		for i := range fanoutMed * 3 {
			sums = append(sums, apprunnertypes.ServiceSummary{ServiceArn: sdkaws.String(appRunnerServiceARN(fmt.Sprintf("svc%d", i)))})
		}
		return &apprunner.ListServicesOutput{ServiceSummaryList: sums}, nil
	}
	handlers["DescribeService"] = func(ctx context.Context, in any) (any, error) {
		if sdkaws.ToString(in.(*apprunner.DescribeServiceInput).ServiceArn) == failing {
			return nil, apiErr("ConflictException", "bad")
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfigCtx(handlers, appRunnerDenied)}

	// Bounds the blocked siblings if the fan-out ever stops cancelling them:
	// the regression then fails as DeadlineExceeded instead of hanging.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _, err := scanAppRunner(ctx, acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "ConflictException") || errors.Is(err, context.Canceled) {
		t.Errorf("err = %v; want the DescribeService ConflictException, not a cancellation", err)
	}
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v; want outcomeError", got)
	}
}

// A service whose DescribeService still fails transiently after the SDK's
// retries is dropped with one warning: its siblings are stored, their custom
// domains listed, and the later phases run.
func TestScanAppRunner_TransientServiceSkipped(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, appRunnerDescribeServiceOp)
	var calls parentCalls
	h := appRunnerTopLevelHandlers(st, &calls)
	describe := h["DescribeService"]
	h["DescribeService"] = func(in any) (any, error) {
		if sdkaws.ToString(in.(*apprunner.DescribeServiceInput).ServiceArn) == appRunnerServiceARN("svc3") {
			return nil, apiErr("ThrottlingException", "Rate exceeded")
		}
		return describe(in)
	}
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, appRunnerDenied)}

	total, _, err := scanAppRunner(context.Background(), acct, testRegion, st, testScanID)
	if err != nil || *warns != 1 {
		t.Fatalf("err, warnings = %v, %d; want nil, 1", err, *warns)
	}
	if total != 5 {
		t.Errorf("total = %d; want 5 (2 services + 1 connection + 2 custom domains)", total)
	}
	svc1, svc2 := appRunnerServiceARN("svc1"), appRunnerServiceARN("svc2")
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{svc1, svc2}) {
		t.Errorf("services queried for custom domains = %v; want [%s %s]", got, svc1, svc2)
	}
	if _, ok := rowsByNativeID(t, st, TypeAppRunnerConnection)[appRunnerConnectionARN]; !ok {
		t.Error("connection row missing: the later phases did not run")
	}
}

// A failing earlier phase still leaves the custom-domain phase to run over
// the services stored before it, and its error is returned.
func TestScanAppRunner_EarlierPhaseErrorStillScansCustomDomains(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	h := appRunnerTopLevelHandlers(st, &calls)
	h["ListVpcConnectors"] = func(any) (any, error) { return nil, apiErr("ConflictException", "bad") }
	h["DescribeCustomDomains"] = func(in any) (any, error) {
		calls.add(sdkaws.ToString(in.(*apprunner.DescribeCustomDomainsInput).ServiceArn))
		return &apprunner.DescribeCustomDomainsOutput{CustomDomains: []apprunnertypes.CustomDomain{appRunnerDomain("example.com")}}, nil
	}
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, appRunnerDenied)}

	total, _, err := scanAppRunner(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "ConflictException") {
		t.Errorf("err = %v; want the ListVpcConnectors ConflictException", err)
	}
	if total != 4 {
		t.Errorf("total = %d; want 4 (2 services + 2 custom domains)", total)
	}
	if n := len(rowsByNativeID(t, st, TypeAppRunnerCustomDomain)); n != 2 {
		t.Errorf("custom domain rows = %d; want 2", n)
	}
}

// A transient earlier-phase error is demoted to a warning, so the
// custom-domain phase's hard error still reaches the dispatcher as a hard
// error, and the service rows stay.
func TestScanAppRunner_TransientDoesNotMaskCustomDomainError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "apprunner")
	var calls parentCalls
	h := appRunnerTopLevelHandlers(st, &calls)
	h["ListVpcConnectors"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
	h["DescribeCustomDomains"] = func(any) (any, error) { return nil, apiErr("ConflictException", "bad") }
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, appRunnerDenied)}

	_, _, err := scanAppRunner(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "ConflictException") {
		t.Errorf("err = %v; want the custom-domain phase's ConflictException", err)
	}
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v; want outcomeError", got)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d; want 1 for the demoted transient", *warns)
	}
	if n := len(rowsByNativeID(t, st, TypeAppRunnerService)); n != 2 {
		t.Errorf("service rows = %d; want 2", n)
	}
}
