package aws

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dataexchange"
	dxtypes "github.com/aws/aws-sdk-go-v2/service/dataexchange/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/store"
)

var (
	dxDenied   = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: dataexchange:List"}
	dxBadInput = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	dxGone     = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "not found"}
	dxCreated  = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
)

func dxsDataSetARN(ds string) string {
	return fmt.Sprintf("arn:aws:dataexchange:%s:%s:data-sets/%s", testRegion, testAccountID, ds)
}

func dxsRevisionARN(ds, rev string) string { return dxsDataSetARN(ds) + "/revisions/" + rev }

func dxsAssetARN(ds, rev, asset string) string {
	return dxsRevisionARN(ds, rev) + "/assets/" + asset
}

func dxsGrantARN(id string) string {
	return fmt.Sprintf("arn:aws:dataexchange:%s:%s:data-grants/%s", testRegion, testAccountID, id)
}

func dxsRevision(ds, rev string) dxtypes.RevisionEntry {
	return dxtypes.RevisionEntry{
		Arn: ptrStr(dxsRevisionARN(ds, rev)), Id: ptrStr(rev), DataSetId: ptrStr(ds),
		CreatedAt: &dxCreated, UpdatedAt: &dxCreated, Finalized: true,
	}
}

func dxsAsset(ds, rev, asset string) dxtypes.AssetEntry {
	return dxtypes.AssetEntry{
		Arn: ptrStr(dxsAssetARN(ds, rev, asset)), Id: ptrStr(asset), Name: ptrStr("share-" + asset),
		DataSetId: ptrStr(ds), RevisionId: ptrStr(rev), AssetType: dxtypes.AssetTypeRedshiftDataShare,
		AssetDetails: &dxtypes.AssetDetails{RedshiftDataShareAsset: &dxtypes.RedshiftDataShareAsset{
			Arn: ptrStr("arn:aws:redshift:us-east-1:123456789012:datashare:ns/" + asset),
		}},
		CreatedAt: &dxCreated, UpdatedAt: &dxCreated,
	}
}

func dxsClient(handlers map[string]func(in any) (any, error)) *dataexchange.Client {
	return dataexchange.NewFromConfig(opStubConfig(handlers, dxDenied))
}

// seedDXParents stores a row for each NativeID, so the contains rows the
// fan-out records have both endpoints.
func seedDXParents(t *testing.T, st *store.Store, typ string, nativeIDs ...string) {
	t.Helper()
	seeds := make([]*store.Resource, 0, len(nativeIDs))
	for _, id := range nativeIDs {
		seeds = append(seeds, &store.Resource{Provider: "aws", AccountID: testAccountID, Type: typ, NativeID: id, DiscoveredBy: testScanID})
	}
	if _, err := st.UpsertResources(seeds); err != nil {
		t.Fatalf("seed %s: %v", typ, err)
	}
}

// dxsRevisionHandler serves, for data set "ds1", page one (r1) then page two
// (r2 revoked, an element with no Arn and a draft with no Id); r1 is
// finalized. For any other data set one
// page holding its own r1. A data set in fail returns that error on every
// page; fail["ds1/p2"] fails only ds1's page two. Every call's data set is
// recorded.
func dxsRevisionHandler(calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*dataexchange.ListDataSetRevisionsInput)
		ds := sv(req.DataSetId)
		calls.add(ds)
		if err := fail[ds]; err != nil {
			return nil, err
		}
		if ds != "ds1" {
			return &dataexchange.ListDataSetRevisionsOutput{Revisions: []dxtypes.RevisionEntry{dxsRevision(ds, "r1")}}, nil
		}
		if req.NextToken == nil {
			return &dataexchange.ListDataSetRevisionsOutput{Revisions: []dxtypes.RevisionEntry{dxsRevision("ds1", "r1")}, NextToken: ptrStr("p2")}, nil
		}
		if err := fail["ds1/p2"]; err != nil {
			return nil, err
		}
		revoked := dxsRevision("ds1", "r2")
		revoked.Revoked = true
		draft := dxsRevision("ds1", "rx")
		draft.Id, draft.Finalized = nil, false
		return &dataexchange.ListDataSetRevisionsOutput{Revisions: []dxtypes.RevisionEntry{
			revoked, {Id: ptrStr("r3"), DataSetId: ptrStr("ds1")}, draft,
		}}, nil
	}
}

// scanDXRevisions seeds the data-set rows and fans out over them; ds1 holds
// shares, every other data set S3 snapshots.
func scanDXRevisions(t *testing.T, st *store.Store, handler func(in any) (any, error), dataSets ...string) ([]dxRevision, int, error) {
	t.Helper()
	parents := make([]dxDataSet, 0, len(dataSets))
	arns := make([]string, 0, len(dataSets))
	for _, ds := range dataSets {
		at := dxtypes.AssetTypeS3Snapshot
		if ds == "ds1" {
			at = dxtypes.AssetTypeRedshiftDataShare
		}
		parents = append(parents, dxDataSet{childParent: childParent{id: ds, arn: dxsDataSetARN(ds)}, assetType: at})
		arns = append(arns, dxsDataSetARN(ds))
	}
	seedDXParents(t, st, TypeDataExchangeDataSets, arns...)
	client := dxsClient(map[string]func(any) (any, error){"ListDataSetRevisions": handler})
	revs, total, _, err := scanDataExchangeRevisions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
	return revs, total, err
}

func revisionKeys(revs []dxRevision) []string {
	out := make([]string, 0, len(revs))
	for _, r := range revs {
		out = append(out, r.id+"|"+r.dataSetID+"|"+r.revisionID+"|"+r.arn)
	}
	slices.Sort(out)
	return out
}

func TestScanDataExchangeRevisions_StoresEveryPageOfEveryDataSet(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	revs, total, err := scanDXRevisions(t, st, dxsRevisionHandler(&calls, nil), "ds1", "ds2")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"ds1", "ds2"}) {
		t.Errorf("data sets listed = %v, want [ds1 ds2]", got)
	}
	rows := rowsByNativeID(t, st, TypeDataExchangeDataSetRevision)
	want := []string{dxsRevisionARN("ds1", "r1"), dxsRevisionARN("ds1", "r2"), dxsRevisionARN("ds1", "rx"), dxsRevisionARN("ds2", "r1")}
	if total != 4 || len(rows) != 4 {
		t.Fatalf("total = %d, rows = %v; want %v", total, rows, want)
	}
	for _, id := range want {
		if _, ok := rows[id]; !ok {
			t.Errorf("missing %s in %v", id, rows)
		}
	}
	r2 := rows[dxsRevisionARN("ds1", "r2")]
	if sv(r2.Name) != "r2" || sv(r2.Region) != testRegion || sv(r2.CreatedAt) != "2026-09-01T08:00:00Z" ||
		!strings.Contains(r2.AttributesJSON, `"Finalized":true`) {
		t.Errorf("r2 name/region/created = %q/%q/%q, attrs = %s", sv(r2.Name), sv(r2.Region), sv(r2.CreatedAt), r2.AttributesJSON)
	}
	for native, want := range map[string]string{
		dxsRevisionARN("ds1", "r1"): "FINALIZED",
		dxsRevisionARN("ds1", "r2"): "REVOKED",
		dxsRevisionARN("ds1", "rx"): "DRAFT",
	} {
		if got := sv(rows[native].Status); got != want {
			t.Errorf("%s status = %q, want %q", native, got, want)
		}
	}
	assertContainsNative(t, st, dxsDataSetARN("ds1"), dxsRevisionARN("ds1", "r1"))
	assertContainsNative(t, st, dxsDataSetARN("ds1"), dxsRevisionARN("ds1", "r2"))
	assertContainsNative(t, st, dxsDataSetARN("ds2"), dxsRevisionARN("ds2", "r1"))

	// Only ds1 holds shares; the revision with no Id cannot key an asset list.
	wantRevs := []string{
		"ds1/r1|ds1|r1|" + dxsRevisionARN("ds1", "r1"),
		"ds1/r2|ds1|r2|" + dxsRevisionARN("ds1", "r2"),
	}
	if got := revisionKeys(revs); !slices.Equal(got, wantRevs) {
		t.Errorf("asset parents = %v, want %v", got, wantRevs)
	}
}

func TestScanDataExchangeRevisions_Empty(t *testing.T) {
	st := newTestStore(t)
	revs, total, err := scanDXRevisions(t, st, func(any) (any, error) { return &dataexchange.ListDataSetRevisionsOutput{}, nil }, "ds1")
	if err != nil || total != 0 || len(revs) != 0 || len(rowsByNativeID(t, st, TypeDataExchangeDataSetRevision)) != 0 {
		t.Errorf("total = %d, revisions = %v, err = %v; want 0 rows, nil", total, revs, err)
	}
}

// A data set deleted after it was listed is skipped silently; a denial warns
// once for the op and keeps the other data sets; any other error fails.
func TestScanDataExchangeRevisions_FailedDataSet(t *testing.T) {
	t.Run("gone", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "dataexchange:ListDataSetRevisions")
		var calls parentCalls
		_, total, err := scanDXRevisions(t, st, dxsRevisionHandler(&calls, map[string]error{"ds1": dxGone}), "ds1", "ds2")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if _, ok := rowsByNativeID(t, st, TypeDataExchangeDataSetRevision)[dxsRevisionARN("ds2", "r1")]; total != 1 || !ok || *warns != 0 {
			t.Errorf("total = %d, ds2/r1 stored = %v, warnings = %d; want 1, true, 0", total, ok, *warns)
		}
	})
	t.Run("denied", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "dataexchange:ListDataSetRevisions")
		var calls parentCalls
		_, total, err := scanDXRevisions(t, st, dxsRevisionHandler(&calls, map[string]error{"ds2": dxDenied, "ds3": dxDenied}), "ds1", "ds2", "ds3")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if *warns != 1 {
			t.Errorf("warnings = %d, want 1", *warns)
		}
		if n := len(rowsByNativeID(t, st, TypeDataExchangeDataSetRevision)); total != 3 || n != 3 {
			t.Errorf("total = %d, rows = %d; want ds1's 3 revisions", total, n)
		}
	})
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		var calls parentCalls
		_, _, err := scanDXRevisions(t, st, dxsRevisionHandler(&calls, map[string]error{"ds2": dxBadInput}), "ds1", "ds2")
		assertAPICode(t, err, "ValidationException")
	})
}

// Each page is stored as it arrives: a failure on page two leaves page one's
// revision stored, linked, counted and handed to the asset phase, whether the
// failure is a denial (warned, nil error) or any other error (returned).
func TestScanDataExchangeRevisions_FailedSecondPageKeepsFirstPage(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantCode string
		wantWarn int
	}{
		{name: "denied", err: dxDenied, wantWarn: 1},
		{name: "other", err: dxBadInput, wantCode: "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "dataexchange:ListDataSetRevisions")
			var calls parentCalls
			revs, total, err := scanDXRevisions(t, st, dxsRevisionHandler(&calls, map[string]error{"ds1/p2": tc.err}), "ds1")
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
			}
			rows := rowsByNativeID(t, st, TypeDataExchangeDataSetRevision)
			if _, ok := rows[dxsRevisionARN("ds1", "r1")]; total != 1 || len(rows) != 1 || !ok {
				t.Errorf("total = %d, rows = %v; want page one's ds1/r1 only", total, rows)
			}
			if *warns != tc.wantWarn {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarn)
			}
			if got := revisionKeys(revs); !slices.Equal(got, []string{"ds1/r1|ds1|r1|" + dxsRevisionARN("ds1", "r1")}) {
				t.Errorf("asset parents = %v, want ds1/r1", got)
			}
			assertContainsNative(t, st, dxsDataSetARN("ds1"), dxsRevisionARN("ds1", "r1"))
		})
	}
}

// dxsAssetHandler serves, for revision "r1" of "ds1", page one (a1) then page
// two (a2 and an element with no Arn); for any other revision one page holding
// its own a1. A revision in fail returns that error on every page;
// fail["r1/p2"] fails only r1's page two. Every call's data set and revision
// are recorded as "ds/rev".
func dxsAssetHandler(calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*dataexchange.ListRevisionAssetsInput)
		ds, rev := sv(req.DataSetId), sv(req.RevisionId)
		calls.add(ds + "/" + rev)
		if err := fail[rev]; err != nil {
			return nil, err
		}
		if rev != "r1" {
			return &dataexchange.ListRevisionAssetsOutput{Assets: []dxtypes.AssetEntry{dxsAsset(ds, rev, "a1")}}, nil
		}
		if req.NextToken == nil {
			return &dataexchange.ListRevisionAssetsOutput{Assets: []dxtypes.AssetEntry{dxsAsset(ds, rev, "a1")}, NextToken: ptrStr("p2")}, nil
		}
		if err := fail["r1/p2"]; err != nil {
			return nil, err
		}
		return &dataexchange.ListRevisionAssetsOutput{Assets: []dxtypes.AssetEntry{dxsAsset(ds, rev, "a2"), {Id: ptrStr("a3")}}}, nil
	}
}

// scanDXAssets seeds the revision rows and fans out over them; revisions are
// given as "ds/rev".
func scanDXAssets(t *testing.T, st *store.Store, handler func(in any) (any, error), revisions ...string) (int, error) {
	t.Helper()
	parents := make([]dxRevision, 0, len(revisions))
	arns := make([]string, 0, len(revisions))
	for _, r := range revisions {
		ds, rev, _ := strings.Cut(r, "/")
		parents = append(parents, dxRevision{childParent: childParent{id: r, arn: dxsRevisionARN(ds, rev)}, dataSetID: ds, revisionID: rev})
		arns = append(arns, dxsRevisionARN(ds, rev))
	}
	seedDXParents(t, st, TypeDataExchangeDataSetRevision, arns...)
	client := dxsClient(map[string]func(any) (any, error){"ListRevisionAssets": handler})
	total, _, err := scanDataExchangeAssets(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
	return total, err
}

func TestScanDataExchangeAssets_StoresEveryPageOfEveryRevision(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, err := scanDXAssets(t, st, dxsAssetHandler(&calls, nil), "ds1/r1", "ds2/r9")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"ds1/r1", "ds2/r9"}) {
		t.Errorf("revisions listed = %v, want [ds1/r1 ds2/r9]", got)
	}
	rows := rowsByNativeID(t, st, TypeDataExchangeRevisionAsset)
	want := []string{dxsAssetARN("ds1", "r1", "a1"), dxsAssetARN("ds1", "r1", "a2"), dxsAssetARN("ds2", "r9", "a1")}
	if total != 3 || len(rows) != 3 {
		t.Fatalf("total = %d, rows = %v; want %v", total, rows, want)
	}
	for _, id := range want {
		if _, ok := rows[id]; !ok {
			t.Errorf("missing %s in %v", id, rows)
		}
	}
	a2 := rows[dxsAssetARN("ds1", "r1", "a2")]
	if sv(a2.Name) != "share-a2" || sv(a2.Region) != testRegion || sv(a2.CreatedAt) != "2026-09-01T08:00:00Z" ||
		!strings.Contains(a2.AttributesJSON, `"Arn":"arn:aws:redshift:us-east-1:123456789012:datashare:ns/a2"`) {
		t.Errorf("a2 name/region/created = %q/%q/%q, attrs = %s", sv(a2.Name), sv(a2.Region), sv(a2.CreatedAt), a2.AttributesJSON)
	}
	assertContainsNative(t, st, dxsRevisionARN("ds1", "r1"), dxsAssetARN("ds1", "r1", "a1"))
	assertContainsNative(t, st, dxsRevisionARN("ds1", "r1"), dxsAssetARN("ds1", "r1", "a2"))
	assertContainsNative(t, st, dxsRevisionARN("ds2", "r9"), dxsAssetARN("ds2", "r9", "a1"))
}

// An API asset's key and specification link are redacted; the rest of the
// asset is stored as listed.
func TestScanDataExchangeAssets_RedactsAPIAssetSecrets(t *testing.T) {
	st := newTestStore(t)
	handler := func(any) (any, error) {
		a := dxsAsset("ds1", "r1", "api")
		a.AssetType = dxtypes.AssetTypeApiGatewayApi
		a.AssetDetails = &dxtypes.AssetDetails{ApiGatewayApiAsset: &dxtypes.ApiGatewayApiAsset{
			ApiId: ptrStr("abc123"), ApiKey: ptrStr("k-secret"),
			ApiSpecificationDownloadUrl:          ptrStr("https://example.com/spec?X-Amz-Signature=sig"),
			ApiSpecificationDownloadUrlExpiresAt: &dxCreated,
		}}
		return &dataexchange.ListRevisionAssetsOutput{Assets: []dxtypes.AssetEntry{a}}, nil
	}
	if _, err := scanDXAssets(t, st, handler, "ds1/r1"); err != nil {
		t.Fatalf("scan: %v", err)
	}
	attrs := rowsByNativeID(t, st, TypeDataExchangeRevisionAsset)[dxsAssetARN("ds1", "r1", "api")].AttributesJSON
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

func TestScanDataExchangeAssets_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanDXAssets(t, st, func(any) (any, error) { return &dataexchange.ListRevisionAssetsOutput{}, nil }, "ds1/r1")
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeDataExchangeRevisionAsset)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

func TestScanDataExchangeAssets_FailedRevision(t *testing.T) {
	t.Run("gone", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "dataexchange:ListRevisionAssets")
		var calls parentCalls
		total, err := scanDXAssets(t, st, dxsAssetHandler(&calls, map[string]error{"r1": dxGone}), "ds1/r1", "ds2/r9")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if _, ok := rowsByNativeID(t, st, TypeDataExchangeRevisionAsset)[dxsAssetARN("ds2", "r9", "a1")]; total != 1 || !ok || *warns != 0 {
			t.Errorf("total = %d, r9/a1 stored = %v, warnings = %d; want 1, true, 0", total, ok, *warns)
		}
	})
	t.Run("denied", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "dataexchange:ListRevisionAssets")
		var calls parentCalls
		total, err := scanDXAssets(t, st, dxsAssetHandler(&calls, map[string]error{"r8": dxDenied, "r9": dxDenied}), "ds1/r1", "ds2/r8", "ds2/r9")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if *warns != 1 {
			t.Errorf("warnings = %d, want 1", *warns)
		}
		if n := len(rowsByNativeID(t, st, TypeDataExchangeRevisionAsset)); total != 2 || n != 2 {
			t.Errorf("total = %d, rows = %d; want r1's 2 assets", total, n)
		}
	})
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		var calls parentCalls
		_, err := scanDXAssets(t, st, dxsAssetHandler(&calls, map[string]error{"r9": dxBadInput}), "ds1/r1", "ds2/r9")
		assertAPICode(t, err, "ValidationException")
	})
}

func TestScanDataExchangeAssets_FailedSecondPageKeepsFirstPage(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantCode string
		wantWarn int
	}{
		{name: "denied", err: dxDenied, wantWarn: 1},
		{name: "other", err: dxBadInput, wantCode: "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "dataexchange:ListRevisionAssets")
			var calls parentCalls
			total, err := scanDXAssets(t, st, dxsAssetHandler(&calls, map[string]error{"r1/p2": tc.err}), "ds1/r1")
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
			}
			rows := rowsByNativeID(t, st, TypeDataExchangeRevisionAsset)
			if _, ok := rows[dxsAssetARN("ds1", "r1", "a1")]; total != 1 || len(rows) != 1 || !ok {
				t.Errorf("total = %d, rows = %v; want page one's a1 only", total, rows)
			}
			if *warns != tc.wantWarn {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarn)
			}
		})
	}
}

func dxsReceivedGrant(id string) dxtypes.ReceivedDataGrantSummariesEntry {
	return dxtypes.ReceivedDataGrantSummariesEntry{
		Arn: ptrStr(dxsGrantARN(id)), Id: ptrStr(id), Name: ptrStr("grant-" + id), DataSetId: ptrStr("entitled-ds"),
		AcceptanceState: dxtypes.DataGrantAcceptanceStateAccepted, CreatedAt: &dxCreated, UpdatedAt: &dxCreated,
		SenderPrincipal: ptrStr("111122223333"), ReceiverPrincipal: ptrStr(testAccountID),
	}
}

// dxsReceivedHandler serves page one (g1) then page two (g2 and an element
// with no Arn); fail["p1"] or fail["p2"] fails that page.
func dxsReceivedHandler(fail map[string]error) func(in any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*dataexchange.ListReceivedDataGrantsInput)
		if req.NextToken == nil {
			if err := fail["p1"]; err != nil {
				return nil, err
			}
			return &dataexchange.ListReceivedDataGrantsOutput{DataGrantSummaries: []dxtypes.ReceivedDataGrantSummariesEntry{dxsReceivedGrant("g1")}, NextToken: ptrStr("p2")}, nil
		}
		if err := fail["p2"]; err != nil {
			return nil, err
		}
		return &dataexchange.ListReceivedDataGrantsOutput{DataGrantSummaries: []dxtypes.ReceivedDataGrantSummariesEntry{
			dxsReceivedGrant("g2"), {Id: ptrStr("g3")},
		}}, nil
	}
}

func scanDXReceived(st *store.Store, handler func(in any) (any, error)) (int, error) {
	client := dxsClient(map[string]func(any) (any, error){"ListReceivedDataGrants": handler})
	total, _, err := scanDataExchangeReceivedDataGrants(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

func TestScanDataExchangeReceivedDataGrants_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	total, err := scanDXReceived(st, dxsReceivedHandler(nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeDataExchangeReceivedDataGrant)
	g1Native := "arn:aws:dataexchange:us-east-1:123456789012:data-grants/g1/received"
	g2Native := "arn:aws:dataexchange:us-east-1:123456789012:data-grants/g2/received"
	if _, ok := rows[g1Native]; total != 2 || len(rows) != 2 || !ok {
		t.Fatalf("total = %d, rows = %v; want %s and %s", total, rows, g1Native, g2Native)
	}
	g2 := rows[g2Native]
	if sv(g2.Name) != "grant-g2" || sv(g2.Status) != "ACCEPTED" || sv(g2.Region) != testRegion || sv(g2.CreatedAt) != "2026-09-01T08:00:00Z" ||
		!strings.Contains(g2.AttributesJSON, `"SenderPrincipal":"111122223333"`) {
		t.Errorf("g2 name/status/region/created = %q/%q/%q/%q, attrs = %s", sv(g2.Name), sv(g2.Status), sv(g2.Region), sv(g2.CreatedAt), g2.AttributesJSON)
	}
}

// Every page asks for every acceptance state, so no state is left to an
// undocumented default.
func TestScanDataExchangeReceivedDataGrants_FiltersEveryAcceptanceState(t *testing.T) {
	st := newTestStore(t)
	var filters parentCalls
	inner := dxsReceivedHandler(nil)
	if _, err := scanDXReceived(st, func(in any) (any, error) {
		var states []string
		for _, s := range in.(*dataexchange.ListReceivedDataGrantsInput).AcceptanceState {
			states = append(states, string(s))
		}
		slices.Sort(states)
		filters.add(strings.Join(states, ","))
		return inner(in)
	}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := filters.ids; !slices.Equal(got, []string{"ACCEPTED,PENDING_RECEIVER_ACCEPTANCE", "ACCEPTED,PENDING_RECEIVER_ACCEPTANCE"}) {
		t.Errorf("filters sent per page = %v, want both states on both pages", got)
	}
}

func TestScanDataExchangeReceivedDataGrants_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanDXReceived(st, func(any) (any, error) { return &dataexchange.ListReceivedDataGrantsOutput{}, nil })
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeDataExchangeReceivedDataGrant)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

func TestScanDataExchangeReceivedDataGrants_Errors(t *testing.T) {
	t.Run("denied", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "dataexchange:ListReceivedDataGrants")
		total, err := scanDXReceived(st, dxsReceivedHandler(map[string]error{"p1": dxDenied}))
		if err != nil || total != 0 || *warns != 1 {
			t.Errorf("total = %d, err = %v, warnings = %d; want 0, nil, 1", total, err, *warns)
		}
	})
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		_, err := scanDXReceived(st, dxsReceivedHandler(map[string]error{"p2": dxBadInput}))
		assertAPICode(t, err, "ValidationException")
	})
}

// Identity includes the account, so a sent and a received grant can share a
// NativeID only if an account grants to itself, which is unverified. The
// /received suffix keeps the two rows apart even then.
func TestScanDataExchangeReceivedDataGrants_KeepsApartFromSentGrant(t *testing.T) {
	st := newTestStore(t)
	client := dxsClient(map[string]func(any) (any, error){
		"ListDataGrants": func(any) (any, error) {
			return &dataexchange.ListDataGrantsOutput{DataGrantSummaries: []dxtypes.DataGrantSummaryEntry{{
				Arn: ptrStr(dxsGrantARN("g1")), Id: ptrStr("g1"), Name: ptrStr("grant-g1"), AcceptanceState: dxtypes.DataGrantAcceptanceStateAccepted,
			}}}, nil
		},
		"ListReceivedDataGrants": func(any) (any, error) {
			return &dataexchange.ListReceivedDataGrantsOutput{DataGrantSummaries: []dxtypes.ReceivedDataGrantSummariesEntry{dxsReceivedGrant("g1")}}, nil
		},
	})
	acct := newTestAccount(testAccountID)
	if _, _, err := scanDataExchangeDataGrants(context.Background(), client, acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("data grants: %v", err)
	}
	if _, _, err := scanDataExchangeReceivedDataGrants(context.Background(), client, acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("received data grants: %v", err)
	}
	sent := rowsByNativeID(t, st, TypeDataExchangeDataGrants)["arn:aws:dataexchange:us-east-1:123456789012:data-grants/g1"]
	received := rowsByNativeID(t, st, TypeDataExchangeReceivedDataGrant)["arn:aws:dataexchange:us-east-1:123456789012:data-grants/g1/received"]
	if sent.ID == "" || received.ID == "" || sent.ID == received.ID {
		t.Errorf("sent id = %q, received id = %q; want two distinct rows", sent.ID, received.ID)
	}
}

// dxsTopLevelHandlers answers every op: data sets ds1 (shares) and ds2 (S3
// snapshots), one event action (the last pre-existing phase), each data set's
// revisions, the share revisions' assets and the received grants. Data grants
// are denied and warn-skipped by their phase. Asset calls are recorded.
func dxsTopLevelHandlers(assetCalls *parentCalls) map[string]func(in any) (any, error) {
	var revCalls parentCalls
	return map[string]func(in any) (any, error){
		"ListDataSets": func(any) (any, error) {
			return &dataexchange.ListDataSetsOutput{DataSets: []dxtypes.DataSetEntry{
				{Arn: ptrStr(dxsDataSetARN("ds1")), Id: ptrStr("ds1"), AssetType: dxtypes.AssetTypeRedshiftDataShare},
				{Arn: ptrStr(dxsDataSetARN("ds2")), Id: ptrStr("ds2"), AssetType: dxtypes.AssetTypeS3Snapshot},
			}}, nil
		},
		"ListEventActions": func(any) (any, error) {
			return &dataexchange.ListEventActionsOutput{EventActions: []dxtypes.EventActionEntry{{
				Arn: ptrStr(fmt.Sprintf("arn:aws:dataexchange:%s:%s:event-actions/ea1", testRegion, testAccountID)), Id: ptrStr("ea1"),
			}}}, nil
		},
		"ListDataSetRevisions":   dxsRevisionHandler(&revCalls, nil),
		"ListRevisionAssets":     dxsAssetHandler(assetCalls, nil),
		"ListReceivedDataGrants": dxsReceivedHandler(nil),
	}
}

// Revisions are listed for every stored data set, assets only for the
// revisions of the share data set, and every row lands under its parent.
func TestScanDataExchange_StoresChildrenOfListedDataSets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var assetCalls parentCalls
	acct.cfg = opStubConfig(dxsTopLevelHandlers(&assetCalls), dxDenied)

	total, _, err := scanDataExchange(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanDataExchange: %v", err)
	}
	if got := assetCalls.distinct(); !slices.Equal(got, []string{"ds1/r1", "ds1/r2"}) {
		t.Errorf("assets listed for %v, want [ds1/r1 ds1/r2]", got)
	}
	// 2 data sets, 1 event action, 4 revisions, 3 assets (r1: a1 a2, r2: a1), 2 received grants.
	if total != 12 {
		t.Errorf("total = %d, want 12", total)
	}
	assertContainsNative(t, st, dxsDataSetARN("ds2"), dxsRevisionARN("ds2", "r1"))
	assertContainsNative(t, st, dxsRevisionARN("ds1", "r2"), dxsAssetARN("ds1", "r2", "a1"))
	if n := len(rowsByNativeID(t, st, TypeDataExchangeReceivedDataGrant)); n != 2 {
		t.Errorf("received grants = %d, want 2", n)
	}
}

func TestScanDataExchange_CallsGoToScannedRegion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var assetCalls, regions parentCalls
	cfg := opStubConfig(dxsTopLevelHandlers(&assetCalls), dxDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	if _, _, err := scanDataExchange(context.Background(), acct, "eu-west-1", st, testScanID); err != nil {
		t.Fatalf("scanDataExchange: %v", err)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
	if n := len(assetCalls.distinct()); n != 2 {
		t.Errorf("asset list calls = %d revisions, want 2", n)
	}
}

// A fan-out phase that fails after storing rows reports them in the scan total.
func TestScanDataExchange_FailedPhaseCountsStoredRows(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var assetCalls, revCalls parentCalls
	handlers := dxsTopLevelHandlers(&assetCalls)
	handlers["ListDataSets"] = func(any) (any, error) {
		return &dataexchange.ListDataSetsOutput{DataSets: []dxtypes.DataSetEntry{
			{Arn: ptrStr(dxsDataSetARN("ds1")), Id: ptrStr("ds1"), AssetType: dxtypes.AssetTypeRedshiftDataShare},
		}}, nil
	}
	handlers["ListDataSetRevisions"] = dxsRevisionHandler(&revCalls, map[string]error{"ds1/p2": dxBadInput})
	acct.cfg = opStubConfig(handlers, dxDenied)

	total, _, err := scanDataExchange(context.Background(), acct, testRegion, st, testScanID)
	assertAPICode(t, err, "ValidationException")
	// ds1, the event action, 2 received grants and ds1's page-one revision r1.
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
}

// Each new phase runs after every pre-existing phase, received grants before
// the two fan-outs, and a phase's hard error reaches the caller with the
// counts of the phases before it.
func TestScanDataExchange_NewPhaseErrorAfterExistingPhases(t *testing.T) {
	for _, tc := range []struct {
		op                     string
		wantTotal              int
		wantReceived, wantRevs int
	}{
		// 2 data sets + 1 event action; no fan-out has run.
		{op: "ListReceivedDataGrants", wantTotal: 3},
		// + 2 received grants; every data set's revisions fail.
		{op: "ListDataSetRevisions", wantTotal: 5, wantReceived: 2},
		// + 4 revisions.
		{op: "ListRevisionAssets", wantTotal: 9, wantReceived: 2, wantRevs: 4},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			var assetCalls parentCalls
			handlers := dxsTopLevelHandlers(&assetCalls)
			var failed atomic.Bool
			handlers[tc.op] = func(any) (any, error) {
				failed.Store(true)
				return nil, dxBadInput
			}
			acct.cfg = opStubConfig(handlers, dxDenied)

			total, _, err := scanDataExchange(context.Background(), acct, testRegion, st, testScanID)
			if !failed.Load() {
				t.Fatalf("%s never called", tc.op)
			}
			assertAPICode(t, err, "ValidationException")
			if n := len(rowsByNativeID(t, st, TypeDataExchangeEventActions)); n != 1 {
				t.Errorf("event action rows = %d, want 1 before the %s phase", n, tc.op)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
			if n := len(rowsByNativeID(t, st, TypeDataExchangeReceivedDataGrant)); n != tc.wantReceived {
				t.Errorf("received grant rows = %d, want %d", n, tc.wantReceived)
			}
			if n := len(rowsByNativeID(t, st, TypeDataExchangeDataSetRevision)); n != tc.wantRevs {
				t.Errorf("revision rows = %d, want %d", n, tc.wantRevs)
			}
		})
	}
}
