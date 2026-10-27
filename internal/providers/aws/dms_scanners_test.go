package aws

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/databasemigrationservice"
	dmstypes "github.com/aws/aws-sdk-go-v2/service/databasemigrationservice/types"
	"github.com/icearp/disco-cli/store"
)

var (
	dmsDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: dms:Describe")
	dmsNotFound = apiErr("ResourceNotFoundFault", "gone")
	dmsBadState = apiErr("InvalidResourceStateFault", "busy")
	dmsThrottle = apiErr("ThrottlingException", "slow down")
)

const dmsTagsOp = "dms:ListTagsForResource"

// dmsKind is one listing of the fixture: the Describe op, the type it stores,
// the DMS ARN resource kind, whether the op is sent the documented page cap,
// and page, which builds the op's output from an element's ARN and
// identifier and the next Marker.
type dmsKind struct {
	op, typ, kind string
	capped        bool
	page          func(arn, id *string, next *string) any
}

func dmsKinds() []dmsKind {
	return []dmsKind{
		{"DescribeCertificates", TypeDMSCertificate, "cert", false, func(arn, id, next *string) any {
			return &databasemigrationservice.DescribeCertificatesOutput{Certificates: []dmstypes.Certificate{{CertificateArn: arn, CertificateIdentifier: id}}, Marker: next}
		}},
		{"DescribeDataMigrations", TypeDMSDataMigration, "data-migration", false, func(arn, id, next *string) any {
			return &databasemigrationservice.DescribeDataMigrationsOutput{DataMigrations: []dmstypes.DataMigration{{DataMigrationArn: arn, DataMigrationName: id}}, Marker: next}
		}},
		{"DescribeDataProviders", TypeDMSDataProvider, "data-provider", false, func(arn, id, next *string) any {
			return &databasemigrationservice.DescribeDataProvidersOutput{DataProviders: []dmstypes.DataProvider{{DataProviderArn: arn, DataProviderName: id}}, Marker: next}
		}},
		{"DescribeEndpoints", TypeDMSEndpoint, "endpoint", true, func(arn, id, next *string) any {
			return &databasemigrationservice.DescribeEndpointsOutput{Endpoints: []dmstypes.Endpoint{{EndpointArn: arn, EndpointIdentifier: id}}, Marker: next}
		}},
		{"DescribeEventSubscriptions", TypeDMSEventSubscription, "es", true, func(_, id, next *string) any {
			return &databasemigrationservice.DescribeEventSubscriptionsOutput{EventSubscriptionsList: []dmstypes.EventSubscription{{CustSubscriptionId: id}}, Marker: next}
		}},
		{"DescribeInstanceProfiles", TypeDMSInstanceProfile, "instance-profile", false, func(arn, id, next *string) any {
			return &databasemigrationservice.DescribeInstanceProfilesOutput{InstanceProfiles: []dmstypes.InstanceProfile{{InstanceProfileArn: arn, InstanceProfileName: id}}, Marker: next}
		}},
		{"DescribeMigrationProjects", TypeDMSMigrationProject, "migration-project", false, func(arn, id, next *string) any {
			return &databasemigrationservice.DescribeMigrationProjectsOutput{MigrationProjects: []dmstypes.MigrationProject{{MigrationProjectArn: arn, MigrationProjectName: id}}, Marker: next}
		}},
		{"DescribeReplicationConfigs", TypeDMSReplicationConfig, "replication-config", false, func(arn, id, next *string) any {
			return &databasemigrationservice.DescribeReplicationConfigsOutput{ReplicationConfigs: []dmstypes.ReplicationConfig{{ReplicationConfigArn: arn, ReplicationConfigIdentifier: id}}, Marker: next}
		}},
		{"DescribeReplicationInstances", TypeDMSReplicationInstance, "rep", true, func(arn, id, next *string) any {
			return &databasemigrationservice.DescribeReplicationInstancesOutput{ReplicationInstances: []dmstypes.ReplicationInstance{{ReplicationInstanceArn: arn, ReplicationInstanceIdentifier: id}}, Marker: next}
		}},
		{"DescribeReplicationSubnetGroups", TypeDMSReplicationSubnetGroup, "subgrp", true, func(_, id, next *string) any {
			return &databasemigrationservice.DescribeReplicationSubnetGroupsOutput{ReplicationSubnetGroups: []dmstypes.ReplicationSubnetGroup{{ReplicationSubnetGroupIdentifier: id}}, Marker: next}
		}},
		{"DescribeReplicationTasks", TypeDMSReplicationTask, "task", true, func(arn, id, next *string) any {
			return &databasemigrationservice.DescribeReplicationTasksOutput{ReplicationTasks: []dmstypes.ReplicationTask{{ReplicationTaskArn: arn, ReplicationTaskIdentifier: id}}, Marker: next}
		}},
	}
}

func dmsKindOf(t *testing.T, op string) dmsKind {
	t.Helper()
	for _, k := range dmsKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("no fixture kind for %s", op)
	return dmsKind{}
}

// dmsID is the identifier of the fixture's resource name ("tagged" or
// "bare") of kind k; it names the row.
func dmsID(k dmsKind, name string) string { return k.kind + "-" + name }

// dmsARN is the ARN of the fixture's resource name of kind k in the test
// account and region.
func dmsARN(k dmsKind, name string) string {
	return "arn:aws:dms:" + testRegion + ":" + testAccountID + ":" + k.kind + ":" + dmsID(k, name)
}

// dmsCalls records the stubbed DMS calls: how often each op ran, the
// MaxRecords each Describe op was sent (nil when none), and the ARNs sent to
// ListTagsForResource, which it answers from tags or errs, both keyed by ARN.
// The tag reads run concurrently.
type dmsCalls struct {
	mu         sync.Mutex
	ops        map[string]int
	maxRecords map[string][]*int32
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

// newDMSCalls tags every "tagged" resource of the fixture with its type.
func newDMSCalls() *dmsCalls {
	c := &dmsCalls{ops: map[string]int{}, maxRecords: map[string][]*int32{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range dmsKinds() {
		c.tags[dmsARN(k, "tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

func (c *dmsCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

func (c *dmsCalls) listTags(in any) (any, error) {
	req := in.(*databasemigrationservice.ListTagsForResourceInput)
	arn := sv(req.ResourceArn)
	c.mu.Lock()
	c.ops["ListTagsForResource"]++
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if len(req.ResourceArnList) > 0 {
		return nil, errors.New("test: ResourceArnList sent")
	}
	if err != nil {
		return nil, err
	}
	var list []dmstypes.Tag
	for k, v := range tags {
		list = append(list, dmstypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v), ResourceArn: sdkaws.String(arn)})
	}
	return &databasemigrationservice.ListTagsForResourceOutput{TagList: list}, nil
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second, in partition.
func (c *dmsCalls) twoPages(k dmsKind, partition string) func(any) (any, error) {
	return func(in any) (any, error) {
		marker := bacField[*string](in, "Marker")
		c.mu.Lock()
		c.ops[k.op]++
		c.maxRecords[k.op] = append(c.maxRecords[k.op], bacField[*int32](in, "MaxRecords"))
		c.mu.Unlock()
		name, next := "tagged", sdkaws.String("m2")
		if marker != nil {
			name, next = "bare", nil
		}
		id := dmsID(k, name)
		arn := "arn:" + partition + ":dms:" + testRegion + ":" + testAccountID + ":" + k.kind + ":" + id
		return k.page(&arn, &id, next), nil
	}
}

// dmsHandlers answers every op the scan calls with the fixture: two
// resources of every kind.
func dmsHandlers(c *dmsCalls) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){"ListTagsForResource": c.listTags}
	for _, k := range dmsKinds() {
		h[k.op] = c.twoPages(k, "aws")
	}
	return h
}

// dmsScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func dmsScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, dmsDenied)
	return scanDMS(ctx, acct, testRegion, st, testScanID)
}

func dmsRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// Every type stores both pages of its listing, named by its identifier, with
// the tags ListTagsForResource returns for its ARN, and nil for a resource
// without tags; exactly those ARNs are sent, one per call. The five ops whose
// docs state a page maximum are sent 100; the others are sent none. The scan
// reports no error or warning.
func TestScanDMS_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newDMSCalls()

	total, inserted, err := dmsScan(context.Background(), st, dmsHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 22 || inserted != 22 {
		t.Errorf("total, inserted = %d, %d; want 22, 22", total, inserted)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantARNs []string
	for _, k := range dmsKinds() {
		for _, name := range []string{"tagged", "bare"} {
			arn := dmsARN(k, name)
			wantARNs = append(wantARNs, arn)
			r, ok := dmsRowOf(t, st, k.typ, arn)
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			if got := sv(r.Name); got != dmsID(k, name) {
				t.Errorf("%s %s Name = %q; want %q", k.typ, arn, got, dmsID(k, name))
			}
			if name == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s %s TagsJSON = %q; want nil", k.typ, arn, *r.TagsJSON)
			}
		}
		got := calls.maxRecords[k.op]
		if len(got) != 2 {
			t.Errorf("%s called %d times; want 2", k.op, len(got))
		}
		for _, n := range got {
			switch {
			case k.capped && (n == nil || *n != 100):
				t.Errorf("%s MaxRecords = %v; want 100", k.op, n)
			case !k.capped && n != nil:
				t.Errorf("%s MaxRecords = %d; want none", k.op, *n)
			}
		}
	}
	slices.Sort(wantARNs)
	slices.Sort(calls.tagARNs)
	if !slices.Equal(calls.tagARNs, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", calls.tagARNs, wantARNs)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanDMS_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newDMSCalls()
	if _, _, err := dmsScan(context.Background(), st, dmsHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := dmsScan(context.Background(), st, dmsHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != 22 || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want 22, 0", total, inserted)
	}
}

// A failed tag read: a denial keeps the row untagged and warns once under
// dms:ListTagsForResource; a throttle after retries keeps it and is a
// transient error; InvalidResourceStateFault, the op's other modeled code,
// keeps it and is a hard error. ResourceNotFoundFault, the op's modeled
// not-found, drops a row whose NativeID is its listed ARN silently, but keeps
// a row whose ARN is synthesized and is a hard error: there it more likely
// means the ARN is wrong.
func TestScanDMS_TagReadFailures(t *testing.T) {
	for _, target := range []struct {
		op        string
		synthetic bool
	}{
		{"DescribeEndpoints", false},
		{"DescribeEventSubscriptions", true},
		{"DescribeReplicationSubnetGroups", true},
	} {
		k := dmsKindOf(t, target.op)
		arn := dmsARN(k, "tagged")
		notFound := struct {
			outcome   serviceOutcome
			code      string
			wantTotal int
			wantKept  bool
		}{0, "", 21, false}
		if target.synthetic {
			notFound.outcome, notFound.code, notFound.wantTotal, notFound.wantKept = outcomeError, "ResourceNotFoundFault", 22, true
		}
		t.Run(k.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				code      string
				wantTotal int
				wantKept  bool
				wantWarns int
			}{
				{"not found", dmsNotFound, notFound.outcome, notFound.code, notFound.wantTotal, notFound.wantKept, 0},
				{"denied", dmsDenied, 0, "", 22, true, 1},
				{"transient", dmsThrottle, outcomeTransient, "", 22, true, 0},
				{"other error", dmsBadState, outcomeError, "InvalidResourceStateFault", 22, true, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					all := countWarnings(st)
					warnings := bacWarnings(st, dmsTagsOp)
					calls := newDMSCalls()
					calls.errs[arn] = c.err

					total, _, err := dmsScan(context.Background(), st, dmsHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.code != "" {
						assertAPICode(t, err, c.code)
					}
					if total != c.wantTotal {
						t.Errorf("total = %d; want %d", total, c.wantTotal)
					}
					r, ok := dmsRowOf(t, st, k.typ, arn)
					if ok != c.wantKept {
						t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if len(*warnings) != c.wantWarns || *all != c.wantWarns {
						t.Fatalf("%s warnings = %v (all ops %d); want %d", dmsTagsOp, *warnings, *all, c.wantWarns)
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
func TestScanDMS_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, dmsTagsOp)
	calls := newDMSCalls()
	calls.errs[dmsARN(dmsKindOf(t, "DescribeReplicationTasks"), "tagged")] = dmsThrottle
	calls.errs[dmsARN(dmsKindOf(t, "DescribeCertificates"), "bare")] = dmsBadState

	_, _, err := dmsScan(context.Background(), st, dmsHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidResourceStateFault")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", dmsTagsOp, *warns)
	}
}

// A denied listing warns once under its own op, stores nothing of its type,
// and every other listing still runs and stores its rows.
func TestScanDMS_DeniedListingWarnsUnderItsOp(t *testing.T) {
	for _, k := range dmsKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warnings := bacWarnings(st, "dms:"+k.op)
			handlers := dmsHandlers(newDMSCalls())
			delete(handlers, k.op)

			total, _, err := dmsScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != 20 {
				t.Errorf("total = %d; want 20", total)
			}
			if len(*warnings) != 1 || *all != 1 {
				t.Errorf("dms:%s warnings = %v (all ops %d); want 1", k.op, *warnings, *all)
			}
			if got := len(rowsByNativeID(t, st, k.typ)); got != 0 {
				t.Errorf("%s rows = %d; want 0", k.typ, got)
			}
		})
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanDMS_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeDMSEndpoint)
	calls := newDMSCalls()
	calls.errs[dmsARN(dmsKindOf(t, "DescribeEndpoints"), "tagged")] = dmsDenied

	_, _, err := dmsScan(context.Background(), st, dmsHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	if n := calls.count("DescribeEventSubscriptions"); n != 0 {
		t.Errorf("DescribeEventSubscriptions called %d times after the failed write", n)
	}
}

// A listing's failed page keeps the pages read before it, and its error does
// not stop the listings after it.
func TestScanDMS_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	handlers := dmsHandlers(newDMSCalls())
	handlers["DescribeCertificates"] = failOnCall(2, dmsBadState, handlers["DescribeCertificates"])

	total, _, err := dmsScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidResourceStateFault")
	if total != 21 {
		t.Errorf("total = %d; want 21", total)
	}
	k := dmsKindOf(t, "DescribeCertificates")
	if _, ok := dmsRowOf(t, st, k.typ, dmsARN(k, "tagged")); !ok {
		t.Error("certificate from the page before the failure missing")
	}
	if got := len(rowsByNativeID(t, st, TypeDMSReplicationTask)); got != 2 {
		t.Errorf("replication tasks stored = %d; want 2", got)
	}
}

// A repeated Marker ends the listing as an error, keeping its pages.
func TestScanDMS_RepeatedMarkerKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := dmsHandlers(newDMSCalls())
	k := dmsKindOf(t, "DescribeEndpoints")
	handlers[k.op] = func(in any) (any, error) {
		name := "tagged"
		if bacField[*string](in, "Marker") != nil {
			name = "bare"
		}
		arn, id := dmsARN(k, name), dmsID(k, name)
		return k.page(&arn, &id, sdkaws.String("same")), nil
	}

	total, _, err := dmsScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != 22 {
		t.Errorf("total = %d; want 22", total)
	}
	for _, name := range []string{"tagged", "bare"} {
		if _, ok := dmsRowOf(t, st, k.typ, dmsARN(k, name)); !ok {
			t.Errorf("endpoint %s from the pages read missing", name)
		}
	}
}

// An element without the field its NativeID is built from is warned about
// under its listing, not stored; one with an ARN but no identifier is stored,
// named by its ARN.
func TestScanDMS_UnstorableItemsWarn(t *testing.T) {
	st := newTestStore(t)
	all := countWarnings(st)
	certWarns := bacWarnings(st, "dms:DescribeCertificates")
	esWarns := bacWarnings(st, "dms:DescribeEventSubscriptions")
	sgWarns := bacWarnings(st, "dms:DescribeReplicationSubnetGroups")
	handlers := dmsHandlers(newDMSCalls())
	unnamed := "arn:aws:dms:" + testRegion + ":" + testAccountID + ":cert:unnamed"
	handlers["DescribeCertificates"] = func(any) (any, error) {
		return &databasemigrationservice.DescribeCertificatesOutput{Certificates: []dmstypes.Certificate{
			{CertificateIdentifier: sdkaws.String("no-arn")},
			{CertificateArn: sdkaws.String(unnamed)},
		}}, nil
	}
	handlers["DescribeEventSubscriptions"] = func(any) (any, error) {
		return &databasemigrationservice.DescribeEventSubscriptionsOutput{EventSubscriptionsList: []dmstypes.EventSubscription{{SnsTopicArn: sdkaws.String("topic")}}}, nil
	}
	handlers["DescribeReplicationSubnetGroups"] = func(any) (any, error) {
		return &databasemigrationservice.DescribeReplicationSubnetGroupsOutput{ReplicationSubnetGroups: []dmstypes.ReplicationSubnetGroup{{}}}, nil
	}

	total, _, err := dmsScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 17 {
		t.Errorf("total = %d; want 17", total)
	}
	if r, ok := dmsRowOf(t, st, TypeDMSCertificate, unnamed); !ok || sv(r.Name) != unnamed {
		t.Errorf("unnamed certificate stored = %v, Name = %q; want stored, named by its ARN", ok, sv(r.Name))
	}
	for _, c := range []struct {
		op    string
		got   *[]store.ScanWarning
		first string
	}{
		{"DescribeCertificates", certWarns, "no-arn"},
		{"DescribeEventSubscriptions", esWarns, "topic"},
		{"DescribeReplicationSubnetGroups", sgWarns, "(an element with no VPC)"},
	} {
		if len(*c.got) != 1 || !strings.HasSuffix((*c.got)[0].Message, "first "+c.first) {
			t.Errorf("dms:%s warnings = %v; want one naming %s first", c.op, *c.got, c.first)
		}
	}
	if *all != 3 {
		t.Errorf("warnings = %d; want 3", *all)
	}
}

// Rows of an account in another partition are sent to the tag op under
// their own ARNs, the synthesized ones built in that partition.
func TestScanDMS_TagReadInAccountPartition(t *testing.T) {
	acct := partitionAccount("aws-us-gov")
	region := acct.Regions[0]
	st := newTestStore(t)
	calls := newDMSCalls()
	handlers := map[string]func(any) (any, error){"ListTagsForResource": calls.listTags}
	for _, op := range []string{"DescribeCertificates", "DescribeEventSubscriptions"} {
		k := dmsKindOf(t, op)
		handlers[op] = func(in any) (any, error) {
			id := dmsID(k, "tagged")
			arn := "arn:aws-us-gov:dms:" + region + ":" + testAccountID + ":" + k.kind + ":" + id
			return k.page(&arn, &id, nil), nil
		}
	}
	gov := func(kind string) string {
		return "arn:aws-us-gov:dms:" + region + ":" + testAccountID + ":" + kind + ":" + kind + "-tagged"
	}
	calls.tags[gov("es")] = map[string]string{"k": "v"}
	acct.cfg = opStubConfig(handlers, dmsDenied)

	if _, _, err := scanDMS(context.Background(), acct, region, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := []string{gov("cert"), gov("es")}
	slices.Sort(calls.tagARNs)
	if !slices.Equal(calls.tagARNs, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", calls.tagARNs, want)
	}
	r, ok := dmsRowOf(t, st, TypeDMSEventSubscription, gov("es"))
	if !ok {
		t.Fatal("gov event subscription missing")
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"k": "v"})
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it too.
func TestScanDMS_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newDMSCalls()
	handlers := dmsHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	certs := handlers["DescribeCertificates"]
	handlers["DescribeCertificates"] = func(in any) (any, error) {
		if bacField[*string](in, "Marker") != nil {
			cancel()
			return nil, context.Canceled
		}
		return certs(in)
	}

	_, _, err := dmsScan(ctx, st, handlers)
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
	for _, k := range dmsKinds()[1:] {
		if n := calls.count(k.op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", k.op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanDMS_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "dms.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("later phase after rows were stored", func(t *testing.T) {
		st := newTestStore(t)
		handlers := dmsHandlers(newDMSCalls())
		handlers["DescribeReplicationTasks"] = func(any) (any, error) { return nil, nx }
		total, _, err := dmsScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != 20 {
			t.Errorf("total = %d; want the 20 earlier rows counted", total)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newDMSCalls()
		calls.errs[dmsARN(dmsKindOf(t, "DescribeCertificates"), "bare")] = nx
		_, _, err := dmsScan(context.Background(), st, dmsHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanDMS(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
	})
}
