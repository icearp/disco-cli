package azure

import (
	"context"
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeComputeVirtualMachine, Service: "microsoft.compute"})
	registerType(restype.Descriptor{Type: TypeComputeVMExtension, Service: "microsoft.compute"})
}

func scanVMs(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewVirtualMachinesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewVirtualMachinesClient: %w", err)
	}

	pager := client.NewListAllPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, skipIfAccessDenied(st, "armcompute:VMs.ListAll", sub.ID, err)
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
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeVirtualMachine,
				NativeID:       sv(vm.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(vm),
				DiscoveredBy:   scanID,
			}
			if len(vm.Zones) > 0 {
				z := sv(vm.Zones[0])
				r.Zone = &z
			}
			if vm.Properties != nil {
				r.CreatedAt = tp(vm.Properties.TimeCreated)
			}
			r.TagsJSON = azTagsJSON(vm.Tags)
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeComputeVirtualMachine, sv(vm.ID)))
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure VMs: %w", err)
			}
			total += len(batch)
			inserted += n
			if err := st.RecordHierarchyBatch(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure Azure VMs: %w", err)
			}
		}
	}
	return total, inserted, nil
}

// scanVMExtensions lists all extensions for every VM in the subscription, fanning
// out one API call per VM via errgroup (bounded by maxConcurrentFanout). Must run
// after scanVMs has populated the store.
func scanVMExtensions(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewVirtualMachineExtensionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewVirtualMachineExtensionsClient: %w", err)
	}

	// Load all VMs for this subscription from the store.
	vms, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
		AccountID: sub.ID,
		Types:     []string{TypeComputeVirtualMachine},
		Limit:     util.AllResources,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("list VMs for extension scan: %w", err)
	}
	if len(vms) == 0 {
		return 0, 0, nil
	}

	var (
		mu    sync.Mutex
		batch []*store.Resource
		pairs [][2]string
	)

	sem := semaphore.NewWeighted(maxConcurrentFanout)
	g, gctx := errgroup.WithContext(ctx)
	for _, vm := range vms {
		vmNativeID := vm.NativeID
		vmDiscoID := vm.ID // stable disco ID is the parent
		rgName := rgNameFromID(vmNativeID)
		vmName := nameFromID(vmNativeID)
		if rgName == "" || vmName == "" {
			continue
		}
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)

			resp, err := client.List(gctx, rgName, vmName, nil)
			if err != nil {
				if isSkippableScanError(err) {
					return skipIfAccessDenied(st, "armcompute:VMExtensions.List", sub.ID, err)
				}
				return fmt.Errorf("armcompute:VMExtensions.List %s/%s: %w", rgName, vmName, err)
			}

			var localBatch []*store.Resource
			var localPairs [][2]string
			for _, ext := range resp.Value {
				if ext.ID == nil {
					continue
				}
				name := sv(ext.Name)
				location := sv(ext.Location)
				r := &store.Resource{
					Provider:       "azure",
					AccountID:      sub.ID,
					AccountName:    &sub.Name,
					Type:           TypeComputeVMExtension,
					NativeID:       sv(ext.ID),
					Name:           &name,
					Region:         &location,
					AttributesJSON: mustJSON(ext),
					DiscoveredBy:   scanID,
				}
				localBatch = append(localBatch, r)
				extID := store.ResourceID("azure", sub.ID, sv(ext.ID))
				localPairs = append(localPairs, [2]string{extID, vmDiscoID})
			}
			if len(localBatch) > 0 {
				mu.Lock()
				batch = append(batch, localBatch...)
				pairs = append(pairs, localPairs...)
				mu.Unlock()
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}

	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure VM extensions: %w", err)
		}
		total = len(batch)
		inserted = n
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure VM extensions: %w", err)
		}
	}
	return total, inserted, nil
}
