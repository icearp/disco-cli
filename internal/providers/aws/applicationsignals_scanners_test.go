package aws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/applicationsignals"
	applicationsignalstypes "github.com/aws/aws-sdk-go-v2/service/applicationsignals/types"
	"github.com/icearp/disco-cli/store"
)

// stubApplicationSignals is a deterministic in-memory replacement for
// applicationSignalsAPI used by the scanner tests.
type stubApplicationSignals struct {
	sloPages      []*applicationsignals.ListServiceLevelObjectivesOutput
	sloIdx        int
	groupingPages []*applicationsignals.ListGroupingAttributeDefinitionsOutput
	groupingIdx   int
	servicePages  []*applicationsignals.ListServicesOutput
	serviceIdx    int
	sloErr        error
	groupingErr   error
	serviceErr    error
}

func (s *stubApplicationSignals) ListServices(_ context.Context, _ *applicationsignals.ListServicesInput, _ ...func(*applicationsignals.Options)) (*applicationsignals.ListServicesOutput, error) {
	if s.serviceErr != nil {
		return nil, s.serviceErr
	}
	if s.serviceIdx >= len(s.servicePages) {
		return &applicationsignals.ListServicesOutput{}, nil
	}
	out := s.servicePages[s.serviceIdx]
	s.serviceIdx++
	return out, nil
}

func (s *stubApplicationSignals) ListServiceLevelObjectives(_ context.Context, _ *applicationsignals.ListServiceLevelObjectivesInput, _ ...func(*applicationsignals.Options)) (*applicationsignals.ListServiceLevelObjectivesOutput, error) {
	if s.sloErr != nil {
		return nil, s.sloErr
	}
	if s.sloIdx >= len(s.sloPages) {
		return &applicationsignals.ListServiceLevelObjectivesOutput{}, nil
	}
	out := s.sloPages[s.sloIdx]
	s.sloIdx++
	return out, nil
}

func (s *stubApplicationSignals) ListGroupingAttributeDefinitions(_ context.Context, _ *applicationsignals.ListGroupingAttributeDefinitionsInput, _ ...func(*applicationsignals.Options)) (*applicationsignals.ListGroupingAttributeDefinitionsOutput, error) {
	if s.groupingErr != nil {
		return nil, s.groupingErr
	}
	if s.groupingIdx >= len(s.groupingPages) {
		return &applicationsignals.ListGroupingAttributeDefinitionsOutput{}, nil
	}
	out := s.groupingPages[s.groupingIdx]
	s.groupingIdx++
	return out, nil
}

func (s *stubApplicationSignals) ListInstrumentationConfigurations(_ context.Context, _ *applicationsignals.ListInstrumentationConfigurationsInput, _ ...func(*applicationsignals.Options)) (*applicationsignals.ListInstrumentationConfigurationsOutput, error) {
	return &applicationsignals.ListInstrumentationConfigurationsOutput{}, nil
}

func (s *stubApplicationSignals) ListTagsForResource(_ context.Context, _ *applicationsignals.ListTagsForResourceInput, _ ...func(*applicationsignals.Options)) (*applicationsignals.ListTagsForResourceOutput, error) {
	return &applicationsignals.ListTagsForResourceOutput{}, nil
}

func TestApplicationSignalsGroupingConfigurationNativeID(t *testing.T) {
	t.Parallel()
	got := applicationSignalsGroupingConfigurationNativeID("aws", "us-east-1", "111111111111")
	want := "arn:aws:application-signals:us-east-1:111111111111:grouping-configuration"
	if got != want {
		t.Errorf("native id = %q, want %q", got, want)
	}
}

func TestScanApplicationSignalsGroupingConfiguration_Populated(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	gname := "BusinessUnit"
	updated := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	stub := &stubApplicationSignals{
		groupingPages: []*applicationsignals.ListGroupingAttributeDefinitionsOutput{{
			GroupingAttributeDefinitions: []applicationsignalstypes.GroupingAttributeDefinition{{
				GroupingName:       &gname,
				GroupingSourceKeys: []string{"team"},
			}},
			UpdatedAt: &updated,
		}},
	}
	total, inserted, err := scanApplicationSignalsGroupingConfiguration(context.Background(), stub, acct, region, st, testScanID)
	if err != nil {
		t.Fatalf("scanApplicationSignalsGroupingConfiguration: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1", total, inserted)
	}
	want := applicationSignalsGroupingConfigurationNativeID("aws", region, acct.ID)
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeApplicationSignalsGroupingConfiguration}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rows) != 1 || rows[0].NativeID != want {
		t.Errorf("rows=%+v, want one row with NativeID %s", rows, want)
	}
}

func TestApplicationSignalsServiceNativeID_StableAcrossMapOrder(t *testing.T) {
	t.Parallel()
	a := applicationSignalsServiceNativeID("aws", "us-east-1", "111111111111", map[string]string{"Type": "Service", "Name": "checkout", "Environment": "prod"})
	b := applicationSignalsServiceNativeID("aws", "us-east-1", "111111111111", map[string]string{"Name": "checkout", "Environment": "prod", "Type": "Service"})
	if a != b {
		t.Errorf("native id not stable across map order:\n a=%q\n b=%q", a, b)
	}
	want := "arn:aws:application-signals:us-east-1:111111111111:service/Environment=prod;Name=checkout;Type=Service"
	if a != want {
		t.Errorf("native id = %q, want %q", a, want)
	}
}

func TestScanApplicationSignalsServices_Populated(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	stub := &stubApplicationSignals{
		servicePages: []*applicationsignals.ListServicesOutput{{
			ServiceSummaries: []applicationsignalstypes.ServiceSummary{{
				KeyAttributes: map[string]string{"Type": "Service", "Name": "checkout", "Environment": "prod"},
			}},
		}},
	}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	total, inserted, _, err := scanApplicationSignalsServices(context.Background(), stub, acct, region, st, testScanID, now)
	if err != nil {
		t.Fatalf("scanApplicationSignalsServices: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1", total, inserted)
	}
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCloudWatchService}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	want := applicationSignalsServiceNativeID("aws", region, acct.ID, map[string]string{"Type": "Service", "Name": "checkout", "Environment": "prod"})
	if len(rows) != 1 || rows[0].NativeID != want {
		t.Errorf("rows=%+v, want one row with NativeID %s", rows, want)
	}
}

func TestScanApplicationSignalsServices_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	stub := &stubApplicationSignals{}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	total, inserted, _, err := scanApplicationSignalsServices(context.Background(), stub, acct, testRegion, st, testScanID, now)
	if err != nil {
		t.Fatalf("scanApplicationSignalsServices: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total=%d inserted=%d, want 0/0 (no services)", total, inserted)
	}
}

func TestScanApplicationSignalsGroupingConfiguration_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	stub := &stubApplicationSignals{}
	total, inserted, err := scanApplicationSignalsGroupingConfiguration(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanApplicationSignalsGroupingConfiguration: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total=%d inserted=%d, want 0/0 (skip empty)", total, inserted)
	}
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeApplicationSignalsGroupingConfiguration}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected no rows; got %+v", rows)
	}
}

var appSignalsDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: application-signals:List")

var appSignalsCreated = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)

func appSignalsServiceKeyAttrs(name string) map[string]string {
	return map[string]string{"Type": "Service", "Name": name, "Environment": "eks:prod/default"}
}

func appSignalsTestService(name string) applicationSignalsService {
	return applicationSignalsService{
		name: name, environment: "eks:prod/default",
		nativeID: applicationSignalsServiceNativeID("aws", testRegion, testAccountID, appSignalsServiceKeyAttrs(name)),
	}
}

func appSignalsConfigARN(service, loc string) string {
	return arnFor("aws", "application-signals", testRegion, testAccountID, "instrumentationConfig/"+service+"/eks:prod/default/SNAPSHOT/"+loc)
}

func appSignalsConfig(service, loc string) applicationsignalstypes.InstrumentationConfigurationWithoutServiceEnv {
	return applicationsignalstypes.InstrumentationConfigurationWithoutServiceEnv{
		ARN:                 sdkaws.String(appSignalsConfigARN(service, loc)),
		CreatedAt:           &appSignalsCreated,
		Description:         sdkaws.String("desc " + loc),
		InstrumentationType: applicationsignalstypes.InstrumentationTypeProbe,
		LocationHash:        sdkaws.String(loc),
		SignalType:          applicationsignalstypes.DynamicInstrumentationSignalTypeSnapshot,
		Location: &applicationsignalstypes.LocationMemberCodeLocation{Value: applicationsignalstypes.CodeLocation{
			FilePath: sdkaws.String("src/" + loc + ".py"),
		}},
		CaptureConfiguration: &applicationsignalstypes.CaptureConfigurationMemberCodeCapture{Value: applicationsignalstypes.CodeCaptureConfiguration{
			CaptureLimits: &applicationsignalstypes.CaptureLimitsConfig{MaxHits: sdkaws.Int32(7)},
		}},
	}
}

// appSignalsConfigHandler answers ListInstrumentationConfigurations with two
// PROBE configurations per service over two pages, recording each call as
// service/environment/type. A call without the model's page cap, or for
// BREAKPOINT, fails hard.
func appSignalsConfigHandler(calls *parentCalls) func(in any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*applicationsignals.ListInstrumentationConfigurationsInput)
		svc := sdkaws.ToString(req.Service)
		calls.add(svc + "/" + sdkaws.ToString(req.Environment) + "/" + string(req.InstrumentationType))
		switch {
		case sdkaws.ToInt32(req.MaxResults) != 100:
			return nil, apiErr("UnexpectedMaxResults", "want 100")
		case req.InstrumentationType != applicationsignalstypes.InstrumentationTypeProbe:
			return nil, apiErr("UnexpectedInstrumentationType", "want PROBE")
		case req.NextToken == nil:
			return &applicationsignals.ListInstrumentationConfigurationsOutput{
				LatestConfigurations: []applicationsignalstypes.InstrumentationConfigurationWithoutServiceEnv{appSignalsConfig(svc, svc+"-probe1")},
				NextToken:            sdkaws.String("p2"),
			}, nil
		default:
			return &applicationsignals.ListInstrumentationConfigurationsOutput{
				LatestConfigurations: []applicationsignalstypes.InstrumentationConfigurationWithoutServiceEnv{appSignalsConfig(svc, svc+"-probe2")},
			}, nil
		}
	}
}

// appSignalsClient answers handlers' ops, and ListTagsForResource with no
// tags unless handlers answer it; every other op is denied.
func appSignalsClient(handlers map[string]func(in any) (any, error)) *applicationsignals.Client {
	if _, ok := handlers["ListTagsForResource"]; !ok {
		handlers["ListTagsForResource"] = func(any) (any, error) { return &applicationsignals.ListTagsForResourceOutput{}, nil }
	}
	return applicationsignals.NewFromConfig(opStubConfig(handlers, appSignalsDenied))
}

// runAppSignalsInstrumentation runs the configurations phase alone against
// client, reading tags through it, and returns the phase's result.
func runAppSignalsInstrumentation(client *applicationsignals.Client, st *store.Store, services []applicationSignalsService) (int, int, error) {
	acct := newTestAccount(testAccountID)
	var p phaseTotals
	scanApplicationSignalsInstrumentationConfigurations(context.Background(), client, acct, testRegion, st, testScanID, services, appSignalsTags(client), &p)
	return p.result(st, acct, testRegion)
}

// seedAppSignalsServices stores the service rows the configurations are
// linked under, as the services phase would.
func seedAppSignalsServices(t *testing.T, st *store.Store, services []applicationSignalsService) {
	t.Helper()
	for _, s := range services {
		upsertTestResource(t, st, "aws", testAccountID, TypeCloudWatchService, s.nativeID, testRegion, "{}")
	}
}

func TestScanApplicationSignalsInstrumentationConfigurations_ProbesOfEveryPage(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	services := []applicationSignalsService{appSignalsTestService("checkout"), appSignalsTestService("cart")}
	seedAppSignalsServices(t, st, services)
	var calls parentCalls
	client := appSignalsClient(map[string]func(any) (any, error){"ListInstrumentationConfigurations": appSignalsConfigHandler(&calls)})

	total, _, err := runAppSignalsInstrumentation(client, st, services)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	wantCalls := []string{"cart/eks:prod/default/PROBE", "checkout/eks:prod/default/PROBE"}
	if got := calls.distinct(); !reflect.DeepEqual(got, wantCalls) {
		t.Errorf("calls = %v; want %v", got, wantCalls)
	}
	rows := rowsByNativeID(t, st, TypeApplicationSignalsInstrumentationConfiguration)
	if total != 4 || len(rows) != 4 {
		t.Fatalf("total, rows = %d, %d; want 4, 4", total, len(rows))
	}
	for _, s := range services {
		for _, loc := range []string{s.name + "-probe1", s.name + "-probe2"} {
			arn := appSignalsConfigARN(s.name, loc)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("configuration %s not stored", arn)
				continue
			}
			if r.Type != TypeApplicationSignalsInstrumentationConfiguration || sv(r.Region) != testRegion ||
				sv(r.Name) != "desc "+loc || sv(r.CreatedAt) != "2026-09-01T08:00:00Z" {
				t.Errorf("row %s = type %q region %q name %q created %q", arn, r.Type, sv(r.Region), sv(r.Name), sv(r.CreatedAt))
			}
			if !strings.Contains(r.AttributesJSON, `"Location":{"CodeLocation":{"FilePath":"src/`+loc+`.py"`) ||
				!strings.Contains(r.AttributesJSON, `"CaptureConfiguration":{"CodeCapture":{"CaptureLimits":{`) {
				t.Errorf("attrs of %s = %s; want the union members labelled", arn, r.AttributesJSON)
			}
			assertContainsNative(t, st, s.nativeID, arn)
		}
	}
	assertContainsEdgesDeclared(t, st)
}

func TestScanApplicationSignalsInstrumentationConfigurations_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	services := []applicationSignalsService{appSignalsTestService("checkout")}
	seedAppSignalsServices(t, st, services)
	var calls parentCalls
	client := appSignalsClient(map[string]func(any) (any, error){
		"ListInstrumentationConfigurations": func(in any) (any, error) {
			calls.add(string(in.(*applicationsignals.ListInstrumentationConfigurationsInput).InstrumentationType))
			return &applicationsignals.ListInstrumentationConfigurationsOutput{}, nil
		},
	})

	total, inserted, err := runAppSignalsInstrumentation(client, st, services)
	if err != nil || total != 0 || inserted != 0 {
		t.Errorf("total, inserted, err = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if got := calls.distinct(); !reflect.DeepEqual(got, []string{"PROBE"}) {
		t.Errorf("types queried = %v; want [PROBE]", got)
	}
	total, _, err = runAppSignalsInstrumentation(client, st, nil)
	if err != nil || total != 0 || len(calls.ids) != 1 {
		t.Errorf("no services: total, err, calls = %d, %v, %v; want 0, nil, no new calls", total, err, calls.ids)
	}
	if rows := rowsByNativeID(t, st, TypeApplicationSignalsInstrumentationConfiguration); len(rows) != 0 {
		t.Errorf("rows = %v; want none", rows)
	}
}

// A configuration listed without an ARN is not stored and is warned about
// once under the listing; its sibling is stored.
func TestScanApplicationSignalsInstrumentationConfigurations_EmptyARNWarned(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warnings := bacWarnings(st, appSignalsListInstrumentationConfigurationsOp)
	services := []applicationSignalsService{appSignalsTestService("checkout")}
	seedAppSignalsServices(t, st, services)
	noARN := appSignalsConfig("checkout", "no-arn")
	noARN.ARN = nil
	client := appSignalsClient(map[string]func(any) (any, error){
		"ListInstrumentationConfigurations": func(any) (any, error) {
			return &applicationsignals.ListInstrumentationConfigurationsOutput{
				LatestConfigurations: []applicationsignalstypes.InstrumentationConfigurationWithoutServiceEnv{noARN, appSignalsConfig("checkout", "kept")},
			}, nil
		},
	})

	total, _, err := runAppSignalsInstrumentation(client, st, services)
	if err != nil || total != 1 {
		t.Fatalf("total, err = %d, %v; want 1, nil", total, err)
	}
	rows := rowsByNativeID(t, st, TypeApplicationSignalsInstrumentationConfiguration)
	if _, ok := rows[appSignalsConfigARN("checkout", "kept")]; !ok || len(rows) != 1 {
		t.Errorf("rows = %v; want only the configuration with an ARN", rows)
	}
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion {
		t.Errorf("warnings = %v; want one, scoped %s/%s", *warnings, testAccountID, testRegion)
	}
}

// Two service rows sharing a name and environment (other KeyAttributes differ)
// are queried once, and each configuration is stored once under the first.
func TestScanApplicationSignalsInstrumentationConfigurations_SharedNameEnvironmentQueriedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	first := appSignalsTestService("checkout")
	attrs := appSignalsServiceKeyAttrs("checkout")
	attrs["AwsAccountId"] = "210987654321"
	second := first
	second.nativeID = applicationSignalsServiceNativeID("aws", testRegion, testAccountID, attrs)
	services := []applicationSignalsService{first, second}
	seedAppSignalsServices(t, st, services)
	var calls parentCalls
	client := appSignalsClient(map[string]func(any) (any, error){"ListInstrumentationConfigurations": appSignalsConfigHandler(&calls)})

	total, _, err := runAppSignalsInstrumentation(client, st, services)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if total != 2 || len(calls.ids) != 2 {
		t.Errorf("total, calls = %d, %v; want 2 configurations from one two-page listing", total, calls.ids)
	}
	arn := appSignalsConfigARN("checkout", "checkout-probe1")
	assertContainsNative(t, st, first.nativeID, arn)
	rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, second.nativeID), store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("second service contains %v; want nothing", rels)
	}
}

// Per service error policy: cart fails with code (on its second page when
// secondPage is set), checkout succeeds. Every failure keeps checkout's
// configurations and those cart listed before failing; a throttle after the
// SDK's retries is returned, not warned, since some or all of cart's
// configurations were lost.
func TestScanApplicationSignalsInstrumentationConfigurations_ErrorPolicy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		code       string
		bothFail   bool
		secondPage bool
		wantWarns  int
		outcome    serviceOutcome
	}{
		{name: "not found skipped silently", code: "ResourceNotFoundException"},
		{name: "not found mid-listing warns once", code: "ResourceNotFoundException", secondPage: true, wantWarns: 1},
		{name: "op not deployed skipped silently", code: "UnknownOperationException"},
		{name: "op not deployed after a page fails", code: "UnknownOperationException", secondPage: true, outcome: outcomeError},
		{name: "validation warns once", code: "ValidationException", wantWarns: 1},
		{name: "validation after a page fails", code: "ValidationException", secondPage: true, outcome: outcomeError},
		{name: "transient is returned", code: "ThrottlingException", outcome: outcomeTransient},
		{name: "transient after a page is returned", code: "ThrottlingException", secondPage: true, outcome: outcomeTransient},
		{name: "denial warns once", code: "AccessDeniedException", bothFail: true, wantWarns: 1},
		{name: "other error fails the phase", code: "ConflictException", outcome: outcomeError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, appSignalsListInstrumentationConfigurationsOp)
			services := []applicationSignalsService{appSignalsTestService("checkout"), appSignalsTestService("cart")}
			seedAppSignalsServices(t, st, services)
			var calls parentCalls
			ok := appSignalsConfigHandler(&calls)
			client := appSignalsClient(map[string]func(any) (any, error){
				"ListInstrumentationConfigurations": func(in any) (any, error) {
					req := in.(*applicationsignals.ListInstrumentationConfigurationsInput)
					fails := tc.bothFail || sdkaws.ToString(req.Service) == "cart"
					if fails && (!tc.secondPage || req.NextToken != nil) {
						return nil, apiErr(tc.code, "refused")
					}
					return ok(in)
				},
			})

			_, _, err := runAppSignalsInstrumentation(client, st, services)
			if tc.outcome != 0 {
				assertGlueOutcome(t, err, tc.outcome)
				assertAPICode(t, err, tc.code)
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
			want := []string{appSignalsConfigARN("checkout", "checkout-probe1"), appSignalsConfigARN("checkout", "checkout-probe2")}
			if tc.bothFail {
				want = nil
			}
			if tc.secondPage {
				want = append(want, appSignalsConfigARN("cart", "cart-probe1"))
			}
			var got []string
			for arn := range rowsByNativeID(t, st, TypeApplicationSignalsInstrumentationConfiguration) {
				got = append(got, arn)
			}
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("configurations stored = %v; want %v", got, want)
			}
		})
	}
}

// A throttled service beside a hard failure of another service is demoted to
// one warning under the op, so the hard error decides the outcome, whether
// cart is throttled on its first page or after it (keeping cart-probe1).
func TestScanApplicationSignalsInstrumentationConfigurations_ThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	for _, secondPage := range []bool{false, true} {
		t.Run(fmt.Sprintf("secondPage=%v", secondPage), func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := countOpWarnings(st, appSignalsListInstrumentationConfigurationsOp)
			services := []applicationSignalsService{appSignalsTestService("checkout"), appSignalsTestService("cart"), appSignalsTestService("search")}
			seedAppSignalsServices(t, st, services)
			var calls parentCalls
			ok := appSignalsConfigHandler(&calls)
			client := appSignalsClient(map[string]func(any) (any, error){
				"ListInstrumentationConfigurations": func(in any) (any, error) {
					req := in.(*applicationsignals.ListInstrumentationConfigurationsInput)
					switch sdkaws.ToString(req.Service) {
					case "cart":
						if !secondPage || req.NextToken != nil {
							return nil, apiErr("ThrottlingException", "Rate exceeded")
						}
					case "search":
						return nil, apiErr("ConflictException", "bad")
					}
					return ok(in)
				},
			})

			_, _, err := runAppSignalsInstrumentation(client, st, services)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ConflictException")
			if *warns != 1 {
				t.Errorf("warnings = %d; want 1", *warns)
			}
			want := 2
			if secondPage {
				want = 3
			}
			if n := len(rowsByNativeID(t, st, TypeApplicationSignalsInstrumentationConfiguration)); n != want {
				t.Errorf("configurations stored = %d; want %d", n, want)
			}
		})
	}
}

// After a failed write the throttled services' error is returned with the
// write's, not demoted to a warning about configurations the write lost.
func TestScanApplicationSignalsInstrumentationConfigurations_FailedWriteKeepsThrottle(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := recordWarnings(st)
	refuseStoreWrites(t, st, TypeApplicationSignalsInstrumentationConfiguration)
	services := []applicationSignalsService{appSignalsTestService("checkout"), appSignalsTestService("cart")}
	seedAppSignalsServices(t, st, services)
	var calls parentCalls
	ok := appSignalsConfigHandler(&calls)
	client := appSignalsClient(map[string]func(any) (any, error){
		"ListInstrumentationConfigurations": func(in any) (any, error) {
			if sdkaws.ToString(in.(*applicationsignals.ListInstrumentationConfigurationsInput).Service) == "cart" {
				return nil, apiErr("ThrottlingException", "Rate exceeded")
			}
			return ok(in)
		},
	})

	_, _, err := runAppSignalsInstrumentation(client, st, services)
	if !errors.Is(err, store.ErrStoreWrite) || !isAPIErrorCode(err, "ThrottlingException") {
		t.Errorf("err = %v; want store.ErrStoreWrite joined with the ThrottlingException", err)
	}
	if len(*warns) != 0 {
		t.Errorf("warnings = %v; want none", *warns)
	}
}

// The transient error names the smallest throttled service, not the first
// tallied. At most fanoutMed services are listed at once, so smallest, listed
// last, is listed only after an earlier service's throttle was tallied.
func TestScanApplicationSignalsInstrumentationConfigurations_ThrottleNamesSmallestService(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var services []applicationSignalsService
	for i := range fanoutMed {
		services = append(services, appSignalsTestService(fmt.Sprintf("z%02d", i)))
	}
	smallest := appSignalsTestService("a")
	services = append(services, smallest)
	seedAppSignalsServices(t, st, services)
	client := appSignalsClient(map[string]func(any) (any, error){
		"ListInstrumentationConfigurations": func(any) (any, error) { return nil, apiErr("ThrottlingException", "Rate exceeded") },
	})

	_, _, err := runAppSignalsInstrumentation(client, st, services)
	assertGlueOutcome(t, err, outcomeTransient)
	if !strings.Contains(err.Error(), "first "+smallest.nativeID+":") {
		t.Errorf("err = %v; want it naming %s first", err, smallest.nativeID)
	}
}

// A failed services write returns store.ErrStoreWrite and none of the
// services it lost.
func TestScanApplicationSignalsServices_FailedWriteReturnsNoServices(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeCloudWatchService)
	client := appSignalsClient(map[string]func(any) (any, error){
		"ListServices": func(any) (any, error) {
			return &applicationsignals.ListServicesOutput{
				ServiceSummaries: []applicationsignalstypes.ServiceSummary{{KeyAttributes: appSignalsServiceKeyAttrs("checkout")}},
			}, nil
		},
	})

	_, _, services, err := scanApplicationSignalsServices(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, time.Now())
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v; want store.ErrStoreWrite", err)
	}
	if len(services) != 0 {
		t.Errorf("services = %+v; want none", services)
	}
}

func TestScanApplicationSignalsServices_ReturnsStoredInstrumentedServices(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	client := appSignalsClient(map[string]func(any) (any, error){
		"ListServices": func(in any) (any, error) {
			if in.(*applicationsignals.ListServicesInput).NextToken != nil {
				return nil, apiErr("ConflictException", "page 2 fails")
			}
			return &applicationsignals.ListServicesOutput{
				ServiceSummaries: []applicationsignalstypes.ServiceSummary{
					{KeyAttributes: appSignalsServiceKeyAttrs("checkout")},
					{KeyAttributes: map[string]string{"Type": "RemoteService", "Name": "payments", "Environment": "eks:prod/default"}},
					{KeyAttributes: map[string]string{"Type": "AWS::Service", "Name": "AWS::DynamoDB", "Environment": "eks:prod/default"}},
					{KeyAttributes: map[string]string{"Type": "Service", "Name": "no-env"}},
				},
				NextToken: sdkaws.String("p2"),
			}, nil
		},
	})

	total, _, services, err := scanApplicationSignalsServices(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, time.Now())
	if !isAPIErrorCode(err, "ConflictException") {
		t.Errorf("err = %v; want the page 2 ConflictException", err)
	}
	if total != 4 {
		t.Errorf("total = %d; want the 4 services of page 1", total)
	}
	want := []applicationSignalsService{appSignalsTestService("checkout")}
	if !reflect.DeepEqual(services, want) {
		t.Errorf("services = %+v; want %+v", services, want)
	}
}

// The fixture of the whole service scan: ListServiceLevelObjectives serves
// slo-t on its first page and slo-b on its second; one grouping definition;
// the checkout and cart services, each with configurations <svc>-probe1 and
// <svc>-probe2 over two pages. ListTagsForResource answers with tags for
// slo-t and every probe1 configuration, and no tags for the others.

func appSigSLOARN(name string) string {
	return arnFor("aws", "application-signals", testRegion, testAccountID, "slo/"+name)
}

func appSigSLO(name string) applicationsignalstypes.ServiceLevelObjectiveSummary {
	return applicationsignalstypes.ServiceLevelObjectiveSummary{
		Arn: sdkaws.String(appSigSLOARN(name)), Name: sdkaws.String(name), CreatedTime: &appSignalsCreated,
		EvaluationType: applicationsignalstypes.EvaluationTypePeriodBased,
		KeyAttributes:  appSignalsServiceKeyAttrs("checkout"),
	}
}

// appSigTagged is every row the fixture tags, with its tags.
func appSigTagged() map[string]map[string]string {
	return map[string]map[string]string{
		appSigSLOARN("slo-t"):                              {"kind": "slo"},
		appSignalsConfigARN("checkout", "checkout-probe1"): {"kind": "config", "svc": "checkout"},
		appSignalsConfigARN("cart", "cart-probe1"):         {"kind": "config", "svc": "cart"},
	}
}

// appSigTagReads is every ARN the fixture's scan sends ListTagsForResource:
// the SLOs and the configurations, never the services or the grouping
// configuration.
func appSigTagReads() []string {
	arns := []string{appSigSLOARN("slo-t"), appSigSLOARN("slo-b")}
	for _, svc := range []string{"checkout", "cart"} {
		arns = append(arns, appSignalsConfigARN(svc, svc+"-probe1"), appSignalsConfigARN(svc, svc+"-probe2"))
	}
	slices.Sort(arns)
	return arns
}

// appSigCalls records the stubbed calls and answers ListTagsForResource from
// tags and errs, keyed by ARN.
type appSigCalls struct{ *crtCalls }

func newAppSigCalls() *appSigCalls {
	c := &appSigCalls{&crtCalls{newBACCalls()}}
	c.tags = appSigTagged()
	return c
}

func (c *appSigCalls) listTagsAppSig(in any) (any, error) {
	c.called("ListTagsForResource", in)
	arn := sv(in.(*applicationsignals.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	out := &applicationsignals.ListTagsForResourceOutput{}
	for k, v := range tags {
		out.Tags = append(out.Tags, applicationsignalstypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return out, nil
}

// sortedTagARNsAppSig is every ARN sent to ListTagsForResource, sorted.
func (c *appSigCalls) sortedTagARNsAppSig() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	got := slices.Clone(c.tagARNs)
	slices.Sort(got)
	return got
}

var appSigGroupingName = "BusinessUnit"

// appSigHandlers answers every op the scan calls with the fixture.
func appSigHandlers(c *appSigCalls) map[string]func(any) (any, error) {
	var configs parentCalls
	listConfigs := appSignalsConfigHandler(&configs)
	return map[string]func(any) (any, error){
		"ListTagsForResource": c.listTagsAppSig,
		"ListServiceLevelObjectives": c.twoPages("ListServiceLevelObjectives", func(n string, next *string) any {
			return &applicationsignals.ListServiceLevelObjectivesOutput{SloSummaries: []applicationsignalstypes.ServiceLevelObjectiveSummary{appSigSLO("slo-" + n)}, NextToken: next}
		}),
		"ListGroupingAttributeDefinitions": c.answer("ListGroupingAttributeDefinitions", func(any) any {
			return &applicationsignals.ListGroupingAttributeDefinitionsOutput{
				GroupingAttributeDefinitions: []applicationsignalstypes.GroupingAttributeDefinition{{GroupingName: &appSigGroupingName}},
			}
		}),
		"ListServices": c.answer("ListServices", func(any) any {
			return &applicationsignals.ListServicesOutput{
				ServiceSummaries: []applicationsignalstypes.ServiceSummary{
					{KeyAttributes: appSignalsServiceKeyAttrs("checkout")},
					{KeyAttributes: appSignalsServiceKeyAttrs("cart")},
				},
			}
		}),
		"ListInstrumentationConfigurations": func(in any) (any, error) {
			c.called("ListInstrumentationConfigurations", in)
			return listConfigs(in)
		},
	}
}

// appSigScan runs the scan's real entry point against handlers; every op
// they do not answer is denied.
func appSigScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, appSignalsDenied)
	return scanApplicationSignals(ctx, acct, testRegion, st, testScanID)
}

// appSigRowOf is the stored row of typ with NativeID arn.
func appSigRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// The SLOs and the instrumentation configurations store the tags
// ListTagsForResource returns for their ARN, nil for a resource without
// tags, and only their ARNs are sent to the tag op. Every phase runs, every
// listing asks for the model's cap, the configurations are linked under
// their services, and the scan reports no error or warning. The SLO rows
// keep the base scan's Name, Region, CreatedAt and verbatim element.
func TestScanApplicationSignals_StoresTagsOfTaggedTypes(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := recordWarnings(st)
	calls := newAppSigCalls()

	total, inserted, err := appSigScan(context.Background(), st, appSigHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 9 || inserted != 9 {
		t.Errorf("total, inserted = %d, %d; want 9 (2 SLOs, 1 grouping, 2 services, 4 configurations)", total, inserted)
	}
	if len(*warns) != 0 {
		t.Errorf("warnings = %v; want none", *warns)
	}
	if got, want := calls.sortedTagARNsAppSig(), appSigTagReads(); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
	}
	tagged := appSigTagged()
	for _, name := range []string{"slo-t", "slo-b"} {
		arn := appSigSLOARN(name)
		r, ok := appSigRowOf(t, st, TypeApplicationSignalsSLO, arn)
		if !ok {
			t.Errorf("SLO %s not stored", arn)
			continue
		}
		if want := tagged[arn]; want != nil {
			assertTagsMap(t, r.TagsJSON, want)
		} else if r.TagsJSON != nil {
			t.Errorf("SLO %s TagsJSON = %s; want nil", arn, *r.TagsJSON)
		}
		if sv(r.Name) != name || sv(r.Region) != testRegion || sv(r.CreatedAt) != "2026-09-01T08:00:00Z" || r.AttributesJSON != mustJSON(appSigSLO(name)) {
			t.Errorf("SLO %s Name, Region, CreatedAt, attrs = %v, %v, %v, %s", arn, sv(r.Name), sv(r.Region), sv(r.CreatedAt), r.AttributesJSON)
		}
	}
	for _, svc := range []string{"checkout", "cart"} {
		for _, loc := range []string{svc + "-probe1", svc + "-probe2"} {
			arn := appSignalsConfigARN(svc, loc)
			r, ok := appSigRowOf(t, st, TypeApplicationSignalsInstrumentationConfiguration, arn)
			if !ok {
				t.Errorf("configuration %s not stored", arn)
				continue
			}
			if want := tagged[arn]; want != nil {
				assertTagsMap(t, r.TagsJSON, want)
			} else if r.TagsJSON != nil {
				t.Errorf("configuration %s TagsJSON = %s; want nil", arn, *r.TagsJSON)
			}
			if r.AttributesJSON != mustJSON(flattenAppSignalsInstrumentationConfiguration(appSignalsConfig(svc, loc))) {
				t.Errorf("configuration %s attrs = %s", arn, r.AttributesJSON)
			}
			assertContainsNative(t, st, appSignalsTestService(svc).nativeID, arn)
		}
	}
	for _, typ := range []string{TypeCloudWatchService, TypeApplicationSignalsGroupingConfiguration} {
		for arn, r := range rowsByNativeID(t, st, typ) {
			if r.TagsJSON != nil {
				t.Errorf("%s %s TagsJSON = %s; want nil (not tag-read)", typ, arn, *r.TagsJSON)
			}
		}
	}
	for op, want := range map[string][]int32{
		"ListServiceLevelObjectives":        {50, 50},
		"ListServices":                      {100},
		"ListInstrumentationConfigurations": {100, 100, 100, 100},
	} {
		if got := calls.maxResults[op]; !slices.Equal(got, want) {
			t.Errorf("%s MaxResults = %v; want %v", op, got, want)
		}
	}
}

// An aws-us-gov account scanned in us-gov-west-1 sends the gov ARNs its
// listings return to the tag op unchanged and stores their tags.
func TestScanApplicationSignals_GovCloudARNsReachTagOp(t *testing.T) {
	t.Parallel()
	const govRegion = "us-gov-west-1"
	sloARN := arnFor("aws-us-gov", "application-signals", govRegion, testAccountID, "slo/gov")
	configARN := arnFor("aws-us-gov", "application-signals", govRegion, testAccountID, "instrumentationConfig/checkout/eks:prod/default/SNAPSHOT/gov")
	st := newTestStore(t)
	calls := newAppSigCalls()
	calls.tags = map[string]map[string]string{sloARN: {"env": "gov"}, configARN: {"env": "gov-config"}}
	h := appSigHandlers(calls)
	h["ListServiceLevelObjectives"] = func(any) (any, error) {
		return &applicationsignals.ListServiceLevelObjectivesOutput{SloSummaries: []applicationsignalstypes.ServiceLevelObjectiveSummary{{Arn: &sloARN, Name: sdkaws.String("gov")}}}, nil
	}
	h["ListServices"] = func(any) (any, error) {
		return &applicationsignals.ListServicesOutput{ServiceSummaries: []applicationsignalstypes.ServiceSummary{{KeyAttributes: appSignalsServiceKeyAttrs("checkout")}}}, nil
	}
	h["ListInstrumentationConfigurations"] = func(any) (any, error) {
		c := appSignalsConfig("checkout", "gov")
		c.ARN = &configARN
		return &applicationsignals.ListInstrumentationConfigurationsOutput{LatestConfigurations: []applicationsignalstypes.InstrumentationConfigurationWithoutServiceEnv{c}}, nil
	}
	acct := partitionAccount("aws-us-gov")
	acct.cfg = opStubConfig(h, appSignalsDenied)

	total, _, err := scanApplicationSignals(context.Background(), acct, govRegion, st, testScanID)
	if err != nil || total != 4 {
		t.Fatalf("scan = %d, %v; want 4 (SLO, grouping, service, configuration), nil", total, err)
	}
	if got, want := calls.sortedTagARNsAppSig(), []string{configARN, sloARN}; !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
	}
	for _, c := range []struct{ typ, arn, env string }{
		{TypeApplicationSignalsSLO, sloARN, "gov"},
		{TypeApplicationSignalsInstrumentationConfiguration, configARN, "gov-config"},
	} {
		r, ok := appSigRowOf(t, st, c.typ, c.arn)
		if !ok {
			t.Fatalf("%s %s not stored", c.typ, c.arn)
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"env": c.env})
		if sv(r.Region) != govRegion {
			t.Errorf("%s Region = %v; want %s", c.arn, sv(r.Region), govRegion)
		}
	}
}

// No SLO and no service stores nothing tag-read, reads no tags and reports
// nothing.
func TestScanApplicationSignals_NothingToTag(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := recordWarnings(st)
	calls := newAppSigCalls()
	h := appSigHandlers(calls)
	h["ListServiceLevelObjectives"] = func(any) (any, error) { return &applicationsignals.ListServiceLevelObjectivesOutput{}, nil }
	h["ListServices"] = func(any) (any, error) { return &applicationsignals.ListServicesOutput{}, nil }

	total, _, err := appSigScan(context.Background(), st, h)
	if err != nil || total != 1 {
		t.Fatalf("scan = %d, %v; want 1 (the grouping configuration), nil", total, err)
	}
	if len(*warns) != 0 || len(calls.tagARNs) != 0 || calls.count("ListInstrumentationConfigurations") != 0 {
		t.Errorf("warnings, tag reads, configuration listings = %v, %d, %d; want none", *warns, len(calls.tagARNs), calls.count("ListInstrumentationConfigurations"))
	}
}

func TestScanApplicationSignals_RescanInsertsNone(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, _, err := appSigScan(context.Background(), st, appSigHandlers(newAppSigCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := appSigScan(context.Background(), st, appSigHandlers(newAppSigCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != 9 || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want 9, 0", total, inserted)
	}
}

// A failed tag read keeps the row without tags unless the resource is gone:
// a denial warns once under applicationsignals:ListTagsForResource, a throttle
// after retries is a transient error, any other code a hard one; a not-found
// drops the row.
func TestScanApplicationSignals_TagReadFailures(t *testing.T) {
	t.Parallel()
	for _, target := range []struct{ typ, arn string }{
		{TypeApplicationSignalsSLO, appSigSLOARN("slo-t")},
		{TypeApplicationSignalsInstrumentationConfiguration, appSignalsConfigARN("cart", "cart-probe1")},
	} {
		t.Run(target.typ, func(t *testing.T) {
			t.Parallel()
			for _, c := range []struct {
				name      string
				err       error
				kept      bool
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", crDenied, true, 0, 1},
				{"not found", crNotFound, false, 0, 0},
				{"other error", crtBadInput, true, outcomeError, 0},
				{"transient", crThrottled, true, outcomeTransient, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, appSignalsTagsOp)
					calls := newAppSigCalls()
					calls.errs[target.arn] = c.err

					total, _, err := appSigScan(context.Background(), st, appSigHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.outcome == outcomeError {
						assertAPICode(t, err, "ValidationException")
					}
					r, ok := appSigRowOf(t, st, target.typ, target.arn)
					if ok != c.kept {
						t.Fatalf("row stored = %v; want %v", ok, c.kept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					want := 9
					if !c.kept {
						want--
					}
					if total != want || countStoredRows(t, st) != want {
						t.Errorf("total, stored = %d, %d; want %d", total, countStoredRows(t, st), want)
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", appSignalsTagsOp, *warnings, c.wantWarns)
					}
					if c.wantWarns == 1 && (*warnings)[0].Scope != testAccountID+"/"+testRegion {
						t.Errorf("warning Scope = %q; want %s/%s", (*warnings)[0].Scope, testAccountID, testRegion)
					}
				})
			}
		})
	}
}

// Denied tag reads in both tag-read phases are one warning for the scan.
func TestScanApplicationSignals_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warnings := bacWarnings(st, appSignalsTagsOp)
	calls := newAppSigCalls()
	denied := []struct{ typ, arn string }{
		{TypeApplicationSignalsSLO, appSigSLOARN("slo-t")},
		{TypeApplicationSignalsInstrumentationConfiguration, appSignalsConfigARN("checkout", "checkout-probe1")},
	}
	for _, d := range denied {
		calls.errs[d.arn] = crDenied
	}

	total, _, err := appSigScan(context.Background(), st, appSigHandlers(calls))
	if err != nil || total != 9 {
		t.Fatalf("scan = %d, %v; want 9, nil", total, err)
	}
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion {
		t.Errorf("%s warnings = %v; want one, scoped %s/%s", appSignalsTagsOp, *warnings, testAccountID, testRegion)
	}
	for _, d := range denied {
		if r, ok := appSigRowOf(t, st, d.typ, d.arn); !ok || r.TagsJSON != nil {
			t.Errorf("%s stored %v, TagsJSON %v; want it kept without tags", d.arn, ok, r.TagsJSON)
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanApplicationSignals_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, appSignalsTagsOp)
	calls := newAppSigCalls()
	calls.errs[appSigSLOARN("slo-b")] = crThrottled
	calls.errs[appSignalsConfigARN("cart", "cart-probe2")] = crtBadInput

	_, _, err := appSigScan(context.Background(), st, appSigHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", appSignalsTagsOp, *warns)
	}
}

// A failed listing or page keeps the pages read before it and stops none of
// the later phases; the SLO listing's second page failing keeps slo-t with
// its tags.
func TestScanApplicationSignals_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	t.Parallel()
	conflict := apiErr("ConflictException", "bad")
	for _, c := range []struct {
		op        string
		firstPage bool
		lost      int
	}{
		{"ListServiceLevelObjectives", false, 1},
		{"ListServiceLevelObjectives", true, 2},
		{"ListGroupingAttributeDefinitions", true, 1},
	} {
		t.Run(c.op, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			calls := newAppSigCalls()
			h := appSigHandlers(calls)
			list := h[c.op]
			h[c.op] = func(in any) (any, error) {
				if c.firstPage || bacField[*string](in, "NextToken") != nil {
					return nil, conflict
				}
				return list(in)
			}

			total, _, err := appSigScan(context.Background(), st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ConflictException")
			if want := 9 - c.lost; total != want || countStoredRows(t, st) != want {
				t.Errorf("total, stored = %d, %d; want %d", total, countStoredRows(t, st), want)
			}
			if n := len(rowsByNativeID(t, st, TypeApplicationSignalsInstrumentationConfiguration)); n != 4 {
				t.Errorf("configurations stored = %d; want 4", n)
			}
			if !c.firstPage {
				r, ok := appSigRowOf(t, st, TypeApplicationSignalsSLO, appSigSLOARN("slo-t"))
				if !ok {
					t.Fatalf("slo-t from page 1 not stored")
				}
				assertTagsMap(t, r.TagsJSON, map[string]string{"kind": "slo"})
			}
		})
	}
}

// A denied listing warns once under its own op and costs no other phase its
// rows; a denied services listing leaves no service to list configurations
// of.
func TestScanApplicationSignals_DeniedListingWarnsUnderItsOp(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		op   string
		lost int
	}{
		{"ListServiceLevelObjectives", 2},
		{"ListGroupingAttributeDefinitions", 1},
		{"ListServices", 6},
	} {
		t.Run(c.op, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := recordWarnings(st)
			calls := newAppSigCalls()
			h := appSigHandlers(calls)
			h[c.op] = func(any) (any, error) { return nil, appSignalsDenied }

			total, _, err := appSigScan(context.Background(), st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if len(*warns) != 1 || (*warns)[0].Service != "applicationsignals:"+c.op {
				t.Errorf("warnings = %v; want one under applicationsignals:%s", *warns, c.op)
			}
			if want := 9 - c.lost; total != want || countStoredRows(t, st) != want {
				t.Errorf("total, stored = %d, %d; want %d", total, countStoredRows(t, st), want)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping the rows of the
// pages read; the grouping configuration, whose pages make one row, is not
// stored from a partial read.
func TestScanApplicationSignals_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		op, typ, arn string
		kept         bool
	}{
		{"ListServiceLevelObjectives", TypeApplicationSignalsSLO, appSigSLOARN("slo-t"), true},
		{"ListServices", TypeCloudWatchService, appSignalsTestService("checkout").nativeID, true},
		{"ListGroupingAttributeDefinitions", TypeApplicationSignalsGroupingConfiguration, applicationSignalsGroupingConfigurationNativeID("aws", testRegion, testAccountID), false},
		{"ListInstrumentationConfigurations", TypeApplicationSignalsInstrumentationConfiguration, appSignalsConfigARN("checkout", "checkout-probe1"), true},
	} {
		t.Run(c.op, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			calls := newAppSigCalls()
			h := appSigHandlers(calls)
			list := h[c.op]
			h[c.op] = func(in any) (any, error) {
				out, err := list(in)
				reflect.ValueOf(out).Elem().FieldByName("NextToken").Set(reflect.ValueOf(sdkaws.String("same")))
				return out, err
			}

			_, _, err := appSigScan(context.Background(), st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if _, ok := appSigRowOf(t, st, c.typ, c.arn); ok != c.kept {
				t.Errorf("%s stored = %v; want %v", c.arn, ok, c.kept)
			}
			// The configurations are listed once for each of the two services.
			want := 2
			if c.op == "ListInstrumentationConfigurations" {
				want = 4
			}
			if n := calls.count(c.op); n != want {
				t.Errorf("%s calls = %d; want %d (the repeat read once)", c.op, n, want)
			}
		})
	}
}

// An SLO listed without an ARN and a service listed without KeyAttributes
// are not stored and are warned about once under their listing.
func TestScanApplicationSignals_UnstorableItemsWarn(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := recordWarnings(st)
	calls := newAppSigCalls()
	h := appSigHandlers(calls)
	h["ListServiceLevelObjectives"] = func(any) (any, error) {
		return &applicationsignals.ListServiceLevelObjectivesOutput{SloSummaries: []applicationsignalstypes.ServiceLevelObjectiveSummary{
			{Name: sdkaws.String("no-arn")}, appSigSLO("slo-t"),
		}}, nil
	}
	h["ListServices"] = func(any) (any, error) {
		return &applicationsignals.ListServicesOutput{ServiceSummaries: []applicationsignalstypes.ServiceSummary{
			{}, {KeyAttributes: appSignalsServiceKeyAttrs("checkout")},
		}}, nil
	}

	total, _, err := appSigScan(context.Background(), st, h)
	if err != nil || total != 5 {
		t.Fatalf("scan = %d, %v; want 5 (1 SLO, grouping, 1 service, 2 configurations), nil", total, err)
	}
	var services []string
	for _, w := range *warns {
		services = append(services, w.Service)
	}
	slices.Sort(services)
	if want := []string{appSignalsListSLOsOp, appSignalsListServicesOp}; !slices.Equal(services, want) {
		t.Errorf("warnings under %v; want one each under %v", services, want)
	}
}

// A failed write of any type stops the scan before any later listing and
// sends no warning about the rows it lost, their denied tag reads included.
func TestScanApplicationSignals_FailedWriteStopsWithoutWarning(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		typ, deniedARN string
		later          []string
	}{
		{TypeApplicationSignalsSLO, appSigSLOARN("slo-t"), []string{"ListGroupingAttributeDefinitions", "ListServices", "ListInstrumentationConfigurations"}},
		{TypeApplicationSignalsInstrumentationConfiguration, appSignalsConfigARN("cart", "cart-probe1"), nil},
	} {
		t.Run(c.typ, func(t *testing.T) {
			t.Parallel()
			st := newTestStore(t)
			warns := recordWarnings(st)
			refuseStoreWrites(t, st, c.typ)
			calls := newAppSigCalls()
			calls.errs[c.deniedARN] = crDenied

			_, _, err := appSigScan(context.Background(), st, appSigHandlers(calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if len(*warns) != 0 {
				t.Errorf("warnings = %v; want none", *warns)
			}
			for _, op := range c.later {
				if n := calls.count(op); n != 0 {
					t.Errorf("%s called %d times after the failed write", op, n)
				}
			}
		})
	}
}

// A throttled services page is demoted to one warning, so a hard
// configuration error still classifies the service as failed, and the
// configurations of the services on the earlier page are queried.
func TestScanApplicationSignals_ServicesThrottleDemotedBesideHardConfigError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, appSignalsListServicesOp)
	calls := newAppSigCalls()
	h := appSigHandlers(calls)
	h["ListServices"] = func(in any) (any, error) {
		if in.(*applicationsignals.ListServicesInput).NextToken != nil {
			return nil, apiErr("ThrottlingException", "Rate exceeded")
		}
		return &applicationsignals.ListServicesOutput{
			ServiceSummaries: []applicationsignalstypes.ServiceSummary{{KeyAttributes: appSignalsServiceKeyAttrs("checkout")}},
			NextToken:        sdkaws.String("p2"),
		}, nil
	}
	var queried parentCalls
	h["ListInstrumentationConfigurations"] = func(in any) (any, error) {
		queried.add(sdkaws.ToString(in.(*applicationsignals.ListInstrumentationConfigurationsInput).Service))
		return nil, apiErr("ConflictException", "bad")
	}

	_, _, err := appSigScan(context.Background(), st, h)
	if classifyServiceError(err) != outcomeError || *warns != 1 {
		t.Errorf("outcome, warnings = %v, %d; want outcomeError, 1 (err %v)", classifyServiceError(err), *warns, err)
	}
	if got := queried.distinct(); !reflect.DeepEqual(got, []string{"checkout"}) {
		t.Errorf("services queried = %v; want [checkout]", got)
	}
}

// A throttled grouping listing beside a hard SLO failure is demoted to one
// warning under the grouping op, so the hard error decides the outcome.
func TestScanApplicationSignals_GroupingThrottleDemotedUnderItsOp(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := recordWarnings(st)
	h := appSigHandlers(newAppSigCalls())
	h["ListGroupingAttributeDefinitions"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "Rate exceeded") }
	h["ListServiceLevelObjectives"] = func(any) (any, error) { return nil, apiErr("ConflictException", "bad") }

	_, _, err := appSigScan(context.Background(), st, h)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ConflictException")
	if len(*warns) != 1 || (*warns)[0].Service != appSignalsListGroupingOp {
		t.Errorf("warnings = %v; want one under %s", *warns, appSignalsListGroupingOp)
	}
}

// Once ctx ends no further phase starts, and the stop is reported once.
func TestScanApplicationSignals_ContextEndStopsOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newAppSigCalls()
	h := appSigHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h["ListServices"] = func(any) (any, error) {
		cancel()
		return nil, context.Canceled
	}

	_, _, err := appSigScan(ctx, st, h)
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
	if n := calls.count("ListInstrumentationConfigurations"); n != 0 {
		t.Errorf("ListInstrumentationConfigurations called %d times after ctx ended", n)
	}
}

// A region without Application Signals (NXDOMAIN on the SLO listing, nothing
// stored) is unavailable and runs no later listing. An NXDOMAIN after rows
// were stored is a failure of this scan: the counts are kept.
func TestScanApplicationSignals_Unreachable(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "application-signals.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("endpoint absent", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		calls := newAppSigCalls()
		h := appSigHandlers(calls)
		h["ListServiceLevelObjectives"] = func(any) (any, error) { return nil, nx }
		total, _, err := appSigScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 || calls.count("ListServices") != 0 {
			t.Errorf("total, ListServices calls = %d, %d; want 0, 0", total, calls.count("ListServices"))
		}
	})
	t.Run("tag read after rows were listed", func(t *testing.T) {
		t.Parallel()
		st := newTestStore(t)
		calls := newAppSigCalls()
		calls.errs[appSignalsConfigARN("checkout", "checkout-probe2")] = nx
		total, _, err := appSigScan(context.Background(), st, appSigHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
		if total != 9 {
			t.Errorf("total = %d; want 9", total)
		}
	})
}
