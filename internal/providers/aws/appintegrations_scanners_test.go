package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/appintegrations"
	aitypes "github.com/aws/aws-sdk-go-v2/service/appintegrations/types"
	"github.com/icearp/disco-cli/store"
)

const (
	appintTestPrefix = "arn:aws:app-integrations:us-east-1:123456789012:"
	apintAppA        = appintTestPrefix + "application/aaaaaaaa-0000-0000-0000-000000000001"
	apintAppB        = appintTestPrefix + "application/aaaaaaaa-0000-0000-0000-000000000002"
	apintDI1ID       = "dddddddd-0000-0000-0000-000000000001"
	apintDI2ID       = "dddddddd-0000-0000-0000-000000000002"
	apintDI1         = appintTestPrefix + "data-integration/" + apintDI1ID
	apintDI2         = appintTestPrefix + "data-integration/" + apintDI2ID
	appintTestEI     = appintTestPrefix + "event-integration/orders"
	apintEI2         = appintTestPrefix + "event-integration/billing"
	// appintTestEINoName is a second event integration of the resolver tests.
	appintTestEINoName = appintTestPrefix + "event-integration/nameless"

	apintAppAssocA1   = appintTestPrefix + "application-association/aaaaaaaa-0000-0000-0000-000000000001/a1"
	apintAppAssocA2   = appintTestPrefix + "application-association/aaaaaaaa-0000-0000-0000-000000000001/a2"
	apintAppAssocB1   = appintTestPrefix + "application-association/aaaaaaaa-0000-0000-0000-000000000002/b1"
	apintDIAssoc1     = appintTestPrefix + "data-integration-association/dddddddd-0000-0000-0000-000000000001/d1"
	apintDIAssoc2     = appintTestPrefix + "data-integration-association/dddddddd-0000-0000-0000-000000000001/d2"
	appintTestEIAssoc = appintTestPrefix + "event-integration-association/orders/eeeeeeee-0000-0000-0000-000000000001"
	apintEIAssoc2     = appintTestPrefix + "event-integration-association/orders/eeeeeeee-0000-0000-0000-000000000002"

	// apintTestTotal is every row of the fixture: two applications, data
	// integrations and event integrations, three application associations
	// (two of A, one of B), two of DI1 and two of the "orders" event
	// integration.
	apintTestTotal = 2 + 2 + 2 + 3 + 2 + 2
)

var (
	apintDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: app-integrations:List")
	apintBadInput = apiErr("InvalidRequestException", "bad")
	apintThrottle = apiErr("ThrottlingException", "slow down")
	apintGone     = apiErr("ResourceNotFoundException", "gone")
	apintCreated  = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

	// apintTagged maps the rows whose tag read returns tags to them; every
	// other tag read returns none.
	apintTagged = map[string]map[string]string{
		apintAppA:         {"kind": "application"},
		apintDI1:          {"kind": "data-integration"},
		apintAppAssocA1:   {"kind": "application-association"},
		apintDIAssoc1:     {"kind": "data-integration-association"},
		appintTestEIAssoc: {"kind": "event-integration-association"},
	}
	apintAppAssocs = []string{apintAppAssocA1, apintAppAssocA2, apintAppAssocB1}
	apintDIAssocs  = []string{apintDIAssoc1, apintDIAssoc2}
	apintEIAssocs  = []string{appintTestEIAssoc, apintEIAssoc2}
)

// apintCalls records the calls of a scan and answers the tag reads, by ARN,
// with tags or tagErrs, and the association listings, by parent identifier,
// with parentErrs.
type apintCalls struct {
	mu         sync.Mutex
	ops        map[string]int
	limits     map[string][]int32
	parents    map[string][]string
	tagARNs    []string
	tags       map[string]map[string]string
	tagErrs    map[string]error
	parentErrs map[string]error
}

func newApintCalls() *apintCalls {
	return &apintCalls{
		ops: map[string]int{}, limits: map[string][]int32{}, parents: map[string][]string{},
		tags: maps.Clone(apintTagged), tagErrs: map[string]error{}, parentErrs: map[string]error{},
	}
}

// record notes a list call of op, its MaxResults and, for an association
// listing, its parent identifier, and returns that identifier.
func (c *apintCalls) record(op string, in any) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	if n := bacField[*int32](in, "MaxResults"); n != nil {
		c.limits[op] = append(c.limits[op], *n)
	}
	parent := ""
	switch p := in.(type) {
	case *appintegrations.ListApplicationAssociationsInput:
		parent = sv(p.ApplicationId)
	case *appintegrations.ListDataIntegrationAssociationsInput:
		parent = sv(p.DataIntegrationIdentifier)
	case *appintegrations.ListEventIntegrationAssociationsInput:
		parent = sv(p.EventIntegrationName)
	}
	if parent != "" {
		c.parents[op] = append(c.parents[op], parent)
	}
	return parent
}

func (c *apintCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

// listedParents is the sorted distinct parents op was called for.
func (c *apintCalls) listedParents(op string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := slices.Clone(c.parents[op])
	slices.Sort(out)
	return slices.Compact(out)
}

func (c *apintCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := slices.Clone(c.tagARNs)
	slices.Sort(out)
	return out
}

func (c *apintCalls) listTags(in any) (any, error) {
	arn := sv(in.(*appintegrations.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops["ListTagsForResource"]++
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.tagErrs[arn]; err != nil {
		return nil, err
	}
	return &appintegrations.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
}

// apintTwoPages answers a listing with first on its first page and second
// on the next.
func apintTwoPages[Out any](in any, first, second func(next *string) Out) Out {
	if bacField[*string](in, "NextToken") == nil {
		return first(sdkaws.String("p2"))
	}
	return second(nil)
}

// apintHandlers answers every op the scan calls. Every account-wide listing
// and every non-empty association listing spans two pages.
func apintHandlers(c *apintCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListApplications": func(in any) (any, error) {
			c.record("ListApplications", in)
			return apintTwoPages(in,
				func(next *string) *appintegrations.ListApplicationsOutput {
					return &appintegrations.ListApplicationsOutput{NextToken: next, Applications: []aitypes.ApplicationSummary{
						{Arn: sdkaws.String(apintAppA), Name: sdkaws.String("a"), Namespace: sdkaws.String("ns-a"), CreatedTime: &apintCreated},
					}}
				},
				func(next *string) *appintegrations.ListApplicationsOutput {
					return &appintegrations.ListApplicationsOutput{NextToken: next, Applications: []aitypes.ApplicationSummary{
						{Arn: sdkaws.String(apintAppB), Name: sdkaws.String("b")},
					}}
				}), nil
		},
		"ListDataIntegrations": func(in any) (any, error) {
			c.record("ListDataIntegrations", in)
			return apintTwoPages(in,
				func(next *string) *appintegrations.ListDataIntegrationsOutput {
					return &appintegrations.ListDataIntegrationsOutput{NextToken: next, DataIntegrations: []aitypes.DataIntegrationSummary{
						{Arn: sdkaws.String(apintDI1), Name: sdkaws.String("crm"), SourceURI: sdkaws.String("Salesforce://AppFlow/crm")},
					}}
				},
				func(next *string) *appintegrations.ListDataIntegrationsOutput {
					return &appintegrations.ListDataIntegrationsOutput{NextToken: next, DataIntegrations: []aitypes.DataIntegrationSummary{
						{Arn: sdkaws.String(apintDI2), Name: sdkaws.String("erp")},
					}}
				}), nil
		},
		"ListEventIntegrations": func(in any) (any, error) {
			c.record("ListEventIntegrations", in)
			return apintTwoPages(in,
				func(next *string) *appintegrations.ListEventIntegrationsOutput {
					return &appintegrations.ListEventIntegrationsOutput{NextToken: next, EventIntegrations: []aitypes.EventIntegration{
						{EventIntegrationArn: sdkaws.String(appintTestEI), Name: sdkaws.String("orders"), EventBridgeBus: sdkaws.String("default"), Tags: map[string]string{"team": "ops"}},
					}}
				},
				func(next *string) *appintegrations.ListEventIntegrationsOutput {
					return &appintegrations.ListEventIntegrationsOutput{NextToken: next, EventIntegrations: []aitypes.EventIntegration{
						{EventIntegrationArn: sdkaws.String(apintEI2), Name: sdkaws.String("billing")},
					}}
				}), nil
		},
		"ListApplicationAssociations": func(in any) (any, error) {
			app := c.record("ListApplicationAssociations", in)
			if err := c.parentErrs[app]; err != nil {
				return nil, err
			}
			assoc := func(arn string) aitypes.ApplicationAssociationSummary {
				return aitypes.ApplicationAssociationSummary{ApplicationAssociationArn: sdkaws.String(arn), ApplicationArn: sdkaws.String(app), ClientId: sdkaws.String("connect")}
			}
			switch app {
			case apintAppA:
				return apintTwoPages(in,
					func(next *string) *appintegrations.ListApplicationAssociationsOutput {
						return &appintegrations.ListApplicationAssociationsOutput{NextToken: next, ApplicationAssociations: []aitypes.ApplicationAssociationSummary{assoc(apintAppAssocA1)}}
					},
					func(next *string) *appintegrations.ListApplicationAssociationsOutput {
						return &appintegrations.ListApplicationAssociationsOutput{NextToken: next, ApplicationAssociations: []aitypes.ApplicationAssociationSummary{assoc(apintAppAssocA2)}}
					}), nil
			case apintAppB:
				return &appintegrations.ListApplicationAssociationsOutput{ApplicationAssociations: []aitypes.ApplicationAssociationSummary{assoc(apintAppAssocB1)}}, nil
			}
			return nil, apintGone
		},
		"ListDataIntegrationAssociations": func(in any) (any, error) {
			id := c.record("ListDataIntegrationAssociations", in)
			if err := c.parentErrs[id]; err != nil {
				return nil, err
			}
			assoc := func(arn string) aitypes.DataIntegrationAssociationSummary {
				return aitypes.DataIntegrationAssociationSummary{DataIntegrationAssociationArn: sdkaws.String(arn), DataIntegrationArn: sdkaws.String(apintDI1), ClientId: sdkaws.String("wisdom"), DestinationURI: sdkaws.String("arn://wisdom")}
			}
			switch id {
			case apintDI1ID:
				return apintTwoPages(in,
					func(next *string) *appintegrations.ListDataIntegrationAssociationsOutput {
						return &appintegrations.ListDataIntegrationAssociationsOutput{NextToken: next, DataIntegrationAssociations: []aitypes.DataIntegrationAssociationSummary{assoc(apintDIAssoc1)}}
					},
					func(next *string) *appintegrations.ListDataIntegrationAssociationsOutput {
						return &appintegrations.ListDataIntegrationAssociationsOutput{NextToken: next, DataIntegrationAssociations: []aitypes.DataIntegrationAssociationSummary{assoc(apintDIAssoc2)}}
					}), nil
			case apintDI2ID:
				return &appintegrations.ListDataIntegrationAssociationsOutput{}, nil
			}
			return nil, apintGone
		},
		"ListEventIntegrationAssociations": func(in any) (any, error) {
			name := c.record("ListEventIntegrationAssociations", in)
			if err := c.parentErrs[name]; err != nil {
				return nil, err
			}
			assoc := func(arn string) aitypes.EventIntegrationAssociation {
				return aitypes.EventIntegrationAssociation{EventIntegrationAssociationArn: sdkaws.String(arn), EventIntegrationName: sdkaws.String(name), EventBridgeRuleName: sdkaws.String("orders-rule"), ClientId: sdkaws.String("connect")}
			}
			switch name {
			case "orders":
				return apintTwoPages(in,
					func(next *string) *appintegrations.ListEventIntegrationAssociationsOutput {
						return &appintegrations.ListEventIntegrationAssociationsOutput{NextToken: next, EventIntegrationAssociations: []aitypes.EventIntegrationAssociation{assoc(appintTestEIAssoc)}}
					},
					func(next *string) *appintegrations.ListEventIntegrationAssociationsOutput {
						return &appintegrations.ListEventIntegrationAssociationsOutput{NextToken: next, EventIntegrationAssociations: []aitypes.EventIntegrationAssociation{assoc(apintEIAssoc2)}}
					}), nil
			case "billing":
				return &appintegrations.ListEventIntegrationAssociationsOutput{}, nil
			}
			return nil, apintGone
		},
	}
}

// apintScan scans the test account's region with handlers; an op without a
// handler is denied.
func apintScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, apintDenied)
	return scanAppIntegrations(ctx, acct, testRegion, st, testScanID)
}

// apintAssocIDs returns the NativeIDs of every stored association, sorted.
func apintAssocIDs(t *testing.T, st *store.Store) []string {
	t.Helper()
	var ids []string
	for _, typ := range []string{
		TypeAppIntegrationsApplicationAssociation,
		TypeAppIntegrationsDataIntegrationAssociation,
		TypeAppIntegrationsEventIntegrationAssociation,
	} {
		ids = append(ids, nativeIDs(rowsByNativeID(t, st, typ))...)
	}
	slices.Sort(ids)
	return ids
}

func apintSorted(lists ...[]string) []string {
	out := slices.Concat(lists...)
	slices.Sort(out)
	return out
}

// apintWarnings is the op each warning was sent under, in order.
func apintWarnings(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// Every listing stores both its pages, for every parent, each row under its
// ARN, with the tags its tag read returns (nil for none); the event
// integrations keep the tags their element carries and have no tag read.
// ListTagsForResource is sent exactly the ARNs of every other row, and every
// listing the model's MaxResults cap of 50. Each association is recorded
// under its parent. No error, no warning.
func TestScanAppIntegrations_StoresEveryTypeWithTags(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newApintCalls()

	total, inserted, err := apintScan(context.Background(), st, apintHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != apintTestTotal || inserted != apintTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, apintTestTotal, apintTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}

	created := apintCreated.Format(time.RFC3339)
	for _, c := range []struct {
		typ, arn, name, created string
		tags                    map[string]string
	}{
		{TypeAppIntegrationsApplication, apintAppA, "a", created, apintTagged[apintAppA]},
		{TypeAppIntegrationsApplication, apintAppB, "b", "", nil},
		{TypeAppIntegrationsDataIntegration, apintDI1, "crm", "", apintTagged[apintDI1]},
		{TypeAppIntegrationsDataIntegration, apintDI2, "erp", "", nil},
		{TypeAppIntegrationsEventIntegration, appintTestEI, "orders", "", map[string]string{"team": "ops"}},
		{TypeAppIntegrationsEventIntegration, apintEI2, "billing", "", nil},
	} {
		r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
		if !ok {
			t.Errorf("%s %s missing", c.typ, c.arn)
			continue
		}
		if r.Name == nil || *r.Name != c.name || sv(r.Region) != testRegion || r.Status != nil || sv(r.CreatedAt) != c.created || r.ManagedByProvider {
			t.Errorf("%s Name, Region, Status, CreatedAt, Managed = %v, %q, %v, %q, %v; want %q, %q, nil, %q, false",
				c.arn, r.Name, sv(r.Region), r.Status, sv(r.CreatedAt), r.ManagedByProvider, c.name, testRegion, c.created)
		}
		if c.tags == nil {
			if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", c.arn, *r.TagsJSON)
			}
		} else {
			assertTagsMap(t, r.TagsJSON, c.tags)
		}
	}
	var appAttrs struct{ Arn, Namespace string }
	if err := json.Unmarshal([]byte(rowsByNativeID(t, st, TypeAppIntegrationsApplication)[apintAppA].AttributesJSON), &appAttrs); err != nil || appAttrs.Arn != apintAppA || appAttrs.Namespace != "ns-a" {
		t.Errorf("application attrs = %+v (%v); want the listed element", appAttrs, err)
	}
	var diAttrs struct{ SourceURI string }
	if err := json.Unmarshal([]byte(rowsByNativeID(t, st, TypeAppIntegrationsDataIntegration)[apintDI1].AttributesJSON), &diAttrs); err != nil || diAttrs.SourceURI != "Salesforce://AppFlow/crm" {
		t.Errorf("data integration attrs = %+v (%v); want the listed element", diAttrs, err)
	}
	var eiAttrs struct{ EventBridgeBus string }
	if err := json.Unmarshal([]byte(rowsByNativeID(t, st, TypeAppIntegrationsEventIntegration)[appintTestEI].AttributesJSON), &eiAttrs); err != nil || eiAttrs.EventBridgeBus != "default" {
		t.Errorf("event integration attrs = %+v (%v); want the listed element", eiAttrs, err)
	}

	for _, c := range []struct {
		typ  string
		arns []string
	}{
		{TypeAppIntegrationsApplicationAssociation, apintAppAssocs},
		{TypeAppIntegrationsDataIntegrationAssociation, apintDIAssocs},
		{TypeAppIntegrationsEventIntegrationAssociation, apintEIAssocs},
	} {
		rows := rowsByNativeID(t, st, c.typ)
		if got := nativeIDs(rows); !slices.Equal(got, c.arns) {
			t.Errorf("%s rows = %v; want %v", c.typ, got, c.arns)
		}
		for _, arn := range c.arns {
			r := rows[arn]
			if r.Name != nil || sv(r.Region) != testRegion || r.Status != nil || r.CreatedAt != nil {
				t.Errorf("%s Name, Region, Status, CreatedAt = %v, %q, %v, %v; want nil, %q, nil, nil", arn, r.Name, sv(r.Region), r.Status, r.CreatedAt, testRegion)
			}
			var attrs struct {
				ClientID string `json:"ClientId"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.ClientID == "" {
				t.Errorf("%s attrs = %s (%v); want the listed element", arn, r.AttributesJSON, err)
			}
			if tags, ok := apintTagged[arn]; ok {
				assertTagsMap(t, r.TagsJSON, tags)
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
	}
	var eiAssoc struct{ EventBridgeRuleName string }
	if err := json.Unmarshal([]byte(rowsByNativeID(t, st, TypeAppIntegrationsEventIntegrationAssociation)[appintTestEIAssoc].AttributesJSON), &eiAssoc); err != nil || eiAssoc.EventBridgeRuleName != "orders-rule" {
		t.Errorf("event integration association attrs = %+v (%v); want the listed element", eiAssoc, err)
	}

	assertContainsNative(t, st, apintAppA, apintAppAssocA1)
	assertContainsNative(t, st, apintAppA, apintAppAssocA2)
	assertContainsNative(t, st, apintAppB, apintAppAssocB1)
	assertContainsNative(t, st, apintDI1, apintDIAssoc1)
	assertContainsNative(t, st, apintDI1, apintDIAssoc2)
	assertContainsNative(t, st, appintTestEI, appintTestEIAssoc)
	assertContainsNative(t, st, appintTestEI, apintEIAssoc2)

	wantTagARNs := apintSorted([]string{apintAppA, apintAppB, apintDI1, apintDI2}, apintAppAssocs, apintDIAssocs, apintEIAssocs)
	if got := calls.sortedTagARNs(); !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, wantTagARNs)
	}
	for op, want := range map[string][]string{
		"ListApplicationAssociations":      {apintAppA, apintAppB},
		"ListDataIntegrationAssociations":  {apintDI1ID, apintDI2ID},
		"ListEventIntegrationAssociations": {"billing", "orders"},
	} {
		if got := calls.listedParents(op); !slices.Equal(got, want) {
			t.Errorf("%s parents = %v; want %v", op, got, want)
		}
	}
	for _, op := range []string{"ListApplications", "ListDataIntegrations", "ListEventIntegrations", "ListApplicationAssociations", "ListDataIntegrationAssociations", "ListEventIntegrationAssociations"} {
		if len(calls.limits[op]) == 0 || len(calls.limits[op]) != calls.count(op) {
			t.Errorf("%s sent MaxResults on %d of %d calls", op, len(calls.limits[op]), calls.count(op))
		}
		for _, n := range calls.limits[op] {
			if n != 50 {
				t.Errorf("%s MaxResults = %d; want 50", op, n)
			}
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanAppIntegrations_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := apintScan(context.Background(), st, apintHandlers(newApintCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := apintScan(context.Background(), st, apintHandlers(newApintCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != apintTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, apintTestTotal)
	}
}

// Empty listings store nothing, list no associations and read no tags.
func TestScanAppIntegrations_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newApintCalls()
	handlers := apintHandlers(calls)
	handlers["ListApplications"] = func(any) (any, error) { return &appintegrations.ListApplicationsOutput{}, nil }
	handlers["ListDataIntegrations"] = func(any) (any, error) { return &appintegrations.ListDataIntegrationsOutput{}, nil }
	handlers["ListEventIntegrations"] = func(any) (any, error) { return &appintegrations.ListEventIntegrationsOutput{}, nil }

	total, _, err := apintScan(context.Background(), st, handlers)
	if err != nil || total != 0 {
		t.Fatalf("total, err = %d, %v; want 0, nil", total, err)
	}
	for _, op := range []string{"ListApplicationAssociations", "ListDataIntegrationAssociations", "ListEventIntegrationAssociations", "ListTagsForResource"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A failed tag read keeps the row untagged and decides the outcome by its
// cause: a resource deleted since it was listed is dropped silently (an
// application with its associations unlisted), a denial warns once under
// the tag op, a throttle is returned as transient and any other error as an
// error.
func TestScanAppIntegrations_TagReadFailures(t *testing.T) {
	for _, c := range []struct {
		name      string
		arn, typ  string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}{
		{"application/not found", apintAppA, TypeAppIntegrationsApplication, apintGone, -1, "", apintTestTotal - 3, false, false},
		{"application/denied", apintAppA, TypeAppIntegrationsApplication, apintDenied, -1, "", apintTestTotal, true, true},
		{"application/transient", apintAppA, TypeAppIntegrationsApplication, apintThrottle, outcomeTransient, "ThrottlingException", apintTestTotal, true, false},
		{"application/other error", apintAppA, TypeAppIntegrationsApplication, apintBadInput, outcomeError, "InvalidRequestException", apintTestTotal, true, false},
		{"data integration/not found", apintDI1, TypeAppIntegrationsDataIntegration, apintGone, -1, "", apintTestTotal - 3, false, false},
		{"application association/not found", apintAppAssocA1, TypeAppIntegrationsApplicationAssociation, apintGone, -1, "", apintTestTotal - 1, false, false},
		{"application association/denied", apintAppAssocA1, TypeAppIntegrationsApplicationAssociation, apintDenied, -1, "", apintTestTotal, true, true},
		{"data integration association/transient", apintDIAssoc1, TypeAppIntegrationsDataIntegrationAssociation, apintThrottle, outcomeTransient, "ThrottlingException", apintTestTotal, true, false},
		{"event integration association/other error", appintTestEIAssoc, TypeAppIntegrationsEventIntegrationAssociation, apintBadInput, outcomeError, "InvalidRequestException", apintTestTotal, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newApintCalls()
			calls.tagErrs[c.arn] = c.err

			total, _, err := apintScan(context.Background(), st, apintHandlers(calls))
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
			var want []string
			if c.warn {
				want = []string{appIntegrationsTagsOp}
			}
			if got := apintWarnings(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// Denied tag reads in several phases send one warning for the whole scan;
// the rows are kept.
func TestScanAppIntegrations_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newApintCalls()
	for _, arn := range []string{apintAppB, apintDI2, apintAppAssocB1, apintEIAssoc2} {
		calls.tagErrs[arn] = apintDenied
	}

	total, _, err := apintScan(context.Background(), st, apintHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != apintTestTotal {
		t.Errorf("total = %d; want %d", total, apintTestTotal)
	}
	if got := apintWarnings(warns()); !slices.Equal(got, []string{appIntegrationsTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, appIntegrationsTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanAppIntegrations_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, appIntegrationsTagsOp)
	calls := newApintCalls()
	calls.tagErrs[apintDI2] = apintThrottle
	calls.tagErrs[apintEIAssoc2] = apintBadInput

	_, _, err := apintScan(context.Background(), st, apintHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", appIntegrationsTagsOp, *warns)
	}
}

// A transient parent listing beside a later phase's hard error is demoted to
// one warning under its own op, so the hard error decides the outcome.
func TestScanAppIntegrations_ParentTransientDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, appIntegrationsListEventIntegrationsOp)
	calls := newApintCalls()
	handlers := apintHandlers(calls)
	handlers["ListEventIntegrations"] = func(any) (any, error) { return nil, apintThrottle }
	calls.parentErrs[apintAppB] = apintBadInput

	_, _, err := apintScan(context.Background(), st, handlers)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", appIntegrationsListEventIntegrationsOp, *warns)
	}
	if got := apintAssocIDs(t, st); !slices.Equal(got, apintSorted([]string{apintAppAssocA1, apintAppAssocA2}, apintDIAssocs)) {
		t.Errorf("association rows = %v; want A's and DI1's", got)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored. A failed closure write follows the write of the rows it
// links, so the denial of a row it stored is still warned about.
func TestScanAppIntegrations_FailedWriteStopsScan(t *testing.T) {
	for _, c := range []struct {
		name      string
		refuse    func(t *testing.T, st *store.Store)
		denied    string
		laterOps  []string
		wantTotal int
		wantWarns []string
	}{
		{
			"data integrations", func(t *testing.T, st *store.Store) { refuseStoreWrites(t, st, TypeAppIntegrationsDataIntegration) }, apintDI1,
			[]string{"ListEventIntegrations", "ListApplicationAssociations", "ListDataIntegrationAssociations", "ListEventIntegrationAssociations"},
			2, nil,
		},
		{
			"event integrations", func(t *testing.T, st *store.Store) { refuseStoreWrites(t, st, TypeAppIntegrationsEventIntegration) }, apintDIAssoc1,
			[]string{"ListApplicationAssociations", "ListDataIntegrationAssociations", "ListEventIntegrationAssociations"},
			4, nil,
		},
		{
			"application associations", func(t *testing.T, st *store.Store) {
				refuseStoreWrites(t, st, TypeAppIntegrationsApplicationAssociation)
			}, apintAppAssocA1,
			[]string{"ListDataIntegrationAssociations", "ListEventIntegrationAssociations"},
			6, nil,
		},
		{
			"association closure", refuseClosureWrites, apintAppAssocA1,
			[]string{"ListDataIntegrationAssociations", "ListEventIntegrationAssociations"},
			6 + 3,
			[]string{appIntegrationsTagsOp},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			c.refuse(t, st)
			calls := newApintCalls()
			calls.tagErrs[c.denied] = apintDenied

			total, _, err := apintScan(context.Background(), st, apintHandlers(calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if got := apintWarnings(warns()); !slices.Equal(got, c.wantWarns) {
				t.Errorf("warnings = %v; want %v", got, c.wantWarns)
			}
			for _, op := range c.laterOps {
				if n := calls.count(op); n != 0 {
					t.Errorf("%s called %d times; want 0 after the failed write", op, n)
				}
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
		})
	}
}

// One parent's failed association listing costs the other parents and the
// other phases none of their rows: a parent deleted since it was listed is
// skipped silently, a denial warns once under the op however many parents
// hit it, a throttle is returned as transient and any other error as an
// error.
func TestScanAppIntegrations_AssociationParentFailures(t *testing.T) {
	onlyB := []string{apintAppAssocB1}
	for _, c := range []struct {
		name    string
		op      string
		fail    []string // parent identifiers
		err     error
		outcome serviceOutcome // -1: no error
		code    string
		want    []string
		warn    bool
	}{
		{"application gone", appIntegrationsListApplicationAssociationsOp, []string{apintAppA}, apintGone, -1, "", apintSorted(onlyB, apintDIAssocs, apintEIAssocs), false},
		{"applications denied", appIntegrationsListApplicationAssociationsOp, []string{apintAppA, apintAppB}, apintDenied, -1, "", apintSorted(apintDIAssocs, apintEIAssocs), true},
		{"application throttled", appIntegrationsListApplicationAssociationsOp, []string{apintAppA}, apintThrottle, outcomeTransient, "ThrottlingException", apintSorted(onlyB, apintDIAssocs, apintEIAssocs), false},
		{"application other error", appIntegrationsListApplicationAssociationsOp, []string{apintAppA}, apintBadInput, outcomeError, "InvalidRequestException", apintSorted(onlyB, apintDIAssocs, apintEIAssocs), false},
		{"data integration gone", appIntegrationsListDataIntegrationAssociationsOp, []string{apintDI1ID}, apintGone, -1, "", apintSorted(apintAppAssocs, apintEIAssocs), false},
		{"data integration denied", appIntegrationsListDataIntegrationAssociationsOp, []string{apintDI1ID}, apintDenied, -1, "", apintSorted(apintAppAssocs, apintEIAssocs), true},
		{"data integration other error", appIntegrationsListDataIntegrationAssociationsOp, []string{apintDI1ID}, apintBadInput, outcomeError, "InvalidRequestException", apintSorted(apintAppAssocs, apintEIAssocs), false},
		{"event integration gone", appIntegrationsListEventIntegrationAssociationsOp, []string{"orders"}, apintGone, -1, "", apintSorted(apintAppAssocs, apintDIAssocs), false},
		{"event integration denied", appIntegrationsListEventIntegrationAssociationsOp, []string{"orders"}, apintDenied, -1, "", apintSorted(apintAppAssocs, apintDIAssocs), true},
		{"event integration throttled", appIntegrationsListEventIntegrationAssociationsOp, []string{"orders"}, apintThrottle, outcomeTransient, "ThrottlingException", apintSorted(apintAppAssocs, apintDIAssocs), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newApintCalls()
			for _, p := range c.fail {
				calls.parentErrs[p] = c.err
			}

			total, _, err := apintScan(context.Background(), st, apintHandlers(calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if got := apintAssocIDs(t, st); !slices.Equal(got, c.want) {
				t.Errorf("association rows = %v; want %v", got, c.want)
			}
			if want := 6 + len(c.want); total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			var wantWarns []string
			if c.warn {
				wantWarns = []string{c.op}
			}
			if got := apintWarnings(warns()); !slices.Equal(got, wantWarns) {
				t.Errorf("warnings = %v; want %v", got, wantWarns)
			}
		})
	}
}

// A listing's failed later page keeps the pages read before it, and its
// error does not stop the later phases. A parent not stored has no
// associations listed.
func TestScanAppIntegrations_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, c := range []struct {
		op         string
		kept, lost string
		typ        string
		wantAssocs []string
	}{
		{"ListApplications", apintAppA, apintAppB, TypeAppIntegrationsApplication, apintSorted([]string{apintAppAssocA1, apintAppAssocA2}, apintDIAssocs, apintEIAssocs)},
		{"ListDataIntegrations", apintDI1, apintDI2, TypeAppIntegrationsDataIntegration, apintSorted(apintAppAssocs, apintDIAssocs, apintEIAssocs)},
		{"ListEventIntegrations", appintTestEI, apintEI2, TypeAppIntegrationsEventIntegration, apintSorted(apintAppAssocs, apintDIAssocs, apintEIAssocs)},
		{"ListApplicationAssociations", apintAppAssocA1, apintAppAssocA2, TypeAppIntegrationsApplicationAssociation, apintSorted([]string{apintAppAssocA1, apintAppAssocB1}, apintDIAssocs, apintEIAssocs)},
		{"ListDataIntegrationAssociations", apintDIAssoc1, apintDIAssoc2, TypeAppIntegrationsDataIntegrationAssociation, apintSorted(apintAppAssocs, []string{apintDIAssoc1}, apintEIAssocs)},
		{"ListEventIntegrationAssociations", appintTestEIAssoc, apintEIAssoc2, TypeAppIntegrationsEventIntegrationAssociation, apintSorted(apintAppAssocs, apintDIAssocs, []string{appintTestEIAssoc})},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newApintCalls()
			handlers := apintHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					calls.record(c.op, in)
					return nil, apintBadInput
				}
				return list(in)
			}

			_, _, err := apintScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidRequestException")
			rows := rowsByNativeID(t, st, c.typ)
			if _, ok := rows[c.kept]; !ok {
				t.Errorf("%s, read before the failure, missing", c.kept)
			}
			if _, ok := rows[c.lost]; ok {
				t.Errorf("%s stored; its page failed", c.lost)
			}
			if got := apintAssocIDs(t, st); !slices.Equal(got, c.wantAssocs) {
				t.Errorf("association rows = %v; want %v", got, c.wantAssocs)
			}
		})
	}
}

// A repeated NextToken ends every listing as an error, keeping the page
// read, rather than looping on it.
func TestScanAppIntegrations_RepeatedTokenKeepsPages(t *testing.T) {
	for _, c := range []struct {
		op, kept, typ string
		// repeat is the output of every call after the first, built afresh
		// for each: the SDK writes ResultMetadata into the output returned,
		// so concurrent parent reads sharing one would race.
		repeat func() any
	}{
		{"ListApplications", apintAppA, TypeAppIntegrationsApplication, func() any { return &appintegrations.ListApplicationsOutput{NextToken: sdkaws.String("same")} }},
		{"ListDataIntegrations", apintDI1, TypeAppIntegrationsDataIntegration, func() any { return &appintegrations.ListDataIntegrationsOutput{NextToken: sdkaws.String("same")} }},
		{"ListEventIntegrations", appintTestEI, TypeAppIntegrationsEventIntegration, func() any { return &appintegrations.ListEventIntegrationsOutput{NextToken: sdkaws.String("same")} }},
		{"ListApplicationAssociations", apintAppAssocA1, TypeAppIntegrationsApplicationAssociation, func() any {
			return &appintegrations.ListApplicationAssociationsOutput{NextToken: sdkaws.String("same")}
		}},
		{"ListDataIntegrationAssociations", apintDIAssoc1, TypeAppIntegrationsDataIntegrationAssociation, func() any {
			return &appintegrations.ListDataIntegrationAssociationsOutput{NextToken: sdkaws.String("same")}
		}},
		{"ListEventIntegrationAssociations", appintTestEIAssoc, TypeAppIntegrationsEventIntegrationAssociation, func() any {
			return &appintegrations.ListEventIntegrationAssociationsOutput{NextToken: sdkaws.String("same")}
		}},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newApintCalls()
			handlers := apintHandlers(calls)
			list := handlers[c.op]
			var loops atomic.Int32
			handlers[c.op] = func(in any) (any, error) {
				if loops.Add(1) > 20 {
					return nil, apiErr("LoopGuard", "the repeated token was not detected")
				}
				if bacField[*string](in, "NextToken") != nil {
					calls.record(c.op, in)
					return c.repeat(), nil
				}
				out, err := list(in)
				if err == nil {
					reflect.ValueOf(out).Elem().FieldByName("NextToken").Set(reflect.ValueOf(sdkaws.String("same")))
				}
				return out, err
			}

			_, _, err := apintScan(context.Background(), st, handlers)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if _, ok := rowsByNativeID(t, st, c.typ)[c.kept]; !ok {
				t.Errorf("%s, on the page read, missing", c.kept)
			}
		})
	}
}

// Elements the scan cannot store or cannot list associations of are warned
// about once per reason under their listing's op and never sent to the tag
// op: an element without an ARN is not stored; a data integration whose ARN
// has no data-integration/<id> segment and an event integration without a
// name are stored, but their associations are not listed.
func TestScanAppIntegrations_ElementsWithoutIdentifier(t *testing.T) {
	const noIDSegment = appintTestPrefix + "data-integration-legacy"
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newApintCalls()
	handlers := apintHandlers(calls)
	handlers["ListApplications"] = func(in any) (any, error) {
		calls.record("ListApplications", in)
		return &appintegrations.ListApplicationsOutput{Applications: []aitypes.ApplicationSummary{
			{Name: sdkaws.String("arnless")},
			{Arn: sdkaws.String(apintAppA), Name: sdkaws.String("a")},
		}}, nil
	}
	handlers["ListDataIntegrations"] = func(in any) (any, error) {
		calls.record("ListDataIntegrations", in)
		return &appintegrations.ListDataIntegrationsOutput{DataIntegrations: []aitypes.DataIntegrationSummary{
			{Arn: sdkaws.String(noIDSegment), Name: sdkaws.String("legacy")},
			{Arn: sdkaws.String(appintTestPrefix + "data-integration/"), Name: sdkaws.String("empty id")},
			{Name: sdkaws.String("arnless")},
			{Arn: sdkaws.String(apintDI1), Name: sdkaws.String("crm")},
		}}, nil
	}
	handlers["ListEventIntegrations"] = func(in any) (any, error) {
		calls.record("ListEventIntegrations", in)
		return &appintegrations.ListEventIntegrationsOutput{EventIntegrations: []aitypes.EventIntegration{
			{EventIntegrationArn: sdkaws.String(apintEI2)},
			{Name: sdkaws.String("arnless")},
			{EventIntegrationArn: sdkaws.String(appintTestEI), Name: sdkaws.String("orders")},
		}}, nil
	}
	list := handlers["ListApplicationAssociations"]
	handlers["ListApplicationAssociations"] = func(in any) (any, error) {
		out, err := list(in)
		if o, ok := out.(*appintegrations.ListApplicationAssociationsOutput); ok && in.(*appintegrations.ListApplicationAssociationsInput).NextToken == nil {
			o.ApplicationAssociations = append(o.ApplicationAssociations, aitypes.ApplicationAssociationSummary{ClientId: sdkaws.String("arnless-client")})
		}
		return out, err
	}

	total, _, err := apintScan(context.Background(), st, handlers)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// One application, three data integrations, two event integrations, A's
	// two associations, DI1's two and orders' two.
	if want := 1 + 3 + 2 + 2 + 2 + 2; total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeAppIntegrationsDataIntegration)); !slices.Equal(got, apintSorted([]string{noIDSegment, appintTestPrefix + "data-integration/", apintDI1})) {
		t.Errorf("data integrations = %v; want the three with an ARN", got)
	}
	if got := calls.listedParents("ListDataIntegrationAssociations"); !slices.Equal(got, []string{apintDI1ID}) {
		t.Errorf("ListDataIntegrationAssociations parents = %v; want only %s", got, apintDI1ID)
	}
	if got := calls.listedParents("ListEventIntegrationAssociations"); !slices.Equal(got, []string{"orders"}) {
		t.Errorf("ListEventIntegrationAssociations parents = %v; want only orders", got)
	}
	if r := rowsByNativeID(t, st, TypeAppIntegrationsEventIntegration)[apintEI2]; r.Name == nil || *r.Name != "" {
		t.Errorf("nameless event integration Name = %v; want \"\"", r.Name)
	}
	if slices.Contains(calls.sortedTagARNs(), "") {
		t.Error("an empty ARN was sent to ListTagsForResource")
	}
	got := apintWarnings(warns())
	slices.Sort(got)
	want := []string{
		appIntegrationsListApplicationAssociationsOp, appIntegrationsListApplicationsOp,
		appIntegrationsListDataIntegrationsOp, appIntegrationsListDataIntegrationsOp,
		appIntegrationsListEventIntegrationsOp, appIntegrationsListEventIntegrationsOp,
	}
	if !slices.Equal(got, want) {
		t.Errorf("warnings = %v; want %v", got, want)
	}
}

// A GovCloud or China ARN reaches the tag op unchanged, in a scan of that
// partition's account and region.
func TestScanAppIntegrations_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			arn := partitionARN(pf.partition, "app-integrations", pf.region, "application/aaaaaaaa-0000-0000-0000-00000000000f")
			st := newTestStore(t)
			calls := newApintCalls()
			calls.tags[arn] = map[string]string{"env": "prod"}
			handlers := apintHandlers(calls)
			handlers["ListApplications"] = func(in any) (any, error) {
				calls.record("ListApplications", in)
				return &appintegrations.ListApplicationsOutput{Applications: []aitypes.ApplicationSummary{{Arn: sdkaws.String(arn), Name: sdkaws.String("p")}}}, nil
			}
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(handlers, apintDenied)

			if _, _, err := scanAppIntegrations(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if !slices.Contains(calls.sortedTagARNs(), arn) {
				t.Fatalf("ListTagsForResource ARNs = %v; want %s among them", calls.sortedTagARNs(), arn)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeAppIntegrationsApplication)[arn].TagsJSON, map[string]string{"env": "prod"})
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing and its tag reads, and the later phases are
// not run.
func TestScanAppIntegrations_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newApintCalls()
	handlers := apintHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListDataIntegrations"]
	handlers["ListDataIntegrations"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := apintScan(ctx, st, handlers)
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
	for _, op := range []string{"ListEventIntegrations", "ListApplicationAssociations"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanAppIntegrations_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "app-integrations.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a later listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newApintCalls()
		calls.parentErrs["orders"] = nx
		total, _, err := apintScan(context.Background(), st, apintHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
		if want := apintTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("a tag read", func(t *testing.T) {
		st := newTestStore(t)
		calls := newApintCalls()
		calls.tagErrs[apintAppA] = nx
		total, _, err := apintScan(context.Background(), st, apintHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
		if total != apintTestTotal {
			t.Errorf("total = %d; want %d", total, apintTestTotal)
		}
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanAppIntegrations(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

func TestDataIntegrationIDFromARN(t *testing.T) {
	for _, c := range []struct{ arn, want string }{
		{apintDI1, apintDI1ID},
		{appintTestPrefix + "data-integration/", ""},
		{appintTestPrefix + "data-integration-legacy", ""},
		{"", ""},
	} {
		if got := dataIntegrationIDFromARN(c.arn); got != c.want {
			t.Errorf("dataIntegrationIDFromARN(%q) = %q; want %q", c.arn, got, c.want)
		}
	}
}
