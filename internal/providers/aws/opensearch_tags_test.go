package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	opensearchtypes "github.com/aws/aws-sdk-go-v2/service/opensearch/types"
	"github.com/icearp/disco-cli/store"
)

var osTestThrottle = apiErr("ThrottlingException", "Rate exceeded")

func osTestDomainARN(partition, region, name string) string {
	return fmt.Sprintf("arn:%s:es:%s:%s:domain/%s", partition, region, testAccountID, name)
}

func osTestAppARN(partition, region, id string) string {
	return fmt.Sprintf("arn:%s:opensearch:%s:%s:application/%s", partition, region, testAccountID, id)
}

func osTestDQARN(partition, region, name string) string {
	return fmt.Sprintf("arn:%s:opensearch:%s:%s:datasource/%s", partition, region, testAccountID, name)
}

// osTestTotal is every row of osTagFixture: two domains, two applications
// (one per page) and one direct-query data source.
const osTestTotal = 5

// osTagFixture answers the ops of a scan: domains logs (created, tagged)
// and metrics (still creating, no tags); applications a1 (tagged, page one)
// and a2 (no tags or status, page two); one direct-query data source whose
// listing carries its tags; every other listing empty. It records the calls,
// the ARNs sent to ListTags and the MaxResults sent to ListApplications.
// tagErrs (by ARN) and describeErrs (by domain name) fail those calls.
type osTagFixture struct {
	partition, region string

	mu            sync.Mutex
	calls         map[string]int
	tagARNs       []string
	appMaxResults []int32
	dsDomains     []string
	attachApps    []string
	tagErrs       map[string]error
	describeErrs  map[string]error
	describeOut   map[string]*opensearch.DescribeDomainOutput
}

func newOSTagFixture() *osTagFixture { return newOSTagFixtureIn("aws", testRegion) }

func newOSTagFixtureIn(partition, region string) *osTagFixture {
	return &osTagFixture{
		partition: partition, region: region, calls: map[string]int{},
		tagErrs: map[string]error{}, describeErrs: map[string]error{}, describeOut: map[string]*opensearch.DescribeDomainOutput{},
	}
}

func (f *osTagFixture) domainARN(name string) string {
	return osTestDomainARN(f.partition, f.region, name)
}

func (f *osTagFixture) appARN(id string) string { return osTestAppARN(f.partition, f.region, id) }

// domainStatus is the body DescribeDomain answers name with.
func (f *osTagFixture) domainStatus(name string) *opensearchtypes.DomainStatus {
	created := name == "logs"
	return &opensearchtypes.DomainStatus{
		ARN: ptrStr(f.domainARN(name)), DomainName: ptrStr(name), DomainId: ptrStr(testAccountID + "/" + name),
		Created: &created, EngineVersion: ptrStr("OpenSearch_2.11"),
	}
}

func (f *osTagFixture) app(id string) opensearchtypes.ApplicationSummary {
	a := opensearchtypes.ApplicationSummary{Id: ptrStr(id), Arn: ptrStr(f.appARN(id)), Name: ptrStr("app-" + id)}
	if id == "a1" {
		a.Status = opensearchtypes.ApplicationStatusActive
		a.CreatedAt = &osCreated
	}
	return a
}

// wantTags is the tags ListTags answers arn with: nil for none.
func (f *osTagFixture) wantTags(arn string) map[string]string {
	switch arn {
	case f.domainARN("logs"):
		return map[string]string{"team": "search"}
	case f.appARN("a1"):
		return map[string]string{"team": "ui"}
	}
	return nil
}

func (f *osTagFixture) osfCount(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[op]
}

func (f *osTagFixture) osfSortedTagARNs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(slices.Values(f.tagARNs))
}

func (f *osTagFixture) osfRecord(op string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[op]++
}

func (f *osTagFixture) osfHandlers() map[string]func(any) (any, error) {
	recorded := func(op string, out any) func(any) (any, error) {
		return func(any) (any, error) {
			f.osfRecord(op)
			return out, nil
		}
	}
	return map[string]func(any) (any, error){
		"ListDomainNames": recorded("ListDomainNames", &opensearch.ListDomainNamesOutput{DomainNames: []opensearchtypes.DomainInfo{
			{DomainName: ptrStr("logs")}, {DomainName: ptrStr("metrics")},
		}}),
		"DescribeDomain": func(in any) (any, error) {
			f.osfRecord("DescribeDomain")
			name := sv(in.(*opensearch.DescribeDomainInput).DomainName)
			f.mu.Lock()
			err, out := f.describeErrs[name], f.describeOut[name]
			f.mu.Unlock()
			switch {
			case err != nil:
				return nil, err
			case out != nil:
				return out, nil
			}
			return &opensearch.DescribeDomainOutput{DomainStatus: f.domainStatus(name)}, nil
		},
		"ListApplications": func(in any) (any, error) {
			f.osfRecord("ListApplications")
			req := in.(*opensearch.ListApplicationsInput)
			f.mu.Lock()
			f.appMaxResults = append(f.appMaxResults, req.MaxResults)
			f.mu.Unlock()
			if req.NextToken == nil {
				return &opensearch.ListApplicationsOutput{NextToken: ptrStr("p2"), ApplicationSummaries: []opensearchtypes.ApplicationSummary{f.app("a1")}}, nil
			}
			return &opensearch.ListApplicationsOutput{ApplicationSummaries: []opensearchtypes.ApplicationSummary{f.app("a2")}}, nil
		},
		"ListTags": func(in any) (any, error) {
			f.osfRecord("ListTags")
			arn := sv(in.(*opensearch.ListTagsInput).ARN)
			f.mu.Lock()
			f.tagARNs = append(f.tagARNs, arn)
			err := f.tagErrs[arn]
			f.mu.Unlock()
			if err != nil {
				return nil, err
			}
			var tags []opensearchtypes.Tag
			for k, v := range f.wantTags(arn) {
				tags = append(tags, opensearchtypes.Tag{Key: ptrStr(k), Value: ptrStr(v)})
			}
			return &opensearch.ListTagsOutput{TagList: tags}, nil
		},
		"ListDataSources": func(in any) (any, error) {
			f.mu.Lock()
			f.dsDomains = append(f.dsDomains, sv(in.(*opensearch.ListDataSourcesInput).DomainName))
			f.mu.Unlock()
			return &opensearch.ListDataSourcesOutput{}, nil
		},
		"ListDirectQueryDataSources": recorded("ListDirectQueryDataSources", &opensearch.ListDirectQueryDataSourcesOutput{DirectQueryDataSources: []opensearchtypes.DirectQueryDataSource{{
			DataSourceArn: ptrStr(osTestDQARN(f.partition, f.region, "cwl")), DataSourceName: ptrStr("cwl"),
			TagList: []opensearchtypes.Tag{{Key: ptrStr("team"), Value: ptrStr("obs")}},
		}}}),
		"ListDataSourceAttachments": func(in any) (any, error) {
			f.mu.Lock()
			f.attachApps = append(f.attachApps, sv(in.(*opensearch.ListDataSourceAttachmentsInput).Id))
			f.mu.Unlock()
			return &opensearch.ListDataSourceAttachmentsOutput{}, nil
		},
		"ListVpcEndpoints":            recorded("ListVpcEndpoints", &opensearch.ListVpcEndpointsOutput{}),
		"DescribePackages":            recorded("DescribePackages", &opensearch.DescribePackagesOutput{}),
		"DescribeReservedInstances":   recorded("DescribeReservedInstances", &opensearch.DescribeReservedInstancesOutput{}),
		"DescribeOutboundConnections": recorded("DescribeOutboundConnections", &opensearch.DescribeOutboundConnectionsOutput{}),
		"DescribeInboundConnections":  recorded("DescribeInboundConnections", &opensearch.DescribeInboundConnectionsOutput{}),
	}
}

// osfAllTagARNs is the ARN of every domain and application, sorted.
func (f *osTagFixture) osfAllTagARNs() []string {
	return slices.Sorted(slices.Values([]string{f.domainARN("logs"), f.domainARN("metrics"), f.appARN("a1"), f.appARN("a2")}))
}

// osTagScan runs the real entry point against handlers in f's partition and
// region; every op they do not answer is denied.
func osTagScan(ctx context.Context, st *store.Store, f *osTagFixture, handlers map[string]func(any) (any, error)) (int, error) {
	acct := newTestAccount(testAccountID)
	acct.Partition = f.partition
	acct.cfg = opStubConfig(handlers, osDenied)
	return scanOpenSearch(ctx, acct, f.region, st, testScanID)
}

func osJSONValue(t *testing.T, v any) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(mustJSON(v)), &m); err != nil {
		t.Fatalf("unmarshal %T: %v", v, err)
	}
	return m
}

// osAssertRow checks r against the row the scan must build: name, status
// (nil for ""), region, no CreatedAt, not managed, the SDK body as its
// attributes and tags (nil for none).
func osAssertRow(t *testing.T, r store.Resource, name, status string, body any, tags map[string]string) {
	t.Helper()
	if sv(r.Name) != name || sv(r.Status) != status || (status == "") != (r.Status == nil) || sv(r.Region) != testRegion || r.CreatedAt != nil || r.ManagedByProvider {
		t.Errorf("%s Name, Status, Region, CreatedAt, Managed = %q, %v, %q, %v, %v; want %q, %q, %q, nil, false",
			r.NativeID, sv(r.Name), r.Status, sv(r.Region), r.CreatedAt, r.ManagedByProvider, name, status, testRegion)
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("%s attrs: %v", r.NativeID, err)
	}
	if want := osJSONValue(t, body); !reflect.DeepEqual(attrs, want) {
		t.Errorf("%s attrs = %s; want %v", r.NativeID, r.AttributesJSON, want)
	}
	if tags == nil {
		if r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %q; want nil", r.NativeID, *r.TagsJSON)
		}
		return
	}
	assertTagsMap(t, r.TagsJSON, tags)
}

// Domains and applications are stored with the tags ListTags returns (nil
// for none), each row built from its describe body or listed summary.
// ListTags is sent exactly the ARN of every domain and application, never
// the direct-query data source's, whose listing carries its tags. Each
// ListApplications page is sent MaxResults 100. No error, no warning.
func TestScanOpenSearch_StoresDomainAndApplicationTags(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newOSTagFixture()
	counted, upserts := countUpserts(st)
	total, err := osTagScan(context.Background(), counted, f, f.osfHandlers())
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != osTestTotal || inserted != osTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, osTestTotal, osTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	domains := rowsByNativeID(t, st, TypeOpenSearchDomain)
	if len(domains) != 2 {
		t.Errorf("domains = %d; want 2", len(domains))
	}
	osAssertRow(t, domains[f.domainARN("logs")], "logs", "active", f.domainStatus("logs"), map[string]string{"team": "search"})
	osAssertRow(t, domains[f.domainARN("metrics")], "metrics", "creating", f.domainStatus("metrics"), nil)
	apps := rowsByNativeID(t, st, TypeOpenSearchApplication)
	if len(apps) != 2 {
		t.Errorf("applications = %d; want 2", len(apps))
	}
	osAssertRow(t, apps[f.appARN("a1")], "app-a1", "ACTIVE", f.app("a1"), map[string]string{"team": "ui"})
	osAssertRow(t, apps[f.appARN("a2")], "app-a2", "", f.app("a2"), nil)
	dq := rowsByNativeID(t, st, TypeOpenSearchDirectQueryDataSource)[osTestDQARN("aws", testRegion, "cwl")]
	assertTagsMap(t, dq.TagsJSON, map[string]string{"team": "obs"})
	if got := f.osfSortedTagARNs(); !slices.Equal(got, f.osfAllTagARNs()) {
		t.Errorf("ListTags ARNs = %v\nwant %v", got, f.osfAllTagARNs())
	}
	if !slices.Equal(f.appMaxResults, []int32{100, 100}) {
		t.Errorf("ListApplications MaxResults = %v; want two calls sent 100", f.appMaxResults)
	}
	if got := slices.Sorted(slices.Values(f.dsDomains)); !slices.Equal(got, []string{"logs", "metrics"}) {
		t.Errorf("ListDataSources domains = %v; want [logs metrics]", got)
	}
	if got := slices.Sorted(slices.Values(f.attachApps)); !slices.Equal(got, []string{"a1", "a2"}) {
		t.Errorf("ListDataSourceAttachments apps = %v; want [a1 a2]", got)
	}
}

// No domain and no application: nothing is described or tag-read, no error,
// no warning.
func TestScanOpenSearch_EmptyListingsReadNoTags(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newOSTagFixture()
	handlers := f.osfHandlers()
	handlers["ListDomainNames"] = func(any) (any, error) { return &opensearch.ListDomainNamesOutput{}, nil }
	handlers["ListApplications"] = func(any) (any, error) { return &opensearch.ListApplicationsOutput{}, nil }
	total, err := osTagScan(context.Background(), st, f, handlers)
	if err != nil || total != 1 {
		t.Errorf("total, err = %d, %v; want 1 (the direct-query data source), nil", total, err)
	}
	if n, m := f.osfCount("DescribeDomain"), f.osfCount("ListTags"); n != 0 || m != 0 {
		t.Errorf("DescribeDomain, ListTags calls = %d, %d; want 0, 0", n, m)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A failed tag read keeps the row untagged unless the resource is gone: a
// not-found drops the row (and an application dropped so is no attachment
// parent); a denial warns once under opensearch:ListTags, scoped to the
// account and region; a transient one is returned as transient; any other
// error is returned. Every other row keeps its tags.
func TestScanOpenSearch_TagReadFailures(t *testing.T) {
	t.Parallel()
	f0 := newOSTagFixture()
	for _, c := range []struct {
		name     string
		arn, typ string
		err      error
		dropped  bool
		outcome  serviceOutcome // 0: no error
		code     string
		warnings int
	}{
		{"domain gone", f0.domainARN("logs"), TypeOpenSearchDomain, osGone, true, 0, "", 0},
		{"application gone", f0.appARN("a1"), TypeOpenSearchApplication, osGone, true, 0, "", 0},
		{"domain denied", f0.domainARN("logs"), TypeOpenSearchDomain, osDenied, false, 0, "", 1},
		{"application denied", f0.appARN("a1"), TypeOpenSearchApplication, osDenied, false, 0, "", 1},
		{"domain throttled", f0.domainARN("logs"), TypeOpenSearchDomain, osTestThrottle, false, outcomeTransient, "ThrottlingException", 0},
		{"application other error", f0.appARN("a1"), TypeOpenSearchApplication, osBadInput, false, outcomeError, "ValidationException", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newOSTagFixture()
			f.tagErrs[c.arn] = c.err
			total, err := osTagScan(context.Background(), st, f, f.osfHandlers())
			if c.outcome == 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			switch {
			case c.dropped && (ok || total != osTestTotal-1):
				t.Errorf("%s stored = %v, total = %d; want dropped, %d", c.arn, ok, total, osTestTotal-1)
			case !c.dropped && (!ok || r.TagsJSON != nil || total != osTestTotal):
				t.Errorf("%s stored = %v with tags %v, total = %d; want kept untagged, %d", c.arn, ok, r.TagsJSON, total, osTestTotal)
			}
			if c.typ == TypeOpenSearchApplication && c.dropped && slices.Contains(f.attachApps, "a1") {
				t.Errorf("attachments listed for %v; want a1's not listed", f.attachApps)
			}
			w := warns()
			if got := kcWarnOps(w); len(got) != c.warnings || c.warnings > 0 && got[0] != osListTagsOp {
				t.Fatalf("warnings = %v; want %d under %s", w, c.warnings, osListTagsOp)
			}
			if c.warnings > 0 && w[0].Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning Scope = %q; want %s/%s", w[0].Scope, testAccountID, testRegion)
			}
			for _, other := range f.osfAllTagARNs() {
				if want := f.wantTags(other); other != c.arn && want != nil {
					typ := TypeOpenSearchDomain
					if other == f.appARN("a1") {
						typ = TypeOpenSearchApplication
					}
					assertTagsMap(t, rowsByNativeID(t, st, typ)[other].TagsJSON, want)
				}
			}
		})
	}
}

// Denied tag reads of a domain and of an application are one warning for
// the scan.
func TestScanOpenSearch_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	f := newOSTagFixture()
	f.tagErrs[f.domainARN("metrics")] = osDenied
	f.tagErrs[f.appARN("a2")] = osDenied
	total, err := osTagScan(context.Background(), st, f, f.osfHandlers())
	if err != nil || total != osTestTotal {
		t.Fatalf("total, err = %d, %v; want %d, nil", total, err, osTestTotal)
	}
	if got := kcWarnOps(warns()); !slices.Equal(got, []string{osListTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, osListTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanOpenSearch_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, osListTagsOp)
	f := newOSTagFixture()
	f.tagErrs[f.domainARN("metrics")] = osTestThrottle
	f.tagErrs[f.appARN("a2")] = osBadInput
	_, err := osTagScan(context.Background(), st, f, f.osfHandlers())
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", osListTagsOp, *warns)
	}
}

// A domain whose describe fails is dropped, as its listing carries no ARN,
// while the other domain is stored with its tags and both names reach the
// data-source phase: not found is silent; a denial warns once under
// opensearch:DescribeDomain; a transient error is returned as transient; any
// other error is returned. A describe answered without an ARN, or without a
// body, warns under opensearch:DescribeDomain. A listed domain without a name
// warns under opensearch:ListDomainNames.
func TestScanOpenSearch_DomainDescribeFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		err     error
		out     *opensearch.DescribeDomainOutput
		outcome serviceOutcome // 0: no error
		code    string
		warnOp  string
	}{
		{name: "gone", err: osGone},
		{name: "denied", err: osDenied, warnOp: osDescribeDomainOp},
		{name: "throttled", err: osTestThrottle, outcome: outcomeTransient, code: "ThrottlingException"},
		{name: "other", err: osBadInput, outcome: outcomeError, code: "ValidationException"},
		{name: "no ARN", out: &opensearch.DescribeDomainOutput{DomainStatus: &opensearchtypes.DomainStatus{DomainName: ptrStr("metrics")}}, warnOp: osDescribeDomainOp},
		{name: "no body", out: &opensearch.DescribeDomainOutput{}, warnOp: osDescribeDomainOp},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			f := newOSTagFixture()
			if c.err != nil {
				f.describeErrs["metrics"] = c.err
			}
			if c.out != nil {
				f.describeOut["metrics"] = c.out
			}
			total, err := osTagScan(context.Background(), st, f, f.osfHandlers())
			if c.outcome == 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			domains := rowsByNativeID(t, st, TypeOpenSearchDomain)
			if _, ok := domains[f.domainARN("metrics")]; ok || len(domains) != 1 || total != osTestTotal-1 {
				t.Errorf("domains = %d (metrics stored: %v), total = %d; want logs only, %d", len(domains), ok, total, osTestTotal-1)
			}
			assertTagsMap(t, domains[f.domainARN("logs")].TagsJSON, map[string]string{"team": "search"})
			if got := f.osfSortedTagARNs(); slices.Contains(got, f.domainARN("metrics")) {
				t.Errorf("ListTags ARNs = %v; want no read for the dropped domain", got)
			}
			if got := slices.Sorted(slices.Values(f.dsDomains)); !slices.Equal(got, []string{"logs", "metrics"}) {
				t.Errorf("ListDataSources domains = %v; want [logs metrics]", got)
			}
			var want []string
			if c.warnOp != "" {
				want = []string{c.warnOp}
			}
			if got := kcWarnOps(warns()); !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
	t.Run("listed without a name", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		f := newOSTagFixture()
		handlers := f.osfHandlers()
		handlers["ListDomainNames"] = func(any) (any, error) {
			return &opensearch.ListDomainNamesOutput{DomainNames: []opensearchtypes.DomainInfo{{DomainName: ptrStr("logs")}, {}}}, nil
		}
		total, err := osTagScan(context.Background(), st, f, handlers)
		if err != nil || total != osTestTotal-1 {
			t.Errorf("total, err = %d, %v; want %d, nil", total, err, osTestTotal-1)
		}
		if n := f.osfCount("DescribeDomain"); n != 1 {
			t.Errorf("DescribeDomain calls = %d; want 1", n)
		}
		if got := kcWarnOps(warns()); !slices.Equal(got, []string{osListDomainNamesOp}) {
			t.Errorf("warnings = %v; want one under %s", got, osListDomainNamesOp)
		}
	})
}

// A throttled describe beside a describe's hard failure is demoted to one
// DescribeDomain warning, so the hard error decides the outcome.
func TestScanOpenSearch_DescribeThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, osDescribeDomainOp)
	f := newOSTagFixture()
	f.describeErrs["logs"], f.describeErrs["metrics"] = osTestThrottle, osBadInput
	total, err := osTagScan(context.Background(), st, f, f.osfHandlers())
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if isTransientNetworkError(err) {
		t.Errorf("err = %v; want not transient", err)
	}
	if *warns != 1 || total != osTestTotal-2 {
		t.Errorf("%s warnings, total = %d, %d; want 1, %d", osDescribeDomainOp, *warns, total, osTestTotal-2)
	}
}

// A describe that cannot reach the endpoint does not stop the later phases:
// the listing reached it, so only that domain is lost.
func TestScanOpenSearch_UnreachableDescribeDoesNotStopLaterPhases(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := newOSTagFixture()
	f.describeErrs["metrics"] = &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	total, err := osTagScan(context.Background(), st, f, f.osfHandlers())
	assertGlueOutcome(t, err, outcomeTransient)
	if total != osTestTotal-1 || f.osfCount("DescribeInboundConnections") != 1 {
		t.Errorf("total = %d, last phase calls = %d; want %d, 1", total, f.osfCount("DescribeInboundConnections"), osTestTotal-1)
	}
}

// A failed write of the domains stops the scan: no warning about the rows it
// lost (a denied describe and a denied tag read among them), and no later
// phase runs.
func TestScanOpenSearch_FailedDomainWriteStopsScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeOpenSearchDomain)
	f := newOSTagFixture()
	f.describeErrs["metrics"] = osDenied
	f.tagErrs[f.domainARN("logs")] = osDenied
	total, err := osTagScan(context.Background(), st, f, f.osfHandlers())
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := f.osfCount("ListApplications"); n != 0 || total != 0 {
		t.Errorf("ListApplications calls = %d, total = %d; want 0, 0 after the failed write", n, total)
	}
}

// A failed later applications page keeps the page read before it, whose
// application is still an attachment parent; the error is returned.
func TestScanOpenSearch_ApplicationsLaterPageFailureKeepsFirstPage(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := newOSTagFixture()
	handlers := f.osfHandlers()
	list := handlers["ListApplications"]
	handlers["ListApplications"] = func(in any) (any, error) {
		if in.(*opensearch.ListApplicationsInput).NextToken != nil {
			return nil, osBadInput
		}
		return list(in)
	}
	_, err := osTagScan(context.Background(), st, f, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	apps := rowsByNativeID(t, st, TypeOpenSearchApplication)
	if _, ok := apps[f.appARN("a1")]; !ok || len(apps) != 1 {
		t.Errorf("applications = %v; want a1 only", apps)
	}
	assertTagsMap(t, apps[f.appARN("a1")].TagsJSON, map[string]string{"team": "ui"})
	if !slices.Equal(f.attachApps, []string{"a1"}) {
		t.Errorf("attachment apps = %v; want [a1]", f.attachApps)
	}
}

// A repeated applications token fails the listing instead of reading as
// complete; the page read is kept.
func TestScanOpenSearch_ApplicationsRepeatedTokenKeepsPage(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := newOSTagFixture()
	handlers := f.osfHandlers()
	handlers["ListApplications"] = func(any) (any, error) {
		f.osfRecord("ListApplications")
		return &opensearch.ListApplicationsOutput{NextToken: ptrStr("same"), ApplicationSummaries: []opensearchtypes.ApplicationSummary{f.app("a1")}}, nil
	}
	_, err := osTagScan(context.Background(), st, f, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if n := f.osfCount("ListApplications"); n != 2 {
		t.Errorf("ListApplications calls = %d; want 2 (stop when the token repeats)", n)
	}
	if _, ok := rowsByNativeID(t, st, TypeOpenSearchApplication)[f.appARN("a1")]; !ok {
		t.Error("a1 missing; want the page read kept")
	}
}

// Domain and application ARNs of a GovCloud or China account reach ListTags
// unchanged.
func TestScanOpenSearch_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			f := newOSTagFixtureIn(c.partition, c.region)
			if _, err := osTagScan(context.Background(), st, f, f.osfHandlers()); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := f.osfSortedTagARNs(); !slices.Equal(got, f.osfAllTagARNs()) {
				t.Errorf("ListTags ARNs = %v\nwant %v", got, f.osfAllTagARNs())
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeOpenSearchDomain)[f.domainARN("logs")].TagsJSON, map[string]string{"team": "search"})
		})
	}
}

// Once ctx ends during the describes, the stop is one member of the error,
// although the describes and the tag reads both saw it, and no later phase
// runs. A describe in flight is sent the scan's ctx, so it sees the end: logs'
// describe waits for it while metrics' ends the scan.
func TestScanOpenSearch_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := newOSTagFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handlers := map[string]func(context.Context, any) (any, error){}
	for op, h := range f.osfHandlers() {
		handlers[op] = func(_ context.Context, in any) (any, error) { return h(in) }
	}
	var logsSawEnd atomic.Bool
	handlers["DescribeDomain"] = func(ctx context.Context, in any) (any, error) {
		f.osfRecord("DescribeDomain")
		if sv(in.(*opensearch.DescribeDomainInput).DomainName) == "metrics" {
			cancel()
			return nil, context.Canceled
		}
		select {
		case <-ctx.Done():
			logsSawEnd.Store(true)
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			return &opensearch.DescribeDomainOutput{DomainStatus: f.domainStatus("logs")}, nil
		}
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfigCtx(handlers, osDenied)
	_, err := scanOpenSearch(ctx, acct, testRegion, st, testScanID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	if !logsSawEnd.Load() {
		t.Error("logs' describe never saw ctx end; want it sent the scan's ctx")
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
	for _, op := range []string{"ListApplications", "ListDirectQueryDataSources", "DescribeInboundConnections"} {
		if n := f.osfCount(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended; want 0", op, n)
		}
	}
}

// A domain listing read once ctx has ended describes none of its domains and
// returns their not being read as a DescribeDomain error carrying the stop,
// so the domains it drops are accounted for.
func TestScanOpenSearchDomains_EndedCtxReportsUndescribed(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	f := newOSTagFixture()
	client := osClient(f.osfHandlers())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var p phaseTotals
	names, failed := scanOpenSearchDomains(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID, openSearchTags(client), &p)
	if failed || !slices.Equal(names, []string{"logs", "metrics"}) {
		t.Errorf("failed, names = %v, %v; want false, [logs metrics]", failed, names)
	}
	if n := f.osfCount("DescribeDomain"); n != 0 || p.total != 0 {
		t.Errorf("DescribeDomain calls, total = %d, %d; want 0, 0", n, p.total)
	}
	stops := 0
	for _, e := range p.errs {
		if e.op == osDescribeDomainOp && errors.Is(e.err, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("%s errors carrying the stop = %d; want 1 (errs %v)", osDescribeDomainOp, stops, p.errs)
	}
}

// Once the domain listing reached the endpoint, an NXDOMAIN from a describe
// or a tag read is a failure of this scan, not a region gap.
func TestScanOpenSearch_LateNXDOMAINIsAnError(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "es.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("describe", func(t *testing.T) {
		st := newTestStore(t)
		f := newOSTagFixture()
		f.describeErrs["metrics"] = nx
		_, err := osTagScan(context.Background(), st, f, f.osfHandlers())
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("tag read", func(t *testing.T) {
		st := newTestStore(t)
		f := newOSTagFixture()
		f.tagErrs[f.appARN("a1")] = nx
		_, err := osTagScan(context.Background(), st, f, f.osfHandlers())
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("describe after a throttled describe", func(t *testing.T) {
		st := newTestStore(t)
		f := newOSTagFixture()
		f.describeErrs["logs"], f.describeErrs["metrics"] = osTestThrottle, nx
		_, err := osTagScan(context.Background(), st, f, f.osfHandlers())
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("every describe, nothing else listed", func(t *testing.T) {
		st := newTestStore(t)
		f := newOSTagFixture()
		f.describeErrs["logs"], f.describeErrs["metrics"] = nx, nx
		handlers := f.osfHandlers()
		handlers["ListApplications"] = func(any) (any, error) { return &opensearch.ListApplicationsOutput{}, nil }
		handlers["ListDirectQueryDataSources"] = func(any) (any, error) { return &opensearch.ListDirectQueryDataSourcesOutput{}, nil }
		total, err := osTagScan(context.Background(), st, f, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != 0 {
			t.Errorf("total = %d; want 0", total)
		}
	})
}
