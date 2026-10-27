package aws

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/datasync"
	dstypes "github.com/aws/aws-sdk-go-v2/service/datasync/types"
	"github.com/icearp/disco-cli/store"
)

var (
	dsyDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: datasync:List")
	dsyBadInput = apiErr("InvalidRequestException", "bad")
	dsyThrottle = apiErr("ThrottlingException", "slow down")
)

const dsyListTagsOp = "datasync:ListTagsForResource"

// dsyNames is the two resources each listing of the fixture serves, "tagged"
// on its first page and "bare" on its second; ListTagsForResource answers
// with two pages of tags for every resource whose ARN ends in "tagged".
var dsyNames = []string{"tagged", "bare"}

func dsyARN(kind, id string) string {
	return "arn:aws:datasync:" + testRegion + ":" + testAccountID + ":" + kind + "/" + id
}

// dsyLocation is one location kind of the fixture: its type, the LocationUri
// scheme the listing returns and the Describe op that reads it.
type dsyLocation struct{ typ, scheme, describe string }

var dsyLocations = []dsyLocation{
	{TypeDataSyncLocationEFS, "efs", "DescribeLocationEfs"},
	{TypeDataSyncLocationNFS, "nfs", "DescribeLocationNfs"},
	{TypeDataSyncLocationSMB, "smb", "DescribeLocationSmb"},
	{TypeDataSyncLocationS3, "s3", "DescribeLocationS3"},
	{TypeDataSyncLocationHDFS, "hdfs", "DescribeLocationHdfs"},
	{TypeDataSyncLocationAzureBlob, "azure-blob", "DescribeLocationAzureBlob"},
	{TypeDataSyncLocationObjectStorage, "object-storage", "DescribeLocationObjectStorage"},
	{TypeDataSyncLocationFSxLustre, "fsxl", "DescribeLocationFsxLustre"},
	{TypeDataSyncLocationFSxONTAP, "fsxn", "DescribeLocationFsxOntap"},
	{TypeDataSyncLocationFSxOpenZFS, "fsxz", "DescribeLocationFsxOpenZfs"},
	{TypeDataSyncLocationFSxWindows, "fsxw", "DescribeLocationFsxWindows"},
}

func dsyLocationARN(scheme, name string) string { return dsyARN("location", "loc-"+scheme+"-"+name) }

// dsyRow is one fixture row: its type and NativeID.
type dsyRow struct{ typ, arn string }

func dsyRows() []dsyRow {
	var rows []dsyRow
	for _, n := range dsyNames {
		rows = append(rows, dsyRow{TypeDataSyncAgent, dsyARN("agent", n)}, dsyRow{TypeDataSyncTask, dsyARN("task", n)})
		for _, l := range dsyLocations {
			rows = append(rows, dsyRow{l.typ, dsyLocationARN(l.scheme, n)})
		}
	}
	return rows
}

// dsyCalls records the stubbed DataSync calls (bacCalls), the ARNs each
// Describe op is sent, and answers the Describes from describeErrs, keyed by
// ARN.
type dsyCalls struct {
	*bacCalls
	described    map[string][]string
	describeErrs map[string]error
}

func newDSYCalls() *dsyCalls {
	c := &dsyCalls{bacCalls: newBACCalls(), described: map[string][]string{}, describeErrs: map[string]error{}}
	for _, r := range dsyRows() {
		if strings.HasSuffix(r.arn, "tagged") {
			c.tags[r.arn] = map[string]string{"type": r.typ}
		}
	}
	return c
}

// listTags answers a tagged resource with its type tag on the first page and
// a "page" tag on the second, and records each ARN once, on its first page.
func (c *dsyCalls) listTags(in any) (any, error) {
	c.called("ListTagsForResource", in)
	req := in.(*datasync.ListTagsForResourceInput)
	arn := sv(req.ResourceArn)
	c.mu.Lock()
	if req.NextToken == nil {
		c.tagARNs = append(c.tagARNs, arn)
	}
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if len(tags) == 0 {
		return &datasync.ListTagsForResourceOutput{}, nil
	}
	if req.NextToken == nil {
		var page []dstypes.TagListEntry
		for k, v := range tags {
			page = append(page, dstypes.TagListEntry{Key: sdkaws.String(k), Value: sdkaws.String(v)})
		}
		return &datasync.ListTagsForResourceOutput{Tags: page, NextToken: sdkaws.String("t2")}, nil
	}
	return &datasync.ListTagsForResourceOutput{Tags: []dstypes.TagListEntry{{Key: sdkaws.String("page"), Value: sdkaws.String("2")}}}, nil
}

// describe records op's call with the ARN in field and answers it with out,
// or with the ARN's describeErrs.
func (c *dsyCalls) describe(op, field string, out func(arn *string) any) func(any) (any, error) {
	return func(in any) (any, error) {
		arn := bacField[*string](in, field)
		c.mu.Lock()
		c.described[op] = append(c.described[op], sv(arn))
		err := c.describeErrs[sv(arn)]
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return out(arn), nil
	}
}

// twoPages answers op with "tagged" on the first page and "bare" on the
// second.
func (c *dsyCalls) twoPages(op string, out func(name string, next *string) any) func(any) (any, error) {
	return c.answer(op, func(in any) any {
		if bacField[*string](in, "NextToken") == nil {
			return out("tagged", sdkaws.String("p2"))
		}
		return out("bare", nil)
	})
}

// dsyHandlers answers every op the scan calls with the fixture (dsyRows).
func dsyHandlers(c *dsyCalls) map[string]func(any) (any, error) {
	s := sdkaws.String
	described := s("described")
	h := map[string]func(any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListAgents": c.twoPages("ListAgents", func(n string, next *string) any {
			return &datasync.ListAgentsOutput{Agents: []dstypes.AgentListEntry{{AgentArn: s(dsyARN("agent", n)), Name: s("agent " + n)}}, NextToken: next}
		}),
		"ListTasks": c.twoPages("ListTasks", func(n string, next *string) any {
			return &datasync.ListTasksOutput{Tasks: []dstypes.TaskListEntry{{TaskArn: s(dsyARN("task", n)), Name: s("task " + n)}}, NextToken: next}
		}),
		"ListLocations": c.twoPages("ListLocations", func(n string, next *string) any {
			var locs []dstypes.LocationListEntry
			for _, l := range dsyLocations {
				locs = append(locs, dstypes.LocationListEntry{LocationArn: s(dsyLocationARN(l.scheme, n)), LocationUri: s(l.scheme + "://" + n + "/dir/")})
			}
			return &datasync.ListLocationsOutput{Locations: locs, NextToken: next}
		}),
		"DescribeAgent": c.describe("DescribeAgent", "AgentArn", func(arn *string) any {
			return &datasync.DescribeAgentOutput{AgentArn: arn, Name: described}
		}),
		"DescribeTask": c.describe("DescribeTask", "TaskArn", func(arn *string) any {
			return &datasync.DescribeTaskOutput{TaskArn: arn, Name: described}
		}),
	}
	locationOut := map[string]func(arn *string) any{
		"DescribeLocationEfs": func(arn *string) any {
			return &datasync.DescribeLocationEfsOutput{LocationArn: arn, LocationUri: described}
		},
		"DescribeLocationNfs": func(arn *string) any {
			return &datasync.DescribeLocationNfsOutput{LocationArn: arn, LocationUri: described}
		},
		"DescribeLocationSmb": func(arn *string) any {
			return &datasync.DescribeLocationSmbOutput{LocationArn: arn, LocationUri: described}
		},
		"DescribeLocationS3": func(arn *string) any {
			return &datasync.DescribeLocationS3Output{LocationArn: arn, LocationUri: described}
		},
		"DescribeLocationHdfs": func(arn *string) any {
			return &datasync.DescribeLocationHdfsOutput{LocationArn: arn, LocationUri: described}
		},
		"DescribeLocationAzureBlob": func(arn *string) any {
			return &datasync.DescribeLocationAzureBlobOutput{LocationArn: arn, LocationUri: described}
		},
		"DescribeLocationObjectStorage": func(arn *string) any {
			return &datasync.DescribeLocationObjectStorageOutput{LocationArn: arn, LocationUri: described}
		},
		"DescribeLocationFsxLustre": func(arn *string) any {
			return &datasync.DescribeLocationFsxLustreOutput{LocationArn: arn, LocationUri: described}
		},
		"DescribeLocationFsxOntap": func(arn *string) any {
			return &datasync.DescribeLocationFsxOntapOutput{LocationArn: arn, LocationUri: described}
		},
		"DescribeLocationFsxOpenZfs": func(arn *string) any {
			return &datasync.DescribeLocationFsxOpenZfsOutput{LocationArn: arn, LocationUri: described}
		},
		"DescribeLocationFsxWindows": func(arn *string) any {
			return &datasync.DescribeLocationFsxWindowsOutput{LocationArn: arn, LocationUri: described}
		},
	}
	for op, out := range locationOut {
		h[op] = c.describe(op, "LocationArn", out)
	}
	return h
}

// dsyScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func dsyScan(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, dsyDenied)
	return scanDataSync(ctx, acct, testRegion, st, testScanID)
}

func dsyRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// Every type stores the tags ListTagsForResource returns for its ARN, both
// pages, and nil for a resource without tags; exactly those rows' ARNs are sent to it. Both pages
// of every listing are stored, every listing and tag read asks for the
// model's page cap, every row stores the body its kind's Describe op
// returned for its ARN, and the scan reports no error or warning.
func TestScanDataSync_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newDSYCalls()

	total, inserted, err := dsyScan(context.Background(), t, st, dsyHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 26 || inserted != 26 {
		t.Errorf("total, inserted = %d, %d; want 26, 26", total, inserted)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantARNs []string
	for _, w := range dsyRows() {
		r, ok := dsyRowOf(t, st, w.typ, w.arn)
		if !ok {
			t.Errorf("%s %s missing", w.typ, w.arn)
			continue
		}
		if !strings.Contains(r.AttributesJSON, `"described"`) {
			t.Errorf("%s %s attributes = %s; want its Describe body", w.typ, w.arn, r.AttributesJSON)
		}
		switch {
		case strings.HasSuffix(w.arn, "tagged"):
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": w.typ, "page": "2"})
		case r.TagsJSON != nil:
			t.Errorf("%s %s TagsJSON = %q; want nil", w.typ, w.arn, *r.TagsJSON)
		}
		wantARNs = append(wantARNs, w.arn)
	}
	slices.Sort(wantARNs)
	slices.Sort(calls.tagARNs)
	if !slices.Equal(calls.tagARNs, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", calls.tagARNs, wantARNs)
	}
	for _, op := range []string{"ListAgents", "ListTasks", "ListLocations", "ListTagsForResource"} {
		got := calls.maxResults[op]
		if len(got) == 0 {
			t.Errorf("%s never sent MaxResults", op)
		}
		for _, n := range got {
			if n != 100 {
				t.Errorf("%s MaxResults = %d; want 100", op, n)
			}
		}
	}
	for _, l := range dsyLocations {
		got := slices.Sorted(slices.Values(calls.described[l.describe]))
		if want := []string{dsyLocationARN(l.scheme, "bare"), dsyLocationARN(l.scheme, "tagged")}; !slices.Equal(got, want) {
			t.Errorf("%s ARNs = %v; want %v", l.describe, got, want)
		}
	}
	if a, _ := dsyRowOf(t, st, TypeDataSyncAgent, dsyARN("agent", "tagged")); sv(a.Name) != "agent tagged" {
		t.Errorf("agent Name = %q; want agent tagged", sv(a.Name))
	}
	if l, _ := dsyRowOf(t, st, TypeDataSyncLocationS3, dsyLocationARN("s3", "bare")); sv(l.Name) != "s3://bare/dir/" {
		t.Errorf("location Name = %q; want its LocationUri", sv(l.Name))
	}
}

// A rescan stores the same rows without inserting any.
func TestScanDataSync_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newDSYCalls()
	if _, _, err := dsyScan(context.Background(), t, st, dsyHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := dsyScan(context.Background(), t, st, dsyHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != 26 || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want 26, 0", total, inserted)
	}
}

// A failed tag read keeps the row without tags: a denial warns once under
// datasync:ListTagsForResource, a throttle after retries is a transient
// error, any other code a hard one. The op models no not-found code, so
// InvalidRequestException, which a deleted resource may also answer, is the
// hard case.
func TestScanDataSync_TagReadFailures(t *testing.T) {
	for _, target := range []dsyRow{
		{TypeDataSyncAgent, dsyARN("agent", "tagged")},
		{TypeDataSyncLocationS3, dsyLocationARN("s3", "tagged")},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", dsyDenied, 0, 1},
				{"other error", dsyBadInput, outcomeError, 0},
				{"transient", dsyThrottle, outcomeTransient, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, dsyListTagsOp)
					calls := newDSYCalls()
					calls.errs[target.arn] = c.err

					total, _, err := dsyScan(context.Background(), t, st, dsyHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.outcome == outcomeError {
						assertAPICode(t, err, "InvalidRequestException")
					}
					if total != 26 {
						t.Errorf("total = %d; want 26", total)
					}
					r, ok := dsyRowOf(t, st, target.typ, target.arn)
					if !ok {
						t.Fatal("row not stored; want it kept")
					}
					if r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", dsyListTagsOp, *warnings, c.wantWarns)
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

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanDataSync_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, dsyListTagsOp)
	calls := newDSYCalls()
	calls.errs[dsyARN("task", "tagged")] = dsyThrottle
	calls.errs[dsyLocationARN("nfs", "tagged")] = dsyBadInput

	_, _, err := dsyScan(context.Background(), t, st, dsyHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", dsyListTagsOp, *warns)
	}
}

// A failed Describe keeps the row as its list element, tags still read: a
// denial warns once under the Describe op of the row's kind, a throttle is a
// transient error, any other code a hard one.
func TestScanDataSync_DescribeFailures(t *testing.T) {
	targets := []struct{ typ, arn, op, listed string }{
		{TypeDataSyncAgent, dsyARN("agent", "tagged"), "datasync:DescribeAgent", `"Name":"agent tagged"`},
		{TypeDataSyncTask, dsyARN("task", "tagged"), "datasync:DescribeTask", `"Name":"task tagged"`},
	}
	for _, l := range dsyLocations {
		targets = append(targets, struct{ typ, arn, op, listed string }{
			l.typ, dsyLocationARN(l.scheme, "tagged"), "datasync:" + l.describe, `"LocationUri":"` + l.scheme + `://tagged/dir/"`,
		})
	}
	for _, target := range targets {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", dsyDenied, 0, 1},
				{"other error", dsyBadInput, outcomeError, 0},
				{"transient", dsyThrottle, outcomeTransient, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					all := countWarnings(st)
					warnings := bacWarnings(st, target.op)
					calls := newDSYCalls()
					calls.describeErrs[target.arn] = c.err

					_, _, err := dsyScan(context.Background(), t, st, dsyHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					r, ok := dsyRowOf(t, st, target.typ, target.arn)
					if !ok {
						t.Fatal("row not stored; want it kept as its list element")
					}
					if strings.Contains(r.AttributesJSON, "described") || !strings.Contains(r.AttributesJSON, target.listed) {
						t.Errorf("attributes = %s; want the list element", r.AttributesJSON)
					}
					assertTagsMap(t, r.TagsJSON, map[string]string{"type": target.typ, "page": "2"})
					if len(*warnings) != c.wantWarns || *all != c.wantWarns {
						t.Fatalf("%s warnings = %v (all ops %d); want %d", target.op, *warnings, *all, c.wantWarns)
					}
				})
			}
		})
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanDataSync_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeDataSyncTask)
	calls := newDSYCalls()
	calls.errs[dsyARN("task", "tagged")] = dsyDenied
	calls.describeErrs[dsyARN("task", "bare")] = dsyThrottle

	_, _, err := dsyScan(context.Background(), t, st, dsyHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	if n := calls.count("ListLocations"); n != 0 {
		t.Errorf("ListLocations called %d times after the failed write", n)
	}
}

// A failed location write sends no warning about a location Describe either:
// a throttled one is returned with the write failure, not demoted beside it.
func TestScanDataSync_FailedLocationWriteSendsNoDescribeWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeDataSyncLocationSMB)
	calls := newDSYCalls()
	calls.describeErrs[dsyLocationARN("hdfs", "bare")] = dsyThrottle

	_, _, err := dsyScan(context.Background(), t, st, dsyHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	assertAPICode(t, err, "ThrottlingException")
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
}

// A listing's failed page keeps the pages read before it, and no listing's
// failure stops the phases after it; a denied listing warns.
func TestScanDataSync_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, "datasync:ListTasks")
	calls := newDSYCalls()
	handlers := dsyHandlers(calls)
	handlers["ListAgents"] = failOnCall(2, dsyBadInput, handlers["ListAgents"])
	handlers["ListTasks"] = func(any) (any, error) { return nil, dsyDenied }

	total, _, err := dsyScan(context.Background(), t, st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidRequestException")
	if total != 23 {
		t.Errorf("total = %d; want 23 (one agent, every location)", total)
	}
	if _, ok := dsyRowOf(t, st, TypeDataSyncAgent, dsyARN("agent", "tagged")); !ok {
		t.Error("agent from the page before the failure missing")
	}
	if got := len(rowsByNativeID(t, st, TypeDataSyncLocationSMB)); got != 2 {
		t.Errorf("SMB locations stored = %d; want 2", got)
	}
	if len(*warnings) != 1 {
		t.Errorf("datasync:ListTasks warnings = %v; want 1", *warnings)
	}
}

// A repeated token ends the listing as an error, keeping its pages; a
// repeated token in a tag listing keeps the row without tags.
func TestScanDataSync_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	calls := newDSYCalls()
	handlers := dsyHandlers(calls)
	handlers["ListTasks"] = func(any) (any, error) {
		return &datasync.ListTasksOutput{Tasks: []dstypes.TaskListEntry{{TaskArn: sdkaws.String(dsyARN("task", "tagged"))}}, NextToken: sdkaws.String("same")}, nil
	}
	tags := handlers["ListTagsForResource"]
	handlers["ListTagsForResource"] = func(in any) (any, error) {
		if sv(in.(*datasync.ListTagsForResourceInput).ResourceArn) == dsyARN("agent", "tagged") {
			return &datasync.ListTagsForResourceOutput{NextToken: sdkaws.String("same")}, nil
		}
		return tags(in)
	}

	_, _, err := dsyScan(context.Background(), t, st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if _, ok := dsyRowOf(t, st, TypeDataSyncTask, dsyARN("task", "tagged")); !ok {
		t.Error("task from the pages read missing")
	}
	if a, ok := dsyRowOf(t, st, TypeDataSyncAgent, dsyARN("agent", "tagged")); !ok || a.TagsJSON != nil {
		t.Errorf("agent stored = %v, TagsJSON = %v; want kept without tags", ok, a.TagsJSON)
	}
}

// An element without an ARN, and a location whose URI scheme maps to no
// type, are warned about under the listing, not stored.
func TestScanDataSync_UnstorableItemsWarn(t *testing.T) {
	st := newTestStore(t)
	agentWarns := bacWarnings(st, "datasync:ListAgents")
	locWarns := bacWarnings(st, "datasync:ListLocations")
	handlers := dsyHandlers(newDSYCalls())
	handlers["ListAgents"] = func(any) (any, error) {
		return &datasync.ListAgentsOutput{Agents: []dstypes.AgentListEntry{{AgentArn: sdkaws.String(dsyARN("agent", "tagged"))}, {Name: sdkaws.String("unnamed")}}}, nil
	}
	handlers["ListLocations"] = func(any) (any, error) {
		return &datasync.ListLocationsOutput{Locations: []dstypes.LocationListEntry{
			{LocationArn: sdkaws.String(dsyLocationARN("s3", "tagged")), LocationUri: sdkaws.String("s3://tagged/")},
			{LocationArn: sdkaws.String(dsyLocationARN("new", "x")), LocationUri: sdkaws.String("new-kind://x/")},
		}}, nil
	}

	if _, _, err := dsyScan(context.Background(), t, st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := len(rowsByNativeID(t, st, TypeDataSyncAgent)); got != 1 {
		t.Errorf("agents stored = %d; want 1", got)
	}
	if len(*agentWarns) != 1 || !strings.HasSuffix((*agentWarns)[0].Message, "first unnamed") {
		t.Errorf("ListAgents warnings = %v; want one naming unnamed first", *agentWarns)
	}
	if len(*locWarns) != 1 || !strings.HasSuffix((*locWarns)[0].Message, "first "+dsyLocationARN("new", "x")) {
		t.Errorf("ListLocations warnings = %v; want one naming the unmapped location", *locWarns)
	}
}

// Once ctx ends no further phase starts, and the stop is reported once,
// naming the phases not run, although the phase in flight saw it.
func TestScanDataSync_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newDSYCalls()
	handlers := dsyHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agents := handlers["ListAgents"]
	handlers["ListAgents"] = func(in any) (any, error) {
		if in.(*datasync.ListAgentsInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return agents(in)
	}

	_, _, err := dsyScan(ctx, t, st, handlers)
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
	if n := calls.count("ListTasks") + calls.count("ListLocations"); n != 0 {
		t.Errorf("later listings called %d times after ctx ended", n)
	}
	if !strings.Contains(err.Error(), "datasync: 2 phases not run") {
		t.Errorf("err = %v; want it to say 2 phases not run", err)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanDataSync_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "datasync.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("later phase after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		handlers := dsyHandlers(newDSYCalls())
		handlers["ListLocations"] = func(any) (any, error) { return nil, nx }
		total, _, err := dsyScan(context.Background(), t, st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != 4 {
			t.Errorf("total = %d; want the 4 agent and task rows counted", total)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newDSYCalls()
		calls.errs[dsyARN("agent", "bare")] = nx
		_, _, err := dsyScan(context.Background(), t, st, dsyHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanDataSync(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
	})
}

// A throttled location Describe beside a hard tag error is demoted to one
// warning under that kind's Describe op, not under the listing.
func TestScanDataSync_LocationDescribeThrottleDemotedUnderItsOp(t *testing.T) {
	st := newTestStore(t)
	listWarns := countOpWarnings(st, "datasync:ListLocations")
	warns := countOpWarnings(st, "datasync:DescribeLocationNfs")
	calls := newDSYCalls()
	calls.describeErrs[dsyLocationARN("nfs", "bare")] = dsyThrottle
	calls.errs[dsyLocationARN("smb", "tagged")] = dsyBadInput

	_, _, err := dsyScan(context.Background(), t, st, dsyHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidRequestException")
	if *warns != 1 || *listWarns != 0 {
		t.Errorf("DescribeLocationNfs, ListLocations warnings = %d, %d; want 1, 0", *warns, *listWarns)
	}
}
