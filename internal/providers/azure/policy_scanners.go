package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armpolicy"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypePolicyDefinition, Service: "microsoft.authorization"})
	registerType(restype.Descriptor{Type: TypePolicySetDefinition, Service: "microsoft.authorization"})
	registerType(restype.Descriptor{Type: TypePolicyAssignment, Service: "microsoft.authorization"})
}

// scanPolicy discovers Azure Policy definitions, set definitions, and
// assignments. Mirrors the RBAC pattern (definitions then assignments) so
// resolvers can FK assignments to definitions locally. Built-ins are returned
// alongside customer-authored definitions and stored under each subscription's
// account_id (acceptable duplication — same as RBAC role-definitions).
// Exemptions (`armpolicy.ExemptionsClient`) deferred — narrow customer base.
func scanPolicy(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	defClient, err := armpolicy.NewDefinitionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armpolicy:NewDefinitionsClient: %w", err)
	}
	dt, di, err := azPageScan(ctx, "armpolicy:Definitions.List", sub, st,
		defClient.NewListPager(nil),
		func(page armpolicy.DefinitionsClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, d := range page.Value {
				if d == nil || d.ID == nil {
					continue
				}
				if sub.tenantID != "" && d.Properties != nil && isTenantDedupedPolicyType(d.Properties.PolicyType) {
					continue // built-ins deduplicated under the tenant account
				}
				name := sv(d.Name)
				var managed bool
				if d.Properties != nil {
					managed = isManagedPolicyType(d.Properties.PolicyType)
				}
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypePolicyDefinition, NativeID: sv(d.ID),
					Name:              &name,
					AttributesJSON:    mustJSON(d),
					DiscoveredBy:      scanID,
					ManagedByProvider: managed,
				})
			}
			return batch, nil
		})
	total += dt
	inserted += di
	if err != nil {
		return total, inserted, err
	}

	setClient, err := armpolicy.NewSetDefinitionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armpolicy:NewSetDefinitionsClient: %w", err)
	}
	st1, si1, err := azPageScan(ctx, "armpolicy:SetDefinitions.List", sub, st,
		setClient.NewListPager(nil),
		func(page armpolicy.SetDefinitionsClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, d := range page.Value {
				if d == nil || d.ID == nil {
					continue
				}
				if sub.tenantID != "" && d.Properties != nil && isTenantDedupedPolicyType(d.Properties.PolicyType) {
					continue // built-ins deduplicated under the tenant account
				}
				name := sv(d.Name)
				var managed bool
				if d.Properties != nil {
					managed = isManagedPolicyType(d.Properties.PolicyType)
				}
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypePolicySetDefinition, NativeID: sv(d.ID),
					Name:              &name,
					AttributesJSON:    mustJSON(d),
					DiscoveredBy:      scanID,
					ManagedByProvider: managed,
				})
			}
			return batch, nil
		})
	total += st1
	inserted += si1
	if err != nil {
		return total, inserted, err
	}

	asnClient, err := armpolicy.NewAssignmentsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return total, inserted, fmt.Errorf("armpolicy:NewAssignmentsClient: %w", err)
	}
	at, ai, err := azPageScan(ctx, "armpolicy:Assignments.List", sub, st,
		asnClient.NewListPager(nil),
		func(page armpolicy.AssignmentsClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, a := range page.Value {
				if a == nil || a.ID == nil {
					continue
				}
				name := sv(a.Name)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypePolicyAssignment, NativeID: sv(a.ID),
					Name:           &name,
					AttributesJSON: mustJSON(a),
					DiscoveredBy:   scanID,
				})
			}
			return batch, nil
		})
	total += at
	inserted += ai
	return total, inserted, err
}

// managedPolicyTypes is the PolicyType value set disco treats as
// provider-owned: BuiltIn (Microsoft-shipped), Static (Defender / system-
// emitted), and NotSpecified (undefined — hide conservatively, customer
// opts in via --include-managed). Custom is the only customer-authored
// kind, so it falls through unmanaged.
var managedPolicyTypes = map[armpolicy.PolicyType]bool{
	armpolicy.PolicyTypeBuiltIn:      true,
	armpolicy.PolicyTypeStatic:       true,
	armpolicy.PolicyTypeNotSpecified: true,
}

func isManagedPolicyType(t *armpolicy.PolicyType) bool {
	if t == nil {
		return false
	}
	return managedPolicyTypes[*t]
}

// isTenantDedupedPolicyType reports whether the policy type is one the tenant-
// level ListBuiltIn endpoint returns — empirically BuiltIn AND Static (Microsoft-
// shipped regulatory/Defender definitions, identical tenant-wide). The per-sub
// scanner skips exactly these when a tenant GUID is set, so they are stored once
// under the tenant rather than duplicated per subscription. NotSpecified is NOT
// returned by ListBuiltIn (and can be subscription-specific), so it stays per-sub
// to avoid data loss.
func isTenantDedupedPolicyType(t *armpolicy.PolicyType) bool {
	return t != nil && (*t == armpolicy.PolicyTypeBuiltIn || *t == armpolicy.PolicyTypeStatic)
}
