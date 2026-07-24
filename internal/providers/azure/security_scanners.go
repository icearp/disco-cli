package azure

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/security/armsecurity"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSecurityPricing, Service: "microsoft.security"})
	registerService(serviceEntry{
		name: "azure:microsoft.security",
		fn:   scanSecurity,
	})
}

// scanSecurity discovers Microsoft Defender for Cloud per-resource-type
// pricing (plan-tier) settings for the subscription — one entry per resource
// type (VirtualMachines, AppServices, SqlServers, KeyVaults, StorageAccounts,
// etc.) with its enabled tier (Free / Standard). Auto-provisioning settings,
// security contacts, workspace settings, assessments, and recommendations
// deferred — config + evaluation surfaces, not edge sources.
func scanSecurity(ctx context.Context, sub *subscription, cred azcore.TokenCredential, st *store.Store, scanID string) (total, inserted int, err error) {
	client, err := armsecurity.NewPricingsClient(cred, azClientOptions)
	if err != nil {
		return 0, 0, fmt.Errorf("armsecurity:NewPricingsClient: %w", err)
	}
	scope := "/subscriptions/" + sub.ID
	resp, err := client.List(ctx, scope, nil)
	if err != nil {
		if isSkippableScanError(err) {
			return 0, 0, skipIfAccessDenied(st, "armsecurity:Pricings.List", sub.ID, err)
		}
		return 0, 0, fmt.Errorf("armsecurity:Pricings.List: %w", err)
	}
	if len(resp.Value) == 0 {
		return 0, 0, nil
	}
	batch := make([]*store.Resource, 0, len(resp.Value))
	for _, p := range resp.Value {
		if p == nil || p.ID == nil {
			continue
		}
		name := sv(p.Name)
		// Defender for Cloud surfaces a pricing row per plan (VirtualMachines,
		// StorageAccounts, ...) on every subscription regardless of whether the
		// customer enabled it. EnablementTime is set only when switched to
		// Standard — its absence marks the row a system-emitted placeholder
		// (Free tier / never-enabled), which we flag managed so `disco
		// resources` / `disco graph` defaults skip them.
		managed := p.Properties == nil || p.Properties.EnablementTime == nil
		batch = append(batch, &store.Resource{
			Provider: "azure", AccountID: sub.ID, AccountName: &sub.Name,
			Type: TypeSecurityPricing, NativeID: sv(p.ID),
			Name:              &name,
			AttributesJSON:    mustJSON(p),
			DiscoveredBy:      scanID,
			ManagedByProvider: managed,
		})
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, fmt.Errorf("upsert security pricings: %w", err)
	}
	return len(batch), n, nil
}
