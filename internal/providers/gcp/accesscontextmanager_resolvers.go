package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerOrgResolver(resolveAccessContextManagerRelationships,
		EdgeDecl{TypeServicePerimeter, TypeAccessLevel, store.RelUses},
		EdgeDecl{TypeServicePerimeter, TypeProject, store.RelAttachedTo},
		EdgeDecl{TypeAccessPolicy, TypeProject, store.RelAttachedTo},
		EdgeDecl{TypeAccessPolicy, TypeFolder, store.RelAttachedTo},
		EdgeDecl{TypeGcpUserAccessBinding, TypeAccessLevel, store.RelUses},
		EdgeDecl{TypeAuthorizedOrgsDesc, TypeOrganization, store.RelUses},
		EdgeDecl{TypeAccessLevel, TypeAccessLevel, store.RelUses},
	)
}

// resolveAccessContextManagerRelationships is the first org-scoped resolver
// (Resolver Wave R26) — Access Context Manager's 5 types are all org/policy
// scoped (AccountID = org name or "accessPolicies/{p}"'s owning org), never a
// project ID, so no per-project resolver could ever see them; this is what
// the org-resolver lane (registerOrgResolver, gcp_registry.go) exists to fix.
//
//   - servicePerimeter -[uses]-> accessLevel: both `status` and `spec`
//     (ServicePerimeterConfig) carry `accessLevels[]`, full same-policy
//     resource names (`accessPolicies/{p}/accessLevels/{id}`), exact-matched.
//   - servicePerimeter -[attached-to]-> project: both configs' `resources[]`
//     lists `projects/{project_number}` entries (project *number*, not the
//     project ID string disco keys projects by) — matched via a
//     number-keyed index built from each scanned Project row's own attrs
//     (`name` field, which cloudresourcemanager/v3 always renders as
//     `projects/{number}`).
//   - accessPolicy -[attached-to]-> project/folder: `scopes[]` restricts the
//     policy to a `folders/{number}` or `projects/{number}` subtree. Folder's
//     own NativeID is the FULL `folders/{number}` string verbatim (same
//     convention as Organization — see gcp_hierarchy.go, which sets
//     `NativeID: folder.Name`), so the folder side matches `scope` directly
//     with no prefix-trim — unlike the project side, which must go through
//     `projectIDByNumberIndex` because Project's own NativeID is the bare
//     project-ID string, not its number.
//   - gcpUserAccessBinding -[uses]-> accessLevel: `accessLevels[]` and
//     `dryRunAccessLevels[]`, same full-resource-name shape as above.
//   - accessLevel -[uses]-> accessLevel: `Basic.Conditions[].RequiredAccessLevels[]`
//     is a same-policy AccessLevel→AccessLevel self-reference (full resource
//     name per the SDK doc's own example). `Custom` (CEL-expressed) levels
//     carry no structured reference, so only Basic levels ever produce this
//     edge.
//   - authorizedOrgsDesc -[uses]-> organization: `orgs[]` lists OTHER orgs'
//     full names (`organizations/{id}`) trusted by this one — inherently
//     cross-tenant, so unresolved orgs get an empty-attribute placeholder
//     self-node (mirrors resolveIAMPolicyRelationships's cross-project-IAM
//     placeholder pattern) rather than being silently dropped.
func resolveAccessContextManagerRelationships(st *store.Store) error {
	perimeters, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, Types: []string{TypeServicePerimeter}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	policies, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, Types: []string{TypeAccessPolicy}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	bindings, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, Types: []string{TypeGcpUserAccessBinding}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	orgsDescs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, Types: []string{TypeAuthorizedOrgsDesc}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	accessLevels, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, Types: []string{TypeAccessLevel}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(perimeters) == 0 && len(policies) == 0 && len(bindings) == 0 && len(orgsDescs) == 0 && len(accessLevels) == 0 {
		return nil
	}

	accessLevelIDByNative, err := accessContextIDByNative(st, TypeAccessLevel)
	if err != nil {
		return err
	}
	projectIDByNumber, err := projectIDByNumberIndex(st)
	if err != nil {
		return err
	}
	folderIDByNative, err := accessContextIDByNative(st, TypeFolder)
	if err != nil {
		return err
	}

	emitAccessLevels := func(fromID string, levelNames []string) error {
		for _, name := range levelNames {
			toID, ok := accessLevelIDByNative[name]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(fromID, toID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert →accessLevel: %w", err)
			}
		}
		return nil
	}

	for _, sp := range perimeters {
		var a struct {
			Status *accessContextPerimeterConfig `json:"status"`
			Spec   *accessContextPerimeterConfig `json:"spec"`
		}
		if err := json.Unmarshal([]byte(sp.AttributesJSON), &a); err != nil {
			continue
		}
		seenLevels := map[string]bool{}
		seenProjects := map[string]bool{}
		for _, cfg := range []*accessContextPerimeterConfig{a.Status, a.Spec} {
			if cfg == nil {
				continue
			}
			for _, name := range cfg.AccessLevels {
				if seenLevels[name] {
					continue
				}
				seenLevels[name] = true
				if err := emitAccessLevels(sp.ID, []string{name}); err != nil {
					return err
				}
			}
			for _, res := range cfg.Resources {
				num, ok := strings.CutPrefix(res, "projects/")
				if !ok || seenProjects[num] {
					continue
				}
				seenProjects[num] = true
				projID, ok := projectIDByNumber[num]
				if !ok {
					continue
				}
				if err := st.UpsertRelationship(sp.ID, projID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert servicePerimeter→project: %w", err)
				}
			}
		}
	}

	for _, pol := range policies {
		var a struct {
			Scopes []string `json:"scopes"`
		}
		if err := json.Unmarshal([]byte(pol.AttributesJSON), &a); err != nil {
			continue
		}
		for _, scope := range a.Scopes {
			switch {
			case strings.HasPrefix(scope, "folders/"):
				toID, ok := folderIDByNative[scope]
				if !ok {
					continue
				}
				if err := st.UpsertRelationship(pol.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert accessPolicy→folder: %w", err)
				}
			case strings.HasPrefix(scope, "projects/"):
				num := strings.TrimPrefix(scope, "projects/")
				toID, ok := projectIDByNumber[num]
				if !ok {
					continue
				}
				if err := st.UpsertRelationship(pol.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert accessPolicy→project: %w", err)
				}
			}
		}
	}

	for _, b := range bindings {
		var a struct {
			AccessLevels       []string `json:"accessLevels"`
			DryRunAccessLevels []string `json:"dryRunAccessLevels"`
		}
		if err := json.Unmarshal([]byte(b.AttributesJSON), &a); err != nil {
			continue
		}
		if err := emitAccessLevels(b.ID, a.AccessLevels); err != nil {
			return err
		}
		if err := emitAccessLevels(b.ID, a.DryRunAccessLevels); err != nil {
			return err
		}
	}

	if len(orgsDescs) > 0 {
		if err := emitAuthorizedOrgsDescOrgs(st, orgsDescs); err != nil {
			return err
		}
	}

	for _, al := range accessLevels {
		var a struct {
			Basic *struct {
				Conditions []struct {
					RequiredAccessLevels []string `json:"requiredAccessLevels"`
				} `json:"conditions"`
			} `json:"basic"`
		}
		if err := json.Unmarshal([]byte(al.AttributesJSON), &a); err != nil || a.Basic == nil {
			continue
		}
		seen := map[string]bool{}
		for _, cond := range a.Basic.Conditions {
			for _, name := range cond.RequiredAccessLevels {
				if seen[name] {
					continue
				}
				seen[name] = true
				if err := emitAccessLevels(al.ID, []string{name}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// accessContextPerimeterConfig mirrors accesscontextmanager/v1's
// ServicePerimeterConfig — only the two fields this resolver reads.
type accessContextPerimeterConfig struct {
	AccessLevels []string `json:"accessLevels"`
	Resources    []string `json:"resources"`
}

// accessContextIDByNative maps every scanned resource of rtype to its
// resource ID, keyed by its own NativeID verbatim (no AccountID filter — org
// resolvers span every scope in the store). Used where the reference field
// already matches the target's own NativeID with no transform needed
// (AccessLevel's full resource name, Folder's bare numeric ID).
func accessContextIDByNative(st *store.Store, rtype string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, Types: []string{rtype}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		idx[r.NativeID] = r.ID
	}
	return idx, nil
}

// projectIDByNumberIndex maps every scanned Project's numeric project number
// (parsed from its own attrs `name` field, e.g. "projects/123456789012" —
// cloudresourcemanager/v3's own project-number identifier, distinct from the
// project-ID string disco otherwise keys Project rows by) to its resource ID.
func projectIDByNumberIndex(st *store.Store) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, Types: []string{TypeProject}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		var a struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		if num, ok := strings.CutPrefix(a.Name, "projects/"); ok && num != "" {
			idx[num] = r.ID
		}
	}
	return idx, nil
}

// emitAuthorizedOrgsDescOrgs wires authorizedOrgsDesc -[uses]-> organization
// for every org listed in `orgs[]`. Cross-tenant by construction — the orgs
// trusted by this authorization relationship are rarely the same org this
// scan ran against — so unresolved orgs get an empty-attribute placeholder
// self-node first (same InsertResourcesIfAbsent pattern as
// resolveIAMPolicyRelationships's cross-project-IAM placeholders), then the
// edge is emitted against the now-guaranteed-present row.
func emitAuthorizedOrgsDescOrgs(st *store.Store, orgsDescs []store.Resource) error {
	type pendingEdge struct {
		fromID, orgName string
	}
	var pending []pendingEdge
	foreignOrgs := map[string]struct{}{}
	scanID := orgsDescs[0].DiscoveredBy

	for _, aod := range orgsDescs {
		var a struct {
			Orgs []string `json:"orgs"`
		}
		if err := json.Unmarshal([]byte(aod.AttributesJSON), &a); err != nil {
			continue
		}
		for _, orgName := range a.Orgs {
			if orgName == "" {
				continue
			}
			pending = append(pending, pendingEdge{fromID: aod.ID, orgName: orgName})
			foreignOrgs[orgName] = struct{}{}
		}
	}
	if len(pending) == 0 {
		return nil
	}

	placeholders := make([]*store.Resource, 0, len(foreignOrgs))
	for orgName := range foreignOrgs {
		name := orgName
		placeholders = append(placeholders, &store.Resource{
			Provider:       "gcp",
			AccountID:      orgName,
			Type:           TypeOrganization,
			NativeID:       orgName,
			Name:           &name,
			AttributesJSON: "{}",
			DiscoveredBy:   scanID,
		})
	}
	if _, err := st.InsertResourcesIfAbsent(placeholders); err != nil {
		return fmt.Errorf("insert referenced-organization placeholders: %w", err)
	}

	for _, e := range pending {
		toID := store.ResourceID("gcp", e.orgName, e.orgName)
		if err := st.UpsertRelationship(e.fromID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert authorizedOrgsDesc→organization: %w", err)
		}
	}
	return nil
}
