package aws

import (
	"context"
	"maps"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	r53Denied   = &smithy.GenericAPIError{Code: "AccessDenied", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: route53:List"}
	r53BadInput = &smithy.GenericAPIError{Code: "InvalidInput", Message: "bad"}
	r53Gone     = &smithy.GenericAPIError{Code: "NoSuchCidrCollectionException", Message: "gone"}
)

const (
	r53CollA = "arn:aws:route53:::cidrcollection/c-a"
	r53CollB = "arn:aws:route53:::cidrcollection/c-b"
	r53CollC = "arn:aws:route53:::cidrcollection/c-c"

	r53LocAEast = "arn:aws:route53:::cidrcollection/c-a/location/east"
	r53LocAWest = "arn:aws:route53:::cidrcollection/c-a/location/west"
	r53LocBEast = "arn:aws:route53:::cidrcollection/c-b/location/east"

	r53BlockAEast = "arn:aws:route53:::cidrcollection/c-a/location/east/cidr/10.0.0.0/24"
	r53BlockAWest = "arn:aws:route53:::cidrcollection/c-a/location/west/cidr/10.0.0.0/24"
	r53BlockBEast = "arn:aws:route53:::cidrcollection/c-b/location/east/cidr/10.0.0.0/24"
)

func r53Client(handlers map[string]func(in any) (any, error)) *route53.Client {
	return route53.NewFromConfig(opStubConfig(handlers, r53Denied))
}

// r53CollectionParents seeds a collection row per id and returns them as
// fan-out parents.
func r53CollectionParents(t *testing.T, st *store.Store, ids ...string) []childParent {
	t.Helper()
	parents := make([]childParent, 0, len(ids))
	arns := make([]string, 0, len(ids))
	for _, id := range ids {
		arn := "arn:aws:route53:::cidrcollection/" + id
		parents = append(parents, childParent{id: id, arn: arn})
		arns = append(arns, arn)
	}
	seedParentRows(t, st, TypeRoute53CIDRCollection, arns...)
	return parents
}

func TestRoute53CIDRNativeIDs(t *testing.T) {
	if got := route53CIDRLocationNativeID(r53CollA, "east"); got != r53LocAEast {
		t.Errorf("location id = %q, want %q", got, r53LocAEast)
	}
	if got := route53CIDRBlockNativeID(r53CollA, "east", "10.0.0.0/24"); got != r53BlockAEast {
		t.Errorf("block id = %q, want %q", got, r53BlockAEast)
	}
	if got := route53CIDRBlockNativeID(r53CollA, "east", "2001:db8::/48"); got != "arn:aws:route53:::cidrcollection/c-a/location/east/cidr/2001:db8::/48" {
		t.Errorf("ipv6 block id = %q", got)
	}
}

// Page two holds a collection without an Id: it is stored but cannot be a
// fan-out parent. A collection without an ARN is not stored.
func TestScanRoute53CIDRCollections_ReturnsStoredCollectionsAsParents(t *testing.T) {
	st := newTestStore(t)
	client := r53Client(map[string]func(any) (any, error){
		"ListCidrCollections": func(in any) (any, error) {
			if in.(*route53.ListCidrCollectionsInput).NextToken == nil {
				return &route53.ListCidrCollectionsOutput{
					CidrCollections: []route53types.CollectionSummary{{Arn: ptrStr(r53CollA), Id: ptrStr("c-a"), Name: ptrStr("a")}},
					NextToken:       ptrStr("p2"),
				}, nil
			}
			return &route53.ListCidrCollectionsOutput{CidrCollections: []route53types.CollectionSummary{
				{Arn: ptrStr(r53CollB), Id: ptrStr("c-b"), Name: ptrStr("b")},
				{Arn: ptrStr(r53CollC), Name: ptrStr("no-id")},
				{Id: ptrStr("c-d"), Name: ptrStr("no-arn")},
			}}, nil
		},
	})
	parents, total, _, err := scanRoute53CIDRCollections(context.Background(), client, newTestAccount(testAccountID), st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := []childParent{{id: "c-a", arn: r53CollA}, {id: "c-b", arn: r53CollB}}
	if !slices.Equal(parents, want) {
		t.Errorf("parents = %v, want %v", parents, want)
	}
	ids := slices.Sorted(maps.Keys(rowsByNativeID(t, st, TypeRoute53CIDRCollection)))
	if wantIDs := []string{r53CollA, r53CollB, r53CollC}; total != 3 || !slices.Equal(ids, wantIDs) {
		t.Errorf("total = %d, rows = %v; want 3, %v", total, ids, wantIDs)
	}
}

// r53LocationHandler serves c-a in two pages (east; then west and a nameless
// element) and every other collection one page (east). A collection id in fail
// returns that error. Every call's collection id is recorded.
func r53LocationHandler(calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	loc := func(name string) route53types.LocationSummary {
		return route53types.LocationSummary{LocationName: ptrStr(name)}
	}
	return func(in any) (any, error) {
		req := in.(*route53.ListCidrLocationsInput)
		id := sv(req.CollectionId)
		calls.add(id)
		if err := fail[id]; err != nil {
			return nil, err
		}
		if id != "c-a" {
			return &route53.ListCidrLocationsOutput{CidrLocations: []route53types.LocationSummary{loc("east")}}, nil
		}
		if req.NextToken == nil {
			return &route53.ListCidrLocationsOutput{CidrLocations: []route53types.LocationSummary{loc("east")}, NextToken: ptrStr("p2")}, nil
		}
		return &route53.ListCidrLocationsOutput{CidrLocations: []route53types.LocationSummary{loc("west"), {}}}, nil
	}
}

func scanR53Locations(t *testing.T, st *store.Store, handler func(in any) (any, error), ids ...string) (map[string]bool, int, error) {
	t.Helper()
	parents := r53CollectionParents(t, st, ids...)
	client := r53Client(map[string]func(any) (any, error){"ListCidrLocations": handler})
	stored, total, _, err := scanRoute53CIDRLocations(context.Background(), client, newTestAccount(testAccountID), st, testScanID, parents)
	return stored, total, err
}

// The same location name in two collections stays two rows.
func TestScanRoute53CIDRLocations_StoresEveryPageOfEveryCollection(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	stored, total, err := scanR53Locations(t, st, r53LocationHandler(&calls, nil), "c-a", "c-b")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"c-a", "c-b"}) {
		t.Errorf("collections listed = %v, want [c-a c-b]", got)
	}
	rows := rowsByNativeID(t, st, TypeRoute53CIDRLocation)
	ids := slices.Sorted(maps.Keys(rows))
	want := []string{r53LocAEast, r53LocAWest, r53LocBEast}
	if total != 3 || !slices.Equal(ids, want) {
		t.Fatalf("total = %d, rows = %v; want 3, %v", total, ids, want)
	}
	if got := slices.Sorted(maps.Keys(stored)); !slices.Equal(got, want) {
		t.Errorf("stored = %v, want %v", got, want)
	}
	west := rows[r53LocAWest]
	if sv(west.Name) != "west" || sv(west.Region) != "global" || west.AttributesJSON != `{"LocationName":"west"}` {
		t.Errorf("west name/region = %q/%q, attrs = %s", sv(west.Name), sv(west.Region), west.AttributesJSON)
	}
	assertContainsNative(t, st, r53CollA, r53LocAEast)
	assertContainsNative(t, st, r53CollA, r53LocAWest)
	assertContainsNative(t, st, r53CollB, r53LocBEast)
	assertContainsEdgesDeclared(t, st)
}

func TestScanRoute53CIDRLocations_Empty(t *testing.T) {
	st := newTestStore(t)
	stored, total, err := scanR53Locations(t, st, func(any) (any, error) { return &route53.ListCidrLocationsOutput{}, nil }, "c-a")
	if err != nil || total != 0 || len(stored) != 0 || len(rowsByNativeID(t, st, TypeRoute53CIDRLocation)) != 0 {
		t.Errorf("total = %d, stored = %v, err = %v; want 0 rows, nil", total, stored, err)
	}
}

// A collection deleted after it was listed is skipped silently. A denial
// warns once for the op and keeps the other collections; any other error
// fails the phase.
func TestScanRoute53CIDRLocations_FailedCollection(t *testing.T) {
	const op = "route53:ListCidrLocations"
	t.Run("gone", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, op)
		var calls parentCalls
		stored, total, err := scanR53Locations(t, st, r53LocationHandler(&calls, map[string]error{"c-a": r53Gone}), "c-a", "c-b")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if got := slices.Sorted(maps.Keys(stored)); total != 1 || *warns != 0 || !slices.Equal(got, []string{r53LocBEast}) {
			t.Errorf("total = %d, warnings = %d, stored = %v; want 1, 0, [%s]", total, *warns, got, r53LocBEast)
		}
	})
	t.Run("denied", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, op)
		var calls parentCalls
		stored, total, err := scanR53Locations(t, st, r53LocationHandler(&calls, map[string]error{"c-b": r53Denied, "c-c": r53Denied}), "c-a", "c-b", "c-c")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		n := len(rowsByNativeID(t, st, TypeRoute53CIDRLocation))
		if *warns != 1 || total != 2 || n != 2 || len(stored) != 2 {
			t.Errorf("warnings = %d, total = %d, rows = %d, stored = %d; want 1, 2, 2, 2", *warns, total, n, len(stored))
		}
	})
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		var calls parentCalls
		stored, _, err := scanR53Locations(t, st, r53LocationHandler(&calls, map[string]error{"c-b": r53BadInput}), "c-a", "c-b")
		assertAPICode(t, err, "InvalidInput")
		if stored != nil {
			t.Errorf("stored = %v, want nil on a hard error", stored)
		}
	})
}

// r53BlockHandler serves c-a in two pages (10.0.0.0/24 in east; then the same
// CIDR in west plus an element missing each key) and every other collection
// one page (10.0.0.0/24 in east). A "collection" or "collection/page2" key in
// fail returns that error. Every call's collection id is recorded, and any
// call that filters on a location fails the test.
func r53BlockHandler(t *testing.T, calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	block := func(cidr, location string) route53types.CidrBlockSummary {
		return route53types.CidrBlockSummary{CidrBlock: ptrStr(cidr), LocationName: ptrStr(location)}
	}
	return func(in any) (any, error) {
		req := in.(*route53.ListCidrBlocksInput)
		id := sv(req.CollectionId)
		calls.add(id)
		if req.LocationName != nil {
			t.Errorf("ListCidrBlocks(%s) filtered on location %q, want one unfiltered list per collection", id, *req.LocationName)
		}
		key := id
		if req.NextToken != nil {
			key += "/page2"
		}
		if err := fail[key]; err != nil {
			return nil, err
		}
		if id != "c-a" {
			return &route53.ListCidrBlocksOutput{CidrBlocks: []route53types.CidrBlockSummary{block("10.0.0.0/24", "east")}}, nil
		}
		if req.NextToken == nil {
			return &route53.ListCidrBlocksOutput{CidrBlocks: []route53types.CidrBlockSummary{block("10.0.0.0/24", "east")}, NextToken: ptrStr("p2")}, nil
		}
		return &route53.ListCidrBlocksOutput{CidrBlocks: []route53types.CidrBlockSummary{
			block("10.0.0.0/24", "west"),
			{LocationName: ptrStr("east")},
			{CidrBlock: ptrStr("10.9.0.0/24")},
		}}, nil
	}
}

// scanR53Blocks seeds the collection rows and the given location rows, then
// fans out with locations as the stored set.
func scanR53Blocks(t *testing.T, st *store.Store, handler func(in any) (any, error), locations []string, ids ...string) (int, error) {
	t.Helper()
	parents := r53CollectionParents(t, st, ids...)
	seedParentRows(t, st, TypeRoute53CIDRLocation, r53LocAEast, r53LocAWest, r53LocBEast)
	stored := map[string]bool{}
	for _, l := range locations {
		stored[l] = true
	}
	client := r53Client(map[string]func(any) (any, error){"ListCidrBlocks": handler})
	total, _, err := scanRoute53CIDRBlocks(context.Background(), client, newTestAccount(testAccountID), st, testScanID, parents, stored)
	return total, err
}

// The same CIDR in two locations of one collection, and in the same-named
// location of two collections, stays three rows.
func TestScanRoute53CIDRBlocks_StoresEveryPageOfEveryCollection(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, err := scanR53Blocks(t, st, r53BlockHandler(t, &calls, nil), []string{r53LocAEast, r53LocAWest, r53LocBEast}, "c-a", "c-b")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"c-a", "c-b"}) {
		t.Errorf("collections listed = %v, want [c-a c-b]", got)
	}
	rows := rowsByNativeID(t, st, TypeRoute53CIDRBlock)
	ids := slices.Sorted(maps.Keys(rows))
	if want := []string{r53BlockAEast, r53BlockAWest, r53BlockBEast}; total != 3 || !slices.Equal(ids, want) {
		t.Fatalf("total = %d, rows = %v; want 3, %v", total, ids, want)
	}
	west := rows[r53BlockAWest]
	if sv(west.Name) != "10.0.0.0/24" || sv(west.Region) != "global" || west.AttributesJSON != `{"CidrBlock":"10.0.0.0/24","LocationName":"west"}` {
		t.Errorf("west name/region = %q/%q, attrs = %s", sv(west.Name), sv(west.Region), west.AttributesJSON)
	}
	assertContainsNative(t, st, r53LocAEast, r53BlockAEast)
	assertContainsNative(t, st, r53LocAWest, r53BlockAWest)
	assertContainsNative(t, st, r53LocBEast, r53BlockBEast)
	assertContainsEdgesDeclared(t, st)
}

// A block whose location the location phase did not store is still stored,
// linked under its collection instead of the location; a block whose location
// was stored stays under the location.
func TestScanRoute53CIDRBlocks_UnstoredLocationLinksBlockToCollection(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, err := scanR53Blocks(t, st, r53BlockHandler(t, &calls, nil), []string{r53LocAEast}, "c-a")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if n := len(rowsByNativeID(t, st, TypeRoute53CIDRBlock)); total != 2 || n != 2 {
		t.Errorf("total = %d, rows = %d; want 2, 2", total, n)
	}
	assertContainsNative(t, st, r53LocAEast, r53BlockAEast)
	rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, r53LocAWest), store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("west contains %v, want nothing: the location phase did not store it", rels)
	}
	assertContainsNative(t, st, r53CollA, r53BlockAWest)
	assertContainsEdgesDeclared(t, st)
}

func TestScanRoute53CIDRBlocks_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanR53Blocks(t, st, func(any) (any, error) { return &route53.ListCidrBlocksOutput{}, nil }, nil, "c-a")
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeRoute53CIDRBlock)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

// A collection deleted after it was listed is skipped silently. A denial
// warns once for the op and keeps the other collections; any other error
// fails the phase, keeping the pages stored before it.
func TestScanRoute53CIDRBlocks_FailedCollection(t *testing.T) {
	const op = "route53:ListCidrBlocks"
	all := []string{r53LocAEast, r53LocAWest, r53LocBEast}
	t.Run("gone", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, op)
		var calls parentCalls
		total, err := scanR53Blocks(t, st, r53BlockHandler(t, &calls, map[string]error{"c-a": r53Gone}), all, "c-a", "c-b")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		ids := slices.Sorted(maps.Keys(rowsByNativeID(t, st, TypeRoute53CIDRBlock)))
		if total != 1 || *warns != 0 || !slices.Equal(ids, []string{r53BlockBEast}) {
			t.Errorf("total = %d, warnings = %d, rows = %v; want 1, 0, [%s]", total, *warns, ids, r53BlockBEast)
		}
	})
	t.Run("denied", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, op)
		var calls parentCalls
		total, err := scanR53Blocks(t, st, r53BlockHandler(t, &calls, map[string]error{"c-b": r53Denied, "c-c": r53Denied}), all, "c-a", "c-b", "c-c")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		n := len(rowsByNativeID(t, st, TypeRoute53CIDRBlock))
		if *warns != 1 || total != 2 || n != 2 {
			t.Errorf("warnings = %d, total = %d, rows = %d; want 1, 2, 2", *warns, total, n)
		}
	})
	t.Run("other on page two", func(t *testing.T) {
		st := newTestStore(t)
		var calls parentCalls
		total, err := scanR53Blocks(t, st, r53BlockHandler(t, &calls, map[string]error{"c-a/page2": r53BadInput}), all, "c-a")
		assertAPICode(t, err, "InvalidInput")
		ids := slices.Sorted(maps.Keys(rowsByNativeID(t, st, TypeRoute53CIDRBlock)))
		if total != 1 || !slices.Equal(ids, []string{r53BlockAEast}) {
			t.Errorf("total = %d, rows = %v; want 1, [%s] from page one", total, ids, r53BlockAEast)
		}
	})
}

// r53TopLevelHandlers answers scanRoute53 with no hosted zones, one traffic
// policy instance (from the last pre-existing phase), collection c-a, its
// location east and one block there. Ops without a handler are denied.
func r53TopLevelHandlers() map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListHostedZones": func(any) (any, error) { return &route53.ListHostedZonesOutput{}, nil },
		"ListTrafficPolicyInstances": func(any) (any, error) {
			return &route53.ListTrafficPolicyInstancesOutput{TrafficPolicyInstances: []route53types.TrafficPolicyInstance{{Id: ptrStr("tpi-1"), Name: ptrStr("www")}}}, nil
		},
		"ListCidrCollections": func(any) (any, error) {
			return &route53.ListCidrCollectionsOutput{CidrCollections: []route53types.CollectionSummary{{Arn: ptrStr(r53CollA), Id: ptrStr("c-a"), Name: ptrStr("a")}}}, nil
		},
		"ListCidrLocations": func(any) (any, error) {
			return &route53.ListCidrLocationsOutput{CidrLocations: []route53types.LocationSummary{{LocationName: ptrStr("east")}}}, nil
		},
		"ListCidrBlocks": func(any) (any, error) {
			return &route53.ListCidrBlocksOutput{CidrBlocks: []route53types.CidrBlockSummary{{CidrBlock: ptrStr("10.0.0.0/24"), LocationName: ptrStr("east")}}}, nil
		},
	}
}

func TestScanRoute53_StoresCIDRLocationsAndBlocks(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(r53TopLevelHandlers(), r53Denied)

	total, _, err := scanRoute53(context.Background(), acct, st, testScanID)
	if err != nil {
		t.Fatalf("scanRoute53: %v", err)
	}
	// 1 traffic policy instance, 1 collection, 1 location, 1 block.
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}
	assertContainsNative(t, st, r53CollA, r53LocAEast)
	assertContainsNative(t, st, r53LocAEast, r53BlockAEast)
	assertContainsEdgesDeclared(t, st)
}

// Each new phase runs after every pre-existing phase, blocks after
// locations, and a phase's hard error reaches the caller with the counts
// stored before it.
func TestScanRoute53_NewPhaseErrorAfterExistingPhases(t *testing.T) {
	for _, tc := range []struct {
		op            string
		wantTotal     int
		wantLocations int
	}{
		// 1 traffic policy instance + 1 collection.
		{op: "ListCidrLocations", wantTotal: 2},
		// + 1 location.
		{op: "ListCidrBlocks", wantTotal: 3, wantLocations: 1},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			handlers := r53TopLevelHandlers()
			var failed atomic.Bool
			handlers[tc.op] = func(any) (any, error) {
				failed.Store(true)
				return nil, r53BadInput
			}
			acct.cfg = opStubConfig(handlers, r53Denied)

			total, _, err := scanRoute53(context.Background(), acct, st, testScanID)
			if !failed.Load() {
				t.Fatalf("%s never called", tc.op)
			}
			assertAPICode(t, err, "InvalidInput")
			if n := len(rowsByNativeID(t, st, TypeRoute53TrafficPolicyInstance)); n != 1 {
				t.Errorf("traffic policy instance rows = %d, want 1 before the %s phase", n, tc.op)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
			if n := len(rowsByNativeID(t, st, TypeRoute53CIDRLocation)); n != tc.wantLocations {
				t.Errorf("location rows = %d, want %d", n, tc.wantLocations)
			}
			if n := len(rowsByNativeID(t, st, TypeRoute53CIDRBlock)); n != 0 {
				t.Errorf("block rows = %d, want 0", n)
			}
		})
	}
}
