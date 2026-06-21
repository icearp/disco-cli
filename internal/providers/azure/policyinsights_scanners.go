package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/policyinsights/armpolicyinsights"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.policyinsights",
		fn:   scanPolicyInsights,
		emits: []coverage.TypeDecl{
			{Service: "microsoft.policyinsights", DiscoType: TypePolicyInsightsRemediation, Leaf: true},
			{Service: "microsoft.policyinsights", DiscoType: TypePolicyInsightsAttestation, Leaf: true},
		},
	})
}

// scanPolicyInsights discovers Azure Policy remediation tasks and attestations
// across the subscription.
func scanPolicyInsights(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	return azRunPhases(
		func() (int, int, error) {
			remClient, err := armpolicyinsights.NewRemediationsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armpolicyinsights:NewRemediationsClient: %w", err)
			}
			return azSimpleScan(ctx, "armpolicyinsights:Remediations.ListForSubscription", TypePolicyInsightsRemediation, sub, st, scanID,
				remClient.NewListForSubscriptionPager(nil),
				func(p armpolicyinsights.RemediationsClientListForSubscriptionResponse) []*armpolicyinsights.Remediation {
					return p.Value
				},
				func(r *armpolicyinsights.Remediation) azTrackedBase {
					return azTrackedBase{id: sv(r.ID), name: sv(r.Name), full: r}
				})
		},
		func() (int, int, error) {
			attClient, err := armpolicyinsights.NewAttestationsClient(sub.ID, cred, azClientOptions)
			if err != nil {
				return 0, 0, fmt.Errorf("armpolicyinsights:NewAttestationsClient: %w", err)
			}
			return azSimpleScan(ctx, "armpolicyinsights:Attestations.ListForSubscription", TypePolicyInsightsAttestation, sub, st, scanID,
				attClient.NewListForSubscriptionPager(nil),
				func(p armpolicyinsights.AttestationsClientListForSubscriptionResponse) []*armpolicyinsights.Attestation {
					return p.Value
				},
				func(a *armpolicyinsights.Attestation) azTrackedBase {
					return azTrackedBase{id: sv(a.ID), name: sv(a.Name), full: a}
				})
		},
	)
}
