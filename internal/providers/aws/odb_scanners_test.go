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
// Database with one backup, one child of each fan-out kind, one Exascale
// storage vault and one Exascale VM cluster, and override replacing an op's
// queue.
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
		"ListExascaleDbStorageVaults":   one(odbVaultPage(nil, odbExaItem{id: "vault", status: odbtypes.ResourceStatusAvailable})),
		"ListExadbVmClusters":           one(odbExadbClusterPage(nil, odbExaItem{id: "exadb", status: odbtypes.ResourceStatusAvailable})),
	}
	for op, c := range override {
		calls[op] = c
	}
	return cfgWithStub(stubResponses(t, calls), testRegion)
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
	if total != 8 {
		t.Errorf("total = %d; want 8 (avm cluster, infrastructure, adb, backup, db server, vm, vault, exadb cluster)", total)
	}
	if _, ok := qsRows(t, st, TypeODBExascaleDbStorageVault)[odbVaultARN("vault")]; !ok {
		t.Errorf("no Exascale storage vault row %s", odbVaultARN("vault"))
	}
	if _, ok := qsRows(t, st, TypeODBExadbVMCluster)[odbExadbClusterARN("exadb")]; !ok {
		t.Errorf("no Exascale VM cluster row %s", odbExadbClusterARN("exadb"))
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

// TestScanODB_ExascaleErrorPropagatesAfterEveryExistingPhase pins that each
// Exascale phase's hard error reaches the service result and that the last
// pre-existing phases (the db-server and virtual-machine fan-outs) stored their
// rows before it ran.
func TestScanODB_ExascaleErrorPropagatesAfterEveryExistingPhase(t *testing.T) {
	for _, op := range []string{"ListExascaleDbStorageVaults", "ListExadbVmClusters"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			acct.cfg = odbServiceStub(t, map[string][]stubCall{op: {{Err: apiErr("InternalServerException", "boom")}}})
			_, _, err := scanODB(context.Background(), acct, testRegion, st, testScanID)
			if !isAPIErrorCode(err, "InternalServerException") {
				t.Fatalf("err = %v; want the %s InternalServerException", err, op)
			}
			if _, ok := qsRows(t, st, TypeODBDbServer)[odbInfraARN("exa")+"/db-server/dbs"]; !ok {
				t.Errorf("db-server phase did not run before the failing %s", op)
			}
			if _, ok := qsRows(t, st, TypeODBAutonomousVirtualMachine)[odbAVMClusterARN("avmc")+"/autonomous-virtual-machine/vm"]; !ok {
				t.Errorf("autonomous-virtual-machine phase did not run before the failing %s", op)
			}
		})
	}
}

func odbVaultARN(id string) string {
	return fmt.Sprintf("arn:aws:odb:%s:%s:exascale-db-storage-vault/%s", testRegion, testAccountID, id)
}

func odbExadbClusterARN(id string) string {
	return fmt.Sprintf("arn:aws:odb:%s:%s:exadb-vm-cluster/%s", testRegion, testAccountID, id)
}

// odbExaItem is one listed Exascale element; noARN leaves its ARN unset.
type odbExaItem struct {
	id     string
	status odbtypes.ResourceStatus
	noARN  bool
}

func odbVaultPage(next *string, items ...odbExaItem) *odb.ListExascaleDbStorageVaultsOutput {
	out := &odb.ListExascaleDbStorageVaultsOutput{NextToken: next}
	for _, it := range items {
		v := odbtypes.ExascaleDbStorageVaultSummary{
			ExascaleDbStorageVaultId: sdkaws.String(it.id), DisplayName: sdkaws.String("vault-" + it.id),
			Status: it.status, CreatedAt: &odbCreated, AvailabilityZone: sdkaws.String("us-east-1a"),
		}
		if !it.noARN {
			v.ExascaleDbStorageVaultArn = sdkaws.String(odbVaultARN(it.id))
		}
		out.ExascaleDbStorageVaults = append(out.ExascaleDbStorageVaults, v)
	}
	return out
}

func odbExadbClusterPage(next *string, items ...odbExaItem) *odb.ListExadbVmClustersOutput {
	out := &odb.ListExadbVmClustersOutput{NextToken: next}
	for _, it := range items {
		c := odbtypes.ExadbVmClusterSummary{
			ExadbVmClusterId: sdkaws.String(it.id), DisplayName: sdkaws.String("exadb-" + it.id),
			Status: it.status, CreatedAt: &odbCreated, Hostname: sdkaws.String("host-" + it.id),
		}
		if !it.noARN {
			c.ExadbVmClusterArn = sdkaws.String(odbExadbClusterARN(it.id))
		}
		out.ExadbVmClusters = append(out.ExadbVmClusters, c)
	}
	return out
}

// odbExaRequest reads the paging fields of either Exascale list input.
func odbExaRequest(in any) (token string, maxResults int32) {
	switch in := in.(type) {
	case *odb.ListExascaleDbStorageVaultsInput:
		return sv(in.NextToken), sdkaws.ToInt32(in.MaxResults)
	case *odb.ListExadbVmClustersInput:
		return sv(in.NextToken), sdkaws.ToInt32(in.MaxResults)
	}
	return "", 0
}

// odbExascaleOps drives each Exascale sub-scanner through one shape.
var odbExascaleOps = []struct {
	name, sdkOp, op, typ, namePrefix, attrKey string
	arn                                       func(string) string
	page                                      func(next *string, items ...odbExaItem) any
	scan                                      func(context.Context, odbAPI, *account, string, *store.Store, string) (int, int, error)
}{
	{
		name: "storage-vaults", sdkOp: "ListExascaleDbStorageVaults", op: "odb:ListExascaleDbStorageVaults",
		typ: TypeODBExascaleDbStorageVault, namePrefix: "vault-", attrKey: "AvailabilityZone", arn: odbVaultARN,
		page: func(next *string, items ...odbExaItem) any { return odbVaultPage(next, items...) },
		scan: scanODBExascaleDbStorageVaults,
	},
	{
		name: "exadb-vm-clusters", sdkOp: "ListExadbVmClusters", op: "odb:ListExadbVmClusters",
		typ: TypeODBExadbVMCluster, namePrefix: "exadb-", attrKey: "Hostname", arn: odbExadbClusterARN,
		page: func(next *string, items ...odbExaItem) any { return odbExadbClusterPage(next, items...) },
		scan: scanODBExadbVMClusters,
	},
}

// odbExaMaxCalls bounds the calls one listing may make, so a pagination loop
// fails the test instead of hanging it.
const odbExaMaxCalls = 5

// runODBExascale runs one Exascale sub-scanner against the real SDK client,
// serving pages keyed by the request's NextToken ("" is the first page; a
// missing key is an empty page) and failing a call with errs[token]. It returns
// the tokens requested, in order.
func runODBExascale(t *testing.T, st *store.Store, op int, pages map[string]any, errs map[string]error) (total int, tokens []string, err error) {
	t.Helper()
	tc := odbExascaleOps[op]
	cfg := opStubConfig(map[string]func(in any) (any, error){
		tc.sdkOp: func(in any) (any, error) {
			token, maxResults := odbExaRequest(in)
			tokens = append(tokens, token)
			if len(tokens) > odbExaMaxCalls {
				return nil, fmt.Errorf("%s called %d times; want at most %d", tc.sdkOp, len(tokens), odbExaMaxCalls)
			}
			if maxResults != 1000 {
				t.Errorf("%s MaxResults = %d; want the model cap 1000", tc.sdkOp, maxResults)
			}
			if e := errs[token]; e != nil {
				return nil, e
			}
			if p, ok := pages[token]; ok {
				return p, nil
			}
			return tc.page(nil), nil
		},
	}, apiErr("AccessDeniedException", "unexpected op"))
	total, _, err = tc.scan(context.Background(), odb.NewFromConfig(cfg), newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, tokens, err
}

func TestScanODBExascale_PaginatesAndStores(t *testing.T) {
	for i, tc := range odbExascaleOps {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, tc.op)
			pages := map[string]any{
				"": tc.page(sdkaws.String("p2"),
					odbExaItem{id: "a1", status: odbtypes.ResourceStatusAvailable},
					odbExaItem{id: "no-arn", status: odbtypes.ResourceStatusAvailable, noARN: true},
					odbExaItem{id: "gone", status: odbtypes.ResourceStatusTerminated},
					odbExaItem{id: "going", status: odbtypes.ResourceStatusTerminating}),
				"p2": tc.page(nil, odbExaItem{id: "a2", status: odbtypes.ResourceStatusAvailable}),
			}
			total, tokens, err := runODBExascale(t, st, i, pages, nil)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != 3 || len(tokens) != 2 {
				t.Errorf("total = %d over calls %q; want 3 rows over 2 pages", total, tokens)
			}
			rows := rowsByNativeID(t, st, tc.typ)
			for _, id := range []string{"a1", "a2", "going"} {
				if _, ok := rows[tc.arn(id)]; !ok {
					t.Errorf("no row %s; got %d rows", tc.arn(id), len(rows))
				}
			}
			if _, ok := rows[tc.arn("gone")]; ok {
				t.Errorf("TERMINATED row %s stored; want it dropped", tc.arn("gone"))
			}
			if len(rows) != 3 {
				t.Errorf("stored %d rows; want 3", len(rows))
			}
			r := rows[tc.arn("a1")]
			if r.Type != tc.typ || sv(r.Region) != testRegion || sv(r.Name) != tc.namePrefix+"a1" || sv(r.Status) != "AVAILABLE" {
				t.Errorf("row a1: type=%q region=%q name=%q status=%q", r.Type, sv(r.Region), sv(r.Name), sv(r.Status))
			}
			if got, err := time.Parse(time.RFC3339, sv(r.CreatedAt)); err != nil || !got.Equal(odbCreated) {
				t.Errorf("row a1: CreatedAt = %q; want %s", sv(r.CreatedAt), odbCreated)
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs[tc.attrKey] == nil {
				t.Errorf("row a1: attributes %s lack %q (err %v)", r.AttributesJSON, tc.attrKey, err)
			}
			if *warns != 1 {
				t.Errorf("recorded %d warnings; want 1 for the ARN-less element", *warns)
			}
		})
	}
}

func TestScanODBExascale_EmptyStoresNothing(t *testing.T) {
	for i, tc := range odbExascaleOps {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, tc.op)
			total, _, err := runODBExascale(t, st, i, nil, nil)
			if err != nil || total != 0 || *warns != 0 {
				t.Errorf("scan = (%d, %v) with %d warnings; want (0, nil) and none", total, err, *warns)
			}
		})
	}
}

// TestScanODBExascale_RepeatedTokenStops pins StopOnDuplicateToken: the service
// handing back the token it was sent ends the listing instead of looping.
func TestScanODBExascale_RepeatedTokenStops(t *testing.T) {
	for i, tc := range odbExascaleOps {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			pages := map[string]any{
				"":     tc.page(sdkaws.String("same"), odbExaItem{id: "a1", status: odbtypes.ResourceStatusAvailable}),
				"same": tc.page(sdkaws.String("same"), odbExaItem{id: "a2", status: odbtypes.ResourceStatusAvailable}),
			}
			total, tokens, err := runODBExascale(t, st, i, pages, nil)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != 2 || len(tokens) != 2 {
				t.Errorf("total = %d over calls %q; want 2 rows over 2 calls", total, tokens)
			}
		})
	}
}

// TestScanODBExascale_NotOnboardedIsDisabled pins the not-onboarded branch: it
// maps to the disabled sentinel like every other ODB phase, not to an IAM
// warning.
// The earlier ODB phases own the disabled sentinel; here, after they stored
// rows, a not-onboarded page is skipped silently and keeps the listed rows.
func TestScanODBExascale_NotOnboardedSkipsSilently(t *testing.T) {
	for i, tc := range odbExascaleOps {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, tc.op)
			notOnboarded := apiErr("AccessDeniedException", "The account isn't onboarded to the service")
			pages := map[string]any{"": tc.page(sdkaws.String("p2"), odbExaItem{id: "a1", status: odbtypes.ResourceStatusAvailable})}
			total, _, err := runODBExascale(t, st, i, pages, map[string]error{"p2": notOnboarded})
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != 1 {
				t.Errorf("total = %d; want 1 (the row listed before the not-onboarded page)", total)
			}
			if *warns != 0 {
				t.Errorf("recorded %d warnings; want none", *warns)
			}
		})
	}
}

func TestScanODBExascale_ErrorLadder(t *testing.T) {
	notDeployed := apiErr("UnknownOperationException", "")
	cases := []struct {
		name      string
		errs      map[string]error
		wantErr   string
		wantRows  int
		wantWarns int
	}{
		{"denied on the first page warns", map[string]error{"": apiErr("AccessDeniedException", "User: x is not authorized to perform: odb:List")}, "", 0, 1},
		{"denied on a later page keeps listed rows", map[string]error{"p2": apiErr("AccessDeniedException", "User: x is not authorized to perform: odb:List")}, "", 1, 1},
		{"op not deployed on the first page is a silent gap", map[string]error{"": notDeployed}, "", 0, 0},
		{"gateway service-not-available denial is a silent gap", map[string]error{"": apiErr("AccessDeniedException", "Unable to determine service/operation name to be authorized")}, "", 0, 0},
		{"op not deployed after a page propagates", map[string]error{"p2": notDeployed}, "UnknownOperationException", 0, 0},
		{"other error propagates", map[string]error{"": apiErr("InternalServerException", "boom")}, "InternalServerException", 0, 0},
	}
	for i, tc := range odbExascaleOps {
		for _, c := range cases {
			t.Run(tc.name+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, tc.op)
				pages := map[string]any{"": tc.page(sdkaws.String("p2"), odbExaItem{id: "a1", status: odbtypes.ResourceStatusAvailable})}
				_, _, err := runODBExascale(t, st, i, pages, c.errs)
				if c.wantErr != "" {
					if !isAPIErrorCode(err, c.wantErr) {
						t.Fatalf("err = %v; want %s propagated", err, c.wantErr)
					}
				} else if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if got := len(rowsByNativeID(t, st, tc.typ)); got != c.wantRows {
					t.Errorf("stored %d rows; want %d", got, c.wantRows)
				}
				if *warns != c.wantWarns {
					t.Errorf("recorded %d warnings; want %d", *warns, c.wantWarns)
				}
			})
		}
	}
}
