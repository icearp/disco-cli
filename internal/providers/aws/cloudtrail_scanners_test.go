package aws

import (
	"encoding/json"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	cloudtrailtypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/icearp/disco-cli/store"
)

var ctAllTrails = []string{ctTestTrailAuditARN, ctTestTrailLegacyARN, ctTestTrailMainARN, ctTestTrailOrgARN}

// ctFailTrail answers op for the trails in fail with their error and for the
// others as base does.
func ctFailTrail(h map[string]func(in any) (any, error), op string, fail map[string]error) {
	base := h[op]
	h[op] = func(in any) (any, error) {
		var arn string
		switch i := in.(type) {
		case *cloudtrail.GetTrailStatusInput:
			arn = sv(i.Name)
		case *cloudtrail.ListTagsInput:
			arn = i.ResourceIdList[0]
		}
		if err := fail[arn]; err != nil {
			return nil, err
		}
		return base(in)
	}
}

// Each per-trail read keeps the trail on a denial or a transient error, with
// one warning per op and cause; drops only an own trail that vanished (the
// op's modeled not-found), while another account's trail answering not-found
// is kept without that part and warned about once; and fails on anything
// else after the other trails are stored and the event-data-store phase ran.
func TestScanCloudTrail_PerTrailReadErrors(t *testing.T) {
	throttled := apiErr("ThrottlingException", "Rate exceeded")
	cases := []struct {
		name, op   string
		fail       map[string]error
		wantTrails []string
		wantWarns  int
		wantCode   string
	}{
		{"status denied", "GetTrailStatus", map[string]error{ctTestTrailMainARN: ctDenied, ctTestTrailAuditARN: ctDenied}, ctAllTrails, 1, ""},
		{"status transient", "GetTrailStatus", map[string]error{ctTestTrailMainARN: throttled, ctTestTrailAuditARN: throttled}, ctAllTrails, 1, ""},
		{
			"status trail gone", "GetTrailStatus",
			map[string]error{ctTestTrailMainARN: apiErr("TrailNotFoundException", "gone")},
			[]string{ctTestTrailAuditARN, ctTestTrailLegacyARN, ctTestTrailOrgARN},
			0, "",
		},
		{
			"status other error", "GetTrailStatus",
			map[string]error{ctTestTrailAuditARN: apiErr("CloudTrailARNInvalidException", "bad arn")},
			[]string{ctTestTrailLegacyARN, ctTestTrailMainARN, ctTestTrailOrgARN},
			0, "CloudTrailARNInvalidException",
		},
		{"tags denied", "ListTags", map[string]error{ctTestTrailMainARN: ctDenied, ctTestTrailAuditARN: ctDenied}, ctAllTrails, 1, ""},
		{"tags transient", "ListTags", map[string]error{ctTestTrailMainARN: throttled, ctTestTrailAuditARN: throttled}, ctAllTrails, 1, ""},
		{
			"tags trail gone", "ListTags",
			map[string]error{ctTestTrailMainARN: apiErr("ResourceNotFoundException", "gone")},
			[]string{ctTestTrailAuditARN, ctTestTrailLegacyARN, ctTestTrailOrgARN},
			0, "",
		},
		{
			"tags other error", "ListTags",
			map[string]error{ctTestTrailAuditARN: apiErr("CloudTrailARNInvalidException", "bad arn")},
			[]string{ctTestTrailLegacyARN, ctTestTrailMainARN, ctTestTrailOrgARN},
			0, "CloudTrailARNInvalidException",
		},
		// The organization trail is owned by another account: not-found keeps it.
		{"status org trail not found", "GetTrailStatus", map[string]error{ctTestTrailOrgARN: apiErr("TrailNotFoundException", "not here")}, ctAllTrails, 1, ""},
		{"tags org trail not found", "ListTags", map[string]error{ctTestTrailOrgARN: apiErr("ResourceNotFoundException", "not here")}, ctAllTrails, 1, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "cloudtrail:"+tc.op)
			var insightCalls, evCfgCalls parentCalls
			h := cloudTrailHandlers(&insightCalls, &evCfgCalls)
			ctFailTrail(h, tc.op, tc.fail)
			_, err := scanCloudTrailTopLevel(t, st, h)
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
			} else if err != nil {
				t.Fatalf("scanCloudTrail: %v", err)
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
				lostStatus := tc.op == "GetTrailStatus" && tc.fail[arn] != nil
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

// A denied or transient GetEventDataStore keeps the store as its
// ListEventDataStores element, still a parent of the configuration phases,
// warning once per cause; a store it no longer finds is not stored and is no
// parent; any other error fails the scan after the other stores are stored and
// the extended phases have run.
func TestScanCloudTrail_GetEventDataStoreErrors(t *testing.T) {
	all := []string{ctTestEDS1ARN, ctTestEDS2ARN, ctTestEDS3ARN}
	cases := []struct {
		name       string
		err        error
		wantStores []string
		wantKept   bool // eds-1 is stored as its list element
		wantWarns  int
		wantCode   string
	}{
		{"denied", ctDenied, all, true, 1, ""},
		{"transient", apiErr("ThrottlingException", "Rate exceeded"), all, true, 1, ""},
		{"gone", apiErr("EventDataStoreNotFoundException", "gone"), []string{ctTestEDS2ARN, ctTestEDS3ARN}, false, 0, ""},
		{"other error", ctBadInput, []string{ctTestEDS2ARN, ctTestEDS3ARN}, false, 0, "InvalidParameterException"},
	}
	for _, tc := range cases {
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
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
			} else if err != nil {
				t.Fatalf("scanCloudTrail: %v", err)
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
