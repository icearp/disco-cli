package aws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/icearp/disco-cli/store"
)

const (
	rdsTestARNPrefix = "arn:aws:rds:" + testRegion + ":" + testAccountID + ":"
	// rdsTestListedCount is the rows of the tagged kinds' listings: two per
	// listing, one per page.
	rdsTestListedCount = 2 * 10
	// rdsTestTargetGroups is the target groups listed: two per proxy, one per
	// page.
	rdsTestTargetGroups = 4
	// rdsTestTagReads is the rows whose tags are read.
	rdsTestTagReads = rdsTestListedCount + rdsTestTargetGroups
	// rdsTestTotal adds the two instance and two cluster automated backups,
	// whose tags come with their listing.
	rdsTestTotal = rdsTestTagReads + 4
)

var (
	rdsTestThrottle = apiErr("Throttling", "Rate exceeded")
	rdsTestInvalid  = apiErr("InvalidParameterValue", "bad")
)

// rdsKind is one listing whose rows have their tags read: the Describe op, the
// type it stores, the ARN resource type of its rows, the not-found code
// ListTagsForResource models for that type ("" for none), and page, which
// builds the op's output holding one element named name under arn, with next.
type rdsKind struct {
	op, typ, arnType, notFound string
	page                       func(arn, name, next *string) any
}

func (k rdsKind) name(suffix string) string { return k.arnType + "-" + suffix }
func (k rdsKind) arn(suffix string) string {
	return rdsTestARNPrefix + k.arnType + ":" + k.name(suffix)
}

func rdsKinds() []rdsKind {
	return []rdsKind{
		{"DescribeDBClusterParameterGroups", TypeRDSDBClusterParameterGroup, "cluster-pg", "", func(arn, name, next *string) any {
			return &rds.DescribeDBClusterParameterGroupsOutput{Marker: next, DBClusterParameterGroups: []rdstypes.DBClusterParameterGroup{{DBClusterParameterGroupArn: arn, DBClusterParameterGroupName: name}}}
		}},
		{"DescribeDBParameterGroups", TypeRDSDBParameterGroup, "pg", "", func(arn, name, next *string) any {
			return &rds.DescribeDBParameterGroupsOutput{Marker: next, DBParameterGroups: []rdstypes.DBParameterGroup{{DBParameterGroupArn: arn, DBParameterGroupName: name}}}
		}},
		{"DescribeDBProxies", TypeRDSDBProxy, "db-proxy", "DBProxyNotFoundFault", func(arn, name, next *string) any {
			return &rds.DescribeDBProxiesOutput{Marker: next, DBProxies: []rdstypes.DBProxy{{DBProxyArn: arn, DBProxyName: name}}}
		}},
		{"DescribeDBProxyEndpoints", TypeRDSDBProxyEndpoint, "db-proxy-endpoint", "DBProxyEndpointNotFoundFault", func(arn, name, next *string) any {
			return &rds.DescribeDBProxyEndpointsOutput{Marker: next, DBProxyEndpoints: []rdstypes.DBProxyEndpoint{{DBProxyEndpointArn: arn, DBProxyEndpointName: name}}}
		}},
		{"DescribeDBSecurityGroups", TypeRDSDBSecurityGroup, "secgrp", "", func(arn, name, next *string) any {
			return &rds.DescribeDBSecurityGroupsOutput{Marker: next, DBSecurityGroups: []rdstypes.DBSecurityGroup{{DBSecurityGroupArn: arn, DBSecurityGroupName: name}}}
		}},
		{"DescribeDBSubnetGroups", TypeRDSDBSubnetGroup, "subgrp", "", func(arn, name, next *string) any {
			return &rds.DescribeDBSubnetGroupsOutput{Marker: next, DBSubnetGroups: []rdstypes.DBSubnetGroup{{DBSubnetGroupArn: arn, DBSubnetGroupName: name}}}
		}},
		{"DescribeEventSubscriptions", TypeRDSEventSubscription, "es", "", func(arn, name, next *string) any {
			return &rds.DescribeEventSubscriptionsOutput{Marker: next, EventSubscriptionsList: []rdstypes.EventSubscription{{EventSubscriptionArn: arn, CustSubscriptionId: name}}}
		}},
		{"DescribeOptionGroups", TypeRDSOptionGroup, "og", "", func(arn, name, next *string) any {
			return &rds.DescribeOptionGroupsOutput{Marker: next, OptionGroupsList: []rdstypes.OptionGroup{{OptionGroupArn: arn, OptionGroupName: name}}}
		}},
		{"DescribeDBClusterEndpoints", TypeRDSClusterEndpoint, "cluster-endpoint", "", func(arn, name, next *string) any {
			return &rds.DescribeDBClusterEndpointsOutput{Marker: next, DBClusterEndpoints: []rdstypes.DBClusterEndpoint{{DBClusterEndpointArn: arn, DBClusterEndpointIdentifier: name, EndpointType: sdkaws.String("CUSTOM")}}}
		}},
		{"DescribeReservedDBInstances", TypeRDSReservedInstance, "ri", "", func(arn, name, next *string) any {
			return &rds.DescribeReservedDBInstancesOutput{Marker: next, ReservedDBInstances: []rdstypes.ReservedDBInstance{{ReservedDBInstanceArn: arn, ReservedDBInstanceId: name}}}
		}},
	}
}

func rdsKindOf(t *testing.T, op string) rdsKind {
	t.Helper()
	for _, k := range rdsKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no tagged RDS listing", op)
	return rdsKind{}
}

// rdsProxies is the proxies' kind, whose rows are the target groups' parents.
func rdsProxies() rdsKind {
	for _, k := range rdsKinds() {
		if k.typ == TypeRDSDBProxy {
			return k
		}
	}
	panic("rdsKinds lists no DB proxies")
}

// rdsTargetGroupARN is the ARN of the target group of proxy on page suffix.
func rdsTargetGroupARN(proxy, suffix string) string {
	return rdsTestARNPrefix + "target-group:" + proxy + "-tg-" + suffix
}

// rdsCalls records the calls of a scan and answers ListTagsForResource with
// tags, or errs, by ARN.
type rdsCalls struct {
	mu         sync.Mutex
	ops        map[string]int
	maxRecords map[string][]int32
	tagARNs    []string
	tgProxies  []string
	tags       map[string]map[string]string
	errs       map[string]error
}

// newRDSCalls tags the "tagged" row of every kind, and every proxy's
// "tagged" target group, with its type.
func newRDSCalls() *rdsCalls {
	c := &rdsCalls{ops: map[string]int{}, maxRecords: map[string][]int32{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range rdsKinds() {
		c.tags[k.arn("tagged")] = map[string]string{"type": k.typ}
	}
	proxies := rdsProxies()
	for _, suffix := range []string{"tagged", "bare"} {
		c.tags[rdsTargetGroupARN(proxies.name(suffix), "tagged")] = map[string]string{"type": TypeRDSDBProxyTargetGroup}
	}
	return c
}

func (c *rdsCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	if n := bacField[*int32](in, "MaxRecords"); n != nil {
		c.maxRecords[op] = append(c.maxRecords[op], *n)
	} else {
		c.maxRecords[op] = append(c.maxRecords[op], 0)
	}
}

func (c *rdsCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

func (c *rdsCalls) listTags(in any) (any, error) {
	arn := sv(in.(*rds.ListTagsForResourceInput).ResourceName)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var list []rdstypes.Tag
	for k, v := range tags {
		list = append(list, rdstypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &rds.ListTagsForResourceOutput{TagList: list}, nil
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second.
func (c *rdsCalls) twoPages(k rdsKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("m2")
		if bacField[*string](in, "Marker") != nil {
			suffix, next = "bare", nil
		}
		arn, name := k.arn(suffix), k.name(suffix)
		return k.page(&arn, &name, next), nil
	}
}

// targetGroups answers each proxy's target groups in two pages.
func (c *rdsCalls) targetGroups(in any) (any, error) {
	c.record("DescribeDBProxyTargetGroups", in)
	proxy := sv(in.(*rds.DescribeDBProxyTargetGroupsInput).DBProxyName)
	suffix, next := "tagged", sdkaws.String("m2")
	if bacField[*string](in, "Marker") != nil {
		suffix, next = "bare", nil
	} else {
		c.mu.Lock()
		c.tgProxies = append(c.tgProxies, proxy)
		c.mu.Unlock()
	}
	return &rds.DescribeDBProxyTargetGroupsOutput{Marker: next, TargetGroups: []rdstypes.DBProxyTargetGroup{{
		TargetGroupArn: sdkaws.String(rdsTargetGroupARN(proxy, suffix)), TargetGroupName: sdkaws.String(proxy + "-tg-" + suffix), DBProxyName: &proxy,
	}}}, nil
}

// rdsTagged is the tags of a fixture row whose element carries them.
var rdsTagged = []rdstypes.Tag{{Key: sdkaws.String("type"), Value: sdkaws.String("backup")}}

// rdsHandlers answers every RDS op the scan calls: each tagged kind in two
// pages, each proxy's target groups in two pages, one tagged and one bare
// automated backup of each kind, and every other listing empty.
func rdsHandlers(c *rdsCalls) map[string]func(in any) (any, error) {
	empty := func(out any) func(any) (any, error) { return func(any) (any, error) { return out, nil } }
	h := map[string]func(in any) (any, error){
		"ListTagsForResource":               c.listTags,
		"DescribeDBProxyTargetGroups":       c.targetGroups,
		"DescribeDBInstances":               empty(&rds.DescribeDBInstancesOutput{}),
		"DescribeDBClusters":                empty(&rds.DescribeDBClustersOutput{}),
		"DescribeDBShardGroups":             empty(&rds.DescribeDBShardGroupsOutput{}),
		"DescribeGlobalClusters":            empty(&rds.DescribeGlobalClustersOutput{}),
		"DescribeIntegrations":              empty(&rds.DescribeIntegrationsOutput{}),
		"DescribeDBEngineVersions":          empty(&rds.DescribeDBEngineVersionsOutput{}),
		"DescribeDBSnapshots":               empty(&rds.DescribeDBSnapshotsOutput{}),
		"DescribeDBClusterSnapshots":        empty(&rds.DescribeDBClusterSnapshotsOutput{}),
		"DescribeTenantDatabases":           empty(&rds.DescribeTenantDatabasesOutput{}),
		"DescribeDBSnapshotTenantDatabases": empty(&rds.DescribeDBSnapshotTenantDatabasesOutput{}),
		"DescribeBlueGreenDeployments":      empty(&rds.DescribeBlueGreenDeploymentsOutput{}),
		"DescribeDBInstanceAutomatedBackups": empty(&rds.DescribeDBInstanceAutomatedBackupsOutput{DBInstanceAutomatedBackups: []rdstypes.DBInstanceAutomatedBackup{
			{DBInstanceAutomatedBackupsArn: sdkaws.String(rdsTestARNPrefix + "auto-backup:ab-tagged"), TagList: rdsTagged},
			{DBInstanceAutomatedBackupsArn: sdkaws.String(rdsTestARNPrefix + "auto-backup:ab-bare")},
		}}),
		"DescribeDBClusterAutomatedBackups": empty(&rds.DescribeDBClusterAutomatedBackupsOutput{DBClusterAutomatedBackups: []rdstypes.DBClusterAutomatedBackup{
			{DBClusterAutomatedBackupsArn: sdkaws.String(rdsTestARNPrefix + "cluster-auto-backup:cab-tagged"), TagList: rdsTagged},
			{DBClusterAutomatedBackupsArn: sdkaws.String(rdsTestARNPrefix + "cluster-auto-backup:cab-bare")},
		}}),
	}
	for _, k := range rdsKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// rdsScan runs the scan's real entry point against handlers; every op they do
// not answer is denied.
func rdsScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, rdsDenied)
	return scanRDS(ctx, acct, testRegion, st, testScanID)
}

// rdsTagReadARNs is the ARN of every row whose tags the fixture's scan reads.
func rdsTagReadARNs() []string {
	var arns []string
	for _, k := range rdsKinds() {
		arns = append(arns, k.arn("tagged"), k.arn("bare"))
	}
	proxies := rdsProxies()
	for _, p := range []string{"tagged", "bare"} {
		arns = append(arns, rdsTargetGroupARN(proxies.name(p), "tagged"), rdsTargetGroupARN(proxies.name(p), "bare"))
	}
	slices.Sort(arns)
	return arns
}

// Every tagged listing stores both its pages, each row under its ARN and
// named by its name, with the tags ListTagsForResource returns for that ARN,
// and nil for a resource without tags. Each stored proxy's target groups are
// listed under its name and tagged likewise. Exactly those ARNs are sent to
// the tag op, every listing is sent the model's page cap of 100, and the
// automated backups keep the tags their listing returns. No error, no
// warning.
func TestScanRDS_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newRDSCalls()

	total, inserted, err := rdsScan(context.Background(), st, rdsHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != rdsTestTotal || inserted != rdsTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, rdsTestTotal, rdsTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range rdsKinds() {
		for _, suffix := range []string{"tagged", "bare"} {
			arn := k.arn(suffix)
			r, ok := rowsByNativeID(t, st, k.typ)[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			if sv(r.Name) != k.name(suffix) || sv(r.Region) != testRegion || !strings.Contains(r.AttributesJSON, `"`+arn+`"`) {
				t.Errorf("%s Name, Region, attrs = %q, %q, %s; want %q, %q, the listed element", arn, sv(r.Name), sv(r.Region), r.AttributesJSON, k.name(suffix), testRegion)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
	}
	tgs := rowsByNativeID(t, st, TypeRDSDBProxyTargetGroup)
	proxies := rdsProxies()
	for _, p := range []string{"tagged", "bare"} {
		for _, suffix := range []string{"tagged", "bare"} {
			arn := rdsTargetGroupARN(proxies.name(p), suffix)
			r, ok := tgs[arn]
			switch {
			case !ok:
				t.Errorf("target group %s missing", arn)
			case suffix == "tagged":
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeRDSDBProxyTargetGroup})
			case r.TagsJSON != nil:
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
	}
	if got, want := slices.Sorted(slices.Values(calls.tgProxies)), []string{proxies.name("bare"), proxies.name("tagged")}; !slices.Equal(got, want) {
		t.Errorf("DescribeDBProxyTargetGroups DBProxyName = %v; want %v", got, want)
	}
	for _, typ := range []string{TypeRDSAutoBackup, TypeRDSClusterAutoBackup} {
		for arn, r := range rowsByNativeID(t, st, typ) {
			if strings.HasSuffix(arn, "-tagged") {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": "backup"})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
	}
	for _, op := range append(slices.Collect(func(yield func(string) bool) {
		for _, k := range rdsKinds() {
			yield(k.op)
		}
	}), "DescribeDBProxyTargetGroups") {
		if len(calls.maxRecords[op]) == 0 {
			t.Errorf("%s not called", op)
		}
		for _, n := range calls.maxRecords[op] {
			if n != 100 {
				t.Errorf("%s MaxRecords = %d; want 100", op, n)
			}
		}
	}
	if got := slices.Sorted(slices.Values(calls.tagARNs)); !slices.Equal(got, rdsTagReadARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, rdsTagReadARNs())
	}
}

// A rescan stores the same rows without inserting any.
func TestScanRDS_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := rdsScan(context.Background(), st, rdsHandlers(newRDSCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := rdsScan(context.Background(), st, rdsHandlers(newRDSCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != rdsTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, rdsTestTotal)
	}
}

// A failed tag read: the not-found code the op models for the row's type
// drops the row silently; a not-found code it does not model for that type
// keeps the row and is an error; a denial keeps the row untagged and warns
// once under rds:ListTagsForResource; a throttle keeps it and is transient;
// any other error keeps it and is an error.
func TestScanRDS_TagReadFailures(t *testing.T) {
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
	proxies := rdsProxies()
	tg := rdsTargetGroupARN(proxies.name("bare"), "tagged")
	targets := []struct{ arn, typ, notFound, unmodeled string }{
		{rdsKindOf(t, "DescribeDBParameterGroups").arn("tagged"), TypeRDSDBParameterGroup, "", "DBParameterGroupNotFound"},
		{rdsKindOf(t, "DescribeEventSubscriptions").arn("bare"), TypeRDSEventSubscription, "", "SubscriptionNotFound"},
		{rdsKindOf(t, "DescribeDBProxyEndpoints").arn("tagged"), TypeRDSDBProxyEndpoint, "DBProxyEndpointNotFoundFault", ""},
		{tg, TypeRDSDBProxyTargetGroup, "DBProxyTargetGroupNotFoundFault", ""},
	}
	for _, x := range targets {
		if x.notFound != "" {
			cases = append(cases, tagCase{x.typ + "/not found", x.arn, x.typ, apiErr(x.notFound, "gone"), -1, "", rdsTestTotal - 1, false, 0})
		} else {
			cases = append(cases, tagCase{x.typ + "/unmodeled not found", x.arn, x.typ, apiErr(x.unmodeled, "gone"), outcomeError, x.unmodeled, rdsTestTotal, true, 0})
		}
		cases = append(cases,
			tagCase{x.typ + "/denied", x.arn, x.typ, rdsDenied, -1, "", rdsTestTotal, true, 1},
			tagCase{x.typ + "/transient", x.arn, x.typ, rdsTestThrottle, outcomeTransient, "Throttling", rdsTestTotal, true, 0},
			tagCase{x.typ + "/other error", x.arn, x.typ, rdsTestInvalid, outcomeError, "InvalidParameterValue", rdsTestTotal, true, 0},
		)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newRDSCalls()
			calls.errs[c.arn] = c.err

			total, _, err := rdsScan(context.Background(), st, rdsHandlers(calls))
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
				if x.Service != rdsTagsOp || x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning under %q, Scope %q; want %s, %s/%s", x.Service, x.Scope, rdsTagsOp, testAccountID, testRegion)
				}
			}
		})
	}
}

// A proxy whose tag read finds it deleted is dropped, and its target groups
// are not listed; the other proxy's are.
func TestScanRDS_ProxyGoneAtTagReadHasNoTargetGroupRead(t *testing.T) {
	st := newTestStore(t)
	calls := newRDSCalls()
	proxies := rdsProxies()
	calls.errs[proxies.arn("tagged")] = apiErr("DBProxyNotFoundFault", "gone")

	total, _, err := rdsScan(context.Background(), st, rdsHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if want := rdsTestTotal - 1 - 2; total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
	if !slices.Equal(calls.tgProxies, []string{proxies.name("bare")}) {
		t.Errorf("target groups listed for %v; want only %s", calls.tgProxies, proxies.name("bare"))
	}
}

// One proxy's failed target-group listing costs the other proxy none of its
// target groups: a proxy deleted since it was listed (DBProxyNotFoundFault)
// is skipped silently, a denial warns once under the op, and any other error
// is returned.
func TestScanRDS_TargetGroupListingFailures(t *testing.T) {
	proxies := rdsProxies()
	gone := proxies.name("tagged")
	for _, c := range []struct {
		name      string
		err       error
		code      string
		wantWarns int
	}{
		{"proxy deleted", apiErr("DBProxyNotFoundFault", "gone"), "", 0},
		{"denied", rdsDenied, "", 1},
		{"proxy busy", apiErr("InvalidDBProxyStateFault", "deleting"), "InvalidDBProxyStateFault", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := bacWarnings(st, "rds:DescribeDBProxyTargetGroups")
			handlers := rdsHandlers(newRDSCalls())
			list := handlers["DescribeDBProxyTargetGroups"]
			handlers["DescribeDBProxyTargetGroups"] = func(in any) (any, error) {
				if sv(in.(*rds.DescribeDBProxyTargetGroupsInput).DBProxyName) == gone {
					return nil, c.err
				}
				return list(in)
			}

			total, _, err := rdsScan(context.Background(), st, handlers)
			if c.code == "" {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, c.code)
			}
			if want := rdsTestTotal - 2; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if _, ok := rowsByNativeID(t, st, TypeRDSDBProxyTargetGroup)[rdsTargetGroupARN(proxies.name("bare"), "bare")]; !ok {
				t.Error("the other proxy's target group missing")
			}
			if len(*warns) != c.wantWarns {
				t.Errorf("warnings = %v; want %d", *warns, c.wantWarns)
			}
		})
	}
}

// Denied tag reads in two listings, which run as separate families, send one
// warning for the whole scan, counting every denied row; the rows are kept.
func TestScanRDS_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, rdsTagsOp)
	calls := newRDSCalls()
	for _, op := range []string{"DescribeDBParameterGroups", "DescribeOptionGroups"} {
		k := rdsKindOf(t, op)
		calls.errs[k.arn("tagged")] = rdsDenied
		calls.errs[k.arn("bare")] = rdsDenied
	}

	total, _, err := rdsScan(context.Background(), st, rdsHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != rdsTestTotal {
		t.Errorf("total = %d; want %d", total, rdsTestTotal)
	}
	if len(*warnings) != 1 {
		t.Fatalf("%s warnings = %d; want 1", rdsTagsOp, len(*warnings))
	}
	for _, k := range []string{"DescribeDBParameterGroups", "DescribeOptionGroups"} {
		if got := len(rowsByNativeID(t, st, rdsKindOf(t, k).typ)); got != 2 {
			t.Errorf("%s rows = %d; want both kept", k, got)
		}
	}
}

// The tag reads of every family share one bound of fanoutMed, though each
// family's reads alone would allow fanoutMed.
func TestScanRDS_TagReadsShareOneBound(t *testing.T) {
	st := newTestStore(t)
	gate := &cfGatedTags{over: make(chan struct{})}
	handlers := rdsHandlers(newRDSCalls())
	handlers["ListTagsForResource"] = func(in any) (any, error) {
		if _, err := gate.listTags(in); err != nil {
			return nil, err
		}
		return &rds.ListTagsForResourceOutput{}, nil
	}

	if _, _, err := rdsScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := gate.calls.Load(); got != rdsTestTagReads {
		t.Errorf("ListTagsForResource calls = %d; want %d", got, rdsTestTagReads)
	}
	if most := gate.most.Load(); most > fanoutMed {
		t.Errorf("tag reads in flight at once = %d; want at most %d", most, fanoutMed)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanRDS_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, rdsTagsOp)
	calls := newRDSCalls()
	calls.errs[rdsKindOf(t, "DescribeDBParameterGroups").arn("tagged")] = rdsTestThrottle
	calls.errs[rdsKindOf(t, "DescribeOptionGroups").arn("bare")] = rdsTestInvalid

	_, _, err := rdsScan(context.Background(), st, rdsHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidParameterValue")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", rdsTagsOp, *warns)
	}
}

// A failed write stops its listing and sends no warning about the rows it
// lost, while every other family stores its rows.
func TestScanRDS_FailedWriteKeepsOtherFamilies(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeRDSDBParameterGroup)
	calls := newRDSCalls()
	k := rdsKindOf(t, "DescribeDBParameterGroups")
	calls.errs[k.arn("tagged")] = rdsDenied

	total, _, err := rdsScan(context.Background(), st, rdsHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count(k.op); n != 1 {
		t.Errorf("%s called %d times; want 1, none after the failed write", k.op, n)
	}
	if want := rdsTestTotal - 2; total != want {
		t.Errorf("total = %d; want %d (every other family's rows)", total, want)
	}
	if got := len(rowsByNativeID(t, st, TypeRDSOptionGroup)); got != 2 {
		t.Errorf("option groups (another family) = %d; want 2", got)
	}
}

// A failed proxy write stops the proxies' family before any target group is
// listed.
func TestScanRDS_FailedProxyWriteListsNoTargetGroups(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeRDSDBProxy)
	calls := newRDSCalls()

	_, _, err := rdsScan(context.Background(), st, rdsHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if n := calls.count("DescribeDBProxyTargetGroups"); n != 0 {
		t.Errorf("DescribeDBProxyTargetGroups called %d times after the failed write", n)
	}
}

// A listing's failed page keeps the pages read before it, and its error does
// not stop the other listings. A proxy not stored has no target groups read.
func TestScanRDS_ListFailureKeepsPagesAndOtherFamilies(t *testing.T) {
	for _, c := range []struct {
		op        string
		wantTotal int
	}{
		{"DescribeOptionGroups", rdsTestTotal - 1},
		{"DescribeDBProxies", rdsTestTotal - 1 - 2},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			handlers := rdsHandlers(newRDSCalls())
			k := rdsKindOf(t, c.op)
			handlers[c.op] = failOnCall(2, rdsTestInvalid, handlers[c.op])

			total, _, err := rdsScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidParameterValue")
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			if _, ok := rowsByNativeID(t, st, k.typ)[k.arn("tagged")]; !ok {
				t.Errorf("%s row from the page before the failure missing", k.typ)
			}
			if got := len(rowsByNativeID(t, st, TypeRDSEventSubscription)); got != 2 {
				t.Errorf("event subscriptions (another family) = %d; want 2", got)
			}
		})
	}
}

// A repeated marker ends the listing as an error, keeping its pages.
func TestScanRDS_RepeatedMarkerKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := rdsHandlers(newRDSCalls())
	k := rdsKindOf(t, "DescribeEventSubscriptions")
	handlers[k.op] = func(in any) (any, error) {
		suffix := "tagged"
		if bacField[*string](in, "Marker") != nil {
			suffix = "bare"
		}
		arn, name := k.arn(suffix), k.name(suffix)
		return k.page(&arn, &name, sdkaws.String("same")), nil
	}

	total, _, err := rdsScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != rdsTestTotal {
		t.Errorf("total = %d; want %d", total, rdsTestTotal)
	}
}

// An element without an ARN is not stored, is not sent to the tag op, and is
// warned about once under its listing, named by its name or by the fallback
// text when it has none. A proxy without one has no target groups read.
func TestScanRDS_ElementWithoutARNWarns(t *testing.T) {
	for _, c := range []struct {
		name, op, key string
		answer        func(in any) (any, error)
		lost          int
	}{
		{"parameter group", "DescribeDBParameterGroups", "pg-x", func(any) (any, error) {
			return &rds.DescribeDBParameterGroupsOutput{DBParameterGroups: []rdstypes.DBParameterGroup{{DBParameterGroupName: sdkaws.String("pg-x")}}}, nil
		}, 2},
		{"cluster parameter group without a name", "DescribeDBClusterParameterGroups", rdsNoIdentifier, func(any) (any, error) {
			return &rds.DescribeDBClusterParameterGroupsOutput{DBClusterParameterGroups: []rdstypes.DBClusterParameterGroup{{}}}, nil
		}, 2},
		{"proxy", "DescribeDBProxies", "px-x", func(any) (any, error) {
			return &rds.DescribeDBProxiesOutput{DBProxies: []rdstypes.DBProxy{{DBProxyName: sdkaws.String("px-x")}}}, nil
		}, 2 + rdsTestTargetGroups},
		{"target group", "DescribeDBProxyTargetGroups", "tg-x", func(any) (any, error) {
			return &rds.DescribeDBProxyTargetGroupsOutput{TargetGroups: []rdstypes.DBProxyTargetGroup{{TargetGroupName: sdkaws.String("tg-x")}}}, nil
		}, rdsTestTargetGroups},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newRDSCalls()
			handlers := rdsHandlers(calls)
			handlers[c.op] = c.answer

			total, _, err := rdsScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := rdsTestTotal - c.lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			w := warns()
			if len(w) != 1 {
				t.Fatalf("warnings = %v; want 1", w)
			}
			if w[0].Service != "rds:"+c.op || !strings.Contains(w[0].Message, c.key) || w[0].Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning %+v; want one under rds:%s naming %q in %s/%s", w[0], c.op, c.key, testAccountID, testRegion)
			}
			if slices.Contains(calls.tagARNs, "") {
				t.Error("an empty ARN was sent to ListTagsForResource")
			}
			if c.op == "DescribeDBProxies" && len(calls.tgProxies) != 0 {
				t.Errorf("target groups listed for %v; want none", calls.tgProxies)
			}
		})
	}
}

// A reserved instance listed without its ARN is stored under the ARN the
// Service Reference's ri format gives for its id, in the account's partition,
// and that ARN is sent to the tag op; no warning.
func TestScanRDS_ReservedInstanceWithoutARNStoredUnderItsARN(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newRDSCalls()
	handlers := rdsHandlers(calls)
	handlers["DescribeReservedDBInstances"] = func(any) (any, error) {
		return &rds.DescribeReservedDBInstancesOutput{ReservedDBInstances: []rdstypes.ReservedDBInstance{{ReservedDBInstanceId: sdkaws.String("ri-x")}}}, nil
	}
	want := rdsTestARNPrefix + "ri:ri-x"
	calls.tags[want] = map[string]string{"k": "v"}

	total, _, err := rdsScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != rdsTestTotal-1 {
		t.Errorf("total = %d; want %d", total, rdsTestTotal-1)
	}
	r, ok := rowsByNativeID(t, st, TypeRDSReservedInstance)[want]
	if !ok {
		t.Fatalf("reserved instance not stored under %s", want)
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"k": "v"})
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// Rows of the aws-us-gov and aws-cn partitions reach the tag op under their
// listed ARNs unchanged, and a reserved instance without an ARN under one
// built in the account's partition.
func TestScanRDS_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newRDSCalls()
			handlers := rdsHandlers(calls)
			prefix := "arn:" + c.partition + ":rds:" + c.region + ":" + testAccountID + ":"
			pgARN := prefix + "pg:pg-p"
			handlers["DescribeDBParameterGroups"] = func(any) (any, error) {
				return &rds.DescribeDBParameterGroupsOutput{DBParameterGroups: []rdstypes.DBParameterGroup{{DBParameterGroupArn: &pgARN, DBParameterGroupName: sdkaws.String("pg-p")}}}, nil
			}
			handlers["DescribeReservedDBInstances"] = func(any) (any, error) {
				return &rds.DescribeReservedDBInstancesOutput{ReservedDBInstances: []rdstypes.ReservedDBInstance{{ReservedDBInstanceId: sdkaws.String("ri-p")}}}, nil
			}
			acct := newTestAccount(testAccountID)
			acct.Partition = c.partition
			acct.cfg = opStubConfig(handlers, rdsDenied)
			if _, _, err := scanRDS(context.Background(), acct, c.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			for _, want := range []string{pgARN, prefix + "ri:ri-p"} {
				if !slices.Contains(calls.tagARNs, want) {
					t.Errorf("ListTagsForResource ARNs %v lack %s", calls.tagARNs, want)
				}
			}
		})
	}
}

// A failed tag read of an AWS-created default parameter, cluster parameter
// or option group keeps the row untagged and, however many fail, warns once
// under the tag op naming the first one's ARN, with no error. A non-default
// group's same failure is still an error (TestScanRDS_TagReadFailures).
func TestScanRDS_DefaultGroupTagFailureWarnsOnce(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newRDSCalls()
	handlers := rdsHandlers(calls)
	pg := rdsTestARNPrefix + "pg:default.mysql8.0"
	cpg := rdsTestARNPrefix + "cluster-pg:default.aurora-mysql8.0"
	og := rdsTestARNPrefix + "og:default:mysql-8-0"
	handlers["DescribeDBParameterGroups"] = func(any) (any, error) {
		return &rds.DescribeDBParameterGroupsOutput{DBParameterGroups: []rdstypes.DBParameterGroup{{DBParameterGroupArn: &pg, DBParameterGroupName: sdkaws.String("default.mysql8.0")}}}, nil
	}
	handlers["DescribeDBClusterParameterGroups"] = func(any) (any, error) {
		return &rds.DescribeDBClusterParameterGroupsOutput{DBClusterParameterGroups: []rdstypes.DBClusterParameterGroup{{DBClusterParameterGroupArn: &cpg, DBClusterParameterGroupName: sdkaws.String("default.aurora-mysql8.0")}}}, nil
	}
	handlers["DescribeOptionGroups"] = func(any) (any, error) {
		return &rds.DescribeOptionGroupsOutput{OptionGroupsList: []rdstypes.OptionGroup{{OptionGroupArn: &og, OptionGroupName: sdkaws.String("default:mysql-8-0")}}}, nil
	}
	for _, arn := range []string{pg, cpg, og} {
		calls.errs[arn] = rdsTestInvalid
	}

	total, _, err := rdsScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if want := rdsTestTotal - 3; total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
	for typ, arn := range map[string]string{TypeRDSDBParameterGroup: pg, TypeRDSDBClusterParameterGroup: cpg, TypeRDSOptionGroup: og} {
		r, ok := rowsByNativeID(t, st, typ)[arn]
		if !ok || r.TagsJSON != nil {
			t.Errorf("%s stored %v, TagsJSON %v; want stored untagged", arn, ok, r.TagsJSON)
		}
	}
	w := warns()
	if len(w) != 1 || w[0].Service != rdsTagsOp || w[0].Scope != testAccountID+"/"+testRegion {
		t.Fatalf("warnings = %v; want one under %s", w, rdsTagsOp)
	}
	// The smallest of the three ARNs, whichever family's read failed first.
	if !strings.Contains(w[0].Message, cpg) {
		t.Errorf("warning %q does not name the smallest ARN %s", w[0].Message, cpg)
	}
}

// After a failed write, a default group's failed tag read sends no warning:
// its row may be one the write lost.
func TestScanRDS_DefaultGroupFailureAfterFailedWriteWarnsNothing(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeRDSDBParameterGroup)
	calls := newRDSCalls()
	handlers := rdsHandlers(calls)
	pg := rdsTestARNPrefix + "pg:default.mysql8.0"
	handlers["DescribeDBParameterGroups"] = func(any) (any, error) {
		return &rds.DescribeDBParameterGroupsOutput{DBParameterGroups: []rdstypes.DBParameterGroup{{DBParameterGroupArn: &pg, DBParameterGroupName: sdkaws.String("default.mysql8.0")}}}, nil
	}
	calls.errs[pg] = rdsTestInvalid

	_, _, err := rdsScan(context.Background(), st, handlers)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// The refusal tally names the smallest ARN whatever order the reads end in,
// since the families read concurrently.
func TestRDSDefaultGroupRefusals_NamesSmallestARN(t *testing.T) {
	var d rdsDefaultGroupRefusals
	tags := d.tags(rowTagReader{read: func(context.Context, *store.Resource) (*string, error) { return nil, rdsTestInvalid }})
	for _, arn := range []string{rdsTestARNPrefix + "pg:default.b", rdsTestARNPrefix + "og:default:c", rdsTestARNPrefix + "cluster-pg:default.a"} {
		name := arn[strings.LastIndex(arn, ":default")+1:]
		if got, err := tags.read(context.Background(), &store.Resource{NativeID: arn, Name: &name}); got != nil || err != nil {
			t.Fatalf("read %s = %v, %v; want nil, nil", arn, got, err)
		}
	}
	if want := rdsTestARNPrefix + "cluster-pg:default.a"; d.count != 3 || d.first != want {
		t.Errorf("count, first = %d, %s; want 3, %s", d.count, d.first, want)
	}
}

// A default group's tag read that fails with any code but the expected
// refusals is an error, as for any other group, with no warning.
func TestScanRDS_DefaultGroupUnexpectedCodeIsError(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newRDSCalls()
	handlers := rdsHandlers(calls)
	pg := rdsTestARNPrefix + "pg:default.mysql8.0"
	handlers["DescribeDBParameterGroups"] = func(any) (any, error) {
		return &rds.DescribeDBParameterGroupsOutput{DBParameterGroups: []rdstypes.DBParameterGroup{{DBParameterGroupArn: &pg, DBParameterGroupName: sdkaws.String("default.mysql8.0")}}}, nil
	}
	calls.errs[pg] = apiErr("DBParameterGroupNotFound", "gone")

	_, _, err := rdsScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "DBParameterGroupNotFound")
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if r, ok := rowsByNativeID(t, st, TypeRDSDBParameterGroup)[pg]; !ok || r.TagsJSON != nil {
		t.Errorf("%s stored %v; want stored untagged", pg, ok)
	}
}

// A default group's tag read cut off by the scan's stop is the stop, not a
// group stored without tags: the error carries it and nothing is warned.
func TestScanRDS_DefaultGroupReadCutByStopIsNoRefusal(t *testing.T) {
	st := newTestStore(t)
	warns := bacWarnings(st, rdsTagsOp)
	calls := newRDSCalls()
	handlers := rdsHandlers(calls)
	pg := rdsTestARNPrefix + "pg:default.mysql8.0"
	handlers["DescribeDBParameterGroups"] = func(any) (any, error) {
		return &rds.DescribeDBParameterGroupsOutput{DBParameterGroups: []rdstypes.DBParameterGroup{{DBParameterGroupArn: &pg, DBParameterGroupName: sdkaws.String("default.mysql8.0")}}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tags := handlers["ListTagsForResource"]
	handlers["ListTagsForResource"] = func(in any) (any, error) {
		if sv(in.(*rds.ListTagsForResourceInput).ResourceName) == pg {
			cancel()
			return nil, context.Canceled
		}
		return tags(in)
	}

	_, _, err := rdsScan(ctx, st, handlers)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	if len(*warns) != 0 {
		t.Errorf("%s warnings = %v; want none", rdsTagsOp, *warns)
	}
}

// A default group's denied or throttled tag read keeps its usual path: the
// denial is the shared denial warning, the throttle a transient error.
func TestScanRDS_DefaultGroupKeepsDenialAndTransientPaths(t *testing.T) {
	pg := rdsTestARNPrefix + "pg:default.mysql8.0"
	for _, c := range []struct {
		name    string
		err     error
		outcome serviceOutcome // -1: no error
	}{
		{"denied", rdsDenied, -1},
		{"transient", rdsTestThrottle, outcomeTransient},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := bacWarnings(st, rdsTagsOp)
			calls := newRDSCalls()
			handlers := rdsHandlers(calls)
			handlers["DescribeDBParameterGroups"] = func(any) (any, error) {
				return &rds.DescribeDBParameterGroupsOutput{DBParameterGroups: []rdstypes.DBParameterGroup{{DBParameterGroupArn: &pg, DBParameterGroupName: sdkaws.String("default.mysql8.0")}}}, nil
			}
			calls.errs[pg] = c.err
			if c.outcome < 0 {
				calls.errs[rdsKindOf(t, "DescribeOptionGroups").arn("tagged")] = c.err
			}
			_, _, err := rdsScan(context.Background(), st, handlers)
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				// With another group denied too, one warning means the default
				// group's denial joined the shared one rather than warning apart.
				if len(*warns) != 1 {
					t.Errorf("warnings = %v; want the one shared denial warning", *warns)
				}
				return
			}
			assertGlueOutcome(t, err, c.outcome)
			if len(*warns) != 0 {
				t.Errorf("warnings = %v; want none", *warns)
			}
		})
	}
}

// Aurora's built-in WRITER and READER endpoints are listed without an ARN
// or identifier: they are stored untagged, with no tag read and no warning,
// and both collapse into the one row under NativeID "" (a known defect,
// pinned so that the doc on scanDBClusterEndpoints stays true).
func TestScanRDS_BuiltInClusterEndpointsStoredUntagged(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newRDSCalls()
	handlers := rdsHandlers(calls)
	k := rdsKindOf(t, "DescribeDBClusterEndpoints")
	handlers[k.op] = func(any) (any, error) {
		arn := k.arn("tagged")
		return &rds.DescribeDBClusterEndpointsOutput{DBClusterEndpoints: []rdstypes.DBClusterEndpoint{
			{DBClusterEndpointArn: &arn, DBClusterEndpointIdentifier: sdkaws.String(k.name("tagged")), EndpointType: sdkaws.String("CUSTOM")},
			{DBClusterIdentifier: sdkaws.String("c1"), EndpointType: sdkaws.String("WRITER"), Endpoint: sdkaws.String("c1.cluster-x.us-east-1.rds.amazonaws.com")},
			{DBClusterIdentifier: sdkaws.String("c1"), EndpointType: sdkaws.String("READER"), Endpoint: sdkaws.String("c1.cluster-ro-x.us-east-1.rds.amazonaws.com")},
		}}, nil
	}

	total, _, err := rdsScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != rdsTestTotal+1 {
		t.Errorf("total = %d; want %d (both built-ins counted)", total, rdsTestTotal+1)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	rows := rowsByNativeID(t, st, TypeRDSClusterEndpoint)
	if r, ok := rows[""]; !ok || r.TagsJSON != nil {
		t.Errorf("built-in endpoint row = %+v (stored %v); want one untagged row under NativeID \"\"", r, ok)
	}
	if len(rows) != 2 {
		t.Errorf("cluster endpoint rows = %v; want the custom one and one built-in row for both built-ins", nativeIDs(rows))
	}
	assertTagsMap(t, rows[k.arn("tagged")].TagsJSON, map[string]string{"type": TypeRDSClusterEndpoint})
	if slices.Contains(calls.tagARNs, "") {
		t.Error("the built-in endpoint was sent to ListTagsForResource")
	}
}

// A denied listing warns once under its op; a denial on a later page keeps
// the page read before it. Every other listing still runs.
func TestScanRDS_DeniedListingWarnsUnderItsOp(t *testing.T) {
	for _, call := range []int{1, 2} {
		t.Run(fmt.Sprintf("page%d", call), func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			handlers := rdsHandlers(newRDSCalls())
			k := rdsKindOf(t, "DescribeDBSubnetGroups")
			handlers[k.op] = failOnCall(call, rdsDenied, handlers[k.op])

			total, _, err := rdsScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			kept := call - 1
			if want := rdsTestTotal - 2 + kept; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if w := warns(); len(w) != 1 || w[0].Service != "rds:"+k.op {
				t.Errorf("warnings = %v; want one under rds:%s", w, k.op)
			}
			if got := len(rowsByNativeID(t, st, k.typ)); got != kept {
				t.Errorf("%s rows = %d; want %d", k.typ, got, kept)
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although two families
// in flight saw it in their listings.
func TestScanRDS_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	handlers := rdsHandlers(newRDSCalls())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, op := range []string{"DescribeDBParameterGroups", "DescribeOptionGroups"} {
		list := handlers[op]
		handlers[op] = func(in any) (any, error) {
			if bacField[*string](in, "Marker") != nil {
				cancel()
				return nil, context.Canceled
			}
			return list(in)
		}
	}

	_, _, err := rdsScan(ctx, st, handlers)
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
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanRDS_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "rds.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := rdsHandlers(newRDSCalls())
		handlers["DescribeOptionGroups"] = func(any) (any, error) { return nil, nx }
		total, _, err := rdsScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != rdsTestTotal-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, rdsTestTotal-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newRDSCalls()
		calls.errs[rdsKindOf(t, "DescribeDBSubnetGroups").arn("bare")] = nx
		_, _, err := rdsScan(context.Background(), st, rdsHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanRDS(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}
