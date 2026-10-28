package aws

import (
	"context"
	"errors"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/frauddetector"
	fdtypes "github.com/aws/aws-sdk-go-v2/service/frauddetector/types"
	"github.com/icearp/disco-cli/store"
)

// fdtTwoVersions makes every detector list versions "1" and "2".
func fdtTwoVersions(h map[string]func(any) (any, error)) {
	h["DescribeDetector"] = func(any) (any, error) {
		return &frauddetector.DescribeDetectorOutput{DetectorVersionSummaries: []fdtypes.DetectorVersionSummary{
			{DetectorVersionId: sdkaws.String("1")}, {DetectorVersionId: sdkaws.String("2")},
		}}, nil
	}
}

// A GetDetectorVersion failure costs only its own version, which has no ARN
// to be stored under: one deleted since the listing is skipped silently, a
// denial warns once under GetDetectorVersion, a throttle after retries is a
// transient error and any other code a hard one. The detector's other
// version and the other detector's versions are stored.
func TestScanFDDetectorVersions_VersionReadFailures(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		outcome   serviceOutcome
		wantWarns int
	}{
		{"not found", fdtNotFound, 0, 0},
		{"denied", fdtDenied, 0, 1},
		{"other error", fdtBadInput, outcomeError, 0},
		{"transient", fdtThrottled, outcomeTransient, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, fdGetDetectorVersionOp)
			calls := newFDTCalls()
			h := fdtHandlers(calls)
			fdtTwoVersions(h)
			get := h["GetDetectorVersion"]
			h["GetDetectorVersion"] = func(in any) (any, error) {
				v := in.(*frauddetector.GetDetectorVersionInput)
				if sv(v.DetectorId) == "detector_t" && sv(v.DetectorVersionId) == "1" {
					return nil, c.err
				}
				return get(in)
			}

			_, _, err := fdtScan(context.Background(), st, h)
			if c.outcome == 0 && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.outcome != 0 {
				assertGlueOutcome(t, err, c.outcome)
			}
			if c.outcome == outcomeError {
				assertAPICode(t, err, "ValidationException")
			}
			if *warns != c.wantWarns || *opWarns != c.wantWarns {
				t.Errorf("warnings = %d (%s: %d); want %d under the op", *warns, fdGetDetectorVersionOp, *opWarns, c.wantWarns)
			}
			rows := rowsByNativeID(t, st, TypeFraudDetectorDetectorVersion)
			if _, ok := rows[fdtDetectorVersion("t", "1")]; ok {
				t.Error("the failed version is stored; want it dropped")
			}
			for _, want := range []string{fdtDetectorVersion("t", "2"), fdtDetectorVersion("b", "1"), fdtDetectorVersion("b", "2")} {
				if _, ok := rows[want]; !ok {
					t.Errorf("%s missing; want the failed version's siblings stored", want)
				}
			}
		})
	}
}

// A throttled version read beside a hard error of the same op is demoted to
// one warning. After a failed write, of the versions or of their closure, it
// is returned with the write's error instead, and no warning is sent, not
// even for a denied version read.
func TestScanFDDetectorVersions_ThrottledRead(t *testing.T) {
	// fail makes version 1 of detector_t throttled and, when other is set,
	// that of detector_b fail with other.
	fail := func(h map[string]func(any) (any, error), other error) {
		fdtTwoVersions(h)
		get := h["GetDetectorVersion"]
		h["GetDetectorVersion"] = func(in any) (any, error) {
			v := in.(*frauddetector.GetDetectorVersionInput)
			switch {
			case sv(v.DetectorVersionId) != "1":
			case sv(v.DetectorId) == "detector_t":
				return nil, fdtThrottled
			case other != nil:
				return nil, other
			}
			return get(in)
		}
	}
	t.Run("beside a hard error", func(t *testing.T) {
		st := newTestStore(t)
		warns := countOpWarnings(st, fdGetDetectorVersionOp)
		h := fdtHandlers(newFDTCalls())
		fail(h, fdtBadInput)
		_, _, err := fdtScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		if *warns != 1 {
			t.Errorf("%s warnings = %d; want 1", fdGetDetectorVersionOp, *warns)
		}
	})
	for _, c := range []struct {
		name   string
		refuse func(t *testing.T, st *store.Store)
	}{
		{"after a failed write", func(t *testing.T, st *store.Store) {
			refuseStoreWrites(t, st, TypeFraudDetectorDetectorVersion)
		}},
		{"after a failed closure write", refuseClosureWrites},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			c.refuse(t, st)
			calls := newFDTCalls()
			h := fdtHandlers(calls)
			fail(h, fdtDenied)
			_, _, err := fdtScan(context.Background(), st, h)
			if !errors.Is(err, store.ErrStoreWrite) || !joinCarriesAPIErrorCode(err, "ThrottlingException") {
				t.Errorf("err = %v; want store.ErrStoreWrite carrying the throttle", err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
			if n := calls.count("DescribeModelVersions"); n != 0 {
				t.Errorf("DescribeModelVersions called %d times after the failed write", n)
			}
		})
	}
}

// A version listed without an id, or read without an ARN, is not stored and
// is warned about under DescribeDetector, once per reason.
func TestScanFDDetectorVersions_UnstorableVersionsWarn(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, "frauddetector:DescribeDetector")
	h := fdtHandlers(newFDTCalls())
	h["DescribeDetector"] = func(any) (any, error) {
		return &frauddetector.DescribeDetectorOutput{DetectorVersionSummaries: []fdtypes.DetectorVersionSummary{
			{Description: sdkaws.String("no id")}, {DetectorVersionId: sdkaws.String("1")},
		}}, nil
	}
	h["GetDetectorVersion"] = func(in any) (any, error) {
		v := in.(*frauddetector.GetDetectorVersionInput)
		return &frauddetector.GetDetectorVersionOutput{DetectorId: v.DetectorId, DetectorVersionId: v.DetectorVersionId}, nil
	}

	if _, _, err := fdtScan(context.Background(), st, h); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if n := len(rowsByNativeID(t, st, TypeFraudDetectorDetectorVersion)); n != 0 {
		t.Errorf("stored %d detector versions; want 0", n)
	}
	var keys []string
	for _, w := range *warnings {
		keys = append(keys, w.Message)
	}
	if len(keys) != 2 || !strings.Contains(keys[0]+keys[1], "(a version with no id) of detector detector_") || !strings.Contains(keys[0]+keys[1], "detector_t/1") {
		t.Errorf("warnings = %v; want one per reason, naming the id-less version and detector_t/1", keys)
	}
}

// DescribeDetector's pages are all read, each version read once; a
// repeated token ends that detector's listing as an error, keeping the
// versions of the pages read and the other detector's.
func TestScanFDDetectorVersions_Pages(t *testing.T) {
	for _, c := range []struct {
		name   string
		repeat bool
	}{{"two pages", false}, {"repeated token", true}} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			calls := newFDTCalls()
			h := fdtHandlers(calls)
			h["DescribeDetector"] = func(in any) (any, error) {
				d := in.(*frauddetector.DescribeDetectorInput)
				calls.called("DescribeDetector", in)
				if d.NextToken == nil {
					return &frauddetector.DescribeDetectorOutput{DetectorVersionSummaries: []fdtypes.DetectorVersionSummary{{DetectorVersionId: sdkaws.String("1")}}, NextToken: sdkaws.String("p2")}, nil
				}
				next := (*string)(nil)
				if c.repeat && sv(d.DetectorId) == "detector_t" {
					next = sdkaws.String("p2")
				}
				return &frauddetector.DescribeDetectorOutput{DetectorVersionSummaries: []fdtypes.DetectorVersionSummary{{DetectorVersionId: sdkaws.String("2")}}, NextToken: next}, nil
			}

			_, _, err := fdtScan(context.Background(), st, h)
			if c.repeat != errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken %v", err, c.repeat)
			}
			if !c.repeat && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			rows := rowsByNativeID(t, st, TypeFraudDetectorDetectorVersion)
			for _, n := range []string{"t", "b"} {
				for _, v := range []string{"1", "2"} {
					if _, ok := rows[fdtDetectorVersion(n, v)]; !ok {
						t.Errorf("version %s of detector_%s missing", v, n)
					}
				}
				assertContainsNative(t, st, fdtDetector(n), fdtDetectorVersion(n, "2"))
			}
			if got := calls.count("DescribeDetector"); got != 4 {
				t.Errorf("DescribeDetector calls = %d; want 4 (two pages per detector)", got)
			}
			if got := calls.count("GetDetectorVersion"); got != 4 {
				t.Errorf("GetDetectorVersion calls = %d; want 4", got)
			}
		})
	}
}

// A per-detector or per-model listing that no longer finds its parent skips
// it silently; any other error is returned, and the other parent's rows are
// stored either way.
func TestScanFD_PerParentListingFailures(t *testing.T) {
	for _, c := range []struct {
		op, childType string
		parentKey     func(in any) string
		child         func(n string) string
	}{
		{"DescribeDetector", TypeFraudDetectorDetectorVersion, func(in any) string {
			return sv(in.(*frauddetector.DescribeDetectorInput).DetectorId)
		}, func(n string) string { return fdtDetectorVersion(n, "1") }},
		{"DescribeModelVersions", TypeFraudDetectorModelVersion, func(in any) string {
			return sv(in.(*frauddetector.DescribeModelVersionsInput).ModelId)
		}, fdtModelVersion},
	} {
		for _, e := range []struct {
			name string
			err  error
		}{{"not found", fdtNotFound}, {"other error", fdtBadInput}} {
			t.Run(c.op+"/"+e.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countWarnings(st)
				h := fdtHandlers(newFDTCalls())
				next := h[c.op]
				h[c.op] = func(in any) (any, error) {
					if strings.HasSuffix(c.parentKey(in), "_t") {
						return nil, e.err
					}
					return next(in)
				}

				_, _, err := fdtScan(context.Background(), st, h)
				if e.err == fdtNotFound && err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if e.err == fdtBadInput {
					assertAPICode(t, err, "ValidationException")
				}
				if *warns != 0 {
					t.Errorf("warnings = %d; want 0", *warns)
				}
				rows := rowsByNativeID(t, st, c.childType)
				if _, ok := rows[c.child("b")]; !ok || len(rows) != 1 {
					t.Errorf("stored %d %s rows, %s among them %v; want only it", len(rows), c.childType, c.child("b"), ok)
				}
			})
		}
	}
}

// A model listed without a ModelType is stored, but its versions, which
// DescribeModelVersions keys on the type too, are not read: that is warned
// about under GetModels.
func TestScanFDModels_NoTypeNotReadForVersions(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, "frauddetector:GetModels")
	calls := newFDTCalls()
	h := fdtHandlers(calls)
	h["GetModels"] = func(any) (any, error) {
		return &frauddetector.GetModelsOutput{Models: []fdtypes.Model{{Arn: sdkaws.String(fdtItemARN("model", "t")), ModelId: sdkaws.String("model_t")}}}, nil
	}

	if _, _, err := fdtScan(context.Background(), st, h); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if _, ok := fdtRowOf(t, st, TypeFraudDetectorModel, fdtItemARN("model", "t")); !ok {
		t.Error("model_t not stored")
	}
	if n := calls.count("DescribeModelVersions"); n != 0 {
		t.Errorf("DescribeModelVersions called %d times; want 0", n)
	}
	if len(*warnings) != 1 {
		t.Errorf("GetModels warnings = %d; want 1", len(*warnings))
	}
}

// Two models sharing a ModelId under different ModelTypes are distinct
// parents: each one's versions are read with its own type and stored under
// it.
func TestScanFDModelVersions_SameModelIDTwoTypes(t *testing.T) {
	st := newTestStore(t)
	types := []fdtypes.ModelTypeEnum{fdtypes.ModelTypeEnumOnlineFraudInsights, fdtypes.ModelTypeEnumTransactionFraudInsights}
	modelARN := func(typ fdtypes.ModelTypeEnum) string { return fdtARN("model/" + string(typ) + "/shared") }
	versionARN := func(typ fdtypes.ModelTypeEnum) string { return fdtARN("model-version/" + string(typ) + "/shared/1.0") }
	h := fdtHandlers(newFDTCalls())
	h["GetModels"] = func(any) (any, error) {
		var models []fdtypes.Model
		for _, typ := range types {
			models = append(models, fdtypes.Model{Arn: sdkaws.String(modelARN(typ)), ModelId: sdkaws.String("shared"), ModelType: typ})
		}
		return &frauddetector.GetModelsOutput{Models: models}, nil
	}
	h["DescribeModelVersions"] = func(in any) (any, error) {
		m := in.(*frauddetector.DescribeModelVersionsInput)
		return &frauddetector.DescribeModelVersionsOutput{ModelVersionDetails: []fdtypes.ModelVersionDetail{{
			Arn: sdkaws.String(versionARN(m.ModelType)), ModelId: m.ModelId, ModelType: m.ModelType, ModelVersionNumber: sdkaws.String("1.0"),
		}}}, nil
	}

	if _, _, err := fdtScan(context.Background(), st, h); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if n := len(rowsByNativeID(t, st, TypeFraudDetectorModelVersion)); n != 2 {
		t.Errorf("stored %d model versions; want 2", n)
	}
	for _, typ := range types {
		assertContainsNative(t, st, modelARN(typ), versionARN(typ))
	}
}
