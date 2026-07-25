package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/policyinsights/armpolicyinsights"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypePolicyInsightsRemediation, Service: "microsoft.policyinsights", Leaf: true})
	registerType(restype.Descriptor{Type: TypePolicyInsightsAttestation, Service: "microsoft.policyinsights", Leaf: true})
	registerService(serviceEntry{
		name: "azure:microsoft.policyinsights",
		fn:   scanPolicyInsights,
	})
}

// scanPolicyInsights discovers Azure Policy remediation tasks and attestations
// across the subscription.
func scanPolicyInsights(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
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
