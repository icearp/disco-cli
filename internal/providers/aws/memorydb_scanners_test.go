package aws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"
	memorydbtypes "github.com/aws/aws-sdk-go-v2/service/memorydb/types"
	"github.com/icearp/disco-cli/store"
)

const (
	// memDBTestListed is the rows of the two-page listings: two per listing,
	// one per page.
	memDBTestListed = 2 * 8
	// memDBTestTotal adds the one multi-region parameter group.
	memDBTestTotal = memDBTestListed + 1
	memDBTestMRPG  = "arn:aws:memorydb::" + testAccountID + ":multiregionparametergroup/mrpg-1"
	// memDBTestInvalidARN is the bad-request code ListTags models.
	memDBTestInvalidARN = "InvalidARNFault"
)

var (
	memDBDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: memorydb:ListTags")
	memDBThrottle = apiErr("Throttling", "Rate exceeded")
	memDBInvalid  = apiErr(memDBTestInvalidARN, "bad arn")
)

// memDBKind is one listing whose rows have their tags read: the Describe op,
// the type it stores, the ARN resource type of its rows, the not-found code
// ListTags models for that type ("" for none), the status every element
// carries, and page, which builds the op's output holding one element named
// name under arn, with next.
type memDBKind struct {
	op, typ, arnType, notFound, status string
	page                               func(arn, name, next *string) any
}

func (k memDBKind) name(suffix string) string { return k.arnType + "-" + suffix }

// tagRead reports whether the scan reads the tags of k's rows: every kind but
// the reserved node.
func (k memDBKind) tagRead() bool { return k.typ != TypeMemoryDBReservedNode }

// region is the Region stored on k's rows: none for a multi-region cluster,
// whose ARN names none.
func (k memDBKind) region() string {
	if k.typ == TypeMemoryDBMultiRegionCluster {
		return ""
	}
	return testRegion
}

// arn is the row's ARN in the Service Reference's format: a multi-region
// cluster's carries no region.
func (k memDBKind) arn(suffix string) string {
	region := testRegion
	if k.typ == TypeMemoryDBMultiRegionCluster {
		region = ""
	}
	return "arn:aws:memorydb:" + region + ":" + testAccountID + ":" + k.arnType + "/" + k.name(suffix)
}

func memDBKinds() []memDBKind {
	return []memDBKind{
		{"DescribeACLs", TypeMemoryDBACL, "acl", "ACLNotFoundFault", "active", func(arn, name, next *string) any {
			return &memorydb.DescribeACLsOutput{NextToken: next, ACLs: []memorydbtypes.ACL{{ARN: arn, Name: name, Status: sdkaws.String("active")}}}
		}},
		{"DescribeClusters", TypeMemoryDBCluster, "cluster", "ClusterNotFoundFault", "available", func(arn, name, next *string) any {
			return &memorydb.DescribeClustersOutput{NextToken: next, Clusters: []memorydbtypes.Cluster{{ARN: arn, Name: name, Status: sdkaws.String("available")}}}
		}},
		{"DescribeMultiRegionClusters", TypeMemoryDBMultiRegionCluster, "multiregioncluster", "MultiRegionClusterNotFoundFault", "available", func(arn, name, next *string) any {
			return &memorydb.DescribeMultiRegionClustersOutput{NextToken: next, MultiRegionClusters: []memorydbtypes.MultiRegionCluster{{ARN: arn, MultiRegionClusterName: name, Status: sdkaws.String("available")}}}
		}},
		{"DescribeParameterGroups", TypeMemoryDBParameterGroup, "parametergroup", "ParameterGroupNotFoundFault", "", func(arn, name, next *string) any {
			return &memorydb.DescribeParameterGroupsOutput{NextToken: next, ParameterGroups: []memorydbtypes.ParameterGroup{{ARN: arn, Name: name, Family: sdkaws.String("memorydb_redis7")}}}
		}},
		{"DescribeReservedNodes", TypeMemoryDBReservedNode, "reservednode", "", "active", func(arn, name, next *string) any {
			return &memorydb.DescribeReservedNodesOutput{NextToken: next, ReservedNodes: []memorydbtypes.ReservedNode{{ARN: arn, ReservationId: name, State: sdkaws.String("active")}}}
		}},
		{"DescribeSnapshots", TypeMemoryDBSnapshot, "snapshot", "SnapshotNotFoundFault", "available", func(arn, name, next *string) any {
			return &memorydb.DescribeSnapshotsOutput{NextToken: next, Snapshots: []memorydbtypes.Snapshot{{ARN: arn, Name: name, Status: sdkaws.String("available")}}}
		}},
		{"DescribeSubnetGroups", TypeMemoryDBSubnetGroup, "subnetgroup", "SubnetGroupNotFoundFault", "", func(arn, name, next *string) any {
			return &memorydb.DescribeSubnetGroupsOutput{NextToken: next, SubnetGroups: []memorydbtypes.SubnetGroup{{ARN: arn, Name: name, VpcId: sdkaws.String("vpc-1")}}}
		}},
		{"DescribeUsers", TypeMemoryDBUser, "user", "UserNotFoundFault", "active", func(arn, name, next *string) any {
			return &memorydb.DescribeUsersOutput{NextToken: next, Users: []memorydbtypes.User{{ARN: arn, Name: name, Status: sdkaws.String("active"), AccessString: sdkaws.String("on ~* +@all")}}}
		}},
	}
}

func memDBKindOf(t *testing.T, op string) memDBKind {
	t.Helper()
	for _, k := range memDBKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no tagged MemoryDB listing", op)
	return memDBKind{}
}

// memDBCalls records the calls of a scan and answers ListTags with tags, or
// errs, by ARN.
type memDBCalls struct {
	mu      sync.Mutex
	ops     map[string]int
	inputs  map[string][]any
	tagARNs []string
	tags    map[string]map[string]string
	errs    map[string]error
}

// newMemDBCalls tags the "tagged" row of every kind with its type.
func newMemDBCalls() *memDBCalls {
	c := &memDBCalls{ops: map[string]int{}, inputs: map[string][]any{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range memDBKinds() {
		c.tags[k.arn("tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

func (c *memDBCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	c.inputs[op] = append(c.inputs[op], in)
}

func (c *memDBCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

func (c *memDBCalls) listTags(in any) (any, error) {
	arn := sv(in.(*memorydb.ListTagsInput).ResourceArn)
	c.record("ListTags", in)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var list []memorydbtypes.Tag
	for k, v := range tags {
		list = append(list, memorydbtypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &memorydb.ListTagsOutput{TagList: list}, nil
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second.
func (c *memDBCalls) twoPages(k memDBKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("t2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		arn, name := k.arn(suffix), k.name(suffix)
		return k.page(&arn, &name, next), nil
	}
}

// memDBHandlers answers every MemoryDB op the scan calls: each tagged kind in
// two pages and one multi-region parameter group.
func memDBHandlers(c *memDBCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"ListTags": c.listTags,
		"DescribeMultiRegionParameterGroups": func(in any) (any, error) {
			c.record("DescribeMultiRegionParameterGroups", in)
			return &memorydb.DescribeMultiRegionParameterGroupsOutput{MultiRegionParameterGroups: []memorydbtypes.MultiRegionParameterGroup{{
				ARN: sdkaws.String(memDBTestMRPG), Name: sdkaws.String("mrpg-1"), Family: sdkaws.String("valkey7"),
			}}}, nil
		},
	}
	for _, k := range memDBKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// memDBScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func memDBScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, memDBDenied)
	return scanMemoryDB(ctx, acct, testRegion, st, testScanID)
}

// memDBTagReadARNs is the ARN of every row whose tags the fixture's scan
// reads.
func memDBTagReadARNs() []string {
	var arns []string
	for _, k := range memDBKinds() {
		if k.tagRead() {
			arns = append(arns, k.arn("tagged"), k.arn("bare"))
		}
	}
	slices.Sort(arns)
	return arns
}

// Every tagged listing stores both its pages, each row under its ARN, named
// by its name (a reserved node by its reservation id), with its status and
// the listed element as attributes, and the tags ListTags returns for that
// ARN, nil for a resource without tags. Exactly those ARNs are sent to the
// tag op; no listing is sent a MaxResults. The reserved nodes and the
// multi-region parameter group are stored untagged and never sent to the tag
// op; a multi-region row has no Region. No error, no warning.
func TestScanMemoryDB_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newMemDBCalls()

	total, inserted, err := memDBScan(context.Background(), st, memDBHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != memDBTestTotal || inserted != memDBTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, memDBTestTotal, memDBTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range memDBKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %v; want 2", k.typ, nativeIDs(rows))
		}
		for _, suffix := range []string{"tagged", "bare"} {
			arn := k.arn(suffix)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			if sv(r.Name) != k.name(suffix) || sv(r.Region) != k.region() || sv(r.Status) != k.status || r.CreatedAt != nil || r.ManagedByProvider {
				t.Errorf("%s Name, Region, Status, CreatedAt, Managed = %q, %q, %q, %v, %v; want %q, %q, %q, nil, false",
					arn, sv(r.Name), sv(r.Region), sv(r.Status), r.CreatedAt, r.ManagedByProvider, k.name(suffix), k.region(), k.status)
			}
			if !strings.Contains(r.AttributesJSON, `"ARN":"`+arn+`"`) {
				t.Errorf("%s attrs = %s; want the listed element", arn, r.AttributesJSON)
			}
			if suffix == "tagged" && k.tagRead() {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		for _, in := range calls.inputs[k.op] {
			if n := bacField[*int32](in, "MaxResults"); n != nil {
				t.Errorf("%s MaxResults = %d; want none sent", k.op, *n)
			}
		}
		if n := calls.count(k.op); n != 2 {
			t.Errorf("%s calls = %d; want 2", k.op, n)
		}
	}
	mrpg, ok := rowsByNativeID(t, st, TypeMemoryDBMultiRegionParameterGroup)[memDBTestMRPG]
	if !ok || sv(mrpg.Name) != "mrpg-1" || mrpg.Region != nil || mrpg.TagsJSON != nil || mrpg.ManagedByProvider || !strings.Contains(mrpg.AttributesJSON, `"Family":"valkey7"`) {
		t.Errorf("multi-region parameter group stored %v: %+v; want mrpg-1, no Region, untagged, unmanaged, the listed element", ok, mrpg)
	}
	if got := slices.Sorted(slices.Values(calls.tagARNs)); !slices.Equal(got, memDBTagReadARNs()) {
		t.Errorf("ListTags ARNs = %v\nwant %v", got, memDBTagReadARNs())
	}
}

// A rescan stores the same rows without inserting any.
func TestScanMemoryDB_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := memDBScan(context.Background(), st, memDBHandlers(newMemDBCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := memDBScan(context.Background(), st, memDBHandlers(newMemDBCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != memDBTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, memDBTestTotal)
	}
}

// A failed tag read: the not-found code ListTags models for the row's type
// drops the row silently; another type's not-found code keeps the row and
// is an error; a denial
// keeps the row untagged and warns once under memorydb:ListTags; a throttle
// keeps it and is transient; any other error keeps it and is an error.
func TestScanMemoryDB_TagReadFailures(t *testing.T) {
	type tagCase struct {
		name      string
		arn, typ  string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		wantWarns int
	}
	var cases []tagCase
	for _, k := range memDBKinds() {
		if !k.tagRead() {
			continue
		}
		arn := k.arn("tagged")
		cases = append(cases,
			tagCase{k.typ + "/not found", arn, k.typ, apiErr(k.notFound, "gone"), -1, "", memDBTestTotal - 1, false, 0},
			tagCase{k.typ + "/another type's not found", arn, k.typ, apiErr("SnapshotNotFoundFault", "gone"), outcomeError, "SnapshotNotFoundFault", memDBTestTotal, true, 0},
		)
		if k.notFound == "SnapshotNotFoundFault" {
			cases[len(cases)-1].err, cases[len(cases)-1].code = apiErr("UserNotFoundFault", "gone"), "UserNotFoundFault"
		}
		cases = append(cases,
			tagCase{k.typ + "/denied", arn, k.typ, memDBDenied, -1, "", memDBTestTotal, true, 1},
			tagCase{k.typ + "/transient", arn, k.typ, memDBThrottle, outcomeTransient, "Throttling", memDBTestTotal, true, 0},
			tagCase{k.typ + "/other error", arn, k.typ, memDBInvalid, outcomeError, memDBTestInvalidARN, memDBTestTotal, true, 0},
		)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newMemDBCalls()
			calls.errs[c.arn] = c.err

			total, _, err := memDBScan(context.Background(), st, memDBHandlers(calls))
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
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := warns()
			if len(w) != c.wantWarns {
				t.Fatalf("warnings = %v; want %d", w, c.wantWarns)
			}
			for _, x := range w {
				if x.Service != memDBTagsOp || x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, c.arn) {
					t.Errorf("warning %+v; want one under %s in %s/%s naming %s", x, memDBTagsOp, testAccountID, testRegion, c.arn)
				}
			}
		})
	}
}

// Denied tag reads in two listings send one warning for the whole scan,
// under the tag op, naming the first denied row; the rows are kept untagged.
func TestScanMemoryDB_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, memDBTagsOp)
	calls := newMemDBCalls()
	for _, op := range []string{"DescribeClusters", "DescribeUsers"} {
		k := memDBKindOf(t, op)
		calls.errs[k.arn("tagged")] = memDBDenied
		calls.errs[k.arn("bare")] = memDBDenied
	}

	total, _, err := memDBScan(context.Background(), st, memDBHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != memDBTestTotal {
		t.Errorf("total = %d; want %d", total, memDBTestTotal)
	}
	first := memDBKindOf(t, "DescribeClusters").arn("tagged")
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion || !strings.Contains((*warnings)[0].Message, first) {
		t.Fatalf("%s warnings = %v; want 1 in %s/%s naming %s", memDBTagsOp, *warnings, testAccountID, testRegion, first)
	}
	for _, op := range []string{"DescribeClusters", "DescribeUsers"} {
		k := memDBKindOf(t, op)
		for _, arn := range []string{k.arn("tagged"), k.arn("bare")} {
			if r, ok := rowsByNativeID(t, st, k.typ)[arn]; !ok || r.TagsJSON != nil {
				t.Errorf("%s stored %v; want stored untagged", arn, ok)
			}
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to one
// warning, so the hard error decides the outcome.
func TestScanMemoryDB_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, memDBTagsOp)
	calls := newMemDBCalls()
	calls.errs[memDBKindOf(t, "DescribeClusters").arn("tagged")] = memDBThrottle
	calls.errs[memDBKindOf(t, "DescribeSnapshots").arn("bare")] = memDBInvalid

	_, _, err := memDBScan(context.Background(), st, memDBHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, memDBTestInvalidARN)
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", memDBTagsOp, *warns)
	}
}

// A failed write stops the scan: no later phase runs and no warning is sent
// about rows the write lost; the phases before it keep their rows.
func TestScanMemoryDB_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeMemoryDBParameterGroup)
	calls := newMemDBCalls()
	k := memDBKindOf(t, "DescribeParameterGroups")
	calls.errs[k.arn("tagged")] = memDBDenied

	total, _, err := memDBScan(context.Background(), st, memDBHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count(k.op); n != 1 {
		t.Errorf("%s called %d times; want 1, none after the failed write", k.op, n)
	}
	for _, op := range []string{"DescribeReservedNodes", "DescribeSnapshots", "DescribeSubnetGroups", "DescribeUsers"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write; want 0", op, n)
		}
	}
	// ACLs, clusters, multi-region clusters (2 each) and the multi-region
	// parameter group ran before it.
	if total != 3*2+1 {
		t.Errorf("total = %d; want %d", total, 3*2+1)
	}
}

// A listing's failed page keeps the page read before it, and its error does
// not stop the later listings.
func TestScanMemoryDB_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	st := newTestStore(t)
	handlers := memDBHandlers(newMemDBCalls())
	k := memDBKindOf(t, "DescribeACLs")
	handlers[k.op] = failOnCall(2, memDBInvalid, handlers[k.op])

	total, _, err := memDBScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, memDBTestInvalidARN)
	if total != memDBTestTotal-1 {
		t.Errorf("total = %d; want %d", total, memDBTestTotal-1)
	}
	if _, ok := rowsByNativeID(t, st, k.typ)[k.arn("tagged")]; !ok {
		t.Error("ACL from the page before the failure missing")
	}
	if got := len(rowsByNativeID(t, st, TypeMemoryDBUser)); got != 2 {
		t.Errorf("users (the last phase) = %d; want 2", got)
	}
}

// A repeated token ends a listing as an error, keeping its pages, for the
// tagged listings and the multi-region parameter groups alike.
func TestScanMemoryDB_RepeatedTokenKeepsPages(t *testing.T) {
	t.Run("snapshots", func(t *testing.T) {
		st := newTestStore(t)
		handlers := memDBHandlers(newMemDBCalls())
		k := memDBKindOf(t, "DescribeSnapshots")
		handlers[k.op] = func(in any) (any, error) {
			suffix := "tagged"
			if bacField[*string](in, "NextToken") != nil {
				suffix = "bare"
			}
			arn, name := k.arn(suffix), k.name(suffix)
			return k.page(&arn, &name, sdkaws.String("same")), nil
		}
		total, _, err := memDBScan(context.Background(), st, handlers)
		if !errors.Is(err, errRepeatedNextToken) {
			t.Fatalf("err = %v; want errRepeatedNextToken", err)
		}
		if total != memDBTestTotal {
			t.Errorf("total = %d; want %d", total, memDBTestTotal)
		}
	})
	t.Run("multi-region parameter groups", func(t *testing.T) {
		st := newTestStore(t)
		handlers := memDBHandlers(newMemDBCalls())
		handlers["DescribeMultiRegionParameterGroups"] = func(any) (any, error) {
			return &memorydb.DescribeMultiRegionParameterGroupsOutput{NextToken: sdkaws.String("same"), MultiRegionParameterGroups: []memorydbtypes.MultiRegionParameterGroup{{
				ARN: sdkaws.String(memDBTestMRPG), Name: sdkaws.String("mrpg-1"),
			}}}, nil
		}
		total, _, err := memDBScan(context.Background(), st, handlers)
		if !errors.Is(err, errRepeatedNextToken) {
			t.Fatalf("err = %v; want errRepeatedNextToken", err)
		}
		if total != memDBTestTotal+1 {
			t.Errorf("total = %d; want %d (the group counted once per page)", total, memDBTestTotal+1)
		}
		if _, ok := rowsByNativeID(t, st, TypeMemoryDBMultiRegionParameterGroup)[memDBTestMRPG]; !ok {
			t.Error("multi-region parameter group missing")
		}
	})
}

// An element without an ARN is not stored, is not sent to the tag op, and is
// warned about once under its listing, named by its name or by the fallback
// text when it has none.
func TestScanMemoryDB_ElementWithoutARNWarns(t *testing.T) {
	for _, c := range []struct {
		name, op, key string
		answer        func(in any) (any, error)
		lost          int
	}{
		{"user", "DescribeUsers", "u-x", func(any) (any, error) {
			return &memorydb.DescribeUsersOutput{Users: []memorydbtypes.User{{Name: sdkaws.String("u-x")}}}, nil
		}, 2},
		{"cluster without a name", "DescribeClusters", memDBNoIdentifier, func(any) (any, error) {
			return &memorydb.DescribeClustersOutput{Clusters: []memorydbtypes.Cluster{{}}}, nil
		}, 2},
		{"reserved node", "DescribeReservedNodes", "rn-x", func(any) (any, error) {
			return &memorydb.DescribeReservedNodesOutput{ReservedNodes: []memorydbtypes.ReservedNode{{ReservationId: sdkaws.String("rn-x")}}}, nil
		}, 2},
		{"multi-region parameter group", "DescribeMultiRegionParameterGroups", "mrpg-x", func(any) (any, error) {
			return &memorydb.DescribeMultiRegionParameterGroupsOutput{MultiRegionParameterGroups: []memorydbtypes.MultiRegionParameterGroup{{Name: sdkaws.String("mrpg-x")}}}, nil
		}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newMemDBCalls()
			handlers := memDBHandlers(calls)
			handlers[c.op] = c.answer

			total, _, err := memDBScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := memDBTestTotal - c.lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			w := warns()
			if len(w) != 1 {
				t.Fatalf("warnings = %v; want 1", w)
			}
			if w[0].Service != "memorydb:"+c.op || !strings.Contains(w[0].Message, c.key) || w[0].Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning %+v; want one under memorydb:%s naming %q in %s/%s", w[0], c.op, c.key, testAccountID, testRegion)
			}
			if slices.Contains(calls.tagARNs, "") {
				t.Error("an empty ARN was sent to ListTags")
			}
		})
	}
}

// Rows of the aws-us-gov and aws-cn partitions reach the tag op under their
// listed ARNs unchanged.
func TestScanMemoryDB_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newMemDBCalls()
			handlers := memDBHandlers(calls)
			clusterARN := "arn:" + c.partition + ":memorydb:" + c.region + ":" + testAccountID + ":cluster/c-p"
			mrcARN := "arn:" + c.partition + ":memorydb::" + testAccountID + ":multiregioncluster/mrc-p"
			calls.tags[clusterARN] = map[string]string{"k": "v"}
			handlers["DescribeClusters"] = func(any) (any, error) {
				return &memorydb.DescribeClustersOutput{Clusters: []memorydbtypes.Cluster{{ARN: &clusterARN, Name: sdkaws.String("c-p")}}}, nil
			}
			handlers["DescribeMultiRegionClusters"] = func(any) (any, error) {
				return &memorydb.DescribeMultiRegionClustersOutput{MultiRegionClusters: []memorydbtypes.MultiRegionCluster{{ARN: &mrcARN, MultiRegionClusterName: sdkaws.String("mrc-p")}}}, nil
			}
			acct := newTestAccount(testAccountID)
			acct.Partition = c.partition
			acct.cfg = opStubConfig(handlers, memDBDenied)
			if _, _, err := scanMemoryDB(context.Background(), acct, c.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			for _, want := range []string{clusterARN, mrcARN} {
				if !slices.Contains(calls.tagARNs, want) {
					t.Errorf("ListTags ARNs %v lack %s", calls.tagARNs, want)
				}
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeMemoryDBCluster)[clusterARN].TagsJSON, map[string]string{"k": "v"})
		})
	}
}

// The AWS-owned open-access ACL and default.* parameter groups are stored as
// managed, untagged, and never sent to the tag op; the default user is not
// managed and has its tags read.
func TestScanMemoryDB_ManagedRowsNotRead(t *testing.T) {
	st := newTestStore(t)
	calls := newMemDBCalls()
	handlers := memDBHandlers(calls)
	acl := "arn:aws:memorydb:" + testRegion + ":" + testAccountID + ":acl/open-access"
	pg := "arn:aws:memorydb:" + testRegion + ":" + testAccountID + ":parametergroup/default.memorydb-redis7"
	user := "arn:aws:memorydb:" + testRegion + ":" + testAccountID + ":user/default"
	handlers["DescribeACLs"] = func(any) (any, error) {
		return &memorydb.DescribeACLsOutput{ACLs: []memorydbtypes.ACL{{ARN: &acl, Name: sdkaws.String("open-access")}}}, nil
	}
	handlers["DescribeParameterGroups"] = func(any) (any, error) {
		return &memorydb.DescribeParameterGroupsOutput{ParameterGroups: []memorydbtypes.ParameterGroup{{ARN: &pg, Name: sdkaws.String("default.memorydb-redis7")}}}, nil
	}
	handlers["DescribeUsers"] = func(any) (any, error) {
		return &memorydb.DescribeUsersOutput{Users: []memorydbtypes.User{{ARN: &user, Name: sdkaws.String("default")}}}, nil
	}
	calls.tags[user] = map[string]string{"owner": "ops"}

	if _, _, err := memDBScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for typ, arn := range map[string]string{TypeMemoryDBACL: acl, TypeMemoryDBParameterGroup: pg} {
		r, ok := rowsByNativeID(t, st, typ)[arn]
		if !ok || !r.ManagedByProvider || r.TagsJSON != nil {
			t.Errorf("%s stored %v, managed %v, TagsJSON %v; want stored managed and untagged", arn, ok, r.ManagedByProvider, r.TagsJSON)
		}
		if slices.Contains(calls.tagARNs, arn) {
			t.Errorf("%s sent to ListTags", arn)
		}
	}
	u := rowsByNativeID(t, st, TypeMemoryDBUser)[user]
	if u.ManagedByProvider {
		t.Error("default user is managed")
	}
	assertTagsMap(t, u.TagsJSON, map[string]string{"owner": "ops"})
}

// InvalidClusterStateFault on clusters' tag reads keeps the rows untagged
// and warns once under the tag op naming the smallest ARN, with no error.
func TestScanMemoryDB_ClusterNotReadyWarnsOnce(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newMemDBCalls()
	k := memDBKindOf(t, "DescribeClusters")
	notReady := apiErr(memDBClusterNotReady, "cluster is creating")
	calls.errs[k.arn("tagged")] = notReady
	calls.errs[k.arn("bare")] = notReady

	total, _, err := memDBScan(context.Background(), st, memDBHandlers(calls))
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if total != memDBTestTotal {
		t.Errorf("total = %d; want %d", total, memDBTestTotal)
	}
	for _, arn := range []string{k.arn("tagged"), k.arn("bare")} {
		if r, ok := rowsByNativeID(t, st, k.typ)[arn]; !ok || r.TagsJSON != nil {
			t.Errorf("%s stored %v; want stored untagged", arn, ok)
		}
	}
	w := warns()
	if len(w) != 1 || w[0].Service != memDBTagsOp || w[0].Scope != testAccountID+"/"+testRegion {
		t.Fatalf("warnings = %v; want one under %s", w, memDBTagsOp)
	}
	if want := k.arn("bare"); !strings.Contains(w[0].Message, want) {
		t.Errorf("warning %q; want it to name the smallest ARN %s", w[0].Message, want)
	}
}

// InvalidClusterStateFault on any row but a cluster's is an error, as any
// other code is, and warns nothing.
func TestScanMemoryDB_NotReadyOnOtherTypeIsError(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newMemDBCalls()
	k := memDBKindOf(t, "DescribeACLs")
	calls.errs[k.arn("tagged")] = apiErr(memDBClusterNotReady, "x")

	_, _, err := memDBScan(context.Background(), st, memDBHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, memDBClusterNotReady)
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if r, ok := rowsByNativeID(t, st, k.typ)[k.arn("tagged")]; !ok || r.TagsJSON != nil {
		t.Errorf("%s stored %v; want stored untagged", k.arn("tagged"), ok)
	}
}

// After a failed write a refused read sends no warning: its row may be one
// the write lost.
func TestScanMemoryDB_RefusalAfterFailedWriteWarnsNothing(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeMemoryDBUser)
	calls := newMemDBCalls()
	calls.errs[memDBKindOf(t, "DescribeClusters").arn("bare")] = apiErr(memDBClusterNotReady, "x")

	_, _, err := memDBScan(context.Background(), st, memDBHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A region without multi-region support rejects the first page of both
// multi-region listings: they store nothing, silently, and every other
// listing is stored. The same answer on a later page is an error that keeps
// the page before it.
func TestScanMemoryDB_MultiRegionGap(t *testing.T) {
	gap := apiErr("InvalidParameterValueException", "This API operation is currently unavailable")
	t.Run("first page", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		handlers := memDBHandlers(newMemDBCalls())
		handlers["DescribeMultiRegionClusters"] = func(any) (any, error) { return nil, gap }
		handlers["DescribeMultiRegionParameterGroups"] = func(any) (any, error) { return nil, gap }
		total, _, err := memDBScan(context.Background(), st, handlers)
		if err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		if want := memDBTestTotal - 3; total != want {
			t.Errorf("total = %d; want %d", total, want)
		}
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
	t.Run("later page", func(t *testing.T) {
		st := newTestStore(t)
		handlers := memDBHandlers(newMemDBCalls())
		k := memDBKindOf(t, "DescribeMultiRegionClusters")
		handlers[k.op] = failOnCall(2, gap, handlers[k.op])
		total, _, err := memDBScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		assertAPICode(t, err, "InvalidParameterValueException")
		if total != memDBTestTotal-1 {
			t.Errorf("total = %d; want %d", total, memDBTestTotal-1)
		}
		if _, ok := rowsByNativeID(t, st, k.typ)[k.arn("tagged")]; !ok {
			t.Error("multi-region cluster from the first page missing")
		}
	})
}

// A denied listing warns once under its op; a denial on a later page keeps
// the page read before it. Every later listing still runs.
func TestScanMemoryDB_DeniedListingWarnsUnderItsOp(t *testing.T) {
	for _, call := range []int{1, 2} {
		t.Run(fmt.Sprintf("page%d", call), func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			handlers := memDBHandlers(newMemDBCalls())
			k := memDBKindOf(t, "DescribeSnapshots")
			handlers[k.op] = failOnCall(call, memDBDenied, handlers[k.op])

			total, _, err := memDBScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			kept := call - 1
			if want := memDBTestTotal - 2 + kept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if w := warns(); len(w) != 1 || w[0].Service != "memorydb:"+k.op {
				t.Errorf("warnings = %v; want one under memorydb:%s", w, k.op)
			}
			if got := len(rowsByNativeID(t, st, TypeMemoryDBUser)); got != 2 {
				t.Errorf("users (a later phase) = %d; want 2", got)
			}
		})
	}
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it in its listing.
func TestScanMemoryDB_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newMemDBCalls()
	handlers := memDBHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	k := memDBKindOf(t, "DescribeClusters")
	list := handlers[k.op]
	handlers[k.op] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := memDBScan(ctx, st, handlers)
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
	if n := calls.count("DescribeUsers"); n != 0 {
		t.Errorf("DescribeUsers called %d times after the stop; want 0", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanMemoryDB_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "memory-db.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := memDBHandlers(newMemDBCalls())
		handlers["DescribeSubnetGroups"] = func(any) (any, error) { return nil, nx }
		total, _, err := memDBScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != memDBTestTotal-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, memDBTestTotal-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newMemDBCalls()
		calls.errs[memDBKindOf(t, "DescribeACLs").arn("bare")] = nx
		_, _, err := memDBScan(context.Background(), st, memDBHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanMemoryDB(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

// Two regions listing the same multi-region cluster store one row with no
// Region and the tags read in each, inserted by the first and unchanged by
// the second.
func TestScanMemoryDB_MultiRegionClusterListedByTwoRegions(t *testing.T) {
	st := newTestStore(t)
	calls := newMemDBCalls()
	arn := "arn:aws:memorydb::" + testAccountID + ":multiregioncluster/mrc-m"
	// Only the multi-region listings answer; every other listing is denied
	// and stores nothing, so the counters see this row alone.
	handlers := map[string]func(in any) (any, error){
		"ListTags": calls.listTags,
		"DescribeMultiRegionParameterGroups": func(any) (any, error) {
			return &memorydb.DescribeMultiRegionParameterGroupsOutput{}, nil
		},
		"DescribeMultiRegionClusters": func(any) (any, error) {
			return &memorydb.DescribeMultiRegionClustersOutput{MultiRegionClusters: []memorydbtypes.MultiRegionCluster{{
				ARN: sdkaws.String(arn), MultiRegionClusterName: sdkaws.String("mrc-m"), Status: sdkaws.String("available"),
			}}}, nil
		},
	}
	calls.tags[arn] = map[string]string{"k": "v"}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, memDBDenied)
	for i, region := range []string{"us-east-1", "us-west-2"} {
		var added, changed atomic.Int64
		total, inserted, err := scanMemoryDB(context.Background(), acct, region, st.WithUpsertCounters(&added, &changed), testScanID)
		if err != nil {
			t.Fatalf("scan %s: %v", region, err)
		}
		want := 1 - i
		if total != 1 || inserted != want || added.Load() != int64(want) || changed.Load() != 0 {
			t.Errorf("%s: total, inserted, new, changed = %d, %d, %d, %d; want 1, %d, %d, 0", region, total, inserted, added.Load(), changed.Load(), want, want)
		}
	}
	rows := rowsByNativeID(t, st, TypeMemoryDBMultiRegionCluster)
	r, ok := rows[arn]
	if len(rows) != 1 || !ok || r.Region != nil {
		t.Fatalf("rows = %v, Region %v; want the one row with no Region", nativeIDs(rows), r.Region)
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"k": "v"})
	if n := slices.Index(calls.tagARNs, arn); n < 0 || slices.Index(calls.tagARNs[n+1:], arn) < 0 {
		t.Errorf("ListTags ARNs %v; want %s read by both regions", calls.tagARNs, arn)
	}
}
