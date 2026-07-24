package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(resolveIAMPolicyRelationships,
		EdgeDecl{TypeIAMPolicy, TypeIAMServiceAccount, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeWorkspaceUser, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeCloudIdentityGroup, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeIAMWorkforcePool, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeIAMWorkloadIdentityPool, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeIAMServiceAccount, store.RelCrossProjectIAM},
		EdgeDecl{TypeIAMPolicy, TypeProject, store.RelCrossProjectIAM},
	)
	registerOrgResolver(resolveIAMPolicyOrgRelationships,
		EdgeDecl{TypeIAMPolicy, TypeWorkspaceUser, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeCloudIdentityGroup, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeIAMWorkforcePool, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeIAMWorkloadIdentityPool, store.RelUses},
		EdgeDecl{TypeIAMPolicy, TypeIAMServiceAccount, store.RelOrgIAM},
		EdgeDecl{TypeIAMPolicy, TypeProject, store.RelOrgIAM},
	)
}

// resolveIAMPolicyRelationships walks each gcp:iam:policy resource's bindings
// and emits edges for every service-account / user / group member.
//
//   - same-project SA → `uses` edge (FK-checked via buildSAEmailIndex).
//
//   - cross-project SA: if the SA exists in another scanned project, emit
//     `cross-project-iam` directly to it. Otherwise emit `cross-project-iam`
//     to that project's self-node (gcp:cloudresourcemanager:project) as an
//     insert-if-absent empty-attribute placeholder, version-populated if the
//     project is later scanned. R5.
//
//   - `user:{email}` → `uses` edge to a gcp:admin:user row when the Cloud
//     Identity / Workspace Directory scanner populated one with the same
//     primary email.
//
//   - `group:{email}` → `uses` edge to a gcp:cloudidentity:group row when the
//     scanner populated one with the same group-key email.
//
//   - `principal://iam.googleapis.com/{pool}/subject/...` and
//     `principalSet://iam.googleapis.com/{pool}/{group,attribute.*,*}` →
//     `uses` edge to the referenced Workforce or Workload Identity Pool.
//
// `domain:`, `allUsers`, `allAuthenticatedUsers` still skip — no resource
// rows.
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

	saByEmail, err := buildSAEmailIndex(p, st)
	if err != nil {
		return err
	}

	return resolveIAMPolicyBindings(st, policies, saByEmail, store.RelCrossProjectIAM)
}

// resolveIAMPolicyOrgRelationships is the org/folder-scope counterpart to
// resolveIAMPolicyRelationships. iampolicy_org_scanners.go scans gcp:iam:policy
// rows at org/folder scope (AccountID = "organizations/{n}" or "folders/{n}",
// via registerOrgService) — the per-project resolver's `AccountID: p.ID`
// filter never matches those rows, so they were scanned but never resolved.
// Registered via registerOrgResolver (runs once per scan, after every
// per-project resolve pass), this queries every gcp:iam:policy row with no
// AccountID filter and keeps only the org/folder-shaped ones — the exact
// complement of what the per-project resolver already covers, so nothing is
// double-processed.
//
// There is no "same project" concept at org/folder scope, so saByEmail is
// nil (every serviceAccount: member falls through to the cross-scope path
// in classifySAMember) and the edge kind is RelOrgIAM rather than
// RelCrossProjectIAM — an org/folder-level grant has org-wide blast radius,
// a materially different risk category from a two-project grant, even
// though the target-resolution mechanism (direct match in another scanned
// project, else a project self-node placeholder) is identical.
func resolveIAMPolicyOrgRelationships(st *store.Store) error {
	all, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, Types: []string{TypeIAMPolicy},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	orgPolicies := make([]store.Resource, 0, len(all))
	for _, r := range all {
		if strings.HasPrefix(r.AccountID, "organizations/") || strings.HasPrefix(r.AccountID, "folders/") {
			orgPolicies = append(orgPolicies, r)
		}
	}
	if len(orgPolicies) == 0 {
		return nil
	}
	return resolveIAMPolicyBindings(st, orgPolicies, nil, store.RelOrgIAM)
}

// resolveIAMPolicyBindings walks each gcp:iam:policy resource's bindings and
// emits member edges — shared by the per-project resolver
// (resolveIAMPolicyRelationships, saByEmail scoped via buildSAEmailIndex) and
// the org/folder-scope resolver (resolveIAMPolicyOrgRelationships, saByEmail
// nil since there's no "same project" concept at that scope — every
// serviceAccount: member falls through to the cross-scope path). crossKind
// is the edge kind used for that cross-scope path (RelCrossProjectIAM for
// the per-project lane, RelOrgIAM for the org/folder lane) since the two
// scopes carry different blast-radius semantics despite an identical
// resolution mechanism.
func resolveIAMPolicyBindings(st *store.Store, policies []store.Resource, saByEmail map[string]string, crossKind string) error {
	scanID := policies[0].DiscoveredBy

	// Tenant-wide identity indexes (workspace users + Cloud Identity groups).
	// Lazily populated on first non-SA member encountered so single-project
	// scans without an Entra-equivalent identity scan pay nothing.
	var (
		userByEmail  map[string]string
		groupByEmail map[string]string
	)

	// Federation pool indexes (Workforce/Workload Identity). Lazily populated
	// per pool type, same reasoning as above.
	var (
		workforceByNative map[string]string
		workloadByNative  map[string]string
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
				if handled, err := emitFederationMemberEdge(st, r.ID, b.Role, m, &workforceByNative, &workloadByNative); err != nil {
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
			toID = store.ResourceID("gcp", e.projectID, e.projectID)
		}
		attrs := mustJSON(map[string]string{
			"role":           e.role,
			"member-email":   e.email,
			"member-project": e.projectID,
		})
		if err := st.UpsertRelationship(e.fromID, toID, crossKind, "directed", &attrs); err != nil {
			return fmt.Errorf("upsert %s: %w", crossKind, err)
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

// federationPoolFromPrincipal extracts the pool resource-name prefix from a
// `principal://iam.googleapis.com/{poolPrefix}/subject/{...}` or
// `principalSet://iam.googleapis.com/{poolPrefix}/group/{...}` /
// `.../attribute.{name}/{value}` / `.../*` member string (Workforce/Workload
// Identity Federation). poolPrefix is exactly the pool's own `Name` field
// (`locations/global/workforcePools/{id}` or
// `projects/{num}/locations/global/workloadIdentityPools/{id}` — confirmed
// via Google's own docs), so callers match it directly against a NativeID
// index built from the pool rows the scanner already stored — no format
// reconstruction, no project-number-vs-ID guessing.
func federationPoolFromPrincipal(member string) (poolPrefix string, ok bool) {
	rest, ok := strings.CutPrefix(member, "principal://iam.googleapis.com/")
	if !ok {
		rest, ok = strings.CutPrefix(member, "principalSet://iam.googleapis.com/")
	}
	if !ok {
		return "", false
	}
	// Find the leftmost-occurring separator, not the first checked — a
	// free-form IdP-asserted subject/group/attribute value (e.g. an OIDC
	// `sub` claim) can itself contain one of the other separators as a
	// substring, which would mis-split if separators were tried in a fixed
	// priority order instead of by position.
	bestIdx := -1
	for _, sep := range []string{"/subject/", "/group/", "/attribute."} {
		if i := strings.Index(rest, sep); i >= 0 && (bestIdx == -1 || i < bestIdx) {
			bestIdx = i
		}
	}
	if bestIdx >= 0 {
		return rest[:bestIdx], true
	}
	if prefix, ok := strings.CutSuffix(rest, "/*"); ok {
		return prefix, true
	}
	return "", false
}

// emitFederationMemberEdge handles Workforce/Workload Identity Federation
// principal bindings. Lazily builds each pool-type index on first match.
func emitFederationMemberEdge(st *store.Store, fromID, role, member string, workforceByNative, workloadByNative *map[string]string) (bool, error) {
	poolPrefix, ok := federationPoolFromPrincipal(member)
	if !ok {
		return false, nil
	}
	target := func(idxPtr *map[string]string, rtype string) (string, error) {
		if *idxPtr == nil {
			idx, err := accessContextIDByNative(st, rtype)
			if err != nil {
				return "", err
			}
			*idxPtr = idx
		}
		return (*idxPtr)[poolPrefix], nil
	}
	toID, err := target(workforceByNative, TypeIAMWorkforcePool)
	if err != nil {
		return true, err
	}
	if toID == "" {
		if toID, err = target(workloadByNative, TypeIAMWorkloadIdentityPool); err != nil {
			return true, err
		}
	}
	if toID == "" {
		return true, nil
	}
	attrs := mustJSON(map[string]string{"role": role})
	if err := st.UpsertRelationship(fromID, toID, store.RelUses, "directed", &attrs); err != nil {
		return true, fmt.Errorf("upsert policy→federation pool: %w", err)
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
