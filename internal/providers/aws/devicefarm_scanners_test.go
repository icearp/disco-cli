package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/devicefarm"
	dftypes "github.com/aws/aws-sdk-go-v2/service/devicefarm/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

type stubDeviceFarm struct {
	deviceFarmAPI
	projects     []dftypes.Project
	pools        []dftypes.DevicePool
	netProfiles  []dftypes.NetworkProfile
	instProfiles []dftypes.InstanceProfile
	devInstances []dftypes.DeviceInstance
	vpces        []dftypes.VPCEConfiguration
	testGrids    []dftypes.TestGridProject
}

func (s *stubDeviceFarm) ListProjects(_ context.Context, _ *devicefarm.ListProjectsInput, _ ...func(*devicefarm.Options)) (*devicefarm.ListProjectsOutput, error) {
	return &devicefarm.ListProjectsOutput{Projects: s.projects}, nil
}

func (s *stubDeviceFarm) ListDevicePools(_ context.Context, _ *devicefarm.ListDevicePoolsInput, _ ...func(*devicefarm.Options)) (*devicefarm.ListDevicePoolsOutput, error) {
	return &devicefarm.ListDevicePoolsOutput{DevicePools: s.pools}, nil
}

func (s *stubDeviceFarm) ListNetworkProfiles(_ context.Context, _ *devicefarm.ListNetworkProfilesInput, _ ...func(*devicefarm.Options)) (*devicefarm.ListNetworkProfilesOutput, error) {
	return &devicefarm.ListNetworkProfilesOutput{NetworkProfiles: s.netProfiles}, nil
}

func (s *stubDeviceFarm) ListInstanceProfiles(_ context.Context, _ *devicefarm.ListInstanceProfilesInput, _ ...func(*devicefarm.Options)) (*devicefarm.ListInstanceProfilesOutput, error) {
	return &devicefarm.ListInstanceProfilesOutput{InstanceProfiles: s.instProfiles}, nil
}

func (s *stubDeviceFarm) ListDeviceInstances(_ context.Context, _ *devicefarm.ListDeviceInstancesInput, _ ...func(*devicefarm.Options)) (*devicefarm.ListDeviceInstancesOutput, error) {
	return &devicefarm.ListDeviceInstancesOutput{DeviceInstances: s.devInstances}, nil
}

func (s *stubDeviceFarm) ListVPCEConfigurations(_ context.Context, _ *devicefarm.ListVPCEConfigurationsInput, _ ...func(*devicefarm.Options)) (*devicefarm.ListVPCEConfigurationsOutput, error) {
	return &devicefarm.ListVPCEConfigurationsOutput{VpceConfigurations: s.vpces}, nil
}

func (s *stubDeviceFarm) ListTestGridProjects(_ context.Context, _ *devicefarm.ListTestGridProjectsInput, _ ...func(*devicefarm.Options)) (*devicefarm.ListTestGridProjectsOutput, error) {
	return &devicefarm.ListTestGridProjectsOutput{TestGridProjects: s.testGrids}, nil
}

func TestScanDeviceFarmPhases(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-west-2"
	projARN := "arn:aws:devicefarm:us-west-2:111111111111:project:PROJ-1"
	stub := &stubDeviceFarm{
		projects:     []dftypes.Project{{Arn: ptrStr(projARN), Name: ptrStr("p1")}},
		pools:        []dftypes.DevicePool{{Arn: ptrStr(projARN + "/POOL-1"), Name: ptrStr("pool1")}},
		instProfiles: []dftypes.InstanceProfile{{Arn: ptrStr("arn:aws:devicefarm:us-west-2:111111111111:instanceprofile:IP-1"), Name: ptrStr("ip1")}},
		testGrids:    []dftypes.TestGridProject{{Arn: ptrStr("arn:aws:devicefarm:us-west-2:111111111111:testgrid-project:TG-1"), Name: ptrStr("tg1")}},
	}

	arns, _, _, err := scanDeviceFarmProjects(context.Background(), stub, acct, region, st, testScanID)
	if err != nil || len(arns) != 1 {
		t.Fatalf("scanDeviceFarmProjects: arns=%v err=%v", arns, err)
	}
	if _, _, err := scanDeviceFarmDevicePools(context.Background(), stub, acct, region, st, testScanID, arns[0]); err != nil {
		t.Fatalf("scanDeviceFarmDevicePools: %v", err)
	}
	if _, _, err := scanDeviceFarmInstanceProfiles(context.Background(), stub, acct, region, st, testScanID); err != nil {
		t.Fatalf("scanDeviceFarmInstanceProfiles: %v", err)
	}
	if _, _, err := scanDeviceFarmTestGridProjects(context.Background(), stub, acct, region, st, testScanID); err != nil {
		t.Fatalf("scanDeviceFarmTestGridProjects: %v", err)
	}
	for _, typ := range []string{TypeDeviceFarmProject, TypeDeviceFarmDevicePool, TypeDeviceFarmInstanceProfile, TypeDeviceFarmTestGridProject} {
		rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{typ}, Limit: util.AllResources})
		if err != nil {
			t.Fatalf("ListResources %s: %v", typ, err)
		}
		if len(rows) != 1 {
			t.Errorf("%s: got %d rows, want 1", typ, len(rows))
		}
	}
}

const testDFRegion = "us-west-2"

func testDFProjectARN(guid string) string {
	return "arn:aws:devicefarm:" + testDFRegion + ":" + testAccountID + ":project:" + guid
}

func testDFUploadARN(projectGUID, uploadGUID string) string {
	return "arn:aws:devicefarm:" + testDFRegion + ":" + testAccountID + ":upload:" + projectGUID + "/" + uploadGUID
}

func testDFUpload(projectGUID, uploadGUID string) dftypes.Upload {
	created := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	return dftypes.Upload{
		Arn:      sdkaws.String(testDFUploadARN(projectGUID, uploadGUID)),
		Name:     sdkaws.String(uploadGUID + ".apk"),
		Type:     dftypes.UploadTypeAndroidApp,
		Status:   dftypes.UploadStatusSucceeded,
		Category: dftypes.UploadCategoryPrivate,
		Created:  &created,
		Url:      sdkaws.String("https://prod-us-west-2-uploads.s3-us-west-2.amazonaws.com/x?X-Amz-Signature=deadbeef"),
	}
}

// fakeDeviceFarmUploads serves ListUploads pages keyed by project ARN then
// NextToken ("" for the first page), and records every project queried.
type fakeDeviceFarmUploads struct {
	deviceFarmAPI
	pages   map[string]map[string]*devicefarm.ListUploadsOutput
	errs    map[string]error
	queried []string
}

func (f *fakeDeviceFarmUploads) ListUploads(_ context.Context, in *devicefarm.ListUploadsInput, _ ...func(*devicefarm.Options)) (*devicefarm.ListUploadsOutput, error) {
	arn := sdkaws.ToString(in.Arn)
	f.queried = append(f.queried, arn)
	if err := f.errs[arn]; err != nil {
		return nil, err
	}
	if out := f.pages[arn][sdkaws.ToString(in.NextToken)]; out != nil {
		return out, nil
	}
	return &devicefarm.ListUploadsOutput{}, nil
}

func deviceFarmUploadRows(t *testing.T, st *store.Store) map[string]store.Resource {
	t.Helper()
	rows, err := st.ListResources(store.ResourceFilter{Types: []string{TypeDeviceFarmUpload}, Limit: util.AllResources})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	byNativeID := make(map[string]store.Resource, len(rows))
	for _, r := range rows {
		byNativeID[r.NativeID] = r
	}
	return byNativeID
}

func TestScanDeviceFarmUploads_PaginatesEveryProject(t *testing.T) {
	st := newTestStore(t)
	projA, projB := testDFProjectARN("PA"), testDFProjectARN("PB")
	fake := &fakeDeviceFarmUploads{pages: map[string]map[string]*devicefarm.ListUploadsOutput{
		projA: {
			"":   {Uploads: []dftypes.Upload{testDFUpload("PA", "U1")}, NextToken: sdkaws.String("p2")},
			"p2": {Uploads: []dftypes.Upload{testDFUpload("PA", "U2"), {Name: sdkaws.String("no-arn")}}},
		},
		projB: {"": {Uploads: []dftypes.Upload{testDFUpload("PB", "U3")}}},
	}}

	total, _, err := scanDeviceFarmUploads(context.Background(), fake, newTestAccount(testAccountID), testDFRegion, st, testScanID, []string{projA, projB})
	if err != nil {
		t.Fatalf("scanDeviceFarmUploads: %v", err)
	}
	if want := []string{projA, projA, projB}; !slices.Equal(fake.queried, want) {
		t.Errorf("queried projects = %v; want %v (two pages of PA, one of PB)", fake.queried, want)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3 (both pages of PA plus PB; ARN-less upload skipped)", total)
	}
	rows := deviceFarmUploadRows(t, st)
	if len(rows) != 3 {
		t.Errorf("stored %d upload rows; want 3", len(rows))
	}
	for _, arn := range []string{testDFUploadARN("PA", "U1"), testDFUploadARN("PA", "U2"), testDFUploadARN("PB", "U3")} {
		if _, ok := rows[arn]; !ok {
			t.Errorf("upload %s not stored", arn)
		}
	}
	r := rows[testDFUploadARN("PA", "U1")]
	if r.Type != TypeDeviceFarmUpload {
		t.Errorf("Type = %q; want %q", r.Type, TypeDeviceFarmUpload)
	}
	if got := sdkaws.ToString(r.Region); got != testDFRegion {
		t.Errorf("Region = %q; want %q", got, testDFRegion)
	}
	if got := sdkaws.ToString(r.Name); got != "U1.apk" {
		t.Errorf("Name = %q; want U1.apk", got)
	}
	if got := sdkaws.ToString(r.Status); got != string(dftypes.UploadStatusSucceeded) {
		t.Errorf("Status = %q; want %q", got, dftypes.UploadStatusSucceeded)
	}
	if got := sdkaws.ToString(r.CreatedAt); got != "2026-09-01T12:00:00Z" {
		t.Errorf("CreatedAt = %q; want 2026-09-01T12:00:00Z", got)
	}
	var attrs dftypes.Upload
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("unmarshal attrs: %v", err)
	}
	if attrs.Type != dftypes.UploadTypeAndroidApp {
		t.Errorf("attrs Type = %q; want %q", attrs.Type, dftypes.UploadTypeAndroidApp)
	}
}

func TestScanDeviceFarmUploads_Empty(t *testing.T) {
	st := newTestStore(t)
	fake := &fakeDeviceFarmUploads{}

	total, _, err := scanDeviceFarmUploads(context.Background(), fake, newTestAccount(testAccountID), testDFRegion, st, testScanID, []string{testDFProjectARN("PA")})
	if err != nil {
		t.Fatalf("scanDeviceFarmUploads: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d; want 0", total)
	}
	if n := len(deviceFarmUploadRows(t, st)); n != 0 {
		t.Errorf("stored %d upload rows; want 0", n)
	}
}

// A project deleted between ListProjects and ListUploads is skipped without a
// warning; its siblings are still listed.
func TestScanDeviceFarmUploads_VanishedProjectSkipped(t *testing.T) {
	st := newTestStore(t)
	warned := false
	st.OnWarn = func(store.ScanWarning) { warned = true }
	gone, live := testDFProjectARN("GONE"), testDFProjectARN("LIVE")
	fake := &fakeDeviceFarmUploads{
		errs:  map[string]error{gone: apiErr("NotFoundException", "project not found")},
		pages: map[string]map[string]*devicefarm.ListUploadsOutput{live: {"": {Uploads: []dftypes.Upload{testDFUpload("LIVE", "U1")}}}},
	}

	total, _, err := scanDeviceFarmUploads(context.Background(), fake, newTestAccount(testAccountID), testDFRegion, st, testScanID, []string{gone, live})
	if err != nil {
		t.Fatalf("a vanished project must not fail the phase, got %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d; want 1 from the live project", total)
	}
	if _, ok := deviceFarmUploadRows(t, st)[testDFUploadARN("LIVE", "U1")]; !ok {
		t.Error("live project's upload not stored")
	}
	if warned {
		t.Error("a vanished project must not record a scan warning")
	}
}

// ListUploads can be denied per project, so a denial warns once and the
// remaining projects are still listed.
func TestScanDeviceFarmUploads_AccessDeniedWarnsOnceAndContinues(t *testing.T) {
	st := newTestStore(t)
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	deny := apiErr("AccessDeniedException", "User: arn:aws:sts::123456789012:assumed-role/x is not authorized to perform: devicefarm:ListUploads")
	d1, d2, permitted := testDFProjectARN("D1"), testDFProjectARN("D2"), testDFProjectARN("OK")
	fake := &fakeDeviceFarmUploads{
		errs:  map[string]error{d1: deny, d2: deny},
		pages: map[string]map[string]*devicefarm.ListUploadsOutput{permitted: {"": {Uploads: []dftypes.Upload{testDFUpload("OK", "U1")}}}},
	}

	total, _, err := scanDeviceFarmUploads(context.Background(), fake, newTestAccount(testAccountID), testDFRegion, st, testScanID, []string{d1, d2, permitted})
	if err != nil {
		t.Fatalf("access denied must not fail the phase, got %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d; want 1 from the permitted project", total)
	}
	if len(warnings) != 1 {
		t.Errorf("recorded %d warnings; want exactly 1", len(warnings))
	}
}

func TestScanDeviceFarmUploads_UnexpectedErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	boom := apiErr("ServiceAccountException", "boom")
	proj := testDFProjectARN("PA")
	fake := &fakeDeviceFarmUploads{errs: map[string]error{proj: boom}}

	_, _, err := scanDeviceFarmUploads(context.Background(), fake, newTestAccount(testAccountID), testDFRegion, st, testScanID, []string{proj})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v; want it to wrap %v", err, boom)
	}
}

// scanDeviceFarm hands the projects it listed to the uploads phase, and the
// stored upload carries the redacted presigned URL rather than its signature.
func TestScanDeviceFarm_ListsUploadsOfListedProjects(t *testing.T) {
	st := newTestStore(t)
	proj := testDFProjectARN("PA")
	stub := stubResponses(t, map[string][]stubCall{
		"ListProjects":           {{Output: &devicefarm.ListProjectsOutput{Projects: []dftypes.Project{{Arn: sdkaws.String(proj), Name: sdkaws.String("p")}}}}},
		"ListDevicePools":        {{Output: &devicefarm.ListDevicePoolsOutput{}}},
		"ListNetworkProfiles":    {{Output: &devicefarm.ListNetworkProfilesOutput{}}},
		"ListInstanceProfiles":   {{Output: &devicefarm.ListInstanceProfilesOutput{}}},
		"ListDeviceInstances":    {{Output: &devicefarm.ListDeviceInstancesOutput{}}},
		"ListVPCEConfigurations": {{Output: &devicefarm.ListVPCEConfigurationsOutput{}}},
		"ListTestGridProjects":   {{Output: &devicefarm.ListTestGridProjectsOutput{}}},
		"ListUploads":            {{Output: &devicefarm.ListUploadsOutput{Uploads: []dftypes.Upload{testDFUpload("PA", "U1")}}}},
	})
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(stub, testDFRegion)}

	total, _, err := scanDeviceFarm(context.Background(), acct, "", st, testScanID)
	if err != nil {
		t.Fatalf("scanDeviceFarm: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2 (project + upload)", total)
	}
	r, ok := deviceFarmUploadRows(t, st)[testDFUploadARN("PA", "U1")]
	if !ok {
		t.Fatal("upload row not stored by scanDeviceFarm")
	}
	var attrs dftypes.Upload
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("unmarshal attrs: %v", err)
	}
	if got := sdkaws.ToString(attrs.Url); got != redact.Placeholder {
		t.Errorf("stored Url = %q; want %q", got, redact.Placeholder)
	}
}

// Category CURATED marks an upload AWS manages; PRIVATE is the customer's own.
func TestScanDeviceFarmUploads_CuratedIsManagedByProvider(t *testing.T) {
	st := newTestStore(t)
	proj := testDFProjectARN("PA")
	curated := testDFUpload("PA", "CUR")
	curated.Category = dftypes.UploadCategoryCurated
	fake := &fakeDeviceFarmUploads{pages: map[string]map[string]*devicefarm.ListUploadsOutput{
		proj: {"": {Uploads: []dftypes.Upload{curated, testDFUpload("PA", "PRIV")}}},
	}}

	if _, _, err := scanDeviceFarmUploads(context.Background(), fake, newTestAccount(testAccountID), testDFRegion, st, testScanID, []string{proj}); err != nil {
		t.Fatalf("scanDeviceFarmUploads: %v", err)
	}
	rows, err := st.ListResources(store.ResourceFilter{Types: []string{TypeDeviceFarmUpload}, IncludeManaged: true, Limit: util.AllResources})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	managed := make(map[string]bool, len(rows))
	for _, r := range rows {
		managed[r.NativeID] = r.ManagedByProvider
	}
	for arn, want := range map[string]bool{testDFUploadARN("PA", "CUR"): true, testDFUploadARN("PA", "PRIV"): false} {
		got, ok := managed[arn]
		if !ok {
			t.Errorf("upload %s not stored", arn)
			continue
		}
		if got != want {
			t.Errorf("upload %s ManagedByProvider = %v; want %v", arn, got, want)
		}
	}
}

// The same upload ARN listed under two projects is stored and counted once.
func TestScanDeviceFarmUploads_DuplicateARNAcrossProjectsStoredOnce(t *testing.T) {
	st := newTestStore(t)
	projA, projB := testDFProjectARN("PA"), testDFProjectARN("PB")
	shared := testDFUpload("PA", "SHARED")
	fake := &fakeDeviceFarmUploads{pages: map[string]map[string]*devicefarm.ListUploadsOutput{
		projA: {"": {Uploads: []dftypes.Upload{shared}}},
		projB: {"": {Uploads: []dftypes.Upload{shared}}},
	}}

	total, _, err := scanDeviceFarmUploads(context.Background(), fake, newTestAccount(testAccountID), testDFRegion, st, testScanID, []string{projA, projB})
	if err != nil {
		t.Fatalf("scanDeviceFarmUploads: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d; want 1", total)
	}
	if n := len(deviceFarmUploadRows(t, st)); n != 1 {
		t.Errorf("stored %d upload rows; want 1", n)
	}
}
