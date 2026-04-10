package azure

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "azure:compute", fn: scanCompute}) }

// scanCompute discovers Azure VMs and managed disks in parallel.
func scanCompute(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	var (
		vmTotal, vmInserted     int
		diskTotal, diskInserted int
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		t, n, e := scanVMs(gctx, sub, cred, st, scanID)
		vmTotal, vmInserted = t, n
		return e
	})
	g.Go(func() error {
		t, n, e := scanDisks(gctx, sub, cred, st, scanID)
		diskTotal, diskInserted = t, n
		return e
	})
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	return vmTotal + diskTotal, vmInserted + diskInserted, nil
}

func scanVMs(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewVirtualMachinesClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewVirtualMachinesClient: %w", err)
	}

	pager := client.NewListAllPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:VMs.ListAll", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:VMs.ListAll: %w", err)
		}
		var batch []*store.Resource
		var pairs [][2]string
		for _, vm := range page.Value {
			if vm.ID == nil {
				continue
			}
			name := sv(vm.Name)
			location := sv(vm.Location)
			rgName := rgFromID(sv(vm.ID))
			rgID := store.ResourceID("azure", sub.ID, TypeResourceGroup,
				fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", sub.ID, rgName))
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeVirtualMachine,
				NativeID:       sv(vm.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(vm),
				DiscoveredBy:         scanID,
			}
			if len(vm.Zones) > 0 {
				z := sv(vm.Zones[0])
				r.Zone = &z
			}
			if vm.Properties != nil {
				r.CreatedAt = tp(vm.Properties.TimeCreated)
			}
			if vm.Tags != nil {
				s := mustJSON(vm.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
			vmID := store.ResourceID("azure", sub.ID, TypeVirtualMachine, sv(vm.ID))
			pairs = append(pairs, [2]string{vmID, rgID})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure VMs: %w", err)
			}
			total += len(batch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure Azure VMs: %w", err)
			}
		}
	}
	return total, inserted, nil
}

func scanDisks(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewDisksClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewDisksClient: %w", err)
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:Disks.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:Disks.List: %w", err)
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
				Type:           TypeManagedDisk,
				NativeID:       sv(d.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(d),
				DiscoveredBy:   scanID,
			}
			if d.Tags != nil {
				s := mustJSON(d.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure disks: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
