package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerOrgResolver(resolveCloudIdentityOrgRelationships,
		EdgeDecl{TypeCloudIdentityDeviceUser, TypeWorkspaceUser, store.RelUses},
		EdgeDecl{TypeCloudIdentityInboundSsoAssignment, TypeCloudIdentityGroup, store.RelUses},
		EdgeDecl{TypeCloudIdentityMembership, TypeCloudIdentityGroup, store.RelUses},
		EdgeDecl{TypeCloudIdentityMembership, TypeWorkspaceUser, store.RelUses},
		EdgeDecl{TypeCloudIdentityPolicy, TypeCloudIdentityGroup, store.RelUses},
	)
}

// resolveCloudIdentityOrgRelationships wires the 4 remaining cloudidentity
// orphans — Resolver Wave R26, second user of the new org-resolver lane
// (registerOrgResolver, gcp_registry.go). All 4 are customer-scoped
// (AccountID = Workspace customer ID), so a per-project resolver could never
// see them.
//
//   - deviceUser -[uses]-> workspaceUser via `userEmail`, matched through the
//     pre-existing buildWorkspaceUserEmailIndex (iampolicy_resolvers.go).
//   - inboundSsoAssignment -[uses]-> group via `targetGroup`
//     (`groups/{id}`) — this is a full resource name matching Group's own
//     NativeID directly, NOT the email-keyed shape used elsewhere in this
//     file; `targetOrgUnit` has no scanned OrgUnit type, left unwired.
//   - membership -[uses]-> group or workspaceUser: `preferredMemberKey.id`
//     is always an email address for Google-managed entities per the SDK's
//     own doc comment (true for both user and group members), discriminated
//     by `type`. Non-identity member types (SERVICE_ACCOUNT, SHARED_DRIVE,
//     CBCM_BROWSER, CHROME_OS_DEVICE, OTHER) have no scanned target, skipped.
//   - policy -[uses]-> group via `policyQuery.group` (full `groups/{id}`
//     name, same direct-NativeID-match shape as inboundSsoAssignment).
//     `policyQuery.orgUnit` likewise has no scanned OrgUnit type.
func resolveCloudIdentityOrgRelationships(st *store.Store) error {
	deviceUsers, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, Types: []string{TypeCloudIdentityDeviceUser}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	ssoAssignments, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, Types: []string{TypeCloudIdentityInboundSsoAssignment}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	memberships, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, Types: []string{TypeCloudIdentityMembership}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	policies, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, Types: []string{TypeCloudIdentityPolicy}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(deviceUsers) == 0 && len(ssoAssignments) == 0 && len(memberships) == 0 && len(policies) == 0 {
		return nil
	}

	groupIDByNative, err := accessContextIDByNative(st, TypeCloudIdentityGroup)
	if err != nil {
		return err
	}

	r := &cloudIdentityResolver{st: st, groupIDByNative: groupIDByNative}
	if err := r.resolveDeviceUsers(deviceUsers); err != nil {
		return err
	}
	if err := r.resolveSsoAssignments(ssoAssignments); err != nil {
		return err
	}
	if err := r.resolveMemberships(memberships); err != nil {
		return err
	}
	return r.resolvePolicies(policies)
}

// cloudIdentityResolver holds the store, the group-by-native-name index, and
// the two lazily-built email indexes (workspace users, cloudidentity groups)
// shared across the cloudidentity edge passes. The email indexes are built at
// most once, on first use. Scoped to one
// resolveCloudIdentityOrgRelationships call; not safe for concurrent use.
type cloudIdentityResolver struct {
	st              *store.Store
	groupIDByNative map[string]string
	userByEmail     map[string]string
	groupByEmail    map[string]string
}

// lazyUserByEmail returns the workspace-user email index, building it on first
// call and caching it for the rest of the resolve.
func (r *cloudIdentityResolver) lazyUserByEmail() (map[string]string, error) {
	if r.userByEmail == nil {
		idx, err := buildWorkspaceUserEmailIndex(r.st)
		if err != nil {
			return nil, err
		}
		r.userByEmail = idx
	}
	return r.userByEmail, nil
}

// lazyGroupByEmail returns the cloudidentity-group email index, building it on
// first call and caching it for the rest of the resolve.
func (r *cloudIdentityResolver) lazyGroupByEmail() (map[string]string, error) {
	if r.groupByEmail == nil {
		idx, err := buildCloudIdentityGroupEmailIndex(r.st)
		if err != nil {
			return nil, err
		}
		r.groupByEmail = idx
	}
	return r.groupByEmail, nil
}

// resolveDeviceUsers emits deviceUser -[uses]-> workspaceUser edges via each
// device user's userEmail.
func (r *cloudIdentityResolver) resolveDeviceUsers(deviceUsers []store.Resource) error {
	for _, du := range deviceUsers {
		var a struct {
			UserEmail string `json:"userEmail"`
		}
		if err := json.Unmarshal([]byte(du.AttributesJSON), &a); err != nil || a.UserEmail == "" {
			continue
		}
		idx, err := r.lazyUserByEmail()
		if err != nil {
			return err
		}
		toID, ok := idx[strings.ToLower(a.UserEmail)]
		if !ok {
			continue
		}
		if err := r.st.UpsertRelationship(du.ID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert deviceUser→workspaceUser: %w", err)
		}
	}
	return nil
}

// resolveSsoAssignments emits inboundSsoAssignment -[uses]-> group edges via
// each assignment's targetGroup full resource name.
func (r *cloudIdentityResolver) resolveSsoAssignments(ssoAssignments []store.Resource) error {
	for _, sa := range ssoAssignments {
		var a struct {
			TargetGroup string `json:"targetGroup"`
		}
		if err := json.Unmarshal([]byte(sa.AttributesJSON), &a); err != nil || a.TargetGroup == "" {
			continue
		}
		toID, ok := r.groupIDByNative[a.TargetGroup]
		if !ok {
			continue
		}
		if err := r.st.UpsertRelationship(sa.ID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert inboundSsoAssignment→group: %w", err)
		}
	}
	return nil
}

// resolveMemberships emits membership -[uses]-> group/workspaceUser edges,
// discriminated by member type, via each membership's preferredMemberKey.id
// email.
func (r *cloudIdentityResolver) resolveMemberships(memberships []store.Resource) error {
	for _, m := range memberships {
		var a struct {
			Type               string `json:"type"`
			PreferredMemberKey *struct {
				ID string `json:"id"`
			} `json:"preferredMemberKey"`
		}
		if err := json.Unmarshal([]byte(m.AttributesJSON), &a); err != nil || a.PreferredMemberKey == nil || a.PreferredMemberKey.ID == "" {
			continue
		}
		email := strings.ToLower(a.PreferredMemberKey.ID)
		var toID string
		var ok bool
		switch a.Type {
		case "GROUP":
			idx, err := r.lazyGroupByEmail()
			if err != nil {
				return err
			}
			toID, ok = idx[email]
		case "USER":
			idx, err := r.lazyUserByEmail()
			if err != nil {
				return err
			}
			toID, ok = idx[email]
		default:
			continue
		}
		if !ok {
			continue
		}
		if err := r.st.UpsertRelationship(m.ID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert membership→%s: %w", strings.ToLower(a.Type), err)
		}
	}
	return nil
}

// resolvePolicies emits policy -[uses]-> group edges via each policy's
// policyQuery.group full resource name.
func (r *cloudIdentityResolver) resolvePolicies(policies []store.Resource) error {
	for _, pol := range policies {
		var a struct {
			PolicyQuery *struct {
				Group string `json:"group"`
			} `json:"policyQuery"`
		}
		if err := json.Unmarshal([]byte(pol.AttributesJSON), &a); err != nil || a.PolicyQuery == nil || a.PolicyQuery.Group == "" {
			continue
		}
		toID, ok := r.groupIDByNative[a.PolicyQuery.Group]
		if !ok {
			continue
		}
		if err := r.st.UpsertRelationship(pol.ID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert policy→group: %w", err)
		}
	}
	return nil
}
