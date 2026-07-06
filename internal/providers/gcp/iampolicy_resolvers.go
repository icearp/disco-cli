package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveIAMPolicyRelationships)
}

// resolveIAMPolicyRelationships walks each gcp:iam:policy resource's bindings
// and emits edges for every service-account / user / group member.
//
//   - same-project SA → `uses` edge (FK-checked via buildSAEmailIndex).
//   - cross-project SA: if the SA exists in another scanned project, emit
//     `cross-project-iam` directly to it. Otherwise emit `cross-project-iam`
//     to that project's self-node (gcp:cloudresourcemanager:project) as an
//     insert-if-absent empty-attribute placeholder, version-populated if the
//     project is later scanned. R5.
//   - `user:{email}` → `uses` edge to a gcp:admin:user row when the Cloud
//     Identity / Workspace Directory scanner populated one with the same
//     primary email.
//   - `group:{email}` → `uses` edge to a gcp:cloudidentity:group row when the
//     scanner populated one with the same group-key email.
//
// `domain:`, `allUsers`, `allAuthenticatedUsers` still skip — no resource
// rows. Workforce/Workload identity-pool federation members will land via a
// follow-up resolver once the pool scanners ship.
func resolveIAMPolicyRelationships(p *project, st *store.Store) error {
	policies, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeIAMPolicy},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(policies) == 0 {
		return nil
	}
	scanID := policies[0].DiscoveredBy

	saByEmail, err := buildSAEmailIndex(p, st)
	if err != nil {
		return err
	}

	// Tenant-wide identity indexes (workspace users + Cloud Identity groups).
	// Lazily populated on first non-SA member encountered so single-project
	// scans without an Entra-equivalent identity scan pay nothing.
	var (
		userByEmail  map[string]string
		groupByEmail map[string]string
	)

	// Cross-project SA index: SA email → resource ID across every project in
	// the store. Lazily-built so single-project scans pay nothing.
	var crossSAByEmail map[string]string

	var pending []pendingCross
	foreignProjects := map[string]struct{}{}

	for _, r := range policies {
		var policy struct {
			Bindings []struct {
				Role    string   `json:"role"`
				Members []string `json:"members"`
			} `json:"bindings"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &policy); err != nil {
			continue
		}
		for _, b := range policy.Bindings {
			for _, m := range b.Members {
				if handled, err := emitUserMemberEdge(st, r.ID, b.Role, m, &userByEmail); err != nil {
					return err
				} else if handled {
					continue
				}
				if handled, err := emitGroupMemberEdge(st, r.ID, b.Role, m, &groupByEmail); err != nil {
					return err
				} else if handled {
					continue
				}
				if err := classifySAMember(st, saByEmail, &crossSAByEmail, foreignProjects, &pending, r.ID, b.Role, m); err != nil {
					return err
				}
			}
		}
	}

	if len(foreignProjects) > 0 {
		// Empty-attribute placeholder at the project's self-node natural key
		// (account_id=native_id=<projectID>, matching gcp_hierarchy.go) so the
		// cross-project-iam FK holds and the project version-populates if
		// scanned directly later.
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
		toID := e.targetSAID
		if toID == "" {
			toID = store.ResourceID("gcp", e.projectID, TypeProject, e.projectID)
		}
		attrs := mustJSON(map[string]string{
			"role":           e.role,
			"member-email":   e.email,
			"member-project": e.projectID,
		})
		if err := st.UpsertRelationship(e.fromID, toID, store.RelCrossProjectIAM, "directed", &attrs); err != nil {
			return fmt.Errorf("upsert cross-project-iam: %w", err)
		}
	}
	return nil
}

// pendingCross carries one cross-project SA member edge whose target project
// self-node row may not yet exist when the binding is first walked.
// targetSAID is set when the SA was found in another scanned project; empty
// when the referenced project's self-node placeholder is the destination.
type pendingCross struct {
	fromID     string
	role       string
	email      string
	projectID  string
	targetSAID string
}

// emitUserMemberEdge handles `user:{email}` bindings. Lazily builds the
// workspace-user index on first non-SA member encountered. Returns
// handled=true when the member matched the user prefix (regardless of whether
// an edge was emitted), so the caller can skip the SA path.
func emitUserMemberEdge(st *store.Store, fromID, role, member string, userByEmail *map[string]string) (bool, error) {
	email, ok := strings.CutPrefix(member, "user:")
	if !ok {
		return false, nil
	}
	if *userByEmail == nil {
		idx, err := buildWorkspaceUserEmailIndex(st)
		if err != nil {
			return true, err
		}
		*userByEmail = idx
	}
	uid, ok := (*userByEmail)[strings.ToLower(email)]
	if !ok {
		return true, nil
	}
	attrs := mustJSON(map[string]string{"role": role})
	if err := st.UpsertRelationship(fromID, uid, store.RelUses, "directed", &attrs); err != nil {
		return true, fmt.Errorf("upsert policy→workspace user: %w", err)
	}
	return true, nil
}

// emitGroupMemberEdge handles `group:{email}` bindings. Mirrors emitUserMemberEdge.
func emitGroupMemberEdge(st *store.Store, fromID, role, member string, groupByEmail *map[string]string) (bool, error) {
	email, ok := strings.CutPrefix(member, "group:")
	if !ok {
		return false, nil
	}
	if *groupByEmail == nil {
		idx, err := buildCloudIdentityGroupEmailIndex(st)
		if err != nil {
			return true, err
		}
		*groupByEmail = idx
	}
	gid, ok := (*groupByEmail)[strings.ToLower(email)]
	if !ok {
		return true, nil
	}
	attrs := mustJSON(map[string]string{"role": role})
	if err := st.UpsertRelationship(fromID, gid, store.RelUses, "directed", &attrs); err != nil {
		return true, fmt.Errorf("upsert policy→cloud-identity group: %w", err)
	}
	return true, nil
}

// classifySAMember handles `serviceAccount:{email}` bindings. Same-project
// matches emit a `uses` edge directly; cross-project matches accumulate into
// pending so referenced-project placeholders insert before edges fire.
// Non-SA members fall through silently.
func classifySAMember(st *store.Store, saByEmail map[string]string, crossSAByEmail *map[string]string, foreignProjects map[string]struct{}, pending *[]pendingCross, fromID, role, member string) error {
	email, ok := strings.CutPrefix(member, "serviceAccount:")
	if !ok {
		return nil
	}
	if saID, ok := saByEmail[email]; ok {
		attrs := mustJSON(map[string]string{"role": role})
		if err := st.UpsertRelationship(fromID, saID, store.RelUses, "directed", &attrs); err != nil {
			return fmt.Errorf("upsert policy→service-account: %w", err)
		}
		return nil
	}
	homeProject, ok := projectFromSAEmail(email)
	if !ok {
		return nil
	}
	if *crossSAByEmail == nil {
		idx, err := buildAllProjectSAEmailIndex(st)
		if err != nil {
			return err
		}
		*crossSAByEmail = idx
	}
	targetSAID := (*crossSAByEmail)[email]
	if targetSAID == "" {
		foreignProjects[homeProject] = struct{}{}
	}
	*pending = append(*pending, pendingCross{
		fromID: fromID, role: role, email: email,
		projectID: homeProject, targetSAID: targetSAID,
	})
	return nil
}

// projectFromSAEmail extracts the project ID from a user-managed service
// account email (`{name}@{project}.iam.gserviceaccount.com`). Returns ok=false
// for service-agent emails (e.g. `service-NNN@compute-system.iam.gserviceaccount.com`)
// or any malformed input.
func projectFromSAEmail(email string) (string, bool) {
	_, domain, ok := strings.Cut(email, "@")
	if !ok {
		return "", false
	}
	proj, ok := strings.CutSuffix(domain, ".iam.gserviceaccount.com")
	if !ok {
		return "", false
	}
	// Service agents include a hyphen-separated system name (e.g.
	// "compute-system") rather than a project ID. Project IDs cannot contain
	// the literal "-system" suffix per GCP naming rules.
	if strings.HasSuffix(proj, "-system") || strings.Contains(proj, ".") {
		return "", false
	}
	return proj, true
}

// buildWorkspaceUserEmailIndex returns lowercased primary-email → resource
// ID for every gcp:admin:user in the store. Workspace user attrs carry
// `primaryEmail` (from the admin/directory SDK serialization).
func buildWorkspaceUserEmailIndex(st *store.Store) (map[string]string, error) {
	users, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, Types: []string{TypeWorkspaceUser},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(users))
	for _, u := range users {
		var attrs struct {
			PrimaryEmail string `json:"primaryEmail"`
		}
		if err := json.Unmarshal([]byte(u.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.PrimaryEmail != "" {
			out[strings.ToLower(attrs.PrimaryEmail)] = u.ID
		}
	}
	return out, nil
}

// buildCloudIdentityGroupEmailIndex returns lowercased group-email → resource
// ID for every gcp:cloudidentity:group in the store. Group key id is the
// canonical email under cloudidentity/v1's Group.GroupKey shape.
func buildCloudIdentityGroupEmailIndex(st *store.Store) (map[string]string, error) {
	groups, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, Types: []string{TypeCloudIdentityGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(groups))
	for _, g := range groups {
		var attrs struct {
			GroupKey *struct {
				ID string `json:"id"`
			} `json:"groupKey"`
		}
		if err := json.Unmarshal([]byte(g.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.GroupKey != nil && attrs.GroupKey.ID != "" {
			out[strings.ToLower(attrs.GroupKey.ID)] = g.ID
		}
	}
	return out, nil
}

// buildAllProjectSAEmailIndex returns email → SA resource ID across every
// gcp:iam:service-account in the store, regardless of project. Used to back
// cross-project IAM edges without losing FK safety.
func buildAllProjectSAEmailIndex(st *store.Store) (map[string]string, error) {
	sas, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, Types: []string{TypeIAMServiceAccount},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(sas))
	for _, sa := range sas {
		if i := strings.LastIndex(sa.NativeID, "/"); i >= 0 {
			out[sa.NativeID[i+1:]] = sa.ID
		}
	}
	return out, nil
}
