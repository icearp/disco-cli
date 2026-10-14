package aws

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/healthlake"
	healthlaketypes "github.com/aws/aws-sdk-go-v2/service/healthlake/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	hlDenied   = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: healthlake:List"}
	hlBadInput = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	hlGone     = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "not found"}
	hlUpdated  = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
)

const (
	hlPr1ARN = "arn:aws:healthlake:us-east-1:123456789012:dataTransformationProfile/pr1"
	hlPr2ARN = "arn:aws:healthlake:us-east-1:123456789012:dataTransformationProfile/pr2"
	hlPr3ARN = "arn:aws:healthlake:us-east-1:123456789012:dataTransformationProfile/pr3"
	hlDsARN  = "arn:aws:healthlake:us-east-1:123456789012:datastore/fhir/d1"
)

func hlClient(handlers map[string]func(in any) (any, error)) *healthlake.Client {
	return healthlake.NewFromConfig(opStubConfig(handlers, hlDenied))
}

func hlProfile(id string, format healthlaketypes.SourceFormat) healthlaketypes.DataTransformationProfileSummary {
	return healthlaketypes.DataTransformationProfileSummary{
		ProfileId: ptrStr(id), ProfileName: ptrStr("profile-" + id), ProfileDescription: ptrStr("maps " + id),
		SourceFormat: format, TargetFormat: healthlaketypes.TargetFormatFhirR4, Version: sdkaws.Int32(2), LastUpdatedAt: &hlUpdated,
	}
}

// hlProfileHandler serves by the order formats are first asked for, because
// the SDK does not promise an order for SourceFormat.Values(): the first
// format gets two pages (pr1; then pr2 and an element with no ProfileId), the
// second one page (pr3). fail maps "first", "first/p2" (its page two) or
// "second" to the error that call returns. Every call's format is recorded.
func hlProfileHandler(formats *parentCalls, fail map[string]error) func(in any) (any, error) {
	var first healthlaketypes.SourceFormat
	return func(in any) (any, error) {
		req := in.(*healthlake.ListDataTransformationProfilesInput)
		format := req.SourceFormat
		formats.add(string(format))
		if first == "" {
			first = format
		}
		if format != first {
			if err := fail["second"]; err != nil {
				return nil, err
			}
			return &healthlake.ListDataTransformationProfilesOutput{Items: []healthlaketypes.DataTransformationProfileSummary{hlProfile("pr3", format)}}, nil
		}
		if req.NextToken == nil {
			if err := fail["first"]; err != nil {
				return nil, err
			}
			return &healthlake.ListDataTransformationProfilesOutput{Items: []healthlaketypes.DataTransformationProfileSummary{hlProfile("pr1", format)}, NextToken: ptrStr("p2")}, nil
		}
		if err := fail["first/p2"]; err != nil {
			return nil, err
		}
		return &healthlake.ListDataTransformationProfilesOutput{Items: []healthlaketypes.DataTransformationProfileSummary{
			hlProfile("pr2", format), {ProfileName: ptrStr("no-id"), SourceFormat: format},
		}}, nil
	}
}

func scanHLProfiles(st *store.Store, handler func(in any) (any, error)) ([]childParent, int, error) {
	client := hlClient(map[string]func(any) (any, error){"ListDataTransformationProfiles": handler})
	parents, total, _, err := scanHealthLakeProfiles(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	return parents, total, err
}

func TestScanHealthLakeProfiles_StoresEveryPageOfEveryFormat(t *testing.T) {
	st := newTestStore(t)
	var formats parentCalls
	parents, total, err := scanHLProfiles(st, hlProfileHandler(&formats, nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := formats.distinct(); len(formats.ids) != 3 || !slices.Equal(got, []string{"CCDA", "CSV"}) {
		t.Errorf("formats sent per page = %v, want CCDA and CSV over 3 pages", formats.ids)
	}
	rows := rowsByNativeID(t, st, TypeHealthLakeDataTransformationProfile)
	ids := slices.Sorted(maps.Keys(rows))
	if want := []string{hlPr1ARN, hlPr2ARN, hlPr3ARN}; total != 3 || !slices.Equal(ids, want) {
		t.Fatalf("total = %d, rows = %v; want 3, %v", total, ids, want)
	}
	pr3 := rows[hlPr3ARN]
	if sv(pr3.Name) != "profile-pr3" || sv(pr3.Region) != "us-east-1" ||
		!strings.Contains(pr3.AttributesJSON, `"ProfileDescription":"maps pr3"`) {
		t.Errorf("pr3 name/region = %q/%q, attrs = %s", sv(pr3.Name), sv(pr3.Region), pr3.AttributesJSON)
	}
	slices.SortFunc(parents, func(a, b childParent) int { return strings.Compare(a.id, b.id) })
	want := []childParent{{id: "pr1", arn: hlPr1ARN}, {id: "pr2", arn: hlPr2ARN}, {id: "pr3", arn: hlPr3ARN}}
	if !slices.Equal(parents, want) {
		t.Errorf("parents = %v, want %v", parents, want)
	}
}

func TestScanHealthLakeProfiles_Empty(t *testing.T) {
	st := newTestStore(t)
	parents, total, err := scanHLProfiles(st, func(any) (any, error) { return &healthlake.ListDataTransformationProfilesOutput{}, nil })
	if err != nil || total != 0 || len(parents) != 0 || len(rowsByNativeID(t, st, TypeHealthLakeDataTransformationProfile)) != 0 {
		t.Errorf("total = %d, parents = %v, err = %v; want 0 rows, nil", total, parents, err)
	}
}

// A denial warns and ends the phase without error; any other error fails it.
// Either way the profiles listed before the failure are stored and counted.
func TestScanHealthLakeProfiles_Errors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fail      map[string]error
		wantCode  string
		wantTotal int
		wantWarns int
	}{
		{name: "denied first call", fail: map[string]error{"first": hlDenied}, wantTotal: 0, wantWarns: 1},
		{name: "denied on second format", fail: map[string]error{"second": hlDenied}, wantTotal: 2, wantWarns: 1},
		{name: "other on first format page two", fail: map[string]error{"first/p2": hlBadInput}, wantCode: "ValidationException", wantTotal: 1},
		{name: "other on second format", fail: map[string]error{"second": hlBadInput}, wantCode: "ValidationException", wantTotal: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "health-lake:ListDataTransformationProfiles")
			var formats parentCalls
			parents, total, err := scanHLProfiles(st, hlProfileHandler(&formats, tc.fail))
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
				if parents != nil {
					t.Errorf("parents = %v, want nil on a hard error", parents)
				}
			}
			n := len(rowsByNativeID(t, st, TypeHealthLakeDataTransformationProfile))
			if total != tc.wantTotal || n != tc.wantTotal || *warns != tc.wantWarns {
				t.Errorf("total = %d, rows = %d, warnings = %d; want %d, %d, %d", total, n, *warns, tc.wantTotal, tc.wantTotal, tc.wantWarns)
			}
		})
	}
}

// A failed write of the rows listed before a list error reports both errors.
func TestScanHealthLakeProfiles_WriteAndListErrorsBothReported(t *testing.T) {
	// Not newTestStore: its cleanup queries the DB, which a closed store can't serve.
	st, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	var formats parentCalls
	_, _, err = scanHLProfiles(st, hlProfileHandler(&formats, map[string]error{"second": hlBadInput}))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v, want store.ErrStoreWrite", err)
	}
	assertAPICode(t, err, "ValidationException")
}

// hlVersionHandler serves, for pr1, page one (v2) then page two (v1, v0 and
// an element with no Version); any other profile gets one page with its v0.
// A profile in fail returns that error. Every call's profile is recorded.
func hlVersionHandler(calls *parentCalls, fail map[string]error) func(in any) (any, error) {
	version := func(profile string, n int32) healthlaketypes.DataTransformationProfileVersionSummary {
		return healthlaketypes.DataTransformationProfileVersionSummary{
			ProfileId: ptrStr(profile), Version: sdkaws.Int32(n), ProfileName: ptrStr("profile-" + profile),
			SourceFormat: healthlaketypes.SourceFormatCcda, TargetFormat: healthlaketypes.TargetFormatFhirR4,
			ChangeDescription: ptrStr("change " + profile), LastUpdatedAt: &hlUpdated,
		}
	}
	return func(in any) (any, error) {
		req := in.(*healthlake.ListDataTransformationProfileVersionsInput)
		profile := sv(req.ProfileId)
		calls.add(profile)
		if err := fail[profile]; err != nil {
			return nil, err
		}
		if profile != "pr1" {
			return &healthlake.ListDataTransformationProfileVersionsOutput{Items: []healthlaketypes.DataTransformationProfileVersionSummary{version(profile, 0)}}, nil
		}
		if req.NextToken == nil {
			return &healthlake.ListDataTransformationProfileVersionsOutput{Items: []healthlaketypes.DataTransformationProfileVersionSummary{version("pr1", 2)}, NextToken: ptrStr("p2")}, nil
		}
		return &healthlake.ListDataTransformationProfileVersionsOutput{Items: []healthlaketypes.DataTransformationProfileVersionSummary{
			version("pr1", 1), version("pr1", 0), {ProfileId: ptrStr("pr1")},
		}}, nil
	}
}

// scanHLVersions seeds the profile rows and fans out over them.
func scanHLVersions(t *testing.T, st *store.Store, handler func(in any) (any, error), profiles ...string) (int, error) {
	t.Helper()
	parents := make([]childParent, 0, len(profiles))
	arns := make([]string, 0, len(profiles))
	for _, id := range profiles {
		arn := healthLakeProfileARN(testRegion, testAccountID, id)
		parents = append(parents, childParent{id: id, arn: arn})
		arns = append(arns, arn)
	}
	seedParentRows(t, st, TypeHealthLakeDataTransformationProfile, arns...)
	client := hlClient(map[string]func(any) (any, error){"ListDataTransformationProfileVersions": handler})
	total, _, err := scanHealthLakeProfileVersions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
	return total, err
}

func TestScanHealthLakeProfileVersions_StoresEveryPageOfEveryProfile(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	total, err := scanHLVersions(t, st, hlVersionHandler(&calls, nil), "pr1", "pr2")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{"pr1", "pr2"}) {
		t.Errorf("profiles listed = %v, want [pr1 pr2]", got)
	}
	rows := rowsByNativeID(t, st, TypeHealthLakeDataTransformationProfileVersion)
	want := []string{hlPr1ARN + "/version/0", hlPr1ARN + "/version/1", hlPr1ARN + "/version/2", hlPr2ARN + "/version/0"}
	if total != 4 || len(rows) != 4 {
		t.Fatalf("total = %d, rows = %v; want %v", total, rows, want)
	}
	for _, id := range want {
		if _, ok := rows[id]; !ok {
			t.Errorf("missing %s in %v", id, rows)
		}
	}
	v1 := rows[hlPr1ARN+"/version/1"]
	if sv(v1.Name) != "1" || sv(v1.Region) != "us-east-1" || !strings.Contains(v1.AttributesJSON, `"ChangeDescription":"change pr1"`) {
		t.Errorf("v1 name/region = %q/%q, attrs = %s", sv(v1.Name), sv(v1.Region), v1.AttributesJSON)
	}
	assertContainsNative(t, st, hlPr1ARN, hlPr1ARN+"/version/2")
	assertContainsNative(t, st, hlPr1ARN, hlPr1ARN+"/version/0")
	assertContainsNative(t, st, hlPr2ARN, hlPr2ARN+"/version/0")
	assertContainsEdgesDeclared(t, st)
}

func TestScanHealthLakeProfileVersions_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanHLVersions(t, st, func(any) (any, error) { return &healthlake.ListDataTransformationProfileVersionsOutput{}, nil }, "pr1")
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeHealthLakeDataTransformationProfileVersion)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

// A profile deleted after it was listed is skipped silently; a denial warns
// once for the op and keeps the other profiles; any other error fails.
func TestScanHealthLakeProfileVersions_FailedProfile(t *testing.T) {
	t.Run("gone", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "health-lake:ListDataTransformationProfileVersions")
		var calls parentCalls
		total, err := scanHLVersions(t, st, hlVersionHandler(&calls, map[string]error{"pr1": hlGone}), "pr1", "pr2")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if _, ok := rowsByNativeID(t, st, TypeHealthLakeDataTransformationProfileVersion)[hlPr2ARN+"/version/0"]; total != 1 || !ok || *warns != 0 {
			t.Errorf("total = %d, pr2 v0 stored = %v, warnings = %d; want 1, true, 0", total, ok, *warns)
		}
	})
	t.Run("denied", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, "health-lake:ListDataTransformationProfileVersions")
		var calls parentCalls
		total, err := scanHLVersions(t, st, hlVersionHandler(&calls, map[string]error{"pr2": hlDenied, "pr3": hlDenied}), "pr1", "pr2", "pr3")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if *warns != 1 {
			t.Errorf("warnings = %d, want 1", *warns)
		}
		if n := len(rowsByNativeID(t, st, TypeHealthLakeDataTransformationProfileVersion)); total != 3 || n != 3 {
			t.Errorf("total = %d, rows = %d; want pr1's 3 versions", total, n)
		}
	})
	t.Run("other", func(t *testing.T) {
		st := newTestStore(t)
		var calls parentCalls
		_, err := scanHLVersions(t, st, hlVersionHandler(&calls, map[string]error{"pr2": hlBadInput}), "pr1", "pr2")
		assertAPICode(t, err, "ValidationException")
	})
}

// hlTopLevelHandlers answers every op: one datastore (the pre-existing
// phase), the profiles of hlProfileHandler and their versions.
func hlTopLevelHandlers(versionCalls *parentCalls) map[string]func(in any) (any, error) {
	var formats parentCalls
	return map[string]func(in any) (any, error){
		"ListFHIRDatastores": func(any) (any, error) {
			return &healthlake.ListFHIRDatastoresOutput{DatastorePropertiesList: []healthlaketypes.DatastoreProperties{{
				DatastoreArn: ptrStr(hlDsARN), DatastoreId: ptrStr("d1"), DatastoreName: ptrStr("ds"), DatastoreStatus: healthlaketypes.DatastoreStatusActive,
			}}}, nil
		},
		"ListDataTransformationProfiles":        hlProfileHandler(&formats, nil),
		"ListDataTransformationProfileVersions": hlVersionHandler(versionCalls, nil),
	}
}

// Versions are listed for every profile the profile phase stored, and land
// under it.
func TestScanHealthLake_StoresVersionsOfListedProfiles(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var versionCalls parentCalls
	acct.cfg = opStubConfig(hlTopLevelHandlers(&versionCalls), hlDenied)

	total, _, err := scanHealthLake(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanHealthLake: %v", err)
	}
	if got := versionCalls.distinct(); !slices.Equal(got, []string{"pr1", "pr2", "pr3"}) {
		t.Errorf("versions listed for %v, want [pr1 pr2 pr3]", got)
	}
	// 1 datastore, 3 profiles, 5 versions (pr1: 0 1 2, pr2: 0, pr3: 0).
	if total != 9 {
		t.Errorf("total = %d, want 9", total)
	}
	assertContainsNative(t, st, hlPr3ARN, hlPr3ARN+"/version/0")
	assertContainsEdgesDeclared(t, st)
}

func TestScanHealthLake_CallsGoToScannedRegion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var versionCalls, regions parentCalls
	cfg := opStubConfig(hlTopLevelHandlers(&versionCalls), hlDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	if _, _, err := scanHealthLake(context.Background(), acct, "eu-west-1", st, testScanID); err != nil {
		t.Fatalf("scanHealthLake: %v", err)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"eu-west-1"}) {
		t.Errorf("calls went to regions %v, want [eu-west-1]", got)
	}
	if n := len(versionCalls.distinct()); n != 3 {
		t.Errorf("version list calls = %d profiles, want 3", n)
	}
}

// Each new phase runs after the datastore phase, versions after profiles, and
// a phase's hard error reaches the caller with the counts stored before it.
func TestScanHealthLake_NewPhaseErrorAfterExistingPhases(t *testing.T) {
	for _, tc := range []struct {
		op           string
		wantTotal    int
		wantProfiles int
	}{
		// 1 datastore; the profile phase stores nothing.
		{op: "ListDataTransformationProfiles", wantTotal: 1},
		// + 3 profiles; every profile's versions fail.
		{op: "ListDataTransformationProfileVersions", wantTotal: 4, wantProfiles: 3},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			var versionCalls parentCalls
			handlers := hlTopLevelHandlers(&versionCalls)
			var failed atomic.Bool
			handlers[tc.op] = func(any) (any, error) {
				failed.Store(true)
				return nil, hlBadInput
			}
			acct.cfg = opStubConfig(handlers, hlDenied)

			total, _, err := scanHealthLake(context.Background(), acct, testRegion, st, testScanID)
			if !failed.Load() {
				t.Fatalf("%s never called", tc.op)
			}
			assertAPICode(t, err, "ValidationException")
			if n := len(rowsByNativeID(t, st, TypeHealthLakeFHIRDatastore)); n != 1 {
				t.Errorf("datastore rows = %d, want 1 before the %s phase", n, tc.op)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
			if n := len(rowsByNativeID(t, st, TypeHealthLakeDataTransformationProfile)); n != tc.wantProfiles {
				t.Errorf("profile rows = %d, want %d", n, tc.wantProfiles)
			}
			if n := len(rowsByNativeID(t, st, TypeHealthLakeDataTransformationProfileVersion)); n != 0 {
				t.Errorf("version rows = %d, want 0", n)
			}
		})
	}
}

// A denied datastore list warns and the profile phases still run.
func TestScanHealthLake_DeniedDatastoresKeepProfiles(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	warns := countOpWarnings(st, "health-lake:ListFHIRDatastores")
	var versionCalls parentCalls
	handlers := hlTopLevelHandlers(&versionCalls)
	delete(handlers, "ListFHIRDatastores")
	acct.cfg = opStubConfig(handlers, hlDenied)

	total, _, err := scanHealthLake(context.Background(), acct, testRegion, st, testScanID)
	if err != nil || total != 8 || *warns != 1 {
		t.Errorf("total = %d, err = %v, warnings = %d; want 8 (3 profiles + 5 versions), nil, 1", total, err, *warns)
	}
}
