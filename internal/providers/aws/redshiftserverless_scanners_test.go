package aws

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

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
	scan    func(context.Context, *stubRSS, *store.Store) (int, int, error)
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
			scan: func(ctx context.Context, s *stubRSS, st *store.Store) (int, int, error) {
				return scanRSSReservations(ctx, s, acct, testRegion, st, testScanID)
			},
		},
		{
			op: "ListSnapshotCopyConfigurations", typ: TypeRedshiftServerlessSnapshotCopyConfiguration,
			twoPage: func(s *stubRSS) []string {
				s.copyConfigs = [][]rsstypes.SnapshotCopyConfiguration{{rssCopyConfig("c1"), {NamespaceName: sdkaws.String("ns1")}}, {rssCopyConfig("c2")}}
				return []string{rssARN("snapshotcopyconfiguration", "c1"), rssARN("snapshotcopyconfiguration", "c2")}
			},
			scan: func(ctx context.Context, s *stubRSS, st *store.Store) (int, int, error) {
				return scanRSSSnapshotCopyConfigurations(ctx, s, acct, testRegion, st, testScanID)
			},
		},
		{
			op: "ListUsageLimits", typ: TypeRedshiftServerlessUsageLimit,
			twoPage: func(s *stubRSS) []string {
				s.usageLimits = [][]rsstypes.UsageLimit{{rssUsageLimit("u1"), {UsageLimitId: sdkaws.String("u-noarn")}}, {rssUsageLimit("u2")}}
				return []string{rssARN("usagelimit", "u1"), rssARN("usagelimit", "u2")}
			},
			scan: func(ctx context.Context, s *stubRSS, st *store.Store) (int, int, error) {
				return scanRSSUsageLimits(ctx, s, acct, testRegion, st, testScanID)
			},
		},
		{
			op: "ListCustomDomainAssociations", typ: TypeRedshiftServerlessCustomDomainAssociation,
			twoPage: func(s *stubRSS) []string {
				s.domains = [][]rsstypes.Association{{rssDomain("wg1", "db.example.com"), {WorkgroupName: sdkaws.String("wg1")}}, {rssDomain("wg2", "db2.example.com")}}
				return []string{rssARN("workgroup", "w-1") + "/custom-domain-association/db.example.com", rssARN("workgroup", "w-2") + "/custom-domain-association/db2.example.com"}
			},
			scan: func(ctx context.Context, s *stubRSS, st *store.Store) (int, int, error) {
				return scanRSSCustomDomainAssociations(ctx, s, acct, testRegion, st, testScanID, wgs)
			},
		},
		{
			op: "ListScheduledActions", typ: TypeRedshiftServerlessScheduledAction,
			twoPage: func(s *stubRSS) []string {
				s.actions = [][]rsstypes.ScheduledActionAssociation{{rssAction("ns1", "nightly"), {NamespaceName: sdkaws.String("ns1")}}, {rssAction("ns2", "hourly")}}
				return []string{rssARN("namespace", "n-1") + "/scheduled-action/nightly", rssARN("namespace", "n-2") + "/scheduled-action/hourly"}
			},
			scan: func(ctx context.Context, s *stubRSS, st *store.Store) (int, int, error) {
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
	for _, ph := range rssSingleCallPhases() {
		t.Run(ph.op, func(t *testing.T) {
			st := newTestStore(t)
			seedRSSParents(t, st)
			stub := &stubRSS{}
			want := ph.twoPage(stub)

			total, _, err := ph.scan(context.Background(), stub, st)
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

				_, _, err := ph.scan(context.Background(), stub, st)
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
	st := newTestStore(t)
	stub := &stubRSS{reservations: [][]rsstypes.Reservation{{rssReservation("r1"), {ReservationArn: sdkaws.String(rssARN("reservation", "r0")), Status: sdkaws.String("")}}}}
	if _, _, err := scanRSSReservations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
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
	st := newTestStore(t)
	seedRSSParents(t, st)
	stub := &stubRSS{domains: [][]rsstypes.Association{{
		rssDomain("wg1", "db.example.com"),
		rssDomain("unscanned", "orphan.example.com"),
	}}}
	if _, _, err := scanRSSCustomDomainAssociations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, rssWorkgroupParents()); err != nil {
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
	st := newTestStore(t)
	seedRSSParents(t, st)
	stub := &stubRSS{actions: [][]rsstypes.ScheduledActionAssociation{{
		rssAction("ns1", "nightly"),
		rssAction("unscanned", "orphan"),
	}}}

	if _, _, err := scanRSSScheduledActions(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, rssNamespaceParents()); err != nil {
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
	acct := newTestAccount(testAccountID)
	for _, tc := range []struct {
		op   string
		scan func(context.Context, *stubRSS, *store.Store) (int, int, error)
	}{
		{op: "ListScheduledActions", scan: func(ctx context.Context, s *stubRSS, st *store.Store) (int, int, error) {
			return scanRSSScheduledActions(ctx, s, acct, testRegion, st, testScanID, nil)
		}},
		{op: "ListCustomDomainAssociations", scan: func(ctx context.Context, s *stubRSS, st *store.Store) (int, int, error) {
			return scanRSSCustomDomainAssociations(ctx, s, acct, testRegion, st, testScanID, nil)
		}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			stub := &stubRSS{
				actions: [][]rsstypes.ScheduledActionAssociation{{rssAction("ns1", "nightly")}},
				domains: [][]rsstypes.Association{{rssDomain("wg1", "db.example.com")}},
			}
			total, _, err := tc.scan(context.Background(), stub, st)
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
	st := newTestStore(t)
	if _, _, err := scanRSSPhases(context.Background(), rssPhasesStub(), newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if _, ok := lmvRows(t, st, TypeRedshiftServerlessScheduledAction)[rssARN("namespace", "n-1")+"/scheduled-action/nightly"]; !ok {
		t.Error("scheduled action under the listed namespace not stored")
	}
	if _, ok := lmvRows(t, st, TypeRedshiftServerlessCustomDomainAssociation)[rssARN("workgroup", "w-1")+"/custom-domain-association/db.example.com"]; !ok {
		t.Error("custom domain association under the listed workgroup not stored")
	}
}

// TestScanRSSPhases_NewPhaseErrorStops fails each new phase in turn: the error
// must surface, the established phases (which run first) must have stored
// their rows, and no later phase may run.
func TestScanRSSPhases_NewPhaseErrorStops(t *testing.T) {
	newOps := []string{"ListReservations", "ListSnapshotCopyConfigurations", "ListUsageLimits", "ListScheduledActions", "ListCustomDomainAssociations"}
	for i, failing := range newOps {
		t.Run(failing, func(t *testing.T) {
			st := newTestStore(t)
			stub := rssPhasesStub()
			stub.errs = map[string]error{failing: apiErr("ValidationException", "bad")}

			_, _, err := scanRSSPhases(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
			if !isAPIErrorCode(err, "ValidationException") {
				t.Fatalf("err=%v, want ValidationException from %s propagated", err, failing)
			}
			if _, ok := lmvRows(t, st, TypeRedshiftServerlessNamespace)[rssARN("namespace", "n-1")]; !ok {
				t.Error("namespace row not stored before the failing phase")
			}
			if _, ok := lmvRows(t, st, TypeRedshiftServerlessWorkgroup)[rssARN("workgroup", "w-1")]; !ok {
				t.Error("workgroup row not stored before the failing phase")
			}
			for j, op := range newOps {
				if ran, wantRan := stub.calls[op] > 0, j <= i; ran != wantRan {
					t.Errorf("%s ran=%v, want %v", op, ran, wantRan)
				}
			}
		})
	}
}

// TestScanRSSPhases_NamelessParentAdoptsNothing pins the parent-name guard: a
// namespace or workgroup listed without a name must not become the parent of
// a child whose parent name is missing.
func TestScanRSSPhases_NamelessParentAdoptsNothing(t *testing.T) {
	st := newTestStore(t)
	stub := rssPhasesStub()
	stub.namespaces[0] = append(stub.namespaces[0], rsstypes.Namespace{NamespaceArn: sdkaws.String(rssARN("namespace", "n-0"))})
	stub.workgroups[0] = append(stub.workgroups[0], rsstypes.Workgroup{WorkgroupArn: sdkaws.String(rssARN("workgroup", "w-0"))})
	stub.actions[0] = append(stub.actions[0], rsstypes.ScheduledActionAssociation{ScheduledActionName: sdkaws.String("orphan")})
	stub.domains[0] = append(stub.domains[0], rsstypes.Association{CustomDomainName: sdkaws.String("orphan.example.com")})

	if _, _, err := scanRSSPhases(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if rows := lmvRows(t, st, TypeRedshiftServerlessScheduledAction); len(rows) != 1 {
		t.Errorf("scheduled actions %v, want only ns1/nightly", rows)
	}
	if rows := lmvRows(t, st, TypeRedshiftServerlessCustomDomainAssociation); len(rows) != 1 {
		t.Errorf("custom domain associations %v, want only wg1/db.example.com", rows)
	}
}
