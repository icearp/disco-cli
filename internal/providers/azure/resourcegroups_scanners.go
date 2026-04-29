package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

func init() {
	// scanResourceGroups runs once per subscription invoked direct from
	// azure.go (not via registerService) since it pre-seeds RG parents
	// every other scanner depends on. Emits declared via registerExtraEmits.
	registerExtraEmits(
		coverage.TypeDecl{Service: "resources", DiscoType: TypeResourcesResourceGroup},
	)
}

// scanResourceGroups discovers all resource groups in a subscription and
// upserts them as parent resources. All other Azure resources use the resource
// group disco ID as their parent_id.
func scanResourceGroups(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) error {
	client, err := armresources.NewResourceGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return fmt.Errorf("armresources:NewResourceGroupsClient: %w", err)
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
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
			if rg.Tags != nil {
				s := mustJSON(rg.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if _, err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert resource groups: %w", err)
			}
			// Populate hierarchy closure: each RG belongs to the subscription.
			// We don't model the subscription as a resource here, so we just
			// add self-entries for resource groups.
			pairs := make([][2]string, len(batch))
			for i, r := range batch {
				rgID := store.ResourceID("azure", sub.ID, TypeResourcesResourceGroup, r.NativeID)
				pairs[i] = [2]string{rgID, rgID}
			}
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return fmt.Errorf("closure resource groups: %w", err)
			}
		}
	}
	return nil
}
