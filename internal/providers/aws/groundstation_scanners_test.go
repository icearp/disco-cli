package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/groundstation"
	gstypes "github.com/aws/aws-sdk-go-v2/service/groundstation/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

var gsTestCreated = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func gsEphemerisARN(id string) string {
	return fmt.Sprintf("arn:aws:groundstation:%s:%s:ephemeris/%s", testRegion, testAccountID, id)
}

func gsEphemeris(id string) gstypes.EphemerisItem {
	return gstypes.EphemerisItem{
		EphemerisId: sdkaws.String(id), Name: sdkaws.String("eph-" + id), Status: gstypes.EphemerisStatusEnabled,
		EphemerisType: gstypes.EphemerisTypeOem, CreationTime: &gsTestCreated,
	}
}

// gsRowsByNativeID includes provider-managed rows, which the default reader hides.
func gsRowsByNativeID(t *testing.T, st *store.Store, rtype string) map[string]store.Resource {
	t.Helper()
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, Types: []string{rtype}, Limit: util.AllResources, IncludeManaged: true,
	})
	if err != nil {
		t.Fatalf("ListResources(%s): %v", rtype, err)
	}
	m := make(map[string]store.Resource, len(rows))
	for _, r := range rows {
		m[r.NativeID] = r
	}
	return m
}

// gsPage is one ListSatellites or ListEphemerides response; err replaces it.
type gsPage struct {
	satellites  []gstypes.SatelliteListItem
	ephemerides []gstypes.EphemerisItem
	err         error
}

// gsStub serves pages in order (NextToken is the next page's index) and
// records every ListEphemerides input.
type gsStub struct {
	groundStationAPI
	pages     []gsPage
	ephInputs []groundstation.ListEphemeridesInput
}

func (s *gsStub) page(token *string) (gsPage, *string, error) {
	i := 0
	if token != nil {
		var err error
		if i, err = strconv.Atoi(*token); err != nil {
			return gsPage{}, nil, err
		}
	}
	var next *string
	if i+1 < len(s.pages) {
		next = sdkaws.String(strconv.Itoa(i + 1))
	}
	return s.pages[i], next, nil
}

func (s *gsStub) ListEphemerides(_ context.Context, in *groundstation.ListEphemeridesInput, _ ...func(*groundstation.Options)) (*groundstation.ListEphemeridesOutput, error) {
	s.ephInputs = append(s.ephInputs, *in)
	p, next, err := s.page(in.NextToken)
	if err != nil {
		return nil, err
	}
	if p.err != nil {
		return nil, p.err
	}
	return &groundstation.ListEphemeridesOutput{Ephemerides: p.ephemerides, NextToken: next}, nil
}

// gsPhases runs each new phase against a stub; satellites take no region.
var gsPhases = []struct {
	name  string
	scan  func(*gsStub, *store.Store) (int, int, error)
	rtype string
}{
	{"satellites", func(c *gsStub, st *store.Store) (int, int, error) {
		return scanGSSatellites(context.Background(), c, newTestAccount(testAccountID), st, testScanID)
	}, TypeGroundStationSatellite},
	{"ephemerides", func(c *gsStub, st *store.Store) (int, int, error) {
		return scanGSEphemerides(context.Background(), c, newTestAccount(testAccountID), testRegion, st, testScanID)
	}, TypeGroundStationEphemeris},
}

func TestScanGSEphemerides_PaginatesAndSkipsIDless(t *testing.T) {
	st := newTestStore(t)
	unnamed := gsEphemeris("c")
	unnamed.Name = nil
	unnamed.Status = ""
	stub := &gsStub{pages: []gsPage{
		{ephemerides: []gstypes.EphemerisItem{gsEphemeris("a"), {Name: sdkaws.String("no-id")}}},
		{ephemerides: []gstypes.EphemerisItem{gsEphemeris("b"), unnamed}},
	}}

	total, _, err := scanGSEphemerides(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 3 {
		t.Fatalf("scanGSEphemerides = (%d, %v); want (3, nil)", total, err)
	}
	rows := gsRowsByNativeID(t, st, TypeGroundStationEphemeris)
	if len(rows) != 3 {
		t.Errorf("stored %d ephemerides; want 3 (id-less skipped)", len(rows))
	}
	r, ok := rows[gsEphemerisARN("b")]
	if !ok {
		t.Fatalf("ephemeris b not stored under %s", gsEphemerisARN("b"))
	}
	if sv(r.Region) != testRegion || sv(r.Name) != "eph-b" || sv(r.Status) != "ENABLED" || r.CreatedAt == nil || r.ManagedByProvider {
		t.Errorf("row = region %q name %q status %q createdAt %v managed %v; want %q eph-b ENABLED non-nil false",
			sv(r.Region), sv(r.Name), sv(r.Status), r.CreatedAt, r.ManagedByProvider, testRegion)
	}
	var attrs struct{ EphemerisType string }
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.EphemerisType != "OEM" {
		t.Errorf("attrs EphemerisType = %q (err %v); want OEM", attrs.EphemerisType, err)
	}
	c := rows[gsEphemerisARN("c")]
	if sv(c.Name) != "c" || c.Status != nil {
		t.Errorf("unnamed row = name %q status %v; want id fallback c and nil status", sv(c.Name), c.Status)
	}
}

// ListEphemerides matches on expiration time inside the window, so the window
// must reach back past any real expiration and far ahead of any upload.
func TestScanGSEphemerides_SendsWideWindowAndEveryStatus(t *testing.T) {
	st := newTestStore(t)
	stub := &gsStub{pages: []gsPage{{}, {}}}
	if _, _, err := scanGSEphemerides(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scanGSEphemerides: %v", err)
	}
	if len(stub.ephInputs) != 2 {
		t.Fatalf("ListEphemerides called %d times; want 2 (one per page)", len(stub.ephInputs))
	}
	earliest := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	latest := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, in := range stub.ephInputs {
		if in.StartTime == nil || in.StartTime.After(earliest) {
			t.Errorf("call %d StartTime = %v; want <= %v", i, in.StartTime, earliest)
		}
		if in.EndTime == nil || !in.EndTime.After(latest) {
			t.Errorf("call %d EndTime = %v; want after %v", i, in.EndTime, latest)
		}
		if in.SatelliteId != nil {
			t.Errorf("call %d SatelliteId = %q; want unset (satellite-less ephemerides would be missed)", i, *in.SatelliteId)
		}
		for _, want := range []gstypes.EphemerisStatus{
			gstypes.EphemerisStatusValidating, gstypes.EphemerisStatusInvalid, gstypes.EphemerisStatusError,
			gstypes.EphemerisStatusEnabled, gstypes.EphemerisStatusDisabled, gstypes.EphemerisStatusExpired,
		} {
			if !slices.Contains(in.StatusList, want) {
				t.Errorf("call %d StatusList = %v; missing %s", i, in.StatusList, want)
			}
		}
	}
}

func TestScanGSEphemerides_ServiceManagedIsProviderManaged(t *testing.T) {
	st := newTestStore(t)
	managed := gsEphemeris("m")
	managed.EphemerisType = gstypes.EphemerisTypeServiceManaged
	stub := &gsStub{pages: []gsPage{{ephemerides: []gstypes.EphemerisItem{managed, gsEphemeris("u")}}}}
	if _, _, err := scanGSEphemerides(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scanGSEphemerides: %v", err)
	}
	rows := gsRowsByNativeID(t, st, TypeGroundStationEphemeris)
	if r, ok := rows[gsEphemerisARN("m")]; !ok || !r.ManagedByProvider {
		t.Errorf("SERVICE_MANAGED ephemeris stored=%v managed=%v; want stored and managed", ok, r.ManagedByProvider)
	}
	if r, ok := rows[gsEphemerisARN("u")]; !ok || r.ManagedByProvider {
		t.Errorf("OEM ephemeris stored=%v managed=%v; want stored and not managed", ok, r.ManagedByProvider)
	}
}

func TestScanGSPhases_Empty(t *testing.T) {
	for _, p := range gsPhases {
		t.Run(p.name, func(t *testing.T) {
			st := newTestStore(t)
			total, _, err := p.scan(&gsStub{pages: []gsPage{{}}}, st)
			if err != nil || total != 0 {
				t.Fatalf("scan = (%d, %v); want (0, nil)", total, err)
			}
			if rows := gsRowsByNativeID(t, st, p.rtype); len(rows) != 0 {
				t.Errorf("stored %d rows; want 0", len(rows))
			}
		})
	}
}

func TestScanGSPhases_AccessDeniedKeepsEarlierPages(t *testing.T) {
	for _, p := range gsPhases {
		t.Run(p.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := 0
			st.OnWarn = func(store.ScanWarning) { warnings++ }
			stub := &gsStub{pages: []gsPage{
				{satellites: []gstypes.SatelliteListItem{gsSatellite("a")}, ephemerides: []gstypes.EphemerisItem{gsEphemeris("a")}},
				{err: apiErr("AccessDeniedException", "User: x is not authorized to perform: groundstation:List")},
			}}
			total, _, err := p.scan(stub, st)
			if err != nil || total != 1 {
				t.Fatalf("scan = (%d, %v); want (1, nil)", total, err)
			}
			if warnings != 1 {
				t.Errorf("warnings = %d; want 1", warnings)
			}
			if rows := gsRowsByNativeID(t, st, p.rtype); len(rows) != 1 {
				t.Errorf("stored %d rows; want the 1 from the page before the denial", len(rows))
			}
		})
	}
}

func TestScanGSPhases_OtherErrorPropagates(t *testing.T) {
	for _, p := range gsPhases {
		t.Run(p.name, func(t *testing.T) {
			st := newTestStore(t)
			_, _, err := p.scan(&gsStub{pages: []gsPage{{err: apiErr("InvalidParameterException", "bad")}}}, st)
			if !isAPIErrorCode(err, "InvalidParameterException") {
				t.Fatalf("err = %v; want InvalidParameterException", err)
			}
		})
	}
}

// gsScanResponses queues one page per op for a full scanGroundStation run with
// one mission profile and the given ListEphemerides response. No ListSatellites
// queue is registered, so a regional lane that lists satellites fails the test.
func gsScanResponses(ephemerides stubCall) map[string][]stubCall {
	return map[string][]stubCall{
		"ListConfigs":                {{Output: &groundstation.ListConfigsOutput{}}},
		"ListDataflowEndpointGroups": {{Output: &groundstation.ListDataflowEndpointGroupsOutput{}}},
		"ListMissionProfiles": {{Output: &groundstation.ListMissionProfilesOutput{MissionProfileList: []gstypes.MissionProfileListItem{{
			MissionProfileArn: sdkaws.String(fmt.Sprintf("arn:aws:groundstation:%s:%s:mission-profile/mp", testRegion, testAccountID)),
		}}}}},
		"ListEphemerides": {ephemerides},
	}
}

// Runs through the real client, so the SDK validator also confirms the
// required StartTime/EndTime window is sent.
func TestScanGroundStation_StoresEphemerides(t *testing.T) {
	st := newTestStore(t)
	stub := stubResponses(t, gsScanResponses(
		stubCall{Output: &groundstation.ListEphemeridesOutput{Ephemerides: []gstypes.EphemerisItem{gsEphemeris("e")}}},
	))
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(stub, testRegion)}

	if _, _, err := scanGroundStation(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanGroundStation: %v", err)
	}
	if _, ok := gsRowsByNativeID(t, st, TypeGroundStationEphemeris)[gsEphemerisARN("e")]; !ok {
		t.Error("ephemeris not stored by scanGroundStation")
	}
}

// A new phase's failure must not cost the rows of the phases that existed
// before it, so ephemerides run after every pre-existing phase.
func TestScanGroundStation_EphemeridesErrorAfterExistingPhases(t *testing.T) {
	st := newTestStore(t)
	stub := stubResponses(t, gsScanResponses(stubCall{Err: apiErr("InvalidParameterException", "bad")}))
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(stub, testRegion)}

	_, _, err := scanGroundStation(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "InvalidParameterException") {
		t.Fatalf("scanGroundStation err = %v; want InvalidParameterException", err)
	}
	if rows := gsRowsByNativeID(t, st, TypeGroundStationMissionProfile); len(rows) != 1 {
		t.Errorf("stored %d mission profiles; want 1 (pre-existing phase runs before ephemerides)", len(rows))
	}
}
