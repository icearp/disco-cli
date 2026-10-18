package aws

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/icearp/disco-cli/store"
)

const (
	lateSnap1     = "snap-0001"
	lateSnap2     = "snap-0002"
	lateZoneA     = "us-east-1a"
	lateZoneB     = "us-east-1b"
	lateImage1    = "ami-0001"
	lateImage2    = "ami-0002"
	lateListing1  = "listing-0001"
	lateListing2  = "listing-0002"
	lateRI        = "ri-0001"
	lateLagID     = "outpost-lag-0001"
	lateOutpost   = "arn:aws:outposts:us-east-1:123456789012:outpost/op-0001"
	lateVif1ARN   = "arn:aws:ec2:us-east-1:123456789012:service-link-virtual-interface/sl-vif-0001"
	lateVif2ARN   = "arn:aws:ec2:us-east-1:123456789012:service-link-virtual-interface/sl-vif-0002"
	latePageNext  = "p2"
	lateOtherZone = "us-west-2"
)

var (
	lateDenied  = apiErr("UnauthorizedOperation", "You are not authorized to perform this operation. User: arn:aws:iam::123456789012:user/x is not authorized to perform: ec2:Describe")
	errLateTest = errors.New("ec2 late test failure")
	lateEnabled = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
)

func lateClient(handlers map[string]func(in any) (any, error)) ec2API {
	return ec2.NewFromConfig(opStubConfig(handlers, lateDenied))
}

func lateFSRID(snap, zone string) string {
	return fastSnapshotRestoreNativeID(testRegion, testAccountID, snap, zone)
}

func lateFLID(image string) string { return fastLaunchImageNativeID(testRegion, testAccountID, image) }

func lateListingID(id string) string {
	return ec2ARN(testRegion, testAccountID, "reserved-instances-listing", id)
}

func lateAttrs(t *testing.T, r store.Resource) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &m); err != nil {
		t.Fatalf("attrs %s: %v", r.AttributesJSON, err)
	}
	return m
}

func lateFSRHandler(in any) (any, error) {
	p := in.(*ec2.DescribeFastSnapshotRestoresInput)
	if sv32(p.MaxResults) != ec2FastSnapshotRestoresPageLimit {
		return nil, apiErr("InvalidParameterValue", "MaxResults must be set to the page cap")
	}
	if !lateFilterIs(p.Filters, "owner-id", testAccountID) {
		return nil, apiErr("InvalidParameterValue", "the owner-id filter must name the scanned account")
	}
	if p.NextToken == nil {
		return &ec2.DescribeFastSnapshotRestoresOutput{NextToken: ptrStr(latePageNext), FastSnapshotRestores: []ec2types.DescribeFastSnapshotRestoreSuccessItem{{
			SnapshotId: ptrStr(lateSnap1), AvailabilityZone: ptrStr(lateZoneA), State: ec2types.FastSnapshotRestoreStateCodeEnabled,
			EnablingTime: &lateEnabled, OwnerId: ptrStr(testAccountID),
		}}}, nil
	}
	return &ec2.DescribeFastSnapshotRestoresOutput{FastSnapshotRestores: []ec2types.DescribeFastSnapshotRestoreSuccessItem{
		{SnapshotId: ptrStr(lateSnap1), AvailabilityZone: ptrStr(lateZoneB), State: ec2types.FastSnapshotRestoreStateCodeDisabling},
		{SnapshotId: ptrStr(lateSnap2), AvailabilityZone: ptrStr(lateZoneA), State: ec2types.FastSnapshotRestoreStateCodeDisabled},
		{SnapshotId: ptrStr(lateSnap2), State: ec2types.FastSnapshotRestoreStateCodeEnabled},
		{AvailabilityZone: ptrStr(lateZoneB), State: ec2types.FastSnapshotRestoreStateCodeEnabled},
	}}, nil
}

func lateFLHandler(in any) (any, error) {
	p := in.(*ec2.DescribeFastLaunchImagesInput)
	if sv32(p.MaxResults) != ec2FastLaunchImagesPageLimit {
		return nil, apiErr("InvalidParameterValue", "MaxResults must be set to the page cap")
	}
	if p.NextToken == nil {
		return &ec2.DescribeFastLaunchImagesOutput{NextToken: ptrStr(latePageNext), FastLaunchImages: []ec2types.DescribeFastLaunchImagesSuccessItem{{
			ImageId: ptrStr(lateImage1), State: ec2types.FastLaunchStateCodeEnabled, MaxParallelLaunches: sdkInt32(6),
		}}}, nil
	}
	return &ec2.DescribeFastLaunchImagesOutput{FastLaunchImages: []ec2types.DescribeFastLaunchImagesSuccessItem{
		{ImageId: ptrStr(lateImage2), State: ec2types.FastLaunchStateCodeEnablingFailed},
		{State: ec2types.FastLaunchStateCodeEnabled},
	}}, nil
}

func lateAllowedHandler(any) (any, error) {
	return &ec2.GetAllowedImagesSettingsOutput{
		State: ptrStr("audit-mode"), ManagedBy: ec2types.ManagedByAccount,
		ImageCriteria: []ec2types.ImageCriterion{{ImageProviders: []string{"amazon"}}},
	}, nil
}

// lateFilterIs reports whether filters is exactly one filter name with values.
func lateFilterIs(filters []ec2types.Filter, name string, values ...string) bool {
	return len(filters) == 1 && sv(filters[0].Name) == name && slices.Equal(filters[0].Values, values)
}

// lateListingsHandler requires the open-status filter, yet also answers with
// cancelled and closed listings to exercise the client-side guard.
func lateListingsHandler(in any) (any, error) {
	if !lateFilterIs(in.(*ec2.DescribeReservedInstancesListingsInput).Filters, "status", "active", "pending") {
		return nil, apiErr("InvalidParameterValue", "the status filter must name the open states")
	}
	return &ec2.DescribeReservedInstancesListingsOutput{ReservedInstancesListings: []ec2types.ReservedInstancesListing{
		{
			ReservedInstancesListingId: ptrStr(lateListing1), ReservedInstancesId: ptrStr(lateRI), Status: ec2types.ListingStatusActive,
			CreateDate: &lateEnabled, Tags: []ec2types.Tag{{Key: ptrStr("team"), Value: ptrStr("fin")}},
		},
		{ReservedInstancesListingId: ptrStr(lateListing2), Status: ec2types.ListingStatusPending},
		{ReservedInstancesListingId: ptrStr("listing-0003"), Status: ec2types.ListingStatusCancelled},
		{ReservedInstancesListingId: ptrStr("listing-0004"), Status: ec2types.ListingStatusClosed},
		{Status: ec2types.ListingStatusActive},
	}}, nil
}

func lateVifHandler(in any) (any, error) {
	p := in.(*ec2.DescribeServiceLinkVirtualInterfacesInput)
	if sv32(p.MaxResults) != ec2ServiceLinkVirtualIfacePageSize {
		return nil, apiErr("InvalidParameterValue", "MaxResults must be set to the page cap")
	}
	if p.NextToken == nil {
		return &ec2.DescribeServiceLinkVirtualInterfacesOutput{NextToken: ptrStr(latePageNext), ServiceLinkVirtualInterfaces: []ec2types.ServiceLinkVirtualInterface{{
			ServiceLinkVirtualInterfaceArn: ptrStr(lateVif1ARN), OutpostLagId: ptrStr(lateLagID), OutpostArn: ptrStr(lateOutpost),
			ConfigurationState: ec2types.ServiceLinkVirtualInterfaceConfigurationStateAvailable,
			Tags:               []ec2types.Tag{{Key: ptrStr("Name"), Value: ptrStr("uplink-a")}},
		}}}, nil
	}
	return &ec2.DescribeServiceLinkVirtualInterfacesOutput{ServiceLinkVirtualInterfaces: []ec2types.ServiceLinkVirtualInterface{
		{ServiceLinkVirtualInterfaceArn: ptrStr(lateVif2ARN), ConfigurationState: ec2types.ServiceLinkVirtualInterfaceConfigurationStatePending},
		{
			ServiceLinkVirtualInterfaceArn: ptrStr("arn:aws:ec2:us-east-1:123456789012:service-link-virtual-interface/sl-vif-0003"),
			ConfigurationState:             ec2types.ServiceLinkVirtualInterfaceConfigurationStateDeleted,
		},
		{ServiceLinkVirtualInterfaceId: ptrStr("sl-vif-0004"), ConfigurationState: ec2types.ServiceLinkVirtualInterfaceConfigurationStateAvailable},
	}}, nil
}

func sdkInt32(v int32) *int32 { return &v }

func TestScanEC2FastSnapshotRestores_StoresEnabledPairsAcrossPages(t *testing.T) {
	st := newTestStore(t)
	total, _, err := scanEC2FastSnapshotRestores(context.Background(), lateClient(map[string]func(any) (any, error){
		"DescribeFastSnapshotRestores": lateFSRHandler,
	}), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanEC2FastSnapshotRestores: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	rows := rowsByNativeID(t, st, TypeEC2FastSnapshotRestore)
	if got, want := nativeIDs(rows), []string{lateFSRID(lateSnap1, lateZoneA), lateFSRID(lateSnap1, lateZoneB)}; !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v (disabled, zone-less and snapshot-less entries dropped)", got, want)
	}
	r := rows[lateFSRID(lateSnap1, lateZoneA)]
	if sv(r.Status) != "enabled" || sv(r.Region) != testRegion || sv(r.CreatedAt) != "2026-03-04T05:06:07Z" {
		t.Errorf("status/region/created = %q/%q/%q, want enabled/%s/2026-03-04T05:06:07Z", sv(r.Status), sv(r.Region), sv(r.CreatedAt), testRegion)
	}
	if got := lateAttrs(t, r)["OwnerId"]; got != testAccountID {
		t.Errorf("attrs OwnerId = %v, want %s", got, testAccountID)
	}
	if got := sv(rows[lateFSRID(lateSnap1, lateZoneB)].Status); got != "disabling" {
		t.Errorf("zone b status = %q, want disabling", got)
	}
}

func TestScanEC2FastLaunchImages_StoresConfigsAcrossPages(t *testing.T) {
	st := newTestStore(t)
	total, _, err := scanEC2FastLaunchImages(context.Background(), lateClient(map[string]func(any) (any, error){
		"DescribeFastLaunchImages": lateFLHandler,
	}), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanEC2FastLaunchImages: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	rows := rowsByNativeID(t, st, TypeEC2FastLaunchImage)
	if got, want := nativeIDs(rows), []string{lateFLID(lateImage1), lateFLID(lateImage2)}; !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v (image-less entry dropped)", got, want)
	}
	r := rows[lateFLID(lateImage1)]
	if sv(r.Status) != "enabled" || sv(r.Region) != testRegion {
		t.Errorf("status/region = %q/%q, want enabled/%s", sv(r.Status), sv(r.Region), testRegion)
	}
	if got := lateAttrs(t, r)["MaxParallelLaunches"]; got != float64(6) {
		t.Errorf("attrs MaxParallelLaunches = %v, want 6", got)
	}
	if got := sv(rows[lateFLID(lateImage2)].Status); got != "enabling-failed" {
		t.Errorf("image 2 status = %q, want enabling-failed", got)
	}
}

func TestEC2LateNativeIDShapes(t *testing.T) {
	if got, want := lateFSRID(lateSnap1, lateZoneA), "arn:aws:ec2:us-east-1:123456789012:snapshot/snap-0001/fast-snapshot-restore/us-east-1a"; got != want {
		t.Errorf("fast snapshot restore NativeID = %q, want %q", got, want)
	}
	if got, want := lateFLID(lateImage1), "arn:aws:ec2:us-east-1:123456789012:image/ami-0001/fast-launch"; got != want {
		t.Errorf("fast launch NativeID = %q, want %q", got, want)
	}
}

// The setting is stored once per Region, provider-managed, keyed so that two
// Regions do not share one row.
func TestScanEC2AllowedImagesSetting_StoresManagedRowPerRegion(t *testing.T) {
	st := newTestStore(t)
	client := lateClient(map[string]func(any) (any, error){"GetAllowedImagesSettings": lateAllowedHandler})
	for _, region := range []string{testRegion, lateOtherZone} {
		total, _, err := scanEC2AllowedImagesSetting(context.Background(), client, newTestAccount(testAccountID), region, st, testScanID)
		if err != nil || total != 1 {
			t.Fatalf("%s: total, err = %d, %v; want 1, nil", region, total, err)
		}
	}
	rows := rowsByNativeID(t, st, TypeEC2AllowedImagesSetting)
	east := "arn:aws:ec2:us-east-1:123456789012:allowed-images-setting/123456789012"
	west := "arn:aws:ec2:us-west-2:123456789012:allowed-images-setting/123456789012"
	if got, want := nativeIDs(rows), []string{east, west}; !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	r := rows[east]
	if sv(r.Status) != "audit-mode" || sv(r.Region) != testRegion || !r.ManagedByProvider {
		t.Errorf("status/region/managed = %q/%q/%v, want audit-mode/%s/true", sv(r.Status), sv(r.Region), r.ManagedByProvider, testRegion)
	}
	if got := sv(rows[west].Region); got != lateOtherZone {
		t.Errorf("west region = %q, want %s", got, lateOtherZone)
	}
	if got := lateAttrs(t, r)["ManagedBy"]; got != "account" {
		t.Errorf("attrs ManagedBy = %v, want account", got)
	}
}

func TestScanEC2ReservedInstancesListings_StoresOpenListings(t *testing.T) {
	st := newTestStore(t)
	total, _, err := scanEC2ReservedInstancesListings(context.Background(), lateClient(map[string]func(any) (any, error){
		"DescribeReservedInstancesListings": lateListingsHandler,
	}), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanEC2ReservedInstancesListings: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	rows := rowsByNativeID(t, st, TypeEC2ReservedInstancesListing)
	if got, want := nativeIDs(rows), []string{lateListingID(lateListing1), lateListingID(lateListing2)}; !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v (cancelled, closed and id-less listings dropped)", got, want)
	}
	r := rows[lateListingID(lateListing1)]
	if sv(r.Status) != "active" || sv(r.Region) != testRegion || sv(r.CreatedAt) != "2026-03-04T05:06:07Z" {
		t.Errorf("status/region/created = %q/%q/%q, want active/%s/2026-03-04T05:06:07Z", sv(r.Status), sv(r.Region), sv(r.CreatedAt), testRegion)
	}
	if got := sv(r.TagsJSON); got != `{"team":"fin"}` {
		t.Errorf("tags = %s, want {\"team\":\"fin\"}", got)
	}
	if got := lateAttrs(t, r)["ReservedInstancesId"]; got != lateRI {
		t.Errorf("attrs ReservedInstancesId = %v, want %s", got, lateRI)
	}
	if got := sv(rows[lateListingID(lateListing2)].Status); got != "pending" {
		t.Errorf("listing 2 status = %q, want pending", got)
	}
}

func TestScanEC2ServiceLinkVirtualInterfaces_StoresAcrossPages(t *testing.T) {
	st := newTestStore(t)
	total, _, err := scanEC2ServiceLinkVirtualInterfaces(context.Background(), lateClient(map[string]func(any) (any, error){
		"DescribeServiceLinkVirtualInterfaces": lateVifHandler,
	}), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanEC2ServiceLinkVirtualInterfaces: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	rows := rowsByNativeID(t, st, TypeEC2ServiceLinkVirtualInterface)
	if got, want := nativeIDs(rows), []string{lateVif1ARN, lateVif2ARN}; !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v (deleted and ARN-less interfaces dropped)", got, want)
	}
	r := rows[lateVif1ARN]
	if sv(r.Name) != "uplink-a" || sv(r.Status) != "available" || sv(r.Region) != testRegion {
		t.Errorf("name/status/region = %q/%q/%q, want uplink-a/available/%s", sv(r.Name), sv(r.Status), sv(r.Region), testRegion)
	}
	if got := lateAttrs(t, r)["OutpostLagId"]; got != lateLagID {
		t.Errorf("attrs OutpostLagId = %v, want %s", got, lateLagID)
	}
	if got := sv(rows[lateVif2ARN].Status); got != "pending" {
		t.Errorf("interface 2 status = %q, want pending", got)
	}
}

// A repeated NextToken ends the loop instead of spinning.
func TestScanEC2ServiceLinkVirtualInterfaces_RepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	calls := 0
	_, _, err := scanEC2ServiceLinkVirtualInterfaces(context.Background(), lateClient(map[string]func(any) (any, error){
		"DescribeServiceLinkVirtualInterfaces": func(any) (any, error) {
			calls++
			if calls > 3 {
				return nil, errLateTest
			}
			return &ec2.DescribeServiceLinkVirtualInterfacesOutput{NextToken: ptrStr(latePageNext), ServiceLinkVirtualInterfaces: []ec2types.ServiceLinkVirtualInterface{{
				ServiceLinkVirtualInterfaceArn: ptrStr(lateVif1ARN), ConfigurationState: ec2types.ServiceLinkVirtualInterfaceConfigurationStateAvailable,
			}}}, nil
		},
	}), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanEC2ServiceLinkVirtualInterfaces: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (stop when the token repeats)", calls)
	}
}

// lateFirstPage reports whether in asks for the first page of a paginated op.
func lateFirstPage(in any) bool {
	switch p := in.(type) {
	case *ec2.DescribeFastSnapshotRestoresInput:
		return p.NextToken == nil
	case *ec2.DescribeFastLaunchImagesInput:
		return p.NextToken == nil
	case *ec2.DescribeServiceLinkVirtualInterfacesInput:
		return p.NextToken == nil
	}
	return false
}

type latePhase struct {
	name, op, sdkOp, typ string
	run                  func(context.Context, ec2API, *account, string, *store.Store, string) (int, int, error)
	empty                any
	handler              func(any) (any, error)
	// firstPageID is a row of the first page; "" for an op with one page.
	firstPageID string
}

var latePhases = []latePhase{
	{
		name: "fast snapshot restore", op: ec2FastSnapshotRestoresOp, sdkOp: "DescribeFastSnapshotRestores", typ: TypeEC2FastSnapshotRestore,
		run: scanEC2FastSnapshotRestores, empty: &ec2.DescribeFastSnapshotRestoresOutput{}, handler: lateFSRHandler,
		firstPageID: lateFSRID(lateSnap1, lateZoneA),
	},
	{
		name: "fast launch image", op: ec2FastLaunchImagesOp, sdkOp: "DescribeFastLaunchImages", typ: TypeEC2FastLaunchImage,
		run: scanEC2FastLaunchImages, empty: &ec2.DescribeFastLaunchImagesOutput{}, handler: lateFLHandler,
		firstPageID: lateFLID(lateImage1),
	},
	{
		name: "allowed images setting", op: ec2AllowedImagesSettingsOp, sdkOp: "GetAllowedImagesSettings", typ: TypeEC2AllowedImagesSetting,
		run: scanEC2AllowedImagesSetting, handler: lateAllowedHandler,
	},
	{
		name: "reserved instances listing", op: ec2ReservedInstancesListingsOp, sdkOp: "DescribeReservedInstancesListings", typ: TypeEC2ReservedInstancesListing,
		run: scanEC2ReservedInstancesListings, empty: &ec2.DescribeReservedInstancesListingsOutput{}, handler: lateListingsHandler,
	},
	{
		name: "service link virtual interface", op: ec2ServiceLinkVirtualInterfacesOp, sdkOp: "DescribeServiceLinkVirtualInterfaces", typ: TypeEC2ServiceLinkVirtualInterface,
		run: scanEC2ServiceLinkVirtualInterfaces, empty: &ec2.DescribeServiceLinkVirtualInterfacesOutput{}, handler: lateVifHandler,
		firstPageID: lateVif1ARN,
	},
}

// Every late phase: an empty result stores nothing, a denial warns once, a
// first-page feature gap skips silently, anything else is returned, and an
// error after a good page keeps that page's rows. The Allowed AMIs setting is
// a singleton with no empty result.
func TestScanEC2LatePhases_Errors(t *testing.T) {
	for _, ph := range latePhases {
		run := func(c ec2API, st *store.Store) (int, int, error) {
			return ph.run(context.Background(), c, newTestAccount(testAccountID), testRegion, st, testScanID)
		}
		if ph.empty != nil {
			t.Run(ph.name+"/empty", func(t *testing.T) {
				st := newTestStore(t)
				total, _, err := run(lateClient(map[string]func(any) (any, error){ph.sdkOp: func(any) (any, error) { return ph.empty, nil }}), st)
				if err != nil || total != 0 {
					t.Fatalf("total, err = %d, %v; want 0, nil", total, err)
				}
				if n := len(rowsByNativeID(t, st, ph.typ)); n != 0 {
					t.Errorf("rows = %d, want 0", n)
				}
			})
		}
		t.Run(ph.name+"/denied", func(t *testing.T) {
			st := newTestStore(t)
			warnings := countOpWarnings(st, ph.op)
			if _, _, err := run(lateClient(nil), st); err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if *warnings != 1 {
				t.Errorf("%s warnings = %d, want 1", ph.op, *warnings)
			}
		})
		t.Run(ph.name+"/feature gap", func(t *testing.T) {
			st := newTestStore(t)
			var warned int
			st.OnWarn = func(store.ScanWarning) { warned++ }
			_, _, err := run(lateClient(map[string]func(any) (any, error){
				ph.sdkOp: func(any) (any, error) {
					return nil, apiErr("InvalidAction", "The action is not valid for this web service.")
				},
			}), st)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if warned != 0 {
				t.Errorf("warnings = %d, want 0", warned)
			}
		})
		t.Run(ph.name+"/other error", func(t *testing.T) {
			st := newTestStore(t)
			_, _, err := run(lateClient(map[string]func(any) (any, error){
				ph.sdkOp: func(any) (any, error) { return nil, apiErr("InvalidParameterValue", "bad") },
			}), st)
			if !isAPIErrorCode(err, "InvalidParameterValue") {
				t.Fatalf("err = %v, want the InvalidParameterValue error", err)
			}
		})
		if ph.firstPageID == "" {
			continue
		}
		t.Run(ph.name+"/error after a page", func(t *testing.T) {
			st := newTestStore(t)
			total, _, err := run(lateClient(map[string]func(any) (any, error){
				ph.sdkOp: func(in any) (any, error) {
					if !lateFirstPage(in) {
						return nil, errLateTest
					}
					return ph.handler(in)
				},
			}), st)
			if !errors.Is(err, errLateTest) {
				t.Fatalf("err = %v, want the second-page error", err)
			}
			if total != 1 {
				t.Errorf("total = %d, want 1", total)
			}
			if _, ok := rowsByNativeID(t, st, ph.typ)[ph.firstPageID]; !ok {
				t.Errorf("first-page row %s missing", ph.firstPageID)
			}
		})
	}
}

// lateOlderSnapshot is a snapshot row an older category (compute mgmt) stores.
const lateOlderSnapshot = "snap-older"

// lateTopLevelHandlers answers every late op plus DescribeSnapshots, for an
// older-category row; scanEC2 runs every other EC2 op against the denial.
func lateTopLevelHandlers() map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"DescribeSnapshots": func(any) (any, error) {
			return &ec2.DescribeSnapshotsOutput{Snapshots: []ec2types.Snapshot{{SnapshotId: ptrStr(lateOlderSnapshot)}}}, nil
		},
	}
	for _, ph := range latePhases {
		h[ph.sdkOp] = ph.handler
	}
	return h
}

func lateScanEC2(st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, lateDenied)
	total, _, err := scanEC2(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanEC2_StoresLateRows(t *testing.T) {
	st := newTestStore(t)
	total, err := lateScanEC2(st, lateTopLevelHandlers())
	if err != nil {
		t.Fatalf("scanEC2: %v", err)
	}
	// 1 snapshot + 2 restores + 2 fast launch images + 1 setting + 2 listings + 2 interfaces
	if total != 10 {
		t.Errorf("total = %d, want 10", total)
	}
	for typ, want := range map[string]int{
		TypeEC2FastSnapshotRestore: 2, TypeEC2FastLaunchImage: 2, TypeEC2AllowedImagesSetting: 1,
		TypeEC2ReservedInstancesListing: 2, TypeEC2ServiceLinkVirtualInterface: 2,
	} {
		if n := len(rowsByNativeID(t, st, typ)); n != want {
			t.Errorf("%s rows = %d, want %d", typ, n, want)
		}
	}
}

// The late phases run after every older category: a hard failure of one
// leaves an older category's slow call uncancelled, does not stop the other
// late phases, and an older category's failure does not skip them.
func TestScanEC2_LatePhasesAfterOlderCategories(t *testing.T) {
	snapshotID := ec2ARN(testRegion, testAccountID, "snapshot", lateOlderSnapshot)
	for _, failed := range latePhases {
		t.Run(failed.name+" fails", func(t *testing.T) {
			st := newTestStore(t)
			h := lateTopLevelHandlers()
			h[failed.sdkOp] = func(any) (any, error) { return nil, errLateTest }
			ctxHandlers := ignoreCtx(h)
			// DescribeSnapshots answers only after a pause, and gives up if
			// its context is cancelled first, as it would be by a sibling
			// failure.
			ctxHandlers["DescribeSnapshots"] = func(ctx context.Context, in any) (any, error) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(200 * time.Millisecond):
					return h["DescribeSnapshots"](in)
				}
			}
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfigCtx(ctxHandlers, lateDenied)
			_, _, err := scanEC2(context.Background(), acct, testRegion, st, testScanID)
			if !errors.Is(err, errLateTest) {
				t.Fatalf("err = %v, want the %s error", err, failed.sdkOp)
			}
			if _, ok := rowsByNativeID(t, st, TypeEC2Snapshot)[snapshotID]; !ok {
				t.Errorf("snapshot row missing: the %s failure cancelled an older category", failed.sdkOp)
			}
			for _, other := range latePhases {
				if other.name != failed.name && len(rowsByNativeID(t, st, other.typ)) == 0 {
					t.Errorf("%s rows missing after the %s failure", other.typ, failed.sdkOp)
				}
			}
		})
	}
	t.Run("older category fails", func(t *testing.T) {
		st := newTestStore(t)
		h := lateTopLevelHandlers()
		h["DescribeSnapshots"] = func(any) (any, error) { return nil, errLateTest }
		_, err := lateScanEC2(st, h)
		if !errors.Is(err, errLateTest) {
			t.Fatalf("err = %v, want the DescribeSnapshots error", err)
		}
		for _, ph := range latePhases {
			if len(rowsByNativeID(t, st, ph.typ)) == 0 {
				t.Errorf("%s rows missing after the older-category failure", ph.typ)
			}
		}
	})
}

// A transient error on any side of the join is demoted to one warning, so it
// cannot make the dispatcher read another side's hard error as transient.
func TestScanEC2_LateTransientDoesNotMaskHardError(t *testing.T) {
	type tc struct{ name, transientOp, hardOp, warnOp string }
	cases := []tc{{"transient older category", "DescribeSnapshots", "DescribeFastSnapshotRestores", "ec2"}}
	for _, ph := range latePhases {
		cases = append(cases, tc{"transient " + ph.name, ph.sdkOp, "DescribeSnapshots", ph.op})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			h := lateTopLevelHandlers()
			h[c.transientOp] = func(any) (any, error) { return nil, io.EOF }
			h[c.hardOp] = func(any) (any, error) { return nil, errLateTest }
			warnings := countOpWarnings(st, c.warnOp)
			_, err := lateScanEC2(st, h)
			if !errors.Is(err, errLateTest) {
				t.Fatalf("err = %v, want the %s error", err, c.hardOp)
			}
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError = %v, want outcomeError", got)
			}
			if *warnings != 1 {
				t.Errorf("%s warnings = %d, want 1", c.warnOp, *warnings)
			}
		})
	}
}
