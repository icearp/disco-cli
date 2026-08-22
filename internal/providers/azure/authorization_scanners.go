package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armpolicy"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAuthorizationRoleAssignment, Service: "microsoft.authorization"})
	registerType(restype.Descriptor{Type: TypeAuthorizationRoleDefinition, Service: "microsoft.authorization"})
	registerType(restype.Descriptor{Type: TypeAuthorizationRoleDefinition, Service: "microsoft.authorization"})
	registerType(restype.Descriptor{Type: TypePolicyDefinition, Service: "microsoft.authorization"})
	registerType(restype.Descriptor{Type: TypePolicySetDefinition, Service: "microsoft.authorization"})
	registerService(serviceEntry{
		name: "azure:microsoft.authorization",
		fn:   scanAuthorizationNamespace,
	})
	// Tenant phase: built-in role/policy/set defs are Microsoft-shipped and
	// identical across every subscription. Fetched once per scan and stored
	// under the tenant account, so an N-subscription scan keeps one copy
	// instead of N. Per-sub scanners skip built-ins when a tenant GUID is
	// available (see scanAuthorization / scanPolicy); the resolvers FK across
	// the account boundary via the normalized role key / tenant policy-def
	// merge. Shares the "azure:microsoft.authorization" name with the per-sub
	// service — CollectEmits dedupes by DiscoType, and --services selects both
	// phases.
	registerTenantService(tenantServiceEntry{
		name:      "azure:microsoft.authorization",
		fn:        scanAuthorizationBuiltins,
		dedupOnly: true,
	})
}

// scanAuthorization discovers Azure RBAC role assignments and role definitions
// scoped to (or visible from) the subscription. Built-in role definitions are
// tenant-scoped but returned by the subscription-scoped list call; they are
// stored under each subscription's account_id (acceptable duplication — the
// ResourceID hash differs, so per-sub resolvers still FK-match locally).
func scanAuthorization(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	// Role definitions first so the resolver can FK from assignments.
	defClient, err := armauthorization.NewRoleDefinitionsClient(cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armauthorization:NewRoleDefinitionsClient: %w", err)
	}
	dt, di, err := scanRoleDefinitionsInto(ctx, sub, st, scanID, defClient)
	total += dt
	inserted += di
	if err != nil {
		return total, inserted, err
	}

	// Role assignments.
	asnClient, err := armauthorization.NewRoleAssignmentsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armauthorization:NewRoleAssignmentsClient: %w", err)
	}
	at, ai, err := azPageScan(ctx, "armauthorization:RoleAssignments.ListForSubscription", sub, st,
		asnClient.NewListForSubscriptionPager(nil),
		func(page armauthorization.RoleAssignmentsClientListForSubscriptionResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, ra := range page.Value {
				if ra == nil || ra.ID == nil {
					continue
				}
				name := sv(ra.Name)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeAuthorizationRoleAssignment, NativeID: sv(ra.ID),
					Name:           &name,
					AttributesJSON: mustJSON(ra),
					DiscoveredBy:   scanID,
				})
			}
			return batch, nil
		})
	total += at
	inserted += ai
	return total, inserted, err
}

// scanRoleDefinitionsInto lists role definitions visible from the subscription
// and stores them under sub.ID. When a tenant GUID is set it SKIPS built-in
// roles — those are deduplicated under the tenant account by
// scanAuthorizationBuiltins — so only custom roles persist per-sub. Testable
// core (takes a pre-built client).
func scanRoleDefinitionsInto(ctx context.Context, sub *subscription, st *store.Store, scanID string, client *armauthorization.RoleDefinitionsClient) (total, inserted int, err error) {
	subScope := "/subscriptions/" + sub.ID
	return azPageScan(ctx, "armauthorization:RoleDefinitions.List", sub, st,
		client.NewListPager(subScope, nil),
		func(page armauthorization.RoleDefinitionsClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, rd := range page.Value {
				if rd == nil || rd.ID == nil {
					continue
				}
				name := sv(rd.Name)
				if rd.Properties != nil && rd.Properties.RoleName != nil {
					name = *rd.Properties.RoleName
				}
				managed := rd.Properties != nil && rd.Properties.RoleType != nil &&
					*rd.Properties.RoleType == "BuiltInRole"
				// Built-ins are deduplicated under the tenant account by
				// scanAuthorizationBuiltins; skip them here when a tenant GUID is
				// available. Custom roles always stay per-sub.
				if managed && sub.tenantID != "" {
					continue
				}
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeAuthorizationRoleDefinition, NativeID: sv(rd.ID),
					Name:              &name,
					AttributesJSON:    mustJSON(rd),
					DiscoveredBy:      scanID,
					ManagedByProvider: managed,
				})
			}
			return batch, nil
		})
}

// scanAuthorizationNamespace runs every Microsoft.authorization scanner phase concurrently. The
// authorization ARM namespace spans several disco scanners merged under one
// serviceEntry so the service name aligns to the namespace.
func scanAuthorizationNamespace(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) { return scanAuthorization(ctx, sub, cred, st, scanID) },
		func() (int, int, error) { return scanPolicy(ctx, sub, cred, st, scanID) },
	)
}

// scanAuthorizationBuiltins fetches the tenant-identical built-in definitions
// (role, policy, policy-set) once per scan and stores them under the tenant GUID.
// No-ops when the tenant GUID is unavailable — the per-sub scanners then keep
// storing built-ins under each subscription (current behavior, no data loss).
func scanAuthorizationBuiltins(ctx context.Context, subs []subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(subs) == 0 || subs[0].tenantID == "" {
		return 0, 0, nil
	}
	tenantID := subs[0].tenantID
	add := func(t, i int, e error) {
		total += t
		inserted += i
		if e != nil && err == nil {
			err = e
		}
	}

	// Built-in role definitions: the list API is scope-bound, so try subscriptions
	// in order until one returns built-ins (AccessDenied subs yield zero rows via
	// azPageScan's tolerance; provider-unavailable subs yield a skippable error).
	// One authorized subscription suffices — built-ins are identical tenant-wide.
	// Only the successful sub's counts are kept (no double-count on retry), and a
	// per-sub error is surfaced only when NO subscription yielded built-ins.
	defClient, derr := armauthorization.NewRoleDefinitionsClient(cred, azClientOptions)
	if derr != nil {
		return total, inserted, fmt.Errorf("armauthorization:NewRoleDefinitionsClient: %w", derr)
	}
	var roleTotal, roleIns int
	var roleErr error
	for i := range subs {
		rt, ri, rerr := scanBuiltinRoleDefsInto(ctx, "/subscriptions/"+subs[i].ID, tenantID, st, scanID, defClient)
		if rerr == nil && rt > 0 {
			roleTotal, roleIns, roleErr = rt, ri, nil
			break
		}
		if rerr != nil {
			roleErr = rerr
		}
	}
	total += roleTotal
	inserted += roleIns
	if roleTotal == 0 && roleErr != nil {
		err = roleErr
	}

	// Built-in policy + set definitions: tenant-level list endpoints (no sub
	// scope needed); the client ctor still wants a subscription ID.
	polClient, perr := armpolicy.NewDefinitionsClient(subs[0].ID, cred, azClientOptions)
	if perr != nil {
		return total, inserted, fmt.Errorf("armpolicy:NewDefinitionsClient: %w", perr)
	}
	add(scanBuiltinPolicyDefsInto(ctx, tenantID, st, scanID, polClient))

	setClient, serr := armpolicy.NewSetDefinitionsClient(subs[0].ID, cred, azClientOptions)
	if serr != nil {
		return total, inserted, fmt.Errorf("armpolicy:NewSetDefinitionsClient: %w", serr)
	}
	add(scanBuiltinPolicySetDefsInto(ctx, tenantID, st, scanID, setClient))

	return total, inserted, err
}

// scanBuiltinRoleDefsInto lists built-in role definitions at subScope and stores
// them under accountID with a scope-free NativeID (roleDefSuffix), so the
// resolver FKs assignments to the single tenant copy regardless of each
// assignment's scope prefix. Testable core (takes a pre-built client).
func scanBuiltinRoleDefsInto(ctx context.Context, subScope, accountID string, st *store.Store, scanID string, client *armauthorization.RoleDefinitionsClient) (total, inserted int, err error) {
	scopeRef := &subscription{ID: accountID, Name: "tenant"}
	filter := "type eq 'BuiltInRole'"
	return azPageScan(ctx, "armauthorization:RoleDefinitions.ListBuiltIn", scopeRef, st,
		client.NewListPager(subScope, &armauthorization.RoleDefinitionsClientListOptions{Filter: &filter}),
		func(page armauthorization.RoleDefinitionsClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, rd := range page.Value {
				if rd == nil || rd.ID == nil {
					continue
				}
				// Defensive: the $filter should already exclude custom roles, but
				// never let one leak into the tenant-scope store.
				if rd.Properties == nil || rd.Properties.RoleType == nil || *rd.Properties.RoleType != "BuiltInRole" {
					continue
				}
				name := sv(rd.Name)
				if rd.Properties.RoleName != nil {
					name = *rd.Properties.RoleName
				}
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: accountID,
					Type: TypeAuthorizationRoleDefinition, NativeID: roleDefSuffix(sv(rd.ID)),
					Name:              &name,
					Region:            regionGlobal,
					AttributesJSON:    mustJSON(rd),
					DiscoveredBy:      scanID,
					ManagedByProvider: true,
				})
			}
			return batch, nil
		})
}

// scanBuiltinPolicyDefsInto lists built-in policy definitions (tenant-level) and
// stores them under accountID. Their ARM IDs are already scope-free, so the
// NativeID is stored verbatim. Testable core.
func scanBuiltinPolicyDefsInto(ctx context.Context, accountID string, st *store.Store, scanID string, client *armpolicy.DefinitionsClient) (total, inserted int, err error) {
	scopeRef := &subscription{ID: accountID, Name: "tenant"}
	return azPageScan(ctx, "armpolicy:Definitions.ListBuiltIn", scopeRef, st,
		client.NewListBuiltInPager(nil),
		func(page armpolicy.DefinitionsClientListBuiltInResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, d := range page.Value {
				if d == nil || d.ID == nil {
					continue
				}
				name := sv(d.Name)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: accountID,
					Type: TypePolicyDefinition, NativeID: sv(d.ID),
					Name:              &name,
					Region:            regionGlobal,
					AttributesJSON:    mustJSON(d),
					DiscoveredBy:      scanID,
					ManagedByProvider: true,
				})
			}
			return batch, nil
		})
}

// scanBuiltinPolicySetDefsInto lists built-in policy set definitions
// (tenant-level) and stores them under accountID. Testable core.
func scanBuiltinPolicySetDefsInto(ctx context.Context, accountID string, st *store.Store, scanID string, client *armpolicy.SetDefinitionsClient) (total, inserted int, err error) {
	scopeRef := &subscription{ID: accountID, Name: "tenant"}
	return azPageScan(ctx, "armpolicy:SetDefinitions.ListBuiltIn", scopeRef, st,
		client.NewListBuiltInPager(nil),
		func(page armpolicy.SetDefinitionsClientListBuiltInResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, d := range page.Value {
				if d == nil || d.ID == nil {
					continue
				}
				name := sv(d.Name)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: accountID,
					Type: TypePolicySetDefinition, NativeID: sv(d.ID),
					Name:              &name,
					Region:            regionGlobal,
					AttributesJSON:    mustJSON(d),
					DiscoveredBy:      scanID,
					ManagedByProvider: true,
				})
			}
			return batch, nil
		})
}
