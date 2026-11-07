package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/agentregistrycontrol"
	artypes "github.com/aws/aws-sdk-go-v2/service/agentregistrycontrol/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	arDenied    = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: agent-registry:List"}
	arThrottled = &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}
	arHardErr   = &smithy.GenericAPIError{Code: "UnrecognizedFailureException", Message: "boom"}
	arNotFound  = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "gone"}
	arBadInput  = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad arn"}
	arCreated   = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
)

func arRegistryARN(id string) string {
	return "arn:aws:agent-registry:" + testRegion + ":" + testAccountID + ":registry/" + id
}

func arRecordARN(registryID, id string) string {
	return arRegistryARN(registryID) + "/record/" + id
}

func arRegistryIn(arn, id string) artypes.RegistrySummary {
	return artypes.RegistrySummary{
		RegistryId: ptrStr(id), RegistryArn: ptrStr(arn), Name: ptrStr("reg " + id),
		Status: artypes.RegistryStatusReady, Description: ptrStr("catalog " + id),
		CreatedAt: &arCreated, UpdatedAt: &arCreated,
	}
}

func arRegistry(id string) artypes.RegistrySummary { return arRegistryIn(arRegistryARN(id), id) }

func arRecordIn(registryARN, arn, id string) artypes.RegistryRecordSummary {
	return artypes.RegistryRecordSummary{
		RegistryArn: ptrStr(registryARN), RecordArn: ptrStr(arn),
		RecordId: ptrStr(id), Name: ptrStr("rec " + id), RecordType: artypes.RecordTypeMcp,
		RecordVersion: ptrStr("1"), Status: artypes.RegistryRecordStatusApproved,
		CreatedAt: &arCreated, UpdatedAt: &arCreated,
	}
}

func arRecord(registryID, id string) artypes.RegistryRecordSummary {
	return arRecordIn(arRegistryARN(registryID), arRecordARN(registryID, id), id)
}

// arCalls records what the stubbed Agent Registry calls were sent and
// answers ListTagsForResource from tags or errs, keyed by ResourceArn.
type arCalls struct{ *bacCalls }

func newARCalls() *arCalls {
	c := &arCalls{newBACCalls()}
	for _, arn := range []string{arRegistryARN("r1"), arRecordARN("r1", "c1"), arRecordARN("r2", "c3")} {
		c.tags[arn] = map[string]string{"owner": arn}
	}
	return c
}

func (c *arCalls) arListTags(in any) (any, error) {
	arn := sv(in.(*agentregistrycontrol.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &agentregistrycontrol.ListTagsForResourceOutput{Tags: tags}, nil
}

// arRegistryOf is the registry a ListRegistryRecords call was sent.
func arRegistryOf(in any) string {
	return *in.(*agentregistrycontrol.ListRegistryRecordsInput).RegistryId
}

// arHandlers answers the fixture: registry r1 on the first ListRegistries
// page, r2 on the second; r1's records c1 and c2 on two pages, r2's record c3
// on one. r1, c1 and c3 carry tags; r2 and c2 none.
func arHandlers(c *arCalls) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListTagsForResource": c.arListTags,
		"ListRegistries": c.answer("ListRegistries", func(in any) any {
			if in.(*agentregistrycontrol.ListRegistriesInput).NextToken == nil {
				return &agentregistrycontrol.ListRegistriesOutput{Registries: []artypes.RegistrySummary{arRegistry("r1")}, NextToken: ptrStr("p2")}
			}
			return &agentregistrycontrol.ListRegistriesOutput{Registries: []artypes.RegistrySummary{arRegistry("r2")}}
		}),
		"ListRegistryRecords": c.answer("ListRegistryRecords", func(in any) any {
			req := in.(*agentregistrycontrol.ListRegistryRecordsInput)
			switch {
			case *req.RegistryId == "r2":
				return &agentregistrycontrol.ListRegistryRecordsOutput{RegistryRecords: []artypes.RegistryRecordSummary{arRecord("r2", "c3")}}
			case req.NextToken == nil:
				return &agentregistrycontrol.ListRegistryRecordsOutput{RegistryRecords: []artypes.RegistryRecordSummary{arRecord(*req.RegistryId, "c1")}, NextToken: ptrStr("p2")}
			default:
				return &agentregistrycontrol.ListRegistryRecordsOutput{RegistryRecords: []artypes.RegistryRecordSummary{arRecord(*req.RegistryId, "c2")}}
			}
		}),
	}
}

// arRegistriesHandler lists ids on one page.
func arRegistriesHandler(c *arCalls, ids ...string) func(any) (any, error) {
	regs := make([]artypes.RegistrySummary, len(ids))
	for i, id := range ids {
		regs[i] = arRegistry(id)
	}
	return c.answer("ListRegistries", func(any) any { return &agentregistrycontrol.ListRegistriesOutput{Registries: regs} })
}

// arScan runs the scan's real entry point against handlers; every op they do
// not answer is denied.
func arScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, arDenied)
	return scanAgentRegistry(ctx, acct, testRegion, st, testScanID)
}

// Both types store the tags ListTagsForResource returns for their listed
// ARN, nil for a resource without tags, and every stored row's ARN is sent
// to the tag op once. Both pages of each listing are read, every listing
// call asks for the model's page cap of 100, each record is recorded under
// its registry, and the scan reports no error or warning. The rows keep the
// base scan's Name, Status, CreatedAt and verbatim element.
func TestScanAgentRegistry_StoresTagsOfBothTypes(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newARCalls()

	total, inserted, err := arScan(context.Background(), st, arHandlers(calls))
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if total != 5 || inserted != 5 || *warns != 0 {
		t.Errorf("total, inserted, warnings = %d, %d, %d; want 5, 5, 0", total, inserted, *warns)
	}
	want := map[string]struct {
		typ    string
		tagged bool
	}{
		arRegistryARN("r1"):     {TypeAgentRegistryRegistry, true},
		arRegistryARN("r2"):     {TypeAgentRegistryRegistry, false},
		arRecordARN("r1", "c1"): {TypeAgentRegistryRegistryRecord, true},
		arRecordARN("r1", "c2"): {TypeAgentRegistryRegistryRecord, false},
		arRecordARN("r2", "c3"): {TypeAgentRegistryRegistryRecord, true},
	}
	rows := rowsByNativeID(t, st, TypeAgentRegistryRegistry)
	for k, v := range rowsByNativeID(t, st, TypeAgentRegistryRegistryRecord) {
		rows[k] = v
	}
	if len(rows) != len(want) {
		t.Errorf("rows = %d; want %d", len(rows), len(want))
	}
	for arn, w := range want {
		r, ok := rows[arn]
		if !ok {
			t.Errorf("%s not stored", arn)
			continue
		}
		if r.Type != w.typ {
			t.Errorf("%s Type = %q; want %q", arn, r.Type, w.typ)
		}
		if !w.tagged {
			if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
			continue
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"owner": arn})
	}
	got := slices.Clone(calls.tagARNs)
	slices.Sort(got)
	wantARNs := []string{arRegistryARN("r1"), arRecordARN("r1", "c1"), arRecordARN("r1", "c2"), arRegistryARN("r2"), arRecordARN("r2", "c3")}
	slices.Sort(wantARNs)
	if !slices.Equal(got, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantARNs)
	}
	if got := calls.maxResults["ListRegistries"]; !slices.Equal(got, []int32{100, 100}) {
		t.Errorf("ListRegistries MaxResults = %v; want [100 100]", got)
	}
	if got := calls.maxResults["ListRegistryRecords"]; !slices.Equal(got, []int32{100, 100, 100}) {
		t.Errorf("ListRegistryRecords MaxResults = %v; want [100 100 100]", got)
	}
	reg := rows[arRegistryARN("r2")]
	if sv(reg.Region) != testRegion || sv(reg.Name) != "reg r2" || sv(reg.Status) != "READY" || sv(reg.CreatedAt) != "2026-09-01T08:00:00Z" {
		t.Errorf("registry row = region %q name %q status %q created %q", sv(reg.Region), sv(reg.Name), sv(reg.Status), sv(reg.CreatedAt))
	}
	var regAttrs struct {
		RegistryID  string `json:"RegistryId"`
		Description string
	}
	if err := json.Unmarshal([]byte(reg.AttributesJSON), &regAttrs); err != nil || regAttrs.RegistryID != "r2" || regAttrs.Description != "catalog r2" {
		t.Errorf("registry attrs = %+v (%v); want RegistryId r2, Description catalog r2", regAttrs, err)
	}
	rec := rows[arRecordARN("r2", "c3")]
	if sv(rec.Region) != testRegion || sv(rec.Name) != "rec c3" || sv(rec.Status) != "APPROVED" || sv(rec.CreatedAt) != "2026-09-01T08:00:00Z" {
		t.Errorf("record row = region %q name %q status %q created %q", sv(rec.Region), sv(rec.Name), sv(rec.Status), sv(rec.CreatedAt))
	}
	var recAttrs struct{ RecordType, RecordVersion string }
	if err := json.Unmarshal([]byte(rec.AttributesJSON), &recAttrs); err != nil || recAttrs.RecordType != "MCP" || recAttrs.RecordVersion != "1" {
		t.Errorf("record attrs = %+v (%v); want RecordType MCP, RecordVersion 1", recAttrs, err)
	}
	for _, c := range []struct{ reg, id string }{{"r1", "c1"}, {"r1", "c2"}, {"r2", "c3"}} {
		assertContainsNative(t, st, arRegistryARN(c.reg), arRecordARN(c.reg, c.id))
	}
	assertContainsEdgesDeclared(t, st)
}

// A GovCloud registry's and record's listed ARNs reach the tag op unchanged.
func TestScanAgentRegistry_GovCloudARNsReachTagOp(t *testing.T) {
	t.Parallel()
	const govRegion = "us-gov-west-1"
	regARN := arnFor("aws-us-gov", "agent-registry", govRegion, testAccountID, "registry/rgov")
	recARN := regARN + "/record/cgov"
	st := newTestStore(t)
	calls := newARCalls()
	calls.tags[regARN] = map[string]string{"env": "gov"}
	calls.tags[recARN] = map[string]string{"env": "gov-record"}
	h := arHandlers(calls)
	h["ListRegistries"] = calls.answer("ListRegistries", func(any) any {
		return &agentregistrycontrol.ListRegistriesOutput{Registries: []artypes.RegistrySummary{arRegistryIn(regARN, "rgov")}}
	})
	h["ListRegistryRecords"] = calls.answer("ListRegistryRecords", func(any) any {
		return &agentregistrycontrol.ListRegistryRecordsOutput{RegistryRecords: []artypes.RegistryRecordSummary{arRecordIn(regARN, recARN, "cgov")}}
	})
	acct := partitionAccount("aws-us-gov")
	acct.cfg = opStubConfig(h, arDenied)

	total, _, err := scanAgentRegistry(context.Background(), acct, govRegion, st, testScanID)
	if err != nil || total != 2 {
		t.Fatalf("scan = %d, %v; want 2, nil", total, err)
	}
	got := slices.Clone(calls.tagARNs)
	slices.Sort(got)
	if want := []string{regARN, recARN}; !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
	}
	for _, c := range []struct{ typ, arn, env string }{
		{TypeAgentRegistryRegistry, regARN, "gov"}, {TypeAgentRegistryRegistryRecord, recARN, "gov-record"},
	} {
		r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
		if !ok {
			t.Fatalf("%s %s not stored", c.typ, c.arn)
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"env": c.env})
		if sv(r.Region) != govRegion {
			t.Errorf("%s Region = %v; want %s", c.arn, r.Region, govRegion)
		}
	}
}

// No registries store nothing and make no record listing or tag read; a
// registry without records stores its own row only.
func TestScanAgentRegistry_EmptyListings(t *testing.T) {
	t.Parallel()
	t.Run("no registries", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		calls := newARCalls()
		h := arHandlers(calls)
		h["ListRegistries"] = arRegistriesHandler(calls)
		total, _, err := arScan(context.Background(), st, h)
		if err != nil || total != 0 || *warns != 0 {
			t.Errorf("total, err, warnings = %d, %v, %d; want 0, nil, 0", total, err, *warns)
		}
		if n, tags := calls.count("ListRegistryRecords"), len(calls.tagARNs); n != 0 || tags != 0 {
			t.Errorf("ListRegistryRecords calls, tag reads = %d, %d; want 0, 0", n, tags)
		}
	})
	t.Run("registry without records", func(t *testing.T) {
		st := newTestStore(t)
		calls := newARCalls()
		h := arHandlers(calls)
		h["ListRegistries"] = arRegistriesHandler(calls, "r1")
		h["ListRegistryRecords"] = calls.answer("ListRegistryRecords", func(any) any { return &agentregistrycontrol.ListRegistryRecordsOutput{} })
		total, _, err := arScan(context.Background(), st, h)
		if err != nil || total != 1 {
			t.Errorf("total, err = %d, %v; want 1, nil", total, err)
		}
		if n := len(rowsByNativeID(t, st, TypeAgentRegistryRegistryRecord)); n != 0 {
			t.Errorf("record rows = %d; want 0", n)
		}
	})
}

// A rescan reports every stored row in its total, not only the newly inserted
// ones. The dispatcher reports total for the service, and a first scan into an
// empty store has total == inserted, so only a rescan catches the two swapped.
func TestScanAgentRegistry_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, _, err := arScan(context.Background(), st, arHandlers(newARCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, _, err := arScan(context.Background(), st, arHandlers(newARCalls()))
	if err != nil || total != 5 {
		t.Errorf("rescan = %d, %v; want 5, nil", total, err)
	}
}

// An element without an ARN is not stored and is warned about under its
// listing's op. A registry listed without a RegistryId is stored, its
// records are not listed, and that is warned about once.
func TestScanAgentRegistry_UnstorableItemsWarn(t *testing.T) {
	t.Parallel()
	noID := arRegistry("r3")
	noID.RegistryId = nil
	for _, c := range []struct {
		name, op         string
		registries       []artypes.RegistrySummary
		records          []artypes.RegistryRecordSummary
		total, listCalls int
	}{
		{"registry without ARN", agentRegistryListRegistriesOp, []artypes.RegistrySummary{arRegistry("r1"), {Name: ptrStr("no arn")}}, nil, 2, 1},
		{"registry without id", agentRegistryListRegistriesOp, []artypes.RegistrySummary{arRegistry("r1"), noID}, nil, 3, 1},
		{"record without ARN", agentRegistryListRecordsOp, []artypes.RegistrySummary{arRegistry("r1")}, []artypes.RegistryRecordSummary{{Name: ptrStr("no arn")}, {}}, 2, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, c.op)
			calls := newARCalls()
			h := arHandlers(calls)
			h["ListRegistries"] = calls.answer("ListRegistries", func(any) any { return &agentregistrycontrol.ListRegistriesOutput{Registries: c.registries} })
			h["ListRegistryRecords"] = calls.answer("ListRegistryRecords", func(in any) any {
				return &agentregistrycontrol.ListRegistryRecordsOutput{RegistryRecords: append([]artypes.RegistryRecordSummary{arRecord(arRegistryOf(in), "c1")}, c.records...)}
			})

			total, _, err := arScan(context.Background(), st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != c.total || *warns != 1 || *opWarns != 1 {
				t.Errorf("total, warnings, %s warnings = %d, %d, %d; want %d, 1, 1", c.op, total, *warns, *opWarns, c.total)
			}
			if n := calls.count("ListRegistryRecords"); n != c.listCalls {
				t.Errorf("ListRegistryRecords calls = %d; want %d", n, c.listCalls)
			}
		})
	}
}

// A failed tag read of either type keeps the row without tags, except a
// modeled not-found, which drops it: a dropped registry has no records
// listed. A denial warns once under the tag op; a transient error after the
// retries and any other error are returned.
func TestScanAgentRegistry_TagReadFailures(t *testing.T) {
	t.Parallel()
	for _, target := range []struct {
		typ, arn string
		lost     int // rows a not-found costs: the row and, for r1, its records
	}{
		{TypeAgentRegistryRegistry, arRegistryARN("r1"), 3},
		{TypeAgentRegistryRegistryRecord, arRecordARN("r1", "c1"), 1},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				kept      bool
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", arDenied, true, 0, 1},
				{"not found", arNotFound, false, 0, 0},
				{"other error", arBadInput, true, outcomeError, 0},
				{"transient", arThrottled, true, outcomeTransient, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warns := countWarnings(st)
					warnings := bacWarnings(st, agentRegistryTagsOp)
					calls := newARCalls()
					calls.errs[target.arn] = c.err

					total, _, err := arScan(context.Background(), st, arHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.outcome == outcomeError {
						assertAPICode(t, err, "ValidationException")
					}
					r, ok := rowsByNativeID(t, st, target.typ)[target.arn]
					if ok != c.kept {
						t.Fatalf("row stored = %v; want %v", ok, c.kept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					want := 5
					if !c.kept {
						want -= target.lost
					}
					if total != want {
						t.Errorf("total = %d; want %d", total, want)
					}
					if *warns != c.wantWarns || len(*warnings) != c.wantWarns {
						t.Fatalf("warnings = %d (%s: %v); want %d", *warns, agentRegistryTagsOp, *warnings, c.wantWarns)
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

// Denied tag reads in both phases are one warning for the scan.
func TestScanAgentRegistry_TagDenialsWarnOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countWarnings(st)
	tagWarns := countOpWarnings(st, agentRegistryTagsOp)
	calls := newARCalls()
	calls.errs[arRegistryARN("r2")] = arDenied
	calls.errs[arRecordARN("r1", "c1")] = arDenied

	if _, _, err := arScan(context.Background(), st, arHandlers(calls)); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if *warns != 1 || *tagWarns != 1 {
		t.Errorf("warnings = %d (%s: %d); want 1 under the tag op", *warns, agentRegistryTagsOp, *tagWarns)
	}
	for _, r := range []struct{ typ, arn string }{
		{TypeAgentRegistryRegistry, arRegistryARN("r2")}, {TypeAgentRegistryRegistryRecord, arRecordARN("r1", "c1")},
	} {
		if got, ok := rowsByNativeID(t, st, r.typ)[r.arn]; !ok || got.TagsJSON != nil {
			t.Errorf("%s stored %v, TagsJSON %v; want it kept without tags", r.arn, ok, got.TagsJSON)
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanAgentRegistry_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	tagWarns := countOpWarnings(st, agentRegistryTagsOp)
	calls := newARCalls()
	calls.errs[arRegistryARN("r1")] = arThrottled
	calls.errs[arRecordARN("r2", "c3")] = arBadInput

	total, _, err := arScan(context.Background(), st, arHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *tagWarns != 1 || total != 5 {
		t.Errorf("%s warnings, total = %d, %d; want 1, 5", agentRegistryTagsOp, *tagWarns, total)
	}
}

// One registry's record listing failing with a code costs the other
// registries none of their records. A registry deleted since it was listed
// is skipped silently; ConflictException and ValidationException (a registry
// not READY) and a denial each warn once for the op, the two codes apart; a
// transient error after the retries and any other code are returned.
func TestScanAgentRegistry_RecordListingFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		err       error
		r4Err     error // r4's error when it differs from r2's
		outcome   serviceOutcome
		wantWarns int
	}{
		{"deleted after listing is silent", arNotFound, nil, 0, 0},
		{"conflict warns once", &smithy.GenericAPIError{Code: "ConflictException", Message: "not ready"}, nil, 0, 1},
		{"validation warns once", arBadInput, nil, 0, 1},
		{"conflict and validation warn apart", &smithy.GenericAPIError{Code: "ConflictException", Message: "not ready"}, arBadInput, 0, 2},
		{"denied warns once", arDenied, nil, 0, 1},
		{"transient is returned", arThrottled, nil, outcomeTransient, 0},
		{"other error is returned", arHardErr, nil, outcomeError, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, agentRegistryListRecordsOp)
			calls := newARCalls()
			h := arHandlers(calls)
			h["ListRegistries"] = arRegistriesHandler(calls, "r1", "r2", "r3", "r4")
			h["ListRegistryRecords"] = func(in any) (any, error) {
				id := arRegistryOf(in)
				switch {
				case id == "r4" && c.r4Err != nil:
					return nil, c.r4Err
				case id == "r2" || id == "r4":
					return nil, c.err
				}
				return &agentregistrycontrol.ListRegistryRecordsOutput{RegistryRecords: []artypes.RegistryRecordSummary{arRecord(id, "c-"+id)}}, nil
			}

			total, _, err := arScan(context.Background(), st, h)
			if c.outcome == 0 && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.outcome != 0 {
				assertGlueOutcome(t, err, c.outcome)
				if !errors.Is(err, c.err) {
					t.Errorf("err = %v; want %v", err, c.err)
				}
			}
			if *warns != c.wantWarns || *opWarns != c.wantWarns {
				t.Errorf("warnings = %d (%s: %d); want %d", *warns, agentRegistryListRecordsOp, *opWarns, c.wantWarns)
			}
			rows := rowsByNativeID(t, st, TypeAgentRegistryRegistryRecord)
			if total != 6 || len(rows) != 2 {
				t.Errorf("total, record rows = %d, %d; want 6 (4 registries, 2 records), 2", total, len(rows))
			}
			for _, id := range []string{"r1", "r3"} {
				if _, ok := rows[arRecordARN(id, "c-"+id)]; !ok {
					t.Errorf("record of %s missing", id)
				}
			}
		})
	}
}

// A failed page keeps the pages read before it: page 2 of the registries
// keeps r1 and its records; page 2 of r1's records keeps c1. A registry
// deleted while its records were read keeps the page read and warns once.
// ConflictException or ValidationException on a later page is returned, not
// warned about as a registry skipped: r1 served its first page.
func TestScanAgentRegistry_LaterPageFailuresKeepPages(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, op  string
		err       error
		outcome   serviceOutcome
		wantWarns int
		total     int
		kept      []string
	}{
		{"registries page 2", "ListRegistries", arHardErr, outcomeError, 0, 3, []string{arRegistryARN("r1"), arRecordARN("r1", "c1"), arRecordARN("r1", "c2")}},
		{"records page 2", "ListRegistryRecords", arHardErr, outcomeError, 0, 4, []string{arRecordARN("r1", "c1"), arRecordARN("r2", "c3")}},
		{"registry deleted mid-listing", "ListRegistryRecords", arNotFound, 0, 1, 4, []string{arRecordARN("r1", "c1"), arRecordARN("r2", "c3")}},
		{"records page 2 conflict", "ListRegistryRecords", &smithy.GenericAPIError{Code: "ConflictException", Message: "deleting"}, outcomeError, 0, 4, []string{arRecordARN("r1", "c1"), arRecordARN("r2", "c3")}},
		{"records page 2 validation", "ListRegistryRecords", &smithy.GenericAPIError{Code: "ValidationException", Message: "Invalid pagination token"}, outcomeError, 0, 4, []string{arRecordARN("r1", "c1"), arRecordARN("r2", "c3")}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			calls := newARCalls()
			h := arHandlers(calls)
			next := h[c.op]
			h[c.op] = func(in any) (any, error) {
				if sv(bacField[*string](in, "NextToken")) == "p2" {
					return nil, c.err
				}
				return next(in)
			}

			total, _, err := arScan(context.Background(), st, h)
			if c.outcome == 0 && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.outcome != 0 {
				assertGlueOutcome(t, err, c.outcome)
				if !errors.Is(err, c.err) {
					t.Errorf("err = %v; want %v", err, c.err)
				}
			}
			if total != c.total || *warns != c.wantWarns {
				t.Errorf("total, warnings = %d, %d; want %d, %d", total, *warns, c.total, c.wantWarns)
			}
			rows := rowsByNativeID(t, st, TypeAgentRegistryRegistry)
			for k, v := range rowsByNativeID(t, st, TypeAgentRegistryRegistryRecord) {
				rows[k] = v
			}
			for _, arn := range c.kept {
				if _, ok := rows[arn]; !ok {
					t.Errorf("%s from the pages read missing", arn)
				}
			}
		})
	}
}

// A repeated token ends either listing as an error, keeping its pages.
func TestScanAgentRegistry_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		op, typ, arn string
		out          func(in any) any
	}{
		{"ListRegistries", TypeAgentRegistryRegistry, arRegistryARN("r1"), func(any) any {
			return &agentregistrycontrol.ListRegistriesOutput{Registries: []artypes.RegistrySummary{arRegistry("r1")}, NextToken: ptrStr("same")}
		}},
		{"ListRegistryRecords", TypeAgentRegistryRegistryRecord, arRecordARN("r1", "c1"), func(in any) any {
			return &agentregistrycontrol.ListRegistryRecordsOutput{RegistryRecords: []artypes.RegistryRecordSummary{arRecord(arRegistryOf(in), "c1")}, NextToken: ptrStr("same")}
		}},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newARCalls()
			h := arHandlers(calls)
			h[c.op] = calls.answer(c.op, c.out)

			_, _, err := arScan(context.Background(), st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if _, ok := rowsByNativeID(t, st, c.typ)[c.arn]; !ok {
				t.Errorf("%s from the pages read missing", c.arn)
			}
		})
	}
}

// A failed registry write stops the scan before the records are listed and
// sends no warning about the rows it lost.
func TestScanAgentRegistry_FailedRegistryWriteStopsWithoutWarning(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeAgentRegistryRegistry)
	calls := newARCalls()
	calls.errs[arRegistryARN("r1")] = arDenied

	_, _, err := arScan(context.Background(), st, arHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 || calls.count("ListRegistryRecords") != 0 {
		t.Errorf("warnings, ListRegistryRecords calls = %d, %d; want 0, 0", *warns, calls.count("ListRegistryRecords"))
	}
}

// A failed record write sends no warning: neither the denied tag read nor
// the registry skipped with ConflictException.
func TestScanAgentRegistry_FailedRecordWriteSendsNoWarning(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeAgentRegistryRegistryRecord)
	calls := newARCalls()
	calls.errs[arRecordARN("r1", "c1")] = arDenied
	h := arHandlers(calls)
	records := h["ListRegistryRecords"]
	h["ListRegistryRecords"] = func(in any) (any, error) {
		if arRegistryOf(in) == "r2" {
			return nil, &smithy.GenericAPIError{Code: "ConflictException", Message: "not ready"}
		}
		return records(in)
	}

	_, _, err := arScan(context.Background(), st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
}

// A record write refused on r1's second page keeps the contains edge of c1,
// written on its first page before the failure.
func TestScanAgentRegistry_LaterRecordWriteFailureKeepsEdges(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, err := st.DB().Exec(`CREATE TRIGGER refuse_row BEFORE INSERT ON resources
		WHEN NEW.native_id = '` + arRecordARN("r1", "c2") + `'
		BEGIN SELECT RAISE(ABORT, 'test: write refused'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	_, _, err := arScan(context.Background(), st, arHandlers(newARCalls()))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	assertContainsNative(t, st, arRegistryARN("r1"), arRecordARN("r1", "c1"))
}

// A failed closure write of the records is returned and sends no warning,
// not even about r2 skipped with ConflictException; the rows written stay.
func TestScanAgentRegistry_FailedClosureWriteSendsNoWarning(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseClosureWrites(t, st)
	calls := newARCalls()
	h := arHandlers(calls)
	records := h["ListRegistryRecords"]
	h["ListRegistryRecords"] = func(in any) (any, error) {
		if arRegistryOf(in) == "r2" {
			return nil, &smithy.GenericAPIError{Code: "ConflictException", Message: "not ready"}
		}
		return records(in)
	}

	_, _, err := arScan(context.Background(), st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	if n := countStoredRows(t, st); n != 4 {
		t.Errorf("stored rows = %d; want 4 (r1, r2, c1, c2)", n)
	}
}

// A first ListRegistries page that is denied warns once under the op and
// stores nothing; any other error is returned. Either way no record listing
// or tag read is made.
func TestScanAgentRegistry_RegistriesFirstPageFails(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		err       error
		outcome   serviceOutcome
		wantWarns int
	}{
		{"denied warns", arDenied, 0, 1},
		{"other error is returned", arHardErr, outcomeError, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			warnings := bacWarnings(st, agentRegistryListRegistriesOp)
			calls := newARCalls()
			h := arHandlers(calls)
			h["ListRegistries"] = func(any) (any, error) { return nil, c.err }

			total, _, err := arScan(context.Background(), st, h)
			if c.outcome == 0 && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.outcome != 0 {
				assertGlueOutcome(t, err, c.outcome)
				if !errors.Is(err, c.err) {
					t.Errorf("err = %v; want %v", err, c.err)
				}
			}
			if *warns != c.wantWarns || len(*warnings) != c.wantWarns {
				t.Errorf("warnings = %d (%s: %d); want %d", *warns, agentRegistryListRegistriesOp, len(*warnings), c.wantWarns)
			}
			for _, w := range *warnings {
				if w.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
				}
			}
			if n, rows := calls.count("ListRegistryRecords"), countStoredRows(t, st); total != 0 || rows != 0 || n != 0 || len(calls.tagARNs) != 0 {
				t.Errorf("total, rows, ListRegistryRecords calls, tag reads = %d, %d, %d, %d; want 0, 0, 0, 0", total, rows, n, len(calls.tagARNs))
			}
		})
	}
}

// A throttled registries page is demoted to a warning so that the records
// phase's hard error still classifies the service as failed.
func TestScanAgentRegistry_TransientDoesNotMaskHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warned := countOpWarnings(st, agentRegistryListRegistriesOp)
	calls := newARCalls()
	h := arHandlers(calls)
	h["ListRegistries"] = func(in any) (any, error) {
		if in.(*agentregistrycontrol.ListRegistriesInput).NextToken == nil {
			return &agentregistrycontrol.ListRegistriesOutput{Registries: []artypes.RegistrySummary{arRegistry("r1")}, NextToken: ptrStr("p2")}, nil
		}
		return nil, arThrottled
	}
	h["ListRegistryRecords"] = func(any) (any, error) { return nil, arHardErr }

	total, _, err := arScan(context.Background(), st, h)
	assertGlueOutcome(t, err, outcomeError)
	if !errors.Is(err, arHardErr) {
		t.Errorf("err = %v, want the ListRegistryRecords error", err)
	}
	if *warned != 1 || total != 1 {
		t.Errorf("ListRegistries transient warnings, total = %d, %d; want 1, 1 (the registry stored before the throttle)", *warned, total)
	}
}

// In a region with no agent-registry endpoint the NXDOMAIN reaches the
// dispatcher unchanged, which reports the region unavailable, and no record
// listing is made. Once registries were stored, a records NXDOMAIN is a
// failure of this scan, and the registries keep their counts.
func TestScanAgentRegistry_Unreachable(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "agent-registry-control.us-east-1.api.aws", IsNotFound: true}
	t.Run("endpoint absent", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		calls := newARCalls()
		h := arHandlers(calls)
		h["ListRegistries"] = func(any) (any, error) { return nil, nx }
		total, _, err := arScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 || *warns != 0 || calls.count("ListRegistryRecords") != 0 {
			t.Errorf("total, warnings, ListRegistryRecords calls = %d, %d, %d; want 0, 0, 0", total, *warns, calls.count("ListRegistryRecords"))
		}
	})
	t.Run("endpoint gone after the registries", func(t *testing.T) {
		st := newTestStore(t)
		h := arHandlers(newARCalls())
		h["ListRegistryRecords"] = func(any) (any, error) { return nil, nx }
		total, _, err := arScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		if total != 2 {
			t.Errorf("total = %d; want 2 (the registries)", total)
		}
	})
}

// Once ctx ends during the registries listing, the records phase does not
// run, and the stop is reported once.
func TestScanAgentRegistry_ContextEndStopsOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newARCalls()
	h := arHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registries := h["ListRegistries"]
	h["ListRegistries"] = func(in any) (any, error) {
		if in.(*agentregistrycontrol.ListRegistriesInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return registries(in)
	}

	_, _, err := arScan(ctx, st, h)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 || calls.count("ListRegistryRecords") != 0 {
		t.Errorf("members carrying the stop, ListRegistryRecords calls = %d, %d; want 1, 0 (err %v)", stops, calls.count("ListRegistryRecords"), err)
	}
}
