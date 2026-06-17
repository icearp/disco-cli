package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveSubnetVNetRelationships)
	registerResolver(resolveApplicationGatewayRelationships)
}

func resolveSubnetVNetRelationships(sub *subscription, st *store.Store) error {
	subnets, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeNetworkSubnet},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range subnets {
		// Subnet NativeID is /subscriptions/{sub}/resourceGroups/{rg}/providers/
		// Microsoft.Network/virtualNetworks/{vnet}/subnets/{subnet}.
		// The VNet ID is the parent path up to /subnets/.
		vnetID := vnetIDFromSubnetID(r.NativeID)
		if vnetID != "" {
			vnetResourceID := store.ResourceID("azure", sub.ID, TypeNetworkVirtualNetwork, vnetID)
			if err := st.UpsertRelationship(r.ID, vnetResourceID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert subnet→vnet relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveApplicationGatewayRelationships derives three edge classes per AGW:
//   - AGW -[attached-to]-> VNet via gatewayIPConfigurations[].properties.subnet.id
//   - AGW -[uses]-> Public IP via frontendIPConfigurations[].properties.publicIPAddress.id
//   - AGW -[uses]-> Key Vault via sslCertificates[].properties.keyVaultSecretId
//     (Key Vault reference URI — pointer, not material). Reference URIs now
//     pass the sanitizer allowlist (`store/sanitize.go::isReferenceURI`).
//
// Backend pool members (FQDN/IP addresses, NIC refs) deferred — AGW backends
// are usually FQDNs which don't map cleanly to ARM IDs. Identity → MSI edges
// covered by the generic consumer resolver.
// agwAttrs mirrors the AGW fields the resolver walks.
type agwAttrs struct {
	Properties *struct {
		GatewayIPConfigurations []struct {
			Properties *struct {
				Subnet *struct {
					ID *string `json:"id"`
				} `json:"subnet"`
			} `json:"properties"`
		} `json:"gatewayIPConfigurations"`
		FrontendIPConfigurations []struct {
			Properties *struct {
				PublicIPAddress *struct {
					ID *string `json:"id"`
				} `json:"publicIPAddress"`
			} `json:"properties"`
		} `json:"frontendIPConfigurations"`
		SSLCertificates []struct {
			Properties *struct {
				KeyVaultSecretID *string `json:"keyVaultSecretId"`
			} `json:"properties"`
		} `json:"sslCertificates"`
	} `json:"properties"`
}

// agwTargetSets bundles AGW resolver indexes (PIP NativeID → id, vault
// name → id).
type agwTargetSets struct {
	pipIndex    map[string]string
	vaultByName map[string]string
}

func resolveApplicationGatewayRelationships(sub *subscription, st *store.Store) error {
	gws, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeNetworkApplicationGateway},
		Limit: util.AllResources,
	})
	if err != nil || len(gws) == 0 {
		return err
	}
	sets, err := loadAGWTargetSets(sub, st)
	if err != nil {
		return err
	}
	for _, g := range gws {
		var attrs agwAttrs
		if err := json.Unmarshal([]byte(g.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
			continue
		}
		if err := emitAGWVNetEdges(st, sub, g, attrs); err != nil {
			return err
		}
		if err := emitAGWPIPEdges(st, g, attrs, sets); err != nil {
			return err
		}
		if err := emitAGWVaultEdges(st, g, attrs, sets); err != nil {
			return err
		}
	}
	return nil
}

func loadAGWTargetSets(sub *subscription, st *store.Store) (agwTargetSets, error) {
	var sets agwTargetSets
	pips, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeNetworkPublicIPAddress},
		Limit: util.AllResources,
	})
	if err != nil {
		return sets, err
	}
	sets.pipIndex = make(map[string]string, len(pips))
	for _, p := range pips {
		sets.pipIndex[strings.ToLower(p.NativeID)] = p.ID
	}
	sets.vaultByName, err = vaultNameIndex(sub, st)
	if err != nil {
		return sets, err
	}
	return sets, nil
}

func emitAGWVNetEdges(st *store.Store, sub *subscription, g store.Resource, attrs agwAttrs) error {
	seen := map[string]bool{}
	for _, ipc := range attrs.Properties.GatewayIPConfigurations {
		if ipc.Properties == nil || ipc.Properties.Subnet == nil || ipc.Properties.Subnet.ID == nil {
			continue
		}
		vnetID := vnetIDFromSubnetID(*ipc.Properties.Subnet.ID)
		if vnetID == "" || seen[vnetID] {
			continue
		}
		seen[vnetID] = true
		vnetResourceID := store.ResourceID("azure", sub.ID, TypeNetworkVirtualNetwork, vnetID)
		if _, err := st.GetResource(vnetResourceID); err != nil {
			continue
		}
		if err := st.UpsertRelationship(g.ID, vnetResourceID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert agw→vnet: %w", err)
		}
	}
	return nil
}

func emitAGWPIPEdges(st *store.Store, g store.Resource, attrs agwAttrs, sets agwTargetSets) error {
	seen := map[string]bool{}
	for _, fipc := range attrs.Properties.FrontendIPConfigurations {
		if fipc.Properties == nil || fipc.Properties.PublicIPAddress == nil || fipc.Properties.PublicIPAddress.ID == nil {
			continue
		}
		key := strings.ToLower(*fipc.Properties.PublicIPAddress.ID)
		if seen[key] {
			continue
		}
		seen[key] = true
		toID, ok := sets.pipIndex[key]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(g.ID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert agw→pip: %w", err)
		}
	}
	return nil
}

func emitAGWVaultEdges(st *store.Store, g store.Resource, attrs agwAttrs, sets agwTargetSets) error {
	seen := map[string]bool{}
	for _, sslc := range attrs.Properties.SSLCertificates {
		if sslc.Properties == nil || sslc.Properties.KeyVaultSecretID == nil {
			continue
		}
		vaultName := vaultNameFromKeyURI(*sslc.Properties.KeyVaultSecretID)
		if vaultName == "" || seen[vaultName] {
			continue
		}
		seen[vaultName] = true
		toID, ok := sets.vaultByName[strings.ToLower(vaultName)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(g.ID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert agw→vault: %w", err)
		}
	}
	return nil
}
