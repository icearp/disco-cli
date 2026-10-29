package aws

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iottwinmaker"
	itmtypes "github.com/aws/aws-sdk-go-v2/service/iottwinmaker/types"
	"github.com/icearp/disco-cli/store"
)

// The fixture: ListWorkspaces serves ws-t on its first page and ws-b on its
// second. Each workspace ws-<n> holds one element of every child kind, its id
// ending in -<n>; only ws-t's sync job has a SyncSource. ListTagsForResource
// answers with tags for every ARN ending in "-t".

var itmCreated = time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)

func itmTestARN(res string) string {
	return arnFor("aws", "iottwinmaker", testRegion, testAccountID, res)
}

// itmSuffix is the suffix every element of workspace ws carries.
func itmSuffix(ws string) string { return strings.TrimPrefix(ws, "ws-") }

func itmWorkspace(n string) itmtypes.WorkspaceSummary {
	return itmtypes.WorkspaceSummary{
		Arn: sdkaws.String(itmTestARN("workspace/ws-" + n)), WorkspaceId: sdkaws.String("ws-" + n),
		CreationDateTime: &itmCreated, UpdateDateTime: &itmCreated, Description: sdkaws.String("workspace " + n),
	}
}

func itmComponentType(ws string) itmtypes.ComponentTypeSummary {
	id := "ct-" + itmSuffix(ws)
	return itmtypes.ComponentTypeSummary{
		Arn: sdkaws.String(itmTestARN("workspace/" + ws + "/component-type/" + id)), ComponentTypeId: &id,
		ComponentTypeName: sdkaws.String("component type of " + ws), CreationDateTime: &itmCreated, UpdateDateTime: &itmCreated,
		Status: &itmtypes.Status{State: itmtypes.StateActive},
	}
}

func itmEntity(ws string) itmtypes.EntitySummary {
	id := "en-" + itmSuffix(ws)
	return itmtypes.EntitySummary{
		Arn: sdkaws.String(itmTestARN("workspace/" + ws + "/entity/" + id)), EntityId: &id,
		EntityName: sdkaws.String("entity of " + ws), CreationDateTime: &itmCreated, UpdateDateTime: &itmCreated,
		Status: &itmtypes.Status{State: itmtypes.StateActive},
	}
}

func itmScene(ws string) itmtypes.SceneSummary {
	id := "sc-" + itmSuffix(ws)
	return itmtypes.SceneSummary{
		Arn: sdkaws.String(itmTestARN("workspace/" + ws + "/scene/" + id)), SceneId: &id,
		ContentLocation: sdkaws.String("s3://bucket/" + id + ".json"), CreationDateTime: &itmCreated, UpdateDateTime: &itmCreated,
	}
}

func itmSyncJob(ws string) itmtypes.SyncJobSummary {
	j := itmtypes.SyncJobSummary{
		Arn:         sdkaws.String(itmTestARN("workspace/" + ws + "/sync-job/sj-" + itmSuffix(ws))),
		WorkspaceId: &ws, CreationDateTime: &itmCreated,
		Status: &itmtypes.SyncJobStatus{State: itmtypes.SyncJobStateActive},
	}
	if ws == "ws-t" {
		j.SyncSource = sdkaws.String("SITEWISE")
	}
	return j
}

// itmWant is a row the fixture stores: its type, ARN, Name and the element
// its attributes are.
type itmWant struct {
	typ, arn, name string
	element        any
}

// itmRows is every row the fixture stores.
func itmRows() []itmWant {
	var rows []itmWant
	// A sync job without a SyncSource is named "", as in the base scan.
	for _, f := range []struct{ n, jobName string }{{"t", "SITEWISE"}, {"b", ""}} {
		w := itmWorkspace(f.n)
		ws := sv(w.WorkspaceId)
		c, e, s, j := itmComponentType(ws), itmEntity(ws), itmScene(ws), itmSyncJob(ws)
		rows = append(rows,
			itmWant{TypeIoTTwinMakerWorkspace, sv(w.Arn), ws, w},
			itmWant{TypeIoTTwinMakerComponentType, sv(c.Arn), sv(c.ComponentTypeId), c},
			itmWant{TypeIoTTwinMakerEntity, sv(e.Arn), sv(e.EntityName), e},
			itmWant{TypeIoTTwinMakerScene, sv(s.Arn), sv(s.SceneId), s},
			itmWant{TypeIoTTwinMakerSyncJob, sv(j.Arn), f.jobName, j},
		)
	}
	return rows
}

// itmCalls records the stubbed calls and answers ListTagsForResource from
// tags and errs, keyed by ARN.
type itmCalls struct{ *crtCalls }

func newITMCalls() *itmCalls {
	c := &itmCalls{&crtCalls{newBACCalls()}}
	for _, r := range itmRows() {
		if strings.HasSuffix(r.arn, "-t") {
			c.tags[r.arn] = map[string]string{"type": r.typ}
		}
	}
	return c
}

func (c *itmCalls) listTags(in any) (any, error) {
	c.called("ListTagsForResource", in)
	arn := sv(in.(*iottwinmaker.ListTagsForResourceInput).ResourceARN)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &iottwinmaker.ListTagsForResourceOutput{Tags: tags}, nil
}

// itmWorkspaceOf is the workspace a child listing was sent.
func itmWorkspaceOf(in any) string { return sv(bacField[*string](in, "WorkspaceId")) }

// itmChildOps is every per-workspace listing, in call order.
var itmChildOps = []string{"ListComponentTypes", "ListEntities", "ListScenes", "ListSyncJobs"}

// itmChildOut is op's one page for workspace ws, holding items.
func itmChildOut(op, ws string, next *string) any {
	switch op {
	case "ListComponentTypes":
		return &iottwinmaker.ListComponentTypesOutput{ComponentTypeSummaries: []itmtypes.ComponentTypeSummary{itmComponentType(ws)}, NextToken: next}
	case "ListEntities":
		return &iottwinmaker.ListEntitiesOutput{EntitySummaries: []itmtypes.EntitySummary{itmEntity(ws)}, NextToken: next}
	case "ListScenes":
		return &iottwinmaker.ListScenesOutput{SceneSummaries: []itmtypes.SceneSummary{itmScene(ws)}, NextToken: next}
	default:
		return &iottwinmaker.ListSyncJobsOutput{SyncJobSummaries: []itmtypes.SyncJobSummary{itmSyncJob(ws)}, NextToken: next}
	}
}

// itmEmptyOut is op's empty page.
func itmEmptyOut(op string) any {
	switch op {
	case "ListComponentTypes":
		return &iottwinmaker.ListComponentTypesOutput{}
	case "ListEntities":
		return &iottwinmaker.ListEntitiesOutput{}
	case "ListScenes":
		return &iottwinmaker.ListScenesOutput{}
	default:
		return &iottwinmaker.ListSyncJobsOutput{}
	}
}

// itmHandlers answers every op the scan calls with the fixture (itmRows).
func itmHandlers(c *itmCalls) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListWorkspaces": c.twoPages("ListWorkspaces", func(n string, next *string) any {
			return &iottwinmaker.ListWorkspacesOutput{WorkspaceSummaries: []itmtypes.WorkspaceSummary{itmWorkspace(n)}, NextToken: next}
		}),
	}
	for _, op := range itmChildOps {
		h[op] = c.answer(op, func(in any) any { return itmChildOut(op, itmWorkspaceOf(in), nil) })
	}
	return h
}

// itmScan runs the scan's real entry point against handlers; every op they do
// not answer is denied.
func itmScan(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, crDenied)
	return scanIoTTwinMaker(ctx, acct, testRegion, st, testScanID)
}

func itmRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// Every type stores the tags ListTagsForResource returns for its ARN, nil for
// a resource without tags, and every stored row's ARN is sent to the tag op.
// Both workspace pages are read, each child listing is sent each workspace,
// every call asks for the model's cap of 200, and the scan reports no error
// or warning. Each row keeps the base scan's Name, Region and verbatim
// element, and no CreatedAt or Status.
func TestScanIoTTwinMaker_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newITMCalls()
	h := itmHandlers(calls)
	sentWorkspaces := map[string][]string{}
	for _, op := range itmChildOps {
		list := h[op]
		h[op] = func(in any) (any, error) {
			calls.mu.Lock()
			sentWorkspaces[op] = append(sentWorkspaces[op], itmWorkspaceOf(in))
			calls.mu.Unlock()
			return list(in)
		}
	}

	total, inserted, err := itmScan(context.Background(), t, st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := itmRows()
	if total != 10 || inserted != 10 || len(rows) != 10 {
		t.Errorf("total, inserted = %d, %d; want 10 (fixture rows %d)", total, inserted, len(rows))
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantTagARNs []string
	for _, want := range rows {
		r, ok := itmRowOf(t, st, want.typ, want.arn)
		if !ok {
			t.Errorf("%s %s not stored", want.typ, want.arn)
			continue
		}
		wantTagARNs = append(wantTagARNs, want.arn)
		if strings.HasSuffix(want.arn, "-t") {
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": want.typ})
		} else if r.TagsJSON != nil {
			t.Errorf("%s %s TagsJSON = %s; want nil", want.typ, want.arn, *r.TagsJSON)
		}
		if r.Name == nil || *r.Name != want.name {
			t.Errorf("%s %s Name = %v; want %q", want.typ, want.arn, r.Name, want.name)
		}
		if r.Status != nil || r.CreatedAt != nil {
			t.Errorf("%s %s Status, CreatedAt = %v, %v; want nil, nil", want.typ, want.arn, r.Status, r.CreatedAt)
		}
		if sv(r.Region) != testRegion || r.AttributesJSON != mustJSON(want.element) {
			t.Errorf("%s %s Region, attributes = %v, %s; want %s, %s", want.typ, want.arn, r.Region, r.AttributesJSON, testRegion, mustJSON(want.element))
		}
	}
	got := slices.Clone(calls.tagARNs)
	slices.Sort(got)
	slices.Sort(wantTagARNs)
	if !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantTagARNs)
	}
	for _, op := range itmChildOps {
		sent := sentWorkspaces[op]
		slices.Sort(sent)
		if want := []string{"ws-b", "ws-t"}; !slices.Equal(sent, want) {
			t.Errorf("%s workspaces = %v; want %v", op, sent, want)
		}
	}
	for op, want := range map[string]int{"ListWorkspaces": 2, "ListComponentTypes": 2, "ListEntities": 2, "ListScenes": 2, "ListSyncJobs": 2, "ListTagsForResource": 10} {
		caps := calls.maxResults[op]
		if len(caps) != want || slices.ContainsFunc(caps, func(n int32) bool { return n != 200 }) {
			t.Errorf("%s MaxResults = %v; want %d calls, each 200", op, caps, want)
		}
	}
}

// An aws-us-gov account scanned in us-gov-west-1 sends the gov ARNs its
// listings return to the tag op unchanged and stores their tags.
func TestScanIoTTwinMaker_GovCloudARNsReachTagOp(t *testing.T) {
	const govRegion = "us-gov-west-1"
	wsARN := arnFor("aws-us-gov", "iottwinmaker", govRegion, testAccountID, "workspace/ws-gov")
	entityARN := wsARN + "/entity/en-gov"
	st := newTestStore(t)
	calls := newITMCalls()
	calls.tags[wsARN] = map[string]string{"env": "gov"}
	calls.tags[entityARN] = map[string]string{"env": "gov-entity"}
	h := itmHandlers(calls)
	h["ListWorkspaces"] = calls.answer("ListWorkspaces", func(any) any {
		return &iottwinmaker.ListWorkspacesOutput{WorkspaceSummaries: []itmtypes.WorkspaceSummary{{Arn: &wsARN, WorkspaceId: sdkaws.String("ws-gov")}}}
	})
	for _, op := range itmChildOps {
		h[op] = calls.answer(op, func(any) any { return itmEmptyOut(op) })
	}
	h["ListEntities"] = calls.answer("ListEntities", func(any) any {
		return &iottwinmaker.ListEntitiesOutput{EntitySummaries: []itmtypes.EntitySummary{{Arn: &entityARN, EntityId: sdkaws.String("en-gov"), EntityName: sdkaws.String("gov entity")}}}
	})
	acct := partitionAccount("aws-us-gov")
	acct.cfg = opStubConfig(h, crDenied)

	total, _, err := scanIoTTwinMaker(context.Background(), acct, govRegion, st, testScanID)
	if err != nil || total != 2 {
		t.Fatalf("scan = %d, %v; want 2, nil", total, err)
	}
	got := slices.Clone(calls.tagARNs)
	slices.Sort(got)
	if want := []string{wsARN, entityARN}; !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
	}
	for _, c := range []struct{ typ, arn, env string }{
		{TypeIoTTwinMakerWorkspace, wsARN, "gov"},
		{TypeIoTTwinMakerEntity, entityARN, "gov-entity"},
	} {
		r, ok := itmRowOf(t, st, c.typ, c.arn)
		if !ok {
			t.Fatalf("%s %s not stored", c.typ, c.arn)
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"env": c.env})
		if sv(r.Region) != govRegion {
			t.Errorf("%s Region = %v; want %s", c.arn, r.Region, govRegion)
		}
	}
}

// No workspace stores nothing, makes no child listing, reads no tags and
// reports nothing.
func TestScanIoTTwinMaker_NoWorkspaces(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newITMCalls()
	h := itmHandlers(calls)
	h["ListWorkspaces"] = calls.answer("ListWorkspaces", func(any) any { return &iottwinmaker.ListWorkspacesOutput{} })

	total, inserted, err := itmScan(context.Background(), t, st, h)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if *warns != 0 || len(calls.tagARNs) != 0 {
		t.Errorf("warnings, tag reads = %d, %d; want 0, 0", *warns, len(calls.tagARNs))
	}
	for _, op := range itmChildOps {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s calls = %d; want 0", op, n)
		}
	}
}

// A workspace whose child listings are empty stores only itself, reads only
// its tags and reports nothing.
func TestScanIoTTwinMaker_EmptyWorkspace(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newITMCalls()
	h := itmHandlers(calls)
	h["ListWorkspaces"] = calls.answer("ListWorkspaces", func(any) any {
		return &iottwinmaker.ListWorkspacesOutput{WorkspaceSummaries: []itmtypes.WorkspaceSummary{itmWorkspace("t")}}
	})
	for _, op := range itmChildOps {
		h[op] = calls.answer(op, func(any) any { return itmEmptyOut(op) })
	}

	total, _, err := itmScan(context.Background(), t, st, h)
	if err != nil || total != 1 {
		t.Fatalf("scan = %d, %v; want 1, nil", total, err)
	}
	for _, op := range itmChildOps {
		if n := calls.count(op); n != 1 {
			t.Errorf("%s calls = %d; want 1, for ws-t", op, n)
		}
	}
	if want := []string{itmTestARN("workspace/ws-t")}; *warns != 0 || !slices.Equal(calls.tagARNs, want) {
		t.Errorf("warnings, tag reads = %d, %v; want 0, %v", *warns, calls.tagARNs, want)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanIoTTwinMaker_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newITMCalls()
	if _, _, err := itmScan(context.Background(), t, st, itmHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := itmScan(context.Background(), t, st, itmHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != 10 || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want 10, 0", total, inserted)
	}
}

// A failed tag read keeps the row without tags unless the resource is gone:
// a denial warns once under iottwinmaker:ListTagsForResource, a throttle
// after retries is a transient error, any other code a hard one; a
// not-found drops the row. A workspace dropped as not found is no parent:
// its children are not listed.
func TestScanIoTTwinMaker_TagReadFailures(t *testing.T) {
	for _, target := range []struct {
		typ, arn string
		goneRows int
	}{
		{TypeIoTTwinMakerWorkspace, itmTestARN("workspace/ws-t"), 5},
		{TypeIoTTwinMakerEntity, itmTestARN("workspace/ws-t/entity/en-t"), 1},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				kept      bool
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", crDenied, true, 0, 1},
				{"not found", crNotFound, false, 0, 0},
				{"other error", crtBadInput, true, outcomeError, 0},
				{"transient", crThrottled, true, outcomeTransient, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, itmTagsOp)
					calls := newITMCalls()
					calls.errs[target.arn] = c.err

					total, _, err := itmScan(context.Background(), t, st, itmHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.outcome == outcomeError {
						assertAPICode(t, err, "ValidationException")
					}
					r, ok := itmRowOf(t, st, target.typ, target.arn)
					if ok != c.kept {
						t.Fatalf("row stored = %v; want %v", ok, c.kept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					want := 10
					if !c.kept {
						want -= target.goneRows
					}
					if total != want || countStoredRows(t, st) != want {
						t.Errorf("total, stored = %d, %d; want %d", total, countStoredRows(t, st), want)
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", itmTagsOp, *warnings, c.wantWarns)
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

// The tag op's pages are all read and merged into one tag set; a repeated
// token fails the read, keeping the row untagged and returning the error.
func TestScanIoTTwinMaker_TagPages(t *testing.T) {
	scene := itmTestARN("workspace/ws-b/scene/sc-b")
	pages := func(repeat bool) func(any) (any, error) {
		return func(in any) (any, error) {
			req := in.(*iottwinmaker.ListTagsForResourceInput)
			if sv(req.ResourceARN) != scene {
				return &iottwinmaker.ListTagsForResourceOutput{}, nil
			}
			if req.NextToken == nil {
				return &iottwinmaker.ListTagsForResourceOutput{Tags: map[string]string{"a": "1"}, NextToken: sdkaws.String("p2")}, nil
			}
			next := sdkaws.String("p3")
			if repeat {
				next = req.NextToken
			}
			if sv(req.NextToken) == "p3" {
				next = nil
			}
			return &iottwinmaker.ListTagsForResourceOutput{Tags: map[string]string{"b": "2"}, NextToken: next}, nil
		}
	}
	t.Run("every page", func(t *testing.T) {
		st := newTestStore(t)
		h := itmHandlers(newITMCalls())
		h["ListTagsForResource"] = pages(false)
		if _, _, err := itmScan(context.Background(), t, st, h); err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		r, _ := itmRowOf(t, st, TypeIoTTwinMakerScene, scene)
		assertTagsMap(t, r.TagsJSON, map[string]string{"a": "1", "b": "2"})
	})
	t.Run("repeated token", func(t *testing.T) {
		st := newTestStore(t)
		h := itmHandlers(newITMCalls())
		h["ListTagsForResource"] = pages(true)
		_, _, err := itmScan(context.Background(), t, st, h)
		if !errors.Is(err, errRepeatedNextToken) {
			t.Fatalf("err = %v; want errRepeatedNextToken", err)
		}
		r, ok := itmRowOf(t, st, TypeIoTTwinMakerScene, scene)
		if !ok || r.TagsJSON != nil {
			t.Errorf("scene stored %v, TagsJSON %v; want it kept without tags", ok, r.TagsJSON)
		}
	})
}

// Denied tag reads in several phases are one warning for the scan.
func TestScanIoTTwinMaker_TagDenialsWarnOnce(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, itmTagsOp)
	calls := newITMCalls()
	denied := []struct{ typ, arn string }{
		{TypeIoTTwinMakerWorkspace, itmTestARN("workspace/ws-t")},
		{TypeIoTTwinMakerSyncJob, itmTestARN("workspace/ws-b/sync-job/sj-b")},
	}
	for _, d := range denied {
		calls.errs[d.arn] = crDenied
	}

	total, _, err := itmScan(context.Background(), t, st, itmHandlers(calls))
	if err != nil || total != 10 {
		t.Fatalf("scan = %d, %v; want 10, nil", total, err)
	}
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion {
		t.Errorf("%s warnings = %v; want one, scoped %s/%s", itmTagsOp, *warnings, testAccountID, testRegion)
	}
	for _, d := range denied {
		if r, ok := itmRowOf(t, st, d.typ, d.arn); !ok || r.TagsJSON != nil {
			t.Errorf("%s stored %v, TagsJSON %v; want it kept without tags", d.arn, ok, r.TagsJSON)
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanIoTTwinMaker_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, itmTagsOp)
	calls := newITMCalls()
	calls.errs[itmTestARN("workspace/ws-t")] = crThrottled
	calls.errs[itmTestARN("workspace/ws-b/component-type/ct-b")] = crtBadInput

	_, _, err := itmScan(context.Background(), t, st, itmHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", itmTagsOp, *warns)
	}
}

// A denied listing warns once under its own op, for both workspaces of a
// child listing, and costs no other phase its rows; a denied workspace
// listing leaves the child listings no workspace to read.
func TestScanIoTTwinMaker_DeniedListingWarnsUnderItsOp(t *testing.T) {
	for _, op := range append([]string{"ListWorkspaces"}, itmChildOps...) {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, "iottwinmaker:"+op)
			calls := newITMCalls()
			h := itmHandlers(calls)
			h[op] = func(any) (any, error) { return nil, crDenied }

			total, _, err := itmScan(context.Background(), t, st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || *opWarns != 1 {
				t.Errorf("warnings = %d (iottwinmaker:%s: %d); want 1 under the op", *warns, op, *opWarns)
			}
			want := 10 - 2
			if op == "ListWorkspaces" {
				want = 0
			}
			if stored := countStoredRows(t, st); total != want || stored != want {
				t.Errorf("total = %d, stored rows = %d; want %d", total, stored, want)
			}
			if op == "ListWorkspaces" && calls.count("ListSyncJobs") != 0 {
				t.Errorf("ListSyncJobs called %d times with no workspace read", calls.count("ListSyncJobs"))
			}
		})
	}
}

// A failed write of any type stops the scan before any later listing and
// sends no warning about the rows it lost, their denied tag reads included.
func TestScanIoTTwinMaker_FailedWriteStopsWithoutWarning(t *testing.T) {
	for _, c := range []struct {
		typ, deniedARN string
		later          []string
	}{
		{TypeIoTTwinMakerWorkspace, itmTestARN("workspace/ws-t"), itmChildOps},
		{TypeIoTTwinMakerComponentType, itmTestARN("workspace/ws-t/component-type/ct-t"), itmChildOps[1:]},
		{TypeIoTTwinMakerEntity, itmTestARN("workspace/ws-t/entity/en-t"), itmChildOps[2:]},
		{TypeIoTTwinMakerScene, itmTestARN("workspace/ws-t/scene/sc-t"), itmChildOps[3:]},
		{TypeIoTTwinMakerSyncJob, itmTestARN("workspace/ws-t/sync-job/sj-t"), nil},
	} {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			refuseStoreWrites(t, st, c.typ)
			calls := newITMCalls()
			calls.errs[c.deniedARN] = crDenied

			_, _, err := itmScan(context.Background(), t, st, itmHandlers(calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
			for _, op := range c.later {
				if n := calls.count(op); n != 0 {
					t.Errorf("%s called %d times after the failed write", op, n)
				}
			}
		})
	}
}

// A listing's failed page keeps the pages read before it, one workspace's
// failed listing costs the other workspace none of its rows, and no
// listing's failure stops the phases after it. The child listings model no
// not-found code, so ResourceNotFoundException is returned like any other.
func TestScanIoTTwinMaker_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	calls := newITMCalls()
	h := itmHandlers(calls)
	h["ListWorkspaces"] = calls.answer("ListWorkspaces", func(any) any {
		return &iottwinmaker.ListWorkspacesOutput{WorkspaceSummaries: []itmtypes.WorkspaceSummary{itmWorkspace("t"), itmWorkspace("b")}}
	})
	componentTypes := h["ListComponentTypes"]
	h["ListComponentTypes"] = func(in any) (any, error) {
		if itmWorkspaceOf(in) == "ws-t" {
			return nil, crNotFound
		}
		return componentTypes(in)
	}
	h["ListEntities"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			return nil, apiErr("ConflictException", "page 2")
		}
		return itmChildOut("ListEntities", itmWorkspaceOf(in), sdkaws.String("p2")), nil
	}
	h["ListScenes"] = func(any) (any, error) { return nil, crtBadInput }

	total, _, err := itmScan(context.Background(), t, st, h)
	for _, code := range []string{"ResourceNotFoundException", "ConflictException", "ValidationException"} {
		if !joinCarriesAPIErrorCode(err, code) {
			t.Errorf("err = %v; want it to carry %s", err, code)
		}
	}
	// 10 rows less ct-t and both scenes.
	if stored := countStoredRows(t, st); total != 7 || stored != 7 {
		t.Errorf("total, stored = %d, %d; want 7", total, stored)
	}
	for _, want := range []struct{ typ, arn string }{
		{TypeIoTTwinMakerComponentType, itmTestARN("workspace/ws-b/component-type/ct-b")},
		{TypeIoTTwinMakerEntity, itmTestARN("workspace/ws-t/entity/en-t")},
		{TypeIoTTwinMakerEntity, itmTestARN("workspace/ws-b/entity/en-b")},
		{TypeIoTTwinMakerSyncJob, itmTestARN("workspace/ws-t/sync-job/sj-t")},
	} {
		if _, ok := itmRowOf(t, st, want.typ, want.arn); !ok {
			t.Errorf("%s %s missing", want.typ, want.arn)
		}
	}
}

// A workspace listing whose second page fails keeps the first page's
// workspace, which is still read as a parent, and returns the error.
func TestScanIoTTwinMaker_WorkspacePageFailureKeepsPage(t *testing.T) {
	st := newTestStore(t)
	calls := newITMCalls()
	h := itmHandlers(calls)
	h["ListWorkspaces"] = failOnCall(2, apiErr("ConflictException", "page 2"), h["ListWorkspaces"])

	total, _, err := itmScan(context.Background(), t, st, h)
	assertAPICode(t, err, "ConflictException")
	if total != 5 {
		t.Errorf("total = %d; want 5, ws-t and its children", total)
	}
	if _, ok := itmRowOf(t, st, TypeIoTTwinMakerSyncJob, itmTestARN("workspace/ws-t/sync-job/sj-t")); !ok {
		t.Error("sync job of ws-t, from the page read, missing")
	}
}

// Elements listed without an ARN are not stored or read and are warned about
// once per listing, under a key that is never empty: a child is named by its
// workspace and its own id, the first workspace in listing order (ws-t) as
// each child listing returns the element for both. A workspace without a
// WorkspaceId is stored, warned about, and its children are not listed.
func TestScanIoTTwinMaker_UnstorableItemsWarn(t *testing.T) {
	for _, c := range []struct {
		name, op, key string
		out           any
		stored        int
	}{
		{"workspace without ARN", "ListWorkspaces", "first (an element with no identifier)", &iottwinmaker.ListWorkspacesOutput{WorkspaceSummaries: []itmtypes.WorkspaceSummary{{}, {}}}, 0},
		{"workspace without id", "ListWorkspaces", "first " + itmTestARN("workspace/ws-x"), &iottwinmaker.ListWorkspacesOutput{WorkspaceSummaries: []itmtypes.WorkspaceSummary{{Arn: sdkaws.String(itmTestARN("workspace/ws-x"))}}}, 1},
		{"component type without ARN", "ListComponentTypes", "first ws-t/ct-x", &iottwinmaker.ListComponentTypesOutput{ComponentTypeSummaries: []itmtypes.ComponentTypeSummary{{ComponentTypeId: sdkaws.String("ct-x"), ComponentTypeName: sdkaws.String("name-x")}}}, 8},
		{"entity without ARN", "ListEntities", "first ws-t/en-x", &iottwinmaker.ListEntitiesOutput{EntitySummaries: []itmtypes.EntitySummary{{EntityId: sdkaws.String("en-x"), EntityName: sdkaws.String("name-x")}}}, 8},
		{"scene without ARN", "ListScenes", "first ws-t/sc-x", &iottwinmaker.ListScenesOutput{SceneSummaries: []itmtypes.SceneSummary{{SceneId: sdkaws.String("sc-x")}}}, 8},
		{"sync job without ARN", "ListSyncJobs", "first ws-t/SITEWISE", &iottwinmaker.ListSyncJobsOutput{SyncJobSummaries: []itmtypes.SyncJobSummary{{SyncSource: sdkaws.String("SITEWISE")}}}, 8},
		{"sync job without ARN or source", "ListSyncJobs", "first ws-t/(an element with no identifier)", &iottwinmaker.ListSyncJobsOutput{SyncJobSummaries: []itmtypes.SyncJobSummary{{}}}, 8},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			warnings := bacWarnings(st, "iottwinmaker:"+c.op)
			calls := newITMCalls()
			h := itmHandlers(calls)
			h[c.op] = func(any) (any, error) { return c.out, nil }

			total, _, err := itmScan(context.Background(), t, st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || len(*warnings) != 1 || !strings.HasSuffix((*warnings)[0].Message, c.key) {
				t.Errorf("warnings = %d, under the op %v; want one ending %q", *warns, *warnings, c.key)
			}
			if total != c.stored {
				t.Errorf("total = %d; want %d", total, c.stored)
			}
			if c.op == "ListWorkspaces" && calls.count("ListEntities") != 0 {
				t.Errorf("ListEntities called %d times with no workspace to read", calls.count("ListEntities"))
			}
		})
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanIoTTwinMaker_RepeatedTokenKeepsPages(t *testing.T) {
	for _, c := range []struct{ op, typ, arn string }{
		{"ListWorkspaces", TypeIoTTwinMakerWorkspace, itmTestARN("workspace/ws-t")},
		{"ListEntities", TypeIoTTwinMakerEntity, itmTestARN("workspace/ws-t/entity/en-t")},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			h := itmHandlers(newITMCalls())
			if c.op == "ListWorkspaces" {
				h[c.op] = func(any) (any, error) {
					return &iottwinmaker.ListWorkspacesOutput{WorkspaceSummaries: []itmtypes.WorkspaceSummary{itmWorkspace("t")}, NextToken: sdkaws.String("same")}, nil
				}
			} else {
				h[c.op] = func(in any) (any, error) { return itmChildOut(c.op, itmWorkspaceOf(in), sdkaws.String("same")), nil }
			}

			_, _, err := itmScan(context.Background(), t, st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if _, ok := itmRowOf(t, st, c.typ, c.arn); !ok {
				t.Errorf("%s from the pages read missing", c.arn)
			}
		})
	}
}

// Once ctx ends no further phase starts, and the stop is reported once.
func TestScanIoTTwinMaker_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newITMCalls()
	h := itmHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h["ListEntities"] = func(any) (any, error) {
		cancel()
		return nil, context.Canceled
	}

	_, _, err := itmScan(ctx, t, st, h)
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
	if n := calls.count("ListScenes") + calls.count("ListSyncJobs"); n != 0 {
		t.Errorf("ListScenes and ListSyncJobs called %d times after ctx ended", n)
	}
}

// A region without IoT TwinMaker (NXDOMAIN on the workspace listing, nothing
// stored) is unavailable and makes no child listing. An NXDOMAIN after rows
// were stored is a failure of this scan: the counts are kept.
func TestScanIoTTwinMaker_Unreachable(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "api.iottwinmaker.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("endpoint absent", func(t *testing.T) {
		st := newTestStore(t)
		calls := newITMCalls()
		h := itmHandlers(calls)
		h["ListWorkspaces"] = func(any) (any, error) { return nil, nx }
		total, _, err := itmScan(context.Background(), t, st, h)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 || calls.count("ListComponentTypes") != 0 {
			t.Errorf("total, ListComponentTypes calls = %d, %d; want 0, 0", total, calls.count("ListComponentTypes"))
		}
	})
	t.Run("tag read after rows were listed", func(t *testing.T) {
		st := newTestStore(t)
		calls := newITMCalls()
		calls.errs[itmTestARN("workspace/ws-t/scene/sc-t")] = nx
		total, _, err := itmScan(context.Background(), t, st, itmHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
		if total != 10 {
			t.Errorf("total = %d; want 10", total)
		}
	})
}
