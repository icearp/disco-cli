package azure

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
)

func scanDisks(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewDisksClient(sub.ID, cred, azClientOptions)
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
				Type:           TypeComputeManagedDisk,
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

func scanSnapshots(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewSnapshotsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewSnapshotsClient: %w", err)
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:Snapshots.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:Snapshots.List: %w", err)
		}
		var batch []*store.Resource
		var pairs [][2]string
		for _, s := range page.Value {
			if s.ID == nil {
				continue
			}
			name := sv(s.Name)
			location := sv(s.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeSnapshot,
				NativeID:       sv(s.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			}
			if s.Tags != nil {
				tags := mustJSON(s.Tags)
				r.TagsJSON = &tags
			}
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeComputeSnapshot, sv(s.ID)))
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure snapshots: %w", err)
			}
			total += len(batch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure Azure snapshots: %w", err)
			}
		}
	}
	return total, inserted, nil
}

func scanDiskEncryptionSets(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewDiskEncryptionSetsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewDiskEncryptionSetsClient: %w", err)
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:DiskEncryptionSets.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:DiskEncryptionSets.List: %w", err)
		}
		var batch []*store.Resource
		var pairs [][2]string
		for _, des := range page.Value {
			if des.ID == nil {
				continue
			}
			name := sv(des.Name)
			location := sv(des.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeDiskEncryptionSet,
				NativeID:       sv(des.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(des),
				DiscoveredBy:   scanID,
			}
			if des.Tags != nil {
				s := mustJSON(des.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeComputeDiskEncryptionSet, sv(des.ID)))
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure disk encryption sets: %w", err)
			}
			total += len(batch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure Azure disk encryption sets: %w", err)
			}
		}
	}
	return total, inserted, nil
}

func scanDiskAccesses(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewDiskAccessesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewDiskAccessesClient: %w", err)
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:DiskAccesses.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:DiskAccesses.List: %w", err)
		}
		var batch []*store.Resource
		var pairs [][2]string
		for _, da := range page.Value {
			if da.ID == nil {
				continue
			}
			name := sv(da.Name)
			location := sv(da.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeDiskAccess,
				NativeID:       sv(da.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(da),
				DiscoveredBy:   scanID,
			}
			if da.Tags != nil {
				s := mustJSON(da.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeComputeDiskAccess, sv(da.ID)))
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure disk accesses: %w", err)
			}
			total += len(batch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure Azure disk accesses: %w", err)
			}
		}
	}
	return total, inserted, nil
}
