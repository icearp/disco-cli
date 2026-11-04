package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codepipeline"
	cptypes "github.com/aws/aws-sdk-go-v2/service/codepipeline/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/store"
)

var (
	// The pipelines' and custom action types' NativeIDs, written out by hand
	// in the Service Reference's ARN formats.
	cplTestPipeA   = fmt.Sprintf("arn:aws:codepipeline:%s:%s:pipe-a", testRegion, testAccountID)
	cplTestPipeB   = fmt.Sprintf("arn:aws:codepipeline:%s:%s:pipe-b", testRegion, testAccountID)
	cplTestActionA = fmt.Sprintf("arn:aws:codepipeline:%s:%s:actiontype:Custom/Build/prov-a/1", testRegion, testAccountID)
	cplTestActionB = fmt.Sprintf("arn:aws:codepipeline:%s:%s:actiontype:Custom/Test/prov-b/2", testRegion, testAccountID)
	cplTestHookA   = fmt.Sprintf("arn:aws:codepipeline:%s:%s:webhook:hook-a", testRegion, testAccountID)
	cplTestHookB   = fmt.Sprintf("arn:aws:codepipeline:%s:%s:webhook:hook-b", testRegion, testAccountID)
	cplTestRole    = fmt.Sprintf("arn:aws:iam::%s:role/cp-service", testAccountID)

	cplDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: codepipeline:List")
	cplThrottle   = apiErr("ThrottlingException", "slow down")
	cplBadARN     = apiErr("InvalidArnException", "bad arn")
	cplValidation = apiErr("ValidationException", "bad input")
	cplTagsGoneE  = apiErr("ResourceNotFoundException", "gone")
	cplPipeGoneE  = apiErr("PipelineNotFoundException", "gone")

	// cplTestHookURL is webhook A's Url, with the trigger token a real one
	// carries in its query.
	cplTestHookURL = "https://" + testRegion + ".webhooks.aws/trigger?t=secret-token&v=1"

	// cplTestTags are the tags ListTagsForResource returns: pipe-a's two, one
	// per page (cplCalls.listTags), and action type A's one; every other
	// pipeline and action type has none.
	cplTestTags = map[string]map[string]string{
		cplTestPipeA:   {"env": "prod", "owner": "ci"},
		cplTestActionA: {"team": "build"},
	}
)

// cplTestTotal is every row cplHandlers yields: 2 pipelines, 2 custom action
// types and 2 webhooks.
const cplTestTotal = 6

// cplCalls records the calls cplHandlers answers, and holds the tags and the
// errors its ListTagsForResource returns per ARN on top of cplTestTags, and
// the errors its GetPipeline returns per name.
type cplCalls struct {
	// tagARNs is every ARN ListTagsForResource was sent, gets every name
	// GetPipeline was sent, lists every listing op called.
	tagARNs, gets, lists parentCalls
	tags                 map[string]map[string]string
	tagErrs, getErrs     map[string]error

	mu sync.Mutex
	// maxResults is the MaxResults of every call, per op (0 when unset);
	// owners the ActionOwnerFilter of every ListActionTypes call.
	maxResults map[string][]int32
	owners     []cptypes.ActionOwner
}

// called records a call of op and reports whether it asks for the first page.
func (c *cplCalls) called(op string, in any) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxResults == nil {
		c.maxResults = map[string][]int32{}
	}
	var n int32
	if m := bacField[*int32](in, "MaxResults"); m != nil {
		n = *m
	}
	c.maxResults[op] = append(c.maxResults[op], n)
	return bacField[*string](in, "NextToken") == nil
}

// listed is called for a listing op.
func (c *cplCalls) listed(op string, in any) bool {
	c.lists.add(op)
	return c.called(op, in)
}

// cplTagList is tags as SDK tags, sorted by key.
func cplTagList(tags map[string]string) []cptypes.Tag {
	var out []cptypes.Tag
	for k, v := range tags {
		out = append(out, cptypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	sort.Slice(out, func(i, j int) bool { return *out[i].Key < *out[j].Key })
	return out
}

// listTags answers ListTagsForResource from c.tagErrs, c.tags and cplTestTags,
// in that order. A resource with more than one tag gets one tag per page.
func (c *cplCalls) listTags(in any) (any, error) {
	tin := in.(*codepipeline.ListTagsForResourceInput)
	arn := sv(tin.ResourceArn)
	c.tagARNs.add(arn)
	c.called("ListTagsForResource", in)
	if err, ok := c.tagErrs[arn]; ok {
		return nil, err
	}
	tags, ok := c.tags[arn]
	if !ok {
		tags = cplTestTags[arn]
	}
	list := cplTagList(tags)
	page := 0
	if tok := sv(tin.NextToken); tok != "" {
		_, _ = fmt.Sscanf(tok, "tag-page-%d", &page)
	}
	out := &codepipeline.ListTagsForResourceOutput{}
	if page < len(list) {
		out.Tags = list[page : page+1]
	}
	if page+1 < len(list) {
		out.NextToken = sdkaws.String(fmt.Sprintf("tag-page-%d", page+1))
	}
	return out, nil
}

// cplPipelineBody is the GetPipeline output for the pipeline named name.
func cplPipelineBody(name, arn string) *codepipeline.GetPipelineOutput {
	return &codepipeline.GetPipelineOutput{
		Pipeline: &cptypes.PipelineDeclaration{
			Name: sdkaws.String(name), RoleArn: &cplTestRole, Version: sdkaws.Int32(3),
			ArtifactStore: &cptypes.ArtifactStore{Location: sdkaws.String("cp-artifacts"), Type: cptypes.ArtifactStoreTypeS3},
		},
		Metadata: &cptypes.PipelineMetadata{PipelineArn: sdkaws.String(arn)},
	}
}

// cplActionType is the custom action type category/provider/version.
func cplActionType(category cptypes.ActionCategory, provider, version string) cptypes.ActionType {
	return cptypes.ActionType{
		Id:                    &cptypes.ActionTypeId{Category: category, Owner: cptypes.ActionOwnerCustom, Provider: sdkaws.String(provider), Version: sdkaws.String(version)},
		InputArtifactDetails:  &cptypes.ArtifactDetails{MinimumCount: 0, MaximumCount: 1},
		OutputArtifactDetails: &cptypes.ArtifactDetails{MinimumCount: 0, MaximumCount: 1},
	}
}

// cplHandlers answers a region with pipeline A on page 1 and B on page 2,
// action type A on page 1 and B on page 2, webhook A (tagged in its element)
// on page 1 and B on page 2. Pipeline A and action type A are tagged
// (cplTestTags).
func cplHandlers(calls *cplCalls) map[string]func(in any) (any, error) {
	page2 := "page-2"
	return map[string]func(in any) (any, error){
		"ListPipelines": func(in any) (any, error) {
			if calls.listed("ListPipelines", in) {
				return &codepipeline.ListPipelinesOutput{
					Pipelines: []cptypes.PipelineSummary{{Name: sdkaws.String("pipe-a"), Version: sdkaws.Int32(3), PipelineType: cptypes.PipelineTypeV2}},
					NextToken: &page2,
				}, nil
			}
			return &codepipeline.ListPipelinesOutput{Pipelines: []cptypes.PipelineSummary{{Name: sdkaws.String("pipe-b"), PipelineType: cptypes.PipelineTypeV1}}}, nil
		},
		"GetPipeline": func(in any) (any, error) {
			name := sv(in.(*codepipeline.GetPipelineInput).Name)
			calls.gets.add(name)
			if err, ok := calls.getErrs[name]; ok {
				return nil, err
			}
			return cplPipelineBody(name, codePipelineARN("aws", testRegion, testAccountID, name)), nil
		},
		"ListActionTypes": func(in any) (any, error) {
			first := calls.listed("ListActionTypes", in)
			calls.mu.Lock()
			calls.owners = append(calls.owners, in.(*codepipeline.ListActionTypesInput).ActionOwnerFilter)
			calls.mu.Unlock()
			if first {
				return &codepipeline.ListActionTypesOutput{ActionTypes: []cptypes.ActionType{cplActionType(cptypes.ActionCategoryBuild, "prov-a", "1")}, NextToken: &page2}, nil
			}
			return &codepipeline.ListActionTypesOutput{ActionTypes: []cptypes.ActionType{cplActionType(cptypes.ActionCategoryTest, "prov-b", "2")}}, nil
		},
		"ListWebhooks": func(in any) (any, error) {
			if calls.listed("ListWebhooks", in) {
				return &codepipeline.ListWebhooksOutput{
					Webhooks: []cptypes.ListWebhookItem{{
						Arn: &cplTestHookA, Url: &cplTestHookURL,
						Definition: &cptypes.WebhookDefinition{Name: sdkaws.String("hook-a"), TargetPipeline: sdkaws.String("pipe-a"), TargetAction: sdkaws.String("Source")},
						Tags:       cplTagList(map[string]string{"hook": "gh"}),
					}},
					NextToken: &page2,
				}, nil
			}
			return &codepipeline.ListWebhooksOutput{Webhooks: []cptypes.ListWebhookItem{{Arn: &cplTestHookB}}}, nil
		},
		"ListTagsForResource": calls.listTags,
	}
}

// cplScan runs the service scan entry point against handlers; an op without a
// handler is denied.
func cplScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, cplDenied)
	return scanCodePipeline(ctx, acct, testRegion, st, testScanID)
}

func cplSorted(ids ...string) []string {
	sort.Strings(ids)
	return ids
}

// cplTaggedIDs is the NativeID of every pipeline and action type cplHandlers
// yields, sorted: the ARNs sent to ListTagsForResource.
func cplTaggedIDs() []string {
	return cplSorted(cplTestPipeA, cplTestPipeB, cplTestActionA, cplTestActionB)
}

// cplStored is the NativeID of every stored row of the three types, sorted.
func cplStored(t *testing.T, st *store.Store) []string {
	t.Helper()
	var ids []string
	for _, typ := range []string{TypeCodePipelinePipeline, TypeCodePipelineCustomActionType, TypeCodePipelineWebhook} {
		for id := range rowsByNativeID(t, st, typ) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// cplAll is every row cplHandlers yields, sorted.
func cplAll() []string {
	return cplSorted(cplTestPipeA, cplTestPipeB, cplTestActionA, cplTestActionB, cplTestHookA, cplTestHookB)
}

// cplWithout is cplAll without drop.
func cplWithout(drop ...string) []string {
	return slices.DeleteFunc(cplAll(), func(id string) bool { return slices.Contains(drop, id) })
}

// Every pipeline, custom action type and webhook is stored from both its
// pages. A pipeline's NativeID is built from its name and its attributes are
// its GetPipeline output; an action type's NativeID is built in the
// actiontype ARN format and its attributes are the listed element; a
// webhook is stored under its listed ARN with the listed element as
// attributes, its Url redacted, and the tags the element carries. ListTagsForResource is sent
// exactly the ARN of each pipeline and action type, reads every page of
// their tags and is never sent a webhook's. ListPipelines sends MaxResults
// 1000, ListWebhooks and ListTagsForResource 100, ListActionTypes none and
// ActionOwnerFilter Custom. No error, no warning.
func TestScanCodePipeline_StoresEveryRow(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls cplCalls
	total, inserted, err := cplScan(context.Background(), st, cplHandlers(&calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != cplTestTotal || inserted != cplTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, cplTestTotal, cplTestTotal)
	}
	if got := calls.tagARNs.distinct(); !slices.Equal(got, cplTaggedIDs()) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, cplTaggedIDs())
	}
	if got := calls.gets.distinct(); !slices.Equal(got, []string{"pipe-a", "pipe-b"}) {
		t.Errorf("GetPipeline names = %v; want pipe-a, pipe-b", got)
	}
	// Two pages for each listing; pipe-a's two tags take two pages, the
	// other three rows one call each.
	wantCalls := map[string]struct {
		n   int
		max int32
	}{
		"ListPipelines": {2, 1000}, "ListActionTypes": {2, 0}, "ListWebhooks": {2, 100}, "ListTagsForResource": {5, 100},
	}
	for op, w := range wantCalls {
		got := calls.maxResults[op]
		if len(got) != w.n {
			t.Errorf("%s calls = %d; want %d", op, len(got), w.n)
		}
		for _, m := range got {
			if m != w.max {
				t.Errorf("%s MaxResults = %d; want %d", op, m, w.max)
			}
		}
	}
	if want := []cptypes.ActionOwner{"Custom", "Custom"}; !slices.Equal(calls.owners, want) {
		t.Errorf("ListActionTypes ActionOwnerFilter = %v; want %v", calls.owners, want)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}

	pipeBody := func(name string) func(string) bool {
		return func(a string) bool {
			var got codepipeline.GetPipelineOutput
			return json.Unmarshal([]byte(a), &got) == nil && got.Pipeline != nil &&
				sv(got.Pipeline.Name) == name && sv(got.Pipeline.RoleArn) == cplTestRole && got.Metadata != nil
		}
	}
	actionEl := func(provider string) func(string) bool {
		return func(a string) bool {
			var got cptypes.ActionType
			return json.Unmarshal([]byte(a), &got) == nil && got.Id != nil && sv(got.Id.Provider) == provider && got.Id.Owner == cptypes.ActionOwnerCustom
		}
	}
	for _, c := range []struct {
		typ, id string
		name    string // "-": the row has none
		tags    map[string]string
		attr    func(string) bool
	}{
		{TypeCodePipelinePipeline, cplTestPipeA, "pipe-a", cplTestTags[cplTestPipeA], pipeBody("pipe-a")},
		{TypeCodePipelinePipeline, cplTestPipeB, "pipe-b", nil, pipeBody("pipe-b")},
		{TypeCodePipelineCustomActionType, cplTestActionA, "Build/prov-a/1", cplTestTags[cplTestActionA], actionEl("prov-a")},
		{TypeCodePipelineCustomActionType, cplTestActionB, "Test/prov-b/2", nil, actionEl("prov-b")},
		{TypeCodePipelineWebhook, cplTestHookA, "hook-a", map[string]string{"hook": "gh"}, func(a string) bool {
			var got cptypes.ListWebhookItem
			return json.Unmarshal([]byte(a), &got) == nil && sv(got.Url) == redact.Placeholder && sv(got.Arn) == cplTestHookA &&
				got.Definition != nil && sv(got.Definition.TargetPipeline) == "pipe-a"
		}},
		{TypeCodePipelineWebhook, cplTestHookB, "-", nil, func(a string) bool {
			var got cptypes.ListWebhookItem
			return json.Unmarshal([]byte(a), &got) == nil && sv(got.Arn) == cplTestHookB
		}},
	} {
		t.Run(c.id, func(t *testing.T) {
			r, ok := rowsByNativeID(t, st, c.typ)[c.id]
			if !ok {
				t.Fatalf("%s %s not stored", c.typ, c.id)
			}
			switch {
			case c.name == "-" && r.Name != nil:
				t.Errorf("Name = %q; want none", *r.Name)
			case c.name != "-" && (r.Name == nil || *r.Name != c.name):
				t.Errorf("Name = %v; want %q", r.Name, c.name)
			}
			if r.Status != nil {
				t.Errorf("Status = %q; want none", *r.Status)
			}
			if r.CreatedAt != nil {
				t.Errorf("CreatedAt = %v; want none", r.CreatedAt)
			}
			if r.ManagedByProvider {
				t.Error("ManagedByProvider = true; want false")
			}
			if sv(r.Region) != testRegion {
				t.Errorf("Region = %q; want %q", sv(r.Region), testRegion)
			}
			if r.Provider != "aws" || r.AccountID != testAccountID || sv(r.AccountName) != newTestAccount(testAccountID).Name {
				t.Errorf("Provider, AccountID, AccountName = %q, %q, %q; want aws, %s, %q", r.Provider, r.AccountID, sv(r.AccountName), testAccountID, newTestAccount(testAccountID).Name)
			}
			if !c.attr(r.AttributesJSON) {
				t.Errorf("attributes = %s; want the row's source body", r.AttributesJSON)
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

// A rescan stores the same rows and inserts none: the tags read are the same.
func TestScanCodePipeline_RescanInsertsNone(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls cplCalls
	if _, _, err := cplScan(context.Background(), st, cplHandlers(&calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := cplScan(context.Background(), st, cplHandlers(&calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != cplTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, cplTestTotal)
	}
}

// A region with no pipeline, action type or webhook stores nothing and reads
// no body or tags.
func TestScanCodePipeline_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls cplCalls
	h := cplHandlers(&calls)
	h["ListPipelines"] = func(any) (any, error) { return &codepipeline.ListPipelinesOutput{}, nil }
	h["ListActionTypes"] = func(any) (any, error) { return &codepipeline.ListActionTypesOutput{}, nil }
	h["ListWebhooks"] = func(any) (any, error) { return &codepipeline.ListWebhooksOutput{}, nil }
	total, inserted, err := cplScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
	}
	if got := calls.tagARNs.distinct(); len(got) != 0 {
		t.Errorf("ListTagsForResource ARNs = %v; want none", got)
	}
	if got := calls.gets.distinct(); len(got) != 0 {
		t.Errorf("GetPipeline names = %v; want none", got)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently. A denial keeps the row
// untagged and warns once under the tag op with the scan's scope; a throttle
// keeps it and is transient; any other error keeps it and is an error.
func TestScanCodePipeline_TagReadFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		arn, typ string
		err      error
		outcome  serviceOutcome // -1: no error
		code     string
		wantKept bool
		warn     bool
	}{
		{"pipeline/not found", cplTestPipeA, TypeCodePipelinePipeline, cplTagsGoneE, -1, "", false, false},
		{"action type/not found", cplTestActionB, TypeCodePipelineCustomActionType, cplTagsGoneE, -1, "", false, false},
		{"pipeline/denied", cplTestPipeA, TypeCodePipelinePipeline, cplDenied, -1, "", true, true},
		{"action type/denied", cplTestActionA, TypeCodePipelineCustomActionType, cplDenied, -1, "", true, true},
		{"pipeline/transient", cplTestPipeA, TypeCodePipelinePipeline, cplThrottle, outcomeTransient, "ThrottlingException", true, false},
		{"action type/other error", cplTestActionA, TypeCodePipelineCustomActionType, cplBadARN, outcomeError, "InvalidArnException", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := cplCalls{tagErrs: map[string]error{c.arn: c.err}}
			total, _, err := cplScan(context.Background(), st, cplHandlers(&calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			want := cplAll()
			if !c.wantKept {
				want = cplWithout(c.arn)
			}
			if got := cplStored(t, st); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
			if total != len(want) {
				t.Errorf("total = %d; want %d", total, len(want))
			}
			if r, ok := rowsByNativeID(t, st, c.typ)[c.arn]; ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			var wantOps []string
			if c.warn {
				wantOps = []string{cplListTagsOp}
			}
			if got := amWarnings(w); !slices.Equal(got, wantOps) {
				t.Fatalf("warnings = %v; want %v", w, wantOps)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// A tag read whose second page fails keeps the row untagged and returns the
// error; a repeated nextToken fails the read the same way.
func TestScanCodePipeline_TagPagesFail(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		page func(out *codepipeline.ListTagsForResourceOutput) (any, error)
		is   func(error) bool
	}{
		{
			"second page fails", func(*codepipeline.ListTagsForResourceOutput) (any, error) { return nil, cplValidation },
			func(err error) bool { return isAPIErrorCode(err, "ValidationException") },
		},
		{"repeated token", func(out *codepipeline.ListTagsForResourceOutput) (any, error) {
			out.NextToken = sdkaws.String("tag-page-1")
			return out, nil
		}, func(err error) bool { return errors.Is(err, errRepeatedNextToken) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			var calls cplCalls
			h := cplHandlers(&calls)
			h["ListTagsForResource"] = func(in any) (any, error) {
				out, err := calls.listTags(in)
				if sv(in.(*codepipeline.ListTagsForResourceInput).NextToken) != "" {
					return c.page(out.(*codepipeline.ListTagsForResourceOutput))
				}
				return out, err
			}
			total, _, err := cplScan(context.Background(), st, h)
			if !c.is(err) {
				t.Fatalf("err = %v; want the failed page's error", err)
			}
			if total != cplTestTotal {
				t.Errorf("total = %d; want %d", total, cplTestTotal)
			}
			if r := rowsByNativeID(t, st, TypeCodePipelinePipeline)[cplTestPipeA]; r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
		})
	}
}

// Denied tag reads in both phases send one warning for the whole scan; the
// rows are kept.
func TestScanCodePipeline_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := cplCalls{tagErrs: map[string]error{cplTestPipeA: cplDenied, cplTestActionB: cplDenied}}
	total, _, err := cplScan(context.Background(), st, cplHandlers(&calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != cplTestTotal {
		t.Errorf("total = %d; want %d", total, cplTestTotal)
	}
	if got := amWarnings(warns()); !slices.Equal(got, []string{cplListTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, cplListTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to one
// warning, so the hard error decides the outcome.
func TestScanCodePipeline_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, cplListTagsOp)
	calls := cplCalls{tagErrs: map[string]error{cplTestPipeA: cplThrottle, cplTestActionB: cplBadARN}}
	_, _, err := cplScan(context.Background(), st, cplHandlers(&calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidArnException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", cplListTagsOp, *warns)
	}
}

// A throttled ListWebhooks page beside a hard ListPipelines failure is demoted
// to one warning under ListWebhooks, so the hard error decides the outcome.
func TestScanCodePipeline_WebhookThrottleDemotedUnderItsOp(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls cplCalls
	h := cplHandlers(&calls)
	for op, fail := range map[string]error{"ListPipelines": cplValidation, "ListWebhooks": cplThrottle} {
		list := h[op]
		h[op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				return nil, fail
			}
			return list(in)
		}
	}
	_, _, err := cplScan(context.Background(), st, h)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if got := amWarnings(warns()); !slices.Equal(got, []string{cplListWebhooksOp}) {
		t.Errorf("warnings = %v; want one under %s", got, cplListWebhooksOp)
	}
}

// A failed GetPipeline. PipelineNotFoundException is modeled, so a pipeline
// deleted since it was listed is dropped silently and has no tags read. Any
// other failure keeps the listed summary as the row, its tags still read: a
// denial warns once under GetPipeline, a throttle is transient, any other
// error is an error.
func TestScanCodePipeline_GetPipelineFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		errs     map[string]error
		outcome  serviceOutcome // -1: no error
		code     string
		wantKept bool
		warn     bool
	}{
		{"not found", map[string]error{"pipe-a": cplPipeGoneE}, -1, "", false, false},
		{"denied", map[string]error{"pipe-a": cplDenied, "pipe-b": cplDenied}, -1, "", true, true},
		{"transient", map[string]error{"pipe-a": cplThrottle}, outcomeTransient, "ThrottlingException", true, false},
		{"other error", map[string]error{"pipe-a": cplValidation}, outcomeError, "ValidationException", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := cplCalls{getErrs: c.errs}
			total, _, err := cplScan(context.Background(), st, cplHandlers(&calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			want, wantTags := cplAll(), cplTaggedIDs()
			if !c.wantKept {
				want = cplWithout(cplTestPipeA)
				wantTags = slices.DeleteFunc(wantTags, func(id string) bool { return id == cplTestPipeA })
			}
			if got := cplStored(t, st); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
			if total != len(want) {
				t.Errorf("total = %d; want %d", total, len(want))
			}
			if got := calls.tagARNs.distinct(); !slices.Equal(got, wantTags) {
				t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantTags)
			}
			if r, ok := rowsByNativeID(t, st, TypeCodePipelinePipeline)[cplTestPipeA]; ok {
				var summary cptypes.PipelineSummary
				if err := json.Unmarshal([]byte(r.AttributesJSON), &summary); err != nil || sv(summary.Name) != "pipe-a" || summary.PipelineType != cptypes.PipelineTypeV2 {
					t.Errorf("attributes = %s; want the listed summary", r.AttributesJSON)
				}
				assertTagsMap(t, r.TagsJSON, cplTestTags[cplTestPipeA])
			}
			var wantOps []string
			if c.warn {
				wantOps = []string{cplGetPipelineOp}
			}
			if got := amWarnings(warns()); !slices.Equal(got, wantOps) {
				t.Errorf("warnings = %v; want %v", got, wantOps)
			}
		})
	}
}

// A listing whose second page fails keeps its first page, and the later
// phases still run.
func TestScanCodePipeline_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		op   string
		lost string
	}{
		{"ListPipelines", cplTestPipeB},
		{"ListActionTypes", cplTestActionB},
		{"ListWebhooks", cplTestHookB},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			var calls cplCalls
			h := cplHandlers(&calls)
			list := h[tc.op]
			h[tc.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					return nil, cplValidation
				}
				return list(in)
			}
			total, _, err := cplScan(context.Background(), st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			want := cplWithout(tc.lost)
			if got := cplStored(t, st); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
			if total != len(want) {
				t.Errorf("total = %d, want %d", total, len(want))
			}
		})
	}
}

// A denied listing warns once under its op and is not an error; its first
// page, when it was read, is kept, and the later phases still run.
func TestScanCodePipeline_ListDeniedWarnsAndContinues(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		op   string
		page int // the page denied
		lost []string
	}{
		{"ListPipelines", 1, []string{cplTestPipeA, cplTestPipeB}},
		{"ListActionTypes", 2, []string{cplTestActionB}},
		{"ListWebhooks", 2, []string{cplTestHookB}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			var calls cplCalls
			h := cplHandlers(&calls)
			list := h[tc.op]
			h[tc.op] = func(in any) (any, error) {
				if (bacField[*string](in, "NextToken") == nil) == (tc.page == 1) {
					return nil, cplDenied
				}
				return list(in)
			}
			if _, _, err := cplScan(context.Background(), st, h); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got, want := cplStored(t, st), cplWithout(tc.lost...); !slices.Equal(got, want) {
				t.Errorf("stored = %v; want %v", got, want)
			}
			if got, want := amWarnings(warns()), []string{"codepipeline:" + tc.op}; !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
}

// A failed write stops the scan in each phase: no warning about the rows it
// lost (a denied tag or GetPipeline read among them), no later phase runs,
// and the earlier phases' rows stay counted.
func TestScanCodePipeline_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		typ        string
		tagErrs    map[string]error
		getErrs    map[string]error
		laterLists []string // listings that must not be called
		wantTotal  int
	}{
		{TypeCodePipelinePipeline, map[string]error{cplTestPipeA: cplDenied}, map[string]error{"pipe-a": cplDenied}, []string{"ListActionTypes", "ListWebhooks"}, 0},
		{TypeCodePipelineCustomActionType, map[string]error{cplTestActionA: cplDenied}, nil, []string{"ListWebhooks"}, 2},
		{TypeCodePipelineWebhook, nil, nil, nil, 4},
	} {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			refuseStoreWrites(t, st, c.typ)
			calls := cplCalls{tagErrs: c.tagErrs, getErrs: c.getErrs}
			total, _, err := cplScan(context.Background(), st, cplHandlers(&calls))
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

// The pipeline and action type listings write a page before they read the
// next: the first page's row is stored when the second page is requested.
func TestScanCodePipeline_WritesEachPageBeforeTheNext(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls cplCalls
	h := cplHandlers(&calls)
	pageOne := map[string]string{"ListPipelines": cplTestPipeA, "ListActionTypes": cplTestActionA}
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
	if _, _, err := cplScan(context.Background(), st, h); err != nil {
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
// later phases still run.
func TestScanCodePipeline_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"ListPipelines", "ListActionTypes", "ListWebhooks"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			var calls cplCalls
			h := cplHandlers(&calls)
			twoPages := h[op]
			h[op] = func(in any) (any, error) {
				out, err := twoPages(in)
				switch o := out.(type) {
				case *codepipeline.ListPipelinesOutput:
					o.NextToken = sdkaws.String("same")
				case *codepipeline.ListActionTypesOutput:
					o.NextToken = sdkaws.String("same")
				case *codepipeline.ListWebhooksOutput:
					o.NextToken = sdkaws.String("same")
				}
				return out, err
			}
			total, _, err := cplScan(context.Background(), st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != cplTestTotal {
				t.Errorf("total = %d; want %d", total, cplTestTotal)
			}
			if got := cplStored(t, st); !slices.Equal(got, cplAll()) {
				t.Errorf("stored = %v; want %v", got, cplAll())
			}
		})
	}
}

// cplActionNoProvider is a custom action type whose Id has no Provider.
func cplActionNoProvider() cptypes.ActionType {
	a := cplActionType(cptypes.ActionCategoryDeploy, "", "1")
	a.Id.Provider = nil
	return a
}

// cplAssertNotStoredWarning checks that w holds one warning under op, with the
// scan's scope, counting n elements and naming key.
func cplAssertNotStoredWarning(t *testing.T, w []store.ScanWarning, op string, n int, key string) {
	t.Helper()
	var got []store.ScanWarning
	for _, x := range w {
		if x.Service == op {
			got = append(got, x)
		}
	}
	if len(got) != 1 {
		t.Fatalf("%s warnings = %v; want 1", op, got)
	}
	x := got[0]
	if x.Scope != testAccountID+"/"+testRegion || !strings.HasPrefix(x.Message, fmt.Sprintf("%d ", n)) || !strings.Contains(x.Message, key) {
		t.Errorf("%s warning %+v; want %d elements, first %q, in %s/%s", op, x, n, key, testAccountID, testRegion)
	}
}

// A listed element without the fields its NativeID needs is not stored, and
// each listing warns once under its op, counting those elements and naming
// the first; its siblings are stored and have their body and tags read. An
// action type is not stored when its Id lacks any of category, provider and
// version.
func TestScanCodePipeline_ElementWithoutIdentifier(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls cplCalls
	h := cplHandlers(&calls)
	h["ListPipelines"] = func(any) (any, error) {
		return &codepipeline.ListPipelinesOutput{Pipelines: []cptypes.PipelineSummary{{Version: sdkaws.Int32(1)}, {Name: sdkaws.String("pipe-a")}}}, nil
	}
	h["ListActionTypes"] = func(any) (any, error) {
		return &codepipeline.ListActionTypesOutput{ActionTypes: []cptypes.ActionType{
			{},
			cplActionType(cptypes.ActionCategoryDeploy, "prov-c", ""),
			cplActionType("", "prov-c", "1"),
			cplActionNoProvider(),
			cplActionType(cptypes.ActionCategoryBuild, "prov-a", "1"),
		}}, nil
	}
	h["ListWebhooks"] = func(any) (any, error) {
		return &codepipeline.ListWebhooksOutput{Webhooks: []cptypes.ListWebhookItem{
			{Definition: &cptypes.WebhookDefinition{Name: sdkaws.String("no-arn")}}, {Arn: &cplTestHookA},
		}}, nil
	}
	if _, _, err := cplScan(context.Background(), st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got, want := cplStored(t, st), cplSorted(cplTestPipeA, cplTestActionA, cplTestHookA); !slices.Equal(got, want) {
		t.Errorf("stored = %v; want %v", got, want)
	}
	if got, want := calls.tagARNs.distinct(), cplSorted(cplTestPipeA, cplTestActionA); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
	}
	if got := calls.gets.distinct(); !slices.Equal(got, []string{"pipe-a"}) {
		t.Errorf("GetPipeline names = %v; want pipe-a", got)
	}
	w := warns()
	wantOps := []string{cplListPipelinesOp, cplListActionTypesOp, cplListWebhooksOp}
	if got := amWarnings(w); !slices.Equal(got, wantOps) {
		t.Fatalf("warnings = %v; want %v", w, wantOps)
	}
	cplAssertNotStoredWarning(t, w, cplListPipelinesOp, 1, noIdentifier)
	cplAssertNotStoredWarning(t, w, cplListActionTypesOp, 4, noIdentifier)
	cplAssertNotStoredWarning(t, w, cplListWebhooksOp, 1, "no-arn")
}

// An action type whose Id lacks one of category, provider and version is
// named in the warning by the {Category}/{Provider}/{Version} it has.
func TestScanCodePipeline_ActionTypePartialIDNamed(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		el   cptypes.ActionType
		key  string
	}{
		{"no category", cplActionType("", "prov-c", "1"), "/prov-c/1"},
		{"no provider", cplActionNoProvider(), "Deploy//1"},
		{"no version", cplActionType(cptypes.ActionCategoryDeploy, "prov-c", ""), "Deploy/prov-c/"},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			var calls cplCalls
			h := cplHandlers(&calls)
			h["ListActionTypes"] = func(any) (any, error) {
				return &codepipeline.ListActionTypesOutput{ActionTypes: []cptypes.ActionType{c.el}}, nil
			}
			if _, _, err := cplScan(context.Background(), st, h); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := rowsByNativeID(t, st, TypeCodePipelineCustomActionType); len(got) != 0 {
				t.Errorf("action types stored = %v; want none", got)
			}
			if got, want := calls.tagARNs.distinct(), cplSorted(cplTestPipeA, cplTestPipeB); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
			}
			w := warns()
			if got := amWarnings(w); !slices.Equal(got, []string{cplListActionTypesOp}) {
				t.Fatalf("warnings = %v; want one under %s", w, cplListActionTypesOp)
			}
			cplAssertNotStoredWarning(t, w, cplListActionTypesOp, 1, c.key)
		})
	}
}

// A GovCloud or China pipeline and action type have their NativeIDs built in
// the account's partition, and that ARN reaches the tag op unchanged; a
// webhook keeps its listed ARN and has no tag read.
func TestScanCodePipeline_PartitionARNs(t *testing.T) {
	t.Parallel()
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			pipe := partitionARN(pf.partition, "codepipeline", pf.region, "pipe-a")
			action := partitionARN(pf.partition, "codepipeline", pf.region, "actiontype:Custom/Build/prov-a/1")
			hook := partitionARN(pf.partition, "codepipeline", pf.region, "webhook:hook-gov")
			calls := cplCalls{tags: map[string]map[string]string{
				pipe: {"env": pf.partition}, action: {"env": pf.partition},
			}}
			h := cplHandlers(&calls)
			h["ListPipelines"] = func(any) (any, error) {
				return &codepipeline.ListPipelinesOutput{Pipelines: []cptypes.PipelineSummary{{Name: sdkaws.String("pipe-a")}}}, nil
			}
			h["ListActionTypes"] = func(any) (any, error) {
				return &codepipeline.ListActionTypesOutput{ActionTypes: []cptypes.ActionType{cplActionType(cptypes.ActionCategoryBuild, "prov-a", "1")}}, nil
			}
			h["ListWebhooks"] = func(any) (any, error) {
				return &codepipeline.ListWebhooksOutput{Webhooks: []cptypes.ListWebhookItem{{Arn: &hook, Tags: cplTagList(map[string]string{"env": pf.partition})}}}, nil
			}
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(h, cplDenied)
			if _, _, err := scanCodePipeline(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got, want := calls.tagARNs.distinct(), cplSorted(pipe, action); !slices.Equal(got, want) {
				t.Fatalf("ListTagsForResource ARNs = %v; want %v", got, want)
			}
			for typ, arn := range map[string]string{TypeCodePipelinePipeline: pipe, TypeCodePipelineCustomActionType: action, TypeCodePipelineWebhook: hook} {
				r, ok := rowsByNativeID(t, st, typ)[arn]
				if !ok {
					t.Fatalf("%s %s not stored", typ, arn)
				}
				assertTagsMap(t, r.TagsJSON, map[string]string{"env": pf.partition})
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanCodePipeline_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var calls cplCalls
	h := cplHandlers(&calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := h["ListActionTypes"]
	h["ListActionTypes"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}
	_, _, err := cplScan(ctx, st, h)
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
	if slices.Contains(calls.lists.distinct(), "ListWebhooks") {
		t.Error("ListWebhooks called after ctx ended")
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanCodePipeline_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "codepipeline.us-east-1.amazonaws.com", IsNotFound: true}
	for _, op := range []string{"ListActionTypes", "ListWebhooks"} {
		t.Run(op+" after others stored rows", func(t *testing.T) {
			st := newTestStore(t)
			var calls cplCalls
			h := cplHandlers(&calls)
			h[op] = func(any) (any, error) { return nil, nx }
			total, _, err := cplScan(context.Background(), st, h)
			assertGlueOutcome(t, err, outcomeError)
			if want := cplTestTotal - 2; total != want {
				t.Errorf("total = %d; want the %d other rows counted", total, want)
			}
		})
	}
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := cplCalls{tagErrs: map[string]error{cplTestActionB: nx}}
		_, _, err := cplScan(context.Background(), st, cplHandlers(&calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanCodePipeline(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}
