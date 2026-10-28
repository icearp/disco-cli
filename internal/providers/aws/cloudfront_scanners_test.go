package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"github.com/icearp/disco-cli/store"
)

var (
	cfDenied   = apiErr("AccessDenied", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: cloudfront:List")
	cfNotFound = apiErr("NoSuchResource", "gone")
	cfInvalid  = apiErr("InvalidArgument", "bad arn")
	cfThrottle = apiErr("Throttling", "Rate exceeded")
	// cfNoSubscription is GetMonitoringSubscription's answer for a
	// distribution without one.
	cfNoSubscription = apiErr("NoSuchMonitoringSubscription", "none")
	// cfTime is every fixture element's creation or modification time.
	cfTime = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
)

const (
	cfTagsOp = "cloudfront:ListTagsForResource"
	// cfTotalCount is the rows the full fixture stores: both pages of every
	// tagged kind, one streaming distribution, one field-level encryption
	// profile and the tagged distribution's monitoring subscription.
	cfTotalCount = 2*9 + 3
	// cfScope is the Scope of a denial warning: skipIfAccessDenied scopes a
	// global service's warnings to the account alone.
	cfScope = testAccountID
)

// cfStreamingARN and cfFLEProfileID are the fixture's rows of two listings
// whose tags are not read.
const (
	cfStreamingARN = "arn:aws:cloudfront::" + testAccountID + ":streaming-distribution/ESTREAM"
	cfFLEProfileID = "fle-profile"
)

// cfKind is one listing of the fixture whose rows have their tags read: the
// List op, the type it stores, the ARN resource prefix, the label its
// resources are named after, the Name a row gets, whether the row's CreatedAt
// is the element's cfTime, the Status it gets ("" for none), and page, which
// builds the op's output from an element's ARN and name (the listed id where
// the type has no name field the row uses) and the next marker.
type cfKind struct {
	op, typ, prefix, label string
	name                   cfNaming
	created                bool
	status                 string
	page                   func(arn, name, next *string) any
}

type cfNaming int

const (
	nameByName cfNaming = iota
	nameByID
	nameNone
)

func cfKinds() []cfKind {
	return []cfKind{
		{"ListDistributions", TypeCloudFrontDistribution, "distribution/", "dist", nameByID, true, "Deployed", func(arn, name, next *string) any {
			return &cloudfront.ListDistributionsOutput{DistributionList: &cftypes.DistributionList{Items: []cftypes.DistributionSummary{{ARN: arn, Id: name, Status: sdkaws.String("Deployed"), LastModifiedTime: &cfTime}}, NextMarker: next}}
		}},
		{"ListDistributionTenants", TypeCloudFrontDistributionTenant, "distribution-tenant/", "tenant", nameByID, true, "", func(arn, name, next *string) any {
			return &cloudfront.ListDistributionTenantsOutput{DistributionTenantList: []cftypes.DistributionTenantSummary{{Arn: arn, Id: name, CreatedTime: &cfTime, Status: sdkaws.String("Deployed")}}, NextMarker: next}
		}},
		{"ListFunctions", TypeCloudFrontFunction, "function/", "fn", nameByName, true, "", func(arn, name, next *string) any {
			return &cloudfront.ListFunctionsOutput{FunctionList: &cftypes.FunctionList{Items: []cftypes.FunctionSummary{{Name: name, Status: sdkaws.String("UNASSOCIATED"), FunctionMetadata: &cftypes.FunctionMetadata{FunctionARN: arn, LastModifiedTime: &cfTime}}}, NextMarker: next}}
		}},
		{"ListConnectionFunctions", TypeCloudFrontConnectionFunction, "connection-function/", "cfn", nameByName, true, "", func(arn, name, next *string) any {
			return &cloudfront.ListConnectionFunctionsOutput{ConnectionFunctions: []cftypes.ConnectionFunctionSummary{{ConnectionFunctionArn: arn, Name: name, CreatedTime: &cfTime}}, NextMarker: next}
		}},
		{"ListKeyValueStores", TypeCloudFrontKeyValueStore, "key-value-store/", "kvs", nameByName, false, "", func(arn, name, next *string) any {
			return &cloudfront.ListKeyValueStoresOutput{KeyValueStoreList: &cftypes.KeyValueStoreList{Items: []cftypes.KeyValueStore{{ARN: arn, Name: name, LastModifiedTime: &cfTime}}, NextMarker: next}}
		}},
		{"ListTrustStores", TypeCloudFrontTrustStore, "trust-store/", "ts", nameNone, false, "", func(arn, name, next *string) any {
			return &cloudfront.ListTrustStoresOutput{TrustStoreList: []cftypes.TrustStoreSummary{{Arn: arn, Name: name}}, NextMarker: next}
		}},
		{"ListConnectionGroups", TypeCloudFrontConnectionGroup, "connection-group/", "cg", nameByName, true, "", func(arn, name, next *string) any {
			return &cloudfront.ListConnectionGroupsOutput{ConnectionGroups: []cftypes.ConnectionGroupSummary{{Arn: arn, Name: name, CreatedTime: &cfTime}}, NextMarker: next}
		}},
		{"ListAnycastIpLists", TypeCloudFrontAnycastIPList, "anycast-ip-list/", "aip", nameByName, false, "", func(arn, name, next *string) any {
			return &cloudfront.ListAnycastIpListsOutput{AnycastIpLists: &cftypes.AnycastIpListCollection{Items: []cftypes.AnycastIpListSummary{{Arn: arn, Name: name}}, NextMarker: next}}
		}},
		{"ListVpcOrigins", TypeCloudFrontVpcOrigin, "vpcorigin/", "vo", nameByName, false, "", func(arn, name, next *string) any {
			return &cloudfront.ListVpcOriginsOutput{VpcOriginList: &cftypes.VpcOriginList{Items: []cftypes.VpcOriginSummary{{Arn: arn, Name: name}}, NextMarker: next}}
		}},
	}
}

func cfKindOf(t *testing.T, op string) cfKind {
	t.Helper()
	for _, k := range cfKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("no fixture kind for %s", op)
	return cfKind{}
}

// cfName is the listed name (or id) of the fixture's resource suffix
// ("tagged" or "bare") of kind k.
func cfName(k cfKind, suffix string) string { return k.label + "-" + suffix }

func cfARNIn(partition, account, resource string) string {
	return "arn:" + partition + ":cloudfront::" + account + ":" + resource
}

func cfARN(k cfKind, suffix string) string {
	return cfARNIn("aws", testAccountID, k.prefix+cfName(k, suffix))
}

// cfDistID and cfDistARN are the id and ARN of the fixture's distribution
// suffix.
func cfDistID(suffix string) string { return "dist-" + suffix }

func cfDistARN(suffix string) string {
	return cfARNIn("aws", testAccountID, "distribution/"+cfDistID(suffix))
}

// cfCalls records the stubbed calls: how often each op ran, the MaxItems
// each list op was sent, the ARNs sent to ListTagsForResource, which it
// answers from tags or errs, both keyed by ARN, and the distribution ids sent
// to GetMonitoringSubscription, which it answers from monitoring (keyed by
// id; an error, or nil for a subscription). Calls run concurrently.
type cfCalls struct {
	mu         sync.Mutex
	ops        map[string]int
	maxItems   map[string][]*int32
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
	monitorIDs []string
	monitoring map[string]error
}

// newCFCalls tags every "tagged" resource with its type and gives the
// tagged distribution a monitoring subscription and the bare one none.
func newCFCalls() *cfCalls {
	c := &cfCalls{
		ops: map[string]int{}, maxItems: map[string][]*int32{}, tags: map[string]map[string]string{}, errs: map[string]error{},
		monitoring: map[string]error{cfDistID("tagged"): nil, cfDistID("bare"): cfNoSubscription},
	}
	for _, k := range cfKinds() {
		c.tags[cfARN(k, "tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

func (c *cfCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	if strings.HasPrefix(op, "List") && op != "ListTagsForResource" {
		c.maxItems[op] = append(c.maxItems[op], bacField[*int32](in, "MaxItems"))
	}
}

func (c *cfCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

func (c *cfCalls) sortedMonitorIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := slices.Clone(c.monitorIDs)
	slices.Sort(ids)
	return ids
}

func (c *cfCalls) listTags(in any) (any, error) {
	arn := sv(in.(*cloudfront.ListTagsForResourceInput).Resource)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var items []cftypes.Tag
	for k, v := range tags {
		items = append(items, cftypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &cloudfront.ListTagsForResourceOutput{Tags: &cftypes.Tags{Items: items}}, nil
}

func (c *cfCalls) getMonitoring(in any) (any, error) {
	id := sv(in.(*cloudfront.GetMonitoringSubscriptionInput).DistributionId)
	c.record("GetMonitoringSubscription", in)
	c.mu.Lock()
	c.monitorIDs = append(c.monitorIDs, id)
	err, known := c.monitoring[id]
	c.mu.Unlock()
	if !known {
		return nil, errors.New("test: unknown distribution " + id)
	}
	if err != nil {
		return nil, err
	}
	return &cloudfront.GetMonitoringSubscriptionOutput{MonitoringSubscription: &cftypes.MonitoringSubscription{
		RealtimeMetricsSubscriptionConfig: &cftypes.RealtimeMetricsSubscriptionConfig{RealtimeMetricsSubscriptionStatus: cftypes.RealtimeMetricsSubscriptionStatusEnabled},
	}}, nil
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second.
func (c *cfCalls) twoPages(k cfKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("m2")
		if bacField[*string](in, "Marker") != nil {
			suffix, next = "bare", nil
		}
		arn, name := cfARN(k, suffix), cfName(k, suffix)
		return k.page(&arn, &name, next), nil
	}
}

// cfHandlers answers every op the scan calls: the tagged kinds with two
// pages each, the streaming-distribution and field-level encryption profile
// listings with one row each, every other listing empty, and the monitoring
// subscriptions from c.
func cfHandlers(c *cfCalls) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){"ListTagsForResource": c.listTags, "GetMonitoringSubscription": c.getMonitoring}
	for _, k := range cfKinds() {
		h[k.op] = c.twoPages(k)
	}
	empty := map[string]any{
		"ListCachePolicies":                    &cloudfront.ListCachePoliciesOutput{},
		"ListOriginRequestPolicies":            &cloudfront.ListOriginRequestPoliciesOutput{},
		"ListResponseHeadersPolicies":          &cloudfront.ListResponseHeadersPoliciesOutput{},
		"ListContinuousDeploymentPolicies":     &cloudfront.ListContinuousDeploymentPoliciesOutput{},
		"ListCloudFrontOriginAccessIdentities": &cloudfront.ListCloudFrontOriginAccessIdentitiesOutput{},
		"ListOriginAccessControls":             &cloudfront.ListOriginAccessControlsOutput{},
		"ListKeyGroups":                        &cloudfront.ListKeyGroupsOutput{},
		"ListPublicKeys":                       &cloudfront.ListPublicKeysOutput{},
		"ListRealtimeLogConfigs":               &cloudfront.ListRealtimeLogConfigsOutput{},
		"ListFieldLevelEncryptionConfigs":      &cloudfront.ListFieldLevelEncryptionConfigsOutput{},
		"ListStreamingDistributions": &cloudfront.ListStreamingDistributionsOutput{StreamingDistributionList: &cftypes.StreamingDistributionList{
			Items: []cftypes.StreamingDistributionSummary{{ARN: sdkaws.String(cfStreamingARN), Id: sdkaws.String("ESTREAM")}},
		}},
		"ListFieldLevelEncryptionProfiles": &cloudfront.ListFieldLevelEncryptionProfilesOutput{FieldLevelEncryptionProfileList: &cftypes.FieldLevelEncryptionProfileList{
			Items: []cftypes.FieldLevelEncryptionProfileSummary{{Id: sdkaws.String(cfFLEProfileID)}},
		}},
	}
	for op, out := range empty {
		h[op] = func(in any) (any, error) {
			c.record(op, in)
			return out, nil
		}
	}
	return h
}

// cfScan runs the scan's real entry point against handlers; every op they do
// not answer is denied.
func cfScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, cfDenied)
	return scanCloudFront(ctx, acct, st, testScanID)
}

func cfRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// cfMonitoringARN is the NativeID of the monitoring subscription of the
// fixture's distribution suffix.
func cfMonitoringARN(suffix string) string { return cfDistARN(suffix) + "/monitoring-subscription" }

// Every tagged type stores both pages of its listing, named as it is listed,
// in the region "global", with its CreatedAt and Status, and with the tags
// ListTagsForResource returns for its ARN, nil for a resource without tags.
// Exactly the ARN of every such row is sent to the tag op: not the streaming
// distribution's, nor any other type's. No list op is sent MaxItems. The
// distributions are listed once, and the monitoring subscription of each
// stored one is read. No error or warning.
func TestScanCloudFront_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newCFCalls()

	total, inserted, err := cfScan(context.Background(), st, cfHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != cfTotalCount || inserted != cfTotalCount {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, cfTotalCount, cfTotalCount)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantARNs []string
	for _, k := range cfKinds() {
		for _, suffix := range []string{"tagged", "bare"} {
			arn := cfARN(k, suffix)
			wantARNs = append(wantARNs, arn)
			r, ok := cfRowOf(t, st, k.typ, arn)
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			wantName := cfName(k, suffix)
			if k.name == nameNone {
				wantName = ""
			}
			if got := sv(r.Name); got != wantName {
				t.Errorf("%s %s Name = %q; want %q", k.typ, arn, got, wantName)
			}
			if sv(r.Region) != "global" {
				t.Errorf("%s %s Region = %q; want global", k.typ, arn, sv(r.Region))
			}
			wantCreated := ""
			if k.created {
				wantCreated = sv(tp(&cfTime))
			}
			if got := sv(r.CreatedAt); got != wantCreated {
				t.Errorf("%s %s CreatedAt = %q; want %q", k.typ, arn, got, wantCreated)
			}
			if got := sv(r.Status); got != k.status {
				t.Errorf("%s %s Status = %q; want %q", k.typ, arn, got, k.status)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s %s TagsJSON = %q; want nil", k.typ, arn, *r.TagsJSON)
			}
		}
	}
	if _, ok := cfRowOf(t, st, TypeCloudFrontStreamingDistribution, cfStreamingARN); !ok {
		t.Error("streaming distribution missing")
	}
	if r, ok := cfRowOf(t, st, TypeCloudFrontMonitoringSubscription, cfMonitoringARN("tagged")); !ok || sv(r.Name) != cfDistID("tagged") {
		t.Errorf("monitoring subscription stored = %v, Name = %q; want stored, named %s", ok, sv(r.Name), cfDistID("tagged"))
	}
	slices.Sort(wantARNs)
	slices.Sort(calls.tagARNs)
	if !slices.Equal(calls.tagARNs, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", calls.tagARNs, wantARNs)
	}
	for op, sent := range calls.maxItems {
		for _, n := range sent {
			if n != nil {
				t.Errorf("%s MaxItems = %d; want unset", op, *n)
			}
		}
	}
	if n := calls.count("ListDistributions"); n != 2 {
		t.Errorf("ListDistributions calls = %d; want 2 (one listing of two pages)", n)
	}
	if got, want := calls.sortedMonitorIDs(), []string{cfDistID("bare"), cfDistID("tagged")}; !slices.Equal(got, want) {
		t.Errorf("GetMonitoringSubscription ids = %v; want %v", got, want)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanCloudFront_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newCFCalls()
	if _, _, err := cfScan(context.Background(), st, cfHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := cfScan(context.Background(), st, cfHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != cfTotalCount || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, cfTotalCount)
	}
}

// A failed tag read: NoSuchResource, the op's modeled not-found, drops the
// row silently (a dropped distribution takes its monitoring subscription
// with it); a denial keeps the row untagged and warns once under
// cloudfront:ListTagsForResource, scoped to the account; a throttle after
// retries keeps it and is a transient error; InvalidArgument keeps it and is
// a hard error. Each outcome holds for a row of every tagged kind.
func TestScanCloudFront_TagReadFailures(t *testing.T) {
	for _, k := range cfKinds() {
		t.Run(k.op, func(t *testing.T) {
			arn := cfARN(k, "tagged")
			dropped := 1
			if k.op == "ListDistributions" {
				dropped = 2
			}
			for _, c := range []struct {
				name      string
				err       error
				outcome   serviceOutcome
				code      string
				wantTotal int
				wantKept  bool
				wantWarns int
			}{
				{"not found", cfNotFound, 0, "", cfTotalCount - dropped, false, 0},
				{"denied", cfDenied, 0, "", cfTotalCount, true, 1},
				{"transient", cfThrottle, outcomeTransient, "", cfTotalCount, true, 0},
				{"other error", cfInvalid, outcomeError, "InvalidArgument", cfTotalCount, true, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					all := countWarnings(st)
					warnings := bacWarnings(st, cfTagsOp)
					calls := newCFCalls()
					calls.errs[arn] = c.err

					total, _, err := cfScan(context.Background(), st, cfHandlers(calls))
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
					r, ok := cfRowOf(t, st, k.typ, arn)
					if ok != c.wantKept {
						t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if len(*warnings) != c.wantWarns || *all != c.wantWarns {
						t.Fatalf("%s warnings = %v (all ops %d); want %d", cfTagsOp, *warnings, *all, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != cfScope {
							t.Errorf("warning Scope = %q; want %s", w.Scope, cfScope)
						}
					}
				})
			}
		})
	}
}

// Denied tag reads in two listings, which run as separate families, send one
// warning for the whole scan, counting every denied row; the rows are kept.
func TestScanCloudFront_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, cfTagsOp)
	calls := newCFCalls()
	for _, op := range []string{"ListTrustStores", "ListKeyValueStores"} {
		k := cfKindOf(t, op)
		calls.errs[cfARN(k, "tagged")] = cfDenied
		calls.errs[cfARN(k, "bare")] = cfDenied
	}

	total, _, err := cfScan(context.Background(), st, cfHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != cfTotalCount {
		t.Errorf("total = %d; want %d", total, cfTotalCount)
	}
	if len(*warnings) != 1 {
		t.Fatalf("%s warnings = %d; want 1", cfTagsOp, len(*warnings))
	}
	if msg := (*warnings)[0].Message; !strings.Contains(msg, "4 row(s)") {
		t.Errorf("warning Message = %q; want it to count the 4 denied rows", msg)
	}
}

// cfGatedTags answers ListTagsForResource with no tags and records the most
// reads in flight at once. Each read is held until more than fanoutMed are in
// flight (over closes) or cfGateHold passes, so the reads overlap as far as
// the bound lets them, and an unbounded scan overlaps all of them.
const cfGateHold = 100 * time.Millisecond

type cfGatedTags struct {
	over     chan struct{}
	once     sync.Once
	inFlight atomic.Int32
	most     atomic.Int32
	calls    atomic.Int32
}

func (g *cfGatedTags) listTags(any) (any, error) {
	g.calls.Add(1)
	n := g.inFlight.Add(1)
	defer g.inFlight.Add(-1)
	for {
		m := g.most.Load()
		if n <= m || g.most.CompareAndSwap(m, n) {
			break
		}
	}
	if n > fanoutMed {
		g.once.Do(func() { close(g.over) })
	}
	select {
	case <-g.over:
	case <-time.After(cfGateHold):
	}
	return &cloudfront.ListTagsForResourceOutput{}, nil
}

// The tag reads of every family share one bound of fanoutMed, though each
// family's readRowTags alone would allow fanoutMed.
func TestScanCloudFront_TagReadsShareOneBound(t *testing.T) {
	st := newTestStore(t)
	gate := &cfGatedTags{over: make(chan struct{})}
	handlers := cfHandlers(newCFCalls())
	handlers["ListTagsForResource"] = gate.listTags

	if _, _, err := cfScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := gate.calls.Load(); got != 2*9 {
		t.Errorf("ListTagsForResource calls = %d; want %d", got, 2*9)
	}
	if most := gate.most.Load(); most > fanoutMed {
		t.Errorf("tag reads in flight at once = %d; want at most %d", most, fanoutMed)
	}
}

// A VPC origin another account owns, whose tag read is denied or not found,
// is kept untagged without a warning or an error. The same failures for this
// account's VPC origin still warn or drop the row.
func TestScanCloudFront_OtherAccountVpcOriginKeptUntagged(t *testing.T) {
	const other = "210987654321"
	shared := cfARNIn("aws", other, "vpcorigin/shared")
	for _, c := range []struct {
		name               string
		err                error
		ownKept, ownWarned bool
	}{
		{"denied", cfDenied, true, true},
		{"not found", cfNotFound, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			calls := newCFCalls()
			k := cfKindOf(t, "ListVpcOrigins")
			own := cfARN(k, "tagged")
			calls.errs[shared] = c.err
			calls.errs[own] = c.err
			handlers := cfHandlers(calls)
			handlers[k.op] = func(in any) (any, error) {
				calls.record(k.op, in)
				return &cloudfront.ListVpcOriginsOutput{VpcOriginList: &cftypes.VpcOriginList{Items: []cftypes.VpcOriginSummary{
					{Arn: sdkaws.String(shared), AccountId: sdkaws.String(other)},
					{Arn: sdkaws.String(own)},
				}}}, nil
			}

			if _, _, err := cfScan(context.Background(), st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			r, ok := cfRowOf(t, st, TypeCloudFrontVpcOrigin, shared)
			if !ok {
				t.Fatal("other account's VPC origin missing")
			}
			if r.TagsJSON != nil {
				t.Errorf("other account's TagsJSON = %q; want nil", *r.TagsJSON)
			}
			if _, ok := cfRowOf(t, st, TypeCloudFrontVpcOrigin, own); ok != c.ownKept {
				t.Errorf("own VPC origin stored = %v; want %v", ok, c.ownKept)
			}
			if want := map[bool]int{true: 1, false: 0}[c.ownWarned]; *all != want {
				t.Errorf("warnings = %d; want %d", *all, want)
			}
		})
	}
}

// A throttled tag read beside a hard failure of the same op in another
// family is demoted to one warning, so the hard error decides the outcome.
func TestScanCloudFront_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, cfTagsOp)
	calls := newCFCalls()
	calls.errs[cfARN(cfKindOf(t, "ListFunctions"), "tagged")] = cfThrottle
	calls.errs[cfARN(cfKindOf(t, "ListConnectionGroups"), "bare")] = cfInvalid

	_, _, err := cfScan(context.Background(), st, cfHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidArgument")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", cfTagsOp, *warns)
	}
}

// A denied listing warns once under its own op and stores nothing of its
// type; every other listing still runs. Denied distributions leave no parent
// for the monitoring subscriptions.
func TestScanCloudFront_DeniedListingWarnsUnderItsOp(t *testing.T) {
	for _, k := range cfKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warnings := bacWarnings(st, "cloudfront:"+k.op)
			calls := newCFCalls()
			handlers := cfHandlers(calls)
			delete(handlers, k.op)

			total, _, err := cfScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			lost := 2
			if k.op == "ListDistributions" {
				lost = 3
			}
			if total != cfTotalCount-lost {
				t.Errorf("total = %d; want %d", total, cfTotalCount-lost)
			}
			if len(*warnings) != 1 || *all != 1 {
				t.Errorf("cloudfront:%s warnings = %v (all ops %d); want 1", k.op, *warnings, *all)
			}
			if got := len(rowsByNativeID(t, st, k.typ)); got != 0 {
				t.Errorf("%s rows = %d; want 0", k.typ, got)
			}
			if k.op == "ListDistributions" && calls.count("GetMonitoringSubscription") != 0 {
				t.Error("GetMonitoringSubscription called without a stored distribution")
			}
		})
	}
}

// Each distribution's GetMonitoringSubscription failure is handled on its
// own, the other distribution's subscription still read: a modeled code for
// a distribution without a subscription, deleted, or not offering one skips
// it silently; a denial warns once under the op; any other error is returned
// with every row kept.
func TestScanCloudFront_MonitoringSubscriptionFailures(t *testing.T) {
	for _, c := range []struct {
		name      string
		err       error
		code      string
		wantWarns int
	}{
		{"no subscription", cfNoSubscription, "", 0},
		{"distribution gone", apiErr("NoSuchDistribution", "gone"), "", 0},
		{"unsupported", apiErr("UnsupportedOperation", "no"), "", 0},
		{"denied", cfDenied, "", 1},
		{"other error", cfInvalid, "InvalidArgument", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warnings := bacWarnings(st, cfMonitoringOp)
			calls := newCFCalls()
			// The bare distribution gets the subscription, the tagged one
			// the failure.
			calls.monitoring[cfDistID("bare")] = nil
			calls.monitoring[cfDistID("tagged")] = c.err

			total, _, err := cfScan(context.Background(), st, cfHandlers(calls))
			if c.code != "" {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, c.code)
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != cfTotalCount {
				t.Errorf("total = %d; want %d", total, cfTotalCount)
			}
			if _, ok := cfRowOf(t, st, TypeCloudFrontMonitoringSubscription, cfMonitoringARN("bare")); !ok {
				t.Error("the other distribution's subscription missing")
			}
			if _, ok := cfRowOf(t, st, TypeCloudFrontMonitoringSubscription, cfMonitoringARN("tagged")); ok {
				t.Error("a subscription stored for the failed distribution")
			}
			if len(*warnings) != c.wantWarns || *all != c.wantWarns {
				t.Errorf("%s warnings = %v (all ops %d); want %d", cfMonitoringOp, *warnings, *all, c.wantWarns)
			}
		})
	}
}

// A distribution the tag read drops as deleted is not a parent: its
// monitoring subscription is not read.
func TestScanCloudFront_DroppedDistributionHasNoMonitoringRead(t *testing.T) {
	st := newTestStore(t)
	calls := newCFCalls()
	calls.errs[cfDistARN("tagged")] = cfNotFound

	if _, _, err := cfScan(context.Background(), st, cfHandlers(calls)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got, want := calls.sortedMonitorIDs(), []string{cfDistID("bare")}; !slices.Equal(got, want) {
		t.Errorf("GetMonitoringSubscription ids = %v; want %v", got, want)
	}
}

// A failed write fails the scan and sends no warning about the rows it lost;
// its family stops, so the lost distributions' subscriptions are not read,
// while every other family's rows are stored.
func TestScanCloudFront_FailedWriteKeepsOtherFamilies(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeCloudFrontDistribution)
	calls := newCFCalls()
	calls.errs[cfDistARN("tagged")] = cfDenied

	total, _, err := cfScan(context.Background(), st, cfHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	if n := calls.count("GetMonitoringSubscription"); n != 0 {
		t.Errorf("GetMonitoringSubscription called %d times after the failed write", n)
	}
	if want := cfTotalCount - 3; total != want {
		t.Errorf("total = %d; want %d (every other family's rows)", total, want)
	}
	if _, ok := cfRowOf(t, st, TypeCloudFrontFieldLevelEncryptionProfile, cfFLEProfileID); !ok {
		t.Error("field-level encryption profile (another family) missing")
	}
}

// A listing's failed page keeps the pages read before it, and its error does
// not stop the other listings.
func TestScanCloudFront_ListFailureKeepsPagesAndOtherFamilies(t *testing.T) {
	st := newTestStore(t)
	handlers := cfHandlers(newCFCalls())
	k := cfKindOf(t, "ListDistributionTenants")
	handlers[k.op] = failOnCall(2, cfInvalid, handlers[k.op])

	total, _, err := cfScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidArgument")
	if total != cfTotalCount-1 {
		t.Errorf("total = %d; want %d", total, cfTotalCount-1)
	}
	if _, ok := cfRowOf(t, st, k.typ, cfARN(k, "tagged")); !ok {
		t.Error("tenant from the page before the failure missing")
	}
	if _, ok := cfRowOf(t, st, TypeCloudFrontFieldLevelEncryptionProfile, cfFLEProfileID); !ok {
		t.Error("field-level encryption profile missing")
	}
	if got := len(rowsByNativeID(t, st, TypeCloudFrontVpcOrigin)); got != 2 {
		t.Errorf("VPC origins = %d; want 2", got)
	}
}

// A repeated marker ends the listing as an error, keeping its pages.
func TestScanCloudFront_RepeatedMarkerKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := cfHandlers(newCFCalls())
	k := cfKindOf(t, "ListKeyValueStores")
	handlers[k.op] = func(in any) (any, error) {
		suffix := "tagged"
		if bacField[*string](in, "Marker") != nil {
			suffix = "bare"
		}
		arn, name := cfARN(k, suffix), cfName(k, suffix)
		return k.page(&arn, &name, sdkaws.String("same")), nil
	}

	total, _, err := cfScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != cfTotalCount {
		t.Errorf("total = %d; want %d", total, cfTotalCount)
	}
	for _, suffix := range []string{"tagged", "bare"} {
		if _, ok := cfRowOf(t, st, k.typ, cfARN(k, suffix)); !ok {
			t.Errorf("key value store %s from the pages read missing", suffix)
		}
	}
}

// An element without an ARN, of any tagged kind, is not stored and is warned
// about once under its listing, named by its listed name or id; it is not
// sent to the tag op, and a distribution without one is no monitoring
// parent.
func TestScanCloudFront_ElementWithoutARNWarns(t *testing.T) {
	for _, k := range cfKinds() {
		t.Run(k.op, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warns := bacWarnings(st, "cloudfront:"+k.op)
			calls := newCFCalls()
			handlers := cfHandlers(calls)
			key := cfName(k, "noarn")
			handlers[k.op] = func(any) (any, error) { return k.page(nil, &key, nil), nil }

			if _, _, err := cfScan(context.Background(), st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := len(rowsByNativeID(t, st, k.typ)); got != 0 {
				t.Errorf("%s rows = %d; want 0", k.typ, got)
			}
			if slices.Contains(calls.tagARNs, "") {
				t.Error("an empty ARN was sent to the tag op")
			}
			if n := calls.count("GetMonitoringSubscription"); k.op == "ListDistributions" && n != 0 {
				t.Errorf("GetMonitoringSubscription called %d times for an ARN-less distribution", n)
			}
			if len(*warns) != 1 || *all != 1 {
				t.Fatalf("cloudfront:%s warnings = %v (all ops %d); want 1", k.op, *warns, *all)
			}
			if msg := (*warns)[0].Message; !strings.Contains(msg, key) {
				t.Errorf("warning Message = %q; want it to name the element by key %q", msg, key)
			}
		})
	}
}

// A function without FunctionMetadata has no ARN, and an element with
// neither ARN nor name is named by a fixed text.
func TestScanCloudFront_ElementWithoutIdentifiers(t *testing.T) {
	for _, c := range []struct {
		op, key string
		out     any
	}{
		{"ListFunctions", "fn-nometa", &cloudfront.ListFunctionsOutput{FunctionList: &cftypes.FunctionList{Items: []cftypes.FunctionSummary{{Name: sdkaws.String("fn-nometa")}}}}},
		{"ListVpcOrigins", "(an element with no identifier)", &cloudfront.ListVpcOriginsOutput{VpcOriginList: &cftypes.VpcOriginList{Items: []cftypes.VpcOriginSummary{{}}}}},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := bacWarnings(st, "cloudfront:"+c.op)
			handlers := cfHandlers(newCFCalls())
			handlers[c.op] = func(any) (any, error) { return c.out, nil }

			if _, _, err := cfScan(context.Background(), st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if len(*warns) != 1 || !strings.Contains((*warns)[0].Message, c.key) {
				t.Errorf("cloudfront:%s warnings = %v; want one naming %q", c.op, *warns, c.key)
			}
		})
	}
}

// cfStageCase builds the entries of one function listing: entry is one
// summary, page the op's output listing entries in order, and noARN an
// entry named name without an ARN.
type cfStageCase struct {
	op, typ string
	entry   func(arn, name string, stage cftypes.FunctionStage, status string) any
	page    func(entries []any) any
	noARN   func(name string) any
}

func cfStageCases() []cfStageCase {
	return []cfStageCase{
		{
			"ListFunctions", TypeCloudFrontFunction,
			func(arn, name string, stage cftypes.FunctionStage, status string) any {
				return cftypes.FunctionSummary{Name: &name, Status: &status, FunctionMetadata: &cftypes.FunctionMetadata{FunctionARN: &arn, Stage: stage}}
			},
			func(entries []any) any {
				var items []cftypes.FunctionSummary
				for _, e := range entries {
					items = append(items, e.(cftypes.FunctionSummary))
				}
				return &cloudfront.ListFunctionsOutput{FunctionList: &cftypes.FunctionList{Items: items}}
			},
			func(name string) any { return cftypes.FunctionSummary{Name: &name} },
		},
		{
			"ListConnectionFunctions", TypeCloudFrontConnectionFunction,
			func(arn, name string, stage cftypes.FunctionStage, status string) any {
				return cftypes.ConnectionFunctionSummary{ConnectionFunctionArn: &arn, Name: &name, Stage: stage, Status: &status}
			},
			func(entries []any) any {
				var items []cftypes.ConnectionFunctionSummary
				for _, e := range entries {
					items = append(items, e.(cftypes.ConnectionFunctionSummary))
				}
				return &cloudfront.ListConnectionFunctionsOutput{ConnectionFunctions: items}
			},
			func(name string) any { return cftypes.ConnectionFunctionSummary{Name: &name} },
		},
	}
}

// cfJSON decodes a JSON document for comparison.
func cfJSON(t *testing.T, doc string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatalf("decode %s: %v", doc, err)
	}
	return v
}

// A function listed in both stages under one ARN is one row, counted and
// tag-read once: its LIVE summary, with the DEVELOPMENT summary under
// DevelopmentStage, whichever stage is listed first. A function listed in one
// stage only is stored as its summary alone. An ARN listed twice in
// DEVELOPMENT, which the API does not do, is stored as its first entry.
func TestScanCloudFront_FunctionStagesStoredOnce(t *testing.T) {
	for _, c := range cfStageCases() {
		for _, liveFirst := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/live first %v", c.op, liveFirst), func(t *testing.T) {
				st := newTestStore(t)
				calls := newCFCalls()
				handlers := cfHandlers(calls)
				both := cfARNIn("aws", testAccountID, "function/both")
				devOnly := cfARNIn("aws", testAccountID, "function/dev")
				liveOnly := cfARNIn("aws", testAccountID, "function/live")
				repeated := cfARNIn("aws", testAccountID, "function/repeated")
				dev := c.entry(both, "both", cftypes.FunctionStageDevelopment, "UNPUBLISHED")
				live := c.entry(both, "both", cftypes.FunctionStageLive, "UNASSOCIATED")
				entries := []any{dev, live}
				if liveFirst {
					entries = []any{live, dev}
				}
				only := map[string]any{
					devOnly:  c.entry(devOnly, "dev", cftypes.FunctionStageDevelopment, "UNPUBLISHED"),
					liveOnly: c.entry(liveOnly, "live", cftypes.FunctionStageLive, "UNASSOCIATED"),
					repeated: c.entry(repeated, "repeated", cftypes.FunctionStageDevelopment, "first"),
				}
				entries = append(entries, only[devOnly], only[liveOnly], only[repeated],
					c.entry(repeated, "repeated", cftypes.FunctionStageDevelopment, "second"))
				handlers[c.op] = func(any) (any, error) { return c.page(entries), nil }

				total, _, err := cfScan(context.Background(), st, handlers)
				if err != nil {
					t.Fatalf("scan: %v", err)
				}
				if want := cfTotalCount - 2 + 4; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				rows := rowsByNativeID(t, st, c.typ)
				want := cfJSON(t, mustJSON(live)).(map[string]any)
				want["DevelopmentStage"] = cfJSON(t, mustJSON(dev))
				if got := cfJSON(t, rows[both].AttributesJSON); !reflect.DeepEqual(got, want) {
					t.Errorf("%s attributes = %v\nwant %v", both, got, want)
				}
				for arn, entry := range only {
					if got, want := cfJSON(t, rows[arn].AttributesJSON), cfJSON(t, mustJSON(entry)); !reflect.DeepEqual(got, want) {
						t.Errorf("%s attributes = %v\nwant %v", arn, got, want)
					}
				}
				sent := 0
				for _, arn := range calls.tagARNs {
					if arn == both {
						sent++
					}
				}
				if sent != 1 {
					t.Errorf("%s sent to the tag op %d times; want 1", both, sent)
				}
			})
		}
	}
}

// Listed functions without an ARN are not stored and are warned about once
// under their op, scoped to the account's global region.
func TestScanCloudFront_FunctionsWithoutARNWarned(t *testing.T) {
	for _, c := range cfStageCases() {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			all := countWarnings(st)
			warns := bacWarnings(st, "cloudfront:"+c.op)
			handlers := cfHandlers(newCFCalls())
			handlers[c.op] = func(any) (any, error) { return c.page([]any{c.noARN("a"), c.noARN("b")}), nil }

			if _, _, err := cfScan(context.Background(), st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if len(*warns) != 1 || *all != 1 {
				t.Fatalf("cloudfront:%s warnings = %v (all ops %d); want 1", c.op, *warns, *all)
			}
			if got, want := (*warns)[0].Scope, testAccountID+"/global"; got != want {
				t.Errorf("warning Scope = %q; want %q", got, want)
			}
			if got := len(rowsByNativeID(t, st, c.typ)); got != 0 {
				t.Errorf("%s rows = %d; want 0", c.typ, got)
			}
		})
	}
}

// cfPairEntry is an entry of the cfOneRowPerARN tests.
type cfPairEntry struct {
	arn   string
	stage cftypes.FunctionStage
	n     int
}

func cfPairArnStage(e cfPairEntry) (string, cftypes.FunctionStage) { return e.arn, e.stage }

// cfOneRowPerARN pairs LIVE with DEVELOPMENT, drops a true repeat of a kept
// stage silently, records every other further entry of an ARN as not
// stored, keyed by the ARN, and makes each entry without an ARN its own row.
func TestCFOneRowPerARN_Entries(t *testing.T) {
	const live, dev = cftypes.FunctionStageLive, cftypes.FunctionStageDevelopment
	for _, c := range []struct {
		name      string
		entries   []cfPairEntry
		wantRows  []int // n of each row's entry
		wantDev   []int // n of each row's development entry, 0 for none
		wantSkips int
	}{
		{"two without an ARN", []cfPairEntry{{"", live, 1}, {"", dev, 2}}, []int{1, 2}, []int{0, 0}, 0},
		{"LIVE then DEVELOPMENT", []cfPairEntry{{"a", live, 1}, {"a", dev, 2}}, []int{1}, []int{2}, 0},
		{"DEVELOPMENT then LIVE", []cfPairEntry{{"a", dev, 1}, {"a", live, 2}}, []int{2}, []int{1}, 0},
		{"DEVELOPMENT repeated", []cfPairEntry{{"a", dev, 1}, {"a", dev, 2}}, []int{1}, []int{0}, 0},
		{"LIVE repeated", []cfPairEntry{{"a", live, 1}, {"a", live, 2}}, []int{1}, []int{0}, 0},
		{"pair then DEVELOPMENT repeated", []cfPairEntry{{"a", live, 1}, {"a", dev, 2}, {"a", dev, 3}}, []int{1}, []int{2}, 0},
		{"empty stage then LIVE", []cfPairEntry{{"a", "", 1}, {"a", live, 2}}, []int{2}, []int{0}, 1},
		{"LIVE then empty stage", []cfPairEntry{{"a", live, 1}, {"a", "", 2}}, []int{1}, []int{0}, 1},
		{"DEVELOPMENT then unknown stage", []cfPairEntry{{"a", dev, 1}, {"a", "STAGED", 2}}, []int{1}, []int{0}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			var skips notStoredSkips
			rows := cfOneRowPerARN(c.entries, cfPairArnStage, &skips)
			var gotRows, gotDev []int
			for _, r := range rows {
				gotRows = append(gotRows, r.item.n)
				d := 0
				if r.development != nil {
					d = r.development.n
				}
				gotDev = append(gotDev, d)
			}
			if !slices.Equal(gotRows, c.wantRows) || !slices.Equal(gotDev, c.wantDev) {
				t.Errorf("rows = %v, development = %v; want %v, %v", gotRows, gotDev, c.wantRows, c.wantDev)
			}
			gotSkips, first := 0, ""
			if tally := skips.tallies[cfUnpairedStage]; tally != nil {
				gotSkips, first = tally.count, tally.first
			}
			if gotSkips != c.wantSkips || (c.wantSkips > 0 && first != "a") {
				t.Errorf("not stored = %d, first %q; want %d keyed by \"a\"", gotSkips, first, c.wantSkips)
			}
		})
	}
}

// An aws-cn account's rows are sent to the tag op under the ARNs the
// listings return, in the aws-cn partition.
func TestScanCloudFront_TagReadInAccountPartition(t *testing.T) {
	acct := partitionAccount("aws-cn")
	st := newTestStore(t)
	calls := newCFCalls()
	dist := cfARNIn("aws-cn", testAccountID, "distribution/ECN")
	fn := cfARNIn("aws-cn", testAccountID, "function/cn")
	calls.tags[dist] = map[string]string{"k": "dist"}
	calls.tags[fn] = map[string]string{"k": "fn"}
	calls.monitoring["ECN"] = cfNoSubscription
	handlers := cfHandlers(calls)
	for _, k := range cfKinds() {
		delete(handlers, k.op)
	}
	handlers["ListDistributions"] = func(any) (any, error) {
		return &cloudfront.ListDistributionsOutput{DistributionList: &cftypes.DistributionList{Items: []cftypes.DistributionSummary{{ARN: &dist, Id: sdkaws.String("ECN")}}}}, nil
	}
	handlers["ListFunctions"] = func(any) (any, error) {
		return &cloudfront.ListFunctionsOutput{FunctionList: &cftypes.FunctionList{Items: []cftypes.FunctionSummary{{Name: sdkaws.String("cn"), FunctionMetadata: &cftypes.FunctionMetadata{FunctionARN: &fn}}}}}, nil
	}
	acct.cfg = opStubConfig(handlers, cfDenied)

	if _, _, err := scanCloudFront(context.Background(), acct, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	slices.Sort(calls.tagARNs)
	if want := []string{dist, fn}; !slices.Equal(calls.tagARNs, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", calls.tagARNs, want)
	}
	for _, w := range []struct{ typ, arn, tag string }{{TypeCloudFrontDistribution, dist, "dist"}, {TypeCloudFrontFunction, fn, "fn"}} {
		r, ok := cfRowOf(t, st, w.typ, w.arn)
		if !ok {
			t.Fatalf("%s %s missing", w.typ, w.arn)
		}
		assertTagsMap(t, r.TagsJSON, map[string]string{"k": w.tag})
	}
}

// ctx's end is one member of the error, though several families saw it.
func TestScanCloudFront_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	handlers := cfHandlers(newCFCalls())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, op := range []string{"ListFunctions", "ListTrustStores"} {
		list := handlers[op]
		handlers[op] = func(in any) (any, error) {
			if bacField[*string](in, "Marker") != nil {
				cancel()
				return nil, context.Canceled
			}
			return list(in)
		}
	}

	_, _, err := cfScan(ctx, st, handlers)
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
func TestScanCloudFront_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "cloudfront.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := cfHandlers(newCFCalls())
		handlers["ListTrustStores"] = func(any) (any, error) { return nil, nx }
		total, _, err := cfScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != cfTotalCount-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, cfTotalCount-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newCFCalls()
		calls.errs[cfDistARN("bare")] = nx
		_, _, err := cfScan(context.Background(), st, cfHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := countWarnings(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanCloudFront(context.Background(), acct, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if *warns != 0 {
			t.Errorf("warnings = %d; want 0", *warns)
		}
	})
}
