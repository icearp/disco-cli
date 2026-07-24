package gcp

import (
	"testing"

	monitoringv1 "google.golang.org/api/monitoring/v1"
	monitoring "google.golang.org/api/monitoring/v3"

	"github.com/icearp/disco-cli/store"
)

func TestResolveMonitoringAlertPolicyRelationships_ToNotificationChannel(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	chID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringNotificationChannel, "projects/proj-1/notificationChannels/1", "",
		marshalAttrs(t, &monitoring.NotificationChannel{Name: "projects/proj-1/notificationChannels/1"}))

	polID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringAlertPol, "projects/proj-1/alertPolicies/1", "",
		marshalAttrs(t, &monitoring.AlertPolicy{
			Name:                 "projects/proj-1/alertPolicies/1",
			NotificationChannels: []string{"projects/proj-1/notificationChannels/1"},
		}))

	if err := resolveMonitoringAlertPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringAlertPolicyRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(polID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != chID || rels[0].Kind != "uses" {
		t.Errorf("want alertPolicy->notificationChannel edge, got %+v", rels)
	}
}

func TestResolveMonitoringAlertPolicyRelationships_UnscannedChannelSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	polID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringAlertPol, "projects/proj-1/alertPolicies/1", "",
		marshalAttrs(t, &monitoring.AlertPolicy{
			Name:                 "projects/proj-1/alertPolicies/1",
			NotificationChannels: []string{"projects/proj-1/notificationChannels/not-scanned"},
		}))

	if err := resolveMonitoringAlertPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringAlertPolicyRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(polID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for unscanned channel, got %+v", rels)
	}
}

func TestResolveMonitoringAlertPolicyRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveMonitoringAlertPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringAlertPolicyRelationships on empty project: %v", err)
	}
}

func TestResolveMonitoringSnoozeRelationships_ToAlertPolicy(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	polID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringAlertPol, "projects/proj-1/alertPolicies/1", "",
		marshalAttrs(t, &monitoring.AlertPolicy{Name: "projects/proj-1/alertPolicies/1"}))

	snoozeID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringSnooze, "projects/proj-1/snoozes/1", "",
		marshalAttrs(t, &monitoring.Snooze{
			Name: "projects/proj-1/snoozes/1",
			Criteria: &monitoring.Criteria{
				Policies: []string{"projects/proj-1/alertPolicies/1"},
			},
		}))

	if err := resolveMonitoringSnoozeRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringSnoozeRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(snoozeID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != polID || rels[0].Kind != "uses" {
		t.Errorf("want snooze->alertPolicy edge, got %+v", rels)
	}
}

func TestResolveMonitoringSnoozeRelationships_NilCriteriaSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	snoozeID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringSnooze, "projects/proj-1/snoozes/1", "",
		marshalAttrs(t, &monitoring.Snooze{Name: "projects/proj-1/snoozes/1"}))

	if err := resolveMonitoringSnoozeRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringSnoozeRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(snoozeID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for nil criteria, got %+v", rels)
	}
}

func TestResolveMonitoringSnoozeRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveMonitoringSnoozeRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringSnoozeRelationships on empty project: %v", err)
	}
}

func TestResolveMonitoringGroupRelationships_ToParentGroup(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	parentID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringGroup, "projects/proj-1/groups/parent", "",
		marshalAttrs(t, &monitoring.Group{Name: "projects/proj-1/groups/parent"}))

	childID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringGroup, "projects/proj-1/groups/child", "",
		marshalAttrs(t, &monitoring.Group{
			Name:       "projects/proj-1/groups/child",
			ParentName: "projects/proj-1/groups/parent",
		}))

	if err := resolveMonitoringGroupRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringGroupRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(childID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != parentID || rels[0].Kind != "attached-to" {
		t.Errorf("want child->parent group edge, got %+v", rels)
	}
}

func TestResolveMonitoringGroupRelationships_RootGroupNoParentSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	rootID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringGroup, "projects/proj-1/groups/root", "",
		marshalAttrs(t, &monitoring.Group{Name: "projects/proj-1/groups/root"}))

	if err := resolveMonitoringGroupRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringGroupRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(rootID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge for root group with empty parentName, got %+v", rels)
	}
}

func TestResolveMonitoringGroupRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveMonitoringGroupRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringGroupRelationships on empty project: %v", err)
	}
}

func TestResolveMonitoringUptimeCheckConfigRelationships_ToGroupByBareID(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	groupID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringGroup, "projects/proj-1/groups/g1", "",
		marshalAttrs(t, &monitoring.Group{Name: "projects/proj-1/groups/g1"}))

	ucID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringUptimeCheckConfig, "projects/proj-1/uptimeCheckConfigs/1", "",
		marshalAttrs(t, &monitoring.UptimeCheckConfig{
			Name: "projects/proj-1/uptimeCheckConfigs/1",
			ResourceGroup: &monitoring.ResourceGroup{
				GroupId: "g1",
			},
		}))

	if err := resolveMonitoringUptimeCheckConfigRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringUptimeCheckConfigRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(ucID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != groupID || rels[0].Kind != "uses" {
		t.Errorf("want uptimeCheckConfig->group edge, got %+v", rels)
	}
}

func TestResolveMonitoringUptimeCheckConfigRelationships_NoResourceGroupSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	ucID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringUptimeCheckConfig, "projects/proj-1/uptimeCheckConfigs/1", "",
		marshalAttrs(t, &monitoring.UptimeCheckConfig{Name: "projects/proj-1/uptimeCheckConfigs/1"}))

	if err := resolveMonitoringUptimeCheckConfigRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringUptimeCheckConfigRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(ucID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge when resourceGroup is nil, got %+v", rels)
	}
}

func TestResolveMonitoringUptimeCheckConfigRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveMonitoringUptimeCheckConfigRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringUptimeCheckConfigRelationships on empty project: %v", err)
	}
}

func TestResolveMonitoringDashboardRelationships_ForeignProjectPlaceholder(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	dashID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringDashboard, "projects/proj-1/dashboards/1", "",
		marshalAttrs(t, &monitoringv1.Dashboard{
			Name: "projects/proj-1/dashboards/1",
			Annotations: &monitoringv1.DashboardAnnotations{
				DefaultResourceNames: []string{"projects/other-proj"},
			},
		}))

	if err := resolveMonitoringDashboardRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringDashboardRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(dashID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	wantID := store.ResourceID("gcp", "other-proj", "other-proj")
	if len(rels) != 1 || rels[0].ToID != wantID || rels[0].Kind != "uses" {
		t.Errorf("want dashboard->project edge to placeholder, got %+v", rels)
	}
	r, err := st.GetResource(wantID)
	if err != nil {
		t.Fatalf("GetResource project placeholder: %v", err)
	}
	if r.AttributesJSON != "{}" {
		t.Errorf("placeholder attributes = %q, want empty {}", r.AttributesJSON)
	}
}

func TestResolveMonitoringDashboardRelationships_EventAnnotationHostProject(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	projID := upsertTestResource(t, st, "gcp", p.ID, TypeProject, p.ID, "", "{}")

	dashID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringDashboard, "projects/proj-1/dashboards/1", "",
		marshalAttrs(t, &monitoringv1.Dashboard{
			Name: "projects/proj-1/dashboards/1",
			Annotations: &monitoringv1.DashboardAnnotations{
				EventAnnotations: []*monitoringv1.EventAnnotation{
					{ResourceNames: []string{"projects/proj-1"}},
				},
			},
		}))

	if err := resolveMonitoringDashboardRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringDashboardRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(dashID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != projID || rels[0].Kind != "uses" {
		t.Errorf("want dashboard->host project edge, got %+v", rels)
	}
}

func TestResolveMonitoringDashboardRelationships_NilAnnotationsSkipped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")

	dashID := upsertTestResource(t, st, "gcp", p.ID, TypeMonitoringDashboard, "projects/proj-1/dashboards/1", "",
		marshalAttrs(t, &monitoringv1.Dashboard{Name: "projects/proj-1/dashboards/1"}))

	if err := resolveMonitoringDashboardRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringDashboardRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(dashID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("want no edge when annotations is nil, got %+v", rels)
	}
}

func TestResolveMonitoringDashboardRelationships_EmptyProjectNoResources(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj-1")
	if err := resolveMonitoringDashboardRelationships(p, st); err != nil {
		t.Fatalf("resolveMonitoringDashboardRelationships on empty project: %v", err)
	}
}
