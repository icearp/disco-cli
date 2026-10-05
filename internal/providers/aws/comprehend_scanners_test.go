package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/comprehend"
	comprehendtypes "github.com/aws/aws-sdk-go-v2/service/comprehend/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// stubComprehend returns a canned error from every List op. Each scanner phase
// calls exactly one of them, so a single err field covers all five.
type stubComprehend struct{ err error }

func (s stubComprehend) ListDocumentClassifiers(context.Context, *comprehend.ListDocumentClassifiersInput, ...func(*comprehend.Options)) (*comprehend.ListDocumentClassifiersOutput, error) {
	return nil, s.err
}

func (s stubComprehend) ListEntityRecognizers(context.Context, *comprehend.ListEntityRecognizersInput, ...func(*comprehend.Options)) (*comprehend.ListEntityRecognizersOutput, error) {
	return nil, s.err
}

func (s stubComprehend) ListEndpoints(context.Context, *comprehend.ListEndpointsInput, ...func(*comprehend.Options)) (*comprehend.ListEndpointsOutput, error) {
	return nil, s.err
}

func (s stubComprehend) ListFlywheels(context.Context, *comprehend.ListFlywheelsInput, ...func(*comprehend.Options)) (*comprehend.ListFlywheelsOutput, error) {
	return nil, s.err
}

func (s stubComprehend) ListDatasets(context.Context, *comprehend.ListDatasetsInput, ...func(*comprehend.Options)) (*comprehend.ListDatasetsOutput, error) {
	return nil, s.err
}

func comprehendPhases() map[string]func(context.Context, comprehendAPI, *account, string, *store.Store, string) (int, int, error) {
	return map[string]func(context.Context, comprehendAPI, *account, string, *store.Store, string) (int, int, error){
		"document-classifiers": scanComprehendDocumentClassifiers,
		"entity-recognizers":   scanComprehendEntityRecognizers,
		"endpoints":            scanComprehendEndpoints,
		"flywheels": func(ctx context.Context, c comprehendAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
			t, i, _, err := scanComprehendFlywheels(ctx, c, acct, region, st, scanID)
			return t, i, err
		},
		"datasets": func(ctx context.Context, c comprehendAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
			return scanComprehendDatasets(ctx, c, acct, region, st, scanID, []string{testFlywheelARN("fw")})
		},
	}
}

// An account not subscribed to Comprehend's custom-model surface gets
// NotAuthorizedException "Your account is not authorized to make this call."
// That code is in accessDeniedCodes, so without the guard every phase recorded
// an IAM-style warning on every scan of every such region.
func TestScanComprehendPhases_SilentSkipWhenNotEnabled(t *testing.T) {
	notEnabled := apiErr("NotAuthorizedException", "Your account is not authorized to make this call.")

	for name, phase := range comprehendPhases() {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			warned := false
			st.OnWarn = func(store.ScanWarning) { warned = true }

			total, inserted, err := phase(
				context.Background(), stubComprehend{err: notEnabled}, newTestAccount(testAccountID), "eu-west-3", st, testScanID)
			if err != nil {
				t.Fatalf("not-enabled state must not surface an error, got %v", err)
			}
			if total != 0 || inserted != 0 {
				t.Errorf("want (0,0), got (%d,%d)", total, inserted)
			}
			if warned {
				t.Error("account-not-subscribed must not record a scan warning")
			}
		})
	}
}

// The guard keys on the message, so a genuine per-action IAM denial sharing the
// NotAuthorizedException/AccessDenied code family still warns rather than being
// silently swallowed.
func TestScanComprehendPhases_RealDenialStillWarns(t *testing.T) {
	realDeny := apiErr("AccessDeniedException",
		"User: arn:aws:sts::123456789012:assumed-role/DiscoScanner/x is not authorized to perform: comprehend:ListEndpoints")

	for name, phase := range comprehendPhases() {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			warned := false
			st.OnWarn = func(store.ScanWarning) { warned = true }

			if _, _, err := phase(
				context.Background(), stubComprehend{err: realDeny}, newTestAccount(testAccountID), "us-east-1", st, testScanID); err != nil {
				t.Fatalf("access-denied path returns nil, got %v", err)
			}
			if !warned {
				t.Error("a real IAM denial must still record a scan warning")
			}
		})
	}
}

// Comprehend's per-region feature gap must be a silent skip in every phase,
// not a scan error or a warning.
func TestScanComprehendPhases_SilentSkipOnUnsupportedOperation(t *testing.T) {
	gap := apiErr("InvalidRequestException", "UNSUPPORTED_OPERATION: this operation is not supported in this region")

	for name, phase := range comprehendPhases() {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			warned := false
			st.OnWarn = func(store.ScanWarning) { warned = true }

			total, inserted, err := phase(
				context.Background(), stubComprehend{err: gap}, newTestAccount(testAccountID), testRegion, st, testScanID)
			if err != nil {
				t.Fatalf("feature gap must not surface an error, got %v", err)
			}
			if total != 0 || inserted != 0 {
				t.Errorf("want (0,0), got (%d,%d)", total, inserted)
			}
			if warned {
				t.Error("feature gap must not record a scan warning")
			}
		})
	}
}

func TestIsComprehendNotEnabled(t *testing.T) {
	if !isComprehendNotEnabled(apiErr("NotAuthorizedException", "Your account is not authorized to make this call.")) {
		t.Error("not-subscribed message should match")
	}
	// Same code family, real per-action denial — must not match.
	if isComprehendNotEnabled(apiErr("NotAuthorizedException", "User: arn:... is not authorized to perform: comprehend:ListEndpoints")) {
		t.Error("real IAM denial must not match")
	}
	// Right message, wrong code.
	if isComprehendNotEnabled(apiErr("AccessDeniedException", "account is not authorized to make this call")) {
		t.Error("wrong code must not match")
	}
	if isComprehendNotEnabled(nil) {
		t.Error("nil must not match")
	}
}

func testFlywheelARN(name string) string {
	return "arn:aws:comprehend:" + testRegion + ":" + testAccountID + ":flywheel/" + name
}

// seedTestFlywheels stores the parent rows the flywheels phase would have
// written; the closure writer gates out a contains pair whose parent is absent.
func seedTestFlywheels(t *testing.T, st *store.Store, arns ...string) []string {
	t.Helper()
	for _, arn := range arns {
		upsertTestResource(t, st, "aws", testAccountID, TypeComprehendFlywheel, arn, testRegion, "{}")
	}
	return arns
}

func testDataset(flywheel, name string, status comprehendtypes.DatasetStatus) comprehendtypes.DatasetProperties {
	return comprehendtypes.DatasetProperties{
		DatasetArn:   sdkaws.String(testFlywheelARN(flywheel) + "/dataset/" + name),
		DatasetName:  sdkaws.String(name),
		DatasetType:  comprehendtypes.DatasetTypeTrain,
		Status:       status,
		CreationTime: &testDatasetCreated,
	}
}

var testDatasetCreated = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// fakeComprehendDatasets serves ListDatasets per FlywheelArn, one page per
// NextToken value ("" for the first page). A flywheel with an entry in errs
// fails every call.
type fakeComprehendDatasets struct {
	comprehendAPI
	pages map[string]map[string]*comprehend.ListDatasetsOutput
	errs  map[string]error
}

func (f fakeComprehendDatasets) ListDatasets(_ context.Context, in *comprehend.ListDatasetsInput, _ ...func(*comprehend.Options)) (*comprehend.ListDatasetsOutput, error) {
	arn := sv(in.FlywheelArn)
	if err := f.errs[arn]; err != nil {
		return nil, err
	}
	page, ok := f.pages[arn][sv(in.NextToken)]
	if !ok {
		return nil, fmt.Errorf("fake: no page for flywheel %q token %q", arn, sv(in.NextToken))
	}
	return page, nil
}

func comprehendDatasetRows(t *testing.T, st *store.Store) map[string]store.Resource {
	t.Helper()
	rows, err := st.ListResources(store.ResourceFilter{Types: []string{TypeComprehendDataset}, Limit: util.AllResources})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	byNativeID := make(map[string]store.Resource, len(rows))
	for _, r := range rows {
		byNativeID[r.NativeID] = r
	}
	return byNativeID
}

func TestScanComprehendDatasets_PaginatesEveryFlywheelAndWiresHierarchy(t *testing.T) {
	st := newTestStore(t)
	fwA, fwB := testFlywheelARN("a"), testFlywheelARN("b")
	fake := fakeComprehendDatasets{pages: map[string]map[string]*comprehend.ListDatasetsOutput{
		fwA: {
			"":   {DatasetPropertiesList: []comprehendtypes.DatasetProperties{testDataset("a", "train1", comprehendtypes.DatasetStatusCompleted)}, NextToken: sdkaws.String("p2")},
			"p2": {DatasetPropertiesList: []comprehendtypes.DatasetProperties{testDataset("a", "test1", "")}},
		},
		fwB: {
			"": {DatasetPropertiesList: []comprehendtypes.DatasetProperties{
				testDataset("b", "train2", comprehendtypes.DatasetStatusCreating),
				{DatasetName: sdkaws.String("no-arn")},
			}},
		},
	}}

	total, _, err := scanComprehendDatasets(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, seedTestFlywheels(t, st, fwA, fwB))
	if err != nil {
		t.Fatalf("scanComprehendDatasets: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3 (the ARN-less dataset is skipped)", total)
	}
	rows := comprehendDatasetRows(t, st)
	want := map[string]struct{ flywheel, name, status string }{
		fwA + "/dataset/train1": {fwA, "train1", "COMPLETED"},
		fwA + "/dataset/test1":  {fwA, "test1", ""},
		fwB + "/dataset/train2": {fwB, "train2", "CREATING"},
	}
	if len(rows) != len(want) {
		t.Fatalf("stored %d dataset rows; want %d: %v", len(rows), len(want), rows)
	}
	for nativeID, w := range want {
		r, ok := rows[nativeID]
		if !ok {
			t.Errorf("missing dataset row %q", nativeID)
			continue
		}
		if sv(r.Name) != w.name || sv(r.Region) != testRegion || r.Type != TypeComprehendDataset {
			t.Errorf("row %q: name=%q region=%q type=%q; want %q %q %q", nativeID, sv(r.Name), sv(r.Region), r.Type, w.name, testRegion, TypeComprehendDataset)
		}
		if w.status == "" && r.Status != nil {
			t.Errorf("row %q: status = %q; want nil for an empty status", nativeID, *r.Status)
		}
		if w.status != "" && sv(r.Status) != w.status {
			t.Errorf("row %q: status = %q; want %q", nativeID, sv(r.Status), w.status)
		}
		if sv(r.CreatedAt) != "2026-09-01T12:00:00Z" {
			t.Errorf("row %q: createdAt = %q; want 2026-09-01T12:00:00Z", nativeID, sv(r.CreatedAt))
		}
		var attrs struct{ DatasetType string }
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.DatasetType != "TRAIN" {
			t.Errorf("row %q: attributes DatasetType = %q (err %v); want TRAIN", nativeID, attrs.DatasetType, err)
		}
		parentID := store.ResourceID("aws", testAccountID, w.flywheel)
		rels, err := st.RelationshipsFrom(parentID, store.RelContains)
		if err != nil {
			t.Fatalf("RelationshipsFrom: %v", err)
		}
		assertRelationship(t, rels, parentID, r.ID, store.RelContains)
	}
	relsB, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, fwB), store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(relsB) != 1 {
		t.Errorf("flywheel b contains %d datasets; want 1 (only its own)", len(relsB))
	}
}

func TestScanComprehendDatasets_Empty(t *testing.T) {
	fw := testFlywheelARN("a")
	cases := map[string]struct {
		flywheels []string
		fake      fakeComprehendDatasets
	}{
		"no flywheels": {},
		"flywheel without datasets": {
			flywheels: []string{fw},
			fake:      fakeComprehendDatasets{pages: map[string]map[string]*comprehend.ListDatasetsOutput{fw: {"": {}}}},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			total, inserted, err := scanComprehendDatasets(context.Background(), tc.fake, newTestAccount(testAccountID), testRegion, st, testScanID, tc.flywheels)
			if err != nil || total != 0 || inserted != 0 {
				t.Errorf("got (%d,%d,%v); want (0,0,nil)", total, inserted, err)
			}
		})
	}
}

// A region-wide feature gap hit on a later flywheel stops the listing but keeps
// the datasets earlier flywheels already returned.
func TestScanComprehendDatasets_FeatureGapKeepsEarlierFlywheels(t *testing.T) {
	st := newTestStore(t)
	warned := false
	st.OnWarn = func(store.ScanWarning) { warned = true }
	first, second := testFlywheelARN("first"), testFlywheelARN("second")
	fake := fakeComprehendDatasets{
		errs: map[string]error{second: apiErr("InvalidRequestException", "UNSUPPORTED_OPERATION")},
		pages: map[string]map[string]*comprehend.ListDatasetsOutput{
			first: {"": {DatasetPropertiesList: []comprehendtypes.DatasetProperties{testDataset("first", "d", comprehendtypes.DatasetStatusCompleted)}}},
		},
	}

	total, _, err := scanComprehendDatasets(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, seedTestFlywheels(t, st, first, second))
	if err != nil {
		t.Fatalf("a feature gap must not fail the phase, got %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d; want 1 from the first flywheel", total)
	}
	r, ok := comprehendDatasetRows(t, st)[first+"/dataset/d"]
	if !ok {
		t.Fatal("first flywheel's dataset dropped after the second hit the feature gap")
	}
	firstID := store.ResourceID("aws", testAccountID, first)
	rels, err := st.RelationshipsFrom(firstID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, firstID, r.ID, store.RelContains)
	if warned {
		t.Error("a feature gap must not record a scan warning")
	}
}

// A flywheel deleted between ListFlywheels and ListDatasets is skipped without
// a warning; its siblings are still listed.
func TestScanComprehendDatasets_VanishedFlywheelSkipped(t *testing.T) {
	st := newTestStore(t)
	warned := false
	st.OnWarn = func(store.ScanWarning) { warned = true }
	gone, live := testFlywheelARN("gone"), testFlywheelARN("live")
	fake := fakeComprehendDatasets{
		errs: map[string]error{gone: apiErr("ResourceNotFoundException", "flywheel not found")},
		pages: map[string]map[string]*comprehend.ListDatasetsOutput{
			live: {"": {DatasetPropertiesList: []comprehendtypes.DatasetProperties{testDataset("live", "d", comprehendtypes.DatasetStatusCompleted)}}},
		},
	}

	total, _, err := scanComprehendDatasets(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, seedTestFlywheels(t, st, gone, live))
	if err != nil {
		t.Fatalf("a vanished flywheel must not fail the phase, got %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d; want 1 from the live flywheel", total)
	}
	if _, ok := comprehendDatasetRows(t, st)[live+"/dataset/d"]; !ok {
		t.Error("live flywheel's dataset not stored")
	}
	if warned {
		t.Error("a vanished flywheel must not record a scan warning")
	}
}

// ListDatasets is authorized per flywheel, so a denial on some flywheels warns
// once and the remaining flywheels are still listed.
func TestScanComprehendDatasets_AccessDeniedWarnsOnceAndContinues(t *testing.T) {
	st := newTestStore(t)
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	deny := apiErr("AccessDeniedException", "User: arn:aws:sts::123456789012:assumed-role/x is not authorized to perform: comprehend:ListDatasets")
	d1, d2, permitted := testFlywheelARN("d1"), testFlywheelARN("d2"), testFlywheelARN("ok")
	fake := fakeComprehendDatasets{
		errs: map[string]error{d1: deny, d2: deny},
		pages: map[string]map[string]*comprehend.ListDatasetsOutput{
			permitted: {"": {DatasetPropertiesList: []comprehendtypes.DatasetProperties{testDataset("ok", "d", comprehendtypes.DatasetStatusCompleted)}}},
		},
	}

	total, _, err := scanComprehendDatasets(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, seedTestFlywheels(t, st, d1, d2, permitted))
	if err != nil {
		t.Fatalf("access denied must not fail the phase, got %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d; want 1 from the permitted flywheel", total)
	}
	if len(warnings) != 1 {
		t.Errorf("recorded %d warnings; want exactly 1", len(warnings))
	}
}

func TestScanComprehendDatasets_UnexpectedErrorPropagates(t *testing.T) {
	st := newTestStore(t)
	fw := testFlywheelARN("a")
	boom := apiErr("InternalServerException", "boom")
	fake := fakeComprehendDatasets{errs: map[string]error{fw: boom}}

	_, _, err := scanComprehendDatasets(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, []string{fw})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v; want it to wrap %v", err, boom)
	}
}

// scanComprehend hands the flywheels it listed to the dataset phase, so a
// dataset row and its flywheel->dataset contains edge come out of one scan.
func TestScanComprehend_ListsDatasetsOfListedFlywheels(t *testing.T) {
	st := newTestStore(t)
	fw := testFlywheelARN("a")
	stub := stubResponses(t, map[string][]stubCall{
		"ListDocumentClassifiers": {{Output: &comprehend.ListDocumentClassifiersOutput{}}},
		"ListEntityRecognizers":   {{Output: &comprehend.ListEntityRecognizersOutput{}}},
		"ListEndpoints":           {{Output: &comprehend.ListEndpointsOutput{}}},
		"ListFlywheels": {{Output: &comprehend.ListFlywheelsOutput{FlywheelSummaryList: []comprehendtypes.FlywheelSummary{
			{FlywheelArn: sdkaws.String(fw), Status: comprehendtypes.FlywheelStatusActive},
		}}}},
		"ListDatasets": {{Output: &comprehend.ListDatasetsOutput{DatasetPropertiesList: []comprehendtypes.DatasetProperties{
			testDataset("a", "train", comprehendtypes.DatasetStatusCompleted),
		}}}},
	})
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(stub, testRegion)}

	total, _, err := scanComprehend(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanComprehend: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2 (flywheel + dataset)", total)
	}
	r, ok := comprehendDatasetRows(t, st)[fw+"/dataset/train"]
	if !ok {
		t.Fatal("dataset row not stored by scanComprehend")
	}
	fwID := store.ResourceID("aws", testAccountID, fw)
	rels, err := st.RelationshipsFrom(fwID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, fwID, r.ID, store.RelContains)
}
