package azure

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeComputeVMSS, Service: "microsoft.compute"})
	registerType(restype.Descriptor{Type: TypeComputeVMSSExtension, Service: "microsoft.compute"})
	registerType(restype.Descriptor{Type: TypeComputeVMSSVM, Service: "microsoft.compute"})
	registerType(restype.Descriptor{Type: TypeComputeVMSSVMExtension, Service: "microsoft.compute"})
}

// vmssEntry holds the identifying fields of a VMSS, used to fan out child scans.
type vmssEntry struct {
	rg, name, nativeID, discoID string
}

// vmssVMEntry holds the identifying fields of a VMSS VM instance, used to fan out extension scans.
type vmssVMEntry struct {
	rg, vmssName, instanceID, nativeID, discoID string
}

// scanVMSS discovers VMSS resources across four phases:
//  1. VMSS (subscription-wide list)
//  2. VMSS extensions and VMSS VMs (per VMSS, fanned out concurrently)
//  3. VMSS VM extensions (per VMSS VM instance, fanned out concurrently)
func scanVMSS(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	// Phase A: list all VMSS in the subscription.
	vmssClient, err := armcompute.NewVirtualMachineScaleSetsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewVirtualMachineScaleSetsClient: %w", err)
	}

	var (
		vmssBatch   []*store.Resource
		vmssPairs   [][2]string
		vmssEntries []vmssEntry
	)

	pager := vmssClient.NewListAllPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, skipIfAccessDenied(st, "armcompute:VMSS.ListAll", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:VMSS.ListAll: %w", err)
		}
		for _, v := range page.Value {
			if v.ID == nil {
				continue
			}
			name := sv(v.Name)
			location := sv(v.Location)
			nativeID := sv(v.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeVMSS,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(v),
				DiscoveredBy:   scanID,
			}
			r.TagsJSON = azTagsJSON(v.Tags)
			discoID := store.ResourceID("azure", sub.ID, nativeID)
			vmssBatch = append(vmssBatch, r)
			vmssPairs = append(vmssPairs, rgHierarchyPair(sub, TypeComputeVMSS, nativeID))
			vmssEntries = append(vmssEntries, vmssEntry{
				rg:       rgNameFromID(nativeID),
				name:     name,
				nativeID: nativeID,
				discoID:  discoID,
			})
		}
	}

	if len(vmssBatch) > 0 {
		n, err := st.UpsertResources(vmssBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure VMSS: %w", err)
		}
		total += len(vmssBatch)
		inserted += n
		if err := st.RecordHierarchyBatch(vmssPairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure VMSS: %w", err)
		}
	}
	if len(vmssEntries) == 0 {
		return total, inserted, nil
	}

	// Phase B: for each VMSS, fan out extension and VM scans concurrently.
	var (
		bMu               sync.Mutex
		bTotal, bInserted int
		vmEntries         []vmssVMEntry
	)
	sem := semaphore.NewWeighted(maxConcurrentFanout)
	gB, gBCtx := errgroup.WithContext(ctx)
	for _, entry := range vmssEntries {
		e := entry
		gB.Go(func() error {
			if err := sem.Acquire(gBCtx, 1); err != nil {
				return err
			}
			defer sem.Release(1)

			extT, extN, extErr := scanVMSSExtensions(gBCtx, sub, cred, st, scanID, e)
			if extErr != nil {
				return extErr
			}
			vmT, vmN, vms, vmErr := scanVMSSVMs(gBCtx, sub, cred, st, scanID, e)
			if vmErr != nil {
				return vmErr
			}

			bMu.Lock()
			bTotal += extT + vmT
			bInserted += extN + vmN
			vmEntries = append(vmEntries, vms...)
			bMu.Unlock()
			return nil
		})
	}
	if err := gB.Wait(); err != nil {
		return 0, 0, err
	}
	total += bTotal
	inserted += bInserted

	// Phase C: fan out VM extension scans per VMSS VM instance.
	if len(vmEntries) == 0 {
		return total, inserted, nil
	}
	var (
		cMu               sync.Mutex
		cTotal, cInserted int
	)
	sem2 := semaphore.NewWeighted(maxConcurrentFanout)
	gC, gCCtx := errgroup.WithContext(ctx)
	for _, vm := range vmEntries {
		v := vm
		gC.Go(func() error {
			if err := sem2.Acquire(gCCtx, 1); err != nil {
				return err
			}
			defer sem2.Release(1)
			t, n, e := scanVMSSVMExtensions(gCCtx, sub, cred, st, scanID, v)
			if e != nil {
				return e
			}
			cMu.Lock()
			cTotal += t
			cInserted += n
			cMu.Unlock()
			return nil
		})
	}
	if err := gC.Wait(); err != nil {
		return 0, 0, err
	}
	total += cTotal
	inserted += cInserted

	return total, inserted, nil
}

func scanVMSSExtensions(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, v vmssEntry) (total, inserted int, err error) {
	client, err := armcompute.NewVirtualMachineScaleSetExtensionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewVirtualMachineScaleSetExtensionsClient: %w", err)
	}

	var batch []*store.Resource
	var pairs [][2]string
	pager := client.NewListPager(v.rg, v.name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, skipIfAccessDenied(st, "armcompute:VMSSExtensions.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:VMSSExtensions.List %s/%s: %w", v.rg, v.name, err)
		}
		for _, ext := range page.Value {
			if ext.ID == nil {
				continue
			}
			name := sv(ext.Name)
			nativeID := sv(ext.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeVMSSExtension,
				NativeID:       nativeID,
				Name:           &name,
				AttributesJSON: mustJSON(ext),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
			extID := store.ResourceID("azure", sub.ID, nativeID)
			pairs = append(pairs, [2]string{extID, v.discoID})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure VMSS extensions %s: %w", v.name, err)
		}
		total = len(batch)
		inserted = n
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure VMSS extensions %s: %w", v.name, err)
		}
	}
	return total, inserted, nil
}

func scanVMSSVMs(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, v vmssEntry) (total, inserted int, vms []vmssVMEntry, err error) {
	client, err := armcompute.NewVirtualMachineScaleSetVMsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("armcompute:NewVirtualMachineScaleSetVMsClient: %w", err)
	}

	var batch []*store.Resource
	var pairs [][2]string
	pager := client.NewListPager(v.rg, v.name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "armcompute:VMSSVMs.List", sub.ID, err)
			}
			return 0, 0, nil, fmt.Errorf("armcompute:VMSSVMs.List %s/%s: %w", v.rg, v.name, err)
		}
		for _, vm := range page.Value {
			if vm.ID == nil || vm.InstanceID == nil {
				continue
			}
			name := sv(vm.Name)
			location := sv(vm.Location)
			nativeID := sv(vm.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeVMSSVM,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(vm),
				DiscoveredBy:   scanID,
			}
			r.TagsJSON = azTagsJSON(vm.Tags)
			vmDiscoID := store.ResourceID("azure", sub.ID, nativeID)
			batch = append(batch, r)
			pairs = append(pairs, [2]string{vmDiscoID, v.discoID})
			vms = append(vms, vmssVMEntry{
				rg:         v.rg,
				vmssName:   v.name,
				instanceID: sv(vm.InstanceID),
				nativeID:   nativeID,
				discoID:    vmDiscoID,
			})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("upsert Azure VMSS VMs %s: %w", v.name, err)
		}
		total = len(batch)
		inserted = n
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, nil, fmt.Errorf("closure Azure VMSS VMs %s: %w", v.name, err)
		}
	}
	return total, inserted, vms, nil
}

func scanVMSSVMExtensions(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string, vm vmssVMEntry) (total, inserted int, err error) {
	client, err := armcompute.NewVirtualMachineScaleSetVMExtensionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewVirtualMachineScaleSetVMExtensionsClient: %w", err)
	}

	resp, err := client.List(ctx, vm.rg, vm.vmssName, vm.instanceID, nil)
	if err != nil {
		if isSkippableScanError(err) {
			return 0, 0, skipIfAccessDenied(st, "armcompute:VMSSVMExtensions.List", sub.ID, err)
		}
		return 0, 0, fmt.Errorf("armcompute:VMSSVMExtensions.List %s/%s/%s: %w", vm.rg, vm.vmssName, vm.instanceID, err)
	}

	var batch []*store.Resource
	var pairs [][2]string
	for _, ext := range resp.Value {
		if ext.ID == nil {
			continue
		}
		name := sv(ext.Name)
		nativeID := sv(ext.ID)
		r := &store.Resource{
			Provider:       "azure",
			AccountID:      sub.ID,
			AccountName:    &sub.Name,
			Type:           TypeComputeVMSSVMExtension,
			NativeID:       nativeID,
			Name:           &name,
			AttributesJSON: mustJSON(ext),
			DiscoveredBy:   scanID,
		}
		batch = append(batch, r)
		extID := store.ResourceID("azure", sub.ID, nativeID)
		pairs = append(pairs, [2]string{extID, vm.discoID})
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure VMSS VM extensions: %w", err)
		}
		total = len(batch)
		inserted = n
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure VMSS VM extensions: %w", err)
		}
	}
	return total, inserted, nil
}
