package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/odb"
	odbtypes "github.com/aws/aws-sdk-go-v2/service/odb/types"
	"github.com/icearp/disco-cli/store"
)

// odbCallLog records the parent ids the per-parent ops were called with;
// childFanOut calls the fake from concurrent goroutines.
type odbCallLog struct {
	mu  sync.Mutex
	ids []string
}

func (l *odbCallLog) add(id string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ids = append(l.ids, id)
}

// fakeODB serves the per-parent list ops from pages keyed by parent id and
// NextToken ("" is the first page); errs[parentID] fails that parent.
type fakeODB struct {
	odbAPI
	dbServers map[string]map[string]*odb.ListDbServersOutput
	avms      map[string]map[string]*odb.ListAutonomousVirtualMachinesOutput
	errs      map[string]error
	queried   *odbCallLog
}

func (f fakeODB) ListDbServers(_ context.Context, in *odb.ListDbServersInput, _ ...func(*odb.Options)) (*odb.ListDbServersOutput, error) {
	id := *in.CloudExadataInfrastructureId
	f.queried.add(id)
	if err := f.errs[id]; err != nil {
		return nil, err
	}
	if out := f.dbServers[id][sv(in.NextToken)]; out != nil {
		return out, nil
	}
	return &odb.ListDbServersOutput{}, nil
}

func (f fakeODB) ListAutonomousVirtualMachines(_ context.Context, in *odb.ListAutonomousVirtualMachinesInput, _ ...func(*odb.Options)) (*odb.ListAutonomousVirtualMachinesOutput, error) {
	id := *in.CloudAutonomousVmClusterId
	f.queried.add(id)
	if err := f.errs[id]; err != nil {
		return nil, err
	}
	if out := f.avms[id][sv(in.NextToken)]; out != nil {
		return out, nil
	}
	return &odb.ListAutonomousVirtualMachinesOutput{}, nil
}

var odbCreated = time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)

func odbInfraARN(id string) string {
	return fmt.Sprintf("arn:aws:odb:%s:%s:cloud-exadata-infrastructure/%s", testRegion, testAccountID, id)
}

func odbAVMClusterARN(id string) string {
	return fmt.Sprintf("arn:aws:odb:%s:%s:cloud-autonomous-vm-cluster/%s", testRegion, testAccountID, id)
}

func odbDbServer(id string) odbtypes.DbServerSummary {
	return odbtypes.DbServerSummary{
		DbServerId: sdkaws.String(id), DisplayName: sdkaws.String("srv-" + id),
		Status: odbtypes.ResourceStatusAvailable, CreatedAt: &odbCreated, Shape: sdkaws.String("X11M"),
	}
}

func odbAVM(id string) odbtypes.AutonomousVirtualMachineSummary {
	return odbtypes.AutonomousVirtualMachineSummary{
		AutonomousVirtualMachineId: sdkaws.String(id), VmName: sdkaws.String("vm-" + id),
		Status: odbtypes.ResourceStatusAvailable, DbServerId: sdkaws.String("dbs-1"),
	}
}

// odbChildOps drives each per-parent sub-scanner through one shape: every
// parent gets two pages (c1 plus an id-less element, then c2).
var odbChildOps = []struct {
	name, childType, kind, op, wantName, attrKey string
	parentARN                                    func(string) string
	parentType                                   string
	wantCreated                                  bool
	run                                          func(st *store.Store, parents []childParent, errs map[string]error, queried *odbCallLog) error
}{
	{
		name: "db-servers", childType: TypeODBDbServer, kind: "db-server", op: "odb:ListDbServers",
		wantName: "srv-c1", attrKey: "Shape", parentARN: odbInfraARN, parentType: TypeODBCloudExadataInfrastructure, wantCreated: true,
		run: func(st *store.Store, parents []childParent, errs map[string]error, queried *odbCallLog) error {
			fake := fakeODB{dbServers: map[string]map[string]*odb.ListDbServersOutput{}, errs: errs, queried: queried}
			for _, p := range parents {
				fake.dbServers[p.id] = map[string]*odb.ListDbServersOutput{
					"":   {DbServers: []odbtypes.DbServerSummary{odbDbServer("c1"), {DisplayName: sdkaws.String("no-id")}}, NextToken: sdkaws.String("p2")},
					"p2": {DbServers: []odbtypes.DbServerSummary{odbDbServer("c2")}},
				}
			}
			_, _, err := scanODBDbServers(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
			return err
		},
	},
	{
		name: "autonomous-virtual-machines", childType: TypeODBAutonomousVirtualMachine, kind: "autonomous-virtual-machine",
		op: "odb:ListAutonomousVirtualMachines", wantName: "vm-c1", attrKey: "DbServerId",
		parentARN: odbAVMClusterARN, parentType: TypeODBCloudAutonomousVMCluster,
		run: func(st *store.Store, parents []childParent, errs map[string]error, queried *odbCallLog) error {
			fake := fakeODB{avms: map[string]map[string]*odb.ListAutonomousVirtualMachinesOutput{}, errs: errs, queried: queried}
			for _, p := range parents {
				fake.avms[p.id] = map[string]*odb.ListAutonomousVirtualMachinesOutput{
					"":   {AutonomousVirtualMachines: []odbtypes.AutonomousVirtualMachineSummary{odbAVM("c1"), {VmName: sdkaws.String("no-id")}}, NextToken: sdkaws.String("p2")},
					"p2": {AutonomousVirtualMachines: []odbtypes.AutonomousVirtualMachineSummary{odbAVM("c2")}},
				}
			}
			_, _, err := scanODBAutonomousVirtualMachines(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
			return err
		},
	},
}

func seedODBParents(t *testing.T, st *store.Store, typ string, arn func(string) string, ids ...string) []childParent {
	t.Helper()
	parents := make([]childParent, len(ids))
	for i, id := range ids {
		upsertTestResource(t, st, "aws", testAccountID, typ, arn(id), testRegion, "{}")
		parents[i] = childParent{id: id, arn: arn(id)}
	}
	return parents
}

func TestScanODBChildren_PaginatesEveryParentAndWiresHierarchy(t *testing.T) {
	for _, op := range odbChildOps {
		t.Run(op.name, func(t *testing.T) {
			st := newTestStore(t)
			parents := seedODBParents(t, st, op.parentType, op.parentARN, "p1", "p2")
			var queried odbCallLog
			if err := op.run(st, parents, nil, &queried); err != nil {
				t.Fatalf("run: %v", err)
			}
			rows := qsRows(t, st, op.childType)
			if len(rows) != 4 {
				t.Fatalf("stored %d rows; want 4 (2 pages x 2 parents, id-less skipped)", len(rows))
			}
			for _, p := range []string{"p1", "p2"} {
				c1 := op.parentARN(p) + "/" + op.kind + "/c1"
				r, ok := rows[c1]
				if !ok {
					t.Fatalf("no row %s; got %v", c1, rows)
				}
				if r.Type != op.childType || sv(r.Region) != testRegion || sv(r.Name) != op.wantName || sv(r.Status) != "AVAILABLE" {
					t.Errorf("row %s: type=%q region=%q name=%q status=%q", c1, r.Type, sv(r.Region), sv(r.Name), sv(r.Status))
				}
				if op.wantCreated {
					if got, err := time.Parse(time.RFC3339, sv(r.CreatedAt)); err != nil || !got.Equal(odbCreated) {
						t.Errorf("row %s: CreatedAt = %q; want %s", c1, sv(r.CreatedAt), odbCreated)
					}
				}
				var attrs map[string]any
				if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs[op.attrKey] == nil {
					t.Errorf("row %s: attributes %s lack %q (err %v)", c1, r.AttributesJSON, op.attrKey, err)
				}
				assertQSContains(t, st, op.parentARN(p), c1, op.parentARN(p)+"/"+op.kind+"/c2")
			}
			if len(queried.ids) != 4 {
				t.Errorf("queried %v; want 2 pages for each of 2 parents", queried.ids)
			}
		})
	}
}

func TestScanODBChildren_ErrorHandling(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantErr   bool
		wantRows  int
		wantWarns int
	}{
		{"parent deleted since listing is skipped", apiErr("ResourceNotFoundException", "gone"), false, 4, 0},
		{"access denied warns once and keeps siblings", apiErr("AccessDeniedException", "User: x is not authorized to perform: odb:List"), false, 4, 1},
		{"other error propagates", apiErr("InternalServerException", "boom"), true, 0, 0},
	}
	for _, op := range odbChildOps {
		for _, tc := range cases {
			t.Run(op.name+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				var warned []string
				st.OnWarn = func(w store.ScanWarning) { warned = append(warned, w.Service) }
				parents := seedODBParents(t, st, op.parentType, op.parentARN, "bad1", "bad2", "ok1", "ok2")
				err := op.run(st, parents, map[string]error{"bad1": tc.err, "bad2": tc.err}, nil)
				if tc.wantErr {
					if !isAPIErrorCode(err, "InternalServerException") {
						t.Fatalf("err = %v; want the InternalServerException propagated", err)
					}
				} else if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if got := len(qsRows(t, st, op.childType)); got != tc.wantRows {
					t.Errorf("stored %d rows; want %d", got, tc.wantRows)
				}
				if len(warned) != tc.wantWarns {
					t.Errorf("recorded %d warnings; want %d", len(warned), tc.wantWarns)
				}
				for _, svc := range warned {
					if svc != op.op {
						t.Errorf("warning names %q; want %q", svc, op.op)
					}
				}
			})
		}
	}
}

func TestScanODBChildren_NoParentsMakesNoCalls(t *testing.T) {
	for _, op := range odbChildOps {
		t.Run(op.name, func(t *testing.T) {
			st := newTestStore(t)
			var queried odbCallLog
			if err := op.run(st, nil, nil, &queried); err != nil {
				t.Fatalf("run: %v", err)
			}
			if len(queried.ids) != 0 || len(qsRows(t, st, op.childType)) != 0 {
				t.Errorf("queried %v and stored rows with no parents; want neither", queried.ids)
			}
		})
	}
}

// odbServiceStub serves every op scanODB calls: one Exadata infrastructure and
// one Autonomous VM cluster (the new fan-outs' parents), one Autonomous
// Database with one backup (the last pre-existing phase), one child of each
// new kind, and override replacing an op's queue.
func odbServiceStub(t *testing.T, override map[string][]stubCall) sdkaws.Config {
	t.Helper()
	one := func(out any) []stubCall { return []stubCall{{Output: out}} }
	calls := map[string][]stubCall{
		"ListCloudAutonomousVmClusters": one(&odb.ListCloudAutonomousVmClustersOutput{CloudAutonomousVmClusters: []odbtypes.CloudAutonomousVmClusterSummary{
			{CloudAutonomousVmClusterArn: sdkaws.String(odbAVMClusterARN("avmc")), CloudAutonomousVmClusterId: sdkaws.String("avmc")},
		}}),
		"ListCloudExadataInfrastructures": one(&odb.ListCloudExadataInfrastructuresOutput{CloudExadataInfrastructures: []odbtypes.CloudExadataInfrastructureSummary{
			{CloudExadataInfrastructureArn: sdkaws.String(odbInfraARN("exa")), CloudExadataInfrastructureId: sdkaws.String("exa")},
		}}),
		"ListOdbNetworks":           one(&odb.ListOdbNetworksOutput{}),
		"ListOdbPeeringConnections": one(&odb.ListOdbPeeringConnectionsOutput{}),
		"ListCloudVmClusters":       one(&odb.ListCloudVmClustersOutput{}),
		"ListAutonomousDatabases": one(&odb.ListAutonomousDatabasesOutput{AutonomousDatabases: []odbtypes.AutonomousDatabaseSummary{
			{AutonomousDatabaseArn: sdkaws.String(odbADBARN), AutonomousDatabaseId: sdkaws.String("adb")},
		}}),
		"ListAutonomousDatabaseBackups": one(&odb.ListAutonomousDatabaseBackupsOutput{AutonomousDatabaseBackups: []odbtypes.AutonomousDatabaseBackupSummary{
			{AutonomousDatabaseBackupArn: sdkaws.String(odbBackupARN)},
		}}),
		"ListDbServers":                 one(&odb.ListDbServersOutput{DbServers: []odbtypes.DbServerSummary{odbDbServer("dbs")}}),
		"ListAutonomousVirtualMachines": one(&odb.ListAutonomousVirtualMachinesOutput{AutonomousVirtualMachines: []odbtypes.AutonomousVirtualMachineSummary{odbAVM("vm")}}),
	}
	for op, c := range override {
		calls[op] = c
	}
	return cloud9CfgWithStub(stubResponses(t, calls), testRegion)
}

var (
	odbADBARN    = fmt.Sprintf("arn:aws:odb:%s:%s:autonomous-database/adb", testRegion, testAccountID)
	odbBackupARN = fmt.Sprintf("arn:aws:odb:%s:%s:autonomous-database-backup/bk", testRegion, testAccountID)
)

// TestScanODB_ChildFanOutsAfterExistingPhases runs the whole service against
// the real SDK client (its input validators included): the parent phases hand
// their ids to the fan-outs, whose children are contained by their parents.
func TestScanODB_ChildFanOutsAfterExistingPhases(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = odbServiceStub(t, nil)
	total, _, err := scanODB(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanODB: %v", err)
	}
	if total != 6 {
		t.Errorf("total = %d; want 6 (avm cluster, infrastructure, adb, backup, db server, vm)", total)
	}
	assertQSContains(t, st, odbInfraARN("exa"), odbInfraARN("exa")+"/db-server/dbs")
	assertQSContains(t, st, odbAVMClusterARN("avmc"), odbAVMClusterARN("avmc")+"/autonomous-virtual-machine/vm")
}

// TestScanODB_ChildFanOutErrorPropagates pins that each new fan-out's hard
// error reaches the service result and that every pre-existing phase,
// including the last (autonomous-database backups), ran before it.
func TestScanODB_ChildFanOutErrorPropagates(t *testing.T) {
	for _, op := range []string{"ListDbServers", "ListAutonomousVirtualMachines"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			acct.cfg = odbServiceStub(t, map[string][]stubCall{op: {{Err: apiErr("InternalServerException", "boom")}}})
			_, _, err := scanODB(context.Background(), acct, testRegion, st, testScanID)
			if !isAPIErrorCode(err, "InternalServerException") {
				t.Fatalf("err = %v; want the %s InternalServerException", err, op)
			}
			if _, ok := qsRows(t, st, TypeODBAutonomousDatabaseBackup)[odbBackupARN]; !ok {
				t.Errorf("backup phase did not run before the failing %s", op)
			}
		})
	}
}
