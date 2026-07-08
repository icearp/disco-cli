package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// Resolver Wave R11 of the resolver-implementation backlog (ROADMAP.md
// "Resolver buildout"): Cloud Monitoring's cross-references. AlertPolicy,
// Snooze, and Group all reference sibling monitoring/v3 resources by their
// full resource-name string (same API family, so an exact-string match is
// reliable per this backlog's established rule — see
// project_gcp_cross_api_selflink_mismatch memory). UptimeCheckConfig's
// ResourceGroup.GroupId is the one exception: the API docs explicitly call
// out that it's the bare [GROUP_ID], not the full path, so it needs a
// bare-name index instead of the exact-match helper. Dashboard's
// annotations.defaultResourceNames / eventAnnotations[].resourceNames name
// projects (format "projects/{id}") to scope Cloud Logging event search —
// resolved to the referenced project's self-node, following the same
// insert-if-absent placeholder pattern as resolveIAMPolicyRelationships's
// cross-project-IAM handling for projects not otherwise scanned.
func init() {
	registerResolver(resolveMonitoringAlertPolicyRelationships,
		EdgeDecl{TypeMonitoringAlertPol, TypeMonitoringNotificationChannel, store.RelUses},
	)
	registerResolver(resolveMonitoringSnoozeRelationships,
		EdgeDecl{TypeMonitoringSnooze, TypeMonitoringAlertPol, store.RelUses},
	)
	registerResolver(resolveMonitoringGroupRelationships,
		EdgeDecl{TypeMonitoringGroup, TypeMonitoringGroup, store.RelAttachedTo},
	)
	registerResolver(resolveMonitoringUptimeCheckConfigRelationships,
		EdgeDecl{TypeMonitoringUptimeCheckConfig, TypeMonitoringGroup, store.RelUses},
	)
	registerResolver(resolveMonitoringDashboardRelationships,
		EdgeDecl{TypeMonitoringDashboard, TypeProject, store.RelUses},
	)
}

// resolveMonitoringAlertPolicyRelationships wires AlertPolicy -> the
// NotificationChannel(s) it notifies (`notificationChannels[]`, full
// "projects/{p}/notificationChannels/{id}" resource names).
func resolveMonitoringAlertPolicyRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeMonitoringAlertPol},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeMonitoringNotificationChannel)
	if err != nil {
		return err
	}
	if len(scanned) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			NotificationChannels []string `json:"notificationChannels"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, ch := range attrs.NotificationChannels {
			if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeMonitoringNotificationChannel, ch, store.RelUses); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveMonitoringSnoozeRelationships wires Snooze -> the AlertPolicy(ies)
// it suppresses (`criteria.policies[]`, full
// "projects/{p}/alertPolicies/{id}" resource names).
func resolveMonitoringSnoozeRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeMonitoringSnooze},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeMonitoringAlertPol)
	if err != nil {
		return err
	}
	if len(scanned) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			Criteria struct {
				Policies []string `json:"policies"`
			} `json:"criteria"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, pol := range attrs.Criteria.Policies {
			if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeMonitoringAlertPol, pol, store.RelUses); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveMonitoringGroupRelationships wires Group -> its parent Group
// (`parentName`, full "projects/{p}/groups/{id}" resource name; empty for
// root groups).
func resolveMonitoringGroupRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeMonitoringGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeMonitoringGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ParentName string `json:"parentName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeMonitoringGroup, attrs.ParentName, store.RelAttachedTo); err != nil {
			return err
		}
	}
	return nil
}

// monitoringGroupNameIndex maps a Group's bare GROUP_ID (the segment after
// the last "/" in its NativeID) -> resource ID. UptimeCheckConfig's
// resourceGroup.groupId is documented as the bare ID only, never the full
// "projects/{p}/groups/{id}" path Group.Name/NativeID actually uses.
func monitoringGroupNameIndex(p *project, st *store.Store) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeMonitoringGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		idx[lastSegment(r.NativeID)] = r.ID
	}
	return idx, nil
}

// resolveMonitoringUptimeCheckConfigRelationships wires UptimeCheckConfig ->
// the Group it checks (`resourceGroup.groupId`, bare ID — see
// monitoringGroupNameIndex).
func resolveMonitoringUptimeCheckConfigRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeMonitoringUptimeCheckConfig},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	groupByName, err := monitoringGroupNameIndex(p, st)
	if err != nil {
		return err
	}
	if len(groupByName) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			ResourceGroup struct {
				GroupId string `json:"groupId"`
			} `json:"resourceGroup"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ResourceGroup.GroupId == "" {
			continue
		}
		groupID, ok := groupByName[attrs.ResourceGroup.GroupId]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, groupID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert uptimeCheckConfig→group: %w", err)
		}
	}
	return nil
}

// resolveMonitoringDashboardRelationships wires Dashboard -> each Project
// named in `annotations.defaultResourceNames[]` /
// `annotations.eventAnnotations[].resourceNames[]` (format "projects/{id}",
// scoping which project's logs the dashboard's event annotations search —
// defaults to the host project when empty, but can name any project,
// including ones outside this scan). Projects not already scanned get an
// empty-attribute placeholder inserted at their self-node natural key
// (mirrors resolveIAMPolicyRelationships's cross-project handling), so the
// edge FK holds and the row version-populates if that project is scanned
// directly later.
func resolveMonitoringDashboardRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeMonitoringDashboard},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanID := rows[0].DiscoveredBy

	type pendingEdge struct {
		fromID    string
		projectID string
	}
	var pending []pendingEdge
	foreignProjects := map[string]struct{}{}

	for _, r := range rows {
		var attrs struct {
			Annotations *struct {
				DefaultResourceNames []string `json:"defaultResourceNames"`
				EventAnnotations     []struct {
					ResourceNames []string `json:"resourceNames"`
				} `json:"eventAnnotations"`
			} `json:"annotations"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Annotations == nil {
			continue
		}
		seen := map[string]bool{}
		addRef := func(name string) {
			projectID := strings.TrimPrefix(name, "projects/")
			if projectID == "" || seen[projectID] {
				return
			}
			seen[projectID] = true
			pending = append(pending, pendingEdge{fromID: r.ID, projectID: projectID})
			if projectID != p.ID {
				foreignProjects[projectID] = struct{}{}
			}
		}
		for _, name := range attrs.Annotations.DefaultResourceNames {
			addRef(name)
		}
		for _, ea := range attrs.Annotations.EventAnnotations {
			for _, name := range ea.ResourceNames {
				addRef(name)
			}
		}
	}
	if len(pending) == 0 {
		return nil
	}

	if len(foreignProjects) > 0 {
		placeholders := make([]*store.Resource, 0, len(foreignProjects))
		for proj := range foreignProjects {
			name := proj
			placeholders = append(placeholders, &store.Resource{
				Provider:       "gcp",
				AccountID:      proj,
				Type:           TypeProject,
				NativeID:       proj,
				Name:           &name,
				AttributesJSON: "{}",
				DiscoveredBy:   scanID,
			})
		}
		if _, err := st.InsertResourcesIfAbsent(placeholders); err != nil {
			return fmt.Errorf("insert referenced-project placeholders: %w", err)
		}
	}

	for _, e := range pending {
		toID := store.ResourceID("gcp", e.projectID, TypeProject, e.projectID)
		if err := st.UpsertRelationship(e.fromID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert dashboard→project: %w", err)
		}
	}
	return nil
}
