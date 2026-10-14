package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/frauddetector"
	frauddetectortypes "github.com/aws/aws-sdk-go-v2/service/frauddetector/types"
	"github.com/icearp/disco-cli/store"
)

const fdModelType = frauddetectortypes.ModelTypeEnumOnlineFraudInsights

func fdTestARN(kind, path string) string {
	return fmt.Sprintf("arn:aws:frauddetector:%s:%s:%s/%s", testRegion, testAccountID, kind, path)
}

func fdDetectorARN(id string) string { return fdTestARN("detector", id) }

func fdModelARN(id string) string { return fdTestARN("model", string(fdModelType)+"/"+id) }

func fdDetectorVersionARN(det, ver string) string {
	return fdTestARN("detector-version", det+"/"+ver)
}

func fdModelVersionARN(model, ver string) string {
	return fdTestARN("model-version", string(fdModelType)+"/"+model+"/"+ver)
}

// fakeFD serves the per-parent ops from pages keyed by parent id and NextToken
// ("" is the first page). errs fails a call by the id it is made with: the
// detector or model id, or "<detector>/<version>" for GetDetectorVersion.
// childFanOut calls it from concurrent goroutines, so the call log is locked.
// A call repeated more than fdRunawayCalls times fails, so a pagination loop
// that never ends shows up as an error instead of a hung test.
type fakeFD struct {
	fraudDetectorAPI
	detectorPages map[string]map[string]*frauddetector.DescribeDetectorOutput
	modelPages    map[string]map[string]*frauddetector.DescribeModelVersionsOutput
	errs          map[string]error

	mu      sync.Mutex
	queried []string
}

const fdRunawayCalls = 10

func (f *fakeFD) record(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queried = append(f.queried, id)
	if f.calls(id) > fdRunawayCalls {
		return fmt.Errorf("%s called more than %d times; pagination never ended", id, fdRunawayCalls)
	}
	return f.errs[id]
}

func (f *fakeFD) calls(id string) int {
	n := 0
	for _, q := range f.queried {
		if q == id {
			n++
		}
	}
	return n
}

func (f *fakeFD) DescribeDetector(_ context.Context, in *frauddetector.DescribeDetectorInput, _ ...func(*frauddetector.Options)) (*frauddetector.DescribeDetectorOutput, error) {
	id := *in.DetectorId
	if err := f.record(id); err != nil {
		return nil, err
	}
	if out := f.detectorPages[id][sv(in.NextToken)]; out != nil {
		return out, nil
	}
	return &frauddetector.DescribeDetectorOutput{}, nil
}

func (f *fakeFD) GetDetectorVersion(_ context.Context, in *frauddetector.GetDetectorVersionInput, _ ...func(*frauddetector.Options)) (*frauddetector.GetDetectorVersionOutput, error) {
	det, ver := *in.DetectorId, *in.DetectorVersionId
	if err := f.record(det + "/" + ver); err != nil {
		return nil, err
	}
	return fdDetectorVersion(det, ver), nil
}

func (f *fakeFD) DescribeModelVersions(_ context.Context, in *frauddetector.DescribeModelVersionsInput, _ ...func(*frauddetector.Options)) (*frauddetector.DescribeModelVersionsOutput, error) {
	id := *in.ModelId
	if err := f.record(id); err != nil {
		return nil, err
	}
	if in.ModelType != fdModelType {
		return nil, fmt.Errorf("model %s asked with type %q; want %q", id, in.ModelType, fdModelType)
	}
	if in.MaxResults == nil || *in.MaxResults != 10 {
		return nil, fmt.Errorf("model %s asked with MaxResults %v; want 10", id, in.MaxResults)
	}
	if out := f.modelPages[id][sv(in.NextToken)]; out != nil {
		return out, nil
	}
	return &frauddetector.DescribeModelVersionsOutput{}, nil
}

func fdDetectorVersion(det, ver string) *frauddetector.GetDetectorVersionOutput {
	return &frauddetector.GetDetectorVersionOutput{
		Arn: sdkaws.String(fdDetectorVersionARN(det, ver)), DetectorId: sdkaws.String(det), DetectorVersionId: sdkaws.String(ver),
		Status: frauddetectortypes.DetectorVersionStatusActive, RuleExecutionMode: frauddetectortypes.RuleExecutionModeFirstMatched,
		Rules: []frauddetectortypes.Rule{{DetectorId: sdkaws.String(det), RuleId: sdkaws.String("r1"), RuleVersion: sdkaws.String("1")}},
	}
}

func fdModelVersion(model, ver string) frauddetectortypes.ModelVersionDetail {
	return frauddetectortypes.ModelVersionDetail{
		Arn: sdkaws.String(fdModelVersionARN(model, ver)), ModelId: sdkaws.String(model), ModelType: fdModelType,
		ModelVersionNumber: sdkaws.String(ver), Status: sdkaws.String("ACTIVE"), TrainingDataSource: frauddetectortypes.TrainingDataSourceEnumExternalEvents,
	}
}

func fdVersionSummary(ver string) frauddetectortypes.DetectorVersionSummary {
	return frauddetectortypes.DetectorVersionSummary{DetectorVersionId: sdkaws.String(ver), Status: frauddetectortypes.DetectorVersionStatusActive}
}

// fdChildOps drives each version fan-out through one shape: every parent gets
// two pages (version "1" plus an id-less element, then version "2").
var fdChildOps = []struct {
	name, childType, parentType, op, attrKey string
	parentARN                                func(string) string
	childARN                                 func(parent, ver string) string
	run                                      func(st *store.Store, fake *fakeFD, parents []string) error
}{
	{
		name: "detector-versions", childType: TypeFraudDetectorDetectorVersion, parentType: TypeFraudDetectorDetector,
		op: "frauddetector:DescribeDetector", attrKey: "Rules", parentARN: fdDetectorARN, childARN: fdDetectorVersionARN,
		run: func(st *store.Store, fake *fakeFD, parents []string) error {
			fake.detectorPages = map[string]map[string]*frauddetector.DescribeDetectorOutput{}
			refs := make([]childParent, len(parents))
			for i, p := range parents {
				refs[i] = childParent{id: p, arn: fdDetectorARN(p)}
				fake.detectorPages[p] = map[string]*frauddetector.DescribeDetectorOutput{
					"":   {DetectorVersionSummaries: []frauddetectortypes.DetectorVersionSummary{fdVersionSummary("1"), {Description: sdkaws.String("no-id")}}, NextToken: sdkaws.String("p2")},
					"p2": {DetectorVersionSummaries: []frauddetectortypes.DetectorVersionSummary{fdVersionSummary("2")}},
				}
			}
			_, _, err := scanFDDetectorVersions(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, refs)
			return err
		},
	},
	{
		name: "model-versions", childType: TypeFraudDetectorModelVersion, parentType: TypeFraudDetectorModel,
		op: "frauddetector:DescribeModelVersions", attrKey: "TrainingDataSource", parentARN: fdModelARN, childARN: fdModelVersionARN,
		run: func(st *store.Store, fake *fakeFD, parents []string) error {
			fake.modelPages = map[string]map[string]*frauddetector.DescribeModelVersionsOutput{}
			models := make([]fdModel, len(parents))
			for i, p := range parents {
				models[i] = fdModel{ref: childParent{id: p, arn: fdModelARN(p)}, modelType: fdModelType}
				fake.modelPages[p] = map[string]*frauddetector.DescribeModelVersionsOutput{
					"":   {ModelVersionDetails: []frauddetectortypes.ModelVersionDetail{fdModelVersion(p, "1"), {ModelVersionNumber: sdkaws.String("no-arn")}}, NextToken: sdkaws.String("p2")},
					"p2": {ModelVersionDetails: []frauddetectortypes.ModelVersionDetail{fdModelVersion(p, "2")}},
				}
			}
			_, _, err := scanFDModelVersions(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, models)
			return err
		},
	},
}

func seedFDParents(t *testing.T, st *store.Store, typ string, arn func(string) string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		upsertTestResource(t, st, "aws", testAccountID, typ, arn(id), testRegion, "{}")
	}
}

func TestScanFDVersions_PaginatesEveryParentAndWiresHierarchy(t *testing.T) {
	for _, op := range fdChildOps {
		t.Run(op.name, func(t *testing.T) {
			st := newTestStore(t)
			seedFDParents(t, st, op.parentType, op.parentARN, "p1", "p2")
			fake := &fakeFD{}
			if err := op.run(st, fake, []string{"p1", "p2"}); err != nil {
				t.Fatalf("run: %v", err)
			}
			rows := qsRows(t, st, op.childType)
			if len(rows) != 4 {
				t.Fatalf("stored %d rows; want 4 (2 pages x 2 parents, id-less skipped)", len(rows))
			}
			for _, p := range []string{"p1", "p2"} {
				v1 := op.childARN(p, "1")
				r, ok := rows[v1]
				if !ok {
					t.Fatalf("no row %s; got %v", v1, rows)
				}
				if r.Type != op.childType || sv(r.Region) != testRegion || sv(r.Name) != "1" || sv(r.Status) != "ACTIVE" {
					t.Errorf("row %s: type=%q region=%q name=%q status=%q", v1, r.Type, sv(r.Region), sv(r.Name), sv(r.Status))
				}
				var attrs map[string]any
				if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs[op.attrKey] == nil {
					t.Errorf("row %s: attributes %s lack %q (err %v)", v1, r.AttributesJSON, op.attrKey, err)
				}
				assertQSContains(t, st, op.parentARN(p), v1, op.childARN(p, "2"))
			}
		})
	}
}

func TestScanFDVersions_ParentErrorHandling(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantErr   bool
		wantRows  int
		wantWarns int
	}{
		{"parent deleted since listing is skipped", apiErr("ResourceNotFoundException", "gone"), false, 4, 0},
		{"empty-message access denied warns: the gate already handled closed accounts", apiErr("AccessDeniedException", ""), false, 4, 1},
		{"access denied warns once and keeps siblings", apiErr("AccessDeniedException", "User: x is not authorized to perform: frauddetector:Describe"), false, 4, 1},
		{"other error propagates", apiErr("InternalServerException", "boom"), true, 0, 0},
	}
	for _, op := range fdChildOps {
		for _, tc := range cases {
			t.Run(op.name+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				var warned []string
				st.OnWarn = func(w store.ScanWarning) { warned = append(warned, w.Service) }
				seedFDParents(t, st, op.parentType, op.parentARN, "bad1", "bad2", "ok1", "ok2")
				fake := &fakeFD{errs: map[string]error{"bad1": tc.err, "bad2": tc.err}}
				err := op.run(st, fake, []string{"bad1", "bad2", "ok1", "ok2"})
				if tc.wantErr {
					if !isAPIErrorCode(err, "InternalServerException") {
						t.Fatalf("err = %v; want the InternalServerException propagated", err)
					}
				} else if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if got := len(qsRows(t, st, op.childType)); got != tc.wantRows {
					t.Errorf("stored %d rows; want %d", got, tc.wantRows)
				}
				if len(warned) != tc.wantWarns {
					t.Errorf("recorded %d warnings; want %d", len(warned), tc.wantWarns)
				}
				for _, svc := range warned {
					if svc != op.op {
						t.Errorf("warning names %q; want %q", svc, op.op)
					}
				}
			})
		}
	}
}

// TestScanFDDetectorVersions_VersionErrorHandling covers a GetDetectorVersion
// failure for one listed version: it never drops the detector's other versions
// unless it is a hard error.
func TestScanFDDetectorVersions_VersionErrorHandling(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantErr   bool
		wantRows  int
		wantWarns int
	}{
		{"version deleted since listing is skipped", apiErr("ResourceNotFoundException", "gone"), false, 2, 0},
		{"access denied warns once and keeps siblings", apiErr("AccessDeniedException", "User: x is not authorized to perform: frauddetector:GetDetectorVersion"), false, 2, 1},
		{"other error propagates", apiErr("InternalServerException", "boom"), true, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var warned []string
			st.OnWarn = func(w store.ScanWarning) { warned = append(warned, w.Service) }
			seedFDParents(t, st, TypeFraudDetectorDetector, fdDetectorARN, "d1", "d2")
			fake := &fakeFD{errs: map[string]error{"d1/1": tc.err, "d2/1": tc.err}}
			err := fdChildOps[0].run(st, fake, []string{"d1", "d2"})
			if tc.wantErr {
				if !isAPIErrorCode(err, "InternalServerException") {
					t.Fatalf("err = %v; want the InternalServerException propagated", err)
				}
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			rows := qsRows(t, st, TypeFraudDetectorDetectorVersion)
			if len(rows) != tc.wantRows {
				t.Errorf("stored %d rows; want %d", len(rows), tc.wantRows)
			}
			if !tc.wantErr {
				for _, d := range []string{"d1", "d2"} {
					if _, ok := rows[fdDetectorVersionARN(d, "2")]; !ok {
						t.Errorf("version 2 of %s dropped with its failed sibling", d)
					}
				}
			}
			if len(warned) != tc.wantWarns {
				t.Errorf("recorded %d warnings; want %d", len(warned), tc.wantWarns)
			}
			for _, svc := range warned {
				if svc != "frauddetector:GetDetectorVersion" {
					t.Errorf("warning names %q; want frauddetector:GetDetectorVersion", svc)
				}
			}
		})
	}
}

func TestScanFDVersions_NoParentsMakesNoCalls(t *testing.T) {
	for _, op := range fdChildOps {
		t.Run(op.name, func(t *testing.T) {
			st := newTestStore(t)
			fake := &fakeFD{}
			if err := op.run(st, fake, nil); err != nil {
				t.Fatalf("run: %v", err)
			}
			if len(fake.queried) != 0 || len(qsRows(t, st, op.childType)) != 0 {
				t.Errorf("queried %v and stored rows with no parents; want neither", fake.queried)
			}
		})
	}
}

// TestScanFDDetectorVersions_RepeatedTokenEndsPagination pins the
// StopOnDuplicateToken guard on the manual DescribeDetector loop: a page that
// hands back the token it was asked with is the last page.
func TestScanFDDetectorVersions_RepeatedTokenEndsPagination(t *testing.T) {
	st := newTestStore(t)
	seedFDParents(t, st, TypeFraudDetectorDetector, fdDetectorARN, "d1")
	fake := &fakeFD{detectorPages: map[string]map[string]*frauddetector.DescribeDetectorOutput{"d1": {
		"":   {DetectorVersionSummaries: []frauddetectortypes.DetectorVersionSummary{fdVersionSummary("1")}, NextToken: sdkaws.String("p2")},
		"p2": {DetectorVersionSummaries: []frauddetectortypes.DetectorVersionSummary{fdVersionSummary("2")}, NextToken: sdkaws.String("p2")},
	}}}
	_, _, err := scanFDDetectorVersions(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID,
		[]childParent{{id: "d1", arn: fdDetectorARN("d1")}})
	if err != nil {
		t.Fatalf("scanFDDetectorVersions: %v", err)
	}
	if got := fake.calls("d1"); got != 2 {
		t.Errorf("DescribeDetector called %d times; want 2 (the repeated token ends the loop)", got)
	}
	if got := len(qsRows(t, st, TypeFraudDetectorDetectorVersion)); got != 2 {
		t.Errorf("stored %d rows; want 2", got)
	}
}

func TestScanFDVersions_ParentWithoutVersionsStoresNothing(t *testing.T) {
	st := newTestStore(t)
	seedFDParents(t, st, TypeFraudDetectorDetector, fdDetectorARN, "d1")
	fake := &fakeFD{}
	_, _, err := scanFDDetectorVersions(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID,
		[]childParent{{id: "d1", arn: fdDetectorARN("d1")}})
	if err != nil {
		t.Fatalf("scanFDDetectorVersions: %v", err)
	}
	if len(fake.queried) != 1 || len(qsRows(t, st, TypeFraudDetectorDetectorVersion)) != 0 {
		t.Errorf("queried %v; want one DescribeDetector and no rows", fake.queried)
	}
}

var fdRuleARN = fdTestARN("rule", "det/r1/1")

// fdServiceStub serves every op scanFraudDetector calls: one detector and one
// model (the new fan-outs' parents), one rule (the last pre-existing phase),
// one version of each new kind, and override replacing an op's queue.
// GetDetectors runs three times: the gate, the detector phase and the rules
// phase's own listing.
func fdServiceStub(t *testing.T, override map[string][]stubCall) sdkaws.Config {
	t.Helper()
	one := func(out any) []stubCall { return []stubCall{{Output: out}} }
	detectors := &frauddetector.GetDetectorsOutput{Detectors: []frauddetectortypes.Detector{
		{Arn: sdkaws.String(fdDetectorARN("det")), DetectorId: sdkaws.String("det")},
	}}
	calls := map[string][]stubCall{
		"GetDetectors":     {{Output: detectors}, {Output: detectors}, {Output: detectors}},
		"GetEntityTypes":   one(&frauddetector.GetEntityTypesOutput{}),
		"GetEventTypes":    one(&frauddetector.GetEventTypesOutput{}),
		"GetLabels":        one(&frauddetector.GetLabelsOutput{}),
		"GetListsMetadata": one(&frauddetector.GetListsMetadataOutput{}),
		"GetOutcomes":      one(&frauddetector.GetOutcomesOutput{}),
		"GetVariables":     one(&frauddetector.GetVariablesOutput{}),
		"GetModels": one(&frauddetector.GetModelsOutput{Models: []frauddetectortypes.Model{
			{Arn: sdkaws.String(fdModelARN("mdl")), ModelId: sdkaws.String("mdl"), ModelType: fdModelType},
		}}),
		"GetExternalModels": one(&frauddetector.GetExternalModelsOutput{}),
		"GetRules": one(&frauddetector.GetRulesOutput{RuleDetails: []frauddetectortypes.RuleDetail{
			{Arn: sdkaws.String(fdRuleARN), DetectorId: sdkaws.String("det"), RuleId: sdkaws.String("r1")},
		}}),
		"DescribeDetector": one(&frauddetector.DescribeDetectorOutput{
			DetectorVersionSummaries: []frauddetectortypes.DetectorVersionSummary{fdVersionSummary("1")},
		}),
		"GetDetectorVersion": one(fdDetectorVersion("det", "1")),
		"DescribeModelVersions": one(&frauddetector.DescribeModelVersionsOutput{
			ModelVersionDetails: []frauddetectortypes.ModelVersionDetail{fdModelVersion("mdl", "1.0")},
		}),
	}
	for op, c := range override {
		calls[op] = c
	}
	return cloud9CfgWithStub(stubResponses(t, calls), testRegion)
}

// TestScanFraudDetector_VersionFanOutsAfterExistingPhases runs the whole
// service against the real SDK client (its input validators included): the
// detector and model phases hand their ids to the fan-outs, whose versions are
// contained by their parents.
func TestScanFraudDetector_VersionFanOutsAfterExistingPhases(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = fdServiceStub(t, nil)
	total, _, err := scanFraudDetector(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanFraudDetector: %v", err)
	}
	if total != 5 {
		t.Errorf("total = %d; want 5 (detector, model, rule, detector version, model version)", total)
	}
	assertQSContains(t, st, fdDetectorARN("det"), fdDetectorVersionARN("det", "1"))
	assertQSContains(t, st, fdModelARN("mdl"), fdModelVersionARN("mdl", "1.0"))
}

// TestScanFraudDetector_VersionFanOutErrorPropagates pins that each new
// fan-out's hard error reaches the service result and that every pre-existing
// phase, including the last (rules), ran before it.
func TestScanFraudDetector_VersionFanOutErrorPropagates(t *testing.T) {
	for _, op := range []string{"DescribeDetector", "GetDetectorVersion", "DescribeModelVersions"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			acct.cfg = fdServiceStub(t, map[string][]stubCall{op: {{Err: apiErr("InternalServerException", "boom")}}})
			_, _, err := scanFraudDetector(context.Background(), acct, testRegion, st, testScanID)
			if !isAPIErrorCode(err, "InternalServerException") {
				t.Fatalf("err = %v; want the %s InternalServerException", err, op)
			}
			if _, ok := qsRows(t, st, TypeFraudDetectorRule)[fdRuleARN]; !ok {
				t.Errorf("rules phase did not run before the failing %s", op)
			}
		})
	}
}
