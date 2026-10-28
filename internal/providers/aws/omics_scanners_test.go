package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/omics"
	omicstypes "github.com/aws/aws-sdk-go-v2/service/omics/types"
	"github.com/icearp/disco-cli/store"
)

// The fixture: every account-wide listing serves "<kind>_t" on its first
// page and "<kind>_b" on its second; the run caches' second page also
// carries runCache_gov_t, the account's own in aws-us-gov. Each annotation
// store holds one version, each reference store one reference, each workflow
// one version; ListShares serves one share. Every ARN containing "_t" is
// tagged.

var (
	omtDenied    = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: omics:List")
	omtNotFound  = apiErr("ResourceNotFoundException", "gone")
	omtThrottled = apiErr("ThrottlingException", "rate exceeded")
	omtBadInput  = apiErr("ValidationException", "bad")
	omtNoRegion  = apiErr("AccessDeniedException", "Unable to determine service/operation name to be authorized")
)

const omtOtherAccount = "210987654321"

func omtARN(res string) string { return arnFor("aws", "omics", testRegion, testAccountID, res) }

func omtGovRunCache() string {
	return arnFor("aws-us-gov", "omics", "us-gov-west-1", testAccountID, "runCache/runCache_gov_t")
}

// omtKinds is every account-wide listing but ListShares: its op, the ARN
// kind and id prefix of its items, and their type.
var omtKinds = []struct{ op, kind, typ string }{
	{"ListConfigurations", "configuration", TypeOmicsConfiguration},
	{"ListRunGroups", "runGroup", TypeOmicsRunGroup},
	{"ListSequenceStores", "sequenceStore", TypeOmicsSequenceStore},
	{"ListVariantStores", "variantStore", TypeOmicsVariantStore},
	{"ListRunCaches", "runCache", TypeOmicsRunCache},
	{"ListAnnotationStores", "annotationStore", TypeOmicsAnnotationStore},
	{"ListReferenceStores", "referenceStore", TypeOmicsReferenceStore},
	{"ListWorkflows", "workflow", TypeOmicsWorkflow},
}

func omtItemARN(kind, n string) string { return omtARN(kind + "/" + kind + "_" + n) }

func omtAnnotationVersion(n string) string {
	return omtARN("annotationStore/annotationStore_" + n + "/version/v1")
}

func omtReference(n string) string {
	return omtARN("referenceStore/referenceStore_" + n + "/reference/ref_" + n)
}

func omtWorkflowVersion(n string) string { return omtARN("workflow/workflow_" + n + "/version/v1") }

func omtShareID() string { return omtItemARN("workflow", "t") + "/share/a" }

func omtTagged(arn string) bool { return strings.Contains(arn, "_t") }

type omtRow struct{ typ, arn, name string }

// omtRows is every row the fixture stores, with its Name; the share last.
func omtRows() []omtRow {
	rows := []omtRow{{TypeOmicsRunCache, omtGovRunCache(), "name runCache_gov_t"}}
	for _, n := range []string{"t", "b"} {
		for _, k := range omtKinds {
			rows = append(rows, omtRow{k.typ, omtItemARN(k.kind, n), "name " + k.kind + "_" + n})
		}
		rows = append(rows,
			omtRow{TypeOmicsAnnotationStoreVersion, omtAnnotationVersion(n), "v1"},
			omtRow{TypeOmicsReference, omtReference(n), "name ref_" + n},
			omtRow{TypeOmicsWorkflowVersion, omtWorkflowVersion(n), "v1"})
	}
	return append(rows, omtRow{TypeOmicsShare, omtShareID(), "share-a"})
}

// omtCalls records the stubbed calls and answers ListTagsForResource from
// tags and errs, keyed by ARN.
type omtCalls struct{ *bacCalls }

func newOMTCalls() *omtCalls {
	c := &omtCalls{newBACCalls()}
	for _, r := range omtRows() {
		if r.typ != TypeOmicsShare && omtTagged(r.arn) {
			c.tags[r.arn] = map[string]string{"type": r.typ}
		}
	}
	return c
}

func (c *omtCalls) listTags(in any) (any, error) {
	arn := sv(in.(*omics.ListTagsForResourceInput).ResourceArn)
	c.called("ListTagsForResource", in)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &omics.ListTagsForResourceOutput{Tags: tags}, nil
}

// omtToken is the page token in, whichever field its op names it.
func omtToken(in any) *string {
	return cmp.Or(bacField[*string](in, "NextToken"), bacField[*string](in, "StartingToken"))
}

// omtPage is op's page n of the account-wide listing of kind.
func omtPage(op, kind, n string, next *string) any {
	id := kind + "_" + n
	arn, name := sdkaws.String(omtItemARN(kind, n)), sdkaws.String("name "+id)
	switch op {
	case "ListConfigurations":
		return &omics.ListConfigurationsOutput{Items: []omicstypes.ConfigurationListItem{{Arn: arn, Name: name}}, NextToken: next}
	case "ListRunGroups":
		return &omics.ListRunGroupsOutput{Items: []omicstypes.RunGroupListItem{{Arn: arn, Id: &id, Name: name}}, NextToken: next}
	case "ListSequenceStores":
		return &omics.ListSequenceStoresOutput{SequenceStores: []omicstypes.SequenceStoreDetail{{Arn: arn, Id: &id, Name: name}}, NextToken: next}
	case "ListVariantStores":
		return &omics.ListVariantStoresOutput{VariantStores: []omicstypes.VariantStoreItem{{StoreArn: arn, Id: &id, Name: name}}, NextToken: next}
	case "ListRunCaches":
		items := []omicstypes.RunCacheListItem{{Arn: arn, Id: &id, Name: name}}
		if n == "b" {
			items = append(items, omicstypes.RunCacheListItem{Arn: sdkaws.String(omtGovRunCache()), Id: sdkaws.String("runCache_gov_t"), Name: sdkaws.String("name runCache_gov_t")})
		}
		return &omics.ListRunCachesOutput{Items: items, NextToken: next}
	case "ListAnnotationStores":
		return &omics.ListAnnotationStoresOutput{AnnotationStores: []omicstypes.AnnotationStoreItem{{StoreArn: arn, Id: &id, Name: name}}, NextToken: next}
	case "ListReferenceStores":
		return &omics.ListReferenceStoresOutput{ReferenceStores: []omicstypes.ReferenceStoreDetail{{Arn: arn, Id: &id, Name: name}}, NextToken: next}
	default:
		return &omics.ListWorkflowsOutput{Items: []omicstypes.WorkflowListItem{{Arn: arn, Id: &id, Name: name}}, NextToken: next}
	}
}

// omtSuffix is the page letter a fixture id ends with.
func omtSuffix(id string) string { return id[strings.LastIndex(id, "_")+1:] }

func omtShare(id, resourceARN string) omicstypes.ShareDetails {
	return omicstypes.ShareDetails{
		ShareId: sdkaws.String(id), ShareName: sdkaws.String("share-" + id), ResourceArn: sdkaws.String(resourceARN),
		Status: omicstypes.ShareStatusActive, CreationTime: sdkaws.Time(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)), OwnerId: sdkaws.String(testAccountID),
	}
}

// omtHandlers answers every op the scan calls with the fixture (omtRows).
func omtHandlers(c *omtCalls) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListAnnotationStoreVersions": c.answer("ListAnnotationStoreVersions", func(in any) any {
			v := in.(*omics.ListAnnotationStoreVersionsInput)
			return &omics.ListAnnotationStoreVersionsOutput{AnnotationStoreVersions: []omicstypes.AnnotationStoreVersionItem{{
				VersionArn: sdkaws.String(omtAnnotationVersion(omtSuffix(sv(v.Name)))), VersionName: sdkaws.String("v1"), Name: v.Name,
			}}}
		}),
		"ListReferences": c.answer("ListReferences", func(in any) any {
			n := omtSuffix(sv(in.(*omics.ListReferencesInput).ReferenceStoreId))
			return &omics.ListReferencesOutput{References: []omicstypes.ReferenceListItem{{
				Arn: sdkaws.String(omtReference(n)), Id: sdkaws.String("ref_" + n), Name: sdkaws.String("name ref_" + n),
			}}}
		}),
		"ListWorkflowVersions": c.answer("ListWorkflowVersions", func(in any) any {
			v := in.(*omics.ListWorkflowVersionsInput)
			return &omics.ListWorkflowVersionsOutput{Items: []omicstypes.WorkflowVersionListItem{{
				Arn: sdkaws.String(omtWorkflowVersion(omtSuffix(sv(v.WorkflowId)))), VersionName: sdkaws.String("v1"), WorkflowId: v.WorkflowId,
			}}}
		}),
		"ListShares": c.answer("ListShares", func(any) any {
			return &omics.ListSharesOutput{Shares: []omicstypes.ShareDetails{omtShare("a", omtItemARN("workflow", "t"))}}
		}),
	}
	for _, k := range omtKinds {
		h[k.op] = c.answer(k.op, func(in any) any {
			if omtToken(in) == nil {
				return omtPage(k.op, k.kind, "t", sdkaws.String("p2"))
			}
			return omtPage(k.op, k.kind, "b", nil)
		})
	}
	return h
}

// omtScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func omtScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, omtDenied)
	return scanOmics(ctx, acct, testRegion, st, testScanID)
}

func omtRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// omtListOps is every listing op the scan calls.
func omtListOps() []string {
	ops := []string{"ListAnnotationStoreVersions", "ListReferences", "ListWorkflowVersions", "ListShares"}
	for _, k := range omtKinds {
		ops = append(ops, k.op)
	}
	return ops
}

// Every tagged type stores the tags ListTagsForResource returns for its ARN,
// and nil for a resource without tags; every stored row but the share is
// sent to the tag op, the aws-us-gov run cache included. Both pages of every
// account-wide listing are stored, each listing asks for its op's page cap,
// and the scan reports no error or warning.
func TestScanOmics_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newOMTCalls()

	total, inserted, err := omtScan(context.Background(), st, omtHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := omtRows()
	if total != len(rows) || inserted != len(rows) || len(rows) != 24 {
		t.Errorf("total, inserted = %d, %d; want %d (24 fixture rows)", total, inserted, len(rows))
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantTagARNs []string
	for _, want := range rows {
		r, ok := omtRowOf(t, st, want.typ, want.arn)
		if !ok {
			t.Errorf("%s %s not stored", want.typ, want.arn)
			continue
		}
		if sv(r.Name) != want.name {
			t.Errorf("%s %s Name = %q; want %q", want.typ, want.arn, sv(r.Name), want.name)
		}
		if want.typ == TypeOmicsShare {
			continue
		}
		wantTagARNs = append(wantTagARNs, want.arn)
		if omtTagged(want.arn) {
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": want.typ})
		} else if r.TagsJSON != nil {
			t.Errorf("%s %s TagsJSON = %s; want nil", want.typ, want.arn, *r.TagsJSON)
		}
	}
	got := slices.Clone(calls.tagARNs)
	slices.Sort(got)
	slices.Sort(wantTagARNs)
	if !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantTagARNs)
	}
	for _, op := range omtListOps() {
		want := int32(100)
		switch op {
		case "ListConfigurations":
			want = 10
		case "ListShares":
			if caps := calls.maxResults[op]; len(caps) != 0 {
				t.Errorf("ListShares MaxResults = %v; want none sent", caps)
			}
			continue
		}
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
}

// A rescan stores the same rows without inserting any.
func TestScanOmics_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newOMTCalls()
	if _, _, err := omtScan(context.Background(), st, omtHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := omtScan(context.Background(), st, omtHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != 24 || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want 24, 0", total, inserted)
	}
}

// A failed tag read keeps the row without tags unless the resource is gone:
// a denial warns once under omics:ListTagsForResource, a throttle after
// retries is a transient error, any other code a hard one. A workflow the tag
// read no longer finds is dropped, and its versions are never listed. A
// reference, from a per-store listing, and the account's own aws-us-gov run
// cache behave alike.
func TestScanOmics_TagReadFailures(t *testing.T) {
	for _, target := range []omtRow{
		{typ: TypeOmicsWorkflow, arn: omtItemARN("workflow", "t")},
		{typ: TypeOmicsReference, arn: omtReference("t")},
		{typ: TypeOmicsRunCache, arn: omtGovRunCache()},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				kept      bool
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", omtDenied, true, 0, 1},
				{"not found", omtNotFound, false, 0, 0},
				{"other error", omtBadInput, true, outcomeError, 0},
				{"transient", omtThrottled, true, outcomeTransient, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, omicsListTagsOp)
					calls := newOMTCalls()
					calls.errs[target.arn] = c.err

					_, _, err := omtScan(context.Background(), st, omtHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.outcome == outcomeError {
						assertAPICode(t, err, "ValidationException")
					}
					r, ok := omtRowOf(t, st, target.typ, target.arn)
					if ok != c.kept {
						t.Fatalf("%s row stored = %v; want %v", target.typ, ok, c.kept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if target.typ == TypeOmicsWorkflow {
						_, child := omtRowOf(t, st, TypeOmicsWorkflowVersion, omtWorkflowVersion("t"))
						_, sibling := omtRowOf(t, st, TypeOmicsWorkflowVersion, omtWorkflowVersion("b"))
						if child != c.kept || !sibling {
							t.Errorf("version of workflow_t stored %v, of workflow_b %v; want workflow_t's only when it is kept, workflow_b's always", child, sibling)
						}
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", omicsListTagsOp, *warnings, c.wantWarns)
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

// A row another account owns is read too: its tags are stored, and a denial
// or a not-found answer keeps it untagged without a warning or an error. Any
// other error is still returned.
func TestScanOmics_OtherAccountRowTagRead(t *testing.T) {
	foreign := arnFor("aws", "omics", testRegion, omtOtherAccount, "runGroup/foreign")
	for _, c := range []struct {
		name    string
		err     error
		tags    map[string]string
		outcome serviceOutcome
	}{
		{"tagged", nil, map[string]string{"owner": "other"}, 0},
		{"denied", omtDenied, nil, 0},
		{"not found", omtNotFound, nil, 0},
		{"other error", omtBadInput, nil, outcomeError},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			calls := newOMTCalls()
			calls.errs[foreign] = c.err
			calls.tags[foreign] = c.tags
			h := omtHandlers(calls)
			h["ListRunGroups"] = calls.answer("ListRunGroups", func(any) any {
				return &omics.ListRunGroupsOutput{Items: []omicstypes.RunGroupListItem{{Arn: sdkaws.String(foreign), Id: sdkaws.String("foreign")}}}
			})

			_, _, err := omtScan(context.Background(), st, h)
			if c.outcome == 0 && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.outcome != 0 {
				assertGlueOutcome(t, err, c.outcome)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
			r, ok := omtRowOf(t, st, TypeOmicsRunGroup, foreign)
			if !ok {
				t.Fatal("other account's run group not stored")
			}
			if c.tags != nil {
				assertTagsMap(t, r.TagsJSON, c.tags)
			} else if r.TagsJSON != nil {
				t.Errorf("TagsJSON = %s; want nil", *r.TagsJSON)
			}
		})
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanOmics_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, omicsListTagsOp)
	calls := newOMTCalls()
	calls.errs[omtItemARN("configuration", "t")] = omtThrottled
	calls.errs[omtWorkflowVersion("b")] = omtBadInput

	_, _, err := omtScan(context.Background(), st, omtHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", omicsListTagsOp, *warns)
	}
}

// A denied listing warns once under its own op and costs no other phase its
// rows.
func TestScanOmics_DeniedListingWarnsUnderItsOp(t *testing.T) {
	for _, op := range omtListOps() {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, "omics:"+op)
			h := omtHandlers(newOMTCalls())
			h[op] = func(any) (any, error) { return nil, omtDenied }

			total, _, err := omtScan(context.Background(), st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || *opWarns != 1 {
				t.Errorf("warnings = %d (omics:%s: %d); want 1 under the op", *warns, op, *opWarns)
			}
			if stored := countStoredRows(t, st); total == 0 || total != stored {
				t.Errorf("total = %d, stored rows = %d; want the other phases' rows counted", total, stored)
			}
		})
	}
}

// A region that does not offer HealthOmics answers "Unable to determine
// service/operation name": on a listing's first page, while no listing
// answered with an item or an error, the scan reports the region
// unavailable and runs no later listing. After rows were stored the
// AccessDeniedException form is dropped silently and the NotFoundException
// form is an error; the rows are kept either way.
func TestScanOmics_RegionUnavailable(t *testing.T) {
	notFoundForm := apiErr("NotFoundException", "Unable to determine service/operation name to be authorized")
	for _, c := range []struct {
		name, op     string
		err          error
		emptyEarlier bool
	}{
		{"first listing", "ListConfigurations", omtNoRegion, false},
		{"first listing, NotFoundException form", "ListConfigurations", notFoundForm, false},
		{"later listing, nothing stored", "ListRunGroups", omtNoRegion, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			calls := newOMTCalls()
			h := omtHandlers(calls)
			if c.emptyEarlier {
				h["ListConfigurations"] = func(any) (any, error) { return &omics.ListConfigurationsOutput{}, nil }
			}
			h[c.op] = func(any) (any, error) { return nil, c.err }

			total, _, err := omtScan(context.Background(), st, h)
			assertGlueOutcome(t, err, outcomeUnavailable)
			if !errors.Is(err, errServiceUnavailable) {
				t.Errorf("err = %v; want errServiceUnavailable", err)
			}
			if total != 0 || *warns != 0 {
				t.Errorf("total = %d, warnings = %d; want 0, 0", total, *warns)
			}
			if n := calls.count("ListSequenceStores"); n != 0 {
				t.Errorf("ListSequenceStores called %d times after the region said unavailable", n)
			}
		})
	}
	for _, c := range []struct {
		name string
		err  error
		code string
	}{
		{"after rows, AccessDeniedException form", omtNoRegion, ""},
		{"after rows, NotFoundException form", notFoundForm, "NotFoundException"},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			h := omtHandlers(newOMTCalls())
			h["ListShares"] = func(any) (any, error) { return nil, c.err }

			total, _, err := omtScan(context.Background(), st, h)
			if c.code == "" && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if c.code != "" {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, c.code)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
			if total != 23 {
				t.Errorf("total = %d; want 23 (every fixture row but the share)", total)
			}
		})
	}
}

// The not-available answer on a later page, after an empty first page,
// follows an answer that shows the service served here: it is not the region
// gate, so the scan is not reported unavailable, and the AccessDeniedException
// form is dropped silently while every other phase runs.
func TestScanOmics_RegionUnavailableOnLaterPage(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newOMTCalls()
	h := omtHandlers(calls)
	h["ListConfigurations"] = func(in any) (any, error) {
		if omtToken(in) == nil {
			return &omics.ListConfigurationsOutput{NextToken: sdkaws.String("p2")}, nil
		}
		return nil, omtNoRegion
	}

	total, _, err := omtScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if total != 22 || *warns != 0 {
		t.Errorf("total = %d, warnings = %d; want 22 (the fixture less both configurations), 0", total, *warns)
	}
}

// ListWorkflowVersions is sent a READY2RUN workflow's Type and no owner, a
// shared PRIVATE workflow another account owns that account as
// WorkflowOwnerId and no Type, and an own PRIVATE workflow neither.
func TestScanOmics_WorkflowVersionsOwnerContext(t *testing.T) {
	st := newTestStore(t)
	h := omtHandlers(newOMTCalls())
	h["ListWorkflows"] = func(any) (any, error) {
		return &omics.ListWorkflowsOutput{Items: []omicstypes.WorkflowListItem{
			{Arn: sdkaws.String(omtItemARN("workflow", "t")), Id: sdkaws.String("own"), Type: omicstypes.WorkflowTypePrivate},
			{Arn: sdkaws.String(arnFor("aws", "omics", testRegion, omtOtherAccount, "workflow/8")), Id: sdkaws.String("shared"), Type: omicstypes.WorkflowTypePrivate},
			{Arn: sdkaws.String(arnFor("aws", "omics", testRegion, "999999999999", "workflow/9")), Id: sdkaws.String("ready2run"), Type: omicstypes.WorkflowTypeReady2run},
		}}, nil
	}
	var mu sync.Mutex
	sent := map[string]*omics.ListWorkflowVersionsInput{}
	h["ListWorkflowVersions"] = func(in any) (any, error) {
		v := in.(*omics.ListWorkflowVersionsInput)
		mu.Lock()
		sent[sv(v.WorkflowId)] = v
		mu.Unlock()
		return &omics.ListWorkflowVersionsOutput{}, nil
	}

	if _, _, err := omtScan(context.Background(), st, h); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	for id, want := range map[string]struct {
		owner string
		typ   omicstypes.WorkflowType
	}{
		"own":       {"", ""},
		"shared":    {omtOtherAccount, ""},
		"ready2run": {"", omicstypes.WorkflowTypeReady2run},
	} {
		in := sent[id]
		if in == nil {
			t.Errorf("ListWorkflowVersions not sent for %s (sent for %v)", id, slices.Sorted(maps.Keys(sent)))
			continue
		}
		if sv(in.WorkflowOwnerId) != want.owner || in.Type != want.typ {
			t.Errorf("%s workflow sent owner %q, type %q; want %q, %q", id, sv(in.WorkflowOwnerId), in.Type, want.owner, want.typ)
		}
	}
}

// omtOtherOwnerCase is a per-parent listing whose parents include some
// another account owns, under ARN kind: fa and fb, which it no longer finds,
// and fc, whose first page answers one row and whose second it no longer
// finds when partial, else whose first page it no longer finds.
type omtOtherOwnerCase struct {
	parentOp, op, kind, childType, ownChild string
	parents                                 func(own string, foreign map[string]string) any
	key                                     func(in any) string
	page                                    func(childARN string, next *string) any
}

func omtOtherOwnerCases() []omtOtherOwnerCase {
	return []omtOtherOwnerCase{
		{
			"ListWorkflows", "ListWorkflowVersions", "workflow", TypeOmicsWorkflowVersion, omtWorkflowVersion("t"),
			func(own string, foreign map[string]string) any {
				items := []omicstypes.WorkflowListItem{{Arn: sdkaws.String(own), Id: sdkaws.String("workflow_t")}}
				for _, id := range []string{"fa", "fb", "fc"} {
					items = append(items, omicstypes.WorkflowListItem{Arn: sdkaws.String(foreign[id]), Id: sdkaws.String(id)})
				}
				return &omics.ListWorkflowsOutput{Items: items}
			},
			func(in any) string { return sv(in.(*omics.ListWorkflowVersionsInput).WorkflowId) },
			func(childARN string, next *string) any {
				return &omics.ListWorkflowVersionsOutput{Items: []omicstypes.WorkflowVersionListItem{{Arn: sdkaws.String(childARN), VersionName: sdkaws.String("v1")}}, NextToken: next}
			},
		},
		{
			"ListAnnotationStores", "ListAnnotationStoreVersions", "annotationStore", TypeOmicsAnnotationStoreVersion, omtAnnotationVersion("t"),
			func(own string, foreign map[string]string) any {
				items := []omicstypes.AnnotationStoreItem{{StoreArn: sdkaws.String(own), Name: sdkaws.String("name annotationStore_t")}}
				for _, id := range []string{"fa", "fb", "fc"} {
					items = append(items, omicstypes.AnnotationStoreItem{StoreArn: sdkaws.String(foreign[id]), Name: sdkaws.String(id)})
				}
				return &omics.ListAnnotationStoresOutput{AnnotationStores: items}
			},
			func(in any) string { return sv(in.(*omics.ListAnnotationStoreVersionsInput).Name) },
			func(childARN string, next *string) any {
				return &omics.ListAnnotationStoreVersionsOutput{AnnotationStoreVersions: []omicstypes.AnnotationStoreVersionItem{{VersionArn: sdkaws.String(childARN), VersionName: sdkaws.String("v1")}}, NextToken: next}
			},
		},
	}
}

// omtOtherOwnerHandlers answers c's parent listing with the own "_t" parent
// and fa, fb and fc, and its child listing as omtOtherOwnerCase says. It
// returns the ARN of fc's child.
func omtOtherOwnerHandlers(c omtOtherOwnerCase, partial bool) (map[string]func(any) (any, error), string) {
	h := omtHandlers(newOMTCalls())
	kind := c.kind
	foreign := map[string]string{}
	for _, id := range []string{"fa", "fb", "fc"} {
		foreign[id] = arnFor("aws", "omics", testRegion, omtOtherAccount, kind+"/"+id)
	}
	fcChild := foreign["fc"] + "/version/v1"
	h[c.parentOp] = func(any) (any, error) { return c.parents(omtItemARN(kind, "t"), foreign), nil }
	next := h[c.op]
	h[c.op] = func(in any) (any, error) {
		switch c.key(in) {
		case "fa", "fb":
			return nil, omtNotFound
		case "fc":
			if partial && omtToken(in) == nil {
				return c.page(fcChild, sdkaws.String("p2")), nil
			}
			return nil, omtNotFound
		}
		return next(in)
	}
	return h, fcChild
}

// A per-parent listing that no longer finds parents another account owns
// warns once under its op, since the parent listing had just returned them;
// the rows read before such an answer (partial) and the own parents'
// children are stored, and the scan reports no error.
func TestScanOmics_OtherOwnerChildNotFoundWarns(t *testing.T) {
	for _, c := range omtOtherOwnerCases() {
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/partial=%v", c.op, partial), func(t *testing.T) {
				st := newTestStore(t)
				warns := countWarnings(st)
				opWarns := countOpWarnings(st, "omics:"+c.op)
				h, fcChild := omtOtherOwnerHandlers(c, partial)

				if _, _, err := omtScan(context.Background(), st, h); err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if *warns != 1 || *opWarns != 1 {
					t.Errorf("warnings = %d (omics:%s: %d); want 1 under the op", *warns, c.op, *opWarns)
				}
				if _, ok := omtRowOf(t, st, c.childType, c.ownChild); !ok {
					t.Errorf("%s missing; want the own parent's child stored", c.ownChild)
				}
				if _, ok := omtRowOf(t, st, c.childType, fcChild); ok != partial {
					t.Errorf("%s stored = %v; want %v", fcChild, ok, partial)
				}
			})
		}
	}
}

// When the child rows' write fails, the not-found parents another account
// owns are not warned about, and the write error is returned.
func TestScanOmics_OtherOwnerChildNotFoundFailedWrite(t *testing.T) {
	for _, c := range omtOtherOwnerCases() {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			refuseStoreWrites(t, st, c.childType)
			h, _ := omtOtherOwnerHandlers(c, true)

			_, _, err := omtScan(context.Background(), st, h)
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
		})
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanOmics_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeOmicsConfiguration)
	calls := newOMTCalls()
	calls.errs[omtItemARN("configuration", "t")] = omtDenied

	_, _, err := omtScan(context.Background(), st, omtHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, op := range []string{"ListRunGroups", "ListWorkflows", "ListShares"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write", op, n)
		}
	}
}

// A listing's failed page keeps the pages read before it, and no listing's
// failure stops the phases after it. A store a per-store listing no longer
// finds is skipped silently, its sibling's rows stored.
func TestScanOmics_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newOMTCalls()
	h := omtHandlers(calls)
	h["ListWorkflows"] = failOnCall(2, apiErr("ConflictException", "page 2"), h["ListWorkflows"])
	h["ListRunGroups"] = func(any) (any, error) { return nil, omtBadInput }
	refs := h["ListReferences"]
	h["ListReferences"] = func(in any) (any, error) {
		if sv(in.(*omics.ListReferencesInput).ReferenceStoreId) == "referenceStore_t" {
			return nil, omtNotFound
		}
		return refs(in)
	}

	_, _, err := omtScan(context.Background(), st, h)
	for _, code := range []string{"ConflictException", "ValidationException"} {
		if !joinCarriesAPIErrorCode(err, code) {
			t.Errorf("err = %v; want it to carry %s", err, code)
		}
	}
	if joinCarriesAPIErrorCode(err, "ResourceNotFoundException") {
		t.Errorf("err = %v; want the store ListReferences no longer finds skipped", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, want := range []omtRow{
		{typ: TypeOmicsWorkflow, arn: omtItemARN("workflow", "t")},
		{typ: TypeOmicsWorkflowVersion, arn: omtWorkflowVersion("t")},
		{typ: TypeOmicsReference, arn: omtReference("b")},
		{typ: TypeOmicsShare, arn: omtShareID()},
	} {
		if _, ok := omtRowOf(t, st, want.typ, want.arn); !ok {
			t.Errorf("%s %s missing", want.typ, want.arn)
		}
	}
	for _, gone := range []omtRow{
		{typ: TypeOmicsWorkflow, arn: omtItemARN("workflow", "b")},
		{typ: TypeOmicsReference, arn: omtReference("t")},
		{typ: TypeOmicsRunGroup, arn: omtItemARN("runGroup", "t")},
	} {
		if _, ok := omtRowOf(t, st, gone.typ, gone.arn); ok {
			t.Errorf("%s %s stored; want it not read", gone.typ, gone.arn)
		}
	}
}

// A parent each per-parent listing no longer finds (ResourceNotFoundException,
// which every one of them models) was deleted since it was listed: it is
// skipped without an error or a warning, and its sibling's children are
// stored.
func TestScanOmics_ChildListingParentGone(t *testing.T) {
	for _, c := range []struct {
		op, typ        string
		goneT, storedB string
		parentOfT      func(in any) string
		keyOfT         string
	}{
		{
			"ListAnnotationStoreVersions", TypeOmicsAnnotationStoreVersion, omtAnnotationVersion("t"), omtAnnotationVersion("b"),
			func(in any) string { return sv(in.(*omics.ListAnnotationStoreVersionsInput).Name) }, "name annotationStore_t",
		},
		{
			"ListReferences", TypeOmicsReference, omtReference("t"), omtReference("b"),
			func(in any) string { return sv(in.(*omics.ListReferencesInput).ReferenceStoreId) }, "referenceStore_t",
		},
		{
			"ListWorkflowVersions", TypeOmicsWorkflowVersion, omtWorkflowVersion("t"), omtWorkflowVersion("b"),
			func(in any) string { return sv(in.(*omics.ListWorkflowVersionsInput).WorkflowId) }, "workflow_t",
		},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			h := omtHandlers(newOMTCalls())
			next := h[c.op]
			h[c.op] = func(in any) (any, error) {
				if c.parentOfT(in) == c.keyOfT {
					return nil, omtNotFound
				}
				return next(in)
			}

			if _, _, err := omtScan(context.Background(), st, h); err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 0 {
				t.Errorf("warnings = %d; want 0", *warns)
			}
			rows := rowsByNativeID(t, st, c.typ)
			if _, ok := rows[c.goneT]; ok {
				t.Errorf("%s stored; want its parent skipped", c.goneT)
			}
			if _, ok := rows[c.storedB]; !ok {
				t.Errorf("%s missing; want the sibling's children stored", c.storedB)
			}
		})
	}
}

// Items listed without the ARN their NativeID is, from an account-wide or a
// per-parent listing, and an annotation store without the Name its versions
// are listed by, are not stored or not read, and are warned about once
// under the listing's op.
func TestScanOmics_UnstorableItemsWarn(t *testing.T) {
	for _, op := range []string{"ListConfigurations", "ListAnnotationStores", "ListReferences", "ListWorkflowVersions", "ListAnnotationStoreVersions", "ListShares"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, "omics:"+op)
			h := omtHandlers(newOMTCalls())
			h[op] = func(any) (any, error) {
				switch op {
				case "ListConfigurations":
					return &omics.ListConfigurationsOutput{Items: []omicstypes.ConfigurationListItem{{}, {}}}, nil
				case "ListAnnotationStores":
					return &omics.ListAnnotationStoresOutput{AnnotationStores: []omicstypes.AnnotationStoreItem{{StoreArn: sdkaws.String(omtItemARN("annotationStore", "t"))}}}, nil
				case "ListReferences":
					return &omics.ListReferencesOutput{References: []omicstypes.ReferenceListItem{{}}}, nil
				case "ListWorkflowVersions":
					return &omics.ListWorkflowVersionsOutput{Items: []omicstypes.WorkflowVersionListItem{{}}}, nil
				case "ListAnnotationStoreVersions":
					return &omics.ListAnnotationStoreVersionsOutput{AnnotationStoreVersions: []omicstypes.AnnotationStoreVersionItem{{}}}, nil
				default:
					return &omics.ListSharesOutput{Shares: []omicstypes.ShareDetails{{ShareId: sdkaws.String("x")}}}, nil
				}
			}

			if _, _, err := omtScan(context.Background(), st, h); err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || *opWarns != 1 {
				t.Errorf("warnings = %d (omics:%s: %d); want 1 under the op", *warns, op, *opWarns)
			}
		})
	}
}

// Shares are listed for this account's own resources only; a DELETED share
// is not stored, and no share's tags are read.
func TestScanOmics_SharesOwnLiveOnly(t *testing.T) {
	st := newTestStore(t)
	calls := newOMTCalls()
	h := omtHandlers(calls)
	var owners []omicstypes.ResourceOwner
	revoked := omtShare("d", omtItemARN("workflow", "b"))
	revoked.Status = omicstypes.ShareStatusDeleted
	h["ListShares"] = func(in any) (any, error) {
		owners = append(owners, in.(*omics.ListSharesInput).ResourceOwner)
		return &omics.ListSharesOutput{Shares: []omicstypes.ShareDetails{omtShare("a", omtItemARN("workflow", "t")), revoked}}, nil
	}

	if _, _, err := omtScan(context.Background(), st, h); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	shares := rowsByNativeID(t, st, TypeOmicsShare)
	r, ok := shares[omtShareID()]
	if !ok || len(shares) != 1 {
		t.Fatalf("shares stored = %v; want only %s", shares, omtShareID())
	}
	if sv(r.Status) != "ACTIVE" || r.CreatedAt == nil {
		t.Errorf("share Status = %q, CreatedAt = %v; want ACTIVE and set", sv(r.Status), r.CreatedAt)
	}
	if !slices.Equal(owners, []omicstypes.ResourceOwner{omicstypes.ResourceOwnerSelf}) {
		t.Errorf("ListShares ResourceOwner = %v; want [SELF]", owners)
	}
	if slices.Contains(calls.tagARNs, omtShareID()) {
		t.Error("share sent to ListTagsForResource; want no tag read")
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanOmics_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	h := omtHandlers(newOMTCalls())
	h["ListRunCaches"] = func(any) (any, error) {
		return omtPage("ListRunCaches", "runCache", "t", sdkaws.String("same")), nil
	}

	_, _, err := omtScan(context.Background(), st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if _, ok := omtRowOf(t, st, TypeOmicsRunCache, omtItemARN("runCache", "t")); !ok {
		t.Error("run cache from the pages read missing")
	}
}

// Once ctx ends no further phase starts, and the stop is reported once
// although the phase in flight saw it in its listing.
func TestScanOmics_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newOMTCalls()
	h := omtHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	variants := h["ListVariantStores"]
	h["ListVariantStores"] = func(in any) (any, error) {
		if omtToken(in) != nil {
			cancel()
			return nil, context.Canceled
		}
		return variants(in)
	}

	_, _, err := omtScan(ctx, st, h)
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
	if n := calls.count("ListRunCaches"); n != 0 {
		t.Errorf("ListRunCaches called %d times after ctx ended", n)
	}
}

// An NXDOMAIN on every listing reports the region unavailable. An NXDOMAIN
// on the first listing of the storage or the analytics host alone runs no
// later listing of that host, is dropped, and costs the other hosts nothing.
func TestScanOmics_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "workflows-omics.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("every host", func(t *testing.T) {
		st := newTestStore(t)
		h := omtHandlers(newOMTCalls())
		for _, op := range omtListOps() {
			h[op] = func(any) (any, error) { return nil, nx }
		}
		total, _, err := omtScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 {
			t.Errorf("total = %d; want 0", total)
		}
	})
	for _, c := range []struct {
		host        string
		first       string
		notCalled   []string
		storedTotal int
	}{
		{"storage", "ListSequenceStores", []string{"ListReferenceStores", "ListReferences"}, 18},
		{"analytics", "ListVariantStores", []string{"ListAnnotationStores", "ListAnnotationStoreVersions", "ListShares"}, 17},
	} {
		t.Run(c.host, func(t *testing.T) {
			st := newTestStore(t)
			calls := newOMTCalls()
			h := omtHandlers(calls)
			h[c.first] = func(any) (any, error) { return nil, nx }
			total, _, err := omtScan(context.Background(), st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			for _, op := range c.notCalled {
				if n := calls.count(op); n != 0 {
					t.Errorf("%s called %d times on a host that is not served", op, n)
				}
			}
			if total != c.storedTotal {
				t.Errorf("total = %d; want %d", total, c.storedTotal)
			}
		})
	}
}
