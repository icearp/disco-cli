package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveIAMPolicyRelationships)
	// Synthetic stub for cross-project SA refs whose owning project is out of
	// scan scope (R5). Pure disco bookkeeping — no upstream registry entry.
	registerExtraEmits(
		coverage.TypeDecl{Service: "iam", DiscoType: TypeIAMForeignProject, Synthetic: true},
	)
}

// resolveIAMPolicyRelationships walks each gcp:iam:policy resource's bindings
// and emits edges for every service-account / user / group member.
//
//   - same-project SA → `uses` edge (FK-checked via buildSAEmailIndex).
//   - cross-project SA: if the SA exists in any other scanned project, emit a
//     `cross-project-iam` edge directly to that SA. If the SA's project is not
//     in scan scope, emit `cross-project-iam` to a synthetic
//     gcp:iam:foreign-project stub representing the foreign project. R5.
//   - `user:{email}` → `uses` edge to a gcp:admin:user row when the
//     Cloud Identity / Workspace Directory scanner has populated one with the
//     same primary email.
//   - `group:{email}` → `uses` edge to a gcp:cloudidentity:group row when the
//     scanner has populated one with the same group-key email.
//
// `domain:`, `allUsers`, `allAuthenticatedUsers` still skip — no resource
// rows. Workforce/Workload identity-pool federation members will land via a
// follow-up resolver once the pool scanners ship.
func resolveIAMPolicyRelationships(p *project, st *store.Store) error {
	policies, err := st.ListResources(store.ResourceFilter{
		Provider: "gcp", AccountID: p.ID, Types: []string{TypeIAMPolicy},
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

	// Pre-pass: collect distinct foreign project IDs referenced by SA members
	// not present in any scanned project. We need stubs upserted before edges.
	type pendingCross struct {
		fromID    string
		role      string
		email     string
		projectID string
		// targetSAID set when SA found in another scanned project; empty when
		// only the foreign-project stub is the destination.
		targetSAID string
	}
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
				if email, ok := strings.CutPrefix(m, "user:"); ok {
					if userByEmail == nil {
						userByEmail, err = buildWorkspaceUserEmailIndex(st)
						if err != nil {
							return err
						}
					}
					if uid, ok := userByEmail[strings.ToLower(email)]; ok {
						attrs := mustJSON(map[string]string{"role": b.Role})
						if err := st.UpsertRelationship(r.ID, uid, store.RelUses, "directed", &attrs); err != nil {
							return fmt.Errorf("upsert policy→workspace user: %w", err)
						}
					}
					continue
				}
				if email, ok := strings.CutPrefix(m, "group:"); ok {
					if groupByEmail == nil {
						groupByEmail, err = buildCloudIdentityGroupEmailIndex(st)
						if err != nil {
							return err
						}
					}
					if gid, ok := groupByEmail[strings.ToLower(email)]; ok {
						attrs := mustJSON(map[string]string{"role": b.Role})
						if err := st.UpsertRelationship(r.ID, gid, store.RelUses, "directed", &attrs); err != nil {
							return fmt.Errorf("upsert policy→cloud-identity group: %w", err)
						}
					}
					continue
				}
				email, ok := strings.CutPrefix(m, "serviceAccount:")
				if !ok {
					continue
				}
				if saID, ok := saByEmail[email]; ok {
					attrs := mustJSON(map[string]string{"role": b.Role})
					if err := st.UpsertRelationship(r.ID, saID, store.RelUses, "directed", &attrs); err != nil {
						return fmt.Errorf("upsert policy→service-account: %w", err)
					}
					continue
				}
				// Cross-project. Determine SA's home project from the email
				// suffix; service agents (e.g. service-NNN@compute-system.iam.gserviceaccount.com)
				// don't fit this shape and are skipped.
				homeProject, ok := projectFromSAEmail(email)
				if !ok {
					continue
				}
				if crossSAByEmail == nil {
					crossSAByEmail, err = buildAllProjectSAEmailIndex(st)
					if err != nil {
						return err
					}
				}
				targetSAID := crossSAByEmail[email]
				if targetSAID == "" {
					foreignProjects[homeProject] = struct{}{}
				}
				pending = append(pending, pendingCross{
					fromID: r.ID, role: b.Role, email: email,
					projectID: homeProject, targetSAID: targetSAID,
				})
			}
		}
	}

	if len(foreignProjects) > 0 {
		stubs := make([]*store.Resource, 0, len(foreignProjects))
		for proj := range foreignProjects {
			nativeID := "projects/" + proj
			name := proj
			stubs = append(stubs, &store.Resource{
				Provider:       "gcp",
				AccountID:      proj,
				Type:           TypeIAMForeignProject,
				NativeID:       nativeID,
				Name:           &name,
				AttributesJSON: fmt.Sprintf(`{"projectId":%q,"synthetic":true}`, proj),
				DiscoveredBy:   scanID,
			})
		}
		if _, err := st.UpsertResources(stubs); err != nil {
			return fmt.Errorf("upsert foreign-project stubs: %w", err)
		}
	}

	for _, e := range pending {
		toID := e.targetSAID
		if toID == "" {
			toID = store.ResourceID("gcp", e.projectID, TypeIAMForeignProject, "projects/"+e.projectID)
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
		Provider: "gcp", Types: []string{TypeWorkspaceUser},
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
		Provider: "gcp", Types: []string{TypeCloudIdentityGroup},
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
		Provider: "gcp", Types: []string{TypeIAMServiceAccount},
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
