package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/redshiftserverless"
	rsstypes "github.com/aws/aws-sdk-go-v2/service/redshiftserverless/types"
	"github.com/icearp/disco-cli/store"
)

// stubRSS serves every redshift-serverless list op from page sets; errs[op]
// is returned once that op's pages are exhausted (immediately when it has
// none). calls counts the calls to each of the new ops.
type stubRSS struct {
	mu           sync.Mutex
	calls        map[string]int
	errs         map[string]error
	namespaces   [][]rsstypes.Namespace
	workgroups   [][]rsstypes.Workgroup
	reservations [][]rsstypes.Reservation
	copyConfigs  [][]rsstypes.SnapshotCopyConfiguration
	usageLimits  [][]rsstypes.UsageLimit
	domains      [][]rsstypes.Association
	actions      [][]rsstypes.ScheduledActionAssociation
}

func (s *stubRSS) record(op string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	s.calls[op]++
}

func (s *stubRSS) ListNamespaces(_ context.Context, in *redshiftserverless.ListNamespacesInput, _ ...func(*redshiftserverless.Options)) (*redshiftserverless.ListNamespacesOutput, error) {
	items, next, err := stubPage(s.namespaces, in.NextToken, s.errs["ListNamespaces"])
	if err != nil {
		return nil, err
	}
	return &redshiftserverless.ListNamespacesOutput{Namespaces: items, NextToken: next}, nil
}

func (s *stubRSS) ListWorkgroups(_ context.Context, in *redshiftserverless.ListWorkgroupsInput, _ ...func(*redshiftserverless.Options)) (*redshiftserverless.ListWorkgroupsOutput, error) {
	items, next, err := stubPage(s.workgroups, in.NextToken, s.errs["ListWorkgroups"])
	if err != nil {
		return nil, err
	}
	return &redshiftserverless.ListWorkgroupsOutput{Workgroups: items, NextToken: next}, nil
}

func (s *stubRSS) ListSnapshots(context.Context, *redshiftserverless.ListSnapshotsInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListSnapshotsOutput, error) {
	return &redshiftserverless.ListSnapshotsOutput{}, nil
}

func (s *stubRSS) ListEndpointAccess(context.Context, *redshiftserverless.ListEndpointAccessInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListEndpointAccessOutput, error) {
	return &redshiftserverless.ListEndpointAccessOutput{}, nil
}

func (s *stubRSS) ListRecoveryPoints(context.Context, *redshiftserverless.ListRecoveryPointsInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListRecoveryPointsOutput, error) {
	return &redshiftserverless.ListRecoveryPointsOutput{}, nil
}

func (s *stubRSS) ListManagedWorkgroups(context.Context, *redshiftserverless.ListManagedWorkgroupsInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListManagedWorkgroupsOutput, error) {
	return &redshiftserverless.ListManagedWorkgroupsOutput{}, nil
}

func (s *stubRSS) ListReservations(_ context.Context, in *redshiftserverless.ListReservationsInput, _ ...func(*redshiftserverless.Options)) (*redshiftserverless.ListReservationsOutput, error) {
	s.record("ListReservations")
	items, next, err := stubPage(s.reservations, in.NextToken, s.errs["ListReservations"])
	if err != nil {
		return nil, err
	}
	return &redshiftserverless.ListReservationsOutput{ReservationsList: items, NextToken: next}, nil
}

func (s *stubRSS) ListSnapshotCopyConfigurations(_ context.Context, in *redshiftserverless.ListSnapshotCopyConfigurationsInput, _ ...func(*redshiftserverless.Options)) (*redshiftserverless.ListSnapshotCopyConfigurationsOutput, error) {
	s.record("ListSnapshotCopyConfigurations")
	items, next, err := stubPage(s.copyConfigs, in.NextToken, s.errs["ListSnapshotCopyConfigurations"])
	if err != nil {
		return nil, err
	}
	return &redshiftserverless.ListSnapshotCopyConfigurationsOutput{SnapshotCopyConfigurations: items, NextToken: next}, nil
}

func (s *stubRSS) ListUsageLimits(_ context.Context, in *redshiftserverless.ListUsageLimitsInput, _ ...func(*redshiftserverless.Options)) (*redshiftserverless.ListUsageLimitsOutput, error) {
	s.record("ListUsageLimits")
	items, next, err := stubPage(s.usageLimits, in.NextToken, s.errs["ListUsageLimits"])
	if err != nil {
		return nil, err
	}
	return &redshiftserverless.ListUsageLimitsOutput{UsageLimits: items, NextToken: next}, nil
}

func (s *stubRSS) ListCustomDomainAssociations(_ context.Context, in *redshiftserverless.ListCustomDomainAssociationsInput, _ ...func(*redshiftserverless.Options)) (*redshiftserverless.ListCustomDomainAssociationsOutput, error) {
	s.record("ListCustomDomainAssociations")
	items, next, err := stubPage(s.domains, in.NextToken, s.errs["ListCustomDomainAssociations"])
	if err != nil {
		return nil, err
	}
	return &redshiftserverless.ListCustomDomainAssociationsOutput{Associations: items, NextToken: next}, nil
}

func (s *stubRSS) ListScheduledActions(_ context.Context, in *redshiftserverless.ListScheduledActionsInput, _ ...func(*redshiftserverless.Options)) (*redshiftserverless.ListScheduledActionsOutput, error) {
	s.record("ListScheduledActions")
	if in.NamespaceName != nil {
		return nil, apiErr("ValidationException", "stub serves only the unfiltered list")
	}
	items, next, err := stubPage(s.actions, in.NextToken, s.errs["ListScheduledActions"])
	if err != nil {
		return nil, err
	}
	return &redshiftserverless.ListScheduledActionsOutput{ScheduledActions: items, NextToken: next}, nil
}

func (s *stubRSS) ListTagsForResource(context.Context, *redshiftserverless.ListTagsForResourceInput, ...func(*redshiftserverless.Options)) (*redshiftserverless.ListTagsForResourceOutput, error) {
	s.record("ListTagsForResource")
	return &redshiftserverless.ListTagsForResourceOutput{}, nil
}

func rssARN(kind, id string) string {
	return "arn:aws:redshift-serverless:" + testRegion + ":" + testAccountID + ":" + kind + "/" + id
}

func rssReservation(id string) rsstypes.Reservation {
	return rsstypes.Reservation{ReservationArn: sdkaws.String(rssARN("reservation", id)), ReservationId: sdkaws.String(id), Status: sdkaws.String("ACTIVE"), Capacity: 8}
}

func rssCopyConfig(id string) rsstypes.SnapshotCopyConfiguration {
	return rsstypes.SnapshotCopyConfiguration{SnapshotCopyConfigurationArn: sdkaws.String(rssARN("snapshotcopyconfiguration", id)), SnapshotCopyConfigurationId: sdkaws.String(id), DestinationRegion: sdkaws.String("us-west-2")}
}

func rssUsageLimit(id string) rsstypes.UsageLimit {
	return rsstypes.UsageLimit{UsageLimitArn: sdkaws.String(rssARN("usagelimit", id)), UsageLimitId: sdkaws.String(id), Amount: sdkaws.Int64(60)}
}

func rssDomain(workgroup, domain string) rsstypes.Association {
	return rsstypes.Association{WorkgroupName: sdkaws.String(workgroup), CustomDomainName: sdkaws.String(domain), CustomDomainCertificateArn: sdkaws.String("arn:aws:acm:" + testRegion + ":" + testAccountID + ":certificate/c1")}
}

func rssAction(ns, name string) rsstypes.ScheduledActionAssociation {
	return rsstypes.ScheduledActionAssociation{NamespaceName: sdkaws.String(ns), ScheduledActionName: sdkaws.String(name)}
}

// rssSingleCallPhase is one of the unfiltered, region-wide list phases.
type rssSingleCallPhase struct {
	op      string
	typ     string
	twoPage func(*stubRSS) []string // seeds two pages plus an id-less item; returns the stored NativeIDs
	scan    func(context.Context, *stubRSS, *store.Store) (int, error)
}

func rssSingleCallPhases() []rssSingleCallPhase {
	acct := newTestAccount(testAccountID)
	wgs, nss := rssWorkgroupParents(), rssNamespaceParents()
	return []rssSingleCallPhase{
		{
			op: "ListReservations", typ: TypeRedshiftServerlessReservation,
			twoPage: func(s *stubRSS) []string {
				s.reservations = [][]rsstypes.Reservation{{rssReservation("r1"), {Capacity: 4}}, {rssReservation("r2")}}
				return []string{rssARN("reservation", "r1"), rssARN("reservation", "r2")}
			},
			scan: func(ctx context.Context, s *stubRSS, st *store.Store) (int, error) {
				return scanRSSReservations(ctx, s, acct, testRegion, st, testScanID)
			},
		},
		{
			op: "ListSnapshotCopyConfigurations", typ: TypeRedshiftServerlessSnapshotCopyConfiguration,
			twoPage: func(s *stubRSS) []string {
				s.copyConfigs = [][]rsstypes.SnapshotCopyConfiguration{{rssCopyConfig("c1"), {NamespaceName: sdkaws.String("ns1")}}, {rssCopyConfig("c2")}}
				return []string{rssARN("snapshotcopyconfiguration", "c1"), rssARN("snapshotcopyconfiguration", "c2")}
			},
			scan: func(ctx context.Context, s *stubRSS, st *store.Store) (int, error) {
				return scanRSSSnapshotCopyConfigurations(ctx, s, acct, testRegion, st, testScanID)
			},
		},
		{
			op: "ListUsageLimits", typ: TypeRedshiftServerlessUsageLimit,
			twoPage: func(s *stubRSS) []string {
				s.usageLimits = [][]rsstypes.UsageLimit{{rssUsageLimit("u1"), {UsageLimitId: sdkaws.String("u-noarn")}}, {rssUsageLimit("u2")}}
				return []string{rssARN("usagelimit", "u1"), rssARN("usagelimit", "u2")}
			},
			scan: func(ctx context.Context, s *stubRSS, st *store.Store) (int, error) {
				return scanRSSUsageLimits(ctx, s, acct, testRegion, st, testScanID)
			},
		},
		{
			op: "ListCustomDomainAssociations", typ: TypeRedshiftServerlessCustomDomainAssociation,
			twoPage: func(s *stubRSS) []string {
				s.domains = [][]rsstypes.Association{{rssDomain("wg1", "db.example.com"), {WorkgroupName: sdkaws.String("wg1")}}, {rssDomain("wg2", "db2.example.com")}}
				return []string{rssARN("workgroup", "w-1") + "/custom-domain-association/db.example.com", rssARN("workgroup", "w-2") + "/custom-domain-association/db2.example.com"}
			},
			scan: func(ctx context.Context, s *stubRSS, st *store.Store) (int, error) {
				return scanRSSCustomDomainAssociations(ctx, s, acct, testRegion, st, testScanID, wgs)
			},
		},
		{
			op: "ListScheduledActions", typ: TypeRedshiftServerlessScheduledAction,
			twoPage: func(s *stubRSS) []string {
				s.actions = [][]rsstypes.ScheduledActionAssociation{{rssAction("ns1", "nightly"), {NamespaceName: sdkaws.String("ns1")}}, {rssAction("ns2", "hourly")}}
				return []string{rssARN("namespace", "n-1") + "/scheduled-action/nightly", rssARN("namespace", "n-2") + "/scheduled-action/hourly"}
			},
			scan: func(ctx context.Context, s *stubRSS, st *store.Store) (int, error) {
				return scanRSSScheduledActions(ctx, s, acct, testRegion, st, testScanID, nss)
			},
		},
	}
}

func rssWorkgroupParents() []childParent {
	return []childParent{{id: "wg1", arn: rssARN("workgroup", "w-1")}, {id: "wg2", arn: rssARN("workgroup", "w-2")}}
}

func rssNamespaceParents() []childParent {
	return []childParent{{id: "ns1", arn: rssARN("namespace", "n-1")}, {id: "ns2", arn: rssARN("namespace", "n-2")}}
}

func seedRSSParents(t *testing.T, st *store.Store) {
	t.Helper()
	for _, w := range rssWorkgroupParents() {
		upsertTestResource(t, st, "aws", testAccountID, TypeRedshiftServerlessWorkgroup, w.arn, testRegion, "{}")
	}
	for _, n := range rssNamespaceParents() {
		upsertTestResource(t, st, "aws", testAccountID, TypeRedshiftServerlessNamespace, n.arn, testRegion, "{}")
	}
}

func TestScanRSSSingleCallPhases_PagesStoreEveryRow(t *testing.T) {
	t.Parallel()
	for _, ph := range rssSingleCallPhases() {
		t.Run(ph.op, func(t *testing.T) {
			st := newTestStore(t)
			seedRSSParents(t, st)
			stub := &stubRSS{}
			want := ph.twoPage(stub)

			total, err := ph.scan(context.Background(), stub, st)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != len(want) {
				t.Errorf("total=%d, want %d", total, len(want))
			}
			rows := lmvRows(t, st, ph.typ)
			if len(rows) != len(want) {
				t.Errorf("stored %d rows, want %d (%v)", len(rows), len(want), rows)
			}
			for _, id := range want {
				r, ok := rows[id]
				if !ok {
					t.Errorf("missing row %s", id)
					continue
				}
				if r.Type != ph.typ || sv(r.Region) != testRegion || sv(r.Name) == "" {
					t.Errorf("%s: type=%s region=%s name=%q", id, r.Type, sv(r.Region), sv(r.Name))
				}
			}
		})
	}
}

func TestScanRSSSingleCallPhases_Errors(t *testing.T) {
	t.Parallel()
	deny := apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/x is not authorized to perform: redshift-serverless:List")
	for _, ph := range rssSingleCallPhases() {
		for _, tc := range []struct {
			name         string
			seedPages    bool
			err          error
			wantRows     int
			wantWarnings int
			wantErr      string
		}{
			{name: "empty"},
			{name: "denied warns", err: deny, wantWarnings: 1},
			{name: "denied after pages keeps rows", seedPages: true, err: deny, wantRows: 2, wantWarnings: 1},
			{name: "other error propagates", err: apiErr("ValidationException", "bad"), wantErr: "ValidationException"},
		} {
			t.Run(ph.op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				seedRSSParents(t, st)
				warnings := 0
				st.OnWarn = func(store.ScanWarning) { warnings++ }
				stub := &stubRSS{errs: map[string]error{ph.op: tc.err}}
				if tc.seedPages {
					ph.twoPage(stub)
				}

				_, err := ph.scan(context.Background(), stub, st)
				if tc.wantErr != "" {
					if !isAPIErrorCode(err, tc.wantErr) {
						t.Fatalf("err=%v, want %s propagated", err, tc.wantErr)
					}
					return
				}
				if err != nil {
					t.Fatalf("err=%v, want nil", err)
				}
				if warnings != tc.wantWarnings {
					t.Errorf("warnings=%d, want %d", warnings, tc.wantWarnings)
				}
				if rows := lmvRows(t, st, ph.typ); len(rows) != tc.wantRows {
					t.Errorf("stored %d rows, want %d", len(rows), tc.wantRows)
				}
			})
		}
	}
}

func TestScanRSSReservations_FieldsAndAttrs(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	stub := &stubRSS{reservations: [][]rsstypes.Reservation{{rssReservation("r1"), {ReservationArn: sdkaws.String(rssARN("reservation", "r0")), Status: sdkaws.String("")}}}}
	if _, err := scanRSSReservations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := lmvRows(t, st, TypeRedshiftServerlessReservation)
	r := rows[rssARN("reservation", "r1")]
	if sv(r.Name) != "r1" || sv(r.Status) != "ACTIVE" {
		t.Errorf("name=%q status=%q, want r1/ACTIVE", sv(r.Name), sv(r.Status))
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs["Capacity"] != float64(8) {
		t.Errorf("attrs=%v, want Capacity 8", attrs)
	}
	if r0, ok := rows[rssARN("reservation", "r0")]; !ok || r0.Status != nil {
		t.Errorf("empty-status reservation: present=%v status=%v, want stored with nil status", ok, r0.Status)
	}
}

func TestScanRSSCustomDomainAssociations_KeyedAndContainedByWorkgroup(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	seedRSSParents(t, st)
	stub := &stubRSS{domains: [][]rsstypes.Association{{
		rssDomain("wg1", "db.example.com"),
		rssDomain("unscanned", "orphan.example.com"),
	}}}
	if _, err := scanRSSCustomDomainAssociations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, rssWorkgroupParents()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := lmvRows(t, st, TypeRedshiftServerlessCustomDomainAssociation)
	native := rssARN("workgroup", "w-1") + "/custom-domain-association/db.example.com"
	if len(rows) != 1 {
		t.Fatalf("stored %v, want only %s (association on an unscanned workgroup skipped)", rows, native)
	}
	r, ok := rows[native]
	if !ok || sv(r.Name) != "db.example.com" {
		t.Fatalf("row %s: present=%v name=%q", native, ok, sv(r.Name))
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs["CustomDomainCertificateArn"] == nil {
		t.Errorf("attrs=%v, want CustomDomainCertificateArn", attrs)
	}

	w1 := store.ResourceID("aws", testAccountID, rssARN("workgroup", "w-1"))
	rels, err := st.RelationshipsFrom(w1, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, w1, r.ID, store.RelContains)
	w2rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, rssARN("workgroup", "w-2")), store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(w2rels) != 0 {
		t.Errorf("wg2 contains %v, want nothing", w2rels)
	}
}

func TestScanRSSScheduledActions_KeyedAndContainedByNamespace(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	seedRSSParents(t, st)
	stub := &stubRSS{actions: [][]rsstypes.ScheduledActionAssociation{{
		rssAction("ns1", "nightly"),
		rssAction("unscanned", "orphan"),
	}}}

	if _, err := scanRSSScheduledActions(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, rssNamespaceParents()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := lmvRows(t, st, TypeRedshiftServerlessScheduledAction)
	native := rssARN("namespace", "n-1") + "/scheduled-action/nightly"
	if len(rows) != 1 {
		t.Fatalf("stored %v, want only %s (action on an unscanned namespace skipped)", rows, native)
	}
	r, ok := rows[native]
	if !ok || sv(r.Name) != "nightly" {
		t.Fatalf("row %s: present=%v name=%q", native, ok, sv(r.Name))
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs["NamespaceName"] != "ns1" {
		t.Errorf("attrs=%v, want NamespaceName ns1", attrs)
	}

	n1 := store.ResourceID("aws", testAccountID, rssARN("namespace", "n-1"))
	rels, err := st.RelationshipsFrom(n1, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, n1, r.ID, store.RelContains)
	n2rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, rssARN("namespace", "n-2")), store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(n2rels) != 0 {
		t.Errorf("ns2 contains %v, want nothing", n2rels)
	}
}

func TestScanRSSNamedChildren_NoParentsNoCall(t *testing.T) {
	t.Parallel()
	acct := newTestAccount(testAccountID)
	for _, tc := range []struct {
		op   string
		scan func(context.Context, *stubRSS, *store.Store) (int, error)
	}{
		{op: "ListScheduledActions", scan: func(ctx context.Context, s *stubRSS, st *store.Store) (int, error) {
			return scanRSSScheduledActions(ctx, s, acct, testRegion, st, testScanID, nil)
		}},
		{op: "ListCustomDomainAssociations", scan: func(ctx context.Context, s *stubRSS, st *store.Store) (int, error) {
			return scanRSSCustomDomainAssociations(ctx, s, acct, testRegion, st, testScanID, nil)
		}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			stub := &stubRSS{
				actions: [][]rsstypes.ScheduledActionAssociation{{rssAction("ns1", "nightly")}},
				domains: [][]rsstypes.Association{{rssDomain("wg1", "db.example.com")}},
			}
			total, err := tc.scan(context.Background(), stub, st)
			if err != nil || total != 0 || stub.calls[tc.op] != 0 {
				t.Fatalf("total=%d err=%v calls=%d, want 0/nil/0", total, err, stub.calls[tc.op])
			}
		})
	}
}

func rssPhasesStub() *stubRSS {
	return &stubRSS{
		namespaces: [][]rsstypes.Namespace{{{NamespaceArn: sdkaws.String(rssARN("namespace", "n-1")), NamespaceName: sdkaws.String("ns1")}}},
		workgroups: [][]rsstypes.Workgroup{{{WorkgroupArn: sdkaws.String(rssARN("workgroup", "w-1")), WorkgroupName: sdkaws.String("wg1")}}},
		actions:    [][]rsstypes.ScheduledActionAssociation{{rssAction("ns1", "nightly")}},
		domains:    [][]rsstypes.Association{{rssDomain("wg1", "db.example.com")}},
	}
}

// TestScanRSSPhases_ChildrenUseListedParents runs the whole service body to
// pin that the namespace and workgroup phases hand their lists to the
// scheduled-action and custom-domain phases.
func TestScanRSSPhases_ChildrenUseListedParents(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, err := scanRSSPhases(context.Background(), rssPhasesStub(), newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if _, ok := lmvRows(t, st, TypeRedshiftServerlessScheduledAction)[rssARN("namespace", "n-1")+"/scheduled-action/nightly"]; !ok {
		t.Error("scheduled action under the listed namespace not stored")
	}
	if _, ok := lmvRows(t, st, TypeRedshiftServerlessCustomDomainAssociation)[rssARN("workgroup", "w-1")+"/custom-domain-association/db.example.com"]; !ok {
		t.Error("custom domain association under the listed workgroup not stored")
	}
}

// TestScanRSSPhases_PhaseErrorKeepsLaterPhases fails each region-wide phase
// in turn: the error surfaces, the namespace and workgroup rows are stored,
// and every other phase still runs.
func TestScanRSSPhases_PhaseErrorKeepsLaterPhases(t *testing.T) {
	t.Parallel()
	ops := []string{"ListReservations", "ListSnapshotCopyConfigurations", "ListUsageLimits", "ListScheduledActions", "ListCustomDomainAssociations"}
	for _, failing := range ops {
		t.Run(failing, func(t *testing.T) {
			st := newTestStore(t)
			stub := rssPhasesStub()
			stub.errs = map[string]error{failing: apiErr("ValidationException", "bad")}

			_, err := scanRSSPhases(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
			assertAPICode(t, err, "ValidationException")
			if _, ok := lmvRows(t, st, TypeRedshiftServerlessNamespace)[rssARN("namespace", "n-1")]; !ok {
				t.Error("namespace row not stored")
			}
			if _, ok := lmvRows(t, st, TypeRedshiftServerlessWorkgroup)[rssARN("workgroup", "w-1")]; !ok {
				t.Error("workgroup row not stored")
			}
			for _, op := range ops {
				if stub.calls[op] == 0 {
					t.Errorf("%s did not run after %s failed", op, failing)
				}
			}
		})
	}
}

// TestScanRSSPhases_NamelessParentAdoptsNothing pins the parent-name guard: a
// namespace or workgroup listed without a name is stored, warned about once
// under its listing, and does not become the parent of a child whose parent
// name is missing.
func TestScanRSSPhases_NamelessParentAdoptsNothing(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	stub := rssPhasesStub()
	stub.namespaces[0] = append(stub.namespaces[0], rsstypes.Namespace{NamespaceArn: sdkaws.String(rssARN("namespace", "n-0"))})
	stub.workgroups[0] = append(stub.workgroups[0], rsstypes.Workgroup{WorkgroupArn: sdkaws.String(rssARN("workgroup", "w-0"))})
	stub.actions[0] = append(stub.actions[0], rsstypes.ScheduledActionAssociation{ScheduledActionName: sdkaws.String("orphan")})
	stub.domains[0] = append(stub.domains[0], rsstypes.Association{CustomDomainName: sdkaws.String("orphan.example.com")})

	if _, err := scanRSSPhases(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if rows := lmvRows(t, st, TypeRedshiftServerlessScheduledAction); len(rows) != 1 {
		t.Errorf("scheduled actions %v, want only ns1/nightly", rows)
	}
	if rows := lmvRows(t, st, TypeRedshiftServerlessCustomDomainAssociation); len(rows) != 1 {
		t.Errorf("custom domain associations %v, want only wg1/db.example.com", rows)
	}
	if _, ok := lmvRows(t, st, TypeRedshiftServerlessNamespace)[rssARN("namespace", "n-0")]; !ok {
		t.Error("nameless namespace not stored")
	}
	if _, ok := lmvRows(t, st, TypeRedshiftServerlessWorkgroup)[rssARN("workgroup", "w-0")]; !ok {
		t.Error("nameless workgroup not stored")
	}
	w := warns()
	if len(w) != 2 {
		t.Fatalf("warnings = %v; want one per nameless parent", w)
	}
	for i, want := range []struct{ op, arn string }{
		{"redshiftserverless:ListNamespaces", rssARN("namespace", "n-0")},
		{"redshiftserverless:ListWorkgroups", rssARN("workgroup", "w-0")},
	} {
		if w[i].Service != want.op || !strings.Contains(w[i].Message, want.arn) {
			t.Errorf("warning %d = %+v; want one under %s naming %s", i, w[i], want.op, want.arn)
		}
	}
}

// rssTestTotal is the rows of the scan fixture: two per tagged listing (one
// per page) and one recovery point.
const rssTestTotal = 3*2 + 1

var (
	rssDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: redshift-serverless:ListTagsForResource")
	rssThrottle   = apiErr("ThrottlingException", "Rate exceeded")
	rssValidation = apiErr("ValidationException", "bad arn")
	rssCreated    = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	rssRPARN      = "arn:aws:redshift-serverless:" + testRegion + ":" + testAccountID + ":recovery-point/rp-1"
)

// rssTagKind is one listing whose rows have their tags read: its op, the type
// it stores, the ARN resource type of its rows, the element's ARN member,
// one more member page sets with its value as JSON decodes it, and page,
// which builds the op's output holding one element under arn named name,
// created at rssCreated, with next.
type rssTagKind struct {
	op, typ, arnType string
	arnKey, attrKey  string
	attrWant         any
	page             func(arn, name, next *string) any
}

func (k rssTagKind) name(suffix string) string { return k.arnType + "-" + suffix }

func (k rssTagKind) arn(suffix string) string { return rssARN(k.arnType, k.name(suffix)) }

func rssTagKinds() []rssTagKind {
	return []rssTagKind{
		{"ListNamespaces", TypeRedshiftServerlessNamespace, "namespace", "NamespaceArn", "DbName", "dev", func(arn, name, next *string) any {
			return &redshiftserverless.ListNamespacesOutput{NextToken: next, Namespaces: []rsstypes.Namespace{{
				NamespaceArn: arn, NamespaceName: name, Status: rsstypes.NamespaceStatusAvailable, CreationDate: &rssCreated, DbName: sdkaws.String("dev"),
			}}}
		}},
		{"ListSnapshots", TypeRedshiftServerlessSnapshot, "snapshot", "SnapshotArn", "NamespaceName", "ns1", func(arn, name, next *string) any {
			return &redshiftserverless.ListSnapshotsOutput{NextToken: next, Snapshots: []rsstypes.Snapshot{{
				SnapshotArn: arn, SnapshotName: name, Status: rsstypes.SnapshotStatusAvailable, SnapshotCreateTime: &rssCreated, NamespaceName: sdkaws.String("ns1"),
			}}}
		}},
		{"ListWorkgroups", TypeRedshiftServerlessWorkgroup, "workgroup", "WorkgroupArn", "BaseCapacity", float64(8), func(arn, name, next *string) any {
			return &redshiftserverless.ListWorkgroupsOutput{NextToken: next, Workgroups: []rsstypes.Workgroup{{
				WorkgroupArn: arn, WorkgroupName: name, Status: rsstypes.WorkgroupStatusAvailable, CreationDate: &rssCreated, BaseCapacity: sdkaws.Int32(8),
			}}}
		}},
	}
}

func rssTagKindOf(t *testing.T, op string) rssTagKind {
	t.Helper()
	for _, k := range rssTagKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no tagged Redshift Serverless listing", op)
	return rssTagKind{}
}

// rssCalls records the calls of a scan and answers ListTagsForResource with
// tags, or errs, by ARN.
type rssCalls struct {
	mu      sync.Mutex
	ops     map[string]int
	inputs  map[string][]any
	tagARNs []string
	tags    map[string]map[string]string
	errs    map[string]error
}

// rssTagsOf is the tags newRSSCalls gives the "tagged" row of k: two, so a
// reader keeping only one tag fails.
func rssTagsOf(k rssTagKind) map[string]string {
	return map[string]string{"type": k.typ, "env": "prod"}
}

// newRSSCalls tags the "tagged" row of every kind with rssTagsOf.
func newRSSCalls() *rssCalls {
	c := &rssCalls{ops: map[string]int{}, inputs: map[string][]any{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range rssTagKinds() {
		c.tags[k.arn("tagged")] = rssTagsOf(k)
	}
	return c
}

func (c *rssCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ops[op]++
	c.inputs[op] = append(c.inputs[op], in)
}

func (c *rssCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ops[op]
}

func (c *rssCalls) listTags(in any) (any, error) {
	arn := sv(in.(*redshiftserverless.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var list []rsstypes.Tag
	for k, v := range tags {
		list = append(list, rsstypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &redshiftserverless.ListTagsForResourceOutput{Tags: list}, nil
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second.
func (c *rssCalls) twoPages(k rssTagKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("t2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		arn, name := k.arn(suffix), k.name(suffix)
		return k.page(&arn, &name, next), nil
	}
}

// empty answers op with out, recording the call.
func (c *rssCalls) empty(op string, out any) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(op, in)
		return out, nil
	}
}

// rssHandlers answers every op the scan calls: each tagged kind in two pages,
// one recovery point, and every other listing empty.
func rssHandlers(c *rssCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListRecoveryPoints": c.empty("ListRecoveryPoints", &redshiftserverless.ListRecoveryPointsOutput{RecoveryPoints: []rsstypes.RecoveryPoint{{
			RecoveryPointId: sdkaws.String("rp-1"), NamespaceName: sdkaws.String("namespace-tagged"), RecoveryPointCreateTime: &rssCreated,
		}}}),
		"ListEndpointAccess":             c.empty("ListEndpointAccess", &redshiftserverless.ListEndpointAccessOutput{}),
		"ListManagedWorkgroups":          c.empty("ListManagedWorkgroups", &redshiftserverless.ListManagedWorkgroupsOutput{}),
		"ListReservations":               c.empty("ListReservations", &redshiftserverless.ListReservationsOutput{}),
		"ListSnapshotCopyConfigurations": c.empty("ListSnapshotCopyConfigurations", &redshiftserverless.ListSnapshotCopyConfigurationsOutput{}),
		"ListUsageLimits":                c.empty("ListUsageLimits", &redshiftserverless.ListUsageLimitsOutput{}),
		"ListScheduledActions":           c.empty("ListScheduledActions", &redshiftserverless.ListScheduledActionsOutput{}),
		"ListCustomDomainAssociations":   c.empty("ListCustomDomainAssociations", &redshiftserverless.ListCustomDomainAssociationsOutput{}),
	}
	for _, k := range rssTagKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// rssScan runs the scan's real entry point against handlers; every op they do
// not answer is denied.
func rssScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, rssDenied)
	return scanRedshiftServerless(ctx, acct, testRegion, st, testScanID)
}

// Every tagged listing stores both its pages, each row under its ARN, named
// by its name, with its status, creation time and the listed element as
// attributes, and the tags ListTagsForResource returns for that ARN, nil for
// a resource without tags. Exactly those ARNs are sent to the tag op, and
// each listing call sends MaxResults 100. The recovery point is stored
// untagged and never sent to the tag op. No error, no warning.
func TestScanRedshiftServerless_StoresTagsOfTaggedTypes(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newRSSCalls()

	counted, upserts := countUpserts(st)
	total, err := rssScan(context.Background(), counted, rssHandlers(calls))
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != rssTestTotal || inserted != rssTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, rssTestTotal, rssTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	var wantARNs []string
	for _, k := range rssTagKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %v; want 2", k.typ, nativeIDs(rows))
		}
		for _, suffix := range []string{"tagged", "bare"} {
			arn := k.arn(suffix)
			wantARNs = append(wantARNs, arn)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			if sv(r.Name) != k.name(suffix) || sv(r.Region) != testRegion || sv(r.Status) != "AVAILABLE" || sv(r.CreatedAt) != "2026-03-04T05:06:07Z" {
				t.Errorf("%s Name, Region, Status, CreatedAt = %q, %q, %q, %q; want %q, %q, AVAILABLE, 2026-03-04T05:06:07Z",
					arn, sv(r.Name), sv(r.Region), sv(r.Status), sv(r.CreatedAt), k.name(suffix), testRegion)
			}
			var attrs map[string]any
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				t.Fatalf("%s attrs %s: %v", arn, r.AttributesJSON, err)
			}
			if attrs[k.arnKey] != arn || attrs[k.attrKey] != k.attrWant {
				t.Errorf("%s attrs %s, %s = %v, %v; want the listed element's %q, %v", arn, k.arnKey, k.attrKey, attrs[k.arnKey], attrs[k.attrKey], arn, k.attrWant)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, rssTagsOf(k))
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		if n := calls.count(k.op); n != 2 {
			t.Errorf("%s calls = %d; want 2", k.op, n)
		}
		for _, in := range calls.inputs[k.op] {
			if n := bacField[*int32](in, "MaxResults"); n == nil || *n != 100 {
				t.Errorf("%s MaxResults = %v; want 100", k.op, n)
			}
		}
	}
	rp, ok := rowsByNativeID(t, st, TypeRedshiftServerlessRecoveryPoint)[rssRPARN]
	if !ok || rp.TagsJSON != nil {
		t.Errorf("recovery point stored %v, TagsJSON %v; want stored untagged", ok, rp.TagsJSON)
	}
	slices.Sort(wantARNs)
	if got := slices.Sorted(slices.Values(calls.tagARNs)); !slices.Equal(got, wantARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, wantARNs)
	}
}

// Empty listings store nothing, read no tags and send no warning.
func TestScanRedshiftServerless_EmptyListings(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newRSSCalls()
	handlers := rssHandlers(calls)
	handlers["ListNamespaces"] = calls.empty("ListNamespaces", &redshiftserverless.ListNamespacesOutput{})
	handlers["ListSnapshots"] = calls.empty("ListSnapshots", &redshiftserverless.ListSnapshotsOutput{})
	handlers["ListWorkgroups"] = calls.empty("ListWorkgroups", &redshiftserverless.ListWorkgroupsOutput{})
	handlers["ListRecoveryPoints"] = calls.empty("ListRecoveryPoints", &redshiftserverless.ListRecoveryPointsOutput{})

	total, err := rssScan(context.Background(), st, handlers)
	if err != nil || total != 0 {
		t.Fatalf("total, err = %d, %v; want 0, nil", total, err)
	}
	if n := calls.count("ListTagsForResource"); n != 0 {
		t.Errorf("ListTagsForResource calls = %d; want 0", n)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A rescan reports every stored row in its total, not only the rows the store
// newly wrote. On a first scan into an empty store every row is new, so only a
// rescan catches a total taken from the store's written count.
func TestScanRedshiftServerless_RescanKeepsTotal(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	if _, err := rssScan(context.Background(), st, rssHandlers(newRSSCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, err := rssScan(context.Background(), st, rssHandlers(newRSSCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != rssTestTotal {
		t.Errorf("rescan total = %d; want %d", total, rssTestTotal)
	}
}

// A failed tag read: ResourceNotFoundException drops the row silently; a
// denial keeps the row untagged and warns once under the tag op; a throttle
// keeps it and is transient; any other error keeps it and is an error.
func TestScanRedshiftServerless_TagReadFailures(t *testing.T) {
	t.Parallel()
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
	for _, k := range rssTagKinds() {
		arn := k.arn("tagged")
		cases = append(cases,
			tagCase{k.typ + "/not found", arn, k.typ, apiErr("ResourceNotFoundException", "gone"), -1, "", rssTestTotal - 1, false, 0},
			tagCase{k.typ + "/denied", arn, k.typ, rssDenied, -1, "", rssTestTotal, true, 1},
			tagCase{k.typ + "/transient", arn, k.typ, rssThrottle, outcomeTransient, "ThrottlingException", rssTestTotal, true, 0},
			tagCase{k.typ + "/other error", arn, k.typ, rssValidation, outcomeError, "ValidationException", rssTestTotal, true, 0},
		)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newRSSCalls()
			calls.errs[c.arn] = c.err

			total, err := rssScan(context.Background(), st, rssHandlers(calls))
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
				if x.Service != rssTagsOp || x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, c.arn) {
					t.Errorf("warning %+v; want one under %s in %s/%s naming %s", x, rssTagsOp, testAccountID, testRegion, c.arn)
				}
			}
		})
	}
}

// Denied tag reads in two listings send one warning for the whole scan,
// under the tag op, naming the first denied row; the rows are kept untagged.
func TestScanRedshiftServerless_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warnings := bacWarnings(st, rssTagsOp)
	calls := newRSSCalls()
	for _, op := range []string{"ListNamespaces", "ListWorkgroups"} {
		k := rssTagKindOf(t, op)
		calls.errs[k.arn("tagged")] = rssDenied
		calls.errs[k.arn("bare")] = rssDenied
	}

	total, err := rssScan(context.Background(), st, rssHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != rssTestTotal {
		t.Errorf("total = %d; want %d", total, rssTestTotal)
	}
	first := rssTagKindOf(t, "ListNamespaces").arn("tagged")
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion || !strings.Contains((*warnings)[0].Message, first) {
		t.Fatalf("%s warnings = %v; want 1 in %s/%s naming %s", rssTagsOp, *warnings, testAccountID, testRegion, first)
	}
	for _, op := range []string{"ListNamespaces", "ListWorkgroups"} {
		k := rssTagKindOf(t, op)
		for _, arn := range []string{k.arn("tagged"), k.arn("bare")} {
			if r, ok := rowsByNativeID(t, st, k.typ)[arn]; !ok || r.TagsJSON != nil {
				t.Errorf("%s stored %v; want stored untagged", arn, ok)
			}
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to one
// warning, so the hard error decides the outcome.
func TestScanRedshiftServerless_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, rssTagsOp)
	calls := newRSSCalls()
	calls.errs[rssTagKindOf(t, "ListNamespaces").arn("tagged")] = rssThrottle
	calls.errs[rssTagKindOf(t, "ListWorkgroups").arn("bare")] = rssValidation

	_, err := rssScan(context.Background(), st, rssHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", rssTagsOp, *warns)
	}
}

// A snapshot or workgroup whose ARN names another account (one ListSnapshots'
// and ListWorkgroups' ownerAccount filter can return) is kept untagged
// without a warning when its tag read is refused or not found, while a
// refused read of the scanning account's own row, listed on the next page,
// is kept untagged and is the one warning.
func TestScanRedshiftServerless_OtherAccountRowsKeptUntagged(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"ListSnapshots", "ListWorkgroups"} {
		for _, c := range []struct {
			name string
			err  error
		}{{"denied", rssDenied}, {"not found", apiErr("ResourceNotFoundException", "gone")}} {
			t.Run(op+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newRSSCalls()
				handlers := rssHandlers(calls)
				k := rssTagKindOf(t, op)
				other := "arn:aws:redshift-serverless:" + testRegion + ":210987654321:" + k.arnType + "/shared"
				own := k.arn("tagged")
				calls.errs[other] = c.err
				calls.errs[own] = rssDenied
				handlers[op] = func(in any) (any, error) {
					calls.record(op, in)
					if bacField[*string](in, "NextToken") == nil {
						return k.page(&other, sdkaws.String("shared"), sdkaws.String("t2")), nil
					}
					return k.page(&own, sdkaws.String(k.name("tagged")), nil), nil
				}

				total, err := rssScan(context.Background(), st, handlers)
				if err != nil {
					t.Fatalf("scan: %v", err)
				}
				if total != rssTestTotal {
					t.Errorf("total = %d; want %d", total, rssTestTotal)
				}
				rows := rowsByNativeID(t, st, k.typ)
				for _, arn := range []string{other, own} {
					if r, ok := rows[arn]; !ok || r.TagsJSON != nil {
						t.Errorf("%s stored %v; want stored untagged", arn, ok)
					}
				}
				w := warns()
				if len(w) != 1 || w[0].Service != rssTagsOp || !strings.Contains(w[0].Message, own) || strings.Contains(w[0].Message, other) {
					t.Errorf("warnings = %v; want 1 under %s naming %s and not %s", w, rssTagsOp, own, other)
				}
			})
		}
	}
}

// rssPhaseOps is every listing the scan calls, in the order its phases run.
var rssPhaseOps = []string{
	"ListNamespaces", "ListSnapshots", "ListWorkgroups", "ListEndpointAccess", "ListRecoveryPoints",
	"ListManagedWorkgroups", "ListReservations", "ListSnapshotCopyConfigurations", "ListUsageLimits",
	"ListScheduledActions", "ListCustomDomainAssociations",
}

// A failed write of any tagged listing stops the scan: that listing reads no
// further page, no later phase runs and no warning is sent about rows the
// write lost; the phases before it keep their rows.
func TestScanRedshiftServerless_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		op        string
		wantTotal int // the 2 rows of each earlier tagged listing
	}{{"ListNamespaces", 0}, {"ListSnapshots", 2}, {"ListWorkgroups", 4}} {
		t.Run(c.op, func(t *testing.T) {
			k := rssTagKindOf(t, c.op)
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			refuseStoreWrites(t, st, k.typ)
			calls := newRSSCalls()
			calls.errs[k.arn("tagged")] = rssDenied

			total, err := rssScan(context.Background(), st, rssHandlers(calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			if n := calls.count(k.op); n != 1 {
				t.Errorf("%s called %d times; want 1, none after the failed write", k.op, n)
			}
			later := rssPhaseOps[slices.Index(rssPhaseOps, k.op)+1:]
			for _, op := range later {
				if n := calls.count(op); n != 0 {
					t.Errorf("%s called %d times after the failed write; want 0", op, n)
				}
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
		})
	}
}

// A listing's failed page keeps the page read before it, and its error does
// not stop the later listings.
func TestScanRedshiftServerless_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"ListNamespaces", "ListSnapshots", "ListWorkgroups"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newRSSCalls()
			handlers := rssHandlers(calls)
			k := rssTagKindOf(t, op)
			handlers[op] = failOnCall(2, rssValidation, handlers[op])

			total, err := rssScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if total != rssTestTotal-1 {
				t.Errorf("total = %d; want %d", total, rssTestTotal-1)
			}
			if _, ok := rowsByNativeID(t, st, k.typ)[k.arn("tagged")]; !ok {
				t.Errorf("%s row from the page before the failure missing", k.typ)
			}
			if n := calls.count("ListCustomDomainAssociations"); n != 1 {
				t.Errorf("ListCustomDomainAssociations (the last phase) calls = %d; want 1", n)
			}
		})
	}
}

// A denied listing warns once under its op; a denial on a later page keeps
// the page read before it. Every later listing still runs.
func TestScanRedshiftServerless_DeniedListingWarnsUnderItsOp(t *testing.T) {
	t.Parallel()
	for _, call := range []int{1, 2} {
		t.Run(fmt.Sprintf("page%d", call), func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newRSSCalls()
			handlers := rssHandlers(calls)
			k := rssTagKindOf(t, "ListSnapshots")
			handlers[k.op] = failOnCall(call, rssDenied, handlers[k.op])

			total, err := rssScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if want := rssTestTotal - 2 + call - 1; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if w := warns(); len(w) != 1 || w[0].Service != "redshiftserverless:"+k.op {
				t.Errorf("warnings = %v; want one under redshiftserverless:%s", w, k.op)
			}
			if n := len(rowsByNativeID(t, st, TypeRedshiftServerlessWorkgroup)); n != 2 {
				t.Errorf("workgroups (a later phase) = %d; want 2", n)
			}
		})
	}
}

// A repeated token ends a tagged listing as an error, keeping its pages.
func TestScanRedshiftServerless_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newRSSCalls()
	handlers := rssHandlers(calls)
	k := rssTagKindOf(t, "ListSnapshots")
	handlers[k.op] = func(in any) (any, error) {
		calls.record(k.op, in)
		suffix := "tagged"
		if bacField[*string](in, "NextToken") != nil {
			suffix = "bare"
		}
		arn, name := k.arn(suffix), k.name(suffix)
		return k.page(&arn, &name, sdkaws.String("same")), nil
	}

	total, err := rssScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != rssTestTotal {
		t.Errorf("total = %d; want %d", total, rssTestTotal)
	}
	if n := calls.count(k.op); n != 2 {
		t.Errorf("%s calls = %d; want 2", k.op, n)
	}
}

// An element without an ARN is not stored, is not sent to the tag op, and is
// warned about once under its listing, named by its name or by the fallback
// text when it has none.
func TestScanRedshiftServerless_ElementWithoutARNWarns(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ op, name, key string }{
		{"ListNamespaces", "ns-x", "ns-x"},
		{"ListSnapshots", "snap-x", "snap-x"},
		{"ListWorkgroups", "wg-x", "wg-x"},
		{"ListWorkgroups", "", noIdentifier},
	} {
		t.Run(c.op+"/"+c.key, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newRSSCalls()
			handlers := rssHandlers(calls)
			k := rssTagKindOf(t, c.op)
			handlers[c.op] = func(in any) (any, error) {
				calls.record(c.op, in)
				var name *string
				if c.name != "" {
					name = &c.name
				}
				return k.page(nil, name, nil), nil
			}

			total, err := rssScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != rssTestTotal-2 {
				t.Errorf("total = %d; want %d", total, rssTestTotal-2)
			}
			w := warns()
			if len(w) != 1 {
				t.Fatalf("warnings = %v; want 1", w)
			}
			if w[0].Service != "redshiftserverless:"+c.op || !strings.Contains(w[0].Message, c.key) || w[0].Scope != testAccountID+"/"+testRegion {
				t.Errorf("warning %+v; want one under redshiftserverless:%s naming %q in %s/%s", w[0], c.op, c.key, testAccountID, testRegion)
			}
			if slices.Contains(calls.tagARNs, "") {
				t.Error("an empty ARN was sent to ListTagsForResource")
			}
		})
	}
}

// Rows of the aws-us-gov and aws-cn partitions reach the tag op under their
// listed ARNs unchanged.
func TestScanRedshiftServerless_TagReadsKeepPartitionARNs(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ partition, region string }{
		{"aws-us-gov", "us-gov-west-1"},
		{"aws-cn", "cn-north-1"},
	} {
		t.Run(c.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newRSSCalls()
			handlers := rssHandlers(calls)
			arns := map[string]string{}
			for _, k := range rssTagKinds() {
				arn := "arn:" + c.partition + ":redshift-serverless:" + c.region + ":" + testAccountID + ":" + k.arnType + "/p"
				arns[k.typ] = arn
				calls.tags[arn] = map[string]string{"k": k.arnType}
				handlers[k.op] = func(in any) (any, error) {
					calls.record(k.op, in)
					return k.page(&arn, sdkaws.String("p"), nil), nil
				}
			}
			acct := newTestAccount(testAccountID)
			acct.Partition = c.partition
			acct.cfg = opStubConfig(handlers, rssDenied)
			if _, err := scanRedshiftServerless(context.Background(), acct, c.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			for _, k := range rssTagKinds() {
				arn := arns[k.typ]
				if !slices.Contains(calls.tagARNs, arn) {
					t.Errorf("ListTagsForResource ARNs %v lack %s", calls.tagARNs, arn)
				}
				assertTagsMap(t, rowsByNativeID(t, st, k.typ)[arn].TagsJSON, map[string]string{"k": k.arnType})
			}
		})
	}
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it in its listing.
func TestScanRedshiftServerless_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newRSSCalls()
	handlers := rssHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	k := rssTagKindOf(t, "ListSnapshots")
	list := handlers[k.op]
	handlers[k.op] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, err := rssScan(ctx, st, handlers)
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
	if n := calls.count("ListWorkgroups"); n != 0 {
		t.Errorf("ListWorkgroups called %d times after the stop; want 0", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanRedshiftServerless_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "redshift-serverless.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := rssHandlers(newRSSCalls())
		handlers["ListWorkgroups"] = func(any) (any, error) { return nil, nx }
		total, err := rssScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != rssTestTotal-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, rssTestTotal-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newRSSCalls()
		calls.errs[rssTagKindOf(t, "ListNamespaces").arn("bare")] = nx
		_, err := rssScan(context.Background(), st, rssHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, err := scanRedshiftServerless(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}
