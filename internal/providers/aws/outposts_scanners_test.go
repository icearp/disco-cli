package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/outposts"
	outpoststypes "github.com/aws/aws-sdk-go-v2/service/outposts/types"
	"github.com/icearp/disco-cli/store"
)

const (
	opTestSite = "arn:aws:outposts:us-east-1:123456789012:site/os-0123456789abcdef0"
	opTestOP1  = "arn:aws:outposts:us-east-1:123456789012:outpost/op-0123456789abcdef1"
	opTestOP2  = "arn:aws:outposts:us-east-1:123456789012:outpost/op-0123456789abcdef2"
	opTestA1   = opTestOP1 + "/asset/a1"
	opTestA2   = opTestOP1 + "/asset/a2"
	opTestA3   = opTestOP2 + "/asset/a3"
)

var (
	opTestDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: outposts:ListAssets")
	errOPTest    = errors.New("outposts test failure")
)

// opHandlers answers every op the scanner calls: one site, two outposts over
// two pages (one owned by the test account and tagged, one with no owner reported), three assets for the first outpost over two pages (one without
// an id) and one for the second. assetCalls records the outposts ListAssets
// was called for.
func opHandlers(assetCalls *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListSites": func(any) (any, error) {
			return &outposts.ListSitesOutput{Sites: []outpoststypes.Site{{SiteArn: ptrStr(opTestSite)}}}, nil
		},
		"ListOutposts": func(in any) (any, error) {
			if in.(*outposts.ListOutpostsInput).NextToken == nil {
				return &outposts.ListOutpostsOutput{NextToken: ptrStr("p2"), Outposts: []outpoststypes.Outpost{{OutpostArn: ptrStr(opTestOP1), OwnerId: ptrStr(testAccountID), Tags: map[string]string{"rack": "r1"}}}}, nil
			}
			return &outposts.ListOutpostsOutput{Outposts: []outpoststypes.Outpost{{OutpostArn: ptrStr(opTestOP2)}}}, nil
		},
		"ListAssets": func(in any) (any, error) {
			p := in.(*outposts.ListAssetsInput)
			assetCalls.add(sv(p.OutpostIdentifier))
			if p.MaxResults == nil || *p.MaxResults != 1000 {
				return nil, fmt.Errorf("MaxResults = %v, want 1000", p.MaxResults)
			}
			switch {
			case sv(p.OutpostIdentifier) == opTestOP2:
				return &outposts.ListAssetsOutput{Assets: []outpoststypes.AssetInfo{{
					AssetId: ptrStr("a3"), AssetType: outpoststypes.AssetTypeCompute, ComputeAttributes: &outpoststypes.ComputeAttributes{},
				}}}, nil
			case p.NextToken == nil:
				return &outposts.ListAssetsOutput{NextToken: ptrStr("p2"), Assets: []outpoststypes.AssetInfo{{
					AssetId: ptrStr("a1"), AssetType: outpoststypes.AssetTypeCompute, RackId: ptrStr("rack-1"),
					ComputeAttributes: &outpoststypes.ComputeAttributes{State: outpoststypes.ComputeAssetStateActive, HostId: ptrStr("h-0abc")},
				}}}, nil
			default:
				return &outposts.ListAssetsOutput{Assets: []outpoststypes.AssetInfo{
					{AssetId: ptrStr("a2"), AssetType: outpoststypes.AssetTypeStorage},
					{AssetType: outpoststypes.AssetTypeSwitch},
				}}, nil
			}
		},
	}
}

func scanOPTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, opTestDenied)
	total, _, err := scanOutposts(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

// opFailFor wraps the ListAssets handler so a call for outpost fails with err.
func opFailFor(h map[string]func(in any) (any, error), outpost string, err error) {
	inner := h["ListAssets"]
	h["ListAssets"] = func(in any) (any, error) {
		if sv(in.(*outposts.ListAssetsInput).OutpostIdentifier) == outpost {
			return nil, err
		}
		return inner(in)
	}
}

func TestOutpostsAssetNativeID(t *testing.T) {
	if got := outpostsAssetNativeID(opTestOP1, "a1"); got != opTestA1 {
		t.Errorf("outpostsAssetNativeID = %q, want %q", got, opTestA1)
	}
}

func TestScanOutposts_StoresAssets(t *testing.T) {
	st := newTestStore(t)
	var assetCalls parentCalls
	total, err := scanOPTopLevel(t, st, opHandlers(&assetCalls))
	if err != nil {
		t.Fatalf("scanOutposts: %v", err)
	}
	// 1 site + 2 outposts + 3 assets
	if total != 6 {
		t.Errorf("total = %d, want 6", total)
	}
	if got := assetCalls.distinct(); !slices.Equal(got, []string{opTestOP1, opTestOP2}) {
		t.Errorf("assets listed for %v, want [%s %s]", got, opTestOP1, opTestOP2)
	}

	assets := rowsByNativeID(t, st, TypeOutpostsAsset)
	if got := nativeIDs(assets); !slices.Equal(got, []string{opTestA1, opTestA2, opTestA3}) {
		t.Fatalf("asset rows = %v, want [%s %s %s]", got, opTestA1, opTestA2, opTestA3)
	}
	a1 := assets[opTestA1]
	if a1.Type != TypeOutpostsAsset || sv(a1.Name) != "a1" || sv(a1.Status) != "ACTIVE" || sv(a1.Region) != testRegion {
		t.Errorf("asset type/name/status/region = %q/%q/%q/%q, want %s/a1/ACTIVE/%s",
			a1.Type, sv(a1.Name), sv(a1.Status), sv(a1.Region), TypeOutpostsAsset, testRegion)
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(a1.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs %s: %v", a1.AttributesJSON, err)
	}
	if attrs["RackId"] != "rack-1" {
		t.Errorf("asset RackId = %v, want rack-1", attrs["RackId"])
	}
	if got := assets[opTestA2].Status; got != nil {
		t.Errorf("asset without compute attributes has status %q, want none", *got)
	}
	if got := assets[opTestA3].Status; got != nil {
		t.Errorf("asset with an empty compute state has status %q, want none", *got)
	}

	assertContainsNative(t, st, opTestOP1, opTestA1)
	assertContainsNative(t, st, opTestOP1, opTestA2)
	assertContainsNative(t, st, opTestOP2, opTestA3)
	assertContainsEdgesDeclared(t, st)
}

func TestScanOutposts_NoAssets(t *testing.T) {
	t.Run("no outposts", func(t *testing.T) {
		st := newTestStore(t)
		var assetCalls parentCalls
		h := opHandlers(&assetCalls)
		h["ListOutposts"] = func(any) (any, error) { return &outposts.ListOutpostsOutput{}, nil }
		total, err := scanOPTopLevel(t, st, h)
		if err != nil {
			t.Fatalf("scanOutposts: %v", err)
		}
		if total != 1 {
			t.Errorf("total = %d, want 1 (the site)", total)
		}
		if got := assetCalls.distinct(); len(got) != 0 {
			t.Errorf("assets listed for %v, want none", got)
		}
	})
	t.Run("outposts without assets", func(t *testing.T) {
		st := newTestStore(t)
		var assetCalls parentCalls
		h := opHandlers(&assetCalls)
		h["ListAssets"] = func(in any) (any, error) {
			assetCalls.add(sv(in.(*outposts.ListAssetsInput).OutpostIdentifier))
			return &outposts.ListAssetsOutput{}, nil
		}
		total, err := scanOPTopLevel(t, st, h)
		if err != nil {
			t.Fatalf("scanOutposts: %v", err)
		}
		if total != 3 {
			t.Errorf("total = %d, want 3 (site + 2 outposts)", total)
		}
		if n := len(rowsByNativeID(t, st, TypeOutpostsAsset)); n != 0 {
			t.Errorf("asset rows = %d, want 0", n)
		}
		if got := assetCalls.distinct(); !slices.Equal(got, []string{opTestOP1, opTestOP2}) {
			t.Errorf("assets listed for %v, want [%s %s]", got, opTestOP1, opTestOP2)
		}
	})
}

// A per-outpost failure that is not an error of the scan drops that outpost's
// assets and keeps its sibling's: a denial warns once, an outpost deleted
// since it was listed is skipped silently, and a call still failing
// transiently warns once in summary.
func TestScanOutposts_AssetParentSkipped(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		failFor  []string
		warnings int
		want     []string
	}{
		{"access denied", opTestDenied, []string{opTestOP1, opTestOP2}, 1, nil},
		{"access denied one outpost", opTestDenied, []string{opTestOP2}, 1, []string{opTestA1, opTestA2}},
		{"outpost gone", apiErr("NotFoundException", "Outpost not found"), []string{opTestOP1}, 0, []string{opTestA3}},
		{"transient", io.EOF, []string{opTestOP1, opTestOP2}, 1, nil},
		{"transient one outpost", io.EOF, []string{opTestOP1}, 1, []string{opTestA3}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			var assetCalls parentCalls
			h := opHandlers(&assetCalls)
			for _, op := range c.failFor {
				opFailFor(h, op, c.err)
			}
			warnings := countOpWarnings(st, outpostsListAssetsOp)
			total, err := scanOPTopLevel(t, st, h)
			if err != nil {
				t.Fatalf("scanOutposts: %v", err)
			}
			if *warnings != c.warnings {
				t.Errorf("warnings = %d, want %d", *warnings, c.warnings)
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeOutpostsAsset)); !slices.Equal(got, c.want) {
				t.Errorf("asset rows = %v, want %v", got, c.want)
			}
			if want := 3 + len(c.want); total != want {
				t.Errorf("total = %d, want %d", total, want)
			}
		})
	}
}

// Any other ListAssets error, a ValidationException included, fails the
// scan without costing the site and outpost rows stored before it.
func TestScanOutposts_AssetErrorPropagates(t *testing.T) {
	for _, assetErr := range []error{errOPTest, apiErr("ValidationException", "A parameter is not valid.")} {
		t.Run(assetErr.Error(), func(t *testing.T) {
			st := newTestStore(t)
			var assetCalls parentCalls
			h := opHandlers(&assetCalls)
			opFailFor(h, opTestOP2, assetErr)
			total, err := scanOPTopLevel(t, st, h)
			if !errors.Is(err, assetErr) {
				t.Fatalf("err = %v, want the ListAssets error", err)
			}
			if total != 3 {
				t.Errorf("total = %d, want 3 (site + 2 outposts)", total)
			}
			if n := len(rowsByNativeID(t, st, TypeOutpostsSite)); n != 1 {
				t.Errorf("site rows = %d, want 1", n)
			}
			if n := len(rowsByNativeID(t, st, TypeOutpostsOutpost)); n != 2 {
				t.Errorf("outpost rows = %d, want 2", n)
			}
		})
	}
}

// A RAM-shared outpost owned by another account is stored, but its assets are
// the owner's inventory and are not listed.
func TestScanOutposts_SharedOutpostAssetsNotListed(t *testing.T) {
	const foreign = "arn:aws:outposts:us-east-1:210987654321:outpost/op-0123456789abcdef3"
	st := newTestStore(t)
	var assetCalls parentCalls
	h := opHandlers(&assetCalls)
	inner := h["ListOutposts"]
	h["ListOutposts"] = func(in any) (any, error) {
		out, err := inner(in)
		if in.(*outposts.ListOutpostsInput).NextToken != nil {
			o := out.(*outposts.ListOutpostsOutput)
			o.Outposts = append(o.Outposts, outpoststypes.Outpost{OutpostArn: ptrStr(foreign), OwnerId: ptrStr("210987654321")})
		}
		return out, err
	}
	total, err := scanOPTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scanOutposts: %v", err)
	}
	// 1 site + 3 outposts + 3 assets of the two outposts this account owns
	if total != 7 {
		t.Errorf("total = %d, want 7", total)
	}
	if _, ok := rowsByNativeID(t, st, TypeOutpostsOutpost)[foreign]; !ok {
		t.Errorf("shared outpost row %s missing", foreign)
	}
	if got := assetCalls.distinct(); !slices.Equal(got, []string{opTestOP1, opTestOP2}) {
		t.Errorf("assets listed for %v, want [%s %s]", got, opTestOP1, opTestOP2)
	}
}

// A ListOutposts error, even after a page that listed an outpost, returns
// before the asset phase: the site row is kept and no assets are listed.
func TestScanOutposts_OutpostErrorStopsAssets(t *testing.T) {
	st := newTestStore(t)
	var assetCalls parentCalls
	h := opHandlers(&assetCalls)
	inner := h["ListOutposts"]
	h["ListOutposts"] = func(in any) (any, error) {
		if in.(*outposts.ListOutpostsInput).NextToken != nil {
			return nil, errOPTest
		}
		return inner(in)
	}
	total, err := scanOPTopLevel(t, st, h)
	if !errors.Is(err, errOPTest) {
		t.Fatalf("err = %v, want the ListOutposts error", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1 (the site)", total)
	}
	if got := assetCalls.distinct(); len(got) != 0 {
		t.Errorf("assets listed for %v, want none", got)
	}
}

// Site and outpost rows carry their own listed element's Tags; an untagged
// element stores none, and an asset does not inherit its outpost's tags.
func TestScanOutposts_ReadsTags(t *testing.T) {
	const untaggedSite = "arn:aws:outposts:us-east-1:123456789012:site/os-0123456789abcdef9"
	st := newTestStore(t)
	var assetCalls parentCalls
	h := opHandlers(&assetCalls)
	h["ListSites"] = func(any) (any, error) {
		return &outposts.ListSitesOutput{Sites: []outpoststypes.Site{
			{SiteArn: ptrStr(opTestSite), Tags: map[string]string{"dc": "east"}},
			{SiteArn: ptrStr(untaggedSite)},
		}}, nil
	}
	if _, err := scanOPTopLevel(t, st, h); err != nil {
		t.Fatalf("scanOutposts: %v", err)
	}

	sites := rowsByNativeID(t, st, TypeOutpostsSite)
	assertTagsMap(t, sites[opTestSite].TagsJSON, map[string]string{"dc": "east"})
	if got := sites[untaggedSite].TagsJSON; got != nil {
		t.Errorf("untagged site TagsJSON = %q, want nil", *got)
	}
	outpostRows := rowsByNativeID(t, st, TypeOutpostsOutpost)
	assertTagsMap(t, outpostRows[opTestOP1].TagsJSON, map[string]string{"rack": "r1"})
	if got := outpostRows[opTestOP2].TagsJSON; got != nil {
		t.Errorf("untagged outpost TagsJSON = %q, want nil", *got)
	}
	if got := rowsByNativeID(t, st, TypeOutpostsAsset)[opTestA1].TagsJSON; got != nil {
		t.Errorf("asset TagsJSON = %q, want nil (not inherited from its outpost)", *got)
	}
}
