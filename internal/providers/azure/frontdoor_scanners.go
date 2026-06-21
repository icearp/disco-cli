package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/frontdoor/armfrontdoor"
)

// Classic Azure Front Door, its WAF policies/managed-rulesets, and Network
// Experiment (Internet Analyzer) profiles live under the microsoft.network/*
// ARM namespace but in the armfrontdoor module, so they register as their own
// disco service. The managed-ruleset catalogue is Azure-supplied and
// undeletable (managed=true).
func init() {
	registerExtraEmits([]coverage.TypeDecl{
		{Service: "microsoft.network", DiscoType: TypeNetworkFrontDoor, Leaf: true},
		{Service: "microsoft.network", DiscoType: TypeNetworkFrontDoorWAFPolicy, Leaf: true},
		{Service: "microsoft.network", DiscoType: TypeNetworkFrontDoorWAFManagedRuleset, Leaf: true},
		{Service: "microsoft.network", DiscoType: TypeNetworkExperimentProfile, Leaf: true},
	}...)
}

func scanFrontDoor(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	doors, err := armfrontdoor.NewFrontDoorsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armfrontdoor:NewFrontDoorsClient: %w", err)
	}
	wafPolicies, err := armfrontdoor.NewPoliciesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armfrontdoor:NewPoliciesClient: %w", err)
	}
	managedRulesets, err := armfrontdoor.NewManagedRuleSetsClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armfrontdoor:NewManagedRuleSetsClient: %w", err)
	}
	experiments, err := armfrontdoor.NewNetworkExperimentProfilesClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armfrontdoor:NewNetworkExperimentProfilesClient: %w", err)
	}

	phases := []func() (int, int, error){
		func() (int, int, error) {
			return azSimpleScan(ctx, "armfrontdoor:FrontDoors.List", TypeNetworkFrontDoor, sub, st, scanID,
				doors.NewListPager(nil),
				func(p armfrontdoor.FrontDoorsClientListResponse) []*armfrontdoor.FrontDoor { return p.Value },
				func(r *armfrontdoor.FrontDoor) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armfrontdoor:Policies.ListBySubscription", TypeNetworkFrontDoorWAFPolicy, sub, st, scanID,
				wafPolicies.NewListBySubscriptionPager(nil),
				func(p armfrontdoor.PoliciesClientListBySubscriptionResponse) []*armfrontdoor.WebApplicationFirewallPolicy {
					return p.Value
				},
				func(r *armfrontdoor.WebApplicationFirewallPolicy) azTrackedBase {
					return netBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armfrontdoor:ManagedRuleSets.List", TypeNetworkFrontDoorWAFManagedRuleset, sub, st, scanID,
				managedRulesets.NewListPager(nil),
				func(p armfrontdoor.ManagedRuleSetsClientListResponse) []*armfrontdoor.ManagedRuleSetDefinition {
					return p.Value
				},
				func(r *armfrontdoor.ManagedRuleSetDefinition) azTrackedBase {
					return netManagedBase(r.ID, r.Name, r.Location, r.Tags, r)
				})
		},
		func() (int, int, error) {
			return azSimpleScan(ctx, "armfrontdoor:NetworkExperimentProfiles.List", TypeNetworkExperimentProfile, sub, st, scanID,
				experiments.NewListPager(nil),
				func(p armfrontdoor.NetworkExperimentProfilesClientListResponse) []*armfrontdoor.Profile {
					return p.Value
				},
				func(r *armfrontdoor.Profile) azTrackedBase { return netBase(r.ID, r.Name, r.Location, r.Tags, r) })
		},
	}
	return azRunPhases(phases...)
}
