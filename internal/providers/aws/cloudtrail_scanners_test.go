package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	cloudtrailtypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/icearp/disco-cli/store"
)

var ctAllTrails = []string{ctTestTrailAuditARN, ctTestTrailLegacyARN, ctTestTrailMainARN, ctTestTrailOrgARN}

// ctFailTrailStatus answers GetTrailStatus for the trails in fail with their
// error and for the others as base does.
func ctFailTrailStatus(h map[string]func(in any) (any, error), fail map[string]error) {
	base := h["GetTrailStatus"]
	h["GetTrailStatus"] = func(in any) (any, error) {
		if err := fail[sv(in.(*cloudtrail.GetTrailStatusInput).Name)]; err != nil {
			return nil, err
		}
		return base(in)
	}
}

// GetTrailStatus keeps the trail without its Status on a denial or a
// transient error, with one warning per cause; drops only an own trail that
// vanished (the op's modeled not-found), while another account's trail
// answering not-found is kept without Status and warned about once; and on
// anything else keeps the trail without Status and fails after the other
// trails are stored and the event-data-store phase ran; an NXDOMAIN, which
// comes after DescribeTrails reached the host, is such an error, not a
// transient one. (ListTags failures: TestScanCloudTrail_TagReadFailures.)
func TestScanCloudTrail_PerTrailReadErrors(t *testing.T) {
	throttled := apiErr("ThrottlingException", "Rate exceeded")
	nx := &net.DNSError{Err: "no such host", Name: "cloudtrail.us-east-1.amazonaws.com", IsNotFound: true}
	cases := []struct {
		name, op   string
		fail       map[string]error
		wantTrails []string
		wantWarns  int
		wantErr    func(error) bool // nil: the scan returns nil
	}{
		{"status denied", "GetTrailStatus", map[string]error{ctTestTrailMainARN: ctDenied, ctTestTrailAuditARN: ctDenied}, ctAllTrails, 1, nil},
		{"status transient", "GetTrailStatus", map[string]error{ctTestTrailMainARN: throttled, ctTestTrailAuditARN: throttled}, ctAllTrails, 1, nil},
		{
			"status trail gone", "GetTrailStatus",
			map[string]error{ctTestTrailMainARN: apiErr("TrailNotFoundException", "gone")},
			[]string{ctTestTrailAuditARN, ctTestTrailLegacyARN, ctTestTrailOrgARN},
			0, nil,
		},
		{
			"status other error", "GetTrailStatus",
			map[string]error{ctTestTrailAuditARN: apiErr("CloudTrailARNInvalidException", "bad arn")},
			ctAllTrails, 0,
			func(err error) bool { return isAPIErrorCode(err, "CloudTrailARNInvalidException") },
		},
		{
			"status late NXDOMAIN", "GetTrailStatus",
			map[string]error{ctTestTrailAuditARN: nx},
			ctAllTrails, 0,
			func(err error) bool { return err != nil && !isDNSNotFound(err) && !isTransientNetworkError(err) },
		},
		// The organization trail is owned by another account: not-found keeps it.
		{"status org trail not found", "GetTrailStatus", map[string]error{ctTestTrailOrgARN: apiErr("TrailNotFoundException", "not here")}, ctAllTrails, 1, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "cloudtrail:"+tc.op)
			var insightCalls, evCfgCalls parentCalls
			h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
			ctFailTrailStatus(h, tc.fail)
			_, err := scanCloudTrailTopLevel(t, st, h)
			switch {
			case tc.wantErr == nil && err != nil:
				t.Fatalf("scanCloudTrail: %v", err)
			case tc.wantErr != nil && !tc.wantErr(err):
				t.Fatalf("err = %v; not the error wanted", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("cloudtrail:%s warnings = %d, want %d", tc.op, *warns, tc.wantWarns)
			}
			trails := rowsByNativeID(t, st, TypeCloudTrailTrail)
			if got := nativeIDs(trails); !slices.Equal(got, tc.wantTrails) {
				t.Errorf("trail rows = %v, want %v", got, tc.wantTrails)
			}
			for arn, r := range trails {
				var attrs struct{ Status json.RawMessage }
				if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
					t.Fatalf("attrs of %s: %v", arn, err)
				}
				lostStatus := tc.fail[arn] != nil
				if gotNull := string(attrs.Status) == "null"; gotNull != lostStatus {
					t.Errorf("%s Status = %s, want null only when its status read failed", arn, attrs.Status)
				}
			}
			if n := len(rowsByNativeID(t, st, TypeCloudTrailEventDataStore)); n != 3 {
				t.Errorf("event data store rows = %d, want 3 whatever the trail phase returned", n)
			}
		})
	}
}

// A failed GetEventDataStore keeps the store as its ListEventDataStores
// element, still a parent of the configuration phases but not of the Insights
// phase, since its selectors are unknown: a denial warns once and the scan
// succeeds; a transient error, and an NXDOMAIN once the listing reached the
// host, are returned, so the scan reads as partial or failed, as is any other
// error, after the stores are stored and the later phases have run. A store
// the read no longer finds is not stored and is no parent.
func TestScanCloudTrail_GetEventDataStoreErrors(t *testing.T) {
	all := []string{ctTestEDS1ARN, ctTestEDS2ARN, ctTestEDS3ARN}
	nx := &net.DNSError{Err: "no such host", Name: "cloudtrail.us-east-1.amazonaws.com", IsNotFound: true}
	for _, tc := range []struct {
		name       string
		err        error
		wantErr    func(error) bool // nil: the scan returns nil
		wantStores []string
		wantKept   bool // eds-1 is stored as its list element
		wantWarns  int
	}{
		{"denied", ctDenied, nil, all, true, 1},
		{"transient", apiErr("ThrottlingException", "Rate exceeded"), isTransientNetworkError, all, true, 0},
		{"late NXDOMAIN", nx, func(err error) bool { return err != nil && !isDNSNotFound(err) && !isTransientNetworkError(err) }, all, true, 0},
		{"gone", apiErr("EventDataStoreNotFoundException", "gone"), nil, []string{ctTestEDS2ARN, ctTestEDS3ARN}, false, 0},
		{"other error", ctBadInput, func(err error) bool {
			return isAPIErrorCode(err, "InvalidParameterException") && !isTransientNetworkError(err)
		}, all, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "cloudtrail:GetEventDataStore")
			var insightCalls, evCfgCalls parentCalls
			h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
			get := h["GetEventDataStore"]
			h["GetEventDataStore"] = func(in any) (any, error) {
				if sv(in.(*cloudtrail.GetEventDataStoreInput).EventDataStore) == ctTestEDS1ARN {
					return nil, tc.err
				}
				return get(in)
			}
			var channelCalls atomic.Int32
			channels := h["ListChannels"]
			h["ListChannels"] = func(in any) (any, error) {
				channelCalls.Add(1)
				return channels(in)
			}
			_, err := scanCloudTrailTopLevel(t, st, h)
			switch {
			case tc.wantErr == nil && err != nil:
				t.Fatalf("scanCloudTrail: %v", err)
			case tc.wantErr != nil && !tc.wantErr(err):
				t.Fatalf("err = %v; not the error wanted", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("cloudtrail:GetEventDataStore warnings = %d, want %d", *warns, tc.wantWarns)
			}
			stores := rowsByNativeID(t, st, TypeCloudTrailEventDataStore)
			if got := nativeIDs(stores); !slices.Equal(got, tc.wantStores) {
				t.Fatalf("event data store rows = %v, want %v", got, tc.wantStores)
			}
			if channelCalls.Load() == 0 {
				t.Error("ListChannels never called: the extended phases must run")
			}
			if got := slices.Contains(evCfgCalls.distinct(), ctTestEDS1ARN); got != tc.wantKept {
				t.Errorf("eds-1 read as a configuration parent = %v, want %v (only when stored)", got, tc.wantKept)
			}
			if slices.Contains(insightCalls.distinct(), ctTestEDS1ARN) {
				t.Error("eds-1 read as an Insights parent; its selectors were not read")
			}
			if !tc.wantKept {
				return
			}
			// The list element, verbatim: a field the list no longer returns is
			// null, never a false value.
			var attrs map[string]any
			if err := json.Unmarshal([]byte(stores[ctTestEDS1ARN].AttributesJSON), &attrs); err != nil {
				t.Fatal(err)
			}
			if v, ok := attrs["TerminationProtectionEnabled"]; !ok || v != nil || attrs["EventDataStoreArn"] != ctTestEDS1ARN {
				t.Errorf("attrs = %s, want the list element with TerminationProtectionEnabled null", stores[ctTestEDS1ARN].AttributesJSON)
			}
		})
	}
}

// Transient phase errors with no hard error reach the dispatcher as they are,
// so the scan is partial; beside a hard error in another phase they are
// demoted to warnings, so the hard error is not read as transient.
func TestScanCloudTrail_PhaseErrorJoin(t *testing.T) {
	throttled := apiErr("ThrottlingException", "Rate exceeded")
	t.Run("lone transient", func(t *testing.T) {
		st := newTestStore(t)
		var insightCalls, evCfgCalls parentCalls
		h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
		h["DescribeTrails"] = func(any) (any, error) { return nil, throttled }
		_, err := scanCloudTrailTopLevel(t, st, h)
		if !isTransientNetworkError(err) {
			t.Errorf("err = %v, want the transient DescribeTrails error", err)
		}
	})
	// Two transients and no hard error: nothing is demoted, so the scan still
	// reads as partial rather than complete.
	t.Run("two transients", func(t *testing.T) {
		st := newTestStore(t)
		var insightCalls, evCfgCalls parentCalls
		h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
		h["DescribeTrails"] = func(any) (any, error) { return nil, throttled }
		h["ListEventDataStores"] = func(any) (any, error) { return nil, throttled }
		_, err := scanCloudTrailTopLevel(t, st, h)
		if !isTransientNetworkError(err) {
			t.Errorf("err = %v, want the transient errors", err)
		}
	})
	t.Run("transient beside hard", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "cloudtrail:DescribeTrails")
		var insightCalls, evCfgCalls parentCalls
		h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
		h["DescribeTrails"] = func(any) (any, error) { return nil, throttled }
		h["GetEventDataStore"] = func(any) (any, error) { return nil, ctBadInput }
		_, err := scanCloudTrailTopLevel(t, st, h)
		assertAPICode(t, err, "InvalidParameterException")
		if isTransientNetworkError(err) {
			t.Errorf("err = %v reads as transient; the hard error must not be masked", err)
		}
		if *warns != 1 {
			t.Errorf("cloudtrail:DescribeTrails warnings = %d, want 1 for the demoted transient", *warns)
		}
	})
}

// A trail listed without an ARN is not read or stored and is warned about
// once; its siblings are stored.
func TestScanCloudTrail_ARNlessTrailWarns(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "cloudtrail:DescribeTrails")
	var insightCalls, evCfgCalls parentCalls
	h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
	list := h["DescribeTrails"]
	h["DescribeTrails"] = func(in any) (any, error) {
		out, err := list(in)
		if err != nil {
			return nil, err
		}
		o := out.(*cloudtrail.DescribeTrailsOutput)
		o.TrailList = append(o.TrailList, cloudtrailtypes.Trail{Name: ptrStr("arnless")})
		return o, nil
	}
	var emptyReads atomic.Int32
	status := h["GetTrailStatus"]
	h["GetTrailStatus"] = func(in any) (any, error) {
		if sv(in.(*cloudtrail.GetTrailStatusInput).Name) == "" {
			emptyReads.Add(1)
		}
		return status(in)
	}
	if _, err := scanCloudTrailTopLevel(t, st, h); err != nil {
		t.Fatalf("scanCloudTrail: %v", err)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeCloudTrailTrail)); !slices.Equal(got, ctAllTrails) {
		t.Errorf("trail rows = %q, want %q", got, ctAllTrails)
	}
	if *warns != 1 || emptyReads.Load() != 0 {
		t.Errorf("DescribeTrails warnings = %d, GetTrailStatus calls with no ARN = %d; want 1, 0", *warns, emptyReads.Load())
	}
}

// A GetEventDataStore body without EventDataStoreArn is stored under the ARN
// the store was listed and read by, never under an empty NativeID.
func TestScanCloudTrail_EventDataStoreBodyWithoutARN(t *testing.T) {
	st := newTestStore(t)
	var insightCalls, evCfgCalls parentCalls
	h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
	get := h["GetEventDataStore"]
	h["GetEventDataStore"] = func(in any) (any, error) {
		out, err := get(in)
		if err == nil && sv(in.(*cloudtrail.GetEventDataStoreInput).EventDataStore) == ctTestEDS1ARN {
			out.(*cloudtrail.GetEventDataStoreOutput).EventDataStoreArn = nil
		}
		return out, err
	}
	if _, err := scanCloudTrailTopLevel(t, st, h); err != nil {
		t.Fatalf("scanCloudTrail: %v", err)
	}
	want := []string{ctTestEDS1ARN, ctTestEDS2ARN, ctTestEDS3ARN}
	if got := nativeIDs(rowsByNativeID(t, st, TypeCloudTrailEventDataStore)); !slices.Equal(got, want) {
		t.Errorf("event data store rows = %q, want %q", got, want)
	}
}

// An event data store listed without an ARN is not stored and is warned about
// once; its siblings are stored.
func TestScanCloudTrail_ARNlessEventDataStoreWarns(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "cloudtrail:ListEventDataStores")
	var insightCalls, evCfgCalls parentCalls
	h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
	h["ListEventDataStores"] = func(any) (any, error) {
		return &cloudtrail.ListEventDataStoresOutput{EventDataStores: []cloudtrailtypes.EventDataStore{
			{EventDataStoreArn: ptrStr(ctTestEDS1ARN)}, {Name: ptrStr("arnless")},
		}}, nil
	}
	if _, err := scanCloudTrailTopLevel(t, st, h); err != nil {
		t.Fatalf("scanCloudTrail: %v", err)
	}
	if *warns != 1 {
		t.Errorf("cloudtrail:ListEventDataStores warnings = %d, want 1", *warns)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeCloudTrailEventDataStore)); !slices.Equal(got, []string{ctTestEDS1ARN}) {
		t.Errorf("event data store rows = %v, want only %s", got, ctTestEDS1ARN)
	}
}

// A trail kept without its Status is warned about only once it is stored: a
// failed write returns the store error and no "stored without" warning.
func TestScanCloudTrail_KeptTrailWarnsOnlyOnceStored(t *testing.T) {
	st, closeStore := openClosingStore(t)
	warns := countOpWarnings(st, "cloudtrail:GetTrailStatus")
	var insightCalls, evCfgCalls parentCalls
	h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
	h["GetTrailStatus"] = func(any) (any, error) {
		closeStore()
		return nil, ctDenied
	}
	_, err := scanCloudTrailTopLevel(t, st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v, want the store write failure", err)
	}
	if *warns != 0 {
		t.Errorf("cloudtrail:GetTrailStatus warnings = %d, want 0: no trail was stored", *warns)
	}
}

// ctTagCalls records the ResourceIdList of each ListTags call, in call order.
type ctTagCalls struct {
	mu    sync.Mutex
	calls [][]string
}

func (c *ctTagCalls) add(arns []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, slices.Clone(arns))
}

func (c *ctTagCalls) get() [][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.calls)
}

// ctListTags answers ListTags with each ARN sent, tagged as tags says
// (untagged when absent), unless the call was sent an ARN in fail: then the
// whole call fails with that ARN's error, as one resource's refusal fails a
// ListTags call. calls, when not nil, records each call.
func ctListTags(tags map[string]map[string]string, fail map[string]error, calls *ctTagCalls) func(in any) (any, error) {
	return func(in any) (any, error) {
		arns := in.(*cloudtrail.ListTagsInput).ResourceIdList
		if calls != nil {
			calls.add(arns)
		}
		for _, arn := range arns {
			if err := fail[arn]; err != nil {
				return nil, err
			}
		}
		out := &cloudtrail.ListTagsOutput{}
		for _, arn := range arns {
			var list []cloudtrailtypes.Tag
			for k, v := range tags[arn] {
				list = append(list, cloudtrailtypes.Tag{Key: ptrStr(k), Value: ptrStr(v)})
			}
			out.ResourceTagList = append(out.ResourceTagList, cloudtrailtypes.ResourceTag{ResourceId: ptrStr(arn), TagsList: list})
		}
		return out, nil
	}
}

const (
	ctTestChannel2ARN         = "arn:aws:cloudtrail:us-east-1:123456789012:channel/ch-2"
	ctTestManagedChannelARN   = "arn:aws:cloudtrail:us-east-1:123456789012:channel/svc-1"
	ctTestDashboard2ARN       = "arn:aws:cloudtrail:us-east-1:123456789012:dashboard/db-2"
	ctTestManagedDashboardARN = "arn:aws:cloudtrail:us-east-1:123456789012:dashboard/AWSCloudTrail-Activity"
)

// ctWithAllLakeItems adds to h a page cap recorder (pageCaps, the MaxResults
// each Lake listing sent) and two custom items and one managed item of each
// Lake type: channels ch-1 and ch-2 and service-linked svc-1, custom
// dashboards db-1 and db-2 and the MANAGED AWSCloudTrail-Activity, each with
// its detail body.
func ctWithAllLakeItems(h map[string]func(in any) (any, error), pageCaps map[string]int32) {
	var mu sync.Mutex
	record := func(op string, maxResults *int32) {
		mu.Lock()
		defer mu.Unlock()
		pageCaps[op] = sdkaws.ToInt32(maxResults)
	}
	listEDS := h["ListEventDataStores"]
	h["ListEventDataStores"] = func(in any) (any, error) {
		record("ListEventDataStores", in.(*cloudtrail.ListEventDataStoresInput).MaxResults)
		return listEDS(in)
	}
	h["ListChannels"] = func(in any) (any, error) {
		record("ListChannels", in.(*cloudtrail.ListChannelsInput).MaxResults)
		return &cloudtrail.ListChannelsOutput{Channels: []cloudtrailtypes.Channel{
			{ChannelArn: ptrStr(ctTestChannelARN), Name: ptrStr("ch-1")},
			{ChannelArn: ptrStr(ctTestChannel2ARN), Name: ptrStr("ch-2")},
			{ChannelArn: ptrStr(ctTestManagedChannelARN), Name: ptrStr("aws-service-channel/securitylake/svc-1")},
		}}, nil
	}
	h["GetChannel"] = func(in any) (any, error) {
		arn := in.(*cloudtrail.GetChannelInput).Channel
		return &cloudtrail.GetChannelOutput{ChannelArn: arn, Source: ptrStr("Custom")}, nil
	}
	h["ListDashboards"] = func(in any) (any, error) {
		record("ListDashboards", in.(*cloudtrail.ListDashboardsInput).MaxResults)
		return &cloudtrail.ListDashboardsOutput{Dashboards: []cloudtrailtypes.DashboardDetail{
			{DashboardArn: ptrStr(ctTestDashboardARN), Type: cloudtrailtypes.DashboardTypeCustom},
			{DashboardArn: ptrStr(ctTestDashboard2ARN), Type: cloudtrailtypes.DashboardTypeCustom},
			{DashboardArn: ptrStr(ctTestManagedDashboardARN), Type: cloudtrailtypes.DashboardTypeManaged},
		}}, nil
	}
	h["GetDashboard"] = func(in any) (any, error) {
		arn := in.(*cloudtrail.GetDashboardInput).DashboardId
		return &cloudtrail.GetDashboardOutput{DashboardArn: arn, Status: cloudtrailtypes.DashboardStatusCreated}, nil
	}
}

// ctPolicyEveryTarget answers GetResourcePolicy with a policy for every
// target and records each ResourceArn sent in calls.
func ctPolicyEveryTarget(h map[string]func(in any) (any, error), calls *parentCalls) {
	h["GetResourcePolicy"] = func(in any) (any, error) {
		arn := in.(*cloudtrail.GetResourcePolicyInput).ResourceArn
		calls.add(sv(arn))
		return &cloudtrail.GetResourcePolicyOutput{ResourceArn: arn, ResourcePolicy: ptrStr(`{"Version":"2012-10-17"}`)}, nil
	}
}

// ctLakePolicyTargets are the resource-policy targets of a scan of
// cloudTrailHandlers with ctWithAllLakeItems: every channel, dashboard and
// event data store stored, the managed ones included, sorted.
var ctLakePolicyTargets = []string{
	ctTestChannelARN, ctTestChannel2ARN, ctTestManagedChannelARN,
	ctTestManagedDashboardARN, ctTestDashboardARN, ctTestDashboard2ARN,
	ctTestEDS1ARN, ctTestEDS2ARN, ctTestEDS3ARN,
}

// Every tagged type has its tags read with ListTags, sent the ARNs its
// listing returned, one call per phase here: the trails (the other account's
// organization trail included), the event data stores, and the channels and
// dashboards the account owns; a service-linked channel and a MANAGED
// dashboard are not sent, yet still have their resource policies read.
// Untagged resources store nil tags. A rescan inserts nothing new.
func TestScanCloudTrail_StoresTags(t *testing.T) {
	st := newTestStore(t)
	var insightCalls, evCfgCalls parentCalls
	h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
	pageCaps := map[string]int32{}
	ctWithAllLakeItems(h, pageCaps)
	tagged := map[string]map[string]string{
		ctTestTrailMainARN: {"env": "prod"},
		ctTestTrailOrgARN:  {"org": "yes"},
		ctTestEDS1ARN:      {"team": "lake"},
		ctTestChannelARN:   {"channel": "ch-1"},
		ctTestDashboardARN: {"board": "db-1"},
	}
	var calls ctTagCalls
	h["ListTags"] = ctListTags(tagged, nil, &calls)
	var policyCalls parentCalls
	ctPolicyEveryTarget(h, &policyCalls)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(h, ctDenied)
	total, inserted, err := scanCloudTrail(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanCloudTrail: %v", err)
	}
	// 4 trails + 3 event data stores + 3 channels + 3 dashboards + 9
	// resource policies + 2 insight selectors + 2 event configurations
	if total != 26 || inserted != 26 {
		t.Errorf("total, inserted = %d, %d; want 26, 26", total, inserted)
	}
	wantCalls := [][]string{
		{ctTestTrailMainARN, ctTestTrailAuditARN, ctTestTrailLegacyARN, ctTestTrailOrgARN},
		{ctTestEDS1ARN, ctTestEDS2ARN, ctTestEDS3ARN},
		{ctTestChannelARN, ctTestChannel2ARN},
		{ctTestDashboardARN, ctTestDashboard2ARN},
	}
	if got := calls.get(); !slices.EqualFunc(got, wantCalls, slices.Equal) {
		t.Errorf("ListTags calls = %v\nwant %v", got, wantCalls)
	}
	if want := map[string]int32{"ListEventDataStores": 1000, "ListChannels": 1000, "ListDashboards": 1000}; !maps.Equal(pageCaps, want) {
		t.Errorf("MaxResults sent = %v; want %v", pageCaps, want)
	}

	rows := map[string]store.Resource{}
	for _, typ := range []string{TypeCloudTrailTrail, TypeCloudTrailEventDataStore, TypeCloudTrailChannel, TypeCloudTrailDashboard} {
		maps.Copy(rows, rowsByNativeID(t, st, typ))
	}
	if len(rows) != 13 {
		t.Fatalf("tagged-type rows = %v; want 13", nativeIDs(rows))
	}
	if got := slices.Sorted(slices.Values(policyCalls.ids)); !slices.Equal(got, ctLakePolicyTargets) {
		t.Errorf("GetResourcePolicy ResourceArns = %v\nwant %v", got, ctLakePolicyTargets)
	}
	policies := rowsByNativeID(t, st, TypeCloudTrailResourcePolicy)
	for _, target := range []string{ctTestManagedChannelARN, ctTestManagedDashboardARN} {
		if _, ok := policies[target+"/policy"]; !ok {
			t.Errorf("resource policy of managed %s not stored; got %v", target, nativeIDs(policies))
		}
	}
	var trail struct {
		Trail struct{ Name, TrailARN string }
	}
	if err := json.Unmarshal([]byte(rows[ctTestTrailOrgARN].AttributesJSON), &trail); err != nil {
		t.Fatalf("attrs of %s: %v", ctTestTrailOrgARN, err)
	}
	if trail.Trail.Name != ctTestTrailOrg || trail.Trail.TrailARN != ctTestTrailOrgARN {
		t.Errorf("%s attrs Trail name, ARN = %q, %q; want %s, %s", ctTestTrailOrgARN, trail.Trail.Name, trail.Trail.TrailARN, ctTestTrailOrg, ctTestTrailOrgARN)
	}
	for arn, r := range rows {
		if want, ok := tagged[arn]; ok {
			assertTagsMap(t, r.TagsJSON, want)
		} else if r.TagsJSON != nil {
			t.Errorf("%s tags = %s; want nil", arn, *r.TagsJSON)
		}
	}

	type pin struct {
		typ, name, status string
		managed           bool
		attr, attrValue   string
	}
	for arn, want := range map[string]pin{
		ctTestTrailMainARN:        {TypeCloudTrailTrail, ctTestTrailMain, "", false, "", ""},
		ctTestEDS1ARN:             {TypeCloudTrailEventDataStore, "eds-one", "ENABLED", false, "EventDataStoreArn", ctTestEDS1ARN},
		ctTestChannelARN:          {TypeCloudTrailChannel, "ch-1", "", false, "Source", "Custom"},
		ctTestChannel2ARN:         {TypeCloudTrailChannel, "ch-2", "", false, "ChannelArn", ctTestChannel2ARN},
		ctTestManagedChannelARN:   {TypeCloudTrailChannel, "aws-service-channel/securitylake/svc-1", "", true, "Source", "Custom"},
		ctTestDashboardARN:        {TypeCloudTrailDashboard, "", "CREATED", false, "Status", "CREATED"},
		ctTestDashboard2ARN:       {TypeCloudTrailDashboard, "", "CREATED", false, "DashboardArn", ctTestDashboard2ARN},
		ctTestManagedDashboardARN: {TypeCloudTrailDashboard, "", "CREATED", false, "Status", "CREATED"},
	} {
		r := rows[arn]
		if r.Type != want.typ || sv(r.Name) != want.name || sv(r.Status) != want.status || r.ManagedByProvider != want.managed ||
			sv(r.Region) != testRegion || r.CreatedAt != nil {
			t.Errorf("%s type/name/status/managed/region/created = %s/%q/%q/%v/%q/%v; want %s/%q/%q/%v/%s/nil",
				arn, r.Type, sv(r.Name), sv(r.Status), r.ManagedByProvider, sv(r.Region), r.CreatedAt,
				want.typ, want.name, want.status, want.managed, testRegion)
		}
		if want.attr == "" {
			continue
		}
		var attrs map[string]any
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			t.Fatalf("attrs of %s: %v", arn, err)
		}
		if attrs[want.attr] != want.attrValue {
			t.Errorf("%s attrs[%s] = %v; want %s", arn, want.attr, attrs[want.attr], want.attrValue)
		}
	}

	total, inserted, err = scanCloudTrail(context.Background(), acct, testRegion, st, testScanID)
	if err != nil || total != 26 || inserted != 0 {
		t.Errorf("rescan = %d, %d, %v; want 26, 0, nil", total, inserted, err)
	}
}

// ListTags is sent at most 20 ARNs a call: 21 event data stores take two
// calls, each store's ARN sent once.
func TestScanCloudTrail_TagReadsTwentyARNsACall(t *testing.T) {
	st := newTestStore(t)
	var insightCalls, evCfgCalls parentCalls
	h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
	var stores []cloudtrailtypes.EventDataStore
	var want []string
	for i := range 21 {
		arn := fmt.Sprintf("arn:aws:cloudtrail:us-east-1:123456789012:eventdatastore/eds-%02d", i)
		stores = append(stores, cloudtrailtypes.EventDataStore{EventDataStoreArn: ptrStr(arn)})
		want = append(want, arn)
	}
	h["ListEventDataStores"] = func(any) (any, error) {
		return &cloudtrail.ListEventDataStoresOutput{EventDataStores: stores}, nil
	}
	var calls ctTagCalls
	h["ListTags"] = ctListTags(nil, nil, &calls)
	if _, err := scanCloudTrailTopLevel(t, st, h); err != nil {
		t.Fatalf("scanCloudTrail: %v", err)
	}
	// The trails' call comes first; the stores' two calls run concurrently.
	got := calls.get()
	if len(got) != 3 {
		t.Fatalf("ListTags calls = %v; want the trails' call and two store calls", got)
	}
	storeCalls := got[1:]
	slices.SortFunc(storeCalls, func(a, b []string) int { return len(b) - len(a) })
	if len(storeCalls[0]) != 20 || len(storeCalls[1]) != 1 {
		t.Errorf("store call sizes = %d, %d; want 20, 1", len(storeCalls[0]), len(storeCalls[1]))
	}
	sent := slices.Concat(storeCalls...)
	slices.Sort(sent)
	if !slices.Equal(sent, want) {
		t.Errorf("store ARNs sent = %v; want %v", sent, want)
	}
}

// Per ListTags failure: a denial keeps every row untagged with one warning
// for the scan; a not-found drops only the resource deleted since it was
// listed (an own trail, an event data store), which is then no parent; a
// not-found or denial for the other account's organization trail keeps it
// untagged and silent; a refusal of one store keeps it untagged and fails the
// scan; a transient error keeps the rows untagged and leaves the scan partial.
// A failing call of several ARNs is made again per ARN, so the others keep
// their tags.
func TestScanCloudTrail_TagReadFailures(t *testing.T) {
	throttled := apiErr("ThrottlingException", "Rate exceeded")
	allTrails, allStores := ctAllTrails, []string{ctTestEDS1ARN, ctTestEDS2ARN, ctTestEDS3ARN}
	every := slices.Concat(allTrails, allStores)
	failAll := func(err error) map[string]error {
		m := map[string]error{}
		for _, arn := range every {
			m[arn] = err
		}
		return m
	}
	for _, tc := range []struct {
		name                   string
		fail                   map[string]error
		wantErr                func(error) bool // nil: the scan returns nil
		wantWarns              int
		wantTrails, wantStores []string
		wantUntagged           []string // of the tagged resources, those stored untagged
		wantSplit              bool     // calls made again per ARN
	}{
		{"denied everywhere", failAll(ctDenied), nil, 1, allTrails, allStores, []string{ctTestTrailMainARN, ctTestTrailOrgARN, ctTestEDS1ARN, ctTestEDS3ARN}, true},
		{
			"store gone",
			map[string]error{ctTestEDS2ARN: apiErr("EventDataStoreNotFoundException", "gone")},
			nil, 0,
			allTrails,
			[]string{ctTestEDS1ARN, ctTestEDS3ARN},
			nil, true,
		},
		{
			"own trail gone",
			map[string]error{ctTestTrailMainARN: apiErr("ResourceNotFoundException", "gone")},
			nil, 0,
			[]string{ctTestTrailAuditARN, ctTestTrailLegacyARN, ctTestTrailOrgARN},
			allStores, nil, true,
		},
		{"other account's trail not found", map[string]error{ctTestTrailOrgARN: apiErr("ResourceNotFoundException", "not yours")}, nil, 0, allTrails, allStores, []string{ctTestTrailOrgARN}, true},
		{"other account's trail denied", map[string]error{ctTestTrailOrgARN: ctDenied}, nil, 0, allTrails, allStores, []string{ctTestTrailOrgARN}, true},
		{
			"store refused",
			map[string]error{ctTestEDS1ARN: apiErr("InactiveEventDataStoreException", "inactive")},
			func(err error) bool {
				return isAPIErrorCode(err, "InactiveEventDataStoreException") && !isTransientNetworkError(err)
			},
			0, allTrails, allStores,
			[]string{ctTestEDS1ARN},
			true,
		},
		{"transient", failAll(throttled), isTransientNetworkError, 0, allTrails, allStores, []string{ctTestTrailMainARN, ctTestTrailOrgARN, ctTestEDS1ARN, ctTestEDS3ARN}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var warns []store.ScanWarning
			var mu sync.Mutex
			st.OnWarn = func(w store.ScanWarning) {
				mu.Lock()
				defer mu.Unlock()
				if w.Service == ctListTagsOp {
					warns = append(warns, w)
				}
			}
			var insightCalls, evCfgCalls parentCalls
			h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
			tagged := map[string]map[string]string{
				ctTestTrailMainARN: {"k": "main"}, ctTestTrailOrgARN: {"k": "org"},
				ctTestEDS1ARN: {"k": "eds-1"}, ctTestEDS3ARN: {"k": "eds-3"},
			}
			var calls ctTagCalls
			h["ListTags"] = ctListTags(tagged, tc.fail, &calls)
			_, err := scanCloudTrailTopLevel(t, st, h)
			switch {
			case tc.wantErr == nil && err != nil:
				t.Fatalf("scanCloudTrail: %v", err)
			case tc.wantErr != nil && !tc.wantErr(err):
				t.Fatalf("err = %v; not the error wanted", err)
			}
			if len(warns) != tc.wantWarns {
				t.Fatalf("%s warnings = %v; want %d", ctListTagsOp, warns, tc.wantWarns)
			}
			if tc.wantWarns == 1 && warns[0].Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning scope = %q; want %s/%s", warns[0].Scope, testAccountID, testRegion)
			}
			trails := rowsByNativeID(t, st, TypeCloudTrailTrail)
			stores := rowsByNativeID(t, st, TypeCloudTrailEventDataStore)
			if got := nativeIDs(trails); !slices.Equal(got, tc.wantTrails) {
				t.Errorf("trail rows = %v; want %v", got, tc.wantTrails)
			}
			if got := nativeIDs(stores); !slices.Equal(got, tc.wantStores) {
				t.Errorf("event data store rows = %v; want %v", got, tc.wantStores)
			}
			rows := maps.Clone(trails)
			maps.Copy(rows, stores)
			for arn, want := range tagged {
				r, ok := rows[arn]
				switch {
				case !ok:
				case slices.Contains(tc.wantUntagged, arn):
					if r.TagsJSON != nil {
						t.Errorf("%s tags = %s; want nil", arn, *r.TagsJSON)
					}
				default:
					assertTagsMap(t, r.TagsJSON, want)
				}
			}
			split := false
			for _, call := range calls.get() {
				if len(call) == 1 {
					split = true
				}
			}
			if split != tc.wantSplit {
				t.Errorf("one-ARN calls made = %v; want %v (calls %v)", split, tc.wantSplit, calls.get())
			}
			// A dropped resource is no parent of the configuration phases:
			// trails are passed to GetInsightSelectors by ARN, stores to
			// GetEventConfiguration.
			for _, arn := range allTrails {
				if !slices.Contains(tc.wantTrails, arn) && slices.Contains(insightCalls.distinct(), "trail:"+arn) {
					t.Errorf("%s read as an Insights parent; it was not stored", arn)
				}
			}
			for _, arn := range allStores {
				if !slices.Contains(tc.wantStores, arn) && slices.Contains(evCfgCalls.distinct(), arn) {
					t.Errorf("%s read as an event configuration parent; it was not stored", arn)
				}
			}
		})
	}
}

// ctTestEDSOrgARN is an organization event data store the management
// account owns, seen from a delegated administrator account.
const ctTestEDSOrgARN = "arn:aws:cloudtrail:us-east-1:999999999999:eventdatastore/org"

// An event data store another account owns that ListTags does not find is
// kept untagged, without a warning: it is still read with GetEventDataStore
// and stored with that body, beside the account's own store.
func TestScanCloudTrail_OtherAccountEventDataStoreTagReadNotFound(t *testing.T) {
	for _, code := range []string{"ResourceNotFoundException", "EventDataStoreNotFoundException"} {
		t.Run(code, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, ctListTagsOp)
			var insightCalls, evCfgCalls, getCalls parentCalls
			h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
			h["ListEventDataStores"] = func(any) (any, error) {
				return &cloudtrail.ListEventDataStoresOutput{EventDataStores: []cloudtrailtypes.EventDataStore{
					{EventDataStoreArn: ptrStr(ctTestEDS1ARN)}, {EventDataStoreArn: ptrStr(ctTestEDSOrgARN)},
				}}, nil
			}
			get := h["GetEventDataStore"]
			h["GetEventDataStore"] = func(in any) (any, error) {
				arn := sv(in.(*cloudtrail.GetEventDataStoreInput).EventDataStore)
				getCalls.add(arn)
				if arn == ctTestEDSOrgARN {
					return &cloudtrail.GetEventDataStoreOutput{
						EventDataStoreArn: ptrStr(arn), Name: ptrStr("org-store"), Status: cloudtrailtypes.EventDataStoreStatusEnabled,
					}, nil
				}
				return get(in)
			}
			h["ListTags"] = ctListTags(
				map[string]map[string]string{ctTestEDS1ARN: {"k": "eds-1"}},
				map[string]error{ctTestEDSOrgARN: apiErr(code, "not yours")}, nil)
			if _, err := scanCloudTrailTopLevel(t, st, h); err != nil {
				t.Fatalf("scanCloudTrail: %v", err)
			}
			if *warns != 0 {
				t.Errorf("%s warnings = %d; want 0", ctListTagsOp, *warns)
			}
			if got, want := slices.Sorted(slices.Values(getCalls.ids)), []string{ctTestEDS1ARN, ctTestEDSOrgARN}; !slices.Equal(got, want) {
				t.Errorf("GetEventDataStore calls = %v; want %v", got, want)
			}
			stores := rowsByNativeID(t, st, TypeCloudTrailEventDataStore)
			if got, want := nativeIDs(stores), []string{ctTestEDS1ARN, ctTestEDSOrgARN}; !slices.Equal(got, want) {
				t.Fatalf("event data store rows = %v; want %v", got, want)
			}
			org := stores[ctTestEDSOrgARN]
			if org.TagsJSON != nil {
				t.Errorf("%s tags = %s; want nil", ctTestEDSOrgARN, *org.TagsJSON)
			}
			if sv(org.Name) != "org-store" || sv(org.Status) != string(cloudtrailtypes.EventDataStoreStatusEnabled) {
				t.Errorf("%s name, status = %q, %q; want org-store, %s", ctTestEDSOrgARN, sv(org.Name), sv(org.Status), cloudtrailtypes.EventDataStoreStatusEnabled)
			}
			assertTagsMap(t, stores[ctTestEDS1ARN].TagsJSON, map[string]string{"k": "eds-1"})
		})
	}
}

// A ListTags error still transient after the SDK's retries is demoted to a
// warning under ListTags beside a hard error, so the hard error is not read
// as transient.
func TestScanCloudTrail_TransientTagReadBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, ctListTagsOp)
	var insightCalls, evCfgCalls parentCalls
	h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
	h["ListTags"] = ctListTags(nil, map[string]error{ctTestTrailMainARN: apiErr("ThrottlingException", "Rate exceeded")}, nil)
	h["GetEventConfiguration"] = func(any) (any, error) { return nil, ctBadInput }
	_, err := scanCloudTrailTopLevel(t, st, h)
	assertAPICode(t, err, "InvalidParameterException")
	if isTransientNetworkError(err) {
		t.Errorf("err = %v reads as transient; the hard error must not be masked", err)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1 for the demoted transient", ctListTagsOp, *warns)
	}
}

// A failed trail write sends no tag denial warning, since no trail was
// stored, and stops the later phases.
func TestScanCloudTrail_TagDenialNotWarnedAfterFailedWrite(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeCloudTrailTrail)
	warns := countOpWarnings(st, ctListTagsOp)
	var insightCalls, evCfgCalls parentCalls
	h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
	h["ListTags"] = func(any) (any, error) { return nil, ctDenied }
	var storesListed atomic.Int32
	list := h["ListEventDataStores"]
	h["ListEventDataStores"] = func(in any) (any, error) {
		storesListed.Add(1)
		return list(in)
	}
	_, err := scanCloudTrailTopLevel(t, st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v; want the store write failure", err)
	}
	if *warns != 0 || storesListed.Load() != 0 {
		t.Errorf("%s warnings = %d, ListEventDataStores calls = %d; want 0, 0", ctListTagsOp, *warns, storesListed.Load())
	}
}

// In GovCloud and China, each listed ARN reaches ListTags unchanged and its
// row is tagged.
func TestScanCloudTrail_TagReadsInOtherPartitions(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			arn := func(resource string) string { return partitionARN(pf.partition, "cloudtrail", pf.region, resource) }
			trail, eds, channel, dashboard := arn("trail/t"), arn("eventdatastore/e"), arn("channel/c"), arn("dashboard/d")
			h := map[string]func(in any) (any, error){
				"DescribeTrails": func(any) (any, error) {
					return &cloudtrail.DescribeTrailsOutput{TrailList: []cloudtrailtypes.Trail{{Name: ptrStr("t"), TrailARN: ptrStr(trail)}}}, nil
				},
				"GetTrailStatus": func(any) (any, error) { return &cloudtrail.GetTrailStatusOutput{}, nil },
				"ListEventDataStores": func(any) (any, error) {
					return &cloudtrail.ListEventDataStoresOutput{EventDataStores: []cloudtrailtypes.EventDataStore{{EventDataStoreArn: ptrStr(eds)}}}, nil
				},
				"GetEventDataStore": func(any) (any, error) {
					return &cloudtrail.GetEventDataStoreOutput{EventDataStoreArn: ptrStr(eds)}, nil
				},
				"ListChannels": func(any) (any, error) {
					return &cloudtrail.ListChannelsOutput{Channels: []cloudtrailtypes.Channel{{ChannelArn: ptrStr(channel), Name: ptrStr("c")}}}, nil
				},
				"GetChannel": func(any) (any, error) { return &cloudtrail.GetChannelOutput{ChannelArn: ptrStr(channel)}, nil },
				"ListDashboards": func(any) (any, error) {
					return &cloudtrail.ListDashboardsOutput{Dashboards: []cloudtrailtypes.DashboardDetail{{DashboardArn: ptrStr(dashboard), Type: cloudtrailtypes.DashboardTypeCustom}}}, nil
				},
				"GetDashboard":          func(any) (any, error) { return &cloudtrail.GetDashboardOutput{DashboardArn: ptrStr(dashboard)}, nil },
				"GetResourcePolicy":     func(any) (any, error) { return nil, apiErr("ResourcePolicyNotFoundException", "none") },
				"GetInsightSelectors":   func(any) (any, error) { return nil, apiErr("InsightNotEnabledException", "off") },
				"GetEventConfiguration": func(any) (any, error) { return &cloudtrail.GetEventConfigurationOutput{}, nil },
			}
			tagged := map[string]map[string]string{trail: {"k": "t"}, eds: {"k": "e"}, channel: {"k": "c"}, dashboard: {"k": "d"}}
			var calls ctTagCalls
			h["ListTags"] = ctListTags(tagged, nil, &calls)
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(h, ctDenied)
			if _, _, err := scanCloudTrail(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scanCloudTrail: %v", err)
			}
			if got, want := calls.get(), [][]string{{trail}, {eds}, {channel}, {dashboard}}; !slices.EqualFunc(got, want, slices.Equal) {
				t.Errorf("ListTags calls = %v; want %v", got, want)
			}
			for typ, nativeID := range map[string]string{TypeCloudTrailTrail: trail, TypeCloudTrailEventDataStore: eds, TypeCloudTrailChannel: channel, TypeCloudTrailDashboard: dashboard} {
				r, ok := rowsByNativeID(t, st, typ)[nativeID]
				if !ok {
					t.Fatalf("%s row %s not stored", typ, nativeID)
				}
				assertTagsMap(t, r.TagsJSON, tagged[nativeID])
			}
		})
	}
}

// A ListEventDataStores page that fails or repeats its token after the first
// keeps the first page's store, read and tagged, and returns the error.
func TestScanCloudTrail_EventDataStoreLaterPageFailureKeepsEarlierPages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		later   func() (any, error)
		wantErr func(error) bool
	}{
		{"error", func() (any, error) { return nil, ctBadInput }, func(err error) bool { return isAPIErrorCode(err, "InvalidParameterException") }},
		{"repeated token", func() (any, error) {
			return &cloudtrail.ListEventDataStoresOutput{NextToken: ptrStr("p2")}, nil
		}, func(err error) bool { return errors.Is(err, errRepeatedNextToken) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var insightCalls, evCfgCalls parentCalls
			h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
			list := h["ListEventDataStores"]
			h["ListEventDataStores"] = func(in any) (any, error) {
				if in.(*cloudtrail.ListEventDataStoresInput).NextToken != nil {
					return tc.later()
				}
				return list(in)
			}
			h["ListTags"] = ctListTags(map[string]map[string]string{ctTestEDS1ARN: {"k": "v"}}, nil, nil)
			_, err := scanCloudTrailTopLevel(t, st, h)
			if !tc.wantErr(err) {
				t.Errorf("err = %v; want the second page's failure", err)
			}
			stores := rowsByNativeID(t, st, TypeCloudTrailEventDataStore)
			if got := nativeIDs(stores); !slices.Equal(got, []string{ctTestEDS1ARN}) {
				t.Fatalf("event data store rows = %v; want only %s", got, ctTestEDS1ARN)
			}
			if r := stores[ctTestEDS1ARN]; sv(r.Name) != "eds-one" {
				t.Errorf("eds-1 name = %q; want eds-one, from its GetEventDataStore body", sv(r.Name))
			}
			assertTagsMap(t, stores[ctTestEDS1ARN].TagsJSON, map[string]string{"k": "v"})
		})
	}
}

// When ctx ends during the listing, the stores listed are not read: each is
// stored as its list element, untagged, the later phases do not run, and the
// stop is returned once.
func TestScanCloudTrail_ContextEndKeepsListedStores(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var insightCalls, evCfgCalls parentCalls
	h := ignoreCtx(cloudTrailHandlers(&insightCalls, &evCfgCalls))
	list := h["ListEventDataStores"]
	h["ListEventDataStores"] = func(ctx context.Context, in any) (any, error) {
		if in.(*cloudtrail.ListEventDataStoresInput).NextToken != nil {
			cancel()
		}
		return list(ctx, in)
	}
	var reads, tagCalls atomic.Int32
	get := h["GetEventDataStore"]
	h["GetEventDataStore"] = func(ctx context.Context, in any) (any, error) {
		reads.Add(1)
		return get(ctx, in)
	}
	tags := h["ListTags"]
	h["ListTags"] = func(ctx context.Context, in any) (any, error) {
		if slices.Contains(in.(*cloudtrail.ListTagsInput).ResourceIdList, ctTestEDS1ARN) {
			tagCalls.Add(1)
		}
		return tags(ctx, in)
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfigCtx(h, ctDenied)
	_, _, err := scanCloudTrail(ctx, acct, testRegion, st, testScanID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	if n := len(joinedMembers(err)); n > 1 {
		t.Errorf("err = %v has %d members; want the stop once", err, n)
	}
	stores := rowsByNativeID(t, st, TypeCloudTrailEventDataStore)
	if got := nativeIDs(stores); !slices.Equal(got, []string{ctTestEDS1ARN, ctTestEDS2ARN, ctTestEDS3ARN}) {
		t.Fatalf("event data store rows = %v; want all three", got)
	}
	for arn, r := range stores {
		if r.Status != nil || r.TagsJSON != nil {
			t.Errorf("%s status, tags = %v, %v; want nil, nil (its list element, unread)", arn, r.Status, r.TagsJSON)
		}
	}
	if reads.Load() != 0 || tagCalls.Load() != 0 || len(evCfgCalls.distinct()) != 0 {
		t.Errorf("GetEventDataStore, store ListTags, GetEventConfiguration calls = %d, %d, %d; want 0, 0, 0",
			reads.Load(), tagCalls.Load(), len(evCfgCalls.distinct()))
	}
}

// A denied tag read of any one tagged type keeps its rows untagged and gives
// the one ListTags warning of the scan, sent once its rows are stored.
func TestScanCloudTrail_TagDenialWarnsOncePerScan(t *testing.T) {
	for _, tc := range []struct {
		name string
		arns []string
	}{
		{"trails", []string{ctTestTrailMainARN, ctTestTrailAuditARN}},
		{"event data stores", []string{ctTestEDS1ARN}},
		{"channels", []string{ctTestChannelARN}},
		{"dashboards", []string{ctTestDashboardARN}},
		{"every type", []string{ctTestTrailMainARN, ctTestEDS1ARN, ctTestChannelARN, ctTestDashboardARN}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, ctListTagsOp)
			var insightCalls, evCfgCalls parentCalls
			h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
			ctWithAllLakeItems(h, map[string]int32{})
			fail := map[string]error{}
			tagged := map[string]map[string]string{}
			for _, arn := range []string{ctTestTrailMainARN, ctTestEDS1ARN, ctTestChannelARN, ctTestDashboardARN} {
				tagged[arn] = map[string]string{"k": "v"}
			}
			for _, arn := range tc.arns {
				fail[arn] = ctDenied
			}
			h["ListTags"] = ctListTags(tagged, fail, nil)
			if _, err := scanCloudTrailTopLevel(t, st, h); err != nil {
				t.Fatalf("scanCloudTrail: %v", err)
			}
			if *warns != 1 {
				t.Errorf("%s warnings = %d; want 1", ctListTagsOp, *warns)
			}
			rows := map[string]store.Resource{}
			for _, typ := range []string{TypeCloudTrailTrail, TypeCloudTrailEventDataStore, TypeCloudTrailChannel, TypeCloudTrailDashboard} {
				maps.Copy(rows, rowsByNativeID(t, st, typ))
			}
			for arn, want := range tagged {
				r, ok := rows[arn]
				switch {
				case !ok:
					t.Errorf("%s not stored", arn)
				case fail[arn] != nil:
					if r.TagsJSON != nil {
						t.Errorf("%s tags = %s; want nil", arn, *r.TagsJSON)
					}
				default:
					assertTagsMap(t, r.TagsJSON, want)
				}
			}
		})
	}
}

// listCloudTrailTags reads every page of one call's answer, and a repeated
// token fails the read.
func TestListCloudTrailTags_Pages(t *testing.T) {
	arns := []string{ctTestTrailMainARN, ctTestEDS1ARN}
	page := func(arn string, next *string) *cloudtrail.ListTagsOutput {
		return &cloudtrail.ListTagsOutput{NextToken: next, ResourceTagList: []cloudtrailtypes.ResourceTag{{
			ResourceId: ptrStr(arn), TagsList: []cloudtrailtypes.Tag{{Key: ptrStr("k"), Value: ptrStr(arn)}},
		}}}
	}
	t.Run("every page", func(t *testing.T) {
		var tokens []string
		client := cloudtrail.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
			"ListTags": func(in any) (any, error) {
				i := in.(*cloudtrail.ListTagsInput)
				if !slices.Equal(i.ResourceIdList, arns) {
					t.Errorf("ResourceIdList = %v; want %v", i.ResourceIdList, arns)
				}
				tokens = append(tokens, sv(i.NextToken))
				if i.NextToken == nil {
					return page(arns[0], ptrStr("p2")), nil
				}
				return page(arns[1], nil), nil
			},
		}, ctDenied))
		got, err := listCloudTrailTags(context.Background(), client, arns)
		if err != nil {
			t.Fatalf("listCloudTrailTags: %v", err)
		}
		if !slices.Equal(tokens, []string{"", "p2"}) || len(got) != 2 {
			t.Fatalf("tokens sent = %q, resources = %d; want [\"\" p2], 2", tokens, len(got))
		}
		for _, arn := range arns {
			assertTagsMap(t, got[arn], map[string]string{"k": arn})
		}
	})
	t.Run("repeated token", func(t *testing.T) {
		client := cloudtrail.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
			"ListTags": func(any) (any, error) { return page(arns[0], ptrStr("p2")), nil },
		}, ctDenied))
		if _, err := listCloudTrailTags(context.Background(), client, arns); !errors.Is(err, errRepeatedNextToken) {
			t.Errorf("err = %v; want errRepeatedNextToken", err)
		}
	})
}

// A channel or dashboard ListTags no longer finds was deleted since it was
// listed: it is not stored and has no resource policy read, while the other
// items of its type keep their rows and policy reads.
func TestScanCloudTrail_LakeItemTagReadNotFound(t *testing.T) {
	for _, tc := range []struct {
		name, typ, gone string
		want            []string
	}{
		{"channel", TypeCloudTrailChannel, ctTestChannelARN, []string{ctTestChannel2ARN, ctTestManagedChannelARN}},
		{"dashboard", TypeCloudTrailDashboard, ctTestDashboardARN, []string{ctTestManagedDashboardARN, ctTestDashboard2ARN}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, ctListTagsOp)
			var insightCalls, evCfgCalls, policyCalls parentCalls
			h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
			ctWithAllLakeItems(h, map[string]int32{})
			ctPolicyEveryTarget(h, &policyCalls)
			h["ListTags"] = ctListTags(nil, map[string]error{tc.gone: apiErr("ResourceNotFoundException", "gone")}, nil)
			if _, err := scanCloudTrailTopLevel(t, st, h); err != nil {
				t.Fatalf("scanCloudTrail: %v", err)
			}
			if *warns != 0 {
				t.Errorf("%s warnings = %d; want 0", ctListTagsOp, *warns)
			}
			if got := nativeIDs(rowsByNativeID(t, st, tc.typ)); !slices.Equal(got, tc.want) {
				t.Errorf("%s rows = %v; want %v", tc.typ, got, tc.want)
			}
			wantTargets := slices.DeleteFunc(slices.Clone(ctLakePolicyTargets), func(arn string) bool { return arn == tc.gone })
			if got := slices.Sorted(slices.Values(policyCalls.ids)); !slices.Equal(got, wantTargets) {
				t.Errorf("GetResourcePolicy ResourceArns = %v\nwant %v", got, wantTargets)
			}
		})
	}
}

// A ListEventDataStores page denied after the first keeps the first page's
// store and warns once, under the listing, once it is stored; the scan
// succeeds.
func TestScanCloudTrail_EventDataStoreLaterPageDenialWarns(t *testing.T) {
	st := newTestStore(t)
	var (
		mu    sync.Mutex
		warns []store.ScanWarning
	)
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		defer mu.Unlock()
		if w.Service == "cloudtrail:ListEventDataStores" {
			warns = append(warns, w)
		}
	}
	var insightCalls, evCfgCalls parentCalls
	h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
	list := h["ListEventDataStores"]
	h["ListEventDataStores"] = func(in any) (any, error) {
		if in.(*cloudtrail.ListEventDataStoresInput).NextToken != nil {
			return nil, ctDenied
		}
		return list(in)
	}
	if _, err := scanCloudTrailTopLevel(t, st, h); err != nil {
		t.Fatalf("scanCloudTrail: %v", err)
	}
	if len(warns) != 1 || warns[0].Scope != testAccountID+"/"+testRegion {
		t.Fatalf("cloudtrail:ListEventDataStores warnings = %v; want 1 scoped %s/%s", warns, testAccountID, testRegion)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeCloudTrailEventDataStore)); !slices.Equal(got, []string{ctTestEDS1ARN}) {
		t.Errorf("event data store rows = %v; want only %s", got, ctTestEDS1ARN)
	}
}

// A failed event-data-store write sends none of the phase's warnings (a
// later page's listing denial, a denied GetEventDataStore, a denied tag
// read), since no store was stored, and stops the later phases.
func TestScanCloudTrail_EventDataStoreWriteFailureStopsLaterPhases(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeCloudTrailEventDataStore)
	var mu sync.Mutex
	warns := map[string]int{}
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		defer mu.Unlock()
		warns[w.Service]++
	}
	var insightCalls, evCfgCalls parentCalls
	h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
	list, get := h["ListEventDataStores"], h["GetEventDataStore"]
	h["ListEventDataStores"] = func(in any) (any, error) {
		if in.(*cloudtrail.ListEventDataStoresInput).NextToken != nil {
			return nil, ctDenied
		}
		return list(in)
	}
	h["GetEventDataStore"] = func(in any) (any, error) {
		if sv(in.(*cloudtrail.GetEventDataStoreInput).EventDataStore) == ctTestEDS1ARN {
			return nil, ctDenied
		}
		return get(in)
	}
	h["ListTags"] = ctListTags(nil, map[string]error{ctTestEDS1ARN: ctDenied}, nil)
	var channelCalls atomic.Int32
	channels := h["ListChannels"]
	h["ListChannels"] = func(in any) (any, error) {
		channelCalls.Add(1)
		return channels(in)
	}
	_, err := scanCloudTrailTopLevel(t, st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v; want the store write failure", err)
	}
	if channelCalls.Load() != 0 {
		t.Errorf("ListChannels calls = %d; want 0 after the failed write", channelCalls.Load())
	}
	for _, op := range []string{"cloudtrail:ListEventDataStores", "cloudtrail:GetEventDataStore", ctListTagsOp} {
		if warns[op] != 0 {
			t.Errorf("%s warnings = %d; want 0: no store was stored", op, warns[op])
		}
	}
}

// A resource a successful ListTags response leaves out is stored untagged
// and its read fails the scan (errTagsNotDescribed): the op's docs do not say
// that an untagged resource is left out, so it is not read as untagged.
func TestScanCloudTrail_TagResponseMissingARN(t *testing.T) {
	st := newTestStore(t)
	var insightCalls, evCfgCalls parentCalls
	h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
	tagged := map[string]map[string]string{ctTestEDS1ARN: {"k": "eds-1"}, ctTestEDS2ARN: {"k": "eds-2"}}
	answer := ctListTags(tagged, nil, nil)
	h["ListTags"] = func(in any) (any, error) {
		out, err := answer(in)
		if err != nil {
			return nil, err
		}
		list := out.(*cloudtrail.ListTagsOutput)
		list.ResourceTagList = slices.DeleteFunc(list.ResourceTagList, func(rt cloudtrailtypes.ResourceTag) bool { return sv(rt.ResourceId) == ctTestEDS2ARN })
		return list, nil
	}
	_, err := scanCloudTrailTopLevel(t, st, h)
	if !errors.Is(err, errTagsNotDescribed) || isTransientNetworkError(err) {
		t.Fatalf("err = %v; want errTagsNotDescribed, not transient", err)
	}
	stores := rowsByNativeID(t, st, TypeCloudTrailEventDataStore)
	if got := nativeIDs(stores); !slices.Equal(got, []string{ctTestEDS1ARN, ctTestEDS2ARN, ctTestEDS3ARN}) {
		t.Fatalf("event data store rows = %v; want all three", got)
	}
	if r := stores[ctTestEDS2ARN]; r.TagsJSON != nil {
		t.Errorf("%s tags = %s; want nil", ctTestEDS2ARN, *r.TagsJSON)
	}
	assertTagsMap(t, stores[ctTestEDS1ARN].TagsJSON, tagged[ctTestEDS1ARN])
}

// A resource is stored only if both its detail read and its ListTags read
// leave it, so a failure of either read is reported only for the resources
// stored. A failed detail read (GetTrailStatus, GetEventDataStore,
// GetChannel, GetDashboard) of a resource ListTags then finds gone gives no
// warning or error for it, and a store ListTags finds gone has no
// GetEventDataStore read. A denied ListTags read of a resource the detail
// read finds gone gives no ListTags warning. Either way the resource is not
// stored, and the other resources of its type are.
func TestScanCloudTrail_ReadFailureOfResourceNotStored(t *testing.T) {
	transient := &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset")}
	for _, typ := range []struct {
		name, detailOp, typ, arn, detailGone, tagGone string
		detailIn                                      func(in any) string
		wantRows                                      []string
	}{
		{
			"trail", "GetTrailStatus", TypeCloudTrailTrail, ctTestTrailMainARN, "TrailNotFoundException", "ResourceNotFoundException",
			func(in any) string { return sv(in.(*cloudtrail.GetTrailStatusInput).Name) },
			[]string{ctTestTrailAuditARN, ctTestTrailLegacyARN, ctTestTrailOrgARN},
		},
		{
			"event data store", "GetEventDataStore", TypeCloudTrailEventDataStore, ctTestEDS1ARN, "EventDataStoreNotFoundException", "EventDataStoreNotFoundException",
			func(in any) string { return sv(in.(*cloudtrail.GetEventDataStoreInput).EventDataStore) },
			[]string{ctTestEDS2ARN, ctTestEDS3ARN},
		},
		{
			"channel", "GetChannel", TypeCloudTrailChannel, ctTestChannelARN, "ChannelNotFoundException", "ResourceNotFoundException",
			func(in any) string { return sv(in.(*cloudtrail.GetChannelInput).Channel) },
			[]string{ctTestChannel2ARN, ctTestManagedChannelARN},
		},
		{
			"dashboard", "GetDashboard", TypeCloudTrailDashboard, ctTestDashboardARN, "ResourceNotFoundException", "ResourceNotFoundException",
			func(in any) string { return sv(in.(*cloudtrail.GetDashboardInput).DashboardId) },
			[]string{ctTestManagedDashboardARN, ctTestDashboard2ARN},
		},
	} {
		for _, tc := range []struct {
			name             string
			detailErr        error
			tagErr           error
			wantDetailCalled bool // the detail read is made for typ.arn
		}{
			{"detail denied, tags gone", ctDenied, apiErr(typ.tagGone, "gone"), typ.typ != TypeCloudTrailEventDataStore},
			{"detail transient, tags gone", transient, apiErr(typ.tagGone, "gone"), typ.typ != TypeCloudTrailEventDataStore},
			{"detail other error, tags gone", ctBadInput, apiErr(typ.tagGone, "gone"), typ.typ != TypeCloudTrailEventDataStore},
			{"detail gone, tags denied", apiErr(typ.detailGone, "gone"), ctDenied, true},
		} {
			t.Run(typ.name+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				detailWarns := countOpWarnings(st, "cloudtrail:"+typ.detailOp)
				tagWarns := countOpWarnings(st, ctListTagsOp)
				var insightCalls, evCfgCalls parentCalls
				h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
				ctWithAllLakeItems(h, map[string]int32{})
				var detailCalled atomic.Bool
				detail := h[typ.detailOp]
				h[typ.detailOp] = func(in any) (any, error) {
					if typ.detailIn(in) == typ.arn {
						detailCalled.Store(true)
						return nil, tc.detailErr
					}
					return detail(in)
				}
				h["ListTags"] = ctListTags(nil, map[string]error{typ.arn: tc.tagErr}, nil)
				if _, err := scanCloudTrailTopLevel(t, st, h); err != nil {
					t.Fatalf("scanCloudTrail: %v", err)
				}
				if *detailWarns != 0 || *tagWarns != 0 {
					t.Errorf("cloudtrail:%s, %s warnings = %d, %d; want 0, 0", typ.detailOp, ctListTagsOp, *detailWarns, *tagWarns)
				}
				if detailCalled.Load() != tc.wantDetailCalled {
					t.Errorf("%s read for %s = %v, want %v", typ.detailOp, typ.arn, detailCalled.Load(), tc.wantDetailCalled)
				}
				if got := nativeIDs(rowsByNativeID(t, st, typ.typ)); !slices.Equal(got, typ.wantRows) {
					t.Errorf("%s rows = %v; want %v", typ.typ, got, typ.wantRows)
				}
			})
		}
	}
}
