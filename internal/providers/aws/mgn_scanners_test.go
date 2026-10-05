package aws

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/mgn"
	mgntypes "github.com/aws/aws-sdk-go-v2/service/mgn/types"
	"github.com/icearp/disco-cli/store"
)

// stubMGN serves source servers and launch configuration templates from pages,
// actions from pages keyed by "<Op>:<parent id>", and every other list as the
// single row mgnOtherARNs names for its type.
// errs[key] fails the first call of that key ("<Op>" for account-wide lists).
// Every call is recorded in queried as its key.
type stubMGN struct {
	t               *testing.T
	servers         [][]mgntypes.SourceServer
	templates       [][]mgntypes.LaunchConfigurationTemplate
	serverActions   map[string][][]mgntypes.SourceServerActionDocument
	templateActions map[string][][]mgntypes.TemplateActionDocument
	errs            map[string]error

	mu      sync.Mutex
	queried []string
}

func mgnStubPage[T any](s *stubMGN, key string, pages [][]T, token *string) ([]T, *string, error) {
	s.mu.Lock()
	s.queried = append(s.queried, key)
	s.mu.Unlock()
	if token == nil {
		if err := s.errs[key]; err != nil {
			return nil, nil, err
		}
	} else if _, err := strconv.Atoi(*token); err != nil {
		s.t.Errorf("stub page token %q: %v", *token, err)
	}
	return smPage(pages, token)
}

func (s *stubMGN) calls(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, q := range s.queried {
		if q == key {
			n++
		}
	}
	return n
}

func (s *stubMGN) DescribeSourceServers(_ context.Context, in *mgn.DescribeSourceServersInput, _ ...func(*mgn.Options)) (*mgn.DescribeSourceServersOutput, error) {
	items, next, err := mgnStubPage(s, "DescribeSourceServers", s.servers, in.NextToken)
	if err != nil {
		return nil, err
	}
	return &mgn.DescribeSourceServersOutput{Items: items, NextToken: next}, nil
}

func (s *stubMGN) DescribeLaunchConfigurationTemplates(_ context.Context, in *mgn.DescribeLaunchConfigurationTemplatesInput, _ ...func(*mgn.Options)) (*mgn.DescribeLaunchConfigurationTemplatesOutput, error) {
	items, next, err := mgnStubPage(s, "DescribeLaunchConfigurationTemplates", s.templates, in.NextToken)
	if err != nil {
		return nil, err
	}
	return &mgn.DescribeLaunchConfigurationTemplatesOutput{Items: items, NextToken: next}, nil
}

func (s *stubMGN) ListSourceServerActions(_ context.Context, in *mgn.ListSourceServerActionsInput, _ ...func(*mgn.Options)) (*mgn.ListSourceServerActionsOutput, error) {
	key := sv(in.SourceServerID)
	items, next, err := mgnStubPage(s, "ListSourceServerActions:"+key, s.serverActions[key], in.NextToken)
	if err != nil {
		return nil, err
	}
	return &mgn.ListSourceServerActionsOutput{Items: items, NextToken: next}, nil
}

func (s *stubMGN) ListTemplateActions(_ context.Context, in *mgn.ListTemplateActionsInput, _ ...func(*mgn.Options)) (*mgn.ListTemplateActionsOutput, error) {
	key := sv(in.LaunchConfigurationTemplateID)
	items, next, err := mgnStubPage(s, "ListTemplateActions:"+key, s.templateActions[key], in.NextToken)
	if err != nil {
		return nil, err
	}
	return &mgn.ListTemplateActionsOutput{Items: items, NextToken: next}, nil
}

func (*stubMGN) ListApplications(context.Context, *mgn.ListApplicationsInput, ...func(*mgn.Options)) (*mgn.ListApplicationsOutput, error) {
	return &mgn.ListApplicationsOutput{Items: []mgntypes.Application{{Arn: sdkaws.String(mgnOtherARNs[TypeMGNApplication])}}}, nil
}

func (*stubMGN) ListWaves(context.Context, *mgn.ListWavesInput, ...func(*mgn.Options)) (*mgn.ListWavesOutput, error) {
	return &mgn.ListWavesOutput{Items: []mgntypes.Wave{{Arn: sdkaws.String(mgnOtherARNs[TypeMGNWave])}}}, nil
}

func (*stubMGN) ListConnectors(context.Context, *mgn.ListConnectorsInput, ...func(*mgn.Options)) (*mgn.ListConnectorsOutput, error) {
	return &mgn.ListConnectorsOutput{Items: []mgntypes.Connector{{Arn: sdkaws.String(mgnOtherARNs[TypeMGNConnector])}}}, nil
}

func (*stubMGN) DescribeReplicationConfigurationTemplates(context.Context, *mgn.DescribeReplicationConfigurationTemplatesInput, ...func(*mgn.Options)) (*mgn.DescribeReplicationConfigurationTemplatesOutput, error) {
	return &mgn.DescribeReplicationConfigurationTemplatesOutput{Items: []mgntypes.ReplicationConfigurationTemplate{
		{Arn: sdkaws.String(mgnOtherARNs[TypeMGNReplicationConfigurationTemplate])},
	}}, nil
}

func (*stubMGN) DescribeVcenterClients(context.Context, *mgn.DescribeVcenterClientsInput, ...func(*mgn.Options)) (*mgn.DescribeVcenterClientsOutput, error) {
	return &mgn.DescribeVcenterClientsOutput{Items: []mgntypes.VcenterClient{{Arn: sdkaws.String(mgnOtherARNs[TypeMGNVcenterClient])}}}, nil
}

func (*stubMGN) ListNetworkMigrationDefinitions(context.Context, *mgn.ListNetworkMigrationDefinitionsInput, ...func(*mgn.Options)) (*mgn.ListNetworkMigrationDefinitionsOutput, error) {
	return &mgn.ListNetworkMigrationDefinitionsOutput{Items: []mgntypes.NetworkMigrationDefinitionSummary{
		{Arn: sdkaws.String(mgnOtherARNs[TypeMGNNetworkMigrationDefinition])},
	}}, nil
}

func mgnARN(kind, id string) string {
	return "arn:aws:mgn:" + testRegion + ":" + testAccountID + ":" + kind + "/" + id
}

// mgnOtherARNs is the one row the stub serves for each list that neither has
// action children nor is an action parent.
var mgnOtherARNs = map[string]string{
	TypeMGNApplication:                      mgnARN("application", "app-1"),
	TypeMGNWave:                             mgnARN("wave", "wave-1"),
	TypeMGNConnector:                        mgnARN("connector", "conn-1"),
	TypeMGNReplicationConfigurationTemplate: mgnARN("replication-configuration-template", "rct-1"),
	TypeMGNVcenterClient:                    mgnARN("vcenter-client", "vc-1"),
	TypeMGNNetworkMigrationDefinition:       mgnARN("network-migration-definition", "nmd-1"),
}

func mgnServer(id string) mgntypes.SourceServer {
	return mgntypes.SourceServer{Arn: sdkaws.String(mgnARN("source-server", id)), SourceServerID: sdkaws.String(id)}
}

func mgnTemplate(id string) mgntypes.LaunchConfigurationTemplate {
	return mgntypes.LaunchConfigurationTemplate{Arn: sdkaws.String(mgnARN("launch-configuration-template", id)), LaunchConfigurationTemplateID: sdkaws.String(id)}
}

func runMGN(t *testing.T, st *store.Store, stub *stubMGN) (int, error) {
	t.Helper()
	total, _, err := scanMGNWithClient(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

// Every source server's actions are listed (both pages where there are two),
// actions missing their id are dropped, a server missing its id is stored but
// not listed under, a server deleted mid-scan is skipped, and each action is
// contained by its server.
func TestScanMGN_SourceServerActions(t *testing.T) {
	st := newTestStore(t)
	warnings := countSMWarnings(st)
	s := sdkaws.String
	noID := mgntypes.SourceServer{Arn: s(mgnARN("source-server", "s-noid"))}
	stub := &stubMGN{
		t:       t,
		servers: [][]mgntypes.SourceServer{{mgnServer("s-1"), noID}, {mgnServer("s-2"), mgnServer("s-gone")}},
		serverActions: map[string][][]mgntypes.SourceServerActionDocument{
			"s-1": {
				{{ActionID: s("a-1"), ActionName: s("install agent"), DocumentIdentifier: s("AWS-RunShellScript")}, {ActionName: s("no id")}},
				{{ActionID: s("a-2")}},
			},
			"s-2": {{{ActionID: s("a-1")}}},
		},
		errs: map[string]error{"ListSourceServerActions:s-gone": apiErr("ResourceNotFoundException", "source server not found")},
	}
	total, err := runMGN(t, st, stub)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	s1, s2 := mgnARN("source-server", "s-1"), mgnARN("source-server", "s-2")
	a1, a2, a3 := s1+"/action/a-1", s1+"/action/a-2", s2+"/action/a-1"
	if want := 4 + len(mgnOtherARNs) + 3; total != want {
		t.Errorf("total = %d; want %d (servers + other lists + actions)", total, want)
	}
	assertSMIDs(t, st, TypeMGNSourceServerAction, a1, a2, a3)
	for key, want := range map[string]int{
		"ListSourceServerActions:s-1": 2, "ListSourceServerActions:s-2": 1, "ListSourceServerActions:s-gone": 1,
		"ListSourceServerActions:": 0,
	} {
		if got := stub.calls(key); got != want {
			t.Errorf("calls %s = %d; want %d", key, got, want)
		}
	}
	rows := smStoredRows(t, st, TypeMGNSourceServerAction)
	if got := sv(rows[a1].Name); got != "install agent" {
		t.Errorf("name = %q; want install agent", got)
	}
	if got := mhoAttrs(t, rows[a1])["DocumentIdentifier"]; got != "AWS-RunShellScript" {
		t.Errorf("attrs DocumentIdentifier = %v; want AWS-RunShellScript", got)
	}
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, s1), a1)
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, s1), a2)
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, s2), a3)
	if *warnings != 0 {
		t.Errorf("warnings = %d; want 0", *warnings)
	}
}

func TestScanMGN_TemplateActions(t *testing.T) {
	st := newTestStore(t)
	warnings := countSMWarnings(st)
	s := sdkaws.String
	noID := mgntypes.LaunchConfigurationTemplate{Arn: s(mgnARN("launch-configuration-template", "lt-noid"))}
	stub := &stubMGN{
		t:         t,
		templates: [][]mgntypes.LaunchConfigurationTemplate{{mgnTemplate("lt-1"), noID}, {mgnTemplate("lt-2"), mgnTemplate("lt-gone")}},
		templateActions: map[string][][]mgntypes.TemplateActionDocument{
			"lt-1": {
				{{ActionID: s("a-1"), ActionName: s("harden"), OperatingSystem: s("LINUX")}, {ActionName: s("no id")}},
				{{ActionID: s("a-2")}},
			},
			"lt-2": {{{ActionID: s("a-1")}}},
		},
		errs: map[string]error{"ListTemplateActions:lt-gone": apiErr("ResourceNotFoundException", "template not found")},
	}
	total, err := runMGN(t, st, stub)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	t1, t2 := mgnARN("launch-configuration-template", "lt-1"), mgnARN("launch-configuration-template", "lt-2")
	a1, a2, a3 := t1+"/action/a-1", t1+"/action/a-2", t2+"/action/a-1"
	if want := 4 + len(mgnOtherARNs) + 3; total != want {
		t.Errorf("total = %d; want %d (templates + other lists + actions)", total, want)
	}
	assertSMIDs(t, st, TypeMGNTemplateAction, a1, a2, a3)
	for key, want := range map[string]int{
		"ListTemplateActions:lt-1": 2, "ListTemplateActions:lt-2": 1, "ListTemplateActions:lt-gone": 1,
		"ListTemplateActions:": 0,
	} {
		if got := stub.calls(key); got != want {
			t.Errorf("calls %s = %d; want %d", key, got, want)
		}
	}
	rows := smStoredRows(t, st, TypeMGNTemplateAction)
	if got := sv(rows[a1].Name); got != "harden" {
		t.Errorf("name = %q; want harden", got)
	}
	if got := mhoAttrs(t, rows[a1])["OperatingSystem"]; got != "LINUX" {
		t.Errorf("attrs OperatingSystem = %v; want LINUX", got)
	}
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, t1), a1)
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, t1), a2)
	assertSMContains(t, st, store.ResourceID("aws", testAccountID, t2), a3)
	if *warnings != 0 {
		t.Errorf("warnings = %d; want 0", *warnings)
	}
}

// Each action op: no parents and parents without actions store nothing; an
// AccessDenied on every parent warns once and fails nothing; any other error
// propagates, and every one of the eight pre-existing types still has its rows
// stored, so each action phase runs after all pre-existing phases.
func TestScanMGN_ActionErrorShapes(t *testing.T) {
	ops := []struct {
		op    string
		rtype string
	}{
		{"ListSourceServerActions", TypeMGNSourceServerAction},
		{"ListTemplateActions", TypeMGNTemplateAction},
	}
	cases := []struct {
		name         string
		parents      bool
		err          error
		wantWarnings int
		wantErrCode  string
	}{
		{name: "no parents"},
		{name: "parents without actions", parents: true},
		{name: "access denied warns once", parents: true, err: apiErr("AccessDeniedException", "not authorized"), wantWarnings: 1},
		{name: "other error propagates", parents: true, err: apiErr("InternalServerException", "boom"), wantErrCode: "InternalServerException"},
	}
	for _, o := range ops {
		for _, tc := range cases {
			t.Run(o.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				warnings := countSMWarnings(st)
				stub := &stubMGN{t: t, errs: map[string]error{}}
				if tc.parents {
					stub.servers = [][]mgntypes.SourceServer{{mgnServer("p-1"), mgnServer("p-2")}}
					stub.templates = [][]mgntypes.LaunchConfigurationTemplate{{mgnTemplate("p-1"), mgnTemplate("p-2")}}
				}
				if tc.err != nil {
					stub.errs[o.op+":p-1"] = tc.err
					stub.errs[o.op+":p-2"] = tc.err
				}
				_, err := runMGN(t, st, stub)
				switch {
				case tc.wantErrCode != "":
					if !isAPIErrorCode(err, tc.wantErrCode) {
						t.Fatalf("err = %v; want wrapped %s", err, tc.wantErrCode)
					}
				case err != nil:
					t.Fatalf("scan: %v", err)
				}
				if *warnings != tc.wantWarnings {
					t.Errorf("warnings = %d; want %d", *warnings, tc.wantWarnings)
				}
				assertSMIDs(t, st, o.rtype)
				if tc.parents {
					assertSMIDs(t, st, TypeMGNSourceServer, mgnARN("source-server", "p-1"), mgnARN("source-server", "p-2"))
					assertSMIDs(t, st, TypeMGNLaunchConfigurationTemplate,
						mgnARN("launch-configuration-template", "p-1"), mgnARN("launch-configuration-template", "p-2"))
					for rtype, arn := range mgnOtherARNs {
						assertSMIDs(t, st, rtype, arn)
					}
				}
			})
		}
	}
}

func TestScanMGN_UninitializedAccountDisabled(t *testing.T) {
	st := newTestStore(t)
	stub := &stubMGN{t: t, errs: map[string]error{"DescribeSourceServers": apiErr("UninitializedAccountException", "Account not initialized")}}
	if _, err := runMGN(t, st, stub); !errors.Is(err, errServiceDisabled) {
		t.Fatalf("err = %v; want service-disabled sentinel", err)
	}
}
