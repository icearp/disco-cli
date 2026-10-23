package aws

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/icearp/disco-cli/store"
)

// mexRow is what one stored row must hold.
type mexRow struct {
	name string // "": the row has no Name
	// status is the row's Status; "" for a listing that sets none, or an
	// element without a state.
	status string
	attrs  string
	tags   map[string]string // nil: the row stores no tags
}

// mexPhase is one EC2 region-wide listing stored through storeTokenList.
// page1 answers the first call and carries NextToken elxPageNext; page2
// answers the second and holds an untagged element and one without its key.
type mexPhase struct {
	name, op, sdkOp, typ string
	page1, page2, empty  any
	// request is the MaxResults and NextToken a call sent.
	request func(in any) (int32, *string)
	run     func(context.Context, ec2API, *account, string, *store.Store, string) (int, int, error)
	rows    map[string]mexRow
	// noKey is the key the warning about the element without its key names.
	noKey string
	// page1Row is the row page1 stores.
	page1Row string
	// gapSkipped marks a phase whose first-page region feature gap is an
	// empty listing.
	gapSkipped bool
}

var mexPhases = []mexPhase{mexCapacityExports, mexMeteringPolicies, mexIPAMPools, mexBPAExclusions, mexEndpointServices}

var (
	mexCapacityExports = mexPhase{
		name: "capacity manager data exports", op: "ec2:DescribeCapacityManagerDataExports", sdkOp: "DescribeCapacityManagerDataExports",
		typ: TypeEC2CapacityManagerDataExport,
		page1: &ec2.DescribeCapacityManagerDataExportsOutput{NextToken: ptrStr(elxPageNext), CapacityManagerDataExports: []ec2types.CapacityManagerDataExportResponse{{
			CapacityManagerDataExportId: ptrStr("cmde-1"), OutputFormat: ec2types.OutputFormatParquet,
			Tags: []ec2types.Tag{{Key: ptrStr("env"), Value: ptrStr("prod")}, {Key: ptrStr("team"), Value: ptrStr("cap")}},
		}}},
		page2: &ec2.DescribeCapacityManagerDataExportsOutput{CapacityManagerDataExports: []ec2types.CapacityManagerDataExportResponse{
			{CapacityManagerDataExportId: ptrStr("cmde-2"), OutputFormat: ec2types.OutputFormatCsv},
			{OutputFormat: ec2types.OutputFormatCsv},
		}},
		empty: &ec2.DescribeCapacityManagerDataExportsOutput{},
		request: func(in any) (int32, *string) {
			p := in.(*ec2.DescribeCapacityManagerDataExportsInput)
			return sv32(p.MaxResults), p.NextToken
		},
		run: scanCapacityManagerDataExports,
		rows: map[string]mexRow{
			elxARN("capacity-manager-data-export", "cmde-1"): {
				attrs: `{"CapacityManagerDataExportId":"cmde-1","CreateTime":null,"LatestDeliveryS3LocationUri":null,"LatestDeliveryStatus":"","LatestDeliveryStatusMessage":null,"LatestDeliveryTime":null,"OutputFormat":"parquet","S3BucketName":null,"S3BucketPrefix":null,"Schedule":"","Tags":[{"Key":"env","Value":"prod"},{"Key":"team","Value":"cap"}]}`,
				tags:  map[string]string{"env": "prod", "team": "cap"},
			},
			elxARN("capacity-manager-data-export", "cmde-2"): {
				attrs: `{"CapacityManagerDataExportId":"cmde-2","CreateTime":null,"LatestDeliveryS3LocationUri":null,"LatestDeliveryStatus":"","LatestDeliveryStatusMessage":null,"LatestDeliveryTime":null,"OutputFormat":"csv","S3BucketName":null,"S3BucketPrefix":null,"Schedule":"","Tags":null}`,
			},
		},
		noKey:      "one without a CapacityManagerDataExportId",
		page1Row:   elxARN("capacity-manager-data-export", "cmde-1"),
		gapSkipped: true,
	}
	mexMeteringPolicies = mexPhase{
		name: "transit gateway metering policies", op: "ec2:DescribeTransitGatewayMeteringPolicies", sdkOp: "DescribeTransitGatewayMeteringPolicies",
		typ: TypeEC2TransitGatewayMeteringPolicy,
		page1: &ec2.DescribeTransitGatewayMeteringPoliciesOutput{NextToken: ptrStr(elxPageNext), TransitGatewayMeteringPolicies: []ec2types.TransitGatewayMeteringPolicy{{
			TransitGatewayMeteringPolicyId: ptrStr("tgmp-1"), TransitGatewayId: ptrStr("tgw-1"), State: ec2types.TransitGatewayMeteringPolicyStateAvailable,
			Tags: []ec2types.Tag{{Key: ptrStr("env"), Value: ptrStr("prod")}},
		}}},
		page2: &ec2.DescribeTransitGatewayMeteringPoliciesOutput{TransitGatewayMeteringPolicies: []ec2types.TransitGatewayMeteringPolicy{
			{TransitGatewayMeteringPolicyId: ptrStr("tgmp-2"), TransitGatewayId: ptrStr("tgw-2")},
			{TransitGatewayId: ptrStr("tgw-3")},
		}},
		empty: &ec2.DescribeTransitGatewayMeteringPoliciesOutput{},
		request: func(in any) (int32, *string) {
			p := in.(*ec2.DescribeTransitGatewayMeteringPoliciesInput)
			return sv32(p.MaxResults), p.NextToken
		},
		run: scanTransitGatewayMeteringPolicies,
		rows: map[string]mexRow{
			elxARN("transit-gateway-metering-policy", "tgmp-1"): {
				attrs: `{"MiddleboxAttachmentIds":null,"State":"available","Tags":[{"Key":"env","Value":"prod"}],"TransitGatewayId":"tgw-1","TransitGatewayMeteringPolicyId":"tgmp-1","UpdateEffectiveAt":null}`,
				tags:  map[string]string{"env": "prod"},
			},
			elxARN("transit-gateway-metering-policy", "tgmp-2"): {
				attrs: `{"MiddleboxAttachmentIds":null,"State":"","Tags":null,"TransitGatewayId":"tgw-2","TransitGatewayMeteringPolicyId":"tgmp-2","UpdateEffectiveAt":null}`,
			},
		},
		noKey:    "tgw-3",
		page1Row: elxARN("transit-gateway-metering-policy", "tgmp-1"),
	}
)

// mexHandler answers page1 to the first call and page2 (or fail2) to the
// one carrying page1's token; a call without MaxResults at the page cap fails.
func mexHandler(ph mexPhase, calls *int, fail2 error) func(any) (any, error) {
	return func(in any) (any, error) {
		*calls++
		size, token := ph.request(in)
		if size != 1000 {
			return nil, apiErr("InvalidParameterValue", "MaxResults must be set to the page cap")
		}
		switch sv(token) {
		case "":
			return ph.page1, nil
		case elxPageNext:
			if fail2 != nil {
				return nil, fail2
			}
			return ph.page2, nil
		}
		return nil, apiErr("InvalidPaginationToken", "unexpected token "+sv(token))
	}
}

// Both pages are stored: each row holds its NativeID, Name, the element
// verbatim and its tags (none for the untagged element), and the element
// without its key is warned about once, by that key, instead of stored.
func TestScanEC2RegionListings_StoresTaggedRows(t *testing.T) {
	for _, ph := range mexPhases {
		t.Run(ph.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			calls := 0
			total, inserted, err := elxRun(ph.run, st, map[string]func(any) (any, error){ph.sdkOp: mexHandler(ph, &calls, nil)})
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if total != 2 || inserted != 2 || calls != 2 {
				t.Errorf("total, inserted, calls = %d, %d, %d; want 2, 2, 2", total, inserted, calls)
			}
			mexAssertNotStoredWarning(t, *warns, ph.op, ph.noKey)
			mexAssertRows(t, st, ph.typ, ph.rows)
		})
	}
}

// mexAssertNotStoredWarning fails unless op reported exactly one warning,
// and it names key as the first element not stored.
func mexAssertNotStoredWarning(t *testing.T, warns []store.ScanWarning, op, key string) {
	t.Helper()
	var got []string
	for _, w := range warns {
		if w.Service == op {
			got = append(got, w.Message)
		}
	}
	if len(got) != 1 || !strings.HasSuffix(got[0], "; first "+key) {
		t.Errorf("%s warnings = %q, want one naming first %q", op, got, key)
	}
}

// mexAssertRows fails unless typ's stored rows are exactly want, each with
// its Name, Status, Region, attributes and tags.
func mexAssertRows(t *testing.T, st *store.Store, typ string, want map[string]mexRow) {
	t.Helper()
	rows := rowsByNativeID(t, st, typ)
	if len(rows) != len(want) {
		t.Fatalf("rows = %v, want %d", nativeIDs(rows), len(want))
	}
	for id, w := range want {
		r, ok := rows[id]
		if !ok {
			t.Fatalf("row %s missing; have %v", id, nativeIDs(rows))
		}
		if sv(r.Name) != w.name || (w.name == "" && r.Name != nil) || sv(r.Region) != testRegion {
			t.Errorf("%s name/region = %v/%s, want %q/%s", id, r.Name, sv(r.Region), w.name, testRegion)
		}
		if sv(r.Status) != w.status {
			t.Errorf("%s status = %q, want %q", id, sv(r.Status), w.status)
		}
		assertJSONEqual(t, id+" attributes", r.AttributesJSON, w.attrs)
		if w.tags == nil {
			if r.TagsJSON != nil {
				t.Errorf("%s tags = %s, want none", id, *r.TagsJSON)
			}
			continue
		}
		assertTagsMap(t, r.TagsJSON, w.tags)
	}
}

// Errors: a denial on the first page stores nothing and warns once; after a
// page, a denial keeps it and warns, and any other error keeps it and is
// returned. A region feature gap on the first page is an empty listing where
// the phase treats it so, and is returned otherwise.
func TestScanEC2RegionListings_Errors(t *testing.T) {
	cases := []struct {
		name          string
		first, second error
		wantErr       error
		wantRows      int
		warns         int
	}{
		{"denied first page", elxDenied, nil, nil, 0, 1},
		{"denied second page", nil, elxDenied, nil, 1, 1},
		{"gap second page", nil, elxGap, elxGap, 1, 0},
		{"other second page", nil, errElxTest, errElxTest, 1, 0},
	}
	for _, ph := range mexPhases {
		gap := struct {
			name          string
			first, second error
			wantErr       error
			wantRows      int
			warns         int
		}{"gap first page", elxGap, nil, elxGap, 0, 0}
		if ph.gapSkipped {
			gap.wantErr = nil
		}
		for _, c := range append(slices.Clone(cases), gap) {
			t.Run(ph.name+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warnings := countOpWarnings(st, ph.op)
				calls := 0
				inner := mexHandler(ph, &calls, c.second)
				_, _, err := elxRun(ph.run, st, map[string]func(any) (any, error){ph.sdkOp: func(in any) (any, error) {
					if c.first != nil {
						return nil, c.first
					}
					return inner(in)
				}})
				if c.wantErr == nil && err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if c.wantErr != nil && !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want %v", err, c.wantErr)
				}
				rows := rowsByNativeID(t, st, ph.typ)
				if len(rows) != c.wantRows {
					t.Errorf("rows = %v, want %d", nativeIDs(rows), c.wantRows)
				}
				if _, ok := rows[ph.page1Row]; c.wantRows == 1 && !ok {
					t.Errorf("first page row %s missing", ph.page1Row)
				}
				if *warnings != c.warns {
					t.Errorf("warnings = %d, want %d", *warnings, c.warns)
				}
			})
		}
	}
}

// An empty listing stores nothing; a repeated NextToken ends the listing with
// errRepeatedNextToken and the page read kept.
func TestScanEC2RegionListings_EmptyAndRepeatedToken(t *testing.T) {
	for _, ph := range mexPhases {
		t.Run(ph.name+"/empty", func(t *testing.T) {
			st := newTestStore(t)
			total, _, err := elxRun(ph.run, st, map[string]func(any) (any, error){ph.sdkOp: func(any) (any, error) { return ph.empty, nil }})
			if err != nil || total != 0 {
				t.Fatalf("total, err = %d, %v; want 0, nil", total, err)
			}
		})
		t.Run(ph.name+"/repeated token", func(t *testing.T) {
			st := newTestStore(t)
			calls := 0
			_, _, err := elxRun(ph.run, st, map[string]func(any) (any, error){ph.sdkOp: func(any) (any, error) {
				calls++
				if calls > 3 {
					return nil, errElxTest
				}
				return ph.page1, nil
			}})
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v, want errRepeatedNextToken", err)
			}
			if calls != 2 {
				t.Errorf("calls = %d, want 2 (stop when the token repeats)", calls)
			}
			if _, ok := rowsByNativeID(t, st, ph.typ)[ph.page1Row]; !ok {
				t.Errorf("first page row %s missing", ph.page1Row)
			}
		})
	}
}
