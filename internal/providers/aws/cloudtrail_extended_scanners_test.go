package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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

const (
	ctTestChannelARN   = "arn:aws:cloudtrail:us-east-1:123456789012:channel/ch-1"
	ctTestDashboardARN = "arn:aws:cloudtrail:us-east-1:123456789012:dashboard/db-1"
)

// ctWithLakeHandlers answers ListChannels and ListDashboards with one item on
// a first page whose NextToken is next (nil: the only page), every later page
// with later, and GetResourcePolicy with a policy for every target.
func ctWithLakeHandlers(h map[string]func(in any) (any, error), next *string, later func() (any, error)) {
	h["ListChannels"] = func(in any) (any, error) {
		if in.(*cloudtrail.ListChannelsInput).NextToken != nil {
			return later()
		}
		return &cloudtrail.ListChannelsOutput{Channels: []cloudtrailtypes.Channel{{ChannelArn: sdkaws.String(ctTestChannelARN), Name: sdkaws.String("ch-1")}}, NextToken: next}, nil
	}
	h["ListDashboards"] = func(in any) (any, error) {
		if in.(*cloudtrail.ListDashboardsInput).NextToken != nil {
			return later()
		}
		return &cloudtrail.ListDashboardsOutput{Dashboards: []cloudtrailtypes.DashboardDetail{{DashboardArn: sdkaws.String(ctTestDashboardARN)}}, NextToken: next}, nil
	}
	h["GetResourcePolicy"] = func(in any) (any, error) {
		arn := in.(*cloudtrail.GetResourcePolicyInput).ResourceArn
		return &cloudtrail.GetResourcePolicyOutput{ResourceArn: arn, ResourcePolicy: sdkaws.String(`{"Version":"2012-10-17"}`)}, nil
	}
}

// A failing Lake listing stops none of the other extended phases: the other
// listing's rows and every listed target's resource policy are stored, and
// the error is returned.
func TestScanCloudTrail_ExtendedPhasesIndependent(t *testing.T) {
	for _, tc := range []struct {
		fail, typ, kept string
	}{
		{"ListChannels", TypeCloudTrailDashboard, ctTestDashboardARN},
		{"ListDashboards", TypeCloudTrailChannel, ctTestChannelARN},
	} {
		t.Run(tc.fail, func(t *testing.T) {
			st := newTestStore(t)
			var insightCalls, evCfgCalls parentCalls
			h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
			ctWithLakeHandlers(h, nil, nil)
			h[tc.fail] = func(any) (any, error) { return nil, ctBadInput }
			_, err := scanCloudTrailTopLevel(t, st, h)
			assertAPICode(t, err, "InvalidParameterException")
			if _, ok := rowsByNativeID(t, st, tc.typ)[tc.kept]; !ok {
				t.Errorf("%s not stored", tc.kept)
			}
			policies := rowsByNativeID(t, st, TypeCloudTrailResourcePolicy)
			for _, target := range []string{tc.kept, ctTestEDS1ARN} {
				if _, ok := policies[target+"/policy"]; !ok {
					t.Errorf("resource policy of %s not stored; got %v", target, nativeIDs(policies))
				}
			}
		})
	}
}

// A Lake listing that fails or repeats its token after its first page keeps
// that page's item and reads its resource policy, and returns the error.
func TestScanCloudTrail_LakeLaterPageFailureKeepsEarlierPages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		later   func() (any, error)
		wantErr func(error) bool
	}{
		{"error", func() (any, error) { return nil, ctBadInput }, func(err error) bool { return isAPIErrorCode(err, "InvalidParameterException") }},
		{"repeated token", func() (any, error) {
			return &cloudtrail.ListChannelsOutput{NextToken: sdkaws.String("p2")}, nil
		}, func(err error) bool { return errors.Is(err, errRepeatedNextToken) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var insightCalls, evCfgCalls parentCalls
			h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
			var laterCalls int
			ctWithLakeHandlers(h, sdkaws.String("p2"), func() (any, error) {
				if laterCalls++; laterCalls > 2 {
					return &cloudtrail.ListChannelsOutput{}, nil // a stop that did not happen
				}
				return tc.later()
			})
			// Dashboards list one page, so only the channel listing fails.
			h["ListDashboards"] = func(any) (any, error) { return &cloudtrail.ListDashboardsOutput{}, nil }
			_, err := scanCloudTrailTopLevel(t, st, h)
			if !tc.wantErr(err) {
				t.Errorf("err = %v, want the second page's failure", err)
			}
			if laterCalls != 1 {
				t.Errorf("ListChannels calls after the first page = %d, want 1", laterCalls)
			}
			if _, ok := rowsByNativeID(t, st, TypeCloudTrailChannel)[ctTestChannelARN]; !ok {
				t.Error("channel from the first page not stored")
			}
			if _, ok := rowsByNativeID(t, st, TypeCloudTrailResourcePolicy)[ctTestChannelARN+"/policy"]; !ok {
				t.Error("resource policy of the first page's channel not stored")
			}
		})
	}
}

// A channel or dashboard listed without an ARN is not stored and is warned
// about once under its list op.
func TestScanCloudTrail_ARNlessLakeItemsWarn(t *testing.T) {
	st := newTestStore(t)
	var mu sync.Mutex
	warns := map[string]int{}
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		warns[w.Service]++
		mu.Unlock()
	}
	var insightCalls, evCfgCalls parentCalls
	h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
	h["ListChannels"] = func(any) (any, error) {
		return &cloudtrail.ListChannelsOutput{Channels: []cloudtrailtypes.Channel{{Name: sdkaws.String("no-arn")}}}, nil
	}
	h["ListDashboards"] = func(any) (any, error) {
		return &cloudtrail.ListDashboardsOutput{Dashboards: []cloudtrailtypes.DashboardDetail{{Type: cloudtrailtypes.DashboardTypeCustom}}}, nil
	}
	if _, err := scanCloudTrailTopLevel(t, st, h); err != nil {
		t.Fatalf("scanCloudTrail: %v", err)
	}
	if warns["cloudtrail:ListChannels"] != 1 || warns["cloudtrail:ListDashboards"] != 1 {
		t.Errorf("warnings: ListChannels = %d, ListDashboards = %d; want 1 each", warns["cloudtrail:ListChannels"], warns["cloudtrail:ListDashboards"])
	}
}

// The event data stores whose resource policies are read are those the
// event-data-store phase stored: a store GetEventDataStore no longer finds is
// neither stored nor read, and the stores are listed once.
func TestScanCloudTrail_PoliciesReadForStoredEventDataStores(t *testing.T) {
	st := newTestStore(t)
	var insightCalls, evCfgCalls parentCalls
	h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
	ctWithLakeHandlers(h, nil, nil)
	listEDS, getEDS := h["ListEventDataStores"], h["GetEventDataStore"]
	var listed atomic.Int32
	h["ListEventDataStores"] = func(in any) (any, error) {
		listed.Add(1)
		return listEDS(in)
	}
	h["GetEventDataStore"] = func(in any) (any, error) {
		if sv(in.(*cloudtrail.GetEventDataStoreInput).EventDataStore) == ctTestEDS1ARN {
			return nil, apiErr("EventDataStoreNotFoundException", "gone")
		}
		return getEDS(in)
	}
	if _, err := scanCloudTrailTopLevel(t, st, h); err != nil {
		t.Fatalf("scanCloudTrail: %v", err)
	}
	policies := rowsByNativeID(t, st, TypeCloudTrailResourcePolicy)
	if _, ok := policies[ctTestEDS1ARN+"/policy"]; ok {
		t.Errorf("resource policy of the vanished %s stored", ctTestEDS1ARN)
	}
	if _, ok := policies[ctTestEDS2ARN+"/policy"]; !ok {
		t.Errorf("resource policy of %s not stored; got %v", ctTestEDS2ARN, nativeIDs(policies))
	}
	if n := listed.Load(); n != 2 {
		t.Errorf("ListEventDataStores calls = %d, want 2 (its two pages, once)", n)
	}
}

// A denied GetResourcePolicy skips that target with one warning for all such;
// any other error ends the reads, and the policies read before it are stored.
func TestScanCloudTrail_ResourcePolicyFailures(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "cloudtrail:GetResourcePolicy")
	var insightCalls, evCfgCalls parentCalls
	h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
	ctWithLakeHandlers(h, nil, nil)
	get := h["GetResourcePolicy"]
	// The event data stores' order is not fixed: the first one read succeeds
	// and the next fails.
	var readFirst string
	h["GetResourcePolicy"] = func(in any) (any, error) {
		switch arn := sv(in.(*cloudtrail.GetResourcePolicyInput).ResourceArn); {
		case arn == ctTestChannelARN, arn == ctTestDashboardARN:
			return nil, ctDenied
		case readFirst != "":
			return nil, ctBadInput
		default:
			readFirst = arn
		}
		return get(in)
	}
	_, err := scanCloudTrailTopLevel(t, st, h)
	assertAPICode(t, err, "InvalidParameterException")
	if _, ok := rowsByNativeID(t, st, TypeCloudTrailResourcePolicy)[readFirst+"/policy"]; !ok {
		t.Errorf("resource policy of %q, read before the failure, not stored", readFirst)
	}
	if *warns != 1 {
		t.Errorf("GetResourcePolicy warnings = %d, want 1", *warns)
	}
}

// A failed GetChannel or GetDashboard keeps the item as its list element:
// denied and transient reads warn once and the scan succeeds, any other error
// is returned. An item the read no longer finds was deleted and is not stored.
func TestScanCloudTrail_LakeDetailFailureKeepsListElement(t *testing.T) {
	transient := &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset")}
	for _, op := range []struct {
		name, label, typ, arn, notFound string
	}{
		{"GetChannel", "cloudtrail:GetChannel", TypeCloudTrailChannel, ctTestChannelARN, "ChannelNotFoundException"},
		{"GetDashboard", "cloudtrail:GetDashboard", TypeCloudTrailDashboard, ctTestDashboardARN, "ResourceNotFoundException"},
	} {
		for _, tc := range []struct {
			name      string
			err       error
			wantCode  string // "" means the scan returns nil
			wantWarns int
			wantRow   bool
		}{
			{"read", nil, "", 0, true},
			{"denied", ctDenied, "", 1, true},
			{"transient", transient, "", 1, true},
			{"other", ctBadInput, "InvalidParameterException", 0, true},
			{"not found", apiErr(op.notFound, "gone"), "", 0, false},
		} {
			t.Run(op.name+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, op.label)
				var insightCalls, evCfgCalls parentCalls
				h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
				ctWithLakeHandlers(h, nil, nil)
				h["GetChannel"] = func(any) (any, error) {
					return &cloudtrail.GetChannelOutput{ChannelArn: sdkaws.String(ctTestChannelARN), Name: sdkaws.String("ch-1"), Source: sdkaws.String("Custom")}, nil
				}
				h["GetDashboard"] = func(any) (any, error) {
					return &cloudtrail.GetDashboardOutput{DashboardArn: sdkaws.String(ctTestDashboardARN), Status: cloudtrailtypes.DashboardStatusCreated}, nil
				}
				if tc.err != nil {
					h[op.name] = func(any) (any, error) { return nil, tc.err }
				}
				_, err := scanCloudTrailTopLevel(t, st, h)
				if tc.wantCode != "" {
					assertAPICode(t, err, tc.wantCode)
				} else if err != nil {
					t.Fatalf("scanCloudTrail: %v", err)
				}
				if *warns != tc.wantWarns {
					t.Errorf("%s warnings = %d, want %d", op.label, *warns, tc.wantWarns)
				}
				row, ok := rowsByNativeID(t, st, op.typ)[op.arn]
				if ok != tc.wantRow {
					t.Fatalf("%s stored = %v, want %v", op.arn, ok, tc.wantRow)
				}
				if !ok {
					return
				}
				var attrs map[string]any
				if err := json.Unmarshal([]byte(row.AttributesJSON), &attrs); err != nil {
					t.Fatal(err)
				}
				_, hasSource := attrs["Source"]
				_, hasStatus := attrs["Status"]
				if detailed := hasSource || hasStatus; detailed != (tc.err == nil) {
					t.Errorf("%s attrs = %s; want the %s body: %v", op.arn, row.AttributesJSON, op.name, tc.err == nil)
				}
			})
		}
	}
}

// A failed write stores no channel, so the warning that a channel was stored
// as its list element is not given.
func TestScanCTChannels_NoKeptWarningAfterFailedWrite(t *testing.T) {
	// Not newTestStore: its cleanup queries the DB, which a closed store can't serve.
	st, err := store.Open(filepath.Join(t.TempDir(), "closing.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	insertTestScan(t, st, testScanID, "running")
	warns := countOpWarnings(st, "cloudtrail:GetChannel")
	client := cloudtrail.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
		"ListChannels": func(any) (any, error) {
			return &cloudtrail.ListChannelsOutput{Channels: []cloudtrailtypes.Channel{{ChannelArn: sdkaws.String(ctTestChannelARN), Name: sdkaws.String("ch-1")}}}, nil
		},
		"GetChannel": func(any) (any, error) {
			if err := st.Close(); err != nil {
				t.Errorf("close: %v", err)
			}
			return nil, ctDenied
		},
	}, ctDenied))
	_, _, _, err = scanCTChannels(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("GetChannel warnings = %d, want 0: the channel was not stored", *warns)
	}
}

// A GetResourcePolicy that still fails transiently after the SDK's retries
// skips only that target, with one warning; the other targets' policies are
// read and stored and the scan succeeds.
func TestScanCloudTrail_TransientResourcePolicySkipsOnlyItsTarget(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "cloudtrail:GetResourcePolicy")
	var insightCalls, evCfgCalls parentCalls
	h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
	ctWithLakeHandlers(h, nil, nil)
	get := h["GetResourcePolicy"]
	h["GetResourcePolicy"] = func(in any) (any, error) {
		if sv(in.(*cloudtrail.GetResourcePolicyInput).ResourceArn) == ctTestChannelARN {
			return nil, &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset")}
		}
		return get(in)
	}
	if _, err := scanCloudTrailTopLevel(t, st, h); err != nil {
		t.Fatalf("scanCloudTrail: %v", err)
	}
	policies := rowsByNativeID(t, st, TypeCloudTrailResourcePolicy)
	for _, target := range []string{ctTestDashboardARN, ctTestEDS1ARN, ctTestEDS2ARN} {
		if _, ok := policies[target+"/policy"]; !ok {
			t.Errorf("resource policy of %s not stored; got %v", target, nativeIDs(policies))
		}
	}
	if *warns != 1 {
		t.Errorf("GetResourcePolicy warnings = %d, want 1", *warns)
	}
}
