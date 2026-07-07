package gcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"codeberg.org/icearp/disco/store"
	monitoringv1 "google.golang.org/api/monitoring/v1"
	"google.golang.org/api/monitoring/v3"
	"google.golang.org/api/option"
)

// fakeMonitoringService builds a *monitoring.Service (v3) pointed at the
// fake server. Route templates embed the full "v3/" prefix.
func fakeMonitoringService(t *testing.T, srv *httptest.Server) *monitoring.Service {
	t.Helper()
	svc, err := monitoring.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("monitoring.NewService: %v", err)
	}
	return svc
}

// fakeMonitoringV1Service builds a *monitoringv1.Service (dashboards live on
// a separate API version) pointed at the fake server.
func fakeMonitoringV1Service(t *testing.T, srv *httptest.Server) *monitoringv1.Service {
	t.Helper()
	svc, err := monitoringv1.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("monitoringv1.NewService: %v", err)
	}
	return svc
}

func TestScanMonitoringGroups_MembersEmbedded(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	g1Name := "projects/proj1/groups/g1"
	g2Name := "projects/proj1/groups/g2"

	routes := map[string]string{
		"/v3/projects/proj1/groups": marshalAttrs(t, monitoring.ListGroupsResponse{
			Group: []*monitoring.Group{
				{Name: g1Name, DisplayName: "Web Servers"},
				{Name: g2Name, DisplayName: "Empty Group"},
			},
		}),
		"/v3/" + g1Name + "/members": marshalAttrs(t, monitoring.ListGroupMembersResponse{
			Members: []*monitoring.MonitoredResource{
				{Type: "gce_instance", Labels: map[string]string{"instance_id": "123"}},
			},
		}),
		"/v3/" + g2Name + "/members": marshalAttrs(t, monitoring.ListGroupMembersResponse{}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeMonitoringService(t, srv)

	total, inserted, err := scanMonitoringGroups(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanMonitoringGroups: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}

	g1ID := store.ResourceID("gcp", p.ID, TypeMonitoringGroup, g1Name)
	res, err := st.GetResource(g1ID)
	if err != nil {
		t.Fatalf("GetResource(g1): %v", err)
	}
	if res == nil {
		t.Fatalf("group g1 not stored")
	}
	var attrs struct {
		Members []map[string]any `json:"members"`
	}
	if err := json.Unmarshal([]byte(res.AttributesJSON), &attrs); err != nil {
		t.Fatalf("unmarshal attrs: %v", err)
	}
	if len(attrs.Members) != 1 {
		t.Fatalf("g1 members: got %d, want 1 embedded member; attrs=%s", len(attrs.Members), res.AttributesJSON)
	}
	if attrs.Members[0]["type"] != "gce_instance" {
		t.Errorf("g1 member type: got %v, want gce_instance", attrs.Members[0]["type"])
	}

	// g2 has no members — the "members" key must be absent (omitempty), not
	// present-but-empty, and definitely not populated with g1's members.
	g2ID := store.ResourceID("gcp", p.ID, TypeMonitoringGroup, g2Name)
	res2, err := st.GetResource(g2ID)
	if err != nil {
		t.Fatalf("GetResource(g2): %v", err)
	}
	if res2 == nil {
		t.Fatalf("group g2 not stored")
	}
	var attrs2 map[string]json.RawMessage
	if err := json.Unmarshal([]byte(res2.AttributesJSON), &attrs2); err != nil {
		t.Fatalf("unmarshal attrs2: %v", err)
	}
	if _, ok := attrs2["members"]; ok {
		t.Errorf("g2 should have no members key, got %s", res2.AttributesJSON)
	}
}

func TestScanMonitoringGroups_PermissionDenied(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	body := `{"error":{"code":403,"message":"caller is missing monitoring permission","errors":[{"reason":"forbidden"}]}}`
	srv := fakeGCPServerStatus(t, http.StatusForbidden, body)
	svc := fakeMonitoringService(t, srv)

	total, inserted, err := scanMonitoringGroups(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanMonitoringGroups (denied): expected nil error, got %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}

// TestScanMonitoringGroups_OneGroupErrorDoesNotDropSiblings guards a
// regression where a real (non-permission-denied) error fetching one
// group's members discarded every group's row, including ones whose
// members were already fetched successfully — because the original
// implementation batched all groups behind a single upsert at the end of
// the fan-out instead of committing each group as its own Members fetch
// completes.
func TestScanMonitoringGroups_OneGroupErrorDoesNotDropSiblings(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	g1Name := "projects/proj1/groups/g1"
	g2Name := "projects/proj1/groups/g2"
	serverErrBody := `{"error":{"code":500,"message":"internal error"}}`

	g2MembersBody := marshalAttrs(t, monitoring.ListGroupMembersResponse{
		Members: []*monitoring.MonitoredResource{{Type: "gce_instance"}},
	})
	// g1 and g2 are fanned out concurrently by forEachItem, which cancels
	// every in-flight sibling's context as soon as one goroutine returns a
	// real error — so without ordering, whether g2's upsert has already
	// committed by the time g1 errors is a genuine race. g2ServedCh makes
	// g1's response (and the resulting error/cancellation) wait until the
	// server has at least started writing g2's response, biasing the
	// outcome deterministically toward "g2 finishes first" instead of
	// leaving it to goroutine-scheduling luck.
	g2ServedCh := make(chan struct{})
	var g2ServedOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/projects/proj1/groups":
			_, _ = w.Write([]byte(marshalAttrs(t, monitoring.ListGroupsResponse{
				Group: []*monitoring.Group{
					{Name: g1Name, DisplayName: "Errors"},
					{Name: g2Name, DisplayName: "Fine"},
				},
			})))
		case "/v3/" + g1Name + "/members":
			select {
			case <-g2ServedCh:
			case <-time.After(time.Second):
			}
			// Give g2's client-side JSON decode + upsert (in-process,
			// no further I/O) a generous margin to finish before g1's
			// error reaches forEachItem and cancels g2's context.
			time.Sleep(50 * time.Millisecond)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(serverErrBody))
		case "/v3/" + g2Name + "/members":
			_, _ = w.Write([]byte(g2MembersBody))
			g2ServedOnce.Do(func() { close(g2ServedCh) })
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeMonitoringService(t, srv)

	total, inserted, err := scanMonitoringGroups(t.Context(), svc, p, st, testScanID)
	if err == nil {
		t.Fatalf("scanMonitoringGroups: expected a real (non-permission-denied) error to propagate, got nil (total=%d inserted=%d)", total, inserted)
	}

	g2ID := store.ResourceID("gcp", p.ID, TypeMonitoringGroup, g2Name)
	res, getErr := st.GetResource(g2ID)
	if getErr != nil {
		t.Fatalf("GetResource(g2): %v", getErr)
	}
	if res == nil {
		t.Fatalf("group g2 was dropped after g1's unrelated 500 error; want g2's already-fetched row preserved")
	}
}

func TestScanMonitoringSLOs_PartialDenyContinues(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	svc1Name := "projects/proj1/services/svc1"
	svc2Name := "projects/proj1/services/svc2"
	deniedBody := `{"error":{"code":403,"message":"caller is missing monitoring.slos.list","errors":[{"reason":"forbidden"}]}}`

	// upsertWithParent's closure write silently no-ops when the parent row
	// doesn't already exist — seed both services since this test exercises
	// the SLO fan-out phase standalone, without the preceding Services phase.
	upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringService, svc1Name, "", "{}")
	upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringService, svc2Name, "", "{}")

	svc2SLOBody := marshalAttrs(t, monitoring.ListServiceLevelObjectivesResponse{
		ServiceLevelObjectives: []*monitoring.ServiceLevelObjective{
			{Name: svc2Name + "/serviceLevelObjectives/slo1", DisplayName: "99.9% availability"},
		},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v3/" + svc1Name + "/serviceLevelObjectives":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(deniedBody))
		case "/v3/" + svc2Name + "/serviceLevelObjectives":
			_, _ = w.Write([]byte(svc2SLOBody))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeMonitoringService(t, srv)

	total, inserted, err := scanMonitoringSLOs(t.Context(), svc, p, []string{svc1Name, svc2Name}, st, testScanID)
	if err != nil {
		t.Fatalf("scanMonitoringSLOs: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1 (svc1 denied, svc2 succeeds)", total, inserted)
	}

	sloID := store.ResourceID("gcp", p.ID, TypeMonitoringSLO, svc2Name+"/serviceLevelObjectives/slo1")
	svc2ID := store.ResourceID("gcp", p.ID, TypeMonitoringService, svc2Name)
	rels, err := st.RelationshipsFrom(svc2ID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	var found bool
	for _, r := range rels {
		if r.ToID == sloID {
			found = true
		}
	}
	if !found {
		t.Errorf("SLO %s not found as child of service %s after svc1 denied; got %+v", sloID, svc2ID, rels)
	}

	// svc1 has no children — must not appear as a parent of anything.
	svc1ID := store.ResourceID("gcp", p.ID, TypeMonitoringService, svc1Name)
	rels1, err := st.RelationshipsFrom(svc1ID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(svc1): %v", err)
	}
	if len(rels1) != 0 {
		t.Errorf("service svc1 should have no children (denied), got %+v", rels1)
	}
}

func TestScanMonitoringFlatPhases_Basic(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	routes := map[string]string{
		"/v3/projects/proj1/alertPolicies": marshalAttrs(t, monitoring.ListAlertPoliciesResponse{
			AlertPolicies: []*monitoring.AlertPolicy{{Name: "projects/proj1/alertPolicies/a1", DisplayName: "High CPU"}},
		}),
		"/v3/projects/proj1/notificationChannels": marshalAttrs(t, monitoring.ListNotificationChannelsResponse{
			NotificationChannels: []*monitoring.NotificationChannel{{Name: "projects/proj1/notificationChannels/c1", DisplayName: "Ops Email"}},
		}),
		"/v3/projects/proj1/snoozes": marshalAttrs(t, monitoring.ListSnoozesResponse{
			Snoozes: []*monitoring.Snooze{{Name: "projects/proj1/snoozes/s1", DisplayName: "Maintenance window"}},
		}),
		"/v3/projects/proj1/uptimeCheckConfigs": marshalAttrs(t, monitoring.ListUptimeCheckConfigsResponse{
			UptimeCheckConfigs: []*monitoring.UptimeCheckConfig{{Name: "projects/proj1/uptimeCheckConfigs/u1", DisplayName: "Home page"}},
		}),
		"/v3/projects/proj1/services": marshalAttrs(t, monitoring.ListServicesResponse{
			Services: []*monitoring.MService{{Name: "projects/proj1/services/svc1", DisplayName: "Frontend"}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeMonitoringService(t, srv)

	dashRoutes := map[string]string{
		"/v1/projects/proj1/dashboards": marshalAttrs(t, monitoringv1.ListDashboardsResponse{
			Dashboards: []*monitoringv1.Dashboard{{Name: "projects/proj1/dashboards/d1", DisplayName: "Overview"}},
		}),
	}
	dashSrv := fakeGCPServer(t, dashRoutes)
	dashSvc := fakeMonitoringV1Service(t, dashSrv)

	check := func(label string, fn func() (int, int, error)) {
		t.Helper()
		total, inserted, err := fn()
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if total != 1 || inserted != 1 {
			t.Errorf("%s counts: got total=%d inserted=%d, want 1/1", label, total, inserted)
		}
	}
	check("alertPolicies", func() (int, int, error) { return scanMonitoringAlertPolicies(t.Context(), svc, p, st, testScanID) })
	check("dashboards", func() (int, int, error) { return scanMonitoringDashboards(t.Context(), dashSvc, p, st, testScanID) })
	check("notificationChannels", func() (int, int, error) {
		return scanMonitoringNotificationChannels(t.Context(), svc, p, st, testScanID)
	})
	check("snoozes", func() (int, int, error) { return scanMonitoringSnoozes(t.Context(), svc, p, st, testScanID) })
	check("uptimeCheckConfigs", func() (int, int, error) { return scanMonitoringUptimeCheckConfigs(t.Context(), svc, p, st, testScanID) })

	serviceIDs, total, inserted, err := scanMonitoringServices(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanMonitoringServices: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("services counts: got total=%d inserted=%d, want 1/1", total, inserted)
	}
	if len(serviceIDs) != 1 || serviceIDs[0] != "projects/proj1/services/svc1" {
		t.Fatalf("serviceIDs: got %v, want [projects/proj1/services/svc1]", serviceIDs)
	}
}
