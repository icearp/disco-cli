package azure

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/managementgroups/armmanagementgroups"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeManagementGroup, Service: "microsoft.management"})
	registerType(restype.Descriptor{Type: TypeSubscription, Service: "microsoft.resources"})
	registerTenantService(tenantServiceEntry{
		name: "azure:microsoft.management",
		fn:   scanManagementTenant,
	})
	registerService(serviceEntry{
		name: "azure:microsoft.resources",
		fn:   scanSubscriptionResource,
	})
}

// scanManagementTenant discovers Azure Management Groups, a tenant-level
// construct (the API list is tenant-wide, not subscription-scoped). Runs ONCE
// per scan as a tenant service, storing each MG under the tenant GUID so a
// multi-subscription scan keeps one copy of the MG tree, not one per
// subscription. Falls back to the first subscription's ID when the tenant GUID
// could not be resolved: still a single deduplicated copy, but one MISLABELLED
// as that subscription's, since the list is tenant-wide. Tracked as
// icearp/disco-cli#12, where the expected fix is to skip and report rather than
// fall back. Unreachable under federation — tenantServiceRunnable refuses every
// non-graphScoped tenant service when wifConfig.configured() — so it is the
// standalone-CLI path that carries it.
func scanManagementTenant(ctx context.Context, subs []subscription, cred azcore.TokenCredential, _ wifConfig, st *store.Store, scanID string) (total, inserted int, err error) {
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

// scanManagementInto is the testable core: pages the tenant-wide management
// group list from the supplied client and upserts each MG under accountID.
// Split from scanManagementTenant so tests can drive it with a fake-transport
// client.
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
// Runs ONCE after phase-1 completes (every subscription's RG rows + the
// subscription-as-resource rows, and the tenant phase's MG rows) so
// RecordHierarchyBatch sees both endpoints in `resources` and emits the
// graph-visible `contains` row instead of gating it out.
//
// Closure targets are looked up via store-built NativeID→ResourceID indexes,
// not recomputed via store.ResourceID, so a casing difference between the
// Entities API and the flat MG list can never desync the hash. The
// RG→subscription tier is pure store data (no API), so it links even when the
// caller lacks tenant-level Microsoft.Management read; only the MG tiers need
// the Entities call, whose AccessDenied is tolerated.
//
// Takes the federation contract rather than a boolean: the caller cannot then
// hand this function a decision that disagrees with the rest of the scan, and
// there is no argument to get backwards. Under federation the Entities call
// alone is suppressed (see wifConfig.tenantScopeEnabled) — it is tenant-wide,
// so it answers for the credential's directory rather than the scanned
// subscription's — while the store-derived tiers still link, because they
// read rows this scan already wrote.
func stitchTopHierarchy(ctx context.Context, subs []subscription, cred azcore.TokenCredential, st *store.Store, wif wifConfig) {
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
	mgIndex, err := storeNativeIDIndex(st, TypeManagementGroup)
	if err != nil {
		st.ReportError(store.ScanError{
			Provider: "azure", Service: "azure:microsoft.management", Scope: "tenant",
			Message: "index management groups for hierarchy: " + formatAzureError(err),
		})
		return
	}

	// Tiers 1+2: MG / subscription -[contains]-> parent MG, from the tenant-wide
	// Entities list (the flat MG list carries no parent linkage), shallowest-first
	// so each parent's chain exists before its children record. AccessDenied
	// self-reports via skipIfAccessDenied and degrades to the self-seeds + RG
	// tier; any other failure is reported so the missing tiers don't vanish
	// silently. Under federation the call is skipped for the same reason it
	// tolerates AccessDenied — tenant-wide, so it enumerates the CREDENTIAL's
	// management-group tree, which is not confirmably the scanned tenant's —
	// and degrades the same way.
	var mgPairs [][2]string
	if wif.tenantScopeEnabled() {
		var eerr error
		mgPairs, eerr = managementParentPairs(ctx, cred, subs[0], mgIndex, subIndex, st)
		if eerr != nil {
			st.ReportError(store.ScanError{
				Provider: "azure", Service: "azure:microsoft.management", Scope: "tenant",
				Message: "management-group entities for hierarchy: " + formatAzureError(eerr),
			})
		}
	}
	recordTopHierarchy(st, mgIndex, subIndex, mgPairs)
}

// recordTopHierarchy assembles and records the top-tier closure in the one order
// that lets every transitive ancestor row materialise, then writes it in a single
// RecordHierarchyBatch. Split from stitchTopHierarchy so tests drive it without a
// live Entities client:
//  1. Self-seed every MG and subscription. RecordHierarchyBatch
//     builds a child's depth-N+1 rows by joining its parent's existing closure
//     rows, so a node that is only ever a parent (the tenant root group) must be
//     seeded or its children's chains never form. Seeding from the store indexes
//     (not the Entities API) keeps the RG→sub tier intact even when the tenant
//     Entities read is denied. Mirrors GCP's RecordHierarchy(org, org) root seed.
//  2. The depth-ordered MG/subscription child→parent pairs.
//  3. Resource-group → subscription last — the subscription chains it hangs from
//     are built by step 2.
func recordTopHierarchy(st *store.Store, mgIndex, subIndex map[string]string, mgPairs [][2]string) {
	pairs := make([][2]string, 0, len(mgIndex)+len(subIndex)+len(mgPairs))
	for _, id := range mgIndex {
		pairs = append(pairs, [2]string{id, id})
	}
	for _, id := range subIndex {
		pairs = append(pairs, [2]string{id, id})
	}
	pairs = append(pairs, mgPairs...)

	rgPairs, err := resourceGroupParentPairs(st, subIndex)
	if err != nil {
		st.ReportError(store.ScanError{
			Provider: "azure", Service: "azure:microsoft.management", Scope: "tenant",
			Message: "list resource groups for hierarchy: " + formatAzureError(err),
		})
		return
	}
	pairs = append(pairs, rgPairs...)

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
		Providers: []string{"azure"}, Types: []string{TypeResourcesResourceGroup}, Limit: util.AllResources,
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
// driven by a supplied Entities client so tests can feed a fake transport. The
// returned child→parent pairs are ordered shallowest-first (by the entity's
// parent-name-chain length) so RecordHierarchyBatch records a parent before its
// children and every transitive ancestor row materialises.
func managementParentPairsWithClient(ctx context.Context, client *armmanagementgroups.EntitiesClient, scopeRef subscription, mgIndex, subIndex map[string]string, st *store.Store) ([][2]string, error) {
	type childParent struct {
		pair  [2]string
		depth int
	}
	var nodes []childParent
	pager := client.NewListPager(nil)
	for pager.More() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return nil, skipIfAccessDenied(st, "armmanagementgroups:Entities.List", scopeRef.ID, perr)
			}
			return nil, fmt.Errorf("armmanagementgroups:Entities.List: %w", perr)
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
				nodes = append(nodes, childParent{
					pair: [2]string{childID, parentID}, depth: len(e.Properties.ParentNameChain),
				})
			}
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].depth < nodes[j].depth })
	pairs := make([][2]string, len(nodes))
	for i, n := range nodes {
		pairs[i] = n.pair
	}
	return pairs, nil
}

// storeNativeIDIndex builds a lowercased NativeID→ResourceID index over every
// stored Azure resource of one type, across all accounts (management groups live
// under the tenant account, subscriptions under their own GUID). Distinct from
// the per-subscription nativeIDIndex helper, which scopes to one AccountID.
func storeNativeIDIndex(st *store.Store, rtype string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, Types: []string{rtype}, Limit: util.AllResources,
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
// resource (Microsoft.Resources/subscriptions).
//
// The list call is tenant-wide — armsubscription's NewListPager is
// GET /subscriptions, "gets all subscriptions for a tenant" — so the page is
// filtered to the subscription being scanned before anything is stored. Under
// a Lighthouse managing identity that page carries every subscription
// delegated by every customer, and the unfiltered loop wrote each of them
// under the CURRENT customer's AccountID: customer A's inventory would gain
// customer B's subscription ids and display names. Filtering also drops the
// N-squared write this had for a multi-subscription scan.
//
// A pin that never appears in the page is warned about rather than reported as
// an empty success — see the comment at the !pager.More() check.
//
// Recording subscription-as-resource closes a gap for scope-attached
// resolvers: policy assignments scoped to `/subscriptions/<id>` could not
// previously FK to a stored resource; they now will.
func scanSubscriptionResource(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	subClient, err := armsubscription.NewSubscriptionsClient(cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsubscription:NewSubscriptionsClient: %w", err)
	}
	return scanSubscriptionResourceWithPager(ctx, sub, st, scanID, subClient.NewListPager(nil))
}

// scanSubscriptionResourceWithPager is the pager-consuming half, split out so
// the miss case can be exercised without ARM. See the outer function.
func scanSubscriptionResourceWithPager(ctx context.Context, sub *subscription, st *store.Store, scanID string, pager azPager[armsubscription.SubscriptionsClientListResponse]) (total, inserted int, err error) {
	// Track whether the pin appeared at all. Without this a subscription the
	// credential cannot see — a revoked delegation, a mistyped GUID — filters
	// to nothing on every page and azPageScan returns (0, 0, nil), which the
	// dispatcher reports as ServiceOK: indistinguishable from success.
	seen := false
	total, inserted, err = azPageScan(ctx, "armsubscription:Subscriptions.List", sub, st,
		pager,
		func(page armsubscription.SubscriptionsClientListResponse) ([]*store.Resource, [][2]string) {
			batch := subscriptionResourceBatch(page.Value, sub, scanID)
			seen = seen || len(batch) > 0
			return batch, nil
		})
	// The pager must have DRAINED, and a page tally does not show that: an
	// access-denied part-way through pagination leaves azPageScan answering nil
	// — after reporting its own warning — with pages already read, so counting
	// pages blames a revoked delegation for a permission failure reported one
	// line earlier with a different cause. runtime.Pager only advances its
	// cursor on a successful fetch, so More() stays true after any failure and
	// goes false only when the listing genuinely ended.
	if err == nil && !pager.More() && !seen {
		st.ReportWarning(store.ScanWarning{
			Provider: "azure", Service: "azure:microsoft.resources", Scope: sub.scopeLabel(),
			Message: "subscription not present in the tenant subscription list: it may not exist, or its delegation to this identity may have been revoked",
		})
	}
	return total, inserted, err
}

// subscriptionGUID is the entry's subscription GUID, falling back to the tail
// of its ARM id.
//
// The fallback is not defensive noise: subscriptionResourceBatch DROPS what it
// cannot match, and scanSubscriptionResourceWithPager then reports a dropped
// pin as a possibly-revoked delegation — so a nil SubscriptionID on the very
// entry being scanned would produce a confident, wrong diagnosis.
func subscriptionGUID(s *armsubscription.Subscription) string {
	if id := sv(s.SubscriptionID); id != "" {
		return id
	}
	return nameFromID(sv(s.ID))
}

// subscriptionResourceBatch maps one page of the tenant-wide subscription list
// down to the single subscription being scanned.
//
// The pager is GET /subscriptions, which answers for every subscription the
// credential can see. Under Azure Lighthouse that is every delegation the
// managing tenant holds — i.e. OTHER CUSTOMERS. Recording the page unfiltered
// writes their subscription ids and display names under this customer's
// AccountID, so the filter is a disclosure boundary, not a tidiness measure.
func subscriptionResourceBatch(page []*armsubscription.Subscription, sub *subscription, scanID string) []*store.Resource {
	var batch []*store.Resource
	for _, s := range page {
		if s == nil || s.ID == nil {
			continue
		}
		if !strings.EqualFold(subscriptionGUID(s), sub.ID) {
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
	return batch
}
