package azure

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
)

func scanAvailabilitySets(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewAvailabilitySetsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewAvailabilitySetsClient: %w", err)
	}

	pager := client.NewListBySubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:AvailabilitySets.ListBySubscription", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:AvailabilitySets.ListBySubscription: %w", err)
		}
		var batch []*store.Resource
		var pairs [][2]string
		for _, a := range page.Value {
			if a.ID == nil {
				continue
			}
			name := sv(a.Name)
			location := sv(a.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeAvailabilitySet,
				NativeID:       sv(a.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			}
			if a.Tags != nil {
				s := mustJSON(a.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeComputeAvailabilitySet, sv(a.ID)))
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure availability sets: %w", err)
			}
			total += len(batch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure Azure availability sets: %w", err)
			}
		}
	}
	return total, inserted, nil
}

func scanSSHPublicKeys(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewSSHPublicKeysClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewSSHPublicKeysClient: %w", err)
	}

	pager := client.NewListBySubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:SSHPublicKeys.ListBySubscription", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:SSHPublicKeys.ListBySubscription: %w", err)
		}
		var batch []*store.Resource
		var pairs [][2]string
		for _, k := range page.Value {
			if k.ID == nil {
				continue
			}
			name := sv(k.Name)
			location := sv(k.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeSSHPublicKey,
				NativeID:       sv(k.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(k),
				DiscoveredBy:   scanID,
			}
			if k.Tags != nil {
				s := mustJSON(k.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeComputeSSHPublicKey, sv(k.ID)))
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure SSH public keys: %w", err)
			}
			total += len(batch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure Azure SSH public keys: %w", err)
			}
		}
	}
	return total, inserted, nil
}

func scanProximityPlacementGroups(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewProximityPlacementGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewProximityPlacementGroupsClient: %w", err)
	}

	pager := client.NewListBySubscriptionPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:ProximityPlacementGroups.ListBySubscription", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:ProximityPlacementGroups.ListBySubscription: %w", err)
		}
		var batch []*store.Resource
		var pairs [][2]string
		for _, p := range page.Value {
			if p.ID == nil {
				continue
			}
			name := sv(p.Name)
			location := sv(p.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeProximityPlacementGroup,
				NativeID:       sv(p.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			}
			if p.Tags != nil {
				s := mustJSON(p.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeComputeProximityPlacementGroup, sv(p.ID)))
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure proximity placement groups: %w", err)
			}
			total += len(batch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure Azure proximity placement groups: %w", err)
			}
		}
	}
	return total, inserted, nil
}

func scanComputeImages(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewImagesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewImagesClient: %w", err)
	}

	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:Images.List", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:Images.List: %w", err)
		}
		var batch []*store.Resource
		var pairs [][2]string
		for _, img := range page.Value {
			if img.ID == nil {
				continue
			}
			name := sv(img.Name)
			location := sv(img.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeImage,
				NativeID:       sv(img.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(img),
				DiscoveredBy:   scanID,
			}
			if img.Tags != nil {
				s := mustJSON(img.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeComputeImage, sv(img.ID)))
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure compute images: %w", err)
			}
			total += len(batch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure Azure compute images: %w", err)
			}
		}
	}
	return total, inserted, nil
}

func scanRestorePointCollections(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armcompute.NewRestorePointCollectionsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armcompute:NewRestorePointCollectionsClient: %w", err)
	}

	pager := client.NewListAllPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armcompute:RestorePointCollections.ListAll", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armcompute:RestorePointCollections.ListAll: %w", err)
		}
		var batch []*store.Resource
		var pairs [][2]string
		for _, rpc := range page.Value {
			if rpc.ID == nil {
				continue
			}
			name := sv(rpc.Name)
			location := sv(rpc.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeComputeRestorePointCollection,
				NativeID:       sv(rpc.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(rpc),
				DiscoveredBy:   scanID,
			}
			if rpc.Tags != nil {
				s := mustJSON(rpc.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
			pairs = append(pairs, rgHierarchyPair(sub, TypeComputeRestorePointCollection, sv(rpc.ID)))
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Azure restore point collections: %w", err)
			}
			total += len(batch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
				return 0, 0, fmt.Errorf("closure Azure restore point collections: %w", err)
			}
		}
	}
	return total, inserted, nil
}
