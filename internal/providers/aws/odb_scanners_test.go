package aws

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
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

// odbARN is the ARN of the ODB resource kind/id in the test account.
func odbARN(kind, id string) string {
	return "arn:aws:odb:" + testRegion + ":" + testAccountID + ":" + kind + "/" + id
}

func odbInfraARN(id string) string        { return odbARN("cloud-exadata-infrastructure", id) }
func odbAVMClusterARN(id string) string   { return odbARN("cloud-autonomous-vm-cluster", id) }
func odbVaultARN(id string) string        { return odbARN("exascale-db-storage-vault", id) }
func odbExadbClusterARN(id string) string { return odbARN("exadb-vm-cluster", id) }

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

var (
	odbDenied       = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: odb:List")
	odbNotOnboarded = apiErr("AccessDeniedException", "The account isn't onboarded to the service")
	odbBadInput     = apiErr("ValidationException", "bad")
	odbThrottle     = apiErr("ThrottlingException", "slow down")
	odbGone         = apiErr("ResourceNotFoundException", "gone")
)

const odbTagsOp = "odb:ListTagsForResource"

// odbCalls records the stubbed ODB calls (bacCalls) and answers
// ListTagsForResource from tags or errs, keyed by ResourceArn.
type odbCalls struct{ *bacCalls }

// newODBCalls tags every fixture resource whose ARN ends in "tagged" with its
// type (odbTagged).
func newODBCalls() *odbCalls {
	c := &odbCalls{newBACCalls()}
	for _, r := range odbTagged() {
		if strings.HasSuffix(r.tagARN, "tagged") {
			c.tags[r.tagARN] = map[string]string{"type": r.typ}
		}
	}
	return c
}

func (c *odbCalls) listTags(in any) (any, error) {
	c.called("ListTagsForResource", in)
	arn := sv(in.(*odb.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &odb.ListTagsForResourceOutput{Tags: tags}, nil
}

// paged records op's call and answers it with page(in, NextToken).
func (c *odbCalls) paged(op string, page func(in any, token string) any) func(any) (any, error) {
	return func(in any) (any, error) {
		c.called(op, in)
		return page(in, sv(bacField[*string](in, "NextToken"))), nil
	}
}

// odbTwoPages serves item("tagged") on the first page and item("bare") on
// the second.
func odbTwoPages[T any](token string, item func(id string) T, wrap func(items []T, next *string) any) any {
	if token == "" {
		return wrap([]T{item("tagged")}, sdkaws.String("p2"))
	}
	return wrap([]T{item("bare")}, nil)
}

// odbRowWant is one fixture row the tag read covers: its type, NativeID,
// the ARN its tag read is sent, and the Name it is stored with.
type odbRowWant struct{ typ, nativeID, tagARN, name string }

// odbTopLevel is every region-wide listing of the fixture: its type and ARN
// kind. Each lists "tagged" and "bare", one per page.
var odbTopLevel = []struct{ typ, kind string }{
	{TypeODBCloudAutonomousVMCluster, "cloud-autonomous-vm-cluster"},
	{TypeODBCloudExadataInfrastructure, "cloud-exadata-infrastructure"},
	{TypeODBOdbNetwork, "odb-network"},
	{TypeODBOdbPeeringConnection, "odb-peering-connection"},
	{TypeODBCloudVMCluster, "cloud-vm-cluster"},
	{TypeODBAutonomousDatabase, "autonomous-database"},
	{TypeODBExascaleDbStorageVault, "exascale-db-storage-vault"},
	{TypeODBExadbVMCluster, "exadb-vm-cluster"},
}

func odbName(kind, id string) string { return kind + " " + id }

// odbTagged is every fixture row whose tags are read: the region-wide rows,
// and the DB nodes and backups of each "tagged" and "bare" parent.
func odbTagged() []odbRowWant {
	var rows []odbRowWant
	for _, id := range []string{"tagged", "bare"} {
		for _, k := range odbTopLevel {
			rows = append(rows, odbRowWant{k.typ, odbARN(k.kind, id), odbARN(k.kind, id), odbName(k.kind, id)})
		}
		for _, child := range []string{"tagged", "bare"} {
			node := id + "-" + child
			rows = append(rows,
				odbRowWant{TypeODBDbNode, odbARN("cloud-vm-cluster", id) + "/db-node/" + node, odbARN("db-node", node), "host-" + node},
				odbRowWant{TypeODBAutonomousDatabaseBackup, odbARN("autonomous-database-backup", node), odbARN("autonomous-database-backup", node), odbName("autonomous-database-backup", node)})
		}
	}
	return rows
}

// odbHandlers answers every op scanODB calls: each region-wide listing two
// pages, each DB-node and backup listing two pages per parent, and one DB
// server and one virtual machine per parent.
func odbHandlers(c *odbCalls) map[string]func(any) (any, error) {
	name := func(kind, id string) *string { return sdkaws.String(odbName(kind, id)) }
	return map[string]func(any) (any, error){
		"ListCloudAutonomousVmClusters": c.paged("ListCloudAutonomousVmClusters", func(_ any, token string) any {
			return odbTwoPages(token, func(id string) odbtypes.CloudAutonomousVmClusterSummary {
				k := "cloud-autonomous-vm-cluster"
				return odbtypes.CloudAutonomousVmClusterSummary{CloudAutonomousVmClusterArn: sdkaws.String(odbARN(k, id)), CloudAutonomousVmClusterId: sdkaws.String(id), DisplayName: name(k, id)}
			}, func(items []odbtypes.CloudAutonomousVmClusterSummary, next *string) any {
				return &odb.ListCloudAutonomousVmClustersOutput{CloudAutonomousVmClusters: items, NextToken: next}
			})
		}),
		"ListCloudExadataInfrastructures": c.paged("ListCloudExadataInfrastructures", func(_ any, token string) any {
			return odbTwoPages(token, func(id string) odbtypes.CloudExadataInfrastructureSummary {
				k := "cloud-exadata-infrastructure"
				return odbtypes.CloudExadataInfrastructureSummary{CloudExadataInfrastructureArn: sdkaws.String(odbARN(k, id)), CloudExadataInfrastructureId: sdkaws.String(id), DisplayName: name(k, id)}
			}, func(items []odbtypes.CloudExadataInfrastructureSummary, next *string) any {
				return &odb.ListCloudExadataInfrastructuresOutput{CloudExadataInfrastructures: items, NextToken: next}
			})
		}),
		"ListOdbNetworks": c.paged("ListOdbNetworks", func(_ any, token string) any {
			return odbTwoPages(token, func(id string) odbtypes.OdbNetworkSummary {
				k := "odb-network"
				return odbtypes.OdbNetworkSummary{OdbNetworkArn: sdkaws.String(odbARN(k, id)), OdbNetworkId: sdkaws.String(id), DisplayName: name(k, id)}
			}, func(items []odbtypes.OdbNetworkSummary, next *string) any {
				return &odb.ListOdbNetworksOutput{OdbNetworks: items, NextToken: next}
			})
		}),
		"ListOdbPeeringConnections": c.paged("ListOdbPeeringConnections", func(_ any, token string) any {
			return odbTwoPages(token, func(id string) odbtypes.OdbPeeringConnectionSummary {
				k := "odb-peering-connection"
				return odbtypes.OdbPeeringConnectionSummary{OdbPeeringConnectionArn: sdkaws.String(odbARN(k, id)), OdbPeeringConnectionId: sdkaws.String(id), DisplayName: name(k, id)}
			}, func(items []odbtypes.OdbPeeringConnectionSummary, next *string) any {
				return &odb.ListOdbPeeringConnectionsOutput{OdbPeeringConnections: items, NextToken: next}
			})
		}),
		"ListCloudVmClusters": c.paged("ListCloudVmClusters", func(_ any, token string) any {
			return odbTwoPages(token, func(id string) odbtypes.CloudVmClusterSummary {
				k := "cloud-vm-cluster"
				return odbtypes.CloudVmClusterSummary{CloudVmClusterArn: sdkaws.String(odbARN(k, id)), CloudVmClusterId: sdkaws.String(id), ClusterName: name(k, id)}
			}, func(items []odbtypes.CloudVmClusterSummary, next *string) any {
				return &odb.ListCloudVmClustersOutput{CloudVmClusters: items, NextToken: next}
			})
		}),
		"ListDbNodes": c.paged("ListDbNodes", func(in any, token string) any {
			cluster := sv(in.(*odb.ListDbNodesInput).CloudVmClusterId)
			return odbTwoPages(token, func(id string) odbtypes.DbNodeSummary {
				node := cluster + "-" + id
				return odbtypes.DbNodeSummary{DbNodeArn: sdkaws.String(odbARN("db-node", node)), DbNodeId: sdkaws.String(node), Hostname: sdkaws.String("host-" + node)}
			}, func(items []odbtypes.DbNodeSummary, next *string) any {
				return &odb.ListDbNodesOutput{DbNodes: items, NextToken: next}
			})
		}),
		"ListAutonomousDatabases": c.paged("ListAutonomousDatabases", func(_ any, token string) any {
			return odbTwoPages(token, func(id string) odbtypes.AutonomousDatabaseSummary {
				k := "autonomous-database"
				return odbtypes.AutonomousDatabaseSummary{AutonomousDatabaseArn: sdkaws.String(odbARN(k, id)), AutonomousDatabaseId: sdkaws.String(id), DisplayName: name(k, id)}
			}, func(items []odbtypes.AutonomousDatabaseSummary, next *string) any {
				return &odb.ListAutonomousDatabasesOutput{AutonomousDatabases: items, NextToken: next}
			})
		}),
		"ListAutonomousDatabaseBackups": c.paged("ListAutonomousDatabaseBackups", func(in any, token string) any {
			database := sv(in.(*odb.ListAutonomousDatabaseBackupsInput).AutonomousDatabaseId)
			return odbTwoPages(token, func(id string) odbtypes.AutonomousDatabaseBackupSummary {
				k, backup := "autonomous-database-backup", database+"-"+id
				return odbtypes.AutonomousDatabaseBackupSummary{AutonomousDatabaseBackupArn: sdkaws.String(odbARN(k, backup)), AutonomousDatabaseId: sdkaws.String(database), DisplayName: name(k, backup)}
			}, func(items []odbtypes.AutonomousDatabaseBackupSummary, next *string) any {
				return &odb.ListAutonomousDatabaseBackupsOutput{AutonomousDatabaseBackups: items, NextToken: next}
			})
		}),
		"ListDbServers": c.paged("ListDbServers", func(any, string) any {
			return &odb.ListDbServersOutput{DbServers: []odbtypes.DbServerSummary{odbDbServer("dbs")}}
		}),
		"ListAutonomousVirtualMachines": c.paged("ListAutonomousVirtualMachines", func(any, string) any {
			return &odb.ListAutonomousVirtualMachinesOutput{AutonomousVirtualMachines: []odbtypes.AutonomousVirtualMachineSummary{odbAVM("vm")}}
		}),
		"ListExascaleDbStorageVaults": c.paged("ListExascaleDbStorageVaults", func(_ any, token string) any {
			return odbTwoPages(token, func(id string) odbtypes.ExascaleDbStorageVaultSummary {
				k := "exascale-db-storage-vault"
				return odbtypes.ExascaleDbStorageVaultSummary{ExascaleDbStorageVaultArn: sdkaws.String(odbARN(k, id)), ExascaleDbStorageVaultId: sdkaws.String(id), DisplayName: name(k, id), Status: odbtypes.ResourceStatusAvailable, CreatedAt: &odbCreated}
			}, func(items []odbtypes.ExascaleDbStorageVaultSummary, next *string) any {
				return &odb.ListExascaleDbStorageVaultsOutput{ExascaleDbStorageVaults: items, NextToken: next}
			})
		}),
		"ListExadbVmClusters": c.paged("ListExadbVmClusters", func(_ any, token string) any {
			return odbTwoPages(token, func(id string) odbtypes.ExadbVmClusterSummary {
				k := "exadb-vm-cluster"
				return odbtypes.ExadbVmClusterSummary{ExadbVmClusterArn: sdkaws.String(odbARN(k, id)), ExadbVmClusterId: sdkaws.String(id), DisplayName: name(k, id), Status: odbtypes.ResourceStatusAvailable, CreatedAt: &odbCreated}
			}, func(items []odbtypes.ExadbVmClusterSummary, next *string) any {
				return &odb.ListExadbVmClustersOutput{ExadbVmClusters: items, NextToken: next}
			})
		}),
		"ListTagsForResource": c.listTags,
	}
}

// odbScan runs scanODB, the real entry point, against handlers; an op without
// one is denied.
func odbScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, odbDenied)
	return scanODB(ctx, acct, testRegion, st, testScanID)
}

// odbFixtureTotal is the rows of the fixture: 8 region-wide types x 2, 4 DB
// nodes, 4 backups, 2 DB servers and 2 virtual machines.
const odbFixtureTotal = 28

func odbRowOf(t *testing.T, st *store.Store, typ, nativeID string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[nativeID]
	return r, ok
}

// Every type ListTagsForResource accepts stores its tags, nil for a resource
// without tags, and exactly those rows' ARNs are sent to it, a DB node's own
// DbNodeArn rather than its NativeID. Every page of every listing is stored,
// each listing asks for its page cap, every row keeps its Name, and the scan
// reports no error or warning.
func TestScanODB_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newODBCalls()

	total, inserted, err := odbScan(context.Background(), st, odbHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != odbFixtureTotal || inserted != odbFixtureTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, odbFixtureTotal, odbFixtureTotal)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantARNs []string
	for _, w := range odbTagged() {
		r, ok := odbRowOf(t, st, w.typ, w.nativeID)
		if !ok {
			t.Errorf("%s %s missing", w.typ, w.nativeID)
			continue
		}
		if sv(r.Name) != w.name {
			t.Errorf("%s %s Name = %q; want %q", w.typ, w.nativeID, sv(r.Name), w.name)
		}
		switch {
		case strings.HasSuffix(w.nativeID, "tagged"):
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": w.typ})
		case r.TagsJSON != nil:
			t.Errorf("%s %s TagsJSON = %q; want nil", w.typ, w.nativeID, *r.TagsJSON)
		}
		wantARNs = append(wantARNs, w.tagARN)
	}
	slices.Sort(wantARNs)
	slices.Sort(calls.tagARNs)
	if !slices.Equal(calls.tagARNs, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", calls.tagARNs, wantARNs)
	}
	caps := map[string]int32{
		"ListCloudAutonomousVmClusters": 1000, "ListCloudExadataInfrastructures": 1000, "ListOdbNetworks": 1000,
		"ListOdbPeeringConnections": 100, "ListCloudVmClusters": 1000, "ListDbNodes": 1000, "ListAutonomousDatabases": 1000,
		"ListAutonomousDatabaseBackups": 1000, "ListExascaleDbStorageVaults": 1000, "ListExadbVmClusters": 1000,
	}
	for op, want := range caps {
		got := calls.maxResults[op]
		if len(got) == 0 {
			t.Errorf("%s never sent MaxResults", op)
		}
		for _, n := range got {
			if n != want {
				t.Errorf("%s MaxResults = %d; want %d", op, n, want)
			}
		}
	}
	vault, _ := odbRowOf(t, st, TypeODBExascaleDbStorageVault, odbARN("exascale-db-storage-vault", "tagged"))
	if created, err := time.Parse(time.RFC3339, sv(vault.CreatedAt)); sv(vault.Status) != "AVAILABLE" || err != nil || !created.Equal(odbCreated) {
		t.Errorf("vault Status, CreatedAt = %q, %q; want AVAILABLE, %s", sv(vault.Status), sv(vault.CreatedAt), odbCreated)
	}
	assertQSContains(t, st, odbInfraARN("tagged"), odbInfraARN("tagged")+"/db-server/dbs")
	assertQSContains(t, st, odbAVMClusterARN("bare"), odbAVMClusterARN("bare")+"/autonomous-virtual-machine/vm")
}

// A rescan stores the same rows without inserting any.
func TestScanODB_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newODBCalls()
	if _, _, err := odbScan(context.Background(), st, odbHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := odbScan(context.Background(), st, odbHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != odbFixtureTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, odbFixtureTotal)
	}
}

// A failed tag read keeps the row without tags, except a modeled not-found,
// which drops it: a denial warns once under odb:ListTagsForResource, a
// throttle after retries is a transient error, any other code a hard one.
func TestScanODB_TagReadFailures(t *testing.T) {
	targets := []odbRowWant{
		{typ: TypeODBOdbNetwork, nativeID: odbARN("odb-network", "tagged"), tagARN: odbARN("odb-network", "tagged")},
		{typ: TypeODBDbNode, nativeID: odbARN("cloud-vm-cluster", "bare") + "/db-node/bare-tagged", tagARN: odbARN("db-node", "bare-tagged")},
		{typ: TypeODBAutonomousDatabaseBackup, nativeID: odbARN("autonomous-database-backup", "tagged-tagged"), tagARN: odbARN("autonomous-database-backup", "tagged-tagged")},
	}
	for _, target := range targets {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				wantRows  int
				wantWarns int
			}{
				{"denied", odbDenied, 0, odbFixtureTotal, 1},
				{"not found", odbGone, 0, odbFixtureTotal - 1, 0},
				{"other error", odbBadInput, outcomeError, odbFixtureTotal, 0},
				{"transient", odbThrottle, outcomeTransient, odbFixtureTotal, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, odbTagsOp)
					calls := newODBCalls()
					calls.errs[target.tagARN] = c.err

					total, _, err := odbScan(context.Background(), st, odbHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.outcome == outcomeError {
						assertAPICode(t, err, "ValidationException")
					}
					if total != c.wantRows {
						t.Errorf("total = %d; want %d", total, c.wantRows)
					}
					r, ok := odbRowOf(t, st, target.typ, target.nativeID)
					dropped := c.wantRows < odbFixtureTotal
					switch {
					case dropped && ok:
						t.Error("row stored; want it dropped as deleted")
					case !dropped && !ok:
						t.Fatal("row not stored; want it kept")
					case !dropped && r.TagsJSON != nil:
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", odbTagsOp, *warnings, c.wantWarns)
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
func TestScanODB_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, odbTagsOp)
	calls := newODBCalls()
	calls.errs[odbARN("cloud-vm-cluster", "tagged")] = odbThrottle
	calls.errs[odbARN("exadb-vm-cluster", "tagged")] = odbBadInput

	_, _, err := odbScan(context.Background(), st, odbHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", odbTagsOp, *warns)
	}
}

// odbSingleNetwork answers ListOdbNetworks with one network of arn and every
// other listing with nothing.
func odbSingleNetwork(c *odbCalls, arn string) map[string]func(any) (any, error) {
	empty := func(out any) func(any) (any, error) { return func(any) (any, error) { return out, nil } }
	return map[string]func(any) (any, error){
		"ListCloudAutonomousVmClusters":   empty(&odb.ListCloudAutonomousVmClustersOutput{}),
		"ListCloudExadataInfrastructures": empty(&odb.ListCloudExadataInfrastructuresOutput{}),
		"ListOdbNetworks": empty(&odb.ListOdbNetworksOutput{OdbNetworks: []odbtypes.OdbNetworkSummary{
			{OdbNetworkArn: sdkaws.String(arn), OdbNetworkId: sdkaws.String("net"), DisplayName: sdkaws.String("net")},
		}}),
		"ListOdbPeeringConnections":   empty(&odb.ListOdbPeeringConnectionsOutput{}),
		"ListCloudVmClusters":         empty(&odb.ListCloudVmClustersOutput{}),
		"ListAutonomousDatabases":     empty(&odb.ListAutonomousDatabasesOutput{}),
		"ListExascaleDbStorageVaults": empty(&odb.ListExascaleDbStorageVaultsOutput{}),
		"ListExadbVmClusters":         empty(&odb.ListExadbVmClustersOutput{}),
		"ListTagsForResource":         c.listTags,
	}
}

// A resource another account owns has its tags read; a denied or not-found
// read of it is expected, so the row is kept without tags and without a
// warning. A denied read of an own-account row in another partition still
// warns, an own-account not-found drops the row, and the read is sent the
// listed ARN.
func TestScanODB_TagReadOwnership(t *testing.T) {
	foreign := "arn:aws:odb:" + testRegion + ":210987654321:odb-network/net"
	for _, c := range []struct {
		name      string
		acct      *account
		arn       string
		err       error
		tags      map[string]string
		kept      bool
		wantWarns int
	}{
		{"foreign tags read", newTestAccount(testAccountID), foreign, nil, map[string]string{"k": "v"}, true, 0},
		{"foreign denied is silent", newTestAccount(testAccountID), foreign, odbDenied, nil, true, 0},
		{"foreign not found is kept silent", newTestAccount(testAccountID), foreign, odbGone, nil, true, 0},
		{"own not found is dropped", newTestAccount(testAccountID), "", odbGone, nil, false, 0},
		{"own gov-partition denied warns", partitionAccount("aws-us-gov"), "", odbDenied, nil, true, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			region := c.acct.Regions[0]
			arn := cmp.Or(c.arn, "arn:"+c.acct.partition()+":odb:"+region+":"+testAccountID+":odb-network/net")
			st := newTestStore(t)
			warns := countOpWarnings(st, odbTagsOp)
			calls := newODBCalls()
			calls.tags[arn], calls.errs[arn] = c.tags, c.err
			c.acct.cfg = opStubConfig(odbSingleNetwork(calls, arn), odbDenied)

			if _, _, err := scanODB(context.Background(), c.acct, region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			r, ok := odbRowOf(t, st, TypeODBOdbNetwork, arn)
			if ok != c.kept {
				t.Fatalf("row stored = %v; want %v", ok, c.kept)
			}
			if c.tags != nil {
				assertTagsMap(t, r.TagsJSON, c.tags)
			} else if r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			if !slices.Equal(calls.tagARNs, []string{arn}) {
				t.Errorf("tag reads = %v; want [%s]", calls.tagARNs, arn)
			}
			if *warns != c.wantWarns {
				t.Errorf("%s warnings = %d; want %d", odbTagsOp, *warns, c.wantWarns)
			}
		})
	}
}

// An account not onboarded is reported disabled when the first listing says
// so, and no other op is called. Met after rows were stored, the answer ends
// that listing silently, keeping its rows, and every later phase runs.
func TestScanODB_NotOnboarded(t *testing.T) {
	t.Run("first listing", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		calls := newODBCalls()
		handlers := odbHandlers(calls)
		handlers["ListCloudAutonomousVmClusters"] = func(any) (any, error) { return nil, odbNotOnboarded }

		total, _, err := odbScan(context.Background(), st, handlers)
		if !errors.Is(err, errServiceDisabled) {
			t.Fatalf("err = %v; want errServiceDisabled", err)
		}
		assertGlueOutcome(t, err, outcomeDisabled)
		if total != 0 || *warns != 0 {
			t.Errorf("total, warnings = %d, %d; want 0, 0", total, *warns)
		}
		for op, n := range calls.ops {
			t.Errorf("%s called %d times; want no call after the first listing", op, n)
		}
	})
	t.Run("later listing before any row", func(t *testing.T) {
		st := newTestStore(t)
		calls := newODBCalls()
		handlers := odbHandlers(calls)
		handlers["ListCloudAutonomousVmClusters"] = func(any) (any, error) { return nil, odbDenied }
		handlers["ListCloudExadataInfrastructures"] = func(any) (any, error) { return nil, odbNotOnboarded }

		total, _, err := odbScan(context.Background(), st, handlers)
		if !errors.Is(err, errServiceDisabled) {
			t.Fatalf("err = %v; want errServiceDisabled", err)
		}
		if total != 0 || calls.count("ListOdbNetworks") != 0 {
			t.Errorf("total = %d, ListOdbNetworks calls = %d; want 0, 0", total, calls.count("ListOdbNetworks"))
		}
	})
	t.Run("second page of the first listing", func(t *testing.T) {
		st := newTestStore(t)
		handlers := odbHandlers(newODBCalls())
		handlers["ListCloudAutonomousVmClusters"] = failOnCall(2, odbNotOnboarded, handlers["ListCloudAutonomousVmClusters"])

		total, _, err := odbScan(context.Background(), st, handlers)
		if err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		if total != odbFixtureTotal-2 {
			t.Errorf("total = %d; want %d (one cluster and its virtual machine lost)", total, odbFixtureTotal-2)
		}
		if _, ok := odbRowOf(t, st, TypeODBCloudAutonomousVMCluster, odbAVMClusterARN("tagged")); !ok {
			t.Error("cluster from the first page missing")
		}
	})
	t.Run("after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		handlers := odbHandlers(newODBCalls())
		handlers["ListOdbNetworks"] = failOnCall(2, odbNotOnboarded, handlers["ListOdbNetworks"])

		total, _, err := odbScan(context.Background(), st, handlers)
		if err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		if total != odbFixtureTotal-1 || *warns != 0 {
			t.Errorf("total, warnings = %d, %d; want %d, 0", total, *warns, odbFixtureTotal-1)
		}
		if _, ok := odbRowOf(t, st, TypeODBOdbNetwork, odbARN("odb-network", "tagged")); !ok {
			t.Error("network from the page before the answer missing")
		}
	})
	t.Run("first page of a later listing", func(t *testing.T) {
		st := newTestStore(t)
		handlers := odbHandlers(newODBCalls())
		handlers["ListCloudExadataInfrastructures"] = func(any) (any, error) { return nil, odbNotOnboarded }

		total, _, err := odbScan(context.Background(), st, handlers)
		if err != nil {
			t.Fatalf("err = %v; want nil", err)
		}
		if total != odbFixtureTotal-4 {
			t.Errorf("total = %d; want %d (no infrastructures, no DB servers)", total, odbFixtureTotal-4)
		}
	})
}

// A denied listing warns under its op label and costs no other listing its
// rows.
func TestScanODB_DeniedListingWarnsUnderItsOp(t *testing.T) {
	for sdkOp, lost := range map[string]int{
		"ListCloudAutonomousVmClusters": 4, "ListCloudExadataInfrastructures": 4, "ListOdbNetworks": 2,
		"ListOdbPeeringConnections": 2, "ListCloudVmClusters": 6, "ListDbNodes": 4, "ListAutonomousDatabases": 6,
		"ListAutonomousDatabaseBackups": 4, "ListExascaleDbStorageVaults": 2, "ListExadbVmClusters": 2,
	} {
		t.Run(sdkOp, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, "odb:"+sdkOp)
			handlers := odbHandlers(newODBCalls())
			handlers[sdkOp] = func(any) (any, error) { return nil, odbDenied }

			total, _, err := odbScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != odbFixtureTotal-lost {
				t.Errorf("total = %d; want %d", total, odbFixtureTotal-lost)
			}
			if *opWarns != 1 || *warns != 1 {
				t.Errorf("odb:%s warnings, all warnings = %d, %d; want 1, 1", sdkOp, *opWarns, *warns)
			}
		})
	}
}

// A per-parent listing classifies each parent on its own: a parent deleted
// since it was listed is skipped silently, a hard error is returned, and the
// sibling's rows are stored either way.
func TestScanODB_PerParentListingFailures(t *testing.T) {
	for _, c := range []struct {
		sdkOp, typ, field string
		sibling           odbRowWant
	}{
		{"ListDbNodes", TypeODBDbNode, "CloudVmClusterId", odbRowWant{nativeID: odbARN("cloud-vm-cluster", "bare") + "/db-node/bare-tagged"}},
		{"ListAutonomousDatabaseBackups", TypeODBAutonomousDatabaseBackup, "AutonomousDatabaseId", odbRowWant{nativeID: odbARN("autonomous-database-backup", "bare-tagged")}},
	} {
		for _, f := range []struct {
			name    string
			err     error
			wantErr bool
		}{
			{"parent gone", odbGone, false},
			{"other error", odbBadInput, true},
		} {
			t.Run(c.sdkOp+"/"+f.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countWarnings(st)
				handlers := odbHandlers(newODBCalls())
				next := handlers[c.sdkOp]
				handlers[c.sdkOp] = func(in any) (any, error) {
					if sv(bacField[*string](in, c.field)) == "tagged" {
						return nil, f.err
					}
					return next(in)
				}

				total, _, err := odbScan(context.Background(), st, handlers)
				if f.wantErr {
					assertGlueOutcome(t, err, outcomeError)
					assertAPICode(t, err, "ValidationException")
				} else if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if total != odbFixtureTotal-2 || *warns != 0 {
					t.Errorf("total, warnings = %d, %d; want %d, 0", total, *warns, odbFixtureTotal-2)
				}
				if _, ok := odbRowOf(t, st, c.typ, c.sibling.nativeID); !ok {
					t.Errorf("sibling row %s missing", c.sibling.nativeID)
				}
			})
		}
	}
}

// A listing's failed page keeps the pages read before it, hands its stored
// parents to their child listing, and stops no later phase.
func TestScanODB_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	calls := newODBCalls()
	handlers := odbHandlers(calls)
	handlers["ListCloudExadataInfrastructures"] = failOnCall(2, odbBadInput, handlers["ListCloudExadataInfrastructures"])

	total, _, err := odbScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if total != odbFixtureTotal-2 {
		t.Errorf("total = %d; want %d (one infrastructure and its DB server lost)", total, odbFixtureTotal-2)
	}
	if _, ok := odbRowOf(t, st, TypeODBCloudExadataInfrastructure, odbInfraARN("tagged")); !ok {
		t.Error("infrastructure from the page before the failure missing")
	}
	assertQSContains(t, st, odbInfraARN("tagged"), odbInfraARN("tagged")+"/db-server/dbs")
	if _, ok := odbRowOf(t, st, TypeODBExadbVMCluster, odbARN("exadb-vm-cluster", "bare")); !ok {
		t.Error("last phase's row missing")
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanODB_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := odbHandlers(newODBCalls())
	handlers["ListOdbNetworks"] = func(any) (any, error) {
		return &odb.ListOdbNetworksOutput{OdbNetworks: []odbtypes.OdbNetworkSummary{{OdbNetworkArn: sdkaws.String(odbARN("odb-network", "tagged")), OdbNetworkId: sdkaws.String("tagged")}}, NextToken: sdkaws.String("same")}, nil
	}

	_, _, err := odbScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if _, ok := odbRowOf(t, st, TypeODBOdbNetwork, odbARN("odb-network", "tagged")); !ok {
		t.Error("network from the pages read missing")
	}
}

// An element without the ARN its NativeID is, and a DB node without its id,
// are warned about under their listing, not stored. A DB node without the
// DbNodeArn its tag read is sent is stored without tags, with one warning
// and no tag read.
func TestScanODB_UnstorableItemsWarn(t *testing.T) {
	st := newTestStore(t)
	netWarns := bacWarnings(st, "odb:ListOdbNetworks")
	nodeWarns := bacWarnings(st, "odb:ListDbNodes")
	tagWarns := bacWarnings(st, odbTagsOp)
	calls := newODBCalls()
	handlers := odbHandlers(calls)
	handlers["ListOdbNetworks"] = func(any) (any, error) {
		return &odb.ListOdbNetworksOutput{OdbNetworks: []odbtypes.OdbNetworkSummary{{OdbNetworkId: sdkaws.String("net-1")}}}, nil
	}
	handlers["ListDbNodes"] = func(in any) (any, error) {
		cluster := sv(in.(*odb.ListDbNodesInput).CloudVmClusterId)
		return &odb.ListDbNodesOutput{DbNodes: []odbtypes.DbNodeSummary{
			{DbNodeId: sdkaws.String(cluster + "-no-arn"), Hostname: sdkaws.String("h")},
			{DbNodeArn: sdkaws.String(odbARN("db-node", cluster+"-no-id"))},
		}}, nil
	}

	if _, _, err := odbScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := len(rowsByNativeID(t, st, TypeODBOdbNetwork)); got != 0 {
		t.Errorf("networks stored = %d; want 0", got)
	}
	if len(*netWarns) != 1 || !strings.HasSuffix((*netWarns)[0].Message, "first net-1") {
		t.Errorf("ListOdbNetworks warnings = %v; want one naming net-1", *netWarns)
	}
	if len(*nodeWarns) != 1 || !strings.HasSuffix((*nodeWarns)[0].Message, "first "+odbARN("db-node", "tagged-no-id")) {
		t.Errorf("ListDbNodes warnings = %v; want one naming the id-less node", *nodeWarns)
	}
	nodes := rowsByNativeID(t, st, TypeODBDbNode)
	noARN := odbARN("cloud-vm-cluster", "tagged") + "/db-node/tagged-no-arn"
	if r, ok := nodes[noARN]; len(nodes) != 2 || !ok || r.TagsJSON != nil {
		t.Errorf("DB nodes = %v; want the 2 ARN-less nodes stored without tags", nodes)
	}
	if len(*tagWarns) != 1 || !strings.HasSuffix((*tagWarns)[0].Message, "first "+noARN) {
		t.Errorf("%s warnings = %v; want one naming %s", odbTagsOp, *tagWarns, noARN)
	}
	for _, arn := range calls.tagARNs {
		if strings.Contains(arn, "db-node") {
			t.Errorf("tag read sent %s; want none for an ARN-less node", arn)
		}
	}
}

// Exascale listings drop TERMINATED elements and keep TERMINATING ones. An
// op the region's endpoint does not route is a silent gap on the first page;
// after a page it is an error that keeps the rows read.
func TestScanODB_Exascale(t *testing.T) {
	vault := func(id string, s odbtypes.ResourceStatus) odbtypes.ExascaleDbStorageVaultSummary {
		return odbtypes.ExascaleDbStorageVaultSummary{ExascaleDbStorageVaultArn: sdkaws.String(odbARN("exascale-db-storage-vault", id)), ExascaleDbStorageVaultId: sdkaws.String(id), Status: s}
	}
	cluster := func(id string, s odbtypes.ResourceStatus) odbtypes.ExadbVmClusterSummary {
		return odbtypes.ExadbVmClusterSummary{ExadbVmClusterArn: sdkaws.String(odbARN("exadb-vm-cluster", id)), ExadbVmClusterId: sdkaws.String(id), Status: s}
	}
	t.Run("terminated dropped", func(t *testing.T) {
		st := newTestStore(t)
		calls := newODBCalls()
		handlers := odbHandlers(calls)
		handlers["ListExascaleDbStorageVaults"] = func(any) (any, error) {
			return &odb.ListExascaleDbStorageVaultsOutput{ExascaleDbStorageVaults: []odbtypes.ExascaleDbStorageVaultSummary{
				vault("gone", odbtypes.ResourceStatusTerminated), vault("going", odbtypes.ResourceStatusTerminating),
			}}, nil
		}
		handlers["ListExadbVmClusters"] = func(any) (any, error) {
			return &odb.ListExadbVmClustersOutput{ExadbVmClusters: []odbtypes.ExadbVmClusterSummary{
				cluster("gone", odbtypes.ResourceStatusTerminated), cluster("going", odbtypes.ResourceStatusTerminating),
			}}, nil
		}
		if _, _, err := odbScan(context.Background(), st, handlers); err != nil {
			t.Fatalf("scan: %v", err)
		}
		for typ, kind := range map[string]string{TypeODBExascaleDbStorageVault: "exascale-db-storage-vault", TypeODBExadbVMCluster: "exadb-vm-cluster"} {
			rows := rowsByNativeID(t, st, typ)
			if _, ok := rows[odbARN(kind, "going")]; !ok || len(rows) != 1 {
				t.Errorf("%s rows = %v; want only the TERMINATING one", typ, rows)
			}
			if slices.Contains(calls.tagARNs, odbARN(kind, "gone")) {
				t.Errorf("tags read for the TERMINATED %s", kind)
			}
		}
	})
	for _, sdkOp := range []string{"ListExascaleDbStorageVaults", "ListExadbVmClusters"} {
		t.Run(sdkOp+"/not deployed on the first page", func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			handlers := odbHandlers(newODBCalls())
			handlers[sdkOp] = func(any) (any, error) { return nil, apiErr("UnknownOperationException", "") }
			total, _, err := odbScan(context.Background(), st, handlers)
			if err != nil || total != odbFixtureTotal-2 || *warns != 0 {
				t.Errorf("scan = (%d, %v) with %d warnings; want (%d, nil) and none", total, err, *warns, odbFixtureTotal-2)
			}
		})
		t.Run(sdkOp+"/not deployed after a page", func(t *testing.T) {
			st := newTestStore(t)
			handlers := odbHandlers(newODBCalls())
			handlers[sdkOp] = failOnCall(2, apiErr("UnknownOperationException", ""), handlers[sdkOp])
			total, _, err := odbScan(context.Background(), st, handlers)
			assertAPICode(t, err, "UnknownOperationException")
			if total != odbFixtureTotal-1 {
				t.Errorf("total = %d; want %d (the first page kept)", total, odbFixtureTotal-1)
			}
		})
	}
	t.Run("not deployed is not a gap for the other listings", func(t *testing.T) {
		st := newTestStore(t)
		handlers := odbHandlers(newODBCalls())
		handlers["ListOdbNetworks"] = func(any) (any, error) { return nil, apiErr("UnknownOperationException", "") }
		_, _, err := odbScan(context.Background(), st, handlers)
		assertAPICode(t, err, "UnknownOperationException")
	})
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanODB_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeODBOdbNetwork)
	calls := newODBCalls()
	calls.errs[odbARN("odb-network", "tagged")] = odbDenied
	calls.errs[odbARN("odb-network", "bare")] = odbThrottle

	_, _, err := odbScan(context.Background(), st, odbHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	assertAPICode(t, err, "ThrottlingException")
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	if n := calls.count("ListOdbPeeringConnections"); n != 0 {
		t.Errorf("ListOdbPeeringConnections called %d times after the failed write", n)
	}
}

// A failed DB-node write sends no warning for the ARN-less node either.
func TestScanODB_FailedNodeWriteSendsNoNodeWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeODBDbNode)
	handlers := odbHandlers(newODBCalls())
	handlers["ListDbNodes"] = func(in any) (any, error) {
		return &odb.ListDbNodesOutput{DbNodes: []odbtypes.DbNodeSummary{{DbNodeId: sdkaws.String("n")}}}, nil
	}

	_, _, err := odbScan(context.Background(), st, handlers)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
}

// Once ctx ends no further phase starts, and the stop is reported once,
// naming the phases not run, although the phase in flight saw it.
func TestScanODB_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newODBCalls()
	handlers := odbHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	infras := handlers["ListCloudExadataInfrastructures"]
	handlers["ListCloudExadataInfrastructures"] = func(in any) (any, error) {
		if in.(*odb.ListCloudExadataInfrastructuresInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return infras(in)
	}

	_, _, err := odbScan(ctx, st, handlers)
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
	if n := calls.count("ListOdbNetworks") + calls.count("ListExadbVmClusters"); n != 0 {
		t.Errorf("later listings called %d times after ctx ended", n)
	}
	if !strings.Contains(err.Error(), "odb: 10 phases not run") {
		t.Errorf("err = %v; want it to say 10 phases not run", err)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanODB_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "odb.us-east-1.api.aws", IsNotFound: true}
	t.Run("later phase after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		handlers := odbHandlers(newODBCalls())
		handlers["ListOdbNetworks"] = func(any) (any, error) { return nil, nx }
		total, _, err := odbScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != odbFixtureTotal-2 {
			t.Errorf("total = %d; want %d", total, odbFixtureTotal-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newODBCalls()
		calls.errs[odbAVMClusterARN("bare")] = nx
		_, _, err := odbScan(context.Background(), st, odbHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanODB(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
	})
}
