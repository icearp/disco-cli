package aws

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	smithymw "github.com/aws/smithy-go/middleware"
	"github.com/icearp/disco-cli/store"
)

const (
	rdsTestSnapA        = "snap-a"
	rdsTestSnapB        = "snap-b"
	rdsTestClusterSnapA = "csnap-a"
	rdsTestClusterSnapB = "csnap-b"
	rdsTestSharedWith   = "210987654321"
)

var rdsDenied = apiErr("AccessDenied", "not authorized to perform rds action")

func rdsTestSnapARN(id string) string {
	return "arn:aws:rds:" + testRegion + ":" + testAccountID + ":snapshot:" + id
}

func rdsTestClusterSnapARN(id string) string {
	return "arn:aws:rds:" + testRegion + ":" + testAccountID + ":cluster-snapshot:" + id
}

func rdsTestAttrID(snapshotARN string) string {
	return rdsSnapshotAttributeNativeID(snapshotARN, "restore")
}

// rdsTestRestoreValues is what each snapshot's restore attribute lists:
// snap-a and csnap-a are shared with one account, snap-b and csnap-b public.
var rdsTestRestoreValues = map[string][]string{
	rdsTestSnapA:        {rdsTestSharedWith},
	rdsTestSnapB:        {"all"},
	rdsTestClusterSnapA: {rdsTestSharedWith},
	rdsTestClusterSnapB: {"all"},
}

// rdsSnapshotAttrHandlers serves both snapshot listings over two pages (a
// manual snapshot and an automated one; then a manual snapshot and a manual
// one without an identifier) and both attribute ops, recording the snapshot
// each attribute call names and failing it with fail's error. Every snapshot
// answers its restore attribute and one attribute without a name.
func rdsSnapshotAttrHandlers(calls, clusterCalls *parentCalls, fail func(id string) error) map[string]func(in any) (any, error) {
	manual, automated := ptrStr(rdsManualSnapshot), ptrStr("automated")
	return map[string]func(in any) (any, error){
		"DescribeDBSnapshots": func(in any) (any, error) {
			if in.(*rds.DescribeDBSnapshotsInput).Marker == nil {
				return &rds.DescribeDBSnapshotsOutput{Marker: ptrStr("p2"), DBSnapshots: []rdstypes.DBSnapshot{
					{DBSnapshotIdentifier: ptrStr(rdsTestSnapA), DBSnapshotArn: ptrStr(rdsTestSnapARN(rdsTestSnapA)), SnapshotType: manual},
					{DBSnapshotIdentifier: ptrStr("rds:auto"), DBSnapshotArn: ptrStr(rdsTestSnapARN("rds:auto")), SnapshotType: automated},
				}}, nil
			}
			return &rds.DescribeDBSnapshotsOutput{DBSnapshots: []rdstypes.DBSnapshot{
				{DBSnapshotIdentifier: ptrStr(rdsTestSnapB), DBSnapshotArn: ptrStr(rdsTestSnapARN(rdsTestSnapB)), SnapshotType: manual},
				{DBSnapshotArn: ptrStr(rdsTestSnapARN("no-id")), SnapshotType: manual},
			}}, nil
		},
		"DescribeDBClusterSnapshots": func(in any) (any, error) {
			if in.(*rds.DescribeDBClusterSnapshotsInput).Marker == nil {
				return &rds.DescribeDBClusterSnapshotsOutput{Marker: ptrStr("p2"), DBClusterSnapshots: []rdstypes.DBClusterSnapshot{
					{DBClusterSnapshotIdentifier: ptrStr(rdsTestClusterSnapA), DBClusterSnapshotArn: ptrStr(rdsTestClusterSnapARN(rdsTestClusterSnapA)), SnapshotType: manual},
					{DBClusterSnapshotIdentifier: ptrStr("rds:cauto"), DBClusterSnapshotArn: ptrStr(rdsTestClusterSnapARN("rds:cauto")), SnapshotType: automated},
				}}, nil
			}
			return &rds.DescribeDBClusterSnapshotsOutput{DBClusterSnapshots: []rdstypes.DBClusterSnapshot{
				{DBClusterSnapshotIdentifier: ptrStr(rdsTestClusterSnapB), DBClusterSnapshotArn: ptrStr(rdsTestClusterSnapARN(rdsTestClusterSnapB)), SnapshotType: manual},
				{DBClusterSnapshotArn: ptrStr(rdsTestClusterSnapARN("no-id")), SnapshotType: manual},
			}}, nil
		},
		"DescribeDBSnapshotAttributes": func(in any) (any, error) {
			id := *in.(*rds.DescribeDBSnapshotAttributesInput).DBSnapshotIdentifier
			calls.add(id)
			if err := fail(id); err != nil {
				return nil, err
			}
			return &rds.DescribeDBSnapshotAttributesOutput{DBSnapshotAttributesResult: &rdstypes.DBSnapshotAttributesResult{
				DBSnapshotIdentifier: &id,
				DBSnapshotAttributes: []rdstypes.DBSnapshotAttribute{
					{AttributeName: ptrStr("restore"), AttributeValues: rdsTestRestoreValues[id]},
					{AttributeValues: []string{"unnamed"}},
				},
			}}, nil
		},
		"DescribeDBClusterSnapshotAttributes": func(in any) (any, error) {
			id := *in.(*rds.DescribeDBClusterSnapshotAttributesInput).DBClusterSnapshotIdentifier
			clusterCalls.add(id)
			if err := fail(id); err != nil {
				return nil, err
			}
			return &rds.DescribeDBClusterSnapshotAttributesOutput{DBClusterSnapshotAttributesResult: &rdstypes.DBClusterSnapshotAttributesResult{
				DBClusterSnapshotIdentifier: &id,
				DBClusterSnapshotAttributes: []rdstypes.DBClusterSnapshotAttribute{
					{AttributeName: ptrStr("restore"), AttributeValues: rdsTestRestoreValues[id]},
					{AttributeValues: []string{"unnamed"}},
				},
			}}, nil
		},
	}
}

func noRDSAttrErr(string) error { return nil }

// scanRDSTopLevel runs scanRDS with every op handlers lacks denied.
func scanRDSTopLevel(st *store.Store, handlers map[string]func(in any) (any, error), log *opLog) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, rdsDenied)
	acct.cfg.APIOptions = append([]func(*smithymw.Stack) error{recordOps(log)}, acct.cfg.APIOptions...)
	return scanRDS(context.Background(), acct, testRegion, st, testScanID)
}

func restoreValues(t *testing.T, r store.Resource) []string {
	t.Helper()
	// Both attribute elements marshal to the same shape.
	var attrs rdstypes.DBSnapshotAttribute
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs %s: %v", r.AttributesJSON, err)
	}
	if sv(attrs.AttributeName) != "restore" {
		t.Errorf("attrs AttributeName = %q, want restore", sv(attrs.AttributeName))
	}
	return attrs.AttributeValues
}

// scanRDS describes the attributes of every stored manual snapshot of both
// kinds, after every older phase, and stores each named attribute under its
// snapshot.
func TestScanRDS_StoresSnapshotAttributesLast(t *testing.T) {
	st := newTestStore(t)
	var calls, clusterCalls parentCalls
	var log opLog
	total, inserted, err := scanRDSTopLevel(st, rdsSnapshotAttrHandlers(&calls, &clusterCalls, noRDSAttrErr), &log)
	if err != nil {
		t.Fatalf("scanRDS: %v", err)
	}
	// 4 snapshots + 4 cluster snapshots + 2 + 2 restore attributes; every other phase is denied.
	if total != 12 || inserted != 12 {
		t.Errorf("total, inserted = %d, %d; want 12, 12", total, inserted)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{rdsTestSnapA, rdsTestSnapB}) {
		t.Errorf("snapshot attributes described for %v, want [%s %s]", got, rdsTestSnapA, rdsTestSnapB)
	}
	if got := clusterCalls.distinct(); !slices.Equal(got, []string{rdsTestClusterSnapA, rdsTestClusterSnapB}) {
		t.Errorf("cluster snapshot attributes described for %v, want [%s %s]", got, rdsTestClusterSnapA, rdsTestClusterSnapB)
	}
	ops := log.snapshot()
	isAttrOp := func(op string) bool {
		return op == "DescribeDBSnapshotAttributes" || op == "DescribeDBClusterSnapshotAttributes"
	}
	first := slices.IndexFunc(ops, isAttrOp)
	if first < 0 || slices.ContainsFunc(ops[first:], func(op string) bool { return !isAttrOp(op) }) {
		t.Errorf("call order %v: every attribute call must follow every other phase's calls", ops)
	}

	for _, tc := range []struct {
		typ        string
		snapARN    func(string) string
		shared, pb string
	}{
		{TypeRDSDBSnapshotAttribute, rdsTestSnapARN, rdsTestSnapA, rdsTestSnapB},
		{TypeRDSDBClusterSnapshotAttribute, rdsTestClusterSnapARN, rdsTestClusterSnapA, rdsTestClusterSnapB},
	} {
		sharedID, publicID := rdsTestAttrID(tc.snapARN(tc.shared)), rdsTestAttrID(tc.snapARN(tc.pb))
		rows := rowsByNativeID(t, st, tc.typ)
		if got := nativeIDs(rows); !slices.Equal(got, []string{sharedID, publicID}) {
			t.Fatalf("%s rows = %v, want [%s %s]", tc.typ, got, sharedID, publicID)
		}
		shared := rows[sharedID]
		if shared.Type != tc.typ || sv(shared.Name) != "restore" || sv(shared.Region) != testRegion {
			t.Errorf("%s type/name/region = %q/%q/%q, want %s/restore/%s", sharedID, shared.Type, sv(shared.Name), sv(shared.Region), tc.typ, testRegion)
		}
		if got := restoreValues(t, shared); !slices.Equal(got, []string{rdsTestSharedWith}) {
			t.Errorf("%s restore values = %v, want [%s]", sharedID, got, rdsTestSharedWith)
		}
		if got := restoreValues(t, rows[publicID]); !slices.Equal(got, []string{"all"}) {
			t.Errorf("%s restore values = %v, want [all]", publicID, got)
		}
		assertContainsNative(t, st, tc.snapARN(tc.shared), sharedID)
		assertContainsNative(t, st, tc.snapARN(tc.pb), publicID)
	}
}

// A hard DB-snapshot attribute error is returned after every older phase
// stored its rows, reaches the dispatcher as a hard error, and costs the
// cluster-snapshot attribute phase none of its rows.
func TestScanRDS_SnapshotAttributeErrorKeepsOtherPhases(t *testing.T) {
	st := newTestStore(t)
	var calls, clusterCalls parentCalls
	var log opLog
	failDBSnapshots := func(id string) error {
		if id == rdsTestSnapA || id == rdsTestSnapB {
			return apiErr("InvalidParameterValue", "bad")
		}
		return nil
	}
	total, _, err := scanRDSTopLevel(st, rdsSnapshotAttrHandlers(&calls, &clusterCalls, failDBSnapshots), &log)
	assertAPICode(t, err, "InvalidParameterValue")
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v, want outcomeError", got)
	}
	snaps, clusterSnaps := len(rowsByNativeID(t, st, TypeRDSSnapshot)), len(rowsByNativeID(t, st, TypeRDSClusterSnapshot))
	dbAttrs, clusterAttrs := len(rowsByNativeID(t, st, TypeRDSDBSnapshotAttribute)), len(rowsByNativeID(t, st, TypeRDSDBClusterSnapshotAttribute))
	if total != 10 || snaps != 4 || clusterSnaps != 4 || dbAttrs != 0 || clusterAttrs != 2 {
		t.Errorf("total = %d, rows snapshot/cluster snapshot/attribute/cluster attribute = %d/%d/%d/%d; want 10, 4/4/0/2",
			total, snaps, clusterSnaps, dbAttrs, clusterAttrs)
	}
}

// A throttled older phase is demoted to one warning under its own op, so the
// attribute phase's hard error still reaches the dispatcher as a hard error,
// and the snapshots that phase stored before the throttle are still
// described.
func TestScanRDS_ThrottledOlderPhaseKeepsAttributeHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "rds:DescribeDBSnapshots")
	var calls, clusterCalls parentCalls
	var log opLog
	h := rdsSnapshotAttrHandlers(&calls, &clusterCalls, func(string) error { return apiErr("InvalidParameterValue", "bad") })
	listSnapshots := h["DescribeDBSnapshots"]
	h["DescribeDBSnapshots"] = func(in any) (any, error) {
		if in.(*rds.DescribeDBSnapshotsInput).Marker != nil {
			return nil, apiErr("Throttling", "Rate exceeded")
		}
		return listSnapshots(in)
	}
	_, _, err := scanRDSTopLevel(st, h, &log)
	assertAPICode(t, err, "InvalidParameterValue")
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v, want outcomeError", got)
	}
	if *warns != 1 {
		t.Errorf("rds:DescribeDBSnapshots warnings = %d, want 1 for the demoted throttle", *warns)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{rdsTestSnapA}) {
		t.Errorf("snapshot attributes described for %v, want [%s] from the stored first page", got, rdsTestSnapA)
	}
}

// A snapshot page whose upsert failed stops the scan before the attribute
// phases, which would only fail the same write, and the write failure reaches
// the dispatcher.
func TestScanRDS_FailedWriteRunsNoAttributePhase(t *testing.T) {
	// Not newTestStore: its cleanup queries the DB, which a closed store can't serve.
	st, err := store.Open(filepath.Join(t.TempDir(), "closing.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	insertTestScan(t, st, testScanID, "running")
	var calls, clusterCalls parentCalls
	var log opLog
	// The snapshot listing is the only phase that writes: every other listing
	// and the cluster attribute op have no handler, and the DB attribute op is
	// denied after recording its snapshot, so the closed store's write failure
	// can come only from the snapshot page.
	h := rdsSnapshotAttrHandlers(&calls, &clusterCalls, func(string) error { return rdsDenied })
	delete(h, "DescribeDBClusterSnapshots")
	delete(h, "DescribeDBClusterSnapshotAttributes")
	listSnapshots := h["DescribeDBSnapshots"]
	h["DescribeDBSnapshots"] = func(in any) (any, error) {
		if in.(*rds.DescribeDBSnapshotsInput).Marker != nil {
			// Page 2 is listed, but its upsert fails.
			if err := st.Close(); err != nil {
				t.Errorf("close: %v", err)
			}
		}
		return listSnapshots(in)
	}
	_, _, err = scanRDSTopLevel(st, h, &log)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v, want store.ErrStoreWrite", err)
	}
	if got := calls.distinct(); len(got) != 0 {
		t.Errorf("snapshot attributes described for %v after the failed write, want none", got)
	}
}

// A failed write of the DB snapshot attributes stops the scan before the
// cluster snapshot attributes are described.
func TestScanRDS_FailedAttributeWriteStopsClusterAttributes(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeRDSDBSnapshotAttribute)
	var calls, clusterCalls parentCalls
	var log opLog
	_, _, err := scanRDSTopLevel(st, rdsSnapshotAttrHandlers(&calls, &clusterCalls, noRDSAttrErr), &log)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want store.ErrStoreWrite", err)
	}
	if len(calls.distinct()) == 0 {
		t.Error("snapshot attributes not described")
	}
	if got := clusterCalls.distinct(); len(got) != 0 {
		t.Errorf("cluster snapshot attributes described for %v after the failed write, want none", got)
	}
}

// rdsAttrKind runs one attribute phase over its two manual snapshots.
type rdsAttrKind struct {
	name, op, typ string
	ids           []string
	arn           func(string) string
	parentType    string
	notFound      error
	run           func(context.Context, rdsAPI, *account, string, *store.Store, string, []childParent) (int, int, error)
}

var rdsAttrKinds = []rdsAttrKind{
	{
		"db snapshot", rdsSnapshotAttributesOp, TypeRDSDBSnapshotAttribute,
		[]string{rdsTestSnapA, rdsTestSnapB},
		rdsTestSnapARN, TypeRDSSnapshot,
		&rdstypes.DBSnapshotNotFoundFault{Message: ptrStr("gone")}, scanDBSnapshotAttributes,
	},
	{
		"db cluster snapshot", rdsClusterSnapshotAttributesOp, TypeRDSDBClusterSnapshotAttribute,
		[]string{rdsTestClusterSnapA, rdsTestClusterSnapB},
		rdsTestClusterSnapARN, TypeRDSClusterSnapshot,
		&rdstypes.DBClusterSnapshotNotFoundFault{Message: ptrStr("gone")}, scanDBClusterSnapshotAttributes,
	},
}

func (k rdsAttrKind) parents() []childParent {
	out := make([]childParent, 0, len(k.ids))
	for _, id := range k.ids {
		out = append(out, childParent{id: id, arn: k.arn(id)})
	}
	return out
}

func (k rdsAttrKind) scan(t *testing.T, st *store.Store, fail func(string) error, parents []childParent) (int, int, error) {
	t.Helper()
	var calls, clusterCalls parentCalls
	client := rds.NewFromConfig(opStubConfig(rdsSnapshotAttrHandlers(&calls, &clusterCalls, fail), rdsDenied))
	return k.run(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
}

// Each per-snapshot failure is dropped or returned per its kind, and a
// dropped snapshot costs its sibling none of its rows.
func TestScanRDSSnapshotAttributes_ErrorLadder(t *testing.T) {
	for _, k := range rdsAttrKinds {
		first, second := k.ids[0], k.ids[1]
		for _, tc := range []struct {
			name      string
			failed    []string
			err       error
			wantCode  string
			wantRows  []string
			wantWarns int
		}{
			{"snapshot deleted since listing", []string{second}, k.notFound, "", []string{first}, 0},
			{"denied warns once", []string{first, second}, rdsDenied, "", nil, 1},
			{"denied snapshot keeps sibling", []string{first}, rdsDenied, "", []string{second}, 1},
			{"throttled snapshot keeps sibling", []string{second}, apiErr("Throttling", "Rate exceeded"), "", []string{first}, 1},
			{"other error fails the phase", []string{second}, apiErr("InvalidParameterValue", "bad"), "InvalidParameterValue", nil, 0},
		} {
			t.Run(k.name+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				seedParentRows(t, st, k.parentType, k.arn(first), k.arn(second))
				warns := countOpWarnings(st, k.op)
				total, inserted, err := k.scan(t, st, func(id string) error {
					if slices.Contains(tc.failed, id) {
						return tc.err
					}
					return nil
				}, k.parents())
				if tc.wantCode != "" {
					assertAPICode(t, err, tc.wantCode)
				} else if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				want := make([]string, 0, len(tc.wantRows))
				for _, id := range tc.wantRows {
					want = append(want, rdsTestAttrID(k.arn(id)))
				}
				if got := nativeIDs(rowsByNativeID(t, st, k.typ)); !slices.Equal(got, want) {
					t.Errorf("rows = %v, want %v", got, want)
				}
				if total != len(want) || inserted != len(want) {
					t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, len(want), len(want))
				}
				if *warns != tc.wantWarns {
					t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
				}
			})
		}
	}
}

// No manual snapshot means no call and no row; a result without attributes
// stores nothing.
func TestScanRDSSnapshotAttributes_Empty(t *testing.T) {
	for _, k := range rdsAttrKinds {
		t.Run(k.name+"/no snapshots", func(t *testing.T) {
			st := newTestStore(t)
			total, inserted, err := k.scan(t, st, func(id string) error {
				t.Errorf("attributes described for %s with no snapshots", id)
				return nil
			}, nil)
			if err != nil || total != 0 || inserted != 0 {
				t.Errorf("total, inserted, err = %d, %d, %v; want 0, 0, nil", total, inserted, err)
			}
		})
	}
	for _, tc := range []struct {
		name string
		run  func(context.Context, rdsAPI, *account, string, *store.Store, string, []childParent) (int, int, error)
		op   string
		out  any
	}{
		{"db snapshot", scanDBSnapshotAttributes, "DescribeDBSnapshotAttributes", &rds.DescribeDBSnapshotAttributesOutput{}},
		{"db cluster snapshot", scanDBClusterSnapshotAttributes, "DescribeDBClusterSnapshotAttributes", &rds.DescribeDBClusterSnapshotAttributesOutput{}},
	} {
		t.Run(tc.name+"/no result", func(t *testing.T) {
			st := newTestStore(t)
			client := rds.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
				tc.op: func(any) (any, error) { return tc.out, nil },
			}, rdsDenied))
			parents := []childParent{{id: "s", arn: rdsTestSnapARN("s")}}
			total, inserted, err := tc.run(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
			if err != nil || total != 0 || inserted != 0 {
				t.Errorf("total, inserted, err = %d, %d, %v; want 0, 0, nil", total, inserted, err)
			}
		})
	}
}
