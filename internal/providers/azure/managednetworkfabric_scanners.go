package azure

import (
	"context"
	"fmt"
	"sync"

	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/managednetworkfabric/armmanagednetworkfabric"
)

func init() {
	registerType(restype.Descriptor{Type: TypeManagedNetworkFabricACL, Service: "microsoft.managednetworkfabric", Leaf: true})
	registerType(restype.Descriptor{Type: TypeManagedNetworkFabric, Service: "microsoft.managednetworkfabric", Leaf: true, Redact: []redact.Rule{{Path: "properties.terminalServerConfiguration.password", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeManagedNetworkFabricInternetGwRule, Service: "microsoft.managednetworkfabric", Leaf: true})
	registerType(restype.Descriptor{Type: TypeManagedNetworkFabricInternetGateway, Service: "microsoft.managednetworkfabric", Leaf: true})
	registerType(restype.Descriptor{Type: TypeManagedNetworkFabricIPCommunity, Service: "microsoft.managednetworkfabric", Leaf: true})
	registerType(restype.Descriptor{Type: TypeManagedNetworkFabricIPExtCommunity, Service: "microsoft.managednetworkfabric", Leaf: true})
	registerType(restype.Descriptor{Type: TypeManagedNetworkFabricIPPrefix, Service: "microsoft.managednetworkfabric", Leaf: true})
	registerType(restype.Descriptor{Type: TypeManagedNetworkFabricL2IsolationDom, Service: "microsoft.managednetworkfabric", Leaf: true})
	registerType(restype.Descriptor{Type: TypeManagedNetworkFabricL3IsolationDom, Service: "microsoft.managednetworkfabric", Leaf: true})
	registerType(restype.Descriptor{Type: TypeManagedNetworkFabricNeighborGroup, Service: "microsoft.managednetworkfabric", Leaf: true})
	registerType(restype.Descriptor{Type: TypeManagedNetworkFabricNetworkDevice, Service: "microsoft.managednetworkfabric", Leaf: true})
	registerType(restype.Descriptor{Type: TypeManagedNetworkFabricController, Service: "microsoft.managednetworkfabric", Leaf: true})
	registerType(restype.Descriptor{Type: TypeManagedNetworkFabricPacketBroker, Service: "microsoft.managednetworkfabric", Leaf: true})
	registerType(restype.Descriptor{Type: TypeManagedNetworkFabricRack, Service: "microsoft.managednetworkfabric", Leaf: true})
	registerType(restype.Descriptor{Type: TypeManagedNetworkFabricNetworkTapRule, Service: "microsoft.managednetworkfabric", Leaf: true})
	registerType(restype.Descriptor{Type: TypeManagedNetworkFabricNetworkTap, Service: "microsoft.managednetworkfabric", Leaf: true})
	registerType(restype.Descriptor{Type: TypeManagedNetworkFabricRoutePolicy, Service: "microsoft.managednetworkfabric", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.managednetworkfabric",
		fn:   scanManagedNetworkFabric,
	})
}

// scanManagedNetworkFabric discovers every Microsoft.ManagedNetworkFabric type
// disco scans. Each type exposes a subscription-wide ListBySubscription pager,
// so each phase is a straight azSimpleScan. Phases run concurrently via
// sync.WaitGroup (per "Errors never abort scan"); the orchestrator surfaces
// only the first non-tolerated error.
func scanManagedNetworkFabric(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	aclClient, err := armmanagednetworkfabric.NewAccessControlListsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagednetworkfabric:NewAccessControlListsClient: %w", err)
	}
	fabricClient, err := armmanagednetworkfabric.NewNetworkFabricsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagednetworkfabric:NewNetworkFabricsClient: %w", err)
	}
	igwRuleClient, err := armmanagednetworkfabric.NewInternetGatewayRulesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagednetworkfabric:NewInternetGatewayRulesClient: %w", err)
	}
	igwClient, err := armmanagednetworkfabric.NewInternetGatewaysClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagednetworkfabric:NewInternetGatewaysClient: %w", err)
	}
	ipcClient, err := armmanagednetworkfabric.NewIPCommunitiesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagednetworkfabric:NewIPCommunitiesClient: %w", err)
	}
	ipecClient, err := armmanagednetworkfabric.NewIPExtendedCommunitiesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagednetworkfabric:NewIPExtendedCommunitiesClient: %w", err)
	}
	ippClient, err := armmanagednetworkfabric.NewIPPrefixesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagednetworkfabric:NewIPPrefixesClient: %w", err)
	}
	l2Client, err := armmanagednetworkfabric.NewL2IsolationDomainsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagednetworkfabric:NewL2IsolationDomainsClient: %w", err)
	}
	l3Client, err := armmanagednetworkfabric.NewL3IsolationDomainsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagednetworkfabric:NewL3IsolationDomainsClient: %w", err)
	}
	ngClient, err := armmanagednetworkfabric.NewNeighborGroupsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagednetworkfabric:NewNeighborGroupsClient: %w", err)
	}
	ndClient, err := armmanagednetworkfabric.NewNetworkDevicesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagednetworkfabric:NewNetworkDevicesClient: %w", err)
	}
	nfcClient, err := armmanagednetworkfabric.NewNetworkFabricControllersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagednetworkfabric:NewNetworkFabricControllersClient: %w", err)
	}
	npbClient, err := armmanagednetworkfabric.NewNetworkPacketBrokersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagednetworkfabric:NewNetworkPacketBrokersClient: %w", err)
	}
	nrClient, err := armmanagednetworkfabric.NewNetworkRacksClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagednetworkfabric:NewNetworkRacksClient: %w", err)
	}
	ntrClient, err := armmanagednetworkfabric.NewNetworkTapRulesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagednetworkfabric:NewNetworkTapRulesClient: %w", err)
	}
	ntClient, err := armmanagednetworkfabric.NewNetworkTapsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagednetworkfabric:NewNetworkTapsClient: %w", err)
	}
	rpClient, err := armmanagednetworkfabric.NewRoutePoliciesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armmanagednetworkfabric:NewRoutePoliciesClient: %w", err)
	}

	var (
		mu       sync.Mutex
		firstErr error
	)
	addTotals := func(t, n int, e error) {
		mu.Lock()
		defer mu.Unlock()
		total += t
		inserted += n
		if e != nil && firstErr == nil {
			firstErr = e
		}
	}

	phases := []func() (int, int, error){
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagednetworkfabric:AccessControlLists.ListBySubscription", TypeManagedNetworkFabricACL, sub, st, scanID,
				aclClient.NewListBySubscriptionPager(nil),
				func(p armmanagednetworkfabric.AccessControlListsClientListBySubscriptionResponse) []*armmanagednetworkfabric.AccessControlList {
					return p.Value
				},
				func(r *armmanagednetworkfabric.AccessControlList) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagednetworkfabric:NetworkFabrics.ListBySubscription", TypeManagedNetworkFabric, sub, st, scanID,
				fabricClient.NewListBySubscriptionPager(nil),
				func(p armmanagednetworkfabric.NetworkFabricsClientListBySubscriptionResponse) []*armmanagednetworkfabric.NetworkFabric {
					return p.Value
				},
				func(r *armmanagednetworkfabric.NetworkFabric) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagednetworkfabric:InternetGatewayRules.ListBySubscription", TypeManagedNetworkFabricInternetGwRule, sub, st, scanID,
				igwRuleClient.NewListBySubscriptionPager(nil),
				func(p armmanagednetworkfabric.InternetGatewayRulesClientListBySubscriptionResponse) []*armmanagednetworkfabric.InternetGatewayRule {
					return p.Value
				},
				func(r *armmanagednetworkfabric.InternetGatewayRule) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagednetworkfabric:InternetGateways.ListBySubscription", TypeManagedNetworkFabricInternetGateway, sub, st, scanID,
				igwClient.NewListBySubscriptionPager(nil),
				func(p armmanagednetworkfabric.InternetGatewaysClientListBySubscriptionResponse) []*armmanagednetworkfabric.InternetGateway {
					return p.Value
				},
				func(r *armmanagednetworkfabric.InternetGateway) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagednetworkfabric:IPCommunities.ListBySubscription", TypeManagedNetworkFabricIPCommunity, sub, st, scanID,
				ipcClient.NewListBySubscriptionPager(nil),
				func(p armmanagednetworkfabric.IPCommunitiesClientListBySubscriptionResponse) []*armmanagednetworkfabric.IPCommunity {
					return p.Value
				},
				func(r *armmanagednetworkfabric.IPCommunity) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagednetworkfabric:IPExtendedCommunities.ListBySubscription", TypeManagedNetworkFabricIPExtCommunity, sub, st, scanID,
				ipecClient.NewListBySubscriptionPager(nil),
				func(p armmanagednetworkfabric.IPExtendedCommunitiesClientListBySubscriptionResponse) []*armmanagednetworkfabric.IPExtendedCommunity {
					return p.Value
				},
				func(r *armmanagednetworkfabric.IPExtendedCommunity) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagednetworkfabric:IPPrefixes.ListBySubscription", TypeManagedNetworkFabricIPPrefix, sub, st, scanID,
				ippClient.NewListBySubscriptionPager(nil),
				func(p armmanagednetworkfabric.IPPrefixesClientListBySubscriptionResponse) []*armmanagednetworkfabric.IPPrefix {
					return p.Value
				},
				func(r *armmanagednetworkfabric.IPPrefix) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagednetworkfabric:L2IsolationDomains.ListBySubscription", TypeManagedNetworkFabricL2IsolationDom, sub, st, scanID,
				l2Client.NewListBySubscriptionPager(nil),
				func(p armmanagednetworkfabric.L2IsolationDomainsClientListBySubscriptionResponse) []*armmanagednetworkfabric.L2IsolationDomain {
					return p.Value
				},
				func(r *armmanagednetworkfabric.L2IsolationDomain) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagednetworkfabric:L3IsolationDomains.ListBySubscription", TypeManagedNetworkFabricL3IsolationDom, sub, st, scanID,
				l3Client.NewListBySubscriptionPager(nil),
				func(p armmanagednetworkfabric.L3IsolationDomainsClientListBySubscriptionResponse) []*armmanagednetworkfabric.L3IsolationDomain {
					return p.Value
				},
				func(r *armmanagednetworkfabric.L3IsolationDomain) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagednetworkfabric:NeighborGroups.ListBySubscription", TypeManagedNetworkFabricNeighborGroup, sub, st, scanID,
				ngClient.NewListBySubscriptionPager(nil),
				func(p armmanagednetworkfabric.NeighborGroupsClientListBySubscriptionResponse) []*armmanagednetworkfabric.NeighborGroup {
					return p.Value
				},
				func(r *armmanagednetworkfabric.NeighborGroup) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagednetworkfabric:NetworkDevices.ListBySubscription", TypeManagedNetworkFabricNetworkDevice, sub, st, scanID,
				ndClient.NewListBySubscriptionPager(nil),
				func(p armmanagednetworkfabric.NetworkDevicesClientListBySubscriptionResponse) []*armmanagednetworkfabric.NetworkDevice {
					return p.Value
				},
				func(r *armmanagednetworkfabric.NetworkDevice) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagednetworkfabric:NetworkFabricControllers.ListBySubscription", TypeManagedNetworkFabricController, sub, st, scanID,
				nfcClient.NewListBySubscriptionPager(nil),
				func(p armmanagednetworkfabric.NetworkFabricControllersClientListBySubscriptionResponse) []*armmanagednetworkfabric.NetworkFabricController {
					return p.Value
				},
				func(r *armmanagednetworkfabric.NetworkFabricController) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagednetworkfabric:NetworkPacketBrokers.ListBySubscription", TypeManagedNetworkFabricPacketBroker, sub, st, scanID,
				npbClient.NewListBySubscriptionPager(nil),
				func(p armmanagednetworkfabric.NetworkPacketBrokersClientListBySubscriptionResponse) []*armmanagednetworkfabric.NetworkPacketBroker {
					return p.Value
				},
				func(r *armmanagednetworkfabric.NetworkPacketBroker) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagednetworkfabric:NetworkRacks.ListBySubscription", TypeManagedNetworkFabricRack, sub, st, scanID,
				nrClient.NewListBySubscriptionPager(nil),
				func(p armmanagednetworkfabric.NetworkRacksClientListBySubscriptionResponse) []*armmanagednetworkfabric.NetworkRack {
					return p.Value
				},
				func(r *armmanagednetworkfabric.NetworkRack) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagednetworkfabric:NetworkTapRules.ListBySubscription", TypeManagedNetworkFabricNetworkTapRule, sub, st, scanID,
				ntrClient.NewListBySubscriptionPager(nil),
				func(p armmanagednetworkfabric.NetworkTapRulesClientListBySubscriptionResponse) []*armmanagednetworkfabric.NetworkTapRule {
					return p.Value
				},
				func(r *armmanagednetworkfabric.NetworkTapRule) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagednetworkfabric:NetworkTaps.ListBySubscription", TypeManagedNetworkFabricNetworkTap, sub, st, scanID,
				ntClient.NewListBySubscriptionPager(nil),
				func(p armmanagednetworkfabric.NetworkTapsClientListBySubscriptionResponse) []*armmanagednetworkfabric.NetworkTap {
					return p.Value
				},
				func(r *armmanagednetworkfabric.NetworkTap) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armmanagednetworkfabric:RoutePolicies.ListBySubscription", TypeManagedNetworkFabricRoutePolicy, sub, st, scanID,
				rpClient.NewListBySubscriptionPager(nil),
				func(p armmanagednetworkfabric.RoutePoliciesClientListBySubscriptionResponse) []*armmanagednetworkfabric.RoutePolicy {
					return p.Value
				},
				func(r *armmanagednetworkfabric.RoutePolicy) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), location: sv(r.Location), tags: r.Tags, full: r}
				})
		},
	}

	var wg sync.WaitGroup
	for _, fn := range phases {
		wg.Go(func() {
			t, n, e := fn()
			addTotals(t, n, e)
		})
	}
	wg.Wait()
	return total, inserted, firstErr
}
