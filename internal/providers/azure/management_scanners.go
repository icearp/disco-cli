package azure

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/managementgroups/armmanagementgroups"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription"
)

func init() {
	registerTenantService(tenantServiceEntry{
		name: "azure:microsoft.management",
		fn:   scanManagementTenant,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.management", DiscoType: TypeManagementGroup},
		},
	})
	registerService(serviceEntry{
		name: "azure:microsoft.resources",
		fn:   scanSubscriptionResource,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.resources", DiscoType: TypeSubscription},
		},
	})
}

// scanManagementTenant discovers Azure Management Groups, which are a
// tenant-level construct (the API list is tenant-wide, not subscription-scoped).
// It runs ONCE per scan as a tenant service and stores each MG under the tenant
// GUID, so a multi-subscription scan keeps a single copy of the MG tree rather
// than one per subscription. When the tenant GUID could not be resolved it falls
// back to the first subscription's ID — still a single deduplicated copy.
func scanManagementTenant(ctx context.Context, subs []subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(subs) == 0 {
		return 0, 0, nil
	}
	accountID := subs[0].tenantID
	if accountID == "" {
		accountID = subs[0].ID
	}
	mgClient, err := armmanagementgroups.NewClient(cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagementgroups:NewClient: %w", err)
	}
	return scanManagementInto(ctx, accountID, st, scanID, mgClient)
}

// scanManagementInto is the testable core: it pages the (tenant-wide) management
// group list from the supplied client and upserts each MG under accountID. Split
// from scanManagementTenant so tests can drive it with a fake-transport client.
func scanManagementInto(ctx context.Context, accountID string, st *store.Store, scanID string, client *armmanagementgroups.Client) (total, inserted int, err error) {
	// scopeRef satisfies azPageScan's *subscription parameter (used only for the
	// AccessDenied scope label, never for the stored AccountID).
	scopeRef := &subscription{ID: accountID, Name: "tenant"}
	return azPageScan(ctx, "armmanagementgroups:List", scopeRef, st,
		client.NewListPager(nil),
		func(page armmanagementgroups.ClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, mg := range page.Value {
				if mg == nil || mg.ID == nil {
					continue
				}
				name := sv(mg.Name)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: accountID,
					Type: TypeManagementGroup, NativeID: sv(mg.ID),
					Name:           &name,
					Region:         regionGlobal,
					AttributesJSON: mustJSON(mg),
					DiscoveredBy:   scanID,
				})
			}
			return batch, nil
		})
}

// stitchTopHierarchy links the three top tiers of the Azure hierarchy that no
// single per-subscription scanner can wire, because each tier is stored by a
// different phase under a different account:
//
//	management-group -[contains]-> child management-group   (tenant phase)
//	management-group -[contains]-> subscription             (tenant + per-sub)
//	subscription     -[contains]-> resource-group           (per-sub)
//
// It runs ONCE after the whole scan's phase-1 (every subscription's resource
// groups + the subscription-as-resource rows, and the tenant phase's management
// groups) so RecordHierarchyBatch sees both endpoints in `resources` and emits
// the graph-visible `contains` relationship row rather than gating it out.
//
// Closure targets are looked up in store-built NativeID→ResourceID indexes
// instead of recomputed via store.ResourceID, so a casing difference between the
// Entities API and the flat management-group list can never desync the hash.
// The RG→subscription tier is pure store data (no API), so it links even when
// the caller lacks tenant-level Microsoft.Management read; only the
// management-group tiers need the Entities call, whose AccessDenied is tolerated.
func stitchTopHierarchy(ctx context.Context, subs []subscription, cred azcore.TokenCredential, st *store.Store) {
	if len(subs) == 0 {
		return
	}
	subIndex, err := storeNativeIDIndex(st, TypeSubscription)
	if err != nil {
		st.ReportError(store.ScanError{
			Provider: "azure", Service: "azure:microsoft.management", Scope: "tenant",
			Message: "index subscriptions for hierarchy: " + formatAzureError(err),
		})
		return
	}

	// Tier 3: resource-group -[contains]-> subscription (pure store data).
	pairs, err := resourceGroupParentPairs(st, subIndex)
	if err != nil {
		st.ReportError(store.ScanError{
			Provider: "azure", Service: "azure:microsoft.management", Scope: "tenant",
			Message: "list resource groups for hierarchy: " + formatAzureError(err),
		})
		return
	}

	// Tiers 1+2: management-group / subscription -[contains]-> parent
	// management-group, sourced from the tenant-wide Entities list (the flat
	// management-group list carries no parent linkage). AccessDenied self-reports
	// via skipIfAccessDenied and degrades to the RG tier already collected above;
	// any other failure (index build, transient ARM error) is reported so the
	// missing tiers don't vanish silently.
	mgIndex, merr := storeNativeIDIndex(st, TypeManagementGroup)
	switch {
	case merr != nil:
		st.ReportError(store.ScanError{
			Provider: "azure", Service: "azure:microsoft.management", Scope: "tenant",
			Message: "index management groups for hierarchy: " + formatAzureError(merr),
		})
	default:
		mgPairs, eerr := managementParentPairs(ctx, cred, subs[0], mgIndex, subIndex, st)
		if eerr != nil {
			st.ReportError(store.ScanError{
				Provider: "azure", Service: "azure:microsoft.management", Scope: "tenant",
				Message: "management-group entities for hierarchy: " + formatAzureError(eerr),
			})
		}
		pairs = append(pairs, mgPairs...)
	}

	if len(pairs) == 0 {
		return
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		st.ReportError(store.ScanError{
			Provider: "azure", Service: "azure:microsoft.management", Scope: "tenant",
			Message: "record top-tier hierarchy: " + formatAzureError(err),
		})
	}
}

// resourceGroupParentPairs returns the resource-group -[contains]-> subscription
// closure pairs. A resource group's AccountID is its owning subscription GUID;
// its parent subscription resource is keyed by the canonical
// "/subscriptions/{guid}" NativeID. Resource groups whose subscription was not
// scanned as a resource are skipped (no dangling parent).
func resourceGroupParentPairs(st *store.Store, subIndex map[string]string) ([][2]string, error) {
	rgs, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", Types: []string{TypeResourcesResourceGroup}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	var pairs [][2]string
	for _, rg := range rgs {
		if parentID, ok := subIndex[strings.ToLower("/subscriptions/"+rg.AccountID)]; ok {
			pairs = append(pairs, [2]string{rg.ID, parentID})
		}
	}
	return pairs, nil
}

// managementParentPairs pages the tenant-wide Entities list and returns the
// child→parent closure pairs for management groups and subscriptions whose
// parent management group is in store. Entity IDs are matched against the
// store-built indexes (lowercased) so out-of-scope subscriptions and unscanned
// management groups are skipped rather than emitting dangling edges.
func managementParentPairs(ctx context.Context, cred azcore.TokenCredential, scopeRef subscription, mgIndex, subIndex map[string]string, st *store.Store) ([][2]string, error) {
	client, err := armmanagementgroups.NewEntitiesClient(cred, azClientOptions)
	if err != nil {
		return nil, fmt.Errorf("armmanagementgroups:NewEntitiesClient: %w", err)
	}
	return managementParentPairsWithClient(ctx, client, scopeRef, mgIndex, subIndex, st)
}

// managementParentPairsWithClient is the testable core of managementParentPairs,
// driven by a supplied Entities client so tests can feed a fake transport.
func managementParentPairsWithClient(ctx context.Context, client *armmanagementgroups.EntitiesClient, scopeRef subscription, mgIndex, subIndex map[string]string, st *store.Store) ([][2]string, error) {
	var pairs [][2]string
	pager := client.NewListPager(nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return pairs, skipIfAccessDenied(st, "armmanagementgroups:Entities.List", scopeRef.ID, perr)
			}
			return pairs, fmt.Errorf("armmanagementgroups:Entities.List: %w", perr)
		}
		for _, e := range page.Value {
			if e == nil || e.ID == nil || e.Properties == nil || e.Properties.Parent == nil || e.Properties.Parent.ID == nil {
				continue
			}
			parentID, ok := mgIndex[strings.ToLower(*e.Properties.Parent.ID)]
			if !ok {
				continue
			}
			// An entity is either a management group or a subscription; match it
			// against whichever index holds it.
			childID, ok := mgIndex[strings.ToLower(*e.ID)]
			if !ok {
				childID, ok = subIndex[strings.ToLower(*e.ID)]
			}
			if ok && childID != parentID {
				pairs = append(pairs, [2]string{childID, parentID})
			}
		}
	}
	return pairs, nil
}

// storeNativeIDIndex builds a lowercased NativeID→ResourceID index over every
// stored Azure resource of one type, across all accounts (management groups live
// under the tenant account, subscriptions under their own GUID). Distinct from
// the per-subscription nativeIDIndex helper, which scopes to one AccountID.
func storeNativeIDIndex(st *store.Store, rtype string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", Types: []string{rtype}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		idx[strings.ToLower(r.NativeID)] = r.ID
	}
	return idx, nil
}

// scanSubscriptionResource discovers the subscription itself as a first-class
// resource (Microsoft.Resources/subscriptions). Tenant-scoped API run
// per-subscription (duplication accepted as above).
//
// Recording subscription-as-resource closes a gap for scope-attached
// resolvers: policy assignments scoped to `/subscriptions/<id>` could not
// previously FK to a stored resource; they now will.
func scanSubscriptionResource(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	subClient, err := armsubscription.NewSubscriptionsClient(cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsubscription:NewSubscriptionsClient: %w", err)
	}
	return azPageScan(ctx, "armsubscription:Subscriptions.List", sub, st,
		subClient.NewListPager(nil),
		func(page armsubscription.SubscriptionsClientListResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, s := range page.Value {
				if s == nil || s.ID == nil {
					continue
				}
				name := sv(s.DisplayName)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeSubscription, NativeID: sv(s.ID),
					Name:           &name,
					AttributesJSON: mustJSON(s),
					DiscoveredBy:   scanID,
				})
			}
			return batch, nil
		})
}
