package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveAuthorizationRelationships,
		EdgeDecl{Source: TypeAuthorizationRoleAssignment, Target: TypeAuthorizationRoleDefinition, Kind: store.RelUses},
		EdgeDecl{Source: TypeAuthorizationRoleAssignment, Target: TypeEntraUser, Kind: store.RelUses},
		EdgeDecl{Source: TypeAuthorizationRoleAssignment, Target: TypeEntraGroup, Kind: store.RelUses},
		EdgeDecl{Source: TypeAuthorizationRoleAssignment, Target: TypeEntraServicePrincipal, Kind: store.RelUses},
		EdgeDecl{Source: TypeAuthorizationRoleAssignment, Target: TypeSubscription, Kind: store.RelCrossSubRBAC},
		// scope can be any resource; the canonical scope-container levels:
		EdgeDecl{Source: TypeAuthorizationRoleAssignment, Target: TypeSubscription, Kind: store.RelAttachedTo},
		EdgeDecl{Source: TypeAuthorizationRoleAssignment, Target: TypeManagementGroup, Kind: store.RelAttachedTo},
		EdgeDecl{Source: TypeAuthorizationRoleAssignment, Target: TypeResourcesResourceGroup, Kind: store.RelAttachedTo},
	)
}

// resolveAuthorizationRelationships derives edges from RBAC role assignments:
//   - assignment -[uses]-> role-definition (via RoleDefinitionID)
//   - assignment -[attached-to]-> scoped-resource (via Scope, when the scope
//     matches a known resource in the local store and is in this subscription)
//   - assignment -[cross-sub-rbac]-> the referenced subscription's self-node
//     (when Scope points at a subscription other than the assignment's owner
//     sub). Out-of-scope subs get an empty-attribute placeholder that
//     version-populates if that subscription is later scanned — R5
//
// Principal edges (assignment -[uses]-> entra:user|group|service-principal)
// are emitted when the assignment's PrincipalID matches an in-store Entra row.
// The Entra scanner (azure:microsoft.entra) populates user/group/SP rows under the
// tenant AccountID; this resolver builds a tenant-wide GUID index once and
// FK-checks each assignment's principalId. Managed identities still get their
// edge via resolveManagedIdentityAssignmentPrincipals (different index path —
// MSI rows live under the sub, not the tenant).
func resolveAuthorizationRelationships(sub *subscription, st *store.Store) error {
	assignments, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeAuthorizationRoleAssignment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(assignments) == 0 {
		return nil
	}
	scanID := assignments[0].DiscoveredBy

	// Index every Azure resource across ALL subscriptions in the store so the
	// assignment Scope (cross-sub by construction at MG level) can be resolved
	// to a canonical store ResourceID. Lowercased per ARM-ID case-insensitivity.
	all, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	scopeIndex := make(map[string]string, len(all))
	for _, r := range all {
		scopeIndex[strings.ToLower(r.NativeID)] = r.ID
	}

	// Tenant-wide principal index (Entra users / groups / service-principals).
	// Keyed by lowercased object GUID — RBAC's principalId is the same GUID
	// shape Graph returns. Application registrations excluded — RBAC binds to
	// the SP companion, not the application.
	entra, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
		Types:     []string{TypeEntraUser, TypeEntraGroup, TypeEntraServicePrincipal},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	principalIndex := make(map[string]store.Resource, len(entra))
	for _, r := range entra {
		principalIndex[strings.ToLower(r.NativeID)] = r
	}

	// Role-definition index keyed by scope-independent identity so an assignment
	// FKs to either a per-sub custom definition or a tenant-deduplicated built-in.
	roleDefIndex, err := buildRoleDefIndex(sub, st)
	if err != nil {
		return err
	}

	// Pre-pass: insert placeholders for referenced out-of-scope subscriptions
	// before emitting edges so the cross-sub-rbac FK on relationships.to_id holds.
	if err := upsertForeignSubscriptionPlaceholders(sub, assignments, st, scanID); err != nil {
		return err
	}

	for _, r := range assignments {
		var attrs struct {
			Properties *struct {
				RoleDefinitionID *string `json:"roleDefinitionId"`
				Scope            *string `json:"scope"`
				PrincipalID      *string `json:"principalId"`
				PrincipalType    *string `json:"principalType"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil {
			continue
		}

		// Edge → Entra principal (user / group / service-principal). Tenant-wide
		// FK on lowercased object GUID — same GUID shape Graph returns and RBAC
		// stores. Skips MSIs (separate resolver covers them) and unknown
		// principals (orphan-deleted directory objects, or principals from
		// another tenant via guest-user invitations not yet scanned).
		if attrs.Properties.PrincipalID != nil {
			pidLow := strings.ToLower(*attrs.Properties.PrincipalID)
			if pr, ok := principalIndex[pidLow]; ok {
				edgeAttrs := mustJSON(map[string]string{
					"role-definition-id": ptrOr(attrs.Properties.RoleDefinitionID, ""),
					"principal-type":     ptrOr(attrs.Properties.PrincipalType, ""),
				})
				if err := st.UpsertRelationship(r.ID, pr.ID, store.RelUses, "directed", &edgeAttrs); err != nil {
					return fmt.Errorf("upsert role-assignment→entra principal: %w", err)
				}
			}
		}

		// Edge → role definition. FK on the scope-independent role key so the
		// target resolves whether the definition is a per-sub custom role or a
		// tenant-deduplicated built-in.
		if attrs.Properties.RoleDefinitionID != nil {
			if defID, ok := roleDefIndex[normalizeRoleDefKey(*attrs.Properties.RoleDefinitionID)]; ok {
				if err := st.UpsertRelationship(r.ID, defID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert role-assignment→role-definition: %w", err)
				}
			}
		}

		if attrs.Properties.Scope == nil {
			continue
		}
		scope := *attrs.Properties.Scope

		// Cross-sub edge: scope sub differs from assignment's owner sub. Edge
		// targets the referenced subscription's self-node (placeholder when out
		// of scope). Same-sub assignments fall through to the per-resource scope
		// match below.
		if other, ok := subscriptionFromScope(scope); ok && !strings.EqualFold(other, sub.ID) {
			toID := store.ResourceID("azure", other, TypeSubscription, "/subscriptions/"+other)
			edgeAttrs := mustJSON(map[string]string{
				"scope":              scope,
				"scope-subscription": other,
				"role-definition-id": ptrOr(attrs.Properties.RoleDefinitionID, ""),
				"principal-id":       ptrOr(attrs.Properties.PrincipalID, ""),
			})
			if err := st.UpsertRelationship(r.ID, toID, store.RelCrossSubRBAC, "directed", &edgeAttrs); err != nil {
				return fmt.Errorf("upsert cross-sub-rbac: %w", err)
			}
			// Also try resource-level match if the foreign sub happens to be
			// scanned — emit attached-to alongside cross-sub-rbac for fidelity.
		}

		// Edge → scoped resource (when the scope is a concrete resource we know about).
		if toID, ok := scopeIndex[strings.ToLower(scope)]; ok && toID != r.ID {
			if err := st.UpsertRelationship(r.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert role-assignment→scope: %w", err)
			}
		}
	}
	return nil
}

// assignmentScope extracts properties.scope from a stored role-assignment row.
func assignmentScope(r store.Resource) string {
	var attrs struct {
		Properties *struct {
			Scope *string `json:"scope"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Properties == nil || attrs.Properties.Scope == nil {
		return ""
	}
	return *attrs.Properties.Scope
}

// subscriptionFromScope returns the subscription GUID embedded in an ARM scope
// like "/subscriptions/<guid>/..." — ok=false when the scope is at MG, tenant,
// or root scope (those have no subscription segment).
func subscriptionFromScope(scope string) (string, bool) {
	low := strings.ToLower(scope)
	const prefix = "/subscriptions/"
	if !strings.HasPrefix(low, prefix) {
		return "", false
	}
	rest := low[len(prefix):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" {
		return "", false
	}
	return rest, true
}

func ptrOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

// upsertForeignSubscriptionPlaceholders collects distinct foreign subscription
// IDs referenced by assignment scopes (a scope pointing at a subscription other
// than the assignment's owner — R5 cross-sub RBAC) and insert-if-absents an
// empty-attribute row at each subscription's real self-node natural key
// (azure:microsoft.resources:subscriptions, /subscriptions/<guid>), so the
// later cross-sub-rbac edge has a valid FK target. When that subscription is
// itself scanned, scanSubscriptionResource version-populates the placeholder.
func upsertForeignSubscriptionPlaceholders(sub *subscription, assignments []store.Resource, st *store.Store, scanID string) error {
	foreignSubs := map[string]struct{}{}
	for _, r := range assignments {
		scope := assignmentScope(r)
		if scope == "" {
			continue
		}
		// subscriptionFromScope lowercases the guid — ARM IDs are
		// case-insensitive but ResourceID hashes raw, so the placeholder must
		// use the same (lowercased) form scanSubscriptionResource stores.
		if other, ok := subscriptionFromScope(scope); ok && !strings.EqualFold(other, sub.ID) {
			foreignSubs[other] = struct{}{}
		}
	}
	if len(foreignSubs) == 0 {
		return nil
	}
	placeholders := make([]*store.Resource, 0, len(foreignSubs))
	for other := range foreignSubs {
		nativeID := "/subscriptions/" + other
		name := other
		placeholders = append(placeholders, &store.Resource{
			Provider:       "azure",
			AccountID:      other,
			Type:           TypeSubscription,
			NativeID:       nativeID,
			Name:           &name,
			Region:         regionGlobal,
			AttributesJSON: "{}",
			DiscoveredBy:   scanID,
		})
	}
	if _, err := st.InsertResourcesIfAbsent(placeholders); err != nil {
		return fmt.Errorf("insert referenced-subscription placeholders: %w", err)
	}
	return nil
}

// buildRoleDefIndex returns a map of scope-independent role key → resource ID
// covering role definitions under the subscription (custom roles) and, when a
// tenant GUID is set, the tenant account (deduplicated built-ins). The scope
// prefix on a roleDefinitionId varies by where a role is used; the GUID is the
// stable identity, so keys are normalized via normalizeRoleDefKey.
func buildRoleDefIndex(sub *subscription, st *store.Store) (map[string]string, error) {
	// IncludeManaged is required: built-in role definitions are ManagedByProvider
	// and ListResources hides managed rows by default — without this the index
	// would omit every built-in and assignments would get no role-definition edge.
	roleDefs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types:          []string{TypeAuthorizationRoleDefinition},
		IncludeManaged: true,
		Limit:          util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	if sub.tenantID != "" && sub.tenantID != sub.ID {
		tenantDefs, terr := st.ListResources(store.ResourceFilter{
			Providers: []string{"azure"}, AccountID: sub.tenantID,
			Types:          []string{TypeAuthorizationRoleDefinition},
			IncludeManaged: true,
			Limit:          util.AllResources,
		})
		if terr != nil {
			return nil, terr
		}
		roleDefs = append(roleDefs, tenantDefs...)
	}
	idx := make(map[string]string, len(roleDefs))
	for _, rd := range roleDefs {
		idx[normalizeRoleDefKey(rd.NativeID)] = rd.ID
	}
	return idx, nil
}

// roleDefSegment is the scope-independent tail every role-definition ARM ID
// shares (lowercased for case-insensitive matching).
const roleDefSegment = "/providers/microsoft.authorization/roledefinitions/"

// roleDefSuffix strips the scope prefix from a role-definition ARM ID, returning
// the case-preserved `/providers/Microsoft.Authorization/roleDefinitions/{guid}`
// tail. Built-in role definitions are listed with whatever scope prefix the list
// call used (e.g. `/subscriptions/{sub}/...`); the suffix is their stable
// identity. Returns the input unchanged when the segment is absent. Used by the
// tenant built-in scanner to store a single scope-free NativeID.
func roleDefSuffix(id string) string {
	if i := strings.Index(strings.ToLower(id), roleDefSegment); i >= 0 {
		return id[i:]
	}
	return id
}

// normalizeRoleDefKey reduces a role-definition ARM ID (or a role-assignment's
// roleDefinitionId) to its scope-independent identity, lowercased — the FK key
// matching custom definitions (stored per-sub) and built-ins (deduplicated under
// the tenant account) uniformly.
func normalizeRoleDefKey(id string) string {
	return strings.ToLower(roleDefSuffix(id))
}
