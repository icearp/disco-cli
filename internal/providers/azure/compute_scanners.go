package azure

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
)

// scanCompute discovers Azure VMs and managed disks.
func scanCompute(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) error {
	if err := scanVMs(ctx, sub, cred, st, scanID); err != nil {
		return err
	}
	return scanDisks(ctx, sub, cred, st, scanID)
}

func scanVMs(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) error {
	client, err := armcompute.NewVirtualMachinesClient(sub.ID, cred, nil)
	if err != nil {
		return fmt.Errorf("armcompute:NewVirtualMachinesClient: %w", err)
	}

	pager := client.NewListAllPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("armcompute:VMs.ListAll", sub.ID, err)
			}
			return fmt.Errorf("armcompute:VMs.ListAll: %w", err)
		}
		var batch []*store.Resource
		for _, vm := range page.Value {
			if vm.ID == nil {
				continue
			}
			name := sv(vm.Name)
			location := sv(vm.Location)
			rgName := rgFromID(sv(vm.ID))
			rgID := store.ResourceID("azure", sub.ID, "azure:resources:resource-group",
				fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", sub.ID, rgName))
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           "azure:compute:virtual-machine",
				NativeID:       sv(vm.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(vm),
				ScanID:         scanID,
				ParentID:       &rgID,
			}
			if vm.Tags != nil {
				s := mustJSON(vm.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert Azure VMs: %w", err)
			}
			var pairs [][2]string
			for _, r := range batch {
				if r.ParentID != nil {
					vmID := store.ResourceID("azure", sub.ID, "azure:compute:virtual-machine", r.NativeID)
					pairs = append(pairs, [2]string{vmID, *r.ParentID})
				}
			}
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return fmt.Errorf("closure Azure VMs: %w", err)
			}
		}
	}
	return nil
}

func scanDisks(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) error {
	client, err := armcompute.NewDisksClient(sub.ID, cred, nil)
	if err != nil {
		return fmt.Errorf("armcompute:NewDisksClient: %w", err)
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("armcompute:Disks.List", sub.ID, err)
			}
			return fmt.Errorf("armcompute:Disks.List: %w", err)
		}
		var batch []*store.Resource
		for _, d := range page.Value {
			if d.ID == nil {
				continue
			}
			name := sv(d.Name)
			location := sv(d.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           "azure:compute:disk",
				NativeID:       sv(d.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(d),
				ScanID:         scanID,
			}
			if d.Tags != nil {
				s := mustJSON(d.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert Azure disks: %w", err)
			}
		}
	}
	return nil
}
