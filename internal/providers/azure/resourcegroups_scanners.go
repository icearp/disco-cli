package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
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
func scanResourceGroups(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) error {
	client, err := armresources.NewResourceGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return fmt.Errorf("armresources:NewResourceGroupsClient: %w", err)
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return skipIfAccessDenied(st, "armresources:ResourceGroups.List", sub.ID, err)
			}
			return fmt.Errorf("armresources:ResourceGroups.List: %w", err)
		}
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
				return fmt.Errorf("upsert resource groups: %w", err)
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
				return fmt.Errorf("closure resource groups: %w", err)
			}
		}
	}
	return nil
}
