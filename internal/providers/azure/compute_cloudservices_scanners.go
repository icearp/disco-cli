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
	registerType(restype.Descriptor{Type: TypeComputeCloudService, Service: "microsoft.compute"})
	registerType(restype.Descriptor{Type: TypeComputeCloudServiceRole, Service: "microsoft.compute"})
	registerType(restype.Descriptor{Type: TypeComputeCloudServiceRoleInstance, Service: "microsoft.compute"})
}

// cloudServiceEntry holds the identifying fields of a cloud service for child scans.
type cloudServiceEntry struct {
	rg, name, nativeID, discoID string
}

// scanCloudServices discovers Azure Cloud Services (legacy PaaS): cloud services,
// cloud service roles, and cloud service role instances.
func scanCloudServices(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return scanCloudServiceChain(ctx, sub, cred, st, scanID)
}

// scanCloudServiceChain scans cloud services then fans out role and role instance scans.
func scanCloudServiceChain(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	csClient, err := armcompute.NewCloudServicesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewCloudServicesClient: %w", err)
	}

	var (
		csBatch   []*store.Resource
		csPairs   [][2]string
		csEntries []cloudServiceEntry
	)
	pager := csClient.NewListAllPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, skipIfAccessDenied(st, "armcompute:CloudServices.ListAll", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:CloudServices.ListAll: %w", err)
		}
		for _, cs := range page.Value {
			if cs.ID == nil {
				continue
			}
			name := sv(cs.Name)
			location := sv(cs.Location)
			nativeID := sv(cs.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeCloudService,
				NativeID:       nativeID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(cs),
				DiscoveredBy:   scanID,
			}
			r.TagsJSON = azTagsJSON(cs.Tags)
			discoID := store.ResourceID("azure", sub.ID, TypeComputeCloudService, nativeID)
			csBatch = append(csBatch, r)
			csPairs = append(csPairs, rgHierarchyPair(sub, TypeComputeCloudService, nativeID))
			csEntries = append(csEntries, cloudServiceEntry{
				rg:       rgNameFromID(nativeID),
				name:     name,
				nativeID: nativeID,
				discoID:  discoID,
			})
		}
	}
	if len(csBatch) > 0 {
		n, err := st.UpsertResources(csBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure cloud services: %w", err)
		}
		total += len(csBatch)
		inserted += n
		if err := st.RecordHierarchyBatch(csPairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure cloud services: %w", err)
		}
	}
	if len(csEntries) == 0 {
		return total, inserted, nil
	}

	// Fan out role and role instance scans per cloud service.
	roleClient, err := armcompute.NewCloudServiceRolesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewCloudServiceRolesClient: %w", err)
	}
	riClient, err := armcompute.NewCloudServiceRoleInstancesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewCloudServiceRoleInstancesClient: %w", err)
	}

	var (
		mu                sync.Mutex
		cTotal, cInserted int
	)
	sem := semaphore.NewWeighted(maxConcurrentFanout)
	g, gCtx := errgroup.WithContext(ctx)
	for _, cs := range csEntries {
		entry := cs
		g.Go(func() error {
			if err := sem.Acquire(gCtx, 1); err != nil {
				return err
			}
			defer sem.Release(1)

			rT, rN, rErr := scanCloudServiceRoles(gCtx, sub, roleClient, st, scanID, entry)
			if rErr != nil {
				return rErr
			}
			riT, riN, riErr := scanCloudServiceRoleInstances(gCtx, sub, riClient, st, scanID, entry)
			if riErr != nil {
				return riErr
			}
			mu.Lock()
			cTotal += rT + riT
			cInserted += rN + riN
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	return total + cTotal, inserted + cInserted, nil
}

func scanCloudServiceRoles(ctx context.Context, sub *subscription, client *armcompute.CloudServiceRolesClient, st *store.Store, scanID string, cs cloudServiceEntry) (total, inserted int, err error) {
	var batch []*store.Resource
	var pairs [][2]string
	pager := client.NewListPager(cs.rg, cs.name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, skipIfAccessDenied(st, "armcompute:CloudServiceRoles.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:CloudServiceRoles.List %s/%s: %w", cs.rg, cs.name, err)
		}
		for _, role := range page.Value {
			if role.ID == nil {
				continue
			}
			name := sv(role.Name)
			nativeID := sv(role.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeCloudServiceRole,
				NativeID:       nativeID,
				Name:           &name,
				AttributesJSON: mustJSON(role),
				DiscoveredBy:   scanID,
			}
			roleID := store.ResourceID("azure", sub.ID, TypeComputeCloudServiceRole, nativeID)
			batch = append(batch, r)
			pairs = append(pairs, [2]string{roleID, cs.discoID})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure cloud service roles %s: %w", cs.name, err)
		}
		total = len(batch)
		inserted = n
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure cloud service roles %s: %w", cs.name, err)
		}
	}
	return total, inserted, nil
}

func scanCloudServiceRoleInstances(ctx context.Context, sub *subscription, client *armcompute.CloudServiceRoleInstancesClient, st *store.Store, scanID string, cs cloudServiceEntry) (total, inserted int, err error) {
	var batch []*store.Resource
	var pairs [][2]string
	pager := client.NewListPager(cs.rg, cs.name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isSkippableScanError(err) {
				return 0, 0, skipIfAccessDenied(st, "armcompute:CloudServiceRoleInstances.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:CloudServiceRoleInstances.List %s/%s: %w", cs.rg, cs.name, err)
		}
		for _, ri := range page.Value {
			if ri.ID == nil {
				continue
			}
			name := sv(ri.Name)
			nativeID := sv(ri.ID)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeCloudServiceRoleInstance,
				NativeID:       nativeID,
				Name:           &name,
				AttributesJSON: mustJSON(ri),
				DiscoveredBy:   scanID,
			}
			riID := store.ResourceID("azure", sub.ID, TypeComputeCloudServiceRoleInstance, nativeID)
			batch = append(batch, r)
			pairs = append(pairs, [2]string{riID, cs.discoID})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Azure cloud service role instances %s: %w", cs.name, err)
		}
		total = len(batch)
		inserted = n
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return 0, 0, fmt.Errorf("closure Azure cloud service role instances %s: %w", cs.name, err)
		}
	}
	return total, inserted, nil
}
