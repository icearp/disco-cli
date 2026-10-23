package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/mediapackage"
	mptypes "github.com/aws/aws-sdk-go-v2/service/mediapackage/types"
	"github.com/aws/aws-sdk-go-v2/service/mediapackagevod"
	mpvodtypes "github.com/aws/aws-sdk-go-v2/service/mediapackagevod/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/store"
)

var mpTestTags = map[string]string{"team": "video"}

func mpTestARN(svc, kind, id string) string {
	return "arn:aws:" + svc + ":us-east-1:123456789012:" + kind + "/" + id
}

// mpWantRow is a row a MediaPackage listing must store.
type mpWantRow struct {
	arn, name, attrs string
	tags             map[string]string
}

// mpListingCase is one MediaPackage listing: the type it stores, the SDK op
// that lists it, the op label it warns under, and the rows it must store.
type mpListingCase struct {
	typ, sdkOp, op string
	want           []mpWantRow
}

// mpListing serves a listing as two pages (ivsTwoPages, MaxResults pinned to
// the model cap 1000): page 0 holds tagged, page 1 untagged and, when noARN,
// orphan. check runs on every request.
func mpListing[E any](t *testing.T, sdkOp string, tagged, untagged, orphan E, noARN bool, check func(in any), out func([]E) any) func(in any) (any, error) {
	return ivsTwoPages(t, sdkOp, 1000, func(in any, p int) any {
		check(in)
		if p == 0 {
			return out([]E{tagged})
		}
		es := []E{untagged}
		if noARN {
			es = append(es, orphan)
		}
		return out(es)
	})
}

func mpNoFilter(any) {}

// mpFixture returns every MediaPackage listing's handler and the rows it must
// store. Each listing's tagged element carries Tags and a field beyond the
// identity; the untagged one only Arn and Id; the orphan (served when noARN)
// no Arn. A filtered origin-endpoint, asset or packaging-configuration
// listing fails the test.
func mpFixture(t *testing.T, noARN bool) ([]mpListingCase, map[string]func(in any) (any, error)) {
	t.Helper()
	ch := mptypes.Channel{
		Arn: ptrStr(mpTestARN("mediapackage", "channels", "ch-1")), Id: ptrStr("ch-1"), Description: ptrStr("live"), Tags: mpTestTags,
		HlsIngest: &mptypes.HlsIngest{IngestEndpoints: []mptypes.IngestEndpoint{{
			Id: ptrStr("ie-1"), Password: ptrStr("ingest-secret"), Url: ptrStr("https://ingest.example/in"), Username: ptrStr("ingest-user"),
		}}},
	}
	chWant := ch
	chWant.HlsIngest = &mptypes.HlsIngest{IngestEndpoints: []mptypes.IngestEndpoint{{
		Id: ptrStr("ie-1"), Password: ptrStr(redact.Placeholder), Url: ptrStr("https://ingest.example/in"), Username: ptrStr("ingest-user"),
	}}}
	ch2 := mptypes.Channel{Arn: ptrStr(mpTestARN("mediapackage", "channels", "ch-2")), Id: ptrStr("ch-2")}

	ep := mptypes.OriginEndpoint{
		Arn: ptrStr(mpTestARN("mediapackage", "origin_endpoints", "ep-1")), Id: ptrStr("ep-1"), ChannelId: ptrStr("ch-1"), Tags: mpTestTags,
	}
	ep2 := mptypes.OriginEndpoint{Arn: ptrStr(mpTestARN("mediapackage", "origin_endpoints", "ep-2")), Id: ptrStr("ep-2")}

	as := mpvodtypes.AssetShallow{
		Arn: ptrStr(mpTestARN("mediapackage-vod", "assets", "a-1")), Id: ptrStr("a-1"), PackagingGroupId: ptrStr("pg-1"), Tags: mpTestTags,
	}
	as2 := mpvodtypes.AssetShallow{Arn: ptrStr(mpTestARN("mediapackage-vod", "assets", "a-2")), Id: ptrStr("a-2")}

	pc := mpvodtypes.PackagingConfiguration{
		Arn: ptrStr(mpTestARN("mediapackage-vod", "packaging-configurations", "pc-1")), Id: ptrStr("pc-1"), PackagingGroupId: ptrStr("pg-1"), Tags: mpTestTags,
	}
	pc2 := mpvodtypes.PackagingConfiguration{Arn: ptrStr(mpTestARN("mediapackage-vod", "packaging-configurations", "pc-2")), Id: ptrStr("pc-2")}

	pg := mpvodtypes.PackagingGroup{
		Arn: ptrStr(mpTestARN("mediapackage-vod", "packaging-groups", "pg-1")), Id: ptrStr("pg-1"), DomainName: ptrStr("pg.example"), Tags: mpTestTags,
	}
	pg2 := mpvodtypes.PackagingGroup{Arn: ptrStr(mpTestARN("mediapackage-vod", "packaging-groups", "pg-2")), Id: ptrStr("pg-2")}

	cases := []mpListingCase{
		{TypeMediaPackageChannel, "ListChannels", "mediapackage:ListChannels", []mpWantRow{
			{sv(ch.Arn), "ch-1", mustJSON(chWant), mpTestTags}, {sv(ch2.Arn), "ch-2", mustJSON(ch2), nil},
		}},
		{TypeMediaPackageOriginEndpoint, "ListOriginEndpoints", "mediapackage:ListOriginEndpoints", []mpWantRow{
			{sv(ep.Arn), "ep-1", mustJSON(ep), mpTestTags}, {sv(ep2.Arn), "ep-2", mustJSON(ep2), nil},
		}},
		{TypeMediaPackageAsset, "ListAssets", "mediapackagevod:ListAssets", []mpWantRow{
			{sv(as.Arn), "a-1", mustJSON(as), mpTestTags}, {sv(as2.Arn), "a-2", mustJSON(as2), nil},
		}},
		{TypeMediaPackagePackagingConfiguration, "ListPackagingConfigurations", "mediapackagevod:ListPackagingConfigurations", []mpWantRow{
			{sv(pc.Arn), "pc-1", mustJSON(pc), mpTestTags}, {sv(pc2.Arn), "pc-2", mustJSON(pc2), nil},
		}},
		{TypeMediaPackagePackagingGroup, "ListPackagingGroups", "mediapackagevod:ListPackagingGroups", []mpWantRow{
			{sv(pg.Arn), "pg-1", mustJSON(pg), mpTestTags}, {sv(pg2.Arn), "pg-2", mustJSON(pg2), nil},
		}},
	}
	h := map[string]func(in any) (any, error){
		"ListChannels": mpListing(t, "ListChannels", ch, ch2, mptypes.Channel{Id: ptrStr("ch-orphan")}, noARN, mpNoFilter,
			func(es []mptypes.Channel) any { return &mediapackage.ListChannelsOutput{Channels: es} }),
		"ListOriginEndpoints": mpListing(t, "ListOriginEndpoints", ep, ep2, mptypes.OriginEndpoint{Id: ptrStr("ep-orphan")}, noARN,
			func(in any) {
				if c := in.(*mediapackage.ListOriginEndpointsInput).ChannelId; c != nil {
					t.Errorf("ListOriginEndpoints ChannelId = %q, want every endpoint", *c)
				}
			},
			func(es []mptypes.OriginEndpoint) any {
				return &mediapackage.ListOriginEndpointsOutput{OriginEndpoints: es}
			}),
		"ListAssets": mpListing(t, "ListAssets", as, as2, mpvodtypes.AssetShallow{Id: ptrStr("a-orphan")}, noARN,
			func(in any) {
				if g := in.(*mediapackagevod.ListAssetsInput).PackagingGroupId; g != nil {
					t.Errorf("ListAssets PackagingGroupId = %q, want every asset", *g)
				}
			},
			func(es []mpvodtypes.AssetShallow) any { return &mediapackagevod.ListAssetsOutput{Assets: es} }),
		"ListPackagingConfigurations": mpListing(t, "ListPackagingConfigurations", pc, pc2, mpvodtypes.PackagingConfiguration{Id: ptrStr("pc-orphan")}, noARN,
			func(in any) {
				if g := in.(*mediapackagevod.ListPackagingConfigurationsInput).PackagingGroupId; g != nil {
					t.Errorf("ListPackagingConfigurations PackagingGroupId = %q, want every configuration", *g)
				}
			},
			func(es []mpvodtypes.PackagingConfiguration) any {
				return &mediapackagevod.ListPackagingConfigurationsOutput{PackagingConfigurations: es}
			}),
		"ListPackagingGroups": mpListing(t, "ListPackagingGroups", pg, pg2, mpvodtypes.PackagingGroup{Id: ptrStr("pg-orphan")}, noARN, mpNoFilter,
			func(es []mpvodtypes.PackagingGroup) any {
				return &mediapackagevod.ListPackagingGroupsOutput{PackagingGroups: es}
			}),
	}
	return cases, h
}

func mpScanAll(t *testing.T, st *store.Store, h map[string]func(in any) (any, error)) (int, int, error) {
	t.Helper()
	cfg := opStubConfig(h, apiErr("ForbiddenException", "not authorized to perform this action"))
	return scanMediaPackageAll(context.Background(), mediapackage.NewFromConfig(cfg), mediapackagevod.NewFromConfig(cfg),
		newTestAccount(testAccountID), testRegion, st, testScanID)
}

// assertMPRows checks that typ's stored rows are exactly want: NativeID,
// Name, attributes (the listed element, credentials redacted) and tags (nil
// for none).
func assertMPRows(t *testing.T, st *store.Store, typ string, want []mpWantRow) {
	t.Helper()
	rows := rowsByNativeID(t, st, typ)
	if len(rows) != len(want) {
		t.Errorf("%s: stored %d rows, want %d", typ, len(rows), len(want))
	}
	for _, w := range want {
		r, ok := rows[w.arn]
		if !ok {
			t.Errorf("%s %s not stored", typ, w.arn)
			continue
		}
		if got := sv(r.Name); got != w.name {
			t.Errorf("%s %s Name = %q, want %q", typ, w.arn, got, w.name)
		}
		assertJSONEqual(t, typ+" "+w.arn+" attributes", r.AttributesJSON, w.attrs)
		if w.tags == nil {
			if r.TagsJSON != nil {
				t.Errorf("%s %s TagsJSON = %q, want nil", typ, w.arn, *r.TagsJSON)
			}
			continue
		}
		assertTagsMap(t, r.TagsJSON, w.tags)
	}
}

// TestScanMediaPackageAll_StoresRows pins, per type, the rows read from both
// pages of its listing: NativeID (the Arn), Name (the Id), the element as
// attributes with the channel ingest password redacted, and the element's
// Tags, nil when it has none.
func TestScanMediaPackageAll_StoresRows(t *testing.T) {
	cases, h := mpFixture(t, false)
	st := newTestStore(t)
	total, inserted, err := mpScanAll(t, st, h)
	if err != nil {
		t.Fatalf("scanMediaPackageAll: %v", err)
	}
	if total != 10 || inserted != 10 {
		t.Errorf("total, inserted = %d, %d, want 10, 10", total, inserted)
	}
	for _, c := range cases {
		assertMPRows(t, st, c.typ, c.want)
	}
}

// TestScanMediaPackageAll_EmptyARNWarnsOncePerOp pins that every listing
// warns once for its elements with no Arn and stores the others.
func TestScanMediaPackageAll_EmptyARNWarnsOncePerOp(t *testing.T) {
	cases, h := mpFixture(t, true)
	st := newTestStore(t)
	warns := map[string]*int{}
	for _, c := range cases {
		warns[c.op] = countOpWarnings(st, c.op)
	}
	total, _, err := mpScanAll(t, st, h)
	if err != nil {
		t.Fatalf("scanMediaPackageAll: %v", err)
	}
	if total != 10 {
		t.Errorf("total = %d, want 10", total)
	}
	for _, c := range cases {
		if *warns[c.op] != 1 {
			t.Errorf("%s warnings = %d, want 1", c.op, *warns[c.op])
		}
		assertMPRows(t, st, c.typ, c.want)
	}
}

// TestScanMPPackagingGroups_ListOutcomes pins the listing outcomes every
// MediaPackage phase shares through storeTokenList: rows read before a
// failed page are kept and the error is returned; a repeated token ends the
// listing with errRepeatedNextToken; a denial is warned about, not returned,
// and keeps the rows read before it.
func TestScanMPPackagingGroups_ListOutcomes(t *testing.T) {
	bad := apiErr("UnprocessableEntityException", "bad")
	denied := apiErr("ForbiddenException", "not authorized to perform: mediapackage-vod:ListPackagingGroups")
	page := func(next string, ids ...string) func() (any, error) {
		return func() (any, error) {
			out := &mediapackagevod.ListPackagingGroupsOutput{}
			if next != "" {
				out.NextToken = ptrStr(next)
			}
			for _, id := range ids {
				out.PackagingGroups = append(out.PackagingGroups, mpvodtypes.PackagingGroup{Arn: ptrStr(mpTestARN("mediapackage-vod", "packaging-groups", id)), Id: ptrStr(id)})
			}
			return out, nil
		}
	}
	fail := func(err error) func() (any, error) { return func() (any, error) { return nil, err } }
	for _, c := range []struct {
		name      string
		pages     []func() (any, error)
		wantErr   error
		wantIDs   []string
		wantWarns int
	}{
		{"later page fails keeps rows read", []func() (any, error){page("t1", "pg-1"), fail(bad)}, bad, []string{"pg-1"}, 0},
		{"repeated token keeps rows read", []func() (any, error){page("t1", "pg-1"), page("t1", "pg-2")}, errRepeatedNextToken, []string{"pg-1", "pg-2"}, 0},
		{"denied warns", []func() (any, error){fail(denied)}, nil, nil, 1},
		{"denied after a page keeps rows and warns", []func() (any, error){page("t1", "pg-1"), fail(denied)}, nil, []string{"pg-1"}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "mediapackagevod:ListPackagingGroups")
			var call int
			h := map[string]func(in any) (any, error){
				"ListPackagingGroups": func(any) (any, error) {
					if call >= len(c.pages) {
						t.Fatalf("ListPackagingGroups called %d times, want %d", call+1, len(c.pages))
					}
					call++
					return c.pages[call-1]()
				},
			}
			client := mediapackagevod.NewFromConfig(opStubConfig(h, errors.New("unexpected op")))
			total, _, err := scanMPPackagingGroups(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if c.wantErr == nil && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if total != len(c.wantIDs) {
				t.Errorf("total = %d, want %d", total, len(c.wantIDs))
			}
			var want []mpWantRow
			for _, id := range c.wantIDs {
				g := mpvodtypes.PackagingGroup{Arn: ptrStr(mpTestARN("mediapackage-vod", "packaging-groups", id)), Id: ptrStr(id)}
				want = append(want, mpWantRow{sv(g.Arn), id, mustJSON(g), nil})
			}
			assertMPRows(t, st, TypeMediaPackagePackagingGroup, want)
			if *warns != c.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, c.wantWarns)
			}
		})
	}
}

// TestScanMediaPackageAll_PhaseErrorRunsOtherPhases pins that a failed
// listing stops no other: its error is returned and every other listing's
// rows are stored.
func TestScanMediaPackageAll_PhaseErrorRunsOtherPhases(t *testing.T) {
	bad := apiErr("UnprocessableEntityException", "bad")
	cases, _ := mpFixture(t, false)
	for _, failed := range cases {
		t.Run(failed.sdkOp, func(t *testing.T) {
			_, h := mpFixture(t, false)
			h[failed.sdkOp] = func(any) (any, error) { return nil, bad }
			st := newTestStore(t)
			total, _, err := mpScanAll(t, st, h)
			if !errors.Is(err, bad) {
				t.Fatalf("err = %v, want the %s error", err, failed.sdkOp)
			}
			if total != 8 {
				t.Errorf("total = %d, want 8", total)
			}
			for _, c := range cases {
				if c.typ == failed.typ {
					assertMPRows(t, st, c.typ, nil)
					continue
				}
				assertMPRows(t, st, c.typ, c.want)
			}
		})
	}
}

// TestScanMediaPackageAll_StoreWriteStops pins that a failed write stops the
// scan: no listing after it calls AWS.
func TestScanMediaPackageAll_StoreWriteStops(t *testing.T) {
	calls := map[string]int{}
	_, h := mpFixture(t, false)
	for op, f := range h {
		h[op] = func(in any) (any, error) {
			calls[op]++
			return f(in)
		}
	}
	st, closeStore := openClosingStore(t)
	serve := h["ListOriginEndpoints"]
	h["ListOriginEndpoints"] = func(in any) (any, error) {
		closeStore()
		return serve(in)
	}
	if _, _, err := mpScanAll(t, st, h); !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want ErrStoreWrite", err)
	}
	for _, op := range []string{"ListAssets", "ListPackagingConfigurations", "ListPackagingGroups"} {
		if calls[op] != 0 {
			t.Errorf("%s called %d times after the failed write, want 0", op, calls[op])
		}
	}
}

// TestScanMediaPackageAll_TransientBesideHardError pins that a throttled
// listing beside a hard-failing one is demoted to one warning under the
// throttled listing's own op, and the scan classified as an error.
func TestScanMediaPackageAll_TransientBesideHardError(t *testing.T) {
	cases, _ := mpFixture(t, false)
	for _, c := range cases {
		t.Run(c.sdkOp, func(t *testing.T) {
			hardOp := "ListPackagingGroups"
			if c.sdkOp == hardOp {
				hardOp = "ListChannels"
			}
			_, h := mpFixture(t, false)
			h[c.sdkOp] = func(any) (any, error) { return nil, apiErr("TooManyRequestsException", "slow down") }
			h[hardOp] = func(any) (any, error) { return nil, apiErr("UnprocessableEntityException", "bad") }
			st := newTestStore(t)
			var labels []string
			st.OnWarn = func(w store.ScanWarning) { labels = append(labels, w.Service) }
			_, _, err := mpScanAll(t, st, h)
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			if len(labels) != 1 || labels[0] != c.op {
				t.Errorf("warnings under %v, want exactly one under %s", labels, c.op)
			}
		})
	}
}

// TestScanMediaPackageAll_DeniedEveryOpWarnsWithRegion pins that a role
// denied every listing gets one warning per op, scoped to the account and
// region, and no error.
func TestScanMediaPackageAll_DeniedEveryOpWarnsWithRegion(t *testing.T) {
	cases, _ := mpFixture(t, false)
	st := newTestStore(t)
	var scopes []string
	st.OnWarn = func(w store.ScanWarning) { scopes = append(scopes, w.Service+" "+w.Scope) }
	if _, _, err := mpScanAll(t, st, nil); err != nil {
		t.Fatalf("scanMediaPackageAll: %v", err)
	}
	if len(scopes) != len(cases) {
		t.Errorf("warnings = %v, want one per op (%d)", scopes, len(cases))
	}
	for _, c := range cases {
		want := c.op + " " + testAccountID + "/" + testRegion
		found := false
		for _, s := range scopes {
			found = found || s == want
		}
		if !found {
			t.Errorf("no warning %q in %v", want, scopes)
		}
	}
}
