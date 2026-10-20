package aws

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	cloudtrailtypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/icearp/disco-cli/store"
)

const (
	ctTestTrailMain      = "main"
	ctTestTrailAudit     = "audit"
	ctTestTrailLegacy    = "legacy"
	ctTestTrailOrg       = "org"
	ctTestTrailMainARN   = "arn:aws:cloudtrail:us-east-1:123456789012:trail/main"
	ctTestTrailAuditARN  = "arn:aws:cloudtrail:us-east-1:123456789012:trail/audit"
	ctTestTrailLegacyARN = "arn:aws:cloudtrail:us-east-1:123456789012:trail/legacy"
	// An organization trail owned by the management account, seen from a
	// delegated administrator.
	ctTestTrailOrgARN = "arn:aws:cloudtrail:us-east-1:999999999999:trail/org"
	ctTestEDS1ARN     = "arn:aws:cloudtrail:us-east-1:123456789012:eventdatastore/eds-1"
	ctTestEDS2ARN     = "arn:aws:cloudtrail:us-east-1:123456789012:eventdatastore/eds-2"
	// eds-3 logs data events only.
	ctTestEDS3ARN = "arn:aws:cloudtrail:us-east-1:123456789012:eventdatastore/eds-3"
	ctTestDestARN = "arn:aws:cloudtrail:us-east-1:123456789012:eventdatastore/insights-dest"

	// GetInsightSelectors call keys: trails are passed by ARN.
	ctInsMain   = "trail:" + ctTestTrailMainARN
	ctInsLegacy = "trail:" + ctTestTrailLegacyARN
	ctInsOrg    = "trail:" + ctTestTrailOrgARN
	// GetEventConfiguration call keys: trails are passed by name.
	ctCfgMain   = "trail:" + ctTestTrailMain
	ctCfgAudit  = "trail:" + ctTestTrailAudit
	ctCfgLegacy = "trail:" + ctTestTrailLegacy

	ctTestMainInsight = ctTestTrailMainARN + "/insight-selector"
	ctTestEDS1Insight = ctTestEDS1ARN + "/insight-selector"
	ctTestMainEvCfg   = ctTestTrailMainARN + "/event-configuration"
	ctTestEDS1EvCfg   = ctTestEDS1ARN + "/event-configuration"
)

var (
	ctDenied   = apiErr("AccessDeniedException", "User: arn:aws:sts::123456789012:assumed-role/disco/scan is not authorized to perform: cloudtrail:Get")
	ctBadInput = apiErr("InvalidParameterException", "bad input")
)

// ctInsightKey and ctEvCfgKey name the parent a call was made for: "trail:"
// plus the TrailName input, or the event data store ARN.
func ctInsightKey(in *cloudtrail.GetInsightSelectorsInput) string {
	if in.TrailName != nil {
		return "trail:" + *in.TrailName
	}
	return sv(in.EventDataStore)
}

func ctEvCfgKey(in *cloudtrail.GetEventConfigurationInput) string {
	if in.TrailName != nil {
		return "trail:" + *in.TrailName
	}
	return sv(in.EventDataStore)
}

// cloudTrailHandlers serves four trails (main with Insights, audit without,
// legacy without HasInsightSelectors, org owned by another account) and three
// event data stores listed over two pages (eds-1 and eds-2 log management
// events, eds-3 data events only). GetInsightSelectors answers main and eds-1
// with selectors, legacy with none and the rest with
// InsightNotEnabledException; GetEventConfiguration answers main with an
// aggregation, eds-1 with Large and context key selectors, and the rest with
// nothing configured. Each configuration call records its parent key.
func cloudTrailHandlers(insightCalls, evCfgCalls *parentCalls) map[string]func(in any) (any, error) {
	management := []cloudtrailtypes.AdvancedEventSelector{{FieldSelectors: []cloudtrailtypes.AdvancedFieldSelector{
		{Field: ptrStr("eventCategory"), Equals: []string{"Management"}},
	}}}
	// eds-2 selects management events in a second selector, as its second field.
	managementLater := []cloudtrailtypes.AdvancedEventSelector{
		{FieldSelectors: []cloudtrailtypes.AdvancedFieldSelector{{Field: ptrStr("readOnly"), Equals: []string{"true"}}}},
		{FieldSelectors: []cloudtrailtypes.AdvancedFieldSelector{
			{Field: ptrStr("readOnly"), Equals: []string{"false"}},
			{Field: ptrStr("eventCategory"), Equals: []string{"Management"}},
		}},
	}
	dataOnly := []cloudtrailtypes.AdvancedEventSelector{{FieldSelectors: []cloudtrailtypes.AdvancedFieldSelector{
		{Field: ptrStr("eventCategory"), Equals: []string{"Data"}},
		{Field: ptrStr("resources.type"), Equals: []string{"Management"}},
	}}}
	return map[string]func(in any) (any, error){
		"DescribeTrails": func(any) (any, error) {
			return &cloudtrail.DescribeTrailsOutput{TrailList: []cloudtrailtypes.Trail{
				{Name: ptrStr(ctTestTrailMain), TrailARN: ptrStr(ctTestTrailMainARN), HasInsightSelectors: sdkaws.Bool(true)},
				{Name: ptrStr(ctTestTrailAudit), TrailARN: ptrStr(ctTestTrailAuditARN), HasInsightSelectors: sdkaws.Bool(false)},
				{Name: ptrStr(ctTestTrailLegacy), TrailARN: ptrStr(ctTestTrailLegacyARN)},
				{Name: ptrStr(ctTestTrailOrg), TrailARN: ptrStr(ctTestTrailOrgARN), HasInsightSelectors: sdkaws.Bool(true), IsOrganizationTrail: sdkaws.Bool(true)},
			}}, nil
		},
		"GetTrailStatus": func(any) (any, error) { return &cloudtrail.GetTrailStatusOutput{IsLogging: sdkaws.Bool(true)}, nil },
		"ListTags":       func(any) (any, error) { return &cloudtrail.ListTagsOutput{}, nil },
		"ListEventDataStores": func(in any) (any, error) {
			if in.(*cloudtrail.ListEventDataStoresInput).NextToken == nil {
				return &cloudtrail.ListEventDataStoresOutput{
					NextToken:       ptrStr("p2"),
					EventDataStores: []cloudtrailtypes.EventDataStore{{EventDataStoreArn: ptrStr(ctTestEDS1ARN)}},
				}, nil
			}
			return &cloudtrail.ListEventDataStoresOutput{
				EventDataStores: []cloudtrailtypes.EventDataStore{{EventDataStoreArn: ptrStr(ctTestEDS2ARN)}, {EventDataStoreArn: ptrStr(ctTestEDS3ARN)}},
			}, nil
		},
		"GetEventDataStore": func(in any) (any, error) {
			arn := sv(in.(*cloudtrail.GetEventDataStoreInput).EventDataStore)
			names := map[string]string{ctTestEDS1ARN: "eds-one", ctTestEDS2ARN: "eds-two", ctTestEDS3ARN: "eds-three"}
			selectors := management
			switch arn {
			case ctTestEDS2ARN:
				selectors = managementLater
			case ctTestEDS3ARN:
				selectors = dataOnly
			}
			return &cloudtrail.GetEventDataStoreOutput{
				EventDataStoreArn: ptrStr(arn), Name: ptrStr(names[arn]),
				Status: cloudtrailtypes.EventDataStoreStatusEnabled, AdvancedEventSelectors: selectors,
			}, nil
		},
		"ListChannels":   func(any) (any, error) { return &cloudtrail.ListChannelsOutput{}, nil },
		"ListDashboards": func(any) (any, error) { return &cloudtrail.ListDashboardsOutput{}, nil },
		"GetResourcePolicy": func(any) (any, error) {
			return nil, apiErr("ResourcePolicyNotFoundException", "no policy")
		},
		"GetInsightSelectors": func(in any) (any, error) {
			key := ctInsightKey(in.(*cloudtrail.GetInsightSelectorsInput))
			insightCalls.add(key)
			switch key {
			case ctInsMain:
				return &cloudtrail.GetInsightSelectorsOutput{
					TrailARN:         ptrStr(ctTestTrailMainARN),
					InsightSelectors: []cloudtrailtypes.InsightSelector{{InsightType: cloudtrailtypes.InsightTypeApiCallRateInsight}},
				}, nil
			case ctTestEDS1ARN:
				return &cloudtrail.GetInsightSelectorsOutput{
					EventDataStoreArn: ptrStr(ctTestEDS1ARN), InsightsDestination: ptrStr(ctTestDestARN),
					InsightSelectors: []cloudtrailtypes.InsightSelector{{InsightType: cloudtrailtypes.InsightTypeApiErrorRateInsight}},
				}, nil
			case ctInsLegacy:
				return &cloudtrail.GetInsightSelectorsOutput{}, nil
			default:
				return nil, apiErr("InsightNotEnabledException", "Insights not enabled")
			}
		},
		"GetEventConfiguration": func(in any) (any, error) {
			key := ctEvCfgKey(in.(*cloudtrail.GetEventConfigurationInput))
			evCfgCalls.add(key)
			switch key {
			case ctCfgMain:
				return &cloudtrail.GetEventConfigurationOutput{
					TrailARN: ptrStr(ctTestTrailMainARN),
					AggregationConfigurations: []cloudtrailtypes.AggregationConfiguration{{
						EventCategory: cloudtrailtypes.EventCategoryAggregationData,
						Templates:     []cloudtrailtypes.Template{cloudtrailtypes.TemplateApiActivity},
					}},
				}, nil
			case ctTestEDS1ARN:
				return &cloudtrail.GetEventConfigurationOutput{
					EventDataStoreArn: ptrStr(ctTestEDS1ARN), MaxEventSize: cloudtrailtypes.MaxEventSizeLarge,
					ContextKeySelectors: []cloudtrailtypes.ContextKeySelector{{Type: cloudtrailtypes.TypeTagContext, Equals: []string{"team"}}},
				}, nil
			case ctTestEDS2ARN:
				return &cloudtrail.GetEventConfigurationOutput{EventDataStoreArn: ptrStr(ctTestEDS2ARN), MaxEventSize: cloudtrailtypes.MaxEventSizeStandard}, nil
			default:
				return &cloudtrail.GetEventConfigurationOutput{}, nil
			}
		},
	}
}

func scanCloudTrailTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, ctDenied)
	total, _, err := scanCloudTrail(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanCloudTrail_StoresParentConfigurations(t *testing.T) {
	st := newTestStore(t)
	var insightCalls, evCfgCalls parentCalls
	total, err := scanCloudTrailTopLevel(t, st, cloudTrailHandlers(&insightCalls, &evCfgCalls))
	if err != nil {
		t.Fatalf("scanCloudTrail: %v", err)
	}
	// 4 trails + 3 event data stores + 2 insight selectors + 2 event configurations
	if total != 11 {
		t.Errorf("total = %d, want 11", total)
	}
	wantInsightCalls := []string{ctTestEDS1ARN, ctTestEDS2ARN, ctInsLegacy, ctInsMain, ctInsOrg}
	if got := insightCalls.distinct(); !slices.Equal(got, wantInsightCalls) {
		t.Errorf("insight selectors read for %v, want %v: not the trail whose HasInsightSelectors is false nor the data-only store", got, wantInsightCalls)
	}
	wantEvCfgCalls := []string{ctTestEDS1ARN, ctTestEDS2ARN, ctTestEDS3ARN, ctCfgAudit, ctCfgLegacy, ctCfgMain}
	if got := evCfgCalls.distinct(); !slices.Equal(got, wantEvCfgCalls) {
		t.Errorf("event configurations read for %v, want %v: every store and every trail the account owns", got, wantEvCfgCalls)
	}

	insights := rowsByNativeID(t, st, TypeCloudTrailInsightSelector)
	if got := nativeIDs(insights); !slices.Equal(got, []string{ctTestEDS1Insight, ctTestMainInsight}) {
		t.Fatalf("insight selector rows = %v, want [%s %s]", got, ctTestEDS1Insight, ctTestMainInsight)
	}
	main := insights[ctTestMainInsight]
	if main.Type != TypeCloudTrailInsightSelector || sv(main.Name) != ctTestTrailMain || sv(main.Region) != testRegion {
		t.Errorf("main insight type/name/region = %q/%q/%q, want %s/%s/%s", main.Type, sv(main.Name), sv(main.Region), TypeCloudTrailInsightSelector, ctTestTrailMain, testRegion)
	}
	var insightAttrs struct {
		InsightSelectors []struct{ InsightType string }
	}
	if err := json.Unmarshal([]byte(main.AttributesJSON), &insightAttrs); err != nil {
		t.Fatalf("attrs %s: %v", main.AttributesJSON, err)
	}
	if len(insightAttrs.InsightSelectors) != 1 || insightAttrs.InsightSelectors[0].InsightType != "ApiCallRateInsight" {
		t.Errorf("main insight attrs = %s, want one ApiCallRateInsight selector", main.AttributesJSON)
	}
	if got := sv(insights[ctTestEDS1Insight].Name); got != "eds-one" {
		t.Errorf("eds-1 insight name = %q, want eds-one", got)
	}

	evCfgs := rowsByNativeID(t, st, TypeCloudTrailEventConfiguration)
	if got := nativeIDs(evCfgs); !slices.Equal(got, []string{ctTestEDS1EvCfg, ctTestMainEvCfg}) {
		t.Fatalf("event configuration rows = %v, want [%s %s]", got, ctTestEDS1EvCfg, ctTestMainEvCfg)
	}
	eds1 := evCfgs[ctTestEDS1EvCfg]
	if eds1.Type != TypeCloudTrailEventConfiguration || sv(eds1.Name) != "eds-one" || sv(eds1.Region) != testRegion {
		t.Errorf("eds-1 event configuration type/name/region = %q/%q/%q, want %s/eds-one/%s", eds1.Type, sv(eds1.Name), sv(eds1.Region), TypeCloudTrailEventConfiguration, testRegion)
	}
	var evCfgAttrs map[string]any
	if err := json.Unmarshal([]byte(eds1.AttributesJSON), &evCfgAttrs); err != nil {
		t.Fatalf("attrs %s: %v", eds1.AttributesJSON, err)
	}
	if evCfgAttrs["MaxEventSize"] != "Large" {
		t.Errorf("eds-1 MaxEventSize = %v, want Large", evCfgAttrs["MaxEventSize"])
	}

	trail := rowsByNativeID(t, st, TypeCloudTrailTrail)[ctTestTrailMainARN]
	var trailAttrs struct{ Status map[string]json.RawMessage }
	if err := json.Unmarshal([]byte(trail.AttributesJSON), &trailAttrs); err != nil {
		t.Fatalf("trail attrs %s: %v", trail.AttributesJSON, err)
	}
	if string(trailAttrs.Status["IsLogging"]) != "true" || strings.Contains(trail.AttributesJSON, "ResultMetadata") {
		t.Errorf("trail attrs = %s, want Status.IsLogging true and no SDK ResultMetadata envelope", trail.AttributesJSON)
	}

	assertContainsNative(t, st, ctTestTrailMainARN, ctTestMainInsight)
	assertContainsNative(t, st, ctTestEDS1ARN, ctTestEDS1Insight)
	assertContainsNative(t, st, ctTestTrailMainARN, ctTestMainEvCfg)
	assertContainsNative(t, st, ctTestEDS1ARN, ctTestEDS1EvCfg)
	assertContainsEdgesDeclared(t, st)
}

func TestScanCloudTrail_NoParentsReadsNoConfigurations(t *testing.T) {
	st := newTestStore(t)
	var insightCalls, evCfgCalls parentCalls
	h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
	h["DescribeTrails"] = func(any) (any, error) { return &cloudtrail.DescribeTrailsOutput{}, nil }
	h["ListEventDataStores"] = func(any) (any, error) { return &cloudtrail.ListEventDataStoresOutput{}, nil }
	total, err := scanCloudTrailTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scanCloudTrail: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
	if n := len(insightCalls.distinct()) + len(evCfgCalls.distinct()); n != 0 {
		t.Errorf("configuration calls = %d, want 0", n)
	}
}

func TestGetCloudTrailEventConfiguration_StoresOnlyConfiguredParents(t *testing.T) {
	aggregation := []cloudtrailtypes.AggregationConfiguration{{EventCategory: cloudtrailtypes.EventCategoryAggregationData}}
	selectors := []cloudtrailtypes.ContextKeySelector{{Type: cloudtrailtypes.TypeRequestContext, Equals: []string{"k"}}}
	for _, tc := range []struct {
		name    string
		out     *cloudtrail.GetEventConfigurationOutput
		wantRow bool
	}{
		{"empty", &cloudtrail.GetEventConfigurationOutput{}, false},
		{"standard size only", &cloudtrail.GetEventConfigurationOutput{MaxEventSize: cloudtrailtypes.MaxEventSizeStandard}, false},
		{"large size", &cloudtrail.GetEventConfigurationOutput{MaxEventSize: cloudtrailtypes.MaxEventSizeLarge}, true},
		{"aggregation", &cloudtrail.GetEventConfigurationOutput{AggregationConfigurations: aggregation}, true},
		{"context key selectors", &cloudtrail.GetEventConfigurationOutput{ContextKeySelectors: selectors}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := ctEvCfgStub{out: tc.out}
			p := childParent{id: ctTestTrailMain, arn: ctTestTrailMainARN}
			r, err := getCloudTrailEventConfiguration(context.Background(), client, &cloudtrail.GetEventConfigurationInput{TrailName: &p.id},
				p, newTestAccount(testAccountID), testRegion, testScanID)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if (r != nil) != tc.wantRow {
				t.Errorf("row = %v, want row %v", r, tc.wantRow)
			}
		})
	}
}

type ctEvCfgStub struct {
	cloudtrailAPI
	out *cloudtrail.GetEventConfigurationOutput
}

func (s ctEvCfgStub) GetEventConfiguration(context.Context, *cloudtrail.GetEventConfigurationInput, ...func(*cloudtrail.Options)) (*cloudtrail.GetEventConfigurationOutput, error) {
	return s.out, nil
}

// A failed parent drops only its own configuration: deleted since listing, or
// an expected store state, is silent; a denial or a modeled per-parent refusal
// warns once per code however many parents hit it; a transient error after
// retries warns once; and any other error fails the scan while the other
// fan-out still stores its rows. Every case runs after every older phase
// stored its rows.
func TestScanCloudTrail_FailedConfigurationParent(t *testing.T) {
	throttled := apiErr("ThrottlingException", "slow down")
	notPermitted := apiErr("OperationNotPermittedException", "no")
	for _, tc := range []struct {
		name      string
		op        string
		fail      map[string]error // parent key -> error
		wantCode  string           // "" means the scan returns nil
		wantRows  []string
		wantWarns int
	}{
		{"insight trail gone", "GetInsightSelectors", map[string]error{ctInsMain: apiErr("TrailNotFoundException", "gone")}, "", []string{ctTestEDS1Insight}, 0},
		{"insight store gone", "GetInsightSelectors", map[string]error{ctTestEDS1ARN: apiErr("EventDataStoreNotFoundException", "gone")}, "", []string{ctTestMainInsight}, 0},
		{"insight denied everywhere", "GetInsightSelectors", map[string]error{ctInsMain: ctDenied, ctTestEDS1ARN: ctDenied, ctTestEDS2ARN: ctDenied}, "", nil, 1},
		{"insight refused for two parents", "GetInsightSelectors", map[string]error{ctInsMain: notPermitted, ctTestEDS1ARN: notPermitted}, "", nil, 1},
		{"insight throttled", "GetInsightSelectors", map[string]error{ctInsMain: throttled, ctTestEDS2ARN: throttled}, "", []string{ctTestEDS1Insight}, 1},
		{"insight other error", "GetInsightSelectors", map[string]error{ctTestEDS1ARN: ctBadInput}, "InvalidParameterException", []string{ctTestMainInsight}, 0},
		{"event configuration store gone", "GetEventConfiguration", map[string]error{ctTestEDS1ARN: apiErr("EventDataStoreNotFoundException", "gone")}, "", []string{ctTestMainEvCfg}, 0},
		{"event configuration trail gone", "GetEventConfiguration", map[string]error{ctCfgMain: apiErr("TrailNotFoundException", "gone")}, "", []string{ctTestEDS1EvCfg}, 0},
		{"event configuration denied everywhere", "GetEventConfiguration", map[string]error{ctCfgMain: ctDenied, ctCfgAudit: ctDenied, ctTestEDS1ARN: ctDenied, ctTestEDS2ARN: ctDenied}, "", nil, 1},
		{"event configuration category refused", "GetEventConfiguration", map[string]error{
			ctTestEDS1ARN: apiErr("InvalidEventDataStoreCategoryException", "no"), ctTestEDS2ARN: apiErr("InvalidEventDataStoreCategoryException", "no"),
		}, "", []string{ctTestMainEvCfg}, 0},
		{"event configuration status refused", "GetEventConfiguration", map[string]error{ctTestEDS1ARN: apiErr("InvalidEventDataStoreStatusException", "no")}, "", []string{ctTestMainEvCfg}, 0},
		{"event configuration two refusal codes", "GetEventConfiguration", map[string]error{
			ctCfgMain: notPermitted, ctCfgAudit: notPermitted, ctTestEDS1ARN: apiErr("UnsupportedOperationException", "no"),
		}, "", nil, 2},
		{"event configuration throttled", "GetEventConfiguration", map[string]error{ctCfgMain: throttled, ctTestEDS2ARN: throttled}, "", []string{ctTestEDS1EvCfg}, 1},
		{"event configuration other error", "GetEventConfiguration", map[string]error{ctCfgMain: ctBadInput}, "InvalidParameterException", []string{ctTestEDS1EvCfg}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "cloudtrail:"+tc.op)
			var insightCalls, evCfgCalls parentCalls
			h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
			next := h[tc.op]
			h[tc.op] = func(in any) (any, error) {
				var key string
				switch p := in.(type) {
				case *cloudtrail.GetInsightSelectorsInput:
					key = ctInsightKey(p)
				case *cloudtrail.GetEventConfigurationInput:
					key = ctEvCfgKey(p)
				}
				if err, ok := tc.fail[key]; ok {
					return nil, err
				}
				return next(in)
			}
			_, err := scanCloudTrailTopLevel(t, st, h)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
				if isTransientNetworkError(err) {
					t.Errorf("err = %v reads as transient, want a hard error", err)
				}
			}
			typ := TypeCloudTrailInsightSelector
			if tc.op == "GetEventConfiguration" {
				typ = TypeCloudTrailEventConfiguration
			}
			if got := nativeIDs(rowsByNativeID(t, st, typ)); !slices.Equal(got, tc.wantRows) {
				t.Errorf("%s rows = %v, want %v", typ, got, tc.wantRows)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			for _, older := range []string{ctTestTrailMainARN, ctTestEDS2ARN} {
				if _, ok := rowsByNativeID(t, st, typeOfCloudTrailParent(older))[older]; !ok {
					t.Errorf("parent %s missing; the configuration phases must run after every older phase", older)
				}
			}
		})
	}
}

func typeOfCloudTrailParent(arn string) string {
	if arn == ctTestTrailMainARN {
		return TypeCloudTrailTrail
	}
	return TypeCloudTrailEventDataStore
}

// A hard error in an older phase still lets the configuration phases run over
// the parents stored before the failure, and is returned.
func TestScanCloudTrail_OlderPhaseErrorStillRunsConfigurations(t *testing.T) {
	st := newTestStore(t)
	var insightCalls, evCfgCalls parentCalls
	h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
	h["ListChannels"] = func(any) (any, error) { return nil, ctBadInput }
	total, err := scanCloudTrailTopLevel(t, st, h)
	assertAPICode(t, err, "InvalidParameterException")
	if got := nativeIDs(rowsByNativeID(t, st, TypeCloudTrailInsightSelector)); !slices.Equal(got, []string{ctTestEDS1Insight, ctTestMainInsight}) {
		t.Errorf("insight selector rows = %v, want both", got)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeCloudTrailEventConfiguration)); !slices.Equal(got, []string{ctTestEDS1EvCfg, ctTestMainEvCfg}) {
		t.Errorf("event configuration rows = %v, want both", got)
	}
	if total != 11 {
		t.Errorf("total = %d, want 11", total)
	}
}

// A transient error in an older phase is demoted to a warning so that a
// configuration phase's hard error still reaches the dispatcher as a hard
// error.
func TestScanCloudTrail_OlderPhaseTransientDoesNotMaskHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "cloudtrail")
	var insightCalls, evCfgCalls parentCalls
	h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
	h["ListChannels"] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "slow down") }
	h["GetEventConfiguration"] = func(any) (any, error) { return nil, ctBadInput }
	_, err := scanCloudTrailTopLevel(t, st, h)
	assertAPICode(t, err, "InvalidParameterException")
	if isTransientNetworkError(err) {
		t.Errorf("err = %v reads as transient; the hard configuration error must not be masked", err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
	}
}
