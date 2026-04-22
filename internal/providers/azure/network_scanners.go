package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "azure:network", fn: scanNetwork}) }

// scanNetwork discovers VNets, subnets, NSGs, and public IP addresses in parallel.
func scanNetwork(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	var (
		vnetTotal, vnetInserted int
		nsgTotal, nsgInserted   int
		ipTotal, ipInserted     int
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		t, n, e := scanVNets(gctx, sub, cred, st, scanID)
		vnetTotal, vnetInserted = t, n
		return e
	})
	g.Go(func() error {
		t, n, e := scanNSGs(gctx, sub, cred, st, scanID)
		nsgTotal, nsgInserted = t, n
		return e
	})
	g.Go(func() error {
		t, n, e := scanPublicIPs(gctx, sub, cred, st, scanID)
		ipTotal, ipInserted = t, n
		return e
	})
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	return vnetTotal + nsgTotal + ipTotal, vnetInserted + nsgInserted + ipInserted, nil
}

func scanVNets(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armnetwork.NewVirtualNetworksClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewVirtualNetworksClient: %w", err)
	}

	pager := client.NewListAllPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "armnetwork:VirtualNetworks.ListAll", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armnetwork:VirtualNetworks.ListAll: %w", err)
		}
		var batch []*store.Resource
		var subnetBatch []*store.Resource
		var subnetPairs [][2]string
		for _, vnet := range page.Value {
			if vnet.ID == nil {
				continue
			}
			name := sv(vnet.Name)
			location := sv(vnet.Location)
			vnetID := sv(vnet.ID)
			vnetResourceID := store.ResourceID("azure", sub.ID, TypeNetworkVirtualNetwork, vnetID)

			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeNetworkVirtualNetwork,
				NativeID:       vnetID,
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(vnet),
				DiscoveredBy:   scanID,
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
						Type:           TypeNetworkSubnet,
						NativeID:       snID,
						Name:           &snName,
						Region:         &location,
						AttributesJSON: mustJSON(sn),
						DiscoveredBy:   scanID,
					}
					subnetBatch = append(subnetBatch, snResource)
					snResourceID := store.ResourceID("azure", sub.ID, TypeNetworkSubnet, snID)
					subnetPairs = append(subnetPairs, [2]string{snResourceID, vnetResourceID})
				}
			}
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert VNets: %w", err)
			}
			total += len(batch)
			inserted += n
		}
		if len(subnetBatch) > 0 {
			n, err := st.UpsertResources(subnetBatch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert subnets: %w", err)
			}
			total += len(subnetBatch)
			inserted += n
			if err := st.BatchAddToHierarchyClosure(subnetPairs); err != nil {
				return 0, 0, fmt.Errorf("closure subnets: %w", err)
			}
		}
	}
	return total, inserted, nil
}

func scanNSGs(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armnetwork.NewSecurityGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewSecurityGroupsClient: %w", err)
	}
	return azPageScan(ctx, "armnetwork:SecurityGroups.ListAll", sub, st,
		client.NewListAllPager(nil),
		func(page armnetwork.SecurityGroupsClientListAllResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, nsg := range page.Value {
				if nsg.ID == nil {
					continue
				}
				name, loc := sv(nsg.Name), sv(nsg.Location)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeNetworkSecurityGroup, NativeID: sv(nsg.ID),
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(nsg.Tags), AttributesJSON: mustJSON(nsg),
					DiscoveredBy: scanID,
				})
			}
			return batch, nil
		})
}

func scanPublicIPs(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armnetwork.NewPublicIPAddressesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewPublicIPAddressesClient: %w", err)
	}
	return azPageScan(ctx, "armnetwork:PublicIPAddresses.ListAll", sub, st,
		client.NewListAllPager(nil),
		func(page armnetwork.PublicIPAddressesClientListAllResponse) ([]*store.Resource, [][2]string) {
			var batch []*store.Resource
			for _, ip := range page.Value {
				if ip.ID == nil {
					continue
				}
				name, loc := sv(ip.Name), sv(ip.Location)
				batch = append(batch, &store.Resource{
					Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
					Type: TypeNetworkPublicIPAddress, NativeID: sv(ip.ID),
					Name: &name, Region: &loc,
					TagsJSON: azTagsJSON(ip.Tags), AttributesJSON: mustJSON(ip),
					DiscoveredBy: scanID,
				})
			}
			return batch, nil
		})
}
