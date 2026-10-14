package aws

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/location"
	loctypes "github.com/aws/aws-sdk-go-v2/service/location/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	locDenied   = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: geo:List"}
	locBadInput = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	locGone     = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "collection not found"}
	locCreated  = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	locGeobuf   = []byte{0x0a, 0x01, 0xff}
)

func locCollectionARN(name string) string {
	return fmt.Sprintf("arn:aws:geo:%s:%s:geofence-collection/%s", testRegion, testAccountID, name)
}

func locGeofenceNativeID(collection, id string) string {
	return locCollectionARN(collection) + "/geofence/" + id
}

func locGeofence(id, status string) loctypes.ListGeofenceResponseEntry {
	return loctypes.ListGeofenceResponseEntry{
		GeofenceId: ptrStr(id), Status: ptrStr(status), CreateTime: &locCreated, UpdateTime: &locCreated,
		Geometry: &loctypes.GeofenceGeometry{Polygon: [][][]float64{{{1, 2}, {3, 4}, {5, 6}, {1, 2}}}},
	}
}

// locGeofenceHandler serves, for collection "c1", page one (g1, a Geobuf
// geometry) then page two (g2 DELETING, gd DELETED and an element with no id);
// for any other collection one page holding its own g1. A collection in fail
// returns that error on every page; fail["c1/p2"] fails only c1's page two. Every call's collection and page size are recorded.
func locGeofenceHandler(calls *parentCalls, sizes *parentCalls, fail map[string]error) func(in any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*location.ListGeofencesInput)
		name := sv(req.CollectionName)
		calls.add(name)
		sizes.add(fmt.Sprint(sdkaws.ToInt32(req.MaxResults)))
		if err := fail[name]; err != nil {
			return nil, err
		}
		if name != "c1" {
			return &location.ListGeofencesOutput{Entries: []loctypes.ListGeofenceResponseEntry{locGeofence("g1", "ACTIVE")}}, nil
		}
		if req.NextToken == nil {
			g1 := locGeofence("g1", "ACTIVE")
			g1.Geometry = &loctypes.GeofenceGeometry{Geobuf: locGeobuf}
			return &location.ListGeofencesOutput{Entries: []loctypes.ListGeofenceResponseEntry{g1}, NextToken: ptrStr("p2")}, nil
		}
		if err := fail["c1/p2"]; err != nil {
			return nil, err
		}
		return &location.ListGeofencesOutput{Entries: []loctypes.ListGeofenceResponseEntry{
			locGeofence("g2", "DELETING"), locGeofence("gd", "DELETED"), {Status: ptrStr("ACTIVE")},
		}}, nil
	}
}

func locClient(handlers map[string]func(in any) (any, error)) *location.Client {
	return location.NewFromConfig(opStubConfig(handlers, locDenied))
}

// scanLocGeofences seeds the collection rows and fans out over them.
func scanLocGeofences(t *testing.T, st *store.Store, handler func(in any) (any, error), collections ...string) (int, error) {
	t.Helper()
	parents := make([]childParent, 0, len(collections))
	seeds := make([]*store.Resource, 0, len(collections))
	for _, c := range collections {
		parents = append(parents, childParent{id: c, arn: locCollectionARN(c)})
		seeds = append(seeds, &store.Resource{Provider: "aws", AccountID: testAccountID, Type: TypeLocationGeofenceCollection, NativeID: locCollectionARN(c), DiscoveredBy: testScanID})
	}
	if _, err := st.UpsertResources(seeds); err != nil {
		t.Fatalf("seed collections: %v", err)
	}
	client := locClient(map[string]func(any) (any, error){"ListGeofences": handler})
	total, _, err := scanLocationGeofences(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
	return total, err
}

func TestScanLocationGeofences_StoresEveryPageOfEveryCollection(t *testing.T) {
	st := newTestStore(t)
	var calls, sizes parentCalls
	total, err := scanLocGeofences(t, st, locGeofenceHandler(&calls, &sizes, nil), "c1", "c2")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"c1", "c2"}) {
		t.Errorf("collections listed = %v, want [c1 c2]", got)
	}
	if got := sizes.distinct(); !slices.Equal(got, []string{"100"}) {
		t.Errorf("page sizes sent = %v, want [100]", got)
	}
	rows := rowsByNativeID(t, st, TypeLocationGeofence)
	want := []string{locGeofenceNativeID("c1", "g1"), locGeofenceNativeID("c1", "g2"), locGeofenceNativeID("c2", "g1")}
	if total != len(want) || len(rows) != len(want) {
		t.Fatalf("total = %d, rows = %v; want %v", total, rows, want)
	}
	for _, id := range want {
		if _, ok := rows[id]; !ok {
			t.Errorf("missing %s in %v", id, rows)
		}
	}
	if rows[want[0]].ID == rows[want[2]].ID {
		t.Errorf("g1 in c1 and g1 in c2 share resource id %s", rows[want[0]].ID)
	}

	g2 := rows[locGeofenceNativeID("c1", "g2")]
	if sv(g2.Name) != "g2" || sv(g2.Status) != "DELETING" || sv(g2.Region) != testRegion || sv(g2.CreatedAt) != "2026-09-01T08:00:00Z" ||
		!strings.Contains(g2.AttributesJSON, `"Polygon":[[[1,2],[3,4],[5,6],[1,2]]]`) {
		t.Errorf("g2 name/status/region/created = %q/%q/%q/%q, attrs = %s", sv(g2.Name), sv(g2.Status), sv(g2.Region), sv(g2.CreatedAt), g2.AttributesJSON)
	}
	// locGeobuf {0x0a, 0x01, 0xff} in base64.
	if g1 := rows[want[0]]; !strings.Contains(g1.AttributesJSON, `"Geobuf":"CgH/"`) {
		t.Errorf("c1/g1 attrs = %s, want the Geobuf stored whole as base64", g1.AttributesJSON)
	}
	assertContainsNative(t, st, locCollectionARN("c1"), locGeofenceNativeID("c1", "g1"))
	assertContainsNative(t, st, locCollectionARN("c1"), locGeofenceNativeID("c1", "g2"))
	assertContainsNative(t, st, locCollectionARN("c2"), locGeofenceNativeID("c2", "g1"))
}

func TestScanLocationGeofences_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanLocGeofences(t, st, func(any) (any, error) { return &location.ListGeofencesOutput{}, nil }, "c1")
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeLocationGeofence)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

// A collection deleted after it was listed is skipped silently; a denial warns
// once for the op and keeps the other collections; any other error fails.
func TestScanLocationGeofences_FailedCollection(t *testing.T) {
	t.Run("gone", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "geo:ListGeofences")
		var calls, sizes parentCalls
		total, err := scanLocGeofences(t, st, locGeofenceHandler(&calls, &sizes, map[string]error{"c1": locGone}), "c1", "c2")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if _, ok := rowsByNativeID(t, st, TypeLocationGeofence)[locGeofenceNativeID("c2", "g1")]; total != 1 || !ok || *warns != 0 {
			t.Errorf("total = %d, c2/g1 stored = %v, warnings = %d; want 1, true, 0", total, ok, *warns)
		}
	})
	t.Run("denied", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "geo:ListGeofences")
		var calls, sizes parentCalls
		fail := map[string]error{"c2": locDenied, "c3": locDenied}
		total, err := scanLocGeofences(t, st, locGeofenceHandler(&calls, &sizes, fail), "c1", "c2", "c3")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if *warns != 1 {
			t.Errorf("warnings = %d, want 1", *warns)
		}
		if n := len(rowsByNativeID(t, st, TypeLocationGeofence)); total != 2 || n != 2 {
			t.Errorf("total = %d, rows = %d; want c1's 2 geofences", total, n)
		}
	})
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		var calls, sizes parentCalls
		_, err := scanLocGeofences(t, st, locGeofenceHandler(&calls, &sizes, map[string]error{"c2": locBadInput}), "c1", "c2")
		assertAPICode(t, err, "ValidationException")
	})
}

// Each page is stored as it arrives: a failure on page two leaves page one's
// geofence stored, linked and counted, whether the failure is a denial (warned,
// nil error) or any other error (returned).
func TestScanLocationGeofences_FailedSecondPageKeepsFirstPage(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantCode string
		wantWarn int
	}{
		{name: "denied", err: locDenied, wantWarn: 1},
		{name: "other", err: locBadInput, wantCode: "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "geo:ListGeofences")
			var calls, sizes parentCalls
			total, err := scanLocGeofences(t, st, locGeofenceHandler(&calls, &sizes, map[string]error{"c1/p2": tc.err}), "c1")
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
			}
			rows := rowsByNativeID(t, st, TypeLocationGeofence)
			if _, ok := rows[locGeofenceNativeID("c1", "g1")]; total != 1 || len(rows) != 1 || !ok {
				t.Errorf("total = %d, rows = %v; want page one's c1/g1 only", total, rows)
			}
			if *warns != tc.wantWarn {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarn)
			}
			assertContainsNative(t, st, locCollectionARN("c1"), locGeofenceNativeID("c1", "g1"))
		})
	}
}

// locTopLevelHandlers answers the collection list (c1), the tracker list and
// its consumers (the last pre-existing phase) and the geofence list; every
// other op is denied and warn-skipped by its phase.
func locTopLevelHandlers(calls *parentCalls) map[string]func(in any) (any, error) {
	var sizes parentCalls
	return map[string]func(in any) (any, error){
		"ListGeofenceCollections": func(any) (any, error) {
			return &location.ListGeofenceCollectionsOutput{Entries: []loctypes.ListGeofenceCollectionsResponseEntry{{CollectionName: ptrStr("c1")}}}, nil
		},
		"ListTrackers": func(any) (any, error) {
			return &location.ListTrackersOutput{Entries: []loctypes.ListTrackersResponseEntry{{TrackerName: ptrStr("t1")}}}, nil
		},
		"ListTrackerConsumers": func(any) (any, error) {
			return &location.ListTrackerConsumersOutput{ConsumerArns: []string{locCollectionARN("c1")}}, nil
		},
		"ListGeofences": locGeofenceHandler(calls, &sizes, nil),
	}
}

func TestScanLocation_StoresGeofencesOfListedCollections(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var calls parentCalls
	acct.cfg = opStubConfig(locTopLevelHandlers(&calls), locDenied)

	total, _, err := scanLocation(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanLocation: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"c1"}) {
		t.Errorf("geofences listed for %v, want [c1]", got)
	}
	// c1, t1, its consumer and c1's two stored geofences.
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	assertContainsNative(t, st, locCollectionARN("c1"), locGeofenceNativeID("c1", "g2"))
}

func TestScanLocation_CallsGoToScannedRegion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var calls, regions parentCalls
	cfg := opStubConfig(locTopLevelHandlers(&calls), locDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	if _, _, err := scanLocation(context.Background(), acct, "eu-west-1", st, testScanID); err != nil {
		t.Fatalf("scanLocation: %v", err)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
	if n := len(calls.distinct()); n != 1 {
		t.Errorf("geofence list calls = %d collections, want 1", n)
	}
}

// The geofence phase runs after every pre-existing phase, and its hard error
// reaches the caller with the earlier phases' counts.
func TestScanLocation_GeofenceErrorAfterExistingPhases(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var calls parentCalls
	handlers := locTopLevelHandlers(&calls)
	var failed atomic.Bool
	handlers["ListGeofences"] = func(any) (any, error) {
		failed.Store(true)
		return nil, locBadInput
	}
	acct.cfg = opStubConfig(handlers, locDenied)

	total, _, err := scanLocation(context.Background(), acct, testRegion, st, testScanID)
	if !failed.Load() {
		t.Fatal("ListGeofences never called")
	}
	assertAPICode(t, err, "ValidationException")
	if n := len(rowsByNativeID(t, st, TypeLocationTrackerConsumer)); n != 1 || total != 3 {
		t.Errorf("tracker consumer rows = %d, total = %d; want 1 and 3 (c1, t1, consumer) before the geofence phase", n, total)
	}
}
