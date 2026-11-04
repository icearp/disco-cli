package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dataexchange"
	dxtypes "github.com/aws/aws-sdk-go-v2/service/dataexchange/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/store"
)

const (
	// adxtTagsOp is the tag op's warning label, spelled out so a typo in the
	// production label fails here.
	adxtTagsOp = "dataexchange:ListTagsForResource"
	// adxtSender is the account that sent this account its received grants.
	adxtSender = "111122223333"
	// adxtTotal is every row of the fixture: two data sets, data grants,
	// event actions and received grants (one per page); ds1's revisions r1
	// and r2 (one per page) and ds2's r1; r1's assets a1 and a2 (one per
	// page) and r2's a1.
	adxtTotal = 2 + 2 + 2 + 2 + 3 + 3
)

var (
	adxtDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: dataexchange:List")
	adxtBad      = apiErr("ValidationException", "bad")
	adxtGone     = apiErr("ResourceNotFoundException", "not found")
	adxtThrottle = apiErr("ThrottlingException", "Rate exceeded")
	adxtCreated  = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
)

func adxtDataSetARN(ds string) string {
	return fmt.Sprintf("arn:aws:dataexchange:%s:%s:data-sets/%s", testRegion, testAccountID, ds)
}

func adxtRevisionARN(ds, rev string) string { return adxtDataSetARN(ds) + "/revisions/" + rev }

func adxtAssetARN(ds, rev, asset string) string {
	return adxtRevisionARN(ds, rev) + "/assets/" + asset
}

func adxtGrantARN(acctID, id string) string {
	return fmt.Sprintf("arn:aws:dataexchange:%s:%s:data-grants/%s", testRegion, acctID, id)
}

func adxtEventActionARN(id string) string {
	return fmt.Sprintf("arn:aws:dataexchange:%s:%s:event-actions/%s", testRegion, testAccountID, id)
}

func adxtDataSet(ds string, at dxtypes.AssetType) dxtypes.DataSetEntry {
	return dxtypes.DataSetEntry{
		Arn: ptrStr(adxtDataSetARN(ds)), Id: ptrStr(ds), Name: ptrStr("set-" + ds), AssetType: at,
		Origin: dxtypes.OriginOwned, CreatedAt: &adxtCreated, UpdatedAt: &adxtCreated,
	}
}

func adxtGrant(id string, state dxtypes.DataGrantAcceptanceState) dxtypes.DataGrantSummaryEntry {
	return dxtypes.DataGrantSummaryEntry{
		Arn: ptrStr(adxtGrantARN(testAccountID, id)), Id: ptrStr(id), Name: ptrStr("grant-" + id),
		AcceptanceState: state, SourceDataSetId: ptrStr("ds1"), CreatedAt: &adxtCreated, UpdatedAt: &adxtCreated,
	}
}

func adxtEventAction(id string) dxtypes.EventActionEntry {
	return dxtypes.EventActionEntry{Arn: ptrStr(adxtEventActionARN(id)), Id: ptrStr(id), CreatedAt: &adxtCreated, UpdatedAt: &adxtCreated}
}

// adxtReceived is a grant sender sent this account.
func adxtReceived(sender, id string) dxtypes.ReceivedDataGrantSummariesEntry {
	return dxtypes.ReceivedDataGrantSummariesEntry{
		Arn: ptrStr(adxtGrantARN(sender, id)), Id: ptrStr(id), Name: ptrStr("grant-" + id), DataSetId: ptrStr("entitled-ds"),
		AcceptanceState: dxtypes.DataGrantAcceptanceStateAccepted, CreatedAt: &adxtCreated, UpdatedAt: &adxtCreated,
		SenderPrincipal: ptrStr(sender), ReceiverPrincipal: ptrStr(testAccountID),
	}
}

func adxtRevision(ds, rev string) dxtypes.RevisionEntry {
	return dxtypes.RevisionEntry{
		Arn: ptrStr(adxtRevisionARN(ds, rev)), Id: ptrStr(rev), DataSetId: ptrStr(ds),
		CreatedAt: &adxtCreated, UpdatedAt: &adxtCreated, Finalized: true,
	}
}

func adxtAsset(ds, rev, asset string) dxtypes.AssetEntry {
	return dxtypes.AssetEntry{
		Arn: ptrStr(adxtAssetARN(ds, rev, asset)), Id: ptrStr(asset), Name: ptrStr("share-" + asset),
		DataSetId: ptrStr(ds), RevisionId: ptrStr(rev), AssetType: dxtypes.AssetTypeRedshiftDataShare,
		AssetDetails: &dxtypes.AssetDetails{RedshiftDataShareAsset: &dxtypes.RedshiftDataShareAsset{
			Arn: ptrStr("arn:aws:redshift:us-east-1:123456789012:datashare:ns/" + asset),
		}},
		CreatedAt: &adxtCreated, UpdatedAt: &adxtCreated,
	}
}

// adxtCalls records the calls of a scan and answers the tag reads, by ARN,
// with tags (none for an ARN it does not hold) or errs.
type adxtCalls struct {
	mu      sync.Mutex
	counts  map[string]int
	limits  map[string][]int32
	parents map[string][]string
	tagARNs []string
	origins []string
	states  []string
	tags    map[string]map[string]string
	errs    map[string]error
}

// newADXTCalls tags the first row of every listing with its type.
func newADXTCalls() *adxtCalls {
	c := &adxtCalls{counts: map[string]int{}, limits: map[string][]int32{}, parents: map[string][]string{}, errs: map[string]error{}}
	c.tags = map[string]map[string]string{
		adxtDataSetARN("ds1"):                {"type": TypeDataExchangeDataSets},
		adxtGrantARN(testAccountID, "g1"):    {"type": TypeDataExchangeDataGrants},
		adxtEventActionARN("ea1"):            {"type": TypeDataExchangeEventActions},
		adxtGrantARN(adxtSender, "rg1"):      {"type": TypeDataExchangeReceivedDataGrant},
		adxtRevisionARN("ds1", "r1"):         {"type": TypeDataExchangeDataSetRevision},
		adxtAssetARN("ds1", "r1", "a1"):      {"type": TypeDataExchangeRevisionAsset},
		adxtGrantARN(testAccountID, "rself"): {"type": TypeDataExchangeReceivedDataGrant},
	}
	return c
}

// record notes op's call, its MaxResults, and on a first page the parent it
// lists (a data set, or data set/revision).
func (c *adxtCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[op]++
	switch v := reflect.ValueOf(in).Elem().FieldByName("MaxResults"); {
	case !v.IsValid():
	case v.Kind() == reflect.Int32:
		c.limits[op] = append(c.limits[op], int32(v.Int()))
	case !v.IsNil():
		c.limits[op] = append(c.limits[op], int32(v.Elem().Int()))
	default:
		c.limits[op] = append(c.limits[op], 0)
	}
	switch req := in.(type) {
	case *dataexchange.ListDataSetsInput:
		c.origins = append(c.origins, sv(req.Origin))
	case *dataexchange.ListReceivedDataGrantsInput:
		var states []string
		for _, s := range req.AcceptanceState {
			states = append(states, string(s))
		}
		slices.Sort(states)
		c.states = append(c.states, strings.Join(states, ","))
	case *dataexchange.ListDataSetRevisionsInput:
		if req.NextToken == nil {
			c.parents[op] = append(c.parents[op], sv(req.DataSetId))
		}
	case *dataexchange.ListRevisionAssetsInput:
		if req.NextToken == nil {
			c.parents[op] = append(c.parents[op], sv(req.DataSetId)+"/"+sv(req.RevisionId))
		}
	}
}

func (c *adxtCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[op]
}

func (c *adxtCalls) listedParents(op string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.parents[op]))
}

func (c *adxtCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *adxtCalls) listTags(in any) (any, error) {
	arn := sv(in.(*dataexchange.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts["ListTagsForResource"]++
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	return &dataexchange.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
}

// adxtTwoPages answers op with first on its first page and second on its
// second.
func adxtTwoPages[Out any](c *adxtCalls, op string, first, second func(in any) Out) func(in any) (any, error) {
	return func(in any) (any, error) {
		c.record(op, in)
		if reflect.ValueOf(in).Elem().FieldByName("NextToken").IsNil() {
			return first(in), nil
		}
		return second(in), nil
	}
}

// adxtHandlers answers every op the scan calls with the fixture adxtTotal
// counts; every revision and asset listing of ds1 and r1 has two pages.
func adxtHandlers(c *adxtCalls) map[string]func(in any) (any, error) {
	p2 := ptrStr("p2")
	return map[string]func(in any) (any, error){
		"ListDataSets": adxtTwoPages(c, "ListDataSets",
			func(any) *dataexchange.ListDataSetsOutput {
				return &dataexchange.ListDataSetsOutput{DataSets: []dxtypes.DataSetEntry{adxtDataSet("ds1", dxtypes.AssetTypeRedshiftDataShare)}, NextToken: p2}
			},
			func(any) *dataexchange.ListDataSetsOutput {
				return &dataexchange.ListDataSetsOutput{DataSets: []dxtypes.DataSetEntry{adxtDataSet("ds2", dxtypes.AssetTypeS3Snapshot)}}
			}),
		"ListDataGrants": adxtTwoPages(c, "ListDataGrants",
			func(any) *dataexchange.ListDataGrantsOutput {
				return &dataexchange.ListDataGrantsOutput{DataGrantSummaries: []dxtypes.DataGrantSummaryEntry{adxtGrant("g1", dxtypes.DataGrantAcceptanceStateAccepted)}, NextToken: p2}
			},
			func(any) *dataexchange.ListDataGrantsOutput {
				return &dataexchange.ListDataGrantsOutput{DataGrantSummaries: []dxtypes.DataGrantSummaryEntry{adxtGrant("g2", dxtypes.DataGrantAcceptanceStatePendingReceiverAcceptance)}}
			}),
		"ListEventActions": adxtTwoPages(c, "ListEventActions",
			func(any) *dataexchange.ListEventActionsOutput {
				return &dataexchange.ListEventActionsOutput{EventActions: []dxtypes.EventActionEntry{adxtEventAction("ea1")}, NextToken: p2}
			},
			func(any) *dataexchange.ListEventActionsOutput {
				return &dataexchange.ListEventActionsOutput{EventActions: []dxtypes.EventActionEntry{adxtEventAction("ea2")}}
			}),
		"ListReceivedDataGrants": adxtTwoPages(c, "ListReceivedDataGrants",
			func(any) *dataexchange.ListReceivedDataGrantsOutput {
				return &dataexchange.ListReceivedDataGrantsOutput{DataGrantSummaries: []dxtypes.ReceivedDataGrantSummariesEntry{adxtReceived(adxtSender, "rg1")}, NextToken: p2}
			},
			func(any) *dataexchange.ListReceivedDataGrantsOutput {
				return &dataexchange.ListReceivedDataGrantsOutput{DataGrantSummaries: []dxtypes.ReceivedDataGrantSummariesEntry{adxtReceived(adxtSender, "rg2")}}
			}),
		"ListDataSetRevisions": adxtTwoPages(c, "ListDataSetRevisions",
			func(in any) *dataexchange.ListDataSetRevisionsOutput {
				ds := sv(in.(*dataexchange.ListDataSetRevisionsInput).DataSetId)
				if ds != "ds1" {
					draft := adxtRevision(ds, "r1")
					draft.Finalized = false
					return &dataexchange.ListDataSetRevisionsOutput{Revisions: []dxtypes.RevisionEntry{draft}}
				}
				return &dataexchange.ListDataSetRevisionsOutput{Revisions: []dxtypes.RevisionEntry{adxtRevision(ds, "r1")}, NextToken: p2}
			},
			func(in any) *dataexchange.ListDataSetRevisionsOutput {
				revoked := adxtRevision(sv(in.(*dataexchange.ListDataSetRevisionsInput).DataSetId), "r2")
				revoked.Revoked = true
				return &dataexchange.ListDataSetRevisionsOutput{Revisions: []dxtypes.RevisionEntry{revoked}}
			}),
		"ListRevisionAssets": adxtTwoPages(c, "ListRevisionAssets",
			func(in any) *dataexchange.ListRevisionAssetsOutput {
				req := in.(*dataexchange.ListRevisionAssetsInput)
				ds, rev := sv(req.DataSetId), sv(req.RevisionId)
				out := &dataexchange.ListRevisionAssetsOutput{Assets: []dxtypes.AssetEntry{adxtAsset(ds, rev, "a1")}}
				if rev == "r1" {
					out.NextToken = p2
				}
				return out
			},
			func(in any) *dataexchange.ListRevisionAssetsOutput {
				req := in.(*dataexchange.ListRevisionAssetsInput)
				return &dataexchange.ListRevisionAssetsOutput{Assets: []dxtypes.AssetEntry{adxtAsset(sv(req.DataSetId), sv(req.RevisionId), "a2")}}
			}),
		"ListTagsForResource": c.listTags,
	}
}

// adxtScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func adxtScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, adxtDenied)
	return scanDataExchange(ctx, acct, testRegion, st, testScanID)
}

func adxtWarnings(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// adxtWant is one stored row of the fixture; tagged rows carry their type
// as their one tag.
type adxtWant struct {
	typ, nativeID, name, status, created, arn string
	tagged                                    bool
}

func adxtWantRows() []adxtWant {
	created := "2026-09-01T08:00:00Z"
	return []adxtWant{
		{TypeDataExchangeDataSets, adxtDataSetARN("ds1"), "set-ds1", "", "", adxtDataSetARN("ds1"), true},
		{TypeDataExchangeDataSets, adxtDataSetARN("ds2"), "set-ds2", "", "", adxtDataSetARN("ds2"), false},
		{TypeDataExchangeDataGrants, adxtGrantARN(testAccountID, "g1"), "grant-g1", "ACCEPTED", "", adxtGrantARN(testAccountID, "g1"), true},
		{TypeDataExchangeDataGrants, adxtGrantARN(testAccountID, "g2"), "grant-g2", "PENDING_RECEIVER_ACCEPTANCE", "", adxtGrantARN(testAccountID, "g2"), false},
		{TypeDataExchangeEventActions, adxtEventActionARN("ea1"), "ea1", "", "", adxtEventActionARN("ea1"), true},
		{TypeDataExchangeEventActions, adxtEventActionARN("ea2"), "ea2", "", "", adxtEventActionARN("ea2"), false},
		{TypeDataExchangeReceivedDataGrant, adxtGrantARN(adxtSender, "rg1") + "/received", "grant-rg1", "ACCEPTED", created, adxtGrantARN(adxtSender, "rg1"), true},
		{TypeDataExchangeReceivedDataGrant, adxtGrantARN(adxtSender, "rg2") + "/received", "grant-rg2", "ACCEPTED", created, adxtGrantARN(adxtSender, "rg2"), false},
		{TypeDataExchangeDataSetRevision, adxtRevisionARN("ds1", "r1"), "r1", "FINALIZED", created, adxtRevisionARN("ds1", "r1"), true},
		{TypeDataExchangeDataSetRevision, adxtRevisionARN("ds1", "r2"), "r2", "REVOKED", created, adxtRevisionARN("ds1", "r2"), false},
		{TypeDataExchangeDataSetRevision, adxtRevisionARN("ds2", "r1"), "r1", "DRAFT", created, adxtRevisionARN("ds2", "r1"), false},
		{TypeDataExchangeRevisionAsset, adxtAssetARN("ds1", "r1", "a1"), "share-a1", "", created, adxtAssetARN("ds1", "r1", "a1"), true},
		{TypeDataExchangeRevisionAsset, adxtAssetARN("ds1", "r1", "a2"), "share-a2", "", created, adxtAssetARN("ds1", "r1", "a2"), false},
		{TypeDataExchangeRevisionAsset, adxtAssetARN("ds1", "r2", "a1"), "share-a1", "", created, adxtAssetARN("ds1", "r2", "a1"), false},
	}
}

// Every listing stores both its pages, each row under its ARN (a received
// grant's with /received appended), with its Name, Status and CreatedAt, its
// attributes the listed element, and the tags its tag read returns (nil for
// none). ListTagsForResource is sent exactly the listed ARN of every row,
// one call per row. Every listing is sent the model's MaxResults cap of 200;
// the data sets are listed OWNED only and the received grants in every
// acceptance state. Revisions are listed per data set and assets only for
// the revisions of the share data set, each row under its parent. No error,
// no warning.
func TestScanDataExchange_StoresTagsOfEveryType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newADXTCalls()

	total, inserted, err := adxtScan(context.Background(), st, adxtHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != adxtTotal || inserted != adxtTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, adxtTotal, adxtTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	perType := map[string]int{}
	var wantTagARNs []string
	for _, w := range adxtWantRows() {
		perType[w.typ]++
		wantTagARNs = append(wantTagARNs, w.arn)
		r, ok := rowsByNativeID(t, st, w.typ)[w.nativeID]
		if !ok {
			t.Errorf("%s %s missing", w.typ, w.nativeID)
			continue
		}
		if sv(r.Name) != w.name || sv(r.Status) != w.status || sv(r.CreatedAt) != w.created || sv(r.Region) != testRegion {
			t.Errorf("%s Name, Status, CreatedAt, Region = %q, %q, %q, %q; want %q, %q, %q, %q",
				w.nativeID, sv(r.Name), sv(r.Status), sv(r.CreatedAt), sv(r.Region), w.name, w.status, w.created, testRegion)
		}
		if (w.status == "") != (r.Status == nil) || (w.created == "") != (r.CreatedAt == nil) {
			t.Errorf("%s Status, CreatedAt = %v, %v; want set only when non-empty", w.nativeID, r.Status, r.CreatedAt)
		}
		var attrs struct{ Arn string }
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Arn != w.arn {
			t.Errorf("%s attrs = %s (%v); want the listed element", w.nativeID, r.AttributesJSON, err)
		}
		if w.tagged {
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": w.typ})
		} else if r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %q; want nil", w.nativeID, *r.TagsJSON)
		}
	}
	for typ, n := range perType {
		if got := len(rowsByNativeID(t, st, typ)); got != n {
			t.Errorf("%s rows = %d; want %d", typ, got, n)
		}
	}
	slices.Sort(wantTagARNs)
	if got := calls.sortedTagARNs(); !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, wantTagARNs)
	}
	for _, op := range []string{"ListDataSets", "ListDataGrants", "ListEventActions", "ListReceivedDataGrants", "ListDataSetRevisions", "ListRevisionAssets"} {
		if calls.count(op) == 0 {
			t.Errorf("%s not called", op)
		}
		for _, n := range calls.limits[op] {
			if n != 200 {
				t.Errorf("%s MaxResults = %d; want 200", op, n)
			}
		}
	}
	if !slices.Equal(calls.origins, []string{"OWNED", "OWNED"}) {
		t.Errorf("ListDataSets Origin = %v; want OWNED on both pages", calls.origins)
	}
	if want := "ACCEPTED,PENDING_RECEIVER_ACCEPTANCE"; !slices.Equal(calls.states, []string{want, want}) {
		t.Errorf("ListReceivedDataGrants AcceptanceState = %v; want both states on both pages", calls.states)
	}
	if got := calls.listedParents("ListDataSetRevisions"); !slices.Equal(got, []string{"ds1", "ds2"}) {
		t.Errorf("revisions listed for %v; want [ds1 ds2]", got)
	}
	if got := calls.listedParents("ListRevisionAssets"); !slices.Equal(got, []string{"ds1/r1", "ds1/r2"}) {
		t.Errorf("assets listed for %v; want [ds1/r1 ds1/r2]", got)
	}
	assertContainsNative(t, st, adxtDataSetARN("ds1"), adxtRevisionARN("ds1", "r1"))
	assertContainsNative(t, st, adxtDataSetARN("ds1"), adxtRevisionARN("ds1", "r2"))
	assertContainsNative(t, st, adxtDataSetARN("ds2"), adxtRevisionARN("ds2", "r1"))
	assertContainsNative(t, st, adxtRevisionARN("ds1", "r1"), adxtAssetARN("ds1", "r1", "a1"))
	assertContainsNative(t, st, adxtRevisionARN("ds1", "r1"), adxtAssetARN("ds1", "r1", "a2"))
	assertContainsNative(t, st, adxtRevisionARN("ds1", "r2"), adxtAssetARN("ds1", "r2", "a1"))
}

// A rescan stores the same rows without inserting any.
func TestScanDataExchange_RescanInsertsNone(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, _, err := adxtScan(context.Background(), st, adxtHandlers(newADXTCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := adxtScan(context.Background(), st, adxtHandlers(newADXTCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != adxtTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, adxtTotal)
	}
}

// Every listing empty: nothing stored, no child listing or tag read, no
// error, no warning.
func TestScanDataExchange_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newADXTCalls()
	handlers := map[string]func(in any) (any, error){"ListTagsForResource": calls.listTags}
	for op, out := range map[string]any{
		"ListDataSets": &dataexchange.ListDataSetsOutput{}, "ListDataGrants": &dataexchange.ListDataGrantsOutput{},
		"ListEventActions": &dataexchange.ListEventActionsOutput{}, "ListReceivedDataGrants": &dataexchange.ListReceivedDataGrantsOutput{},
	} {
		handlers[op] = func(in any) (any, error) {
			calls.record(op, in)
			return out, nil
		}
	}

	total, inserted, err := adxtScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListDataSets", "ListDataGrants", "ListEventActions", "ListReceivedDataGrants"} {
		if calls.count(op) != 1 {
			t.Errorf("%s called %d times; want 1", op, calls.count(op))
		}
	}
	for _, op := range []string{"ListDataSetRevisions", "ListRevisionAssets", "ListTagsForResource"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
}

// A failed tag read keeps the row untagged. ListTagsForResource models no
// error, so a resource reported not found is kept and the error returned. A
// denial warns once under the tag op with the scan's scope; a throttle is
// transient; any other error is an error. The row's children are still
// listed.
func TestScanDataExchange_TagReadFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		err     error
		outcome serviceOutcome // -1: no error
		code    string
		warn    bool
	}{
		{"not found", adxtGone, outcomeError, "ResourceNotFoundException", false},
		{"denied", adxtDenied, -1, "", true},
		{"transient", adxtThrottle, outcomeTransient, "ThrottlingException", false},
		{"other error", adxtBad, outcomeError, "ValidationException", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newADXTCalls()
			calls.errs[adxtDataSetARN("ds1")] = c.err

			total, _, err := adxtScan(context.Background(), st, adxtHandlers(calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if total != adxtTotal {
				t.Errorf("total = %d; want %d", total, adxtTotal)
			}
			r, ok := rowsByNativeID(t, st, TypeDataExchangeDataSets)[adxtDataSetARN("ds1")]
			if !ok {
				t.Fatal("ds1 not stored")
			}
			if r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			if got := calls.listedParents("ListDataSetRevisions"); !slices.Equal(got, []string{"ds1", "ds2"}) {
				t.Errorf("revisions listed for %v; want [ds1 ds2]", got)
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{adxtTagsOp}
			}
			if got := adxtWarnings(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// Denied tag reads in several phases send one warning for the whole scan;
// the rows are kept.
func TestScanDataExchange_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newADXTCalls()
	for _, arn := range []string{adxtGrantARN(testAccountID, "g2"), adxtEventActionARN("ea1"), adxtAssetARN("ds1", "r2", "a1")} {
		calls.errs[arn] = adxtDenied
	}

	total, _, err := adxtScan(context.Background(), st, adxtHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != adxtTotal {
		t.Errorf("total = %d; want %d", total, adxtTotal)
	}
	if got := adxtWarnings(warns()); !slices.Equal(got, []string{adxtTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, adxtTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanDataExchange_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, adxtTagsOp)
	calls := newADXTCalls()
	calls.errs[adxtEventActionARN("ea2")] = adxtThrottle
	calls.errs[adxtRevisionARN("ds2", "r1")] = adxtBad

	_, _, err := adxtScan(context.Background(), st, adxtHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", adxtTagsOp, *warns)
	}
}

// A received grant's tag read is sent the grant's own ARN. One another
// account sent whose read is denied is kept untagged without a warning or an
// error; one this account sent itself is read as its own, so its denial
// warns.
func TestScanDataExchange_ReceivedGrantTagReads(t *testing.T) {
	t.Parallel()
	selfNative := adxtGrantARN(testAccountID, "rself") + "/received"
	for _, c := range []struct {
		name     string
		denied   string
		wantWarn []string
	}{
		{"read", "", nil},
		{"sender's grant denied", adxtGrantARN(adxtSender, "rg1"), nil},
		{"own grant denied", adxtGrantARN(testAccountID, "rself"), []string{adxtTagsOp}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newADXTCalls()
			if c.denied != "" {
				calls.errs[c.denied] = adxtDenied
			}
			handlers := adxtHandlers(calls)
			handlers["ListReceivedDataGrants"] = func(in any) (any, error) {
				return &dataexchange.ListReceivedDataGrantsOutput{DataGrantSummaries: []dxtypes.ReceivedDataGrantSummariesEntry{
					adxtReceived(adxtSender, "rg1"), adxtReceived(testAccountID, "rself"),
				}}, nil
			}

			if _, _, err := adxtScan(context.Background(), st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			for _, arn := range []string{adxtGrantARN(adxtSender, "rg1"), adxtGrantARN(testAccountID, "rself")} {
				if !slices.Contains(calls.sortedTagARNs(), arn) {
					t.Errorf("ListTagsForResource ARNs lack %s", arn)
				}
			}
			rows := rowsByNativeID(t, st, TypeDataExchangeReceivedDataGrant)
			for native, arn := range map[string]string{adxtGrantARN(adxtSender, "rg1") + "/received": adxtGrantARN(adxtSender, "rg1"), selfNative: adxtGrantARN(testAccountID, "rself")} {
				r, ok := rows[native]
				switch {
				case !ok:
					t.Errorf("%s not stored", native)
				case arn == c.denied && r.TagsJSON != nil:
					t.Errorf("%s TagsJSON = %q; want nil", native, *r.TagsJSON)
				case arn != c.denied:
					assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeDataExchangeReceivedDataGrant})
				}
			}
			if got := adxtWarnings(warns()); !slices.Equal(got, c.wantWarn) {
				t.Errorf("warnings = %v; want %v", got, c.wantWarn)
			}
		})
	}
}

// A failed write in any phase stops the scan: no warning about the rows it
// lost (a denied tag read among them), no later phase runs, and the earlier
// phases' rows stay stored. A failed closure write stops it alike.
func TestScanDataExchange_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	// The phases in their order, each with the first row its listing
	// returns; total is the rows of the phases before it.
	phases := []struct {
		op, typ, denied string
		total           int
	}{
		{"ListDataSets", TypeDataExchangeDataSets, adxtDataSetARN("ds1"), 0},
		{"ListDataGrants", TypeDataExchangeDataGrants, adxtGrantARN(testAccountID, "g1"), 2},
		{"ListEventActions", TypeDataExchangeEventActions, adxtEventActionARN("ea1"), 4},
		{"ListReceivedDataGrants", TypeDataExchangeReceivedDataGrant, adxtGrantARN(adxtSender, "rg1"), 6},
		{"ListDataSetRevisions", TypeDataExchangeDataSetRevision, adxtRevisionARN("ds1", "r1"), 8},
		{"ListRevisionAssets", TypeDataExchangeRevisionAsset, adxtAssetARN("ds1", "r1", "a1"), 11},
	}
	for i, c := range phases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			refuseStoreWrites(t, st, c.typ)
			calls := newADXTCalls()
			calls.errs[c.denied] = adxtDenied

			total, _, err := adxtScan(context.Background(), st, adxtHandlers(calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			for _, later := range phases[i+1:] {
				if n := calls.count(later.op); n != 0 {
					t.Errorf("%s called %d times; want 0 after the failed write", later.op, n)
				}
			}
			if stored := countStoredRows(t, st); total != c.total || stored != c.total {
				t.Errorf("total, stored rows = %d, %d; want %d, %d", total, stored, c.total, c.total)
			}
		})
	}
	t.Run("closure", func(t *testing.T) {
		st := newTestStore(t)
		refuseClosureWrites(t, st)
		calls := newADXTCalls()

		_, _, err := adxtScan(context.Background(), st, adxtHandlers(calls))
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v; want store.ErrStoreWrite", err)
		}
		if n := calls.count("ListRevisionAssets"); n != 0 {
			t.Errorf("ListRevisionAssets called %d times after the failed closure write", n)
		}
		if n := len(rowsByNativeID(t, st, TypeDataExchangeDataSetRevision)); n != 3 {
			t.Errorf("revision rows = %d; want the 3 written before the closure write", n)
		}
	})
}

// A failed write of a later page in a per-parent phase keeps the contains
// edge of every row written before it: ds1's r2 is refused after its r1,
// and r1's a2 after its a1.
func TestScanDataExchange_LaterPageWriteFailureKeepsEdges(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, refused, parent, written string
	}{
		{"revisions", adxtRevisionARN("ds1", "r2"), adxtDataSetARN("ds1"), adxtRevisionARN("ds1", "r1")},
		{"assets", adxtAssetARN("ds1", "r1", "a2"), adxtRevisionARN("ds1", "r1"), adxtAssetARN("ds1", "r1", "a1")},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			if _, err := st.DB().Exec(`CREATE TRIGGER refuse_row BEFORE INSERT ON resources
				WHEN NEW.native_id = '` + c.refused + `'
				BEGIN SELECT RAISE(ABORT, 'test: write refused'); END`); err != nil {
				t.Fatalf("create trigger: %v", err)
			}

			_, _, err := adxtScan(context.Background(), st, adxtHandlers(newADXTCalls()))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			assertContainsNative(t, st, c.parent, c.written)
		})
	}
}

// A listing's failed later page keeps the pages read before it, and its
// error does not stop the later phases; a denied later page warns instead.
// A data set not stored has no revisions read.
func TestScanDataExchange_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		op, typ    string
		kept, lost string
		err        error
		wantTotal  int
	}{
		// ds2 and its revision are lost.
		{"ListDataSets", TypeDataExchangeDataSets, adxtDataSetARN("ds1"), adxtDataSetARN("ds2"), adxtBad, adxtTotal - 2},
		{"ListDataSets", TypeDataExchangeDataSets, adxtDataSetARN("ds1"), adxtDataSetARN("ds2"), adxtDenied, adxtTotal - 2},
		{"ListDataGrants", TypeDataExchangeDataGrants, adxtGrantARN(testAccountID, "g1"), adxtGrantARN(testAccountID, "g2"), adxtBad, adxtTotal - 1},
		{"ListEventActions", TypeDataExchangeEventActions, adxtEventActionARN("ea1"), adxtEventActionARN("ea2"), adxtBad, adxtTotal - 1},
		{"ListReceivedDataGrants", TypeDataExchangeReceivedDataGrant, adxtGrantARN(adxtSender, "rg1") + "/received", adxtGrantARN(adxtSender, "rg2") + "/received", adxtBad, adxtTotal - 1},
		// r2 and its asset are lost.
		{"ListDataSetRevisions", TypeDataExchangeDataSetRevision, adxtRevisionARN("ds1", "r1"), adxtRevisionARN("ds1", "r2"), adxtBad, adxtTotal - 2},
		{"ListRevisionAssets", TypeDataExchangeRevisionAsset, adxtAssetARN("ds1", "r1", "a1"), adxtAssetARN("ds1", "r1", "a2"), adxtBad, adxtTotal - 1},
	} {
		t.Run(c.op+"/"+c.err.(interface{ ErrorCode() string }).ErrorCode(), func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newADXTCalls()
			handlers := adxtHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if !reflect.ValueOf(in).Elem().FieldByName("NextToken").IsNil() {
					return nil, c.err
				}
				return list(in)
			}

			total, _, err := adxtScan(context.Background(), st, handlers)
			if c.err == adxtDenied {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if got := adxtWarnings(warns()); !slices.Equal(got, []string{"dataexchange:" + c.op}) {
					t.Errorf("warnings = %v; want one under %s", got, c.op)
				}
			} else {
				assertAPICode(t, err, "ValidationException")
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			rows := rowsByNativeID(t, st, c.typ)
			if _, ok := rows[c.kept]; !ok {
				t.Errorf("%s not kept", c.kept)
			}
			if _, ok := rows[c.lost]; ok {
				t.Errorf("%s stored; want it lost with its page", c.lost)
			}
			if c.op != "ListRevisionAssets" && calls.count("ListRevisionAssets") == 0 {
				t.Error("the asset phase did not run after the failure")
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanDataExchange_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newADXTCalls()
	handlers := adxtHandlers(calls)
	handlers["ListEventActions"] = func(in any) (any, error) {
		calls.record("ListEventActions", in)
		id := "ea1"
		if in.(*dataexchange.ListEventActionsInput).NextToken != nil {
			id = "ea2"
		}
		return &dataexchange.ListEventActionsOutput{EventActions: []dxtypes.EventActionEntry{adxtEventAction(id)}, NextToken: ptrStr("same")}, nil
	}

	total, _, err := adxtScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if calls.count("ListEventActions") != 2 {
		t.Errorf("ListEventActions called %d times; want 2", calls.count("ListEventActions"))
	}
	if n := len(rowsByNativeID(t, st, TypeDataExchangeEventActions)); n != 2 || total != adxtTotal {
		t.Errorf("event action rows = %d, total = %d; want both pages' 2, %d", n, total, adxtTotal)
	}
}

// One parent's failed child listing costs the other parents none of their
// rows: a parent deleted before its listing returned anything is skipped
// silently, one deleted mid-listing keeps its first page and warns, a
// denial warns once under the op, and any other error is returned.
func TestScanDataExchange_PerParentListingFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, op  string
		first     bool // fail the first page rather than the second
		err       error
		code      string
		wantTotal int
		warn      bool
	}{
		// ds1's two revisions and three assets are lost.
		{"revisions gone", "ListDataSetRevisions", true, adxtGone, "", adxtTotal - 5, false},
		// ds1's r2 and its asset are lost.
		{"revisions gone mid-listing", "ListDataSetRevisions", false, adxtGone, "", adxtTotal - 2, true},
		{"revisions denied", "ListDataSetRevisions", true, adxtDenied, "", adxtTotal - 5, true},
		{"revisions other", "ListDataSetRevisions", true, adxtBad, "ValidationException", adxtTotal - 5, false},
		// r1's two assets are lost.
		{"assets gone", "ListRevisionAssets", true, adxtGone, "", adxtTotal - 2, false},
		{"assets denied", "ListRevisionAssets", true, adxtDenied, "", adxtTotal - 2, true},
		{"assets other", "ListRevisionAssets", true, adxtBad, "ValidationException", adxtTotal - 2, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newADXTCalls()
			handlers := adxtHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				parent := reflect.ValueOf(in).Elem().FieldByName("DataSetId").Elem().String()
				if rev := reflect.ValueOf(in).Elem().FieldByName("RevisionId"); rev.IsValid() {
					parent += "/" + rev.Elem().String()
				}
				firstPage := reflect.ValueOf(in).Elem().FieldByName("NextToken").IsNil()
				if (parent == "ds1" || parent == "ds1/r1") && firstPage == c.first {
					return nil, c.err
				}
				return list(in)
			}

			total, _, err := adxtScan(context.Background(), st, handlers)
			if c.code == "" {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertAPICode(t, err, c.code)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			if _, ok := rowsByNativeID(t, st, TypeDataExchangeDataSetRevision)[adxtRevisionARN("ds2", "r1")]; !ok {
				t.Error("ds2's revision lost to ds1's failure")
			}
			var want []string
			if c.warn {
				want = []string{"dataexchange:" + c.op}
			}
			if got := adxtWarnings(warns()); !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
}

// An element without an ARN is not stored, is not sent to the tag op, and is
// warned about once under its listing. A data set, or a revision of a share
// data set, without an Id is stored without its children read, with one
// warning naming the empty field; a revision of an S3 snapshot data set
// without one has no assets to list and is not warned about.
func TestScanDataExchange_ElementWithoutARNOrID(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newADXTCalls()
	handlers := adxtHandlers(calls)
	handlers["ListDataSets"] = func(in any) (any, error) {
		calls.record("ListDataSets", in)
		noID := adxtDataSet("ds3", dxtypes.AssetTypeRedshiftDataShare)
		noID.Id = nil
		return &dataexchange.ListDataSetsOutput{DataSets: []dxtypes.DataSetEntry{
			adxtDataSet("ds1", dxtypes.AssetTypeRedshiftDataShare), adxtDataSet("ds2", dxtypes.AssetTypeS3Snapshot), noID, {Name: ptrStr("set-nameless")},
		}}, nil
	}
	list := handlers["ListDataSetRevisions"]
	handlers["ListDataSetRevisions"] = func(in any) (any, error) {
		got, err := list(in)
		out := got.(*dataexchange.ListDataSetRevisionsOutput)
		if req := in.(*dataexchange.ListDataSetRevisionsInput); req.NextToken == nil {
			ds := sv(req.DataSetId)
			noID := adxtRevision(ds, "rx")
			noID.Id = nil
			out.Revisions = append(out.Revisions, noID, dxtypes.RevisionEntry{Id: ptrStr("r-noarn")})
		}
		return out, err
	}

	total, _, err := adxtScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// ds3, and an Id-less revision of each of ds1 and ds2.
	if want := adxtTotal + 3; total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
	if got := calls.listedParents("ListDataSetRevisions"); !slices.Equal(got, []string{"ds1", "ds2"}) {
		t.Errorf("revisions listed for %v; want [ds1 ds2]", got)
	}
	if got := calls.listedParents("ListRevisionAssets"); !slices.Equal(got, []string{"ds1/r1", "ds1/r2"}) {
		t.Errorf("assets listed for %v; want [ds1/r1 ds1/r2]", got)
	}
	for _, arn := range calls.sortedTagARNs() {
		if arn == "" {
			t.Error("an element without an ARN was sent to the tag op")
		}
	}
	// One each: data sets with no ARN and with no Id; revisions with no ARN
	// and, of ds1 only, with no Id.
	got := adxtWarnings(warns())
	slices.Sort(got)
	want := []string{"dataexchange:ListDataSetRevisions", "dataexchange:ListDataSetRevisions", "dataexchange:ListDataSets", "dataexchange:ListDataSets"}
	if !slices.Equal(got, want) {
		t.Errorf("warnings = %v; want %v", got, want)
	}
}

// A revision of an S3 snapshot data set has no assets to list, so one without
// an Id is stored without a warning.
func TestScanDataExchange_SnapshotRevisionWithoutIDNotWarned(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	handlers := adxtHandlers(newADXTCalls())
	list := handlers["ListDataSetRevisions"]
	handlers["ListDataSetRevisions"] = func(in any) (any, error) {
		got, err := list(in)
		if req := in.(*dataexchange.ListDataSetRevisionsInput); sv(req.DataSetId) == "ds2" {
			noID := adxtRevision("ds2", "rx")
			noID.Id = nil
			out := got.(*dataexchange.ListDataSetRevisionsOutput)
			out.Revisions = append(out.Revisions, noID)
		}
		return got, err
	}

	total, _, err := adxtScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := adxtTotal + 1; total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
	if _, ok := rowsByNativeID(t, st, TypeDataExchangeDataSetRevision)[adxtRevisionARN("ds2", "rx")]; !ok {
		t.Error("ds2's revision without an Id not stored")
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// The asset phase reads its revisions in ARN order, whatever order their
// pages were written in, so its errors come in that order: dsb's revision
// is written before dsa's, and the asset listings of both fail.
func TestScanDataExchange_AssetPhaseReadsRevisionsInARNOrder(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newADXTCalls()
	handlers := adxtHandlers(calls)
	handlers["ListDataSets"] = func(any) (any, error) {
		return &dataexchange.ListDataSetsOutput{DataSets: []dxtypes.DataSetEntry{
			adxtDataSet("dsa", dxtypes.AssetTypeRedshiftDataShare), adxtDataSet("dsb", dxtypes.AssetTypeRedshiftDataShare),
		}}, nil
	}
	// dsb's second page is read only once its first page is written; dsa's
	// one page waits for it.
	dsbWritten := make(chan struct{})
	handlers["ListDataSetRevisions"] = func(in any) (any, error) {
		req := in.(*dataexchange.ListDataSetRevisionsInput)
		switch {
		case sv(req.DataSetId) == "dsb" && req.NextToken == nil:
			return &dataexchange.ListDataSetRevisionsOutput{Revisions: []dxtypes.RevisionEntry{adxtRevision("dsb", "r1")}, NextToken: ptrStr("p2")}, nil
		case sv(req.DataSetId) == "dsb":
			close(dsbWritten)
			return &dataexchange.ListDataSetRevisionsOutput{}, nil
		}
		select {
		case <-dsbWritten:
		case <-time.After(10 * time.Second):
			return nil, errors.New("dsb's revision page was not written")
		}
		return &dataexchange.ListDataSetRevisionsOutput{Revisions: []dxtypes.RevisionEntry{adxtRevision("dsa", "r1")}}, nil
	}
	errA, errB := apiErr("ValidationException", "dsa"), apiErr("ValidationException", "dsb")
	handlers["ListRevisionAssets"] = func(in any) (any, error) {
		if sv(in.(*dataexchange.ListRevisionAssetsInput).DataSetId) == "dsa" {
			return nil, errA
		}
		return nil, errB
	}

	_, _, err := adxtScan(context.Background(), st, handlers)
	if got, want := adxtLeafOrder(err, errA, errB), []error{errA, errB}; !slices.Equal(got, want) {
		t.Errorf("asset listing errors in order %v; want %v (err %v)", got, want, err)
	}
}

// adxtLeafOrder returns those of targets err holds, in the order a
// depth-first walk of its joined members meets them.
func adxtLeafOrder(err error, targets ...error) []error {
	if err == nil {
		return nil
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		var got []error
		for _, m := range j.Unwrap() {
			got = append(got, adxtLeafOrder(m, targets...)...)
		}
		return got
	}
	for _, t := range targets {
		if err == t {
			return []error{t}
		}
	}
	return adxtLeafOrder(errors.Unwrap(err), targets...)
}

// A GovCloud or China ARN reaches the tag op unchanged.
func TestScanDataExchange_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	for _, pf := range []struct{ partition, region string }{{"aws-us-gov", "us-gov-west-1"}, {"aws-cn", "cn-north-1"}} {
		t.Run(pf.partition, func(t *testing.T) {
			arn := fmt.Sprintf("arn:%s:dataexchange:%s:%s:event-actions/ea-p", pf.partition, pf.region, testAccountID)
			st := newTestStore(t)
			calls := newADXTCalls()
			calls.tags[arn] = map[string]string{"env": "prod"}
			handlers := adxtHandlers(calls)
			handlers["ListEventActions"] = func(any) (any, error) {
				return &dataexchange.ListEventActionsOutput{EventActions: []dxtypes.EventActionEntry{{Arn: ptrStr(arn), Id: ptrStr("ea-p")}}}, nil
			}
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(handlers, adxtDenied)

			if _, _, err := scanDataExchange(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if !slices.Contains(calls.sortedTagARNs(), arn) {
				t.Fatalf("ListTagsForResource ARNs = %v; want %s among them", calls.sortedTagARNs(), arn)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeDataExchangeEventActions)[arn].TagsJSON, map[string]string{"env": "prod"})
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing and its tag reads, and the later phases are
// not run.
func TestScanDataExchange_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newADXTCalls()
	handlers := adxtHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListEventActions"]
	handlers["ListEventActions"] = func(in any) (any, error) {
		if in.(*dataexchange.ListEventActionsInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := adxtScan(ctx, st, handlers)
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
	for _, op := range []string{"ListReceivedDataGrants", "ListDataSetRevisions", "ListRevisionAssets"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanDataExchange_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "dataexchange.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := adxtHandlers(newADXTCalls())
		handlers["ListReceivedDataGrants"] = func(any) (any, error) { return nil, nx }
		total, _, err := adxtScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := adxtTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newADXTCalls()
		calls.errs[adxtAssetARN("ds1", "r2", "a1")] = nx
		_, _, err := adxtScan(context.Background(), st, adxtHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanDataExchange(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

// Every call goes to the scanned region, not the config's.
func TestScanDataExchange_CallsGoToScannedRegion(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var regions parentCalls
	cfg := opStubConfig(adxtHandlers(newADXTCalls()), adxtDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	if _, _, err := scanDataExchange(context.Background(), acct, "eu-west-1", st, testScanID); err != nil {
		t.Fatalf("scanDataExchange: %v", err)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
}

// An API asset's key and specification link are redacted; the rest of the
// asset is stored as listed.
func TestScanDataExchange_RedactsAPIAssetSecrets(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newADXTCalls()
	handlers := adxtHandlers(calls)
	handlers["ListRevisionAssets"] = func(in any) (any, error) {
		req := in.(*dataexchange.ListRevisionAssetsInput)
		a := adxtAsset(sv(req.DataSetId), sv(req.RevisionId), "api")
		a.AssetType = dxtypes.AssetTypeApiGatewayApi
		a.AssetDetails = &dxtypes.AssetDetails{ApiGatewayApiAsset: &dxtypes.ApiGatewayApiAsset{
			ApiId: ptrStr("abc123"), ApiKey: ptrStr("k-secret"),
			ApiSpecificationDownloadUrl:          ptrStr("https://example.com/spec?X-Amz-Signature=sig"),
			ApiSpecificationDownloadUrlExpiresAt: &adxtCreated,
		}}
		return &dataexchange.ListRevisionAssetsOutput{Assets: []dxtypes.AssetEntry{a}}, nil
	}
	if _, _, err := adxtScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	attrs := rowsByNativeID(t, st, TypeDataExchangeRevisionAsset)[adxtAssetARN("ds1", "r1", "api")].AttributesJSON
	for _, leak := range []string{"k-secret", "X-Amz-Signature"} {
		if strings.Contains(attrs, leak) {
			t.Errorf("attrs hold %q: %s", leak, attrs)
		}
	}
	for _, kept := range []string{`"ApiId":"abc123"`, `"ApiKey":"` + redact.Placeholder + `"`, `"ApiSpecificationDownloadUrl":"` + redact.Placeholder + `"`} {
		if !strings.Contains(attrs, kept) {
			t.Errorf("attrs lack %s: %s", kept, attrs)
		}
	}
}

// Identity includes the account, so a sent and a received grant can share a
// NativeID only if an account grants to itself, which is unverified. The
// /received suffix keeps the two rows apart even then.
func TestScanDataExchange_ReceivedGrantKeptApartFromSentGrant(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newADXTCalls()
	handlers := adxtHandlers(calls)
	handlers["ListReceivedDataGrants"] = func(any) (any, error) {
		return &dataexchange.ListReceivedDataGrantsOutput{DataGrantSummaries: []dxtypes.ReceivedDataGrantSummariesEntry{adxtReceived(testAccountID, "g1")}}, nil
	}
	if _, _, err := adxtScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	sent := rowsByNativeID(t, st, TypeDataExchangeDataGrants)[adxtGrantARN(testAccountID, "g1")]
	received := rowsByNativeID(t, st, TypeDataExchangeReceivedDataGrant)[adxtGrantARN(testAccountID, "g1")+"/received"]
	if sent.ID == "" || received.ID == "" || sent.ID == received.ID {
		t.Errorf("sent id = %q, received id = %q; want two distinct rows", sent.ID, received.ID)
	}
}
