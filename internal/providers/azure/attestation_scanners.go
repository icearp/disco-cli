package azure

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/attestation/armattestation"
)

func init() {
	registerService(serviceEntry{
		name: "azure:microsoft.attestation",
		fn:   scanAttestation,
		emits: []coverage.TypeDecl{
			// Private-endpoint edges resolved centrally; the provider ships
			// scanner-only.
			{Service: "microsoft.attestation", DiscoType: TypeAttestationProvider, Leaf: true},
		},
	})
}

// scanAttestation discovers Azure Attestation providers. The List op is a
// single non-paginated call returning all providers in the subscription.
func scanAttestation(ctx context.Context, sub *subscription, cred *azidentity.DefaultAzureCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armattestation.NewProvidersClient(sub.ID, cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armattestation:NewProvidersClient: %w", err)
	}
	resp, err := client.List(ctx, nil)
	if err != nil {
		if isSkippableScanError(err) {
			return 0, 0, skipIfAccessDenied(st, "armattestation:Providers.List", sub.ID, err)
		}
		return 0, 0, fmt.Errorf("armattestation:Providers.List: %w", err)
	}
	batch, pairs := azTrackedRows(sub, scanID, TypeAttestationProvider, resp.Value,
		func(p *armattestation.Provider) azTrackedBase {
			return azTrackedBase{id: sv(p.ID), name: sv(p.Name), location: sv(p.Location), tags: p.Tags, full: p}
		})
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert attestation: %w", err)
	}
	if len(pairs) > 0 {
		if err := st.RecordHierarchyBatch(pairs); err != nil {
			return len(batch), n, fmt.Errorf("closure attestation: %w", err)
		}
	}
	return len(batch), n, nil
}
