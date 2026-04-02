package azure

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// scanResourceGroups discovers all resource groups in a subscription and
// upserts them as parent resources. All other Azure resources use the resource
// group disco ID as their parent_id.
func scanResourceGroups(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) error {
	client, err := armresources.NewResourceGroupsClient(sub.ID, cred, nil)
	if err != nil {
		return fmt.Errorf("armresources:NewResourceGroupsClient: %w", err)
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("armresources:ResourceGroups.List", sub.ID, err)
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
				Type:           "azure:resources:resource-group",
				NativeID:       sv(rg.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(rg),
				ScanID:         scanID,
			}
			if rg.Tags != nil {
				s := mustJSON(rg.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert resource groups: %w", err)
			}
			// Populate hierarchy closure: each RG belongs to the subscription.
			// We don't model the subscription as a resource here, so we just
			// add self-entries for resource groups.
			for _, r := range batch {
				rgID := store.ResourceID("azure", sub.ID, "azure:resources:resource-group", r.NativeID)
				_ = st.AddToHierarchyClosure(rgID, rgID)
			}
		}
	}
	return nil
}
