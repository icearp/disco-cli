package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/mediaconvert"
	mctypes "github.com/aws/aws-sdk-go-v2/service/mediaconvert/types"
	"github.com/icearp/disco-cli/store"
)

var (
	mcvTestTemplateA = fmt.Sprintf("arn:aws:mediaconvert:%s:%s:jobTemplates/tmpl-a", testRegion, testAccountID)
	mcvTestTemplateB = fmt.Sprintf("arn:aws:mediaconvert:%s:%s:jobTemplates/tmpl-b", testRegion, testAccountID)
	mcvTestTemplateS = fmt.Sprintf("arn:aws:mediaconvert:%s:%s:jobTemplates/System-Ott_Hls_Ts_Avc_Aac", testRegion, testAccountID)
	mcvTestPresetA   = fmt.Sprintf("arn:aws:mediaconvert:%s:%s:presets/preset-a", testRegion, testAccountID)
	mcvTestPresetB   = fmt.Sprintf("arn:aws:mediaconvert:%s:%s:presets/preset-b", testRegion, testAccountID)
	mcvTestPresetS   = fmt.Sprintf("arn:aws:mediaconvert:%s:%s:presets/System-Avc_16x9_1080p_29_97_10000Kbps", testRegion, testAccountID)
	mcvTestQueueA    = fmt.Sprintf("arn:aws:mediaconvert:%s:%s:queues/queue-a", testRegion, testAccountID)
	mcvTestQueueB    = fmt.Sprintf("arn:aws:mediaconvert:%s:%s:queues/queue-b", testRegion, testAccountID)
	mcvTestQueueS    = fmt.Sprintf("arn:aws:mediaconvert:%s:%s:queues/Default", testRegion, testAccountID)

	mcvDenied   = apiErr("ForbiddenException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: mediaconvert:List")
	mcvThrottle = apiErr("TooManyRequestsException", "slow down")
	mcvBadInput = apiErr("BadRequestException", "bad request")
	mcvNotFound = apiErr("NotFoundException", "gone")

	// mcvTestTags are the tags ListTagsForResource returns; every other
	// resource has none.
	mcvTestTags = map[string]map[string]string{
		mcvTestTemplateA: {"env": "prod"},
		mcvTestPresetA:   {"team": "media"},
		mcvTestQueueA:    {"app": "vod", "cost": "7"},
	}
)

// mcvAt is 2026-03-04 05:06 UTC plus sec seconds: a listed element's
// CreatedAt.
func mcvAt(sec int) *time.Time {
	at := time.Date(2026, 3, 4, 5, 6, sec, 0, time.UTC)
	return &at
}

// mcvTestTotal is every row mcvHandlers yields: 3 job templates, 3 presets
// and 3 queues.
const mcvTestTotal = 9

// mcvCalls records the calls mcvHandlers answers, and holds the tags and the
// errors its ListTagsForResource returns per ARN on top of mcvTestTags.
type mcvCalls struct {
	// tagARNs is every ARN ListTagsForResource was sent, lists every
	// listing op called.
	tagARNs, lists parentCalls
	tags           map[string]map[string]string
	tagErrs        map[string]error

	mu sync.Mutex
	// maxResults is the MaxResults of every listing call, per op.
	maxResults map[string][]int32
}

// listed records a listing call of op and reports whether it asks for the
// first page.
func (c *mcvCalls) listed(op string, in any) bool {
	c.lists.add(op)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxResults == nil {
		c.maxResults = map[string][]int32{}
	}
	c.maxResults[op] = append(c.maxResults[op], sdkaws.ToInt32(bacField[*int32](in, "MaxResults")))
	return bacField[*string](in, "NextToken") == nil
}

// listTags answers ListTagsForResource from c.tagErrs, c.tags and
// mcvTestTags, in that order.
func (c *mcvCalls) listTags(in any) (any, error) {
	arn := sv(in.(*mediaconvert.ListTagsForResourceInput).Arn)
	c.tagARNs.add(arn)
	if err, ok := c.tagErrs[arn]; ok {
		return nil, err
	}
	tags, ok := c.tags[arn]
	if !ok {
		tags = mcvTestTags[arn]
	}
	return &mediaconvert.ListTagsForResourceOutput{ResourceTags: &mctypes.ResourceTags{Arn: &arn, Tags: tags}}, nil
}

// mcvHandlers answers a region with custom job template A and system template
// S on page 1 and custom B on page 2; custom preset A on page 1 and system
// preset S and custom B on page 2; the system Default queue and custom queue
// A on page 1 and custom queue B (no status) on page 2. Template A, preset A
// and queue A are tagged (mcvTestTags). Every element but the B ones has a
// CreatedAt (mcvAt).
func mcvHandlers(calls *mcvCalls) map[string]func(in any) (any, error) {
	page2 := "page-2"
	return map[string]func(in any) (any, error){
		"ListJobTemplates": func(in any) (any, error) {
			if calls.listed("ListJobTemplates", in) {
				return &mediaconvert.ListJobTemplatesOutput{
					JobTemplates: []mctypes.JobTemplate{
						{Arn: &mcvTestTemplateA, Name: sdkaws.String("tmpl-a"), Type: mctypes.TypeCustom, CreatedAt: mcvAt(1), Category: sdkaws.String("ott"), Queue: &mcvTestQueueA},
						{Arn: &mcvTestTemplateS, Name: sdkaws.String("System-Ott_Hls_Ts_Avc_Aac"), Type: mctypes.TypeSystem, CreatedAt: mcvAt(2)},
					},
					NextToken: &page2,
				}, nil
			}
			return &mediaconvert.ListJobTemplatesOutput{JobTemplates: []mctypes.JobTemplate{
				{Arn: &mcvTestTemplateB, Name: sdkaws.String("tmpl-b"), Type: mctypes.TypeCustom},
			}}, nil
		},
		"ListPresets": func(in any) (any, error) {
			if calls.listed("ListPresets", in) {
				return &mediaconvert.ListPresetsOutput{
					Presets:   []mctypes.Preset{{Arn: &mcvTestPresetA, Name: sdkaws.String("preset-a"), Type: mctypes.TypeCustom, CreatedAt: mcvAt(3), Description: sdkaws.String("1080p")}},
					NextToken: &page2,
				}, nil
			}
			return &mediaconvert.ListPresetsOutput{Presets: []mctypes.Preset{
				{Arn: &mcvTestPresetS, Name: sdkaws.String("System-Avc_16x9_1080p_29_97_10000Kbps"), Type: mctypes.TypeSystem, CreatedAt: mcvAt(4)},
				{Arn: &mcvTestPresetB, Name: sdkaws.String("preset-b"), Type: mctypes.TypeCustom},
			}}, nil
		},
		"ListQueues": func(in any) (any, error) {
			if calls.listed("ListQueues", in) {
				return &mediaconvert.ListQueuesOutput{
					Queues: []mctypes.Queue{
						{Arn: &mcvTestQueueS, Name: sdkaws.String("Default"), Type: mctypes.TypeSystem, Status: mctypes.QueueStatusActive, CreatedAt: mcvAt(5)},
						{Arn: &mcvTestQueueA, Name: sdkaws.String("queue-a"), Type: mctypes.TypeCustom, Status: mctypes.QueueStatusPaused, CreatedAt: mcvAt(6), PricingPlan: mctypes.PricingPlanOnDemand},
					},
					NextToken: &page2,
				}, nil
			}
			return &mediaconvert.ListQueuesOutput{Queues: []mctypes.Queue{
				{Arn: &mcvTestQueueB, Name: sdkaws.String("queue-b"), Type: mctypes.TypeCustom},
			}}, nil
		},
		"ListTagsForResource": calls.listTags,
	}
}

// mcvScan runs the service scan entry point against handlers; an op without
// a handler is denied.
func mcvScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, mcvDenied)
	return scanMediaConvert(ctx, acct, testRegion, st, testScanID)
}

func mcvSorted(ids ...string) []string {
	sort.Strings(ids)
	return ids
}

// mcvTaggedIDs is the NativeID of every custom job template, preset and
// queue mcvHandlers yields, sorted: the ARNs sent to ListTagsForResource.
func mcvTaggedIDs() []string {
	return mcvSorted(mcvTestTemplateA, mcvTestTemplateB, mcvTestPresetA, mcvTestPresetB, mcvTestQueueA, mcvTestQueueB)
}

// mcvAllIDs is every NativeID mcvHandlers yields, sorted.
func mcvAllIDs() []string {
	return mcvSorted(append(mcvTaggedIDs(), mcvTestTemplateS, mcvTestPresetS, mcvTestQueueS)...)
}

// mcvStored is the NativeID of every stored job template, preset and queue,
// sorted.
func mcvStored(t *testing.T, st *store.Store) []string {
	t.Helper()
	var ids []string
	for _, typ := range []string{TypeMediaConvertJobTemplate, TypeMediaConvertPreset, TypeMediaConvertQueue} {
		for id := range rowsByNativeID(t, st, typ) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// mcvWithout is ids without drop.
func mcvWithout(ids []string, drop ...string) []string {
	return slices.DeleteFunc(slices.Clone(ids), func(id string) bool { return slices.Contains(drop, id) })
}

// Every job template, preset and queue is stored from both its pages under
// its listed ARN, its attributes the listed element, its CreatedAt the
// element's (none when it has none), with the tags
// ListTagsForResource returns (nil for none). The tag op is sent exactly the
// ARN of each custom row, once; a SYSTEM row is ManagedByProvider and its
// tags are not read. Every listing sends MaxResults 20. No error, no warning.
func TestScanMediaConvert_StoresEveryRow(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls mcvCalls
	counted, upserts := countUpserts(st)
	total, err := mcvScan(context.Background(), counted, mcvHandlers(&calls))
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != mcvTestTotal || inserted != mcvTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, mcvTestTotal, mcvTestTotal)
	}
	if got := calls.tagARNs.distinct(); !slices.Equal(got, mcvTaggedIDs()) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, mcvTaggedIDs())
	}
	if n := len(calls.tagARNs.ids); n != len(mcvTaggedIDs()) {
		t.Errorf("ListTagsForResource calls = %d; want one per custom row, %d", n, len(mcvTaggedIDs()))
	}
	for _, op := range []string{"ListJobTemplates", "ListPresets", "ListQueues"} {
		if got := calls.maxResults[op]; !slices.Equal(got, []int32{20, 20}) {
			t.Errorf("%s MaxResults per call = %v; want [20 20]", op, got)
		}
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}

	for _, c := range []struct {
		typ, id, name string
		status        string // "": the row has none
		created       string // "": the row has none
		managed       bool
		tags          map[string]string
		attr          func(string) bool // whether the attributes are the listed element
	}{
		{TypeMediaConvertJobTemplate, mcvTestTemplateA, "tmpl-a", "", "2026-03-04T05:06:01Z", false, mcvTestTags[mcvTestTemplateA], func(a string) bool {
			var jt mctypes.JobTemplate
			return json.Unmarshal([]byte(a), &jt) == nil && sv(jt.Category) == "ott" && sv(jt.Queue) == mcvTestQueueA
		}},
		{TypeMediaConvertJobTemplate, mcvTestTemplateB, "tmpl-b", "", "", false, nil, nil},
		{TypeMediaConvertJobTemplate, mcvTestTemplateS, "System-Ott_Hls_Ts_Avc_Aac", "", "2026-03-04T05:06:02Z", true, nil, func(a string) bool {
			var jt mctypes.JobTemplate
			return json.Unmarshal([]byte(a), &jt) == nil && jt.Type == mctypes.TypeSystem
		}},
		{TypeMediaConvertPreset, mcvTestPresetA, "preset-a", "", "2026-03-04T05:06:03Z", false, mcvTestTags[mcvTestPresetA], func(a string) bool {
			var p mctypes.Preset
			return json.Unmarshal([]byte(a), &p) == nil && sv(p.Description) == "1080p"
		}},
		{TypeMediaConvertPreset, mcvTestPresetB, "preset-b", "", "", false, nil, nil},
		{TypeMediaConvertPreset, mcvTestPresetS, "System-Avc_16x9_1080p_29_97_10000Kbps", "", "2026-03-04T05:06:04Z", true, nil, nil},
		{TypeMediaConvertQueue, mcvTestQueueA, "queue-a", "PAUSED", "2026-03-04T05:06:06Z", false, mcvTestTags[mcvTestQueueA], func(a string) bool {
			var q mctypes.Queue
			return json.Unmarshal([]byte(a), &q) == nil && q.PricingPlan == mctypes.PricingPlanOnDemand
		}},
		{TypeMediaConvertQueue, mcvTestQueueB, "queue-b", "", "", false, nil, nil},
		{TypeMediaConvertQueue, mcvTestQueueS, "Default", "ACTIVE", "2026-03-04T05:06:05Z", true, nil, nil},
	} {
		t.Run(c.id, func(t *testing.T) {
			r, ok := rowsByNativeID(t, st, c.typ)[c.id]
			if !ok {
				t.Fatalf("%s %s not stored", c.typ, c.id)
			}
			if sv(r.Name) != c.name {
				t.Errorf("Name = %v; want %q", r.Name, c.name)
			}
			switch {
			case c.status == "" && r.Status != nil:
				t.Errorf("Status = %q; want none", *r.Status)
			case c.status != "" && sv(r.Status) != c.status:
				t.Errorf("Status = %v; want %q", r.Status, c.status)
			}
			switch {
			case c.created == "" && r.CreatedAt != nil:
				t.Errorf("CreatedAt = %q; want none", *r.CreatedAt)
			case c.created != "" && sv(r.CreatedAt) != c.created:
				t.Errorf("CreatedAt = %v; want %q", r.CreatedAt, c.created)
			}
			if r.ManagedByProvider != c.managed {
				t.Errorf("ManagedByProvider = %v; want %v", r.ManagedByProvider, c.managed)
			}
			if sv(r.Region) != testRegion {
				t.Errorf("Region = %q; want %q", sv(r.Region), testRegion)
			}
			if r.Provider != "aws" || r.AccountID != testAccountID || sv(r.AccountName) != newTestAccount(testAccountID).Name {
				t.Errorf("Provider, AccountID, AccountName = %q, %q, %q; want aws, %s, %q", r.Provider, r.AccountID, sv(r.AccountName), testAccountID, newTestAccount(testAccountID).Name)
			}
			if c.attr != nil && !c.attr(r.AttributesJSON) {
				t.Errorf("attributes = %s; want the listed element", r.AttributesJSON)
			}
			if c.tags == nil {
				if r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				return
			}
			assertTagsMap(t, r.TagsJSON, c.tags)
		})
	}
}

// A tag response without ResourceTags leaves the row untagged and is not an
// error.
func TestScanMediaConvert_TagResponseWithoutResourceTags(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls mcvCalls
	h := mcvHandlers(&calls)
	h["ListTagsForResource"] = func(in any) (any, error) {
		calls.tagARNs.add(sv(in.(*mediaconvert.ListTagsForResourceInput).Arn))
		return &mediaconvert.ListTagsForResourceOutput{}, nil
	}
	total, err := mcvScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != mcvTestTotal {
		t.Errorf("total = %d; want %d", total, mcvTestTotal)
	}
	if r := rowsByNativeID(t, st, TypeMediaConvertQueue)[mcvTestQueueA]; r.TagsJSON != nil {
		t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
	}
}

// A rescan reports every stored row in its total, not only the rows the store
// newly wrote. On a first scan into an empty store every row is new, so only a
// rescan catches a total taken from the store's written count.
func TestScanMediaConvert_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls mcvCalls
	if _, err := mcvScan(context.Background(), st, mcvHandlers(&calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, err := mcvScan(context.Background(), st, mcvHandlers(&calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != mcvTestTotal {
		t.Errorf("rescan total = %d; want %d", total, mcvTestTotal)
	}
}

// A region with no job template, preset or queue stores nothing and reads no
// tags.
func TestScanMediaConvert_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls mcvCalls
	h := mcvHandlers(&calls)
	h["ListJobTemplates"] = func(any) (any, error) { return &mediaconvert.ListJobTemplatesOutput{}, nil }
	h["ListPresets"] = func(any) (any, error) { return &mediaconvert.ListPresetsOutput{}, nil }
	h["ListQueues"] = func(any) (any, error) { return &mediaconvert.ListQueuesOutput{}, nil }
	counted, upserts := countUpserts(st)
	total, err := mcvScan(context.Background(), counted, h)
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
	}
	if got := calls.tagARNs.distinct(); len(got) != 0 {
		t.Errorf("ListTagsForResource ARNs = %v; want none", got)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A failed tag read. NotFoundException is modeled, so a resource deleted
// since it was listed is dropped silently. A denial (ForbiddenException)
// keeps the row untagged and warns once under the tag op with the scan's
// scope; a throttle keeps it and is transient; any other error keeps it and
// is an error.
func TestScanMediaConvert_TagReadFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		arn, typ  string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}{
		{"job template/not found", mcvTestTemplateA, TypeMediaConvertJobTemplate, mcvNotFound, -1, "", mcvTestTotal - 1, false, false},
		{"preset/not found", mcvTestPresetB, TypeMediaConvertPreset, mcvNotFound, -1, "", mcvTestTotal - 1, false, false},
		{"queue/not found", mcvTestQueueA, TypeMediaConvertQueue, mcvNotFound, -1, "", mcvTestTotal - 1, false, false},
		{"preset/denied", mcvTestPresetA, TypeMediaConvertPreset, mcvDenied, -1, "", mcvTestTotal, true, true},
		{"queue/transient", mcvTestQueueA, TypeMediaConvertQueue, mcvThrottle, outcomeTransient, "TooManyRequestsException", mcvTestTotal, true, false},
		{"job template/other error", mcvTestTemplateA, TypeMediaConvertJobTemplate, mcvBadInput, outcomeError, "BadRequestException", mcvTestTotal, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := mcvCalls{tagErrs: map[string]error{c.arn: c.err}}
			total, err := mcvScan(context.Background(), st, mcvHandlers(&calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			want := mcvAllIDs()
			if !c.wantKept {
				want = mcvWithout(want, c.arn)
			}
			if got := mcvStored(t, st); !slices.Equal(got, want) {
				t.Fatalf("stored = %v; want %v", got, want)
			}
			if r, ok := rowsByNativeID(t, st, c.typ)[c.arn]; ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			var wantWarn []string
			if c.warn {
				wantWarn = []string{mcvListTagsForResourceOp}
			}
			if got := amWarnings(w); !slices.Equal(got, wantWarn) {
				t.Fatalf("warnings = %v; want %v", w, wantWarn)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// Denied tag reads in every listing send one warning for the whole scan; the
// rows are kept.
func TestScanMediaConvert_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := mcvCalls{tagErrs: map[string]error{mcvTestTemplateB: mcvDenied, mcvTestPresetA: mcvDenied, mcvTestQueueB: mcvDenied}}
	total, err := mcvScan(context.Background(), st, mcvHandlers(&calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != mcvTestTotal {
		t.Errorf("total = %d; want %d", total, mcvTestTotal)
	}
	if got := amWarnings(warns()); !slices.Equal(got, []string{mcvListTagsForResourceOp}) {
		t.Errorf("warnings = %v; want one under %s", got, mcvListTagsForResourceOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanMediaConvert_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, mcvListTagsForResourceOp)
	calls := mcvCalls{tagErrs: map[string]error{mcvTestTemplateA: mcvThrottle, mcvTestQueueB: mcvBadInput}}
	_, err := mcvScan(context.Background(), st, mcvHandlers(&calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "BadRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", mcvListTagsForResourceOp, *warns)
	}
}

// A listing whose second page fails keeps its first page, and the later
// listings still run.
func TestScanMediaConvert_ListFailureKeepsPagesAndLaterListings(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		op   string
		lost []string // the rows of the failed page
	}{
		{"ListJobTemplates", []string{mcvTestTemplateB}},
		{"ListPresets", []string{mcvTestPresetS, mcvTestPresetB}},
		{"ListQueues", []string{mcvTestQueueB}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			var calls mcvCalls
			h := mcvHandlers(&calls)
			list := h[tc.op]
			h[tc.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					return nil, mcvBadInput
				}
				return list(in)
			}
			total, err := mcvScan(context.Background(), st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "BadRequestException")
			want := mcvWithout(mcvAllIDs(), tc.lost...)
			if total != len(want) {
				t.Errorf("total = %d, want %d", total, len(want))
			}
			if got := mcvStored(t, st); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
		})
	}
}

// A denied listing warns once under its op and is not an error; its first
// page, when it was read, is kept, and the later listings still run.
func TestScanMediaConvert_ListDeniedWarnsAndContinues(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		op   string
		page int // the page denied
		lost []string
	}{
		{"ListJobTemplates", 1, []string{mcvTestTemplateA, mcvTestTemplateS, mcvTestTemplateB}},
		{"ListPresets", 2, []string{mcvTestPresetS, mcvTestPresetB}},
		{"ListQueues", 1, []string{mcvTestQueueS, mcvTestQueueA, mcvTestQueueB}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			var calls mcvCalls
			h := mcvHandlers(&calls)
			list := h[tc.op]
			h[tc.op] = func(in any) (any, error) {
				if (bacField[*string](in, "NextToken") == nil) == (tc.page == 1) {
					return nil, mcvDenied
				}
				return list(in)
			}
			if _, err := mcvScan(context.Background(), st, h); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got, want := mcvStored(t, st), mcvWithout(mcvAllIDs(), tc.lost...); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
			if got, want := amWarnings(warns()), []string{"mediaconvert:" + tc.op}; !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
}

// A failed write stops the scan in each listing: no warning about the rows
// it lost (a denied tag read among them), no later listing runs, and the
// earlier listings' rows stay counted.
func TestScanMediaConvert_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		typ        string
		deniedTag  string
		laterLists []string // listings that must not be called
		wantTotal  int
	}{
		{TypeMediaConvertJobTemplate, mcvTestTemplateA, []string{"ListPresets", "ListQueues"}, 0},
		{TypeMediaConvertPreset, mcvTestPresetA, []string{"ListQueues"}, 3},
		{TypeMediaConvertQueue, mcvTestQueueA, nil, 3 + 3},
	} {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			refuseStoreWrites(t, st, c.typ)
			calls := mcvCalls{tagErrs: map[string]error{c.deniedTag: mcvDenied}}
			total, err := mcvScan(context.Background(), st, mcvHandlers(&calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			for _, op := range c.laterLists {
				if slices.Contains(calls.lists.distinct(), op) {
					t.Errorf("%s called after the failed write", op)
				}
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
		})
	}
}

// Each listing writes a page before it reads the next: the first page's row
// is stored when the second page is requested.
func TestScanMediaConvert_WritesEachPageBeforeTheNext(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls mcvCalls
	h := mcvHandlers(&calls)
	pageOne := map[string]string{"ListJobTemplates": mcvTestTemplateA, "ListPresets": mcvTestPresetA, "ListQueues": mcvTestQueueA}
	var mu sync.Mutex
	lookups := map[string]error{} // per listing: GetResource of its page-1 row, when page 2 was requested
	for op, arn := range pageOne {
		list := h[op]
		h[op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				_, err := st.GetResource(store.ResourceID("aws", testAccountID, arn))
				mu.Lock()
				lookups[op] = err
				mu.Unlock()
			}
			return list(in)
		}
	}
	if _, err := mcvScan(context.Background(), st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for op, arn := range pageOne {
		err, asked := lookups[op]
		if !asked {
			t.Errorf("%s: page 2 never requested", op)
			continue
		}
		if err != nil {
			t.Errorf("%s: %s not stored before page 2 was read: %v", op, arn, err)
		}
	}
}

// A repeated NextToken ends each listing as an error, keeping its pages; the
// later listings still run.
func TestScanMediaConvert_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"ListJobTemplates", "ListPresets", "ListQueues"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			var calls mcvCalls
			h := mcvHandlers(&calls)
			twoPages := h[op]
			h[op] = func(in any) (any, error) {
				out, err := twoPages(in)
				switch o := out.(type) {
				case *mediaconvert.ListJobTemplatesOutput:
					o.NextToken = sdkaws.String("same")
				case *mediaconvert.ListPresetsOutput:
					o.NextToken = sdkaws.String("same")
				case *mediaconvert.ListQueuesOutput:
					o.NextToken = sdkaws.String("same")
				}
				return out, err
			}
			total, err := mcvScan(context.Background(), st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != mcvTestTotal {
				t.Errorf("total = %d; want %d", total, mcvTestTotal)
			}
			if got := mcvStored(t, st); !slices.Equal(got, mcvAllIDs()) {
				t.Errorf("stored = %v; want %v", got, mcvAllIDs())
			}
		})
	}
}

// A listed element without an ARN is not stored, and each listing warns once
// under its op; its siblings are stored and have their tags read.
func TestScanMediaConvert_ElementWithoutARN(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls mcvCalls
	h := mcvHandlers(&calls)
	h["ListJobTemplates"] = func(any) (any, error) {
		return &mediaconvert.ListJobTemplatesOutput{JobTemplates: []mctypes.JobTemplate{{Name: sdkaws.String("no-arn")}, {Arn: &mcvTestTemplateA}}}, nil
	}
	h["ListPresets"] = func(any) (any, error) {
		return &mediaconvert.ListPresetsOutput{Presets: []mctypes.Preset{{}, {Arn: &mcvTestPresetA}}}, nil
	}
	h["ListQueues"] = func(any) (any, error) {
		return &mediaconvert.ListQueuesOutput{Queues: []mctypes.Queue{{Name: sdkaws.String("no-arn")}, {Arn: &mcvTestQueueA}}}, nil
	}
	if _, err := mcvScan(context.Background(), st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := mcvSorted(mcvTestTemplateA, mcvTestPresetA, mcvTestQueueA)
	if got := mcvStored(t, st); !slices.Equal(got, want) {
		t.Errorf("stored = %v; want %v", got, want)
	}
	if got := calls.tagARNs.distinct(); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
	}
	wantOps := []string{mcvListJobTemplatesOp, mcvListPresetsOp, mcvListQueuesOp}
	if got := amWarnings(warns()); !slices.Equal(got, wantOps) {
		t.Fatalf("warnings = %v; want %v", got, wantOps)
	}
}

// A GovCloud or China ARN reaches the tag op unchanged.
func TestScanMediaConvert_PartitionARNs(t *testing.T) {
	t.Parallel()
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			arns := map[string]string{
				TypeMediaConvertJobTemplate: partitionARN(pf.partition, "mediaconvert", pf.region, "jobTemplates/tmpl-gov"),
				TypeMediaConvertPreset:      partitionARN(pf.partition, "mediaconvert", pf.region, "presets/preset-gov"),
				TypeMediaConvertQueue:       partitionARN(pf.partition, "mediaconvert", pf.region, "queues/queue-gov"),
			}
			calls := mcvCalls{tags: map[string]map[string]string{}}
			for _, arn := range arns {
				calls.tags[arn] = map[string]string{"env": pf.partition}
			}
			h := mcvHandlers(&calls)
			h["ListJobTemplates"] = func(any) (any, error) {
				return &mediaconvert.ListJobTemplatesOutput{JobTemplates: []mctypes.JobTemplate{{Arn: sdkaws.String(arns[TypeMediaConvertJobTemplate])}}}, nil
			}
			h["ListPresets"] = func(any) (any, error) {
				return &mediaconvert.ListPresetsOutput{Presets: []mctypes.Preset{{Arn: sdkaws.String(arns[TypeMediaConvertPreset])}}}, nil
			}
			h["ListQueues"] = func(any) (any, error) {
				return &mediaconvert.ListQueuesOutput{Queues: []mctypes.Queue{{Arn: sdkaws.String(arns[TypeMediaConvertQueue])}}}, nil
			}
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(h, mcvDenied)
			if _, err := scanMediaConvert(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			want := mcvSorted(arns[TypeMediaConvertJobTemplate], arns[TypeMediaConvertPreset], arns[TypeMediaConvertQueue])
			if got := calls.tagARNs.distinct(); !slices.Equal(got, want) {
				t.Fatalf("ListTagsForResource ARNs = %v; want %v", got, want)
			}
			for typ, arn := range arns {
				r, ok := rowsByNativeID(t, st, typ)[arn]
				if !ok {
					t.Fatalf("%s %s not stored", typ, arn)
				}
				assertTagsMap(t, r.TagsJSON, map[string]string{"env": pf.partition})
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the listing
// in flight saw it, and the later listings are not run.
func TestScanMediaConvert_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls mcvCalls
	h := mcvHandlers(&calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := h["ListJobTemplates"]
	h["ListJobTemplates"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}
	_, err := mcvScan(ctx, st, h)
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
	for _, op := range []string{"ListPresets", "ListQueues"} {
		if slices.Contains(calls.lists.distinct(), op) {
			t.Errorf("%s called after ctx ended", op)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanMediaConvert_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "mediaconvert.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		var calls mcvCalls
		h := mcvHandlers(&calls)
		h["ListPresets"] = func(any) (any, error) { return nil, nx }
		total, err := mcvScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		if want := mcvTestTotal - 3; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := mcvCalls{tagErrs: map[string]error{mcvTestQueueB: nx}}
		_, err := mcvScan(context.Background(), st, mcvHandlers(&calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, err := scanMediaConvert(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}
