package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awsmw "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/groundstation"
	gstypes "github.com/aws/aws-sdk-go-v2/service/groundstation/types"
	smithymw "github.com/aws/smithy-go/middleware"
	"github.com/icearp/disco-cli/store"
)

// gsSatelliteARN is region-less, as ListSatellites returns it.
func gsSatelliteARN(id string) string {
	return fmt.Sprintf("arn:aws:groundstation::%s:satellite/%s", testAccountID, id)
}

func gsSatellite(id string) gstypes.SatelliteListItem {
	return gstypes.SatelliteListItem{
		SatelliteId: sdkaws.String(id), SatelliteArn: sdkaws.String(gsSatelliteARN(id)), NoradSatelliteID: 25544,
	}
}

func (s *gsStub) ListSatellites(_ context.Context, in *groundstation.ListSatellitesInput, _ ...func(*groundstation.Options)) (*groundstation.ListSatellitesOutput, error) {
	p, next, err := s.page(in.NextToken)
	if err != nil {
		return nil, err
	}
	if p.err != nil {
		return nil, p.err
	}
	return &groundstation.ListSatellitesOutput{Satellites: p.satellites, NextToken: next}, nil
}

func TestScanGSSatellites_PaginatesAndSkipsARNless(t *testing.T) {
	st := newTestStore(t)
	noARN := gsSatellite("c")
	noARN.SatelliteArn = nil
	stub := &gsStub{pages: []gsPage{
		{satellites: []gstypes.SatelliteListItem{gsSatellite("a"), noARN}},
		{satellites: []gstypes.SatelliteListItem{gsSatellite("b")}},
	}}

	total, _, err := scanGSSatellites(context.Background(), stub, newTestAccount(testAccountID), st, testScanID)
	if err != nil || total != 2 {
		t.Fatalf("scanGSSatellites = (%d, %v); want (2, nil)", total, err)
	}
	rows := gsRowsByNativeID(t, st, TypeGroundStationSatellite)
	if len(rows) != 2 {
		t.Errorf("stored %d satellites; want 2 (ARN-less skipped)", len(rows))
	}
	r, ok := rows[gsSatelliteARN("b")]
	if !ok {
		t.Fatalf("satellite b not stored under %s", gsSatelliteARN("b"))
	}
	if sv(r.Region) != "global" || sv(r.Name) != "b" {
		t.Errorf("row = region %q name %q; want global b", sv(r.Region), sv(r.Name))
	}
	var attrs struct{ NoradSatelliteID int32 }
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.NoradSatelliteID != 25544 {
		t.Errorf("attrs NoradSatelliteID = %d (err %v); want 25544", attrs.NoradSatelliteID, err)
	}
}

// ListSatellites returns region-less ARNs, so the same satellite comes back
// from every region. Satellites must be one global service: whatever region
// the dispatcher passes, every call stores the same row under Region "global"
// and the row never version-splits.
func TestGroundStationSatellites_OneRowAcrossRegions(t *testing.T) {
	idx := slices.IndexFunc(registeredServices, func(e serviceEntry) bool { return e.name == "aws:ground-station-satellites" })
	if idx < 0 {
		t.Fatal("aws:ground-station-satellites not registered")
	}
	entry := registeredServices[idx]
	if !entry.global {
		t.Error("aws:ground-station-satellites is regional; want global (one call per account)")
	}

	st := newTestStore(t)
	page := stubCall{Output: &groundstation.ListSatellitesOutput{Satellites: []gstypes.SatelliteListItem{gsSatellite("s")}}}
	stub := stubResponses(t, map[string][]stubCall{"ListSatellites": {page, page}})
	var requestRegions []string
	recordRegion := func(s *smithymw.Stack) error {
		if err := s.Initialize.Add(smithymw.InitializeMiddlewareFunc("gsRecordRegion",
			func(ctx context.Context, in smithymw.InitializeInput, next smithymw.InitializeHandler) (smithymw.InitializeOutput, smithymw.Metadata, error) {
				requestRegions = append(requestRegions, awsmw.GetRegion(ctx))
				return next.HandleInitialize(ctx, in)
			}), smithymw.After); err != nil {
			return err
		}
		return stub(s)
	}
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(recordRegion, testRegion)}
	for _, region := range []string{"us-east-1", "eu-west-1"} {
		if _, _, err := entry.fn(context.Background(), acct, region, st, testScanID); err != nil {
			t.Fatalf("fn(%s): %v", region, err)
		}
	}
	if len(requestRegions) != 2 {
		t.Fatalf("ListSatellites sent %d requests; want 2", len(requestRegions))
	}
	for i, r := range requestRegions {
		if r != groundStationSatelliteRegion {
			t.Errorf("request %d went to region %q; want the pinned home %q", i, r, groundStationSatelliteRegion)
		}
	}

	rows := gsRowsByNativeID(t, st, TypeGroundStationSatellite)
	r, ok := rows[gsSatelliteARN("s")]
	if len(rows) != 1 || !ok {
		t.Fatalf("stored %d satellites (found %v); want 1 under %s", len(rows), ok, gsSatelliteARN("s"))
	}
	if sv(r.Region) != "global" {
		t.Errorf("Region = %q; want global", sv(r.Region))
	}
	versions, err := st.GetResourceVersions(store.ResourceID("aws", testAccountID, gsSatelliteARN("s")))
	if err != nil || len(versions) != 1 {
		t.Errorf("GetResourceVersions = %d versions (err %v); want 1", len(versions), err)
	}
}
