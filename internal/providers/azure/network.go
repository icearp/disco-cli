package azure

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
)

// scanNetwork discovers VNets, subnets, NSGs, and public IP addresses.
func scanNetwork(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) error {
	if err := scanVNets(ctx, sub, cred, st, scanID); err != nil {
		return err
	}
	if err := scanNSGs(ctx, sub, cred, st, scanID); err != nil {
		return err
	}
	return scanPublicIPs(ctx, sub, cred, st, scanID)
}

func scanVNets(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) error {
	client, err := armnetwork.NewVirtualNetworksClient(sub.ID, cred, nil)
	if err != nil {
		return fmt.Errorf("armnetwork:NewVirtualNetworksClient: %w", err)
	}

	pager := client.NewListAllPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("armnetwork:VirtualNetworks.ListAll", sub.ID, err)
			}
			return fmt.Errorf("armnetwork:VirtualNetworks.ListAll: %w", err)
		}
		var batch []*store.Resource
		var subnetBatch []*store.Resource
		for _, vnet := range page.Value {
			if vnet.ID == nil {
				continue
			}
			name := sv(vnet.Name)
			location := sv(vnet.Location)
			vnetID := sv(vnet.ID)
			vnetResourceID := store.ResourceID("azure", sub.ID, "azure:network:virtual-network", vnetID)

			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           "azure:network:virtual-network",
				NativeID:       vnetID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(vnet),
				ScanID:         scanID,
			}
			if vnet.Tags != nil {
				s := mustJSON(vnet.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)

			// Subnets are embedded in the VNet response.
			if vnet.Properties != nil {
				for _, sn := range vnet.Properties.Subnets {
					if sn.ID == nil {
						continue
					}
					snName := sv(sn.Name)
					snID := sv(sn.ID)
					snResource := &store.Resource{
						Provider:       "azure",
						AccountID:      sub.ID,
						AccountName:    &sub.Name,
						Type:           "azure:network:subnet",
						NativeID:       snID,
						Name:           &snName,
						Region:         &location,
						AttributesJSON: mustJSON(sn),
						ScanID:         scanID,
						ParentID:       &vnetResourceID,
					}
					subnetBatch = append(subnetBatch, snResource)
				}
			}
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert VNets: %w", err)
			}
		}
		if len(subnetBatch) > 0 {
			if err := st.UpsertResources(subnetBatch); err != nil {
				return fmt.Errorf("upsert subnets: %w", err)
			}
			for _, r := range subnetBatch {
				snID := store.ResourceID("azure", sub.ID, "azure:network:subnet", r.NativeID)
				if r.ParentID != nil {
					_ = st.AddToHierarchyClosure(snID, *r.ParentID)
				}
			}
		}
	}
	return nil
}

func scanNSGs(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) error {
	client, err := armnetwork.NewSecurityGroupsClient(sub.ID, cred, nil)
	if err != nil {
		return fmt.Errorf("armnetwork:NewSecurityGroupsClient: %w", err)
	}

	pager := client.NewListAllPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("armnetwork:SecurityGroups.ListAll", sub.ID, err)
			}
			return fmt.Errorf("armnetwork:SecurityGroups.ListAll: %w", err)
		}
		var batch []*store.Resource
		for _, nsg := range page.Value {
			if nsg.ID == nil {
				continue
			}
			name := sv(nsg.Name)
			location := sv(nsg.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           "azure:network:network-security-group",
				NativeID:       sv(nsg.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(nsg),
				ScanID:         scanID,
			}
			if nsg.Tags != nil {
				s := mustJSON(nsg.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert NSGs: %w", err)
			}
		}
	}
	return nil
}

func scanPublicIPs(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) error {
	client, err := armnetwork.NewPublicIPAddressesClient(sub.ID, cred, nil)
	if err != nil {
		return fmt.Errorf("armnetwork:NewPublicIPAddressesClient: %w", err)
	}

	pager := client.NewListAllPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("armnetwork:PublicIPAddresses.ListAll", sub.ID, err)
			}
			return fmt.Errorf("armnetwork:PublicIPAddresses.ListAll: %w", err)
		}
		var batch []*store.Resource
		for _, ip := range page.Value {
			if ip.ID == nil {
				continue
			}
			name := sv(ip.Name)
			location := sv(ip.Location)
			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           "azure:network:public-ip-address",
				NativeID:       sv(ip.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(ip),
				ScanID:         scanID,
			}
			if ip.Tags != nil {
				s := mustJSON(ip.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert public IPs: %w", err)
			}
		}
	}
	return nil
}
