package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeResourcesResourceGroup, Service: "resources"})
	// scanResourceGroups runs once per subscription, invoked directly from
	// azure.go (not via registerService) since it pre-seeds RG parents every
	// other scanner depends on. Emits declared via registerExtraEmits.
}

// scanResourceGroups discovers all resource groups in a subscription and
// upserts them as parent resources. All other Azure resources use the RG's
// disco ID as their parent_id.
//
// listed reports whether ARM answered the list call, which is not the same as
// err == nil in either direction: a refusal is absorbed into a ScanWarning and
// returns a NIL error, while a store write that fails after a page came back
// returns an error with listed already TRUE. The caller cannot otherwise tell
// "no groups" from "not allowed to look", and scanSubscription needs the
// distinction to decide whether a 401 on the separate RP-registration probe
// means the whole subscription is unreachable. A successful EMPTY list still
// sets it -- the point is that the token was accepted for this subscription,
// not that anything was found.
func scanResourceGroups(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (listed bool, err error) {
	client, err := armresources.NewResourceGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return false, fmt.Errorf("armresources:NewResourceGroupsClient: %w", err)
	}
	return resourceGroupsFromPager(ctx, sub, st, scanID, client.NewListPager(nil))
}

// resourceGroupsFromPager drains an already-built resource-group pager. Split
// from [scanResourceGroups] so a test can drive it through the SDK fake and
// pin `listed` on every path, which is the value scanSubscription's
// unreachable-subscription gate reads. Same split, same reason, as
// registeredProvidersFromPager.
func resourceGroupsFromPager(ctx context.Context, sub *subscription, st *store.Store, scanID string, pager *runtime.Pager[armresources.ResourceGroupsClientListResponse]) (listed bool, err error) {
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return listed, skipIfAccessDenied(st, "armresources:ResourceGroups.List", sub.ID, err)
			}
			return listed, fmt.Errorf("armresources:ResourceGroups.List: %w", err)
		}
		// Set the moment ARM answers a page, BEFORE any store write. The
		// caller's question is whether the TOKEN was accepted, so a database
		// failure below must not be reported as one that was not: it would let
		// a concurrent 401 on the providers endpoint refuse the whole
		// subscription, and report the account's access as denied because our
		// write failed.
		listed = true
		var batch []*store.Resource
		for _, rg := range page.Value {
			if rg.ID == nil {
				continue
			}
			name := sv(rg.Name)
			location := sv(rg.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeResourcesResourceGroup,
				NativeID:       sv(rg.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(rg),
				DiscoveredBy:   scanID,
			}
			r.TagsJSON = azTagsJSON(rg.Tags)
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if _, err := st.UpsertResources(batch); err != nil {
				return listed, fmt.Errorf("upsert resource groups: %w", err)
			}
			// Seed each RG's closure self-entry now so every RG is queryable even
			// without tenant-level management access. The RG → subscription link
			// is wired later by stitchTopHierarchy, once the subscription-as-
			// resource row exists.
			pairs := make([][2]string, len(batch))
			for i, r := range batch {
				rgID := store.ResourceID("azure", sub.ID, r.NativeID)
				pairs[i] = [2]string{rgID, rgID}
			}
			if err := st.RecordHierarchyBatch(pairs); err != nil {
				return listed, fmt.Errorf("closure resource groups: %w", err)
			}
		}
	}
	return listed, nil
}
