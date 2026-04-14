package azure

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
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
	client, err := armnetwork.NewVirtualNetworksClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewVirtualNetworksClient: %w", err)
	}

	pager := client.NewListAllPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armnetwork:VirtualNetworks.ListAll", sub.ID, err)
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
			vnetResourceID := store.ResourceID("azure", sub.ID, TypeVirtualNetwork, vnetID)

			r := &store.Resource{
				Provider:       "azure",
				AccountID:      sub.ID,
				AccountName:    &sub.Name,
				Type:           TypeVirtualNetwork,
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
						Type:           TypeSubnet,
						NativeID:       snID,
						Name:           &snName,
						Region:         &location,
						AttributesJSON: mustJSON(sn),
						DiscoveredBy:   scanID,
					}
					subnetBatch = append(subnetBatch, snResource)
					snResourceID := store.ResourceID("azure", sub.ID, TypeSubnet, snID)
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
	client, err := armnetwork.NewSecurityGroupsClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewSecurityGroupsClient: %w", err)
	}

	pager := client.NewListAllPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armnetwork:SecurityGroups.ListAll", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armnetwork:SecurityGroups.ListAll: %w", err)
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
				Type:           TypeNetworkSecurityGroup,
				NativeID:       sv(nsg.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(nsg),
				DiscoveredBy:   scanID,
			}
			if nsg.Tags != nil {
				s := mustJSON(nsg.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert NSGs: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

func scanPublicIPs(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armnetwork.NewPublicIPAddressesClient(sub.ID, cred, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("armnetwork:NewPublicIPAddressesClient: %w", err)
	}

	pager := client.NewListAllPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("armnetwork:PublicIPAddresses.ListAll", sub.ID, err)
			}
			return 0, 0, fmt.Errorf("armnetwork:PublicIPAddresses.ListAll: %w", err)
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
				Type:           TypePublicIPAddress,
				NativeID:       sv(ip.ID),
				Name:           &name,
				Region:         &location,
				AttributesJSON: mustJSON(ip),
				DiscoveredBy:   scanID,
			}
			if ip.Tags != nil {
				s := mustJSON(ip.Tags)
				r.TagsJSON = &s
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert public IPs: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
