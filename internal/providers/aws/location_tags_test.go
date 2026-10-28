package aws

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/location"
	loctypes "github.com/aws/aws-sdk-go-v2/service/location/types"
	"github.com/icearp/disco-cli/store"
)

var locThrottle = apiErr("ThrottlingException", "Rate exceeded")

// locListing is one listing of the fixture whose rows carry tags: its op, the
// type and ARN kind of its rows, the Describe op whose body replaces each row
// and carries its tags ("" when its tags are read with ListTagsForResource),
// and its output for one page holding the element named name.
type locListing struct {
	op, typ, kind, describeOp string
	out                       func(next, name *string) any
}

var locListings = []locListing{
	{"ListKeys", TypeLocationAPIKey, "api-key", "", func(next, name *string) any {
		return &location.ListKeysOutput{NextToken: next, Entries: []loctypes.ListKeysResponseEntry{{KeyName: name, CreateTime: &locCreated}}}
	}},
	{"ListGeofenceCollections", TypeLocationGeofenceCollection, "geofence-collection", "DescribeGeofenceCollection", func(next, name *string) any {
		return &location.ListGeofenceCollectionsOutput{NextToken: next, Entries: []loctypes.ListGeofenceCollectionsResponseEntry{{CollectionName: name, CreateTime: &locCreated}}}
	}},
	{"ListMaps", TypeLocationMap, "map", "", func(next, name *string) any {
		return &location.ListMapsOutput{NextToken: next, Entries: []loctypes.ListMapsResponseEntry{{MapName: name, CreateTime: &locCreated}}}
	}},
	{"ListPlaceIndexes", TypeLocationPlaceIndex, "place-index", "", func(next, name *string) any {
		return &location.ListPlaceIndexesOutput{NextToken: next, Entries: []loctypes.ListPlaceIndexesResponseEntry{{IndexName: name, CreateTime: &locCreated}}}
	}},
	{"ListRouteCalculators", TypeLocationRouteCalculator, "route-calculator", "", func(next, name *string) any {
		return &location.ListRouteCalculatorsOutput{NextToken: next, Entries: []loctypes.ListRouteCalculatorsResponseEntry{{CalculatorName: name, CreateTime: &locCreated}}}
	}},
	{"ListTrackers", TypeLocationTracker, "tracker", "DescribeTracker", func(next, name *string) any {
		return &location.ListTrackersOutput{NextToken: next, Entries: []loctypes.ListTrackersResponseEntry{{TrackerName: name, CreateTime: &locCreated}}}
	}},
}

// locTestTotal is every listing's two pages of one element each, one
// consumer per tracker and one geofence per collection.
const locTestTotal = 6*2 + 2 + 2

// locKMSKey is the key every Describe body names, a field only that body
// carries.
const locKMSKey = "arn:aws:kms:" + testRegion + ":" + testAccountID + ":key/k1"

// locName is the name of element id ("x1" or "x2") of the listing of kind:
// unique across listings, so a Describe's name says which row it is.
func locName(kind, id string) string { return kind + "-" + id }

// locTestARN is the commercial ARN of element id of kind, spelled out here
// rather than built with locARN.
func locTestARN(kind, id string) string {
	return "arn:aws:geo:" + testRegion + ":" + testAccountID + ":" + kind + "/" + locName(kind, id)
}

func locListingOf(t *testing.T, op string) locListing {
	t.Helper()
	for _, l := range locListings {
		if l.op == op {
			return l
		}
	}
	t.Fatalf("%s is not a fixture listing", op)
	return locListing{}
}

// locCalls records a scan's calls and answers its tag reads and Describes,
// by ARN, with tags or errors.
type locCalls struct {
	mu                sync.Mutex
	partition, region string
	maxResults        map[string][]int32
	listedFor         map[string][]string // per-parent op -> parents listed
	tagARNs           []string
	tags              map[string]map[string]string
	errs              map[string]error // tag reads, by ARN
	describeErrs      map[string]error // Describes, by name
	consumerErrs      map[string]error // ListTrackerConsumers, by tracker name
	consumers         map[string][]string
}

// newLocCalls tags each listing's first-page element with its type; each
// tracker has one consumer, the first collection.
func newLocCalls() *locCalls {
	c := &locCalls{
		partition: "aws", region: testRegion,
		maxResults: map[string][]int32{}, listedFor: map[string][]string{}, tags: map[string]map[string]string{},
		errs: map[string]error{}, describeErrs: map[string]error{}, consumerErrs: map[string]error{}, consumers: map[string][]string{},
	}
	for _, l := range locListings {
		c.tags[locTestARN(l.kind, "x1")] = map[string]string{"type": l.typ}
	}
	for _, id := range []string{"x1", "x2"} {
		c.consumers[locName("tracker", id)] = []string{locTestARN("geofence-collection", "x1")}
	}
	return c
}

func (c *locCalls) arn(kind, name string) string {
	return "arn:" + c.partition + ":geo:" + c.region + ":" + testAccountID + ":" + kind + "/" + name
}

func (c *locCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var n int32
	if p := bacField[*int32](in, "MaxResults"); p != nil {
		n = *p
	}
	c.maxResults[op] = append(c.maxResults[op], n)
}

func (c *locCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.maxResults[op])
}

func (c *locCalls) parentsListed(op string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.listedFor[op]))
}

func (c *locCalls) readARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

// pages answers l's listing: the first page holds element x1 and names a
// next page, which holds x2 and ends the listing.
func (c *locCalls) pages(l locListing) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(l.op, in)
		first := bacField[*string](in, "NextToken") == nil
		var next *string
		if first {
			next = sdkaws.String("p2")
		}
		return l.out(next, sdkaws.String(locName(l.kind, pick(first, "x1", "x2")))), nil
	}
}

func (c *locCalls) listTags(in any) (any, error) {
	arn := sv(in.(*location.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	return &location.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
}

// described is the Describe answer for the element name of kind: its error,
// or its ARN and tags.
func (c *locCalls) described(op, kind, name string, in any) (arn string, tags map[string]string, err error) {
	c.record(op, in)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.describeErrs[name]; err != nil {
		return "", nil, err
	}
	arn = c.arn(kind, name)
	return arn, c.tags[arn], nil
}

func (c *locCalls) listed(op, parent string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listedFor[op] = append(c.listedFor[op], parent)
}

func locHandlers(c *locCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"ListTagsForResource": c.listTags,
		"DescribeGeofenceCollection": func(in any) (any, error) {
			name := in.(*location.DescribeGeofenceCollectionInput).CollectionName
			arn, tags, err := c.described("DescribeGeofenceCollection", "geofence-collection", sv(name), in)
			if err != nil {
				return nil, err
			}
			return &location.DescribeGeofenceCollectionOutput{CollectionName: name, CollectionArn: &arn, KmsKeyId: sdkaws.String(locKMSKey), Tags: tags, CreateTime: &locCreated}, nil
		},
		"DescribeTracker": func(in any) (any, error) {
			name := in.(*location.DescribeTrackerInput).TrackerName
			arn, tags, err := c.described("DescribeTracker", "tracker", sv(name), in)
			if err != nil {
				return nil, err
			}
			return &location.DescribeTrackerOutput{TrackerName: name, TrackerArn: &arn, KmsKeyId: sdkaws.String(locKMSKey), Tags: tags, CreateTime: &locCreated}, nil
		},
		"ListTrackerConsumers": func(in any) (any, error) {
			name := sv(in.(*location.ListTrackerConsumersInput).TrackerName)
			c.record("ListTrackerConsumers", in)
			c.listed("ListTrackerConsumers", name)
			c.mu.Lock()
			defer c.mu.Unlock()
			if err := c.consumerErrs[name]; err != nil {
				return nil, err
			}
			return &location.ListTrackerConsumersOutput{ConsumerArns: c.consumers[name]}, nil
		},
		"ListGeofences": func(in any) (any, error) {
			name := sv(in.(*location.ListGeofencesInput).CollectionName)
			c.listed("ListGeofences", name)
			return &location.ListGeofencesOutput{Entries: []loctypes.ListGeofenceResponseEntry{locGeofence("g1", "ACTIVE")}}, nil
		},
	}
	for _, l := range locListings {
		h[l.op] = c.pages(l)
	}
	return h
}

// locScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func locScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, locDenied)
	return scanLocation(ctx, acct, testRegion, st, testScanID)
}

func locWarningOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// locConsumerNativeID is the NativeID of tracker id's consumer, the first
// collection.
func locConsumerNativeID(id string) string {
	return locTestARN("tracker", id) + "/consumer/" + locTestARN("geofence-collection", "x1")
}

// Every listing stores both its pages, each row keyed by the ARN built from
// its name, with its listed name, region and creation time and the tags of
// its resource (nil for one without tags). Collections and trackers store
// their Describe body and take their tags from it; every other type's tags
// are read with ListTagsForResource, sent exactly the ARN of each of its
// rows. Every listing page and consumer listing is sent MaxResults 100. Each
// tracker's consumer and each collection's geofence are stored. No error,
// no warning.
func TestScanLocation_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newLocCalls()

	total, inserted, err := locScan(context.Background(), st, locHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != locTestTotal || inserted != locTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, locTestTotal, locTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	var wantARNs []string
	for _, l := range locListings {
		rows := rowsByNativeID(t, st, l.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %d; want 2", l.typ, len(rows))
		}
		for _, id := range []string{"x1", "x2"} {
			arn := locTestARN(l.kind, id)
			if l.describeOp == "" {
				wantARNs = append(wantARNs, arn)
			}
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", l.typ, arn)
				continue
			}
			if sv(r.Name) != locName(l.kind, id) || sv(r.Region) != testRegion || sv(r.CreatedAt) != "2026-09-01T08:00:00Z" || r.Status != nil {
				t.Errorf("%s name, region, created, status = %q, %q, %q, %v; want %q, %q, 2026-09-01T08:00:00Z, nil",
					arn, sv(r.Name), sv(r.Region), sv(r.CreatedAt), r.Status, locName(l.kind, id), testRegion)
			}
			if id == "x1" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": l.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
			// The Describe body is the only response naming the KMS key.
			if got := strings.Contains(r.AttributesJSON, locKMSKey); got != (l.describeOp != "") {
				t.Errorf("%s attributes = %s; Describe body stored = %v, want %v", arn, r.AttributesJSON, got, l.describeOp != "")
			}
			if !strings.Contains(r.AttributesJSON, `"`+locName(l.kind, id)+`"`) {
				t.Errorf("%s attributes = %s; want its name", arn, r.AttributesJSON)
			}
		}
	}
	slices.Sort(wantARNs)
	if got := calls.readARNs(); !slices.Equal(got, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, wantARNs)
	}
	for _, op := range []string{"ListKeys", "ListGeofenceCollections", "ListMaps", "ListPlaceIndexes", "ListRouteCalculators", "ListTrackers", "ListTrackerConsumers"} {
		if got := calls.maxResults[op]; !slices.Equal(got, []int32{100, 100}) {
			t.Errorf("%s MaxResults = %v; want [100 100]", op, got)
		}
	}
	consumers := rowsByNativeID(t, st, TypeLocationTrackerConsumer)
	for _, id := range []string{"x1", "x2"} {
		r, ok := consumers[locConsumerNativeID(id)]
		if !ok {
			t.Errorf("consumer of tracker %s missing from %v", id, consumers)
			continue
		}
		wantAttrs := `{"ConsumerArn":"` + locTestARN("geofence-collection", "x1") + `","TrackerName":"` + locName("tracker", id) + `"}`
		if sv(r.Name) != locTestARN("geofence-collection", "x1") || sv(r.Region) != testRegion || r.AttributesJSON != wantAttrs || r.TagsJSON != nil {
			t.Errorf("consumer name, region, attrs, tags = %q, %q, %s, %v; want %q, %q, %s, nil",
				sv(r.Name), sv(r.Region), r.AttributesJSON, r.TagsJSON, locTestARN("geofence-collection", "x1"), testRegion, wantAttrs)
		}
	}
	for _, id := range []string{"x1", "x2"} {
		assertContainsNative(t, st, locTestARN("geofence-collection", id), locTestARN("geofence-collection", id)+"/geofence/g1")
	}
}

// Empty listings store nothing and warn about nothing: no Describe, tag
// read or per-parent listing is sent, and each listing is asked once for
// MaxResults 100.
func TestScanLocation_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newLocCalls()
	handlers := locHandlers(calls)
	empty := map[string]any{
		"ListKeys":                &location.ListKeysOutput{},
		"ListGeofenceCollections": &location.ListGeofenceCollectionsOutput{},
		"ListMaps":                &location.ListMapsOutput{},
		"ListPlaceIndexes":        &location.ListPlaceIndexesOutput{},
		"ListRouteCalculators":    &location.ListRouteCalculatorsOutput{},
		"ListTrackers":            &location.ListTrackersOutput{},
	}
	for op, out := range empty {
		handlers[op] = func(in any) (any, error) {
			calls.record(op, in)
			return out, nil
		}
	}

	total, inserted, err := locScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("total, inserted, err = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for op := range empty {
		if got := calls.maxResults[op]; !slices.Equal(got, []int32{100}) {
			t.Errorf("%s MaxResults = %v; want [100]", op, got)
		}
	}
	for _, op := range []string{"DescribeGeofenceCollection", "DescribeTracker", "ListTrackerConsumers"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
	if got := calls.readARNs(); len(got) != 0 {
		t.Errorf("ListTagsForResource ARNs = %v; want none", got)
	}
	if got := calls.parentsListed("ListGeofences"); len(got) != 0 {
		t.Errorf("ListGeofences listed for %v; want none", got)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanLocation_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := locScan(context.Background(), st, locHandlers(newLocCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := locScan(context.Background(), st, locHandlers(newLocCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != locTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, locTestTotal)
	}
}

// A failed tag read. ResourceNotFoundException, which the op models, drops
// the row silently. A denial keeps the row untagged and warns once under
// the tag op; a throttle keeps it and is transient; any other error keeps it
// and is an error.
func TestScanLocation_TagReadFailures(t *testing.T) {
	for _, x := range []struct{ kind, typ string }{
		{"api-key", TypeLocationAPIKey},
		{"route-calculator", TypeLocationRouteCalculator},
	} {
		for _, c := range []struct {
			name    string
			err     error
			outcome serviceOutcome // -1: no error
			code    string
			lost    int
			warnOp  string
		}{
			{"not found", locGone, -1, "", 1, ""},
			{"denied", locDenied, -1, "", 0, locTagsOp},
			{"transient", locThrottle, outcomeTransient, "ThrottlingException", 0, ""},
			{"other error", locBadInput, outcomeError, "ValidationException", 0, ""},
		} {
			t.Run(x.typ+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newLocCalls()
				arn := locTestARN(x.kind, "x1")
				calls.errs[arn] = c.err

				total, _, err := locScan(context.Background(), st, locHandlers(calls))
				if c.outcome < 0 {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, c.outcome)
					assertAPICode(t, err, c.code)
				}
				if want := locTestTotal - c.lost; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				r, ok := rowsByNativeID(t, st, x.typ)[arn]
				if ok != (c.lost == 0) {
					t.Fatalf("row stored = %v; want %v", ok, c.lost == 0)
				}
				if ok && r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				w := warns()
				var want []string
				if c.warnOp != "" {
					want = []string{c.warnOp}
				}
				if got := locWarningOps(w); !slices.Equal(got, want) {
					t.Fatalf("warnings = %v; want %v", w, want)
				}
				for _, x := range w {
					if x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, arn) {
						t.Errorf("warning Scope %q, message %q; want %s/%s naming %s", x.Scope, x.Message, testAccountID, testRegion, arn)
					}
				}
			})
		}
	}
}

// Denied tag reads in several phases send one warning for the whole scan;
// the rows are kept.
func TestScanLocation_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newLocCalls()
	for _, kind := range []string{"api-key", "place-index", "route-calculator"} {
		calls.errs[locTestARN(kind, "x2")] = locDenied
	}

	total, _, err := locScan(context.Background(), st, locHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != locTestTotal {
		t.Errorf("total = %d; want %d", total, locTestTotal)
	}
	if got := locWarningOps(warns()); !slices.Equal(got, []string{locTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, locTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanLocation_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, locTagsOp)
	calls := newLocCalls()
	calls.errs[locTestARN("map", "x1")] = locThrottle
	calls.errs[locTestARN("route-calculator", "x2")] = locBadInput

	_, _, err := locScan(context.Background(), st, locHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", locTagsOp, *warns)
	}
}

// A failed Describe of a collection or tracker. ResourceNotFoundException,
// which both Describes model, drops the row: its tags are not read and its
// geofences or consumers are not listed. Any other failure keeps the list
// element as the row, reads its tags with ListTagsForResource and lists its
// children: a denial warns once under the Describe; a throttle is transient;
// any other error is an error.
func TestScanLocation_DescribeFailures(t *testing.T) {
	for _, x := range []struct{ kind, typ, op, childOp string }{
		{"geofence-collection", TypeLocationGeofenceCollection, "geo:DescribeGeofenceCollection", "ListGeofences"},
		{"tracker", TypeLocationTracker, "geo:DescribeTracker", "ListTrackerConsumers"},
	} {
		for _, c := range []struct {
			name    string
			err     error
			outcome serviceOutcome // -1: no error
			code    string
			lost    bool
			warn    bool
		}{
			{"not found", locGone, -1, "", true, false},
			{"denied", locDenied, -1, "", false, true},
			{"transient", locThrottle, outcomeTransient, "ThrottlingException", false, false},
			{"other error", locBadInput, outcomeError, "ValidationException", false, false},
		} {
			t.Run(x.typ+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newLocCalls()
				name, arn := locName(x.kind, "x1"), locTestARN(x.kind, "x1")
				calls.describeErrs[name] = c.err

				total, _, err := locScan(context.Background(), st, locHandlers(calls))
				if c.outcome < 0 {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, c.outcome)
					assertAPICode(t, err, c.code)
				}
				r, ok := rowsByNativeID(t, st, x.typ)[arn]
				if ok == c.lost {
					t.Fatalf("row stored = %v; want %v", ok, !c.lost)
				}
				wantParents := []string{locName(x.kind, "x1"), locName(x.kind, "x2")}
				wantTotal := locTestTotal
				if c.lost {
					// The row and its one child.
					wantTotal -= 2
					wantParents = wantParents[1:]
				}
				if total != wantTotal {
					t.Errorf("total = %d; want %d", total, wantTotal)
				}
				if got := calls.parentsListed(x.childOp); !slices.Equal(got, wantParents) {
					t.Errorf("%s listed for %v; want %v", x.childOp, got, wantParents)
				}
				if read := slices.Contains(calls.readARNs(), arn); read == c.lost {
					t.Errorf("tags read = %v; want %v", read, !c.lost)
				}
				if ok {
					if strings.Contains(r.AttributesJSON, locKMSKey) {
						t.Errorf("attributes = %s; want the list element", r.AttributesJSON)
					}
					if sv(r.Name) != name || sv(r.CreatedAt) != "2026-09-01T08:00:00Z" {
						t.Errorf("name, created = %q, %q; want %q, 2026-09-01T08:00:00Z", sv(r.Name), sv(r.CreatedAt), name)
					}
					assertTagsMap(t, r.TagsJSON, map[string]string{"type": x.typ})
				}
				var want []string
				if c.warn {
					want = []string{x.op}
				}
				w := warns()
				if got := locWarningOps(w); !slices.Equal(got, want) {
					t.Fatalf("warnings = %v; want %v", w, want)
				}
				if c.warn && (w[0].Scope != testAccountID+"/"+testRegion || !strings.Contains(w[0].Message, arn)) {
					t.Errorf("warning Scope %q, message %q; want %s/%s naming %s", w[0].Scope, w[0].Message, testAccountID, testRegion, arn)
				}
			})
		}
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored.
func TestScanLocation_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeLocationMap)
	calls := newLocCalls()
	calls.errs[locTestARN("map", "x1")] = locDenied

	total, _, err := locScan(context.Background(), st, locHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count("ListMaps"); n != 1 {
		t.Errorf("ListMaps called %d times; want 1, none after the failed write", n)
	}
	if n := calls.count("ListPlaceIndexes"); n != 0 {
		t.Errorf("ListPlaceIndexes called %d times; want 0 after the failed write", n)
	}
	if total != 4 {
		t.Errorf("total = %d; want 4 (the API keys and collections)", total)
	}
}

// A listing's failed later page keeps the page read before it, and its
// error does not stop the later phases; a tracker on the lost page has no
// consumers listed.
func TestScanLocation_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, c := range []struct {
		op   string
		lost int
	}{
		{"ListKeys", 1},
		{"ListTrackers", 2},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newLocCalls()
			handlers := locHandlers(calls)
			handlers[c.op] = failOnCall(2, locBadInput, handlers[c.op])
			l := locListingOf(t, c.op)

			total, _, err := locScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if total != locTestTotal-c.lost {
				t.Errorf("total = %d; want %d", total, locTestTotal-c.lost)
			}
			if _, ok := rowsByNativeID(t, st, l.typ)[locTestARN(l.kind, "x1")]; !ok {
				t.Errorf("%s row from the page before the failure missing", l.typ)
			}
			if got := len(rowsByNativeID(t, st, TypeLocationGeofence)); got != 2 {
				t.Errorf("geofences (the last phase) = %d; want 2", got)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanLocation_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := locHandlers(newLocCalls())
	l := locListingOf(t, "ListMaps")
	handlers[l.op] = func(in any) (any, error) {
		id := pick(bacField[*string](in, "NextToken") == nil, "x1", "x2")
		return l.out(sdkaws.String("same"), sdkaws.String(locName(l.kind, id))), nil
	}

	total, _, err := locScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != locTestTotal {
		t.Errorf("total = %d; want %d", total, locTestTotal)
	}
}

// An element without a name has no ARN to be stored under: it is not
// stored, not sent to the tag op or a Describe, and is warned about once
// under its listing. A tracker without a name has no consumers listed.
func TestScanLocation_ElementWithoutName(t *testing.T) {
	for _, c := range []struct {
		op         string
		describeOp string
		lost       int
	}{
		{"ListPlaceIndexes", "", 2},
		{"ListTrackers", "DescribeTracker", 4},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newLocCalls()
			handlers := locHandlers(calls)
			l := locListingOf(t, c.op)
			handlers[c.op] = func(in any) (any, error) {
				calls.record(c.op, in)
				return l.out(nil, nil), nil
			}

			total, _, err := locScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != locTestTotal-c.lost {
				t.Errorf("total = %d; want %d", total, locTestTotal-c.lost)
			}
			w := warns()
			if len(w) != 1 || w[0].Service != "geo:"+c.op || w[0].Scope != testAccountID+"/"+testRegion || !strings.Contains(w[0].Message, locNoName) {
				t.Fatalf("warnings = %v; want one under geo:%s naming %s", w, c.op, locNoName)
			}
			if c.describeOp != "" {
				if n := calls.count(c.describeOp); n != 0 {
					t.Errorf("%s called %d times; want 0", c.describeOp, n)
				}
			}
			var wantARNs []string
			for _, o := range locListings {
				if o.describeOp == "" && o.op != c.op {
					wantARNs = append(wantARNs, locTestARN(o.kind, "x1"), locTestARN(o.kind, "x2"))
				}
			}
			slices.Sort(wantARNs)
			if got := calls.readARNs(); !slices.Equal(got, wantARNs) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, wantARNs)
			}
		})
	}
}

// A GovCloud or China account's ARNs are built in its partition and reach
// the tag op as such.
func TestScanLocation_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newLocCalls()
			calls.partition, calls.region = pf.partition, pf.region
			var want []string
			for _, l := range locListings {
				if l.describeOp == "" {
					want = append(want, calls.arn(l.kind, locName(l.kind, "x1")), calls.arn(l.kind, locName(l.kind, "x2")))
				}
			}
			slices.Sort(want)
			m := partitionARN(pf.partition, "geo", pf.region, "map/map-x1")
			calls.tags[m] = map[string]string{"env": "prod"}
			tr := partitionARN(pf.partition, "geo", pf.region, "tracker/tracker-x1")
			calls.tags[tr] = map[string]string{"env": "dev"}
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(locHandlers(calls), locDenied)

			if _, _, err := scanLocation(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := calls.readARNs(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, want)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeLocationMap)[m].TagsJSON, map[string]string{"env": "prod"})
			assertTagsMap(t, rowsByNativeID(t, st, TypeLocationTracker)[tr].TagsJSON, map[string]string{"env": "dev"})
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanLocation_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newLocCalls()
	handlers := locHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListMaps"]
	handlers["ListMaps"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := locScan(ctx, st, handlers)
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
	if n := calls.count("ListPlaceIndexes"); n != 0 {
		t.Errorf("ListPlaceIndexes called %d times after ctx ended", n)
	}
}

// NXDOMAIN. A part of the service whose first listing stores nothing and
// answers NXDOMAIN is missing from the region: silent, the other parts kept.
// Once rows were stored, an NXDOMAIN of a part already answering (a
// collection's geofences, a tag read) is a failure of this scan, counts
// kept. With nothing stored the region is unavailable.
func TestScanLocation_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "cp.maps.geo.us-east-1.amazonaws.com", IsNotFound: true}
	// Each part's first listing missing in turn costs only that listing's
	// rows and their children: every other listing's two rows, each stored
	// tracker's consumer and each stored collection's geofence are kept. The
	// geofences and consumers belong to the parts of the collections and
	// trackers they are listed for, so a missing tracking part keeps the
	// geofences.
	for _, c := range []struct {
		op   string
		lost int
	}{
		{"ListGeofenceCollections", 4},
		{"ListMaps", 2},
		{"ListPlaceIndexes", 2},
		{"ListRouteCalculators", 2},
		{"ListTrackers", 4},
	} {
		t.Run("a part missing from the region/"+c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			handlers := locHandlers(newLocCalls())
			handlers[c.op] = func(any) (any, error) { return nil, nx }
			total, _, err := locScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != locTestTotal-c.lost {
				t.Errorf("total = %d; want %d", total, locTestTotal-c.lost)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			for _, l := range locListings {
				want := 2
				if l.op == c.op {
					want = 0
				}
				if got := len(rowsByNativeID(t, st, l.typ)); got != want {
					t.Errorf("%s rows = %d; want %d", l.typ, got, want)
				}
			}
			wantChildren := map[string]int{TypeLocationGeofence: 2, TypeLocationTrackerConsumer: 2}
			switch c.op {
			case "ListGeofenceCollections":
				wantChildren[TypeLocationGeofence] = 0
			case "ListTrackers":
				wantChildren[TypeLocationTrackerConsumer] = 0
			}
			for typ, want := range wantChildren {
				if got := len(rowsByNativeID(t, st, typ)); got != want {
					t.Errorf("%s rows = %d; want %d", typ, got, want)
				}
			}
		})
	}
	t.Run("geofences of a stored collection", func(t *testing.T) {
		st := newTestStore(t)
		handlers := locHandlers(newLocCalls())
		handlers["ListGeofences"] = func(any) (any, error) { return nil, nx }
		total, _, err := locScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != locTestTotal-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, locTestTotal-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newLocCalls()
		calls.errs[locTestARN("map", "x2")] = nx
		_, _, err := locScan(context.Background(), st, locHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanLocation(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

// A tracker's failed consumer listing does not cost the other tracker its
// consumer: a tracker deleted since it was listed is skipped silently, a
// denial warns once under the op, any other error is returned. A consumer
// with an empty ARN is not stored and is warned about, naming its tracker.
func TestScanLocation_TrackerConsumerFailures(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		empty   bool
		outcome serviceOutcome // -1: no error
		warn    bool
	}{
		{name: "not found", err: locGone, outcome: -1},
		{name: "denied", err: locDenied, outcome: -1, warn: true},
		{name: "other error", err: locBadInput, outcome: outcomeError},
		{name: "empty consumer ARN", empty: true, outcome: -1, warn: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newLocCalls()
			t1 := locName("tracker", "x1")
			if c.empty {
				calls.consumers[t1] = []string{""}
			} else {
				calls.consumerErrs[t1] = c.err
			}

			total, _, err := locScan(context.Background(), st, locHandlers(calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, "ValidationException")
			}
			if total != locTestTotal-1 {
				t.Errorf("total = %d; want %d", total, locTestTotal-1)
			}
			rows := rowsByNativeID(t, st, TypeLocationTrackerConsumer)
			if _, ok := rows[locConsumerNativeID("x2")]; len(rows) != 1 || !ok {
				t.Errorf("consumers = %v; want tracker x2's only", rows)
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{locListTrackerConsumersOp}
			}
			if got := locWarningOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			if c.warn && (w[0].Scope != testAccountID+"/"+testRegion || !strings.Contains(w[0].Message, t1)) {
				t.Errorf("warning Scope %q, message %q; want %s/%s naming %s", w[0].Scope, w[0].Message, testAccountID, testRegion, t1)
			}
		})
	}
}
