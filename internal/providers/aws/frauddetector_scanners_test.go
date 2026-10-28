package aws

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/frauddetector"
	fdtypes "github.com/aws/aws-sdk-go-v2/service/frauddetector/types"
	"github.com/icearp/disco-cli/store"
)

// The fixture: every account-wide listing serves "<kind>_t" on its first
// page and "<kind>_b" on its second; the variables' second page also carries
// variable_gov_t, the account's own in aws-us-gov. Each detector holds one
// rule and version "1", each model version "1.0". Every ARN containing "_t"
// is tagged, its tags read over two ListTagsForResource pages.

const fdtModelType = fdtypes.ModelTypeEnumOnlineFraudInsights

var (
	fdtDenied    = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: frauddetector:Get")
	fdtClosed    = apiErr("AccessDeniedException", "")
	fdtNotFound  = apiErr("ResourceNotFoundException", "gone")
	fdtThrottled = apiErr("ThrottlingException", "rate exceeded")
	fdtBadInput  = apiErr("ValidationException", "bad")
)

func fdtARN(res string) string {
	return arnFor("aws", "frauddetector", testRegion, testAccountID, res)
}

func fdtGovVariable() string {
	return arnFor("aws-us-gov", "frauddetector", "us-gov-west-1", testAccountID, "variable/variable_gov_t")
}

// fdtKinds is every account-wide listing: its op, the ARN kind and id
// prefix of its items, and their type.
var fdtKinds = []struct{ op, kind, typ string }{
	{"GetDetectors", "detector", TypeFraudDetectorDetector},
	{"GetEntityTypes", "entity-type", TypeFraudDetectorEntityType},
	{"GetEventTypes", "event-type", TypeFraudDetectorEventType},
	{"GetLabels", "label", TypeFraudDetectorLabel},
	{"GetListsMetadata", "list", TypeFraudDetectorList},
	{"GetOutcomes", "outcome", TypeFraudDetectorOutcome},
	{"GetVariables", "variable", TypeFraudDetectorVariable},
	{"GetModels", "model", TypeFraudDetectorModel},
	{"GetExternalModels", "external-model", TypeFraudDetectorExternalModel},
}

// fdtItemARN is the ARN of kind's item on page n ("t" or "b").
func fdtItemARN(kind, n string) string {
	id := strings.ReplaceAll(kind, "-", "_") + "_" + n
	if kind == "model" {
		return fdtARN("model/" + string(fdtModelType) + "/" + id)
	}
	return fdtARN(kind + "/" + id)
}

func fdtDetector(n string) string { return fdtItemARN("detector", n) }

func fdtRule(n string) string { return fdtARN("rule/detector_" + n + "/rule_" + n + "/1") }

func fdtDetectorVersion(n, v string) string {
	return fdtARN("detector-version/detector_" + n + "/" + v)
}

func fdtModelVersion(n string) string {
	return fdtARN("model-version/" + string(fdtModelType) + "/model_" + n + "/1.0")
}

func fdtTagged(arn string) bool { return strings.Contains(arn, "_t") }

type fdtRow struct{ typ, arn, name string }

// fdtRows is every row the fixture stores, with its Name.
func fdtRows() []fdtRow {
	rows := []fdtRow{{TypeFraudDetectorVariable, fdtGovVariable(), "name variable_gov_t"}}
	for _, n := range []string{"t", "b"} {
		for _, k := range fdtKinds {
			id := strings.ReplaceAll(k.kind, "-", "_") + "_" + n
			name := "name " + id
			switch k.typ {
			case TypeFraudDetectorDetector, TypeFraudDetectorModel:
				name = id
			case TypeFraudDetectorExternalModel:
				name = "endpoint " + id
			}
			rows = append(rows, fdtRow{k.typ, fdtItemARN(k.kind, n), name})
		}
		rows = append(rows,
			fdtRow{TypeFraudDetectorRule, fdtRule(n), "rule_" + n},
			fdtRow{TypeFraudDetectorDetectorVersion, fdtDetectorVersion(n, "1"), "1"},
			fdtRow{TypeFraudDetectorModelVersion, fdtModelVersion(n), "1.0"})
	}
	return rows
}

// fdtCalls records the stubbed calls and answers ListTagsForResource from
// tags and errs, keyed by ARN: a tagged ARN's "type" tag on the first page,
// its "page" tag on the second.
type fdtCalls struct{ *bacCalls }

func newFDTCalls() *fdtCalls {
	c := &fdtCalls{newBACCalls()}
	for _, r := range fdtRows() {
		if fdtTagged(r.arn) {
			c.tags[r.arn] = map[string]string{"type": r.typ, "page": "2"}
		}
	}
	return c
}

func (c *fdtCalls) listTags(in any) (any, error) {
	tin := in.(*frauddetector.ListTagsForResourceInput)
	arn := sv(tin.ResourceARN)
	c.called("ListTagsForResource", in)
	c.mu.Lock()
	if tin.NextToken == nil {
		c.tagARNs = append(c.tagARNs, arn)
	}
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if tags == nil {
		return &frauddetector.ListTagsForResourceOutput{}, nil
	}
	if tin.NextToken == nil {
		return &frauddetector.ListTagsForResourceOutput{Tags: []fdtypes.Tag{{Key: sdkaws.String("type"), Value: sdkaws.String(tags["type"])}}, NextToken: sdkaws.String("tags-2")}, nil
	}
	return &frauddetector.ListTagsForResourceOutput{Tags: []fdtypes.Tag{{Key: sdkaws.String("page"), Value: sdkaws.String(tags["page"])}}}, nil
}

// fdtPage is op's page n of the account-wide listing of kind.
func fdtPage(op, kind, n string, next *string) any {
	id := strings.ReplaceAll(kind, "-", "_") + "_" + n
	arn := sdkaws.String(fdtItemARN(kind, n))
	name := sdkaws.String("name " + id)
	switch op {
	case "GetDetectors":
		return &frauddetector.GetDetectorsOutput{Detectors: []fdtypes.Detector{{Arn: arn, DetectorId: &id}}, NextToken: next}
	case "GetEntityTypes":
		return &frauddetector.GetEntityTypesOutput{EntityTypes: []fdtypes.EntityType{{Arn: arn, Name: name}}, NextToken: next}
	case "GetEventTypes":
		return &frauddetector.GetEventTypesOutput{EventTypes: []fdtypes.EventType{{Arn: arn, Name: name}}, NextToken: next}
	case "GetLabels":
		return &frauddetector.GetLabelsOutput{Labels: []fdtypes.Label{{Arn: arn, Name: name}}, NextToken: next}
	case "GetListsMetadata":
		return &frauddetector.GetListsMetadataOutput{Lists: []fdtypes.AllowDenyList{{Arn: arn, Name: name}}, NextToken: next}
	case "GetOutcomes":
		return &frauddetector.GetOutcomesOutput{Outcomes: []fdtypes.Outcome{{Arn: arn, Name: name}}, NextToken: next}
	case "GetVariables":
		items := []fdtypes.Variable{{Arn: arn, Name: name}}
		if n == "b" {
			items = append(items, fdtypes.Variable{Arn: sdkaws.String(fdtGovVariable()), Name: sdkaws.String("name variable_gov_t")})
		}
		return &frauddetector.GetVariablesOutput{Variables: items, NextToken: next}
	case "GetModels":
		return &frauddetector.GetModelsOutput{Models: []fdtypes.Model{{Arn: arn, ModelId: &id, ModelType: fdtModelType}}, NextToken: next}
	default:
		return &frauddetector.GetExternalModelsOutput{ExternalModels: []fdtypes.ExternalModel{{Arn: arn, ModelEndpoint: sdkaws.String("endpoint " + id)}}, NextToken: next}
	}
}

// fdtSuffix is the page letter a fixture id ends with.
func fdtSuffix(id string) string { return id[strings.LastIndex(id, "_")+1:] }

// fdtHandlers answers every op the scan calls with the fixture (fdtRows).
func fdtHandlers(c *fdtCalls) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListTagsForResource": c.listTags,
		"GetRules": c.answer("GetRules", func(in any) any {
			n := fdtSuffix(sv(in.(*frauddetector.GetRulesInput).DetectorId))
			return &frauddetector.GetRulesOutput{RuleDetails: []fdtypes.RuleDetail{
				{Arn: sdkaws.String(fdtRule(n)), RuleId: sdkaws.String("rule_" + n), DetectorId: sdkaws.String("detector_" + n), RuleVersion: sdkaws.String("1")},
			}}
		}),
		"DescribeDetector": c.answer("DescribeDetector", func(any) any {
			return &frauddetector.DescribeDetectorOutput{DetectorVersionSummaries: []fdtypes.DetectorVersionSummary{
				{DetectorVersionId: sdkaws.String("1"), Status: fdtypes.DetectorVersionStatusActive},
			}}
		}),
		"GetDetectorVersion": c.answer("GetDetectorVersion", func(in any) any {
			v := in.(*frauddetector.GetDetectorVersionInput)
			return &frauddetector.GetDetectorVersionOutput{
				Arn:        sdkaws.String(fdtDetectorVersion(fdtSuffix(sv(v.DetectorId)), sv(v.DetectorVersionId))),
				DetectorId: v.DetectorId, DetectorVersionId: v.DetectorVersionId,
				Status: fdtypes.DetectorVersionStatusActive, RuleExecutionMode: fdtypes.RuleExecutionModeFirstMatched,
			}
		}),
		"DescribeModelVersions": c.answer("DescribeModelVersions", func(in any) any {
			m := in.(*frauddetector.DescribeModelVersionsInput)
			if m.ModelType != fdtModelType {
				return &frauddetector.DescribeModelVersionsOutput{}
			}
			return &frauddetector.DescribeModelVersionsOutput{ModelVersionDetails: []fdtypes.ModelVersionDetail{{
				Arn: sdkaws.String(fdtModelVersion(fdtSuffix(sv(m.ModelId)))), ModelId: m.ModelId, ModelType: m.ModelType,
				ModelVersionNumber: sdkaws.String("1.0"), Status: sdkaws.String("ACTIVE"),
			}}}
		}),
	}
	for _, k := range fdtKinds {
		h[k.op] = c.answer(k.op, func(in any) any {
			if bacField[*string](in, "NextToken") == nil {
				return fdtPage(k.op, k.kind, "t", sdkaws.String("p2"))
			}
			return fdtPage(k.op, k.kind, "b", nil)
		})
	}
	return h
}

// fdtScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func fdtScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, fdtDenied)
	return scanFraudDetector(ctx, acct, testRegion, st, testScanID)
}

func fdtRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// Every type stores the tags ListTagsForResource returns for its ARN, read
// over both of the op's pages, and nil for a resource without tags; every
// stored row is sent to the tag op, the aws-us-gov variable included. Both
// pages of every account-wide listing are stored, each listing asks for its
// op's page cap, and the scan reports no error or warning.
func TestScanFraudDetector_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newFDTCalls()

	total, inserted, err := fdtScan(context.Background(), st, fdtHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := fdtRows()
	if total != len(rows) || inserted != len(rows) || len(rows) != 25 {
		t.Errorf("total, inserted = %d, %d; want %d (25 fixture rows)", total, inserted, len(rows))
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantTagARNs []string
	for _, want := range rows {
		r, ok := fdtRowOf(t, st, want.typ, want.arn)
		if !ok {
			t.Errorf("%s %s not stored", want.typ, want.arn)
			continue
		}
		wantTagARNs = append(wantTagARNs, want.arn)
		if fdtTagged(want.arn) {
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": want.typ, "page": "2"})
		} else if r.TagsJSON != nil {
			t.Errorf("%s %s TagsJSON = %s; want nil", want.typ, want.arn, *r.TagsJSON)
		}
		if sv(r.Name) != want.name {
			t.Errorf("%s %s Name = %q; want %q", want.typ, want.arn, sv(r.Name), want.name)
		}
	}
	got := slices.Clone(calls.tagARNs)
	slices.Sort(got)
	slices.Sort(wantTagARNs)
	if !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantTagARNs)
	}
	for op, want := range map[string]int32{
		"GetDetectors": 10, "GetEntityTypes": 10, "GetEventTypes": 10, "GetLabels": 50, "GetListsMetadata": 50,
		"GetOutcomes": 100, "GetVariables": 100, "GetModels": 10, "GetExternalModels": 10, "GetRules": 100,
		"DescribeDetector": 2500, "DescribeModelVersions": 10, "ListTagsForResource": 50,
	} {
		caps := calls.maxResults[op]
		if len(caps) == 0 {
			t.Errorf("%s sent no MaxResults", op)
		}
		for _, n := range caps {
			if n != want {
				t.Errorf("%s MaxResults = %d; want %d", op, n, want)
			}
		}
	}
	for _, n := range []string{"t", "b"} {
		if r, _ := fdtRowOf(t, st, TypeFraudDetectorDetectorVersion, fdtDetectorVersion(n, "1")); sv(r.Status) != "ACTIVE" {
			t.Errorf("detector version %s Status = %q; want ACTIVE", n, sv(r.Status))
		}
		assertContainsNative(t, st, fdtDetector(n), fdtDetectorVersion(n, "1"))
		assertContainsNative(t, st, fdtItemARN("model", n), fdtModelVersion(n))
	}
}

// A rescan stores the same rows without inserting any.
func TestScanFraudDetector_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newFDTCalls()
	if _, _, err := fdtScan(context.Background(), st, fdtHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := fdtScan(context.Background(), st, fdtHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != 25 || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want 25, 0", total, inserted)
	}
}

// A failed tag read keeps the row without tags unless the resource is gone:
// a denial warns once under frauddetector:ListTagsForResource, a throttle
// after retries is a transient error, any other code a hard one. A detector
// the tag read no longer finds is dropped, and its rules and versions are
// never listed. A rule and a detector version, from per-detector listings,
// behave alike.
func TestScanFraudDetector_TagReadFailures(t *testing.T) {
	for _, target := range []fdtRow{
		{typ: TypeFraudDetectorDetector, arn: fdtDetector("t")},
		{typ: TypeFraudDetectorRule, arn: fdtRule("t")},
		{typ: TypeFraudDetectorDetectorVersion, arn: fdtDetectorVersion("t", "1")},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				kept      bool
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", fdtDenied, true, 0, 1},
				{"not found", fdtNotFound, false, 0, 0},
				{"other error", fdtBadInput, true, outcomeError, 0},
				{"transient", fdtThrottled, true, outcomeTransient, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, fdListTagsOp)
					calls := newFDTCalls()
					calls.errs[target.arn] = c.err

					_, _, err := fdtScan(context.Background(), st, fdtHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.outcome == outcomeError {
						assertAPICode(t, err, "ValidationException")
					}
					r, ok := fdtRowOf(t, st, target.typ, target.arn)
					if ok != c.kept {
						t.Fatalf("%s row stored = %v; want %v", target.typ, ok, c.kept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if target.typ == TypeFraudDetectorDetector {
						_, child := fdtRowOf(t, st, TypeFraudDetectorDetectorVersion, fdtDetectorVersion("t", "1"))
						_, sibling := fdtRowOf(t, st, TypeFraudDetectorRule, fdtRule("b"))
						if child != c.kept || !sibling {
							t.Errorf("version of detector_t stored %v, rule of detector_b %v; want detector_t's only when it is kept, detector_b's always", child, sibling)
						}
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", fdListTagsOp, *warnings, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
				})
			}
		})
	}
}

// A tag op answering a repeated page token fails the read: the row is kept
// without tags and the scan returns errRepeatedNextToken.
func TestScanFraudDetector_TagPagesRepeatedToken(t *testing.T) {
	st := newTestStore(t)
	calls := newFDTCalls()
	h := fdtHandlers(calls)
	h["ListTagsForResource"] = func(in any) (any, error) {
		if sv(in.(*frauddetector.ListTagsForResourceInput).ResourceARN) == fdtGovVariable() {
			return &frauddetector.ListTagsForResourceOutput{NextToken: sdkaws.String("same")}, nil
		}
		return calls.listTags(in)
	}

	_, _, err := fdtScan(context.Background(), st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	r, ok := fdtRowOf(t, st, TypeFraudDetectorVariable, fdtGovVariable())
	if !ok || r.TagsJSON != nil {
		t.Errorf("gov variable stored %v with TagsJSON %v; want stored without tags", ok, r.TagsJSON)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanFraudDetector_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, fdListTagsOp)
	calls := newFDTCalls()
	calls.errs[fdtItemARN("label", "t")] = fdtThrottled
	calls.errs[fdtModelVersion("b")] = fdtBadInput

	_, _, err := fdtScan(context.Background(), st, fdtHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", fdListTagsOp, *warns)
	}
}

// fdtListOps is every listing op the scan calls, GetDetectorVersion included.
func fdtListOps() []string {
	ops := []string{"GetRules", "DescribeDetector", "GetDetectorVersion", "DescribeModelVersions"}
	for _, k := range fdtKinds {
		ops = append(ops, k.op)
	}
	return ops
}

// A denied listing warns once under its own op and costs no other phase its
// rows.
func TestScanFraudDetector_DeniedListingWarnsUnderItsOp(t *testing.T) {
	for _, op := range fdtListOps() {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, "frauddetector:"+op)
			h := fdtHandlers(newFDTCalls())
			h[op] = func(any) (any, error) { return nil, fdtDenied }

			total, _, err := fdtScan(context.Background(), st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || *opWarns != 1 {
				t.Errorf("warnings = %d (frauddetector:%s: %d); want 1 under the op", *warns, op, *opWarns)
			}
			if stored := countStoredRows(t, st); total == 0 || total != stored {
				t.Errorf("total = %d, stored rows = %d; want the other phases' rows counted", total, stored)
			}
		})
	}
}

// The closed-to-new-customers answer (an AccessDeniedException without a
// message) on a listing's first page, while no row is stored, reports the
// service not entitled and runs no later listing, whichever listing meets
// it. Met once rows were stored, or on a later page, it is a denial: warned
// about under its op, the rows kept, every later phase run.
func TestScanFraudDetector_ClosedToNewCustomers(t *testing.T) {
	empty := func(op string) func(any) (any, error) {
		return func(any) (any, error) {
			switch op {
			case "GetDetectors":
				return &frauddetector.GetDetectorsOutput{}, nil
			default:
				return &frauddetector.GetEntityTypesOutput{}, nil
			}
		}
	}
	for _, c := range []struct {
		name, op string
		setup    func(h map[string]func(any) (any, error))
	}{
		{"first listing", "GetDetectors", func(h map[string]func(any) (any, error)) {
			h["GetDetectors"] = func(any) (any, error) { return nil, fdtClosed }
		}},
		{"later listing, nothing stored", "GetEventTypes", func(h map[string]func(any) (any, error)) {
			h["GetDetectors"] = empty("GetDetectors")
			h["GetEntityTypes"] = empty("GetEntityTypes")
			h["GetEventTypes"] = func(any) (any, error) { return nil, fdtClosed }
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			calls := newFDTCalls()
			h := fdtHandlers(calls)
			c.setup(h)

			total, _, err := fdtScan(context.Background(), st, h)
			assertGlueOutcome(t, err, outcomeNotEntitled)
			if total != 0 || *warns != 0 {
				t.Errorf("total = %d, warnings = %d; want 0, 0", total, *warns)
			}
			if n := calls.count("GetLabels"); n != 0 {
				t.Errorf("GetLabels called %d times after %s said closed", n, c.op)
			}
		})
	}
	for _, c := range []struct {
		name, op string
		page     int
	}{
		{"later listing after stored rows", "GetEntityTypes", 1},
		{"second page", "GetDetectors", 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, "frauddetector:"+c.op)
			calls := newFDTCalls()
			h := fdtHandlers(calls)
			h[c.op] = failOnCall(c.page, fdtClosed, h[c.op])

			total, _, err := fdtScan(context.Background(), st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || *opWarns != 1 {
				t.Errorf("warnings = %d (frauddetector:%s: %d); want 1 under the op", *warns, c.op, *opWarns)
			}
			if _, ok := fdtRowOf(t, st, TypeFraudDetectorDetector, fdtDetector("t")); !ok || total == 0 {
				t.Errorf("detector_t stored %v, total %d; want the stored rows kept", ok, total)
			}
			if calls.count("DescribeModelVersions") == 0 {
				t.Error("DescribeModelVersions not called; want every later phase run")
			}
		})
	}
}

// The closed answer after an earlier listing failed or was denied, with
// nothing stored, is a denial of its op: the earlier result is kept, the
// service is not reported not entitled, and every later phase runs.
func TestScanFraudDetector_ClosedAfterEarlierFailure(t *testing.T) {
	for _, c := range []struct {
		name      string
		earlier   error
		outcome   serviceOutcome
		wantWarns int
	}{
		{"throttled", fdtThrottled, outcomeTransient, 1},
		{"denied", fdtDenied, 0, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			closedWarns := countOpWarnings(st, "frauddetector:GetEntityTypes")
			calls := newFDTCalls()
			h := fdtHandlers(calls)
			h["GetDetectors"] = func(any) (any, error) { return nil, c.earlier }
			h["GetEntityTypes"] = func(any) (any, error) { return nil, fdtClosed }

			_, _, err := fdtScan(context.Background(), st, h)
			if c.outcome == 0 && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.outcome != 0 {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, "ThrottlingException")
			}
			if *warns != c.wantWarns || *closedWarns != 1 {
				t.Errorf("warnings = %d (GetEntityTypes: %d); want %d with 1 under GetEntityTypes", *warns, *closedWarns, c.wantWarns)
			}
			if calls.count("GetExternalModels") == 0 {
				t.Error("GetExternalModels not called; want every later phase run")
			}
		})
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanFraudDetector_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeFraudDetectorDetector)
	calls := newFDTCalls()
	calls.errs[fdtDetector("t")] = fdtDenied

	_, _, err := fdtScan(context.Background(), st, fdtHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, op := range []string{"GetEntityTypes", "GetRules", "DescribeDetector"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write", op, n)
		}
	}
}

// A listing's failed page keeps the pages read before it, and no listing's
// failure stops the phases after it. A detector a per-detector listing no
// longer finds is skipped silently, its sibling's rows stored.
func TestScanFraudDetector_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newFDTCalls()
	h := fdtHandlers(calls)
	h["GetDetectors"] = failOnCall(2, apiErr("ConflictException", "page 2"), h["GetDetectors"])
	h["GetLabels"] = func(any) (any, error) { return nil, fdtBadInput }
	rules := h["GetRules"]
	h["GetRules"] = func(in any) (any, error) {
		if sv(in.(*frauddetector.GetRulesInput).DetectorId) == "detector_t" {
			return nil, fdtNotFound
		}
		return rules(in)
	}

	_, _, err := fdtScan(context.Background(), st, h)
	for _, code := range []string{"ConflictException", "ValidationException"} {
		if !joinCarriesAPIErrorCode(err, code) {
			t.Errorf("err = %v; want it to carry %s", err, code)
		}
	}
	if joinCarriesAPIErrorCode(err, "ResourceNotFoundException") {
		t.Errorf("err = %v; want the detector GetRules no longer finds skipped", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, want := range []fdtRow{
		{typ: TypeFraudDetectorDetector, arn: fdtDetector("t")},
		{typ: TypeFraudDetectorDetectorVersion, arn: fdtDetectorVersion("t", "1")},
		{typ: TypeFraudDetectorModelVersion, arn: fdtModelVersion("b")},
	} {
		if _, ok := fdtRowOf(t, st, want.typ, want.arn); !ok {
			t.Errorf("%s %s missing", want.typ, want.arn)
		}
	}
	for _, gone := range []fdtRow{
		{typ: TypeFraudDetectorDetector, arn: fdtDetector("b")},
		{typ: TypeFraudDetectorRule, arn: fdtRule("t")},
		{typ: TypeFraudDetectorLabel, arn: fdtItemARN("label", "t")},
	} {
		if _, ok := fdtRowOf(t, st, gone.typ, gone.arn); ok {
			t.Errorf("%s %s stored; want it not read", gone.typ, gone.arn)
		}
	}
}

// Items listed without an ARN, from an account-wide or a per-parent
// listing, are not stored and are warned about once per listing, under a
// key that is never empty.
func TestScanFraudDetector_UnstorableItemsWarn(t *testing.T) {
	for _, c := range []struct{ op, key string }{
		{"GetOutcomes", "(an element with no id)"},
		{"GetRules", "(a rule with no id) of detector detector_"},
		{"DescribeModelVersions", "(a version with no number) of model arn:"},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, "frauddetector:"+c.op)
			h := fdtHandlers(newFDTCalls())
			h[c.op] = func(any) (any, error) {
				switch c.op {
				case "GetOutcomes":
					return &frauddetector.GetOutcomesOutput{Outcomes: []fdtypes.Outcome{{}, {}}}, nil
				case "GetRules":
					return &frauddetector.GetRulesOutput{RuleDetails: []fdtypes.RuleDetail{{}}}, nil
				default:
					return &frauddetector.DescribeModelVersionsOutput{ModelVersionDetails: []fdtypes.ModelVersionDetail{{}}}, nil
				}
			}

			if _, _, err := fdtScan(context.Background(), st, h); err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if len(*warnings) != 1 || !strings.Contains((*warnings)[0].Message, c.key) {
				t.Errorf("warnings = %v; want one naming %q", *warnings, c.key)
			}
		})
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanFraudDetector_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	h := fdtHandlers(newFDTCalls())
	h["GetExternalModels"] = func(any) (any, error) {
		return fdtPage("GetExternalModels", "external-model", "t", sdkaws.String("same")), nil
	}

	_, _, err := fdtScan(context.Background(), st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if _, ok := fdtRowOf(t, st, TypeFraudDetectorExternalModel, fdtItemARN("external-model", "t")); !ok {
		t.Error("external model from the pages read missing")
	}
}

// Once ctx ends no further phase starts, and the stop is reported once
// although the phase in flight saw it in its listing.
func TestScanFraudDetector_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newFDTCalls()
	h := fdtHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	labels := h["GetLabels"]
	h["GetLabels"] = func(in any) (any, error) {
		if in.(*frauddetector.GetLabelsInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return labels(in)
	}

	_, _, err := fdtScan(ctx, st, h)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
	}
	if n := calls.count("GetListsMetadata"); n != 0 {
		t.Errorf("GetListsMetadata called %d times after ctx ended", n)
	}
}

// An NXDOMAIN on the first listing, with nothing stored, reports the region
// unavailable and runs no later listing. After rows were stored it is a
// failure of this scan: the counts are kept.
func TestScanFraudDetector_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "frauddetector.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("first listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newFDTCalls()
		h := fdtHandlers(calls)
		h["GetDetectors"] = func(any) (any, error) { return nil, nx }
		total, _, err := fdtScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 || calls.count("GetEntityTypes") != 0 {
			t.Errorf("total = %d, GetEntityTypes calls = %d; want 0, 0", total, calls.count("GetEntityTypes"))
		}
	})
	t.Run("version read after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		h := fdtHandlers(newFDTCalls())
		h["GetDetectorVersion"] = func(any) (any, error) { return nil, nx }
		total, _, err := fdtScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		if total != 23 {
			t.Errorf("total = %d; want 23 (the fixture less both detector versions)", total)
		}
	})
}
