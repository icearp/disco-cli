package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolveAuthorizationRelationships) }

// resolveAuthorizationRelationships derives edges from RBAC role assignments:
//   - assignment -[uses]-> role-definition (via RoleDefinitionID)
//   - assignment -[attached-to]-> scoped-resource (via Scope, when the scope
//     matches a known resource in the local store and is in this subscription)
//   - assignment -[cross-sub-rbac]-> foreign subscription stub (when Scope
//     points at a subscription other than the assignment's owner sub) — R5
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
		Provider: "azure", AccountID: sub.ID,
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
		Provider: "azure", Limit: util.AllResources,
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
		Provider: "azure",
		Types:    []string{TypeEntraUser, TypeEntraGroup, TypeEntraServicePrincipal},
		Limit:    util.AllResources,
	})
	if err != nil {
		return err
	}
	principalIndex := make(map[string]store.Resource, len(entra))
	for _, r := range entra {
		principalIndex[strings.ToLower(r.NativeID)] = r
	}

	// Pre-pass: collect distinct foreign subscription IDs referenced by Scope
	// so we can pre-upsert stubs before emitting edges (FK-safe).
	foreignSubs := map[string]struct{}{}
	for _, r := range assignments {
		scope := assignmentScope(r)
		if scope == "" {
			continue
		}
		if other, ok := subscriptionFromScope(scope); ok && !strings.EqualFold(other, sub.ID) {
			foreignSubs[other] = struct{}{}
		}
	}
	if len(foreignSubs) > 0 {
		stubs := make([]*store.Resource, 0, len(foreignSubs))
		for other := range foreignSubs {
			nativeID := "/subscriptions/" + other
			name := other
			stubs = append(stubs, &store.Resource{
				Provider:       "azure",
				AccountID:      other,
				Type:           TypeForeignSubscription,
				NativeID:       nativeID,
				Name:           &name,
				Region:         regionGlobal,
				AttributesJSON: fmt.Sprintf(`{"subscriptionId":%q,"synthetic":true}`, other),
				DiscoveredBy:   scanID,
			})
		}
		if _, err := st.UpsertResources(stubs); err != nil {
			return fmt.Errorf("upsert foreign-subscription stubs: %w", err)
		}
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

		// Edge → role definition (FK: same-sub role-definition with matching NativeID).
		if attrs.Properties.RoleDefinitionID != nil {
			defResourceID := store.ResourceID("azure", sub.ID, TypeAuthorizationRoleDefinition, *attrs.Properties.RoleDefinitionID)
			if _, err := st.GetResource(defResourceID); err == nil {
				if err := st.UpsertRelationship(r.ID, defResourceID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert role-assignment→role-definition: %w", err)
				}
			}
		}

		if attrs.Properties.Scope == nil {
			continue
		}
		scope := *attrs.Properties.Scope

		// Cross-sub edge: scope sub differs from assignment's owner sub. Edge
		// targets foreign-subscription stub. Same-sub assignments fall through
		// to the per-resource scope match below.
		if other, ok := subscriptionFromScope(scope); ok && !strings.EqualFold(other, sub.ID) {
			toID := store.ResourceID("azure", other, TypeForeignSubscription, "/subscriptions/"+other)
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
