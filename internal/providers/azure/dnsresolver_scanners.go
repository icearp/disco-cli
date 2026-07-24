package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dnsresolver/armdnsresolver"
)

// Azure DNS Private Resolver lives under the microsoft.network/* ARM namespace
// but ships in its own SDK module (armdnsresolver), registering as its own
// disco service. All four types expose a subscription-wide NewListPager.
func init() {
	registerType(restype.Descriptor{Type: TypeNetworkDNSResolver, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkDNSForwardingRuleset, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkDNSResolverDomainList, Service: "microsoft.network", Leaf: true})
	registerType(restype.Descriptor{Type: TypeNetworkDNSResolverPolicy, Service: "microsoft.network", Leaf: true})
}

func scanDNSResolver(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	resolvers, err := armdnsresolver.NewDNSResolversClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdnsresolver:NewDNSResolversClient: %w", err)
	}
	rulesets, err := armdnsresolver.NewDNSForwardingRulesetsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdnsresolver:NewDNSForwardingRulesetsClient: %w", err)
	}
	domainLists, err := armdnsresolver.NewDomainListsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdnsresolver:NewDomainListsClient: %w", err)
	}
	policies, err := armdnsresolver.NewPoliciesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armdnsresolver:NewPoliciesClient: %w", err)
	}

	phases := []func() (int, int, error){
		func() (int, int, error) {
			return azSimpleScan(ctx, "armdnsresolver:DNSResolvers.List", TypeNetworkDNSResolver, sub, st, scanID,
				resolvers.NewListPager(nil),
				func(p armdnsresolver.DNSResolversClientListResponse) []*armdnsresolver.DNSResolver { return p.Value },
				func(r *armdnsresolver.DNSResolver) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armdnsresolver:DNSForwardingRulesets.List", TypeNetworkDNSForwardingRuleset, sub, st, scanID,
				rulesets.NewListPager(nil),
				func(p armdnsresolver.DNSForwardingRulesetsClientListResponse) []*armdnsresolver.DNSForwardingRuleset {
					return p.Value
				},
				func(r *armdnsresolver.DNSForwardingRuleset) azTrackedBase {
					return netBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armdnsresolver:DomainLists.List", TypeNetworkDNSResolverDomainList, sub, st, scanID,
				domainLists.NewListPager(nil),
				func(p armdnsresolver.DomainListsClientListResponse) []*armdnsresolver.DomainList { return p.Value },
				func(r *armdnsresolver.DomainList) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armdnsresolver:Policies.List", TypeNetworkDNSResolverPolicy, sub, st, scanID,
				policies.NewListPager(nil),
				func(p armdnsresolver.PoliciesClientListResponse) []*armdnsresolver.Policy { return p.Value },
				func(r *armdnsresolver.Policy) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
	}
	return azRunPhases(phases...)
}
