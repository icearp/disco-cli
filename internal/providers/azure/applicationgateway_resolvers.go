package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveApplicationGatewayRelationships) }

// resolveApplicationGatewayRelationships derives three edge classes per AGW:
//   - AGW -[attached-to]-> VNet via gatewayIPConfigurations[].properties.subnet.id
//   - AGW -[uses]-> Public IP via frontendIPConfigurations[].properties.publicIPAddress.id
//   - AGW -[uses]-> Key Vault via sslCertificates[].properties.keyVaultSecretId
//     (Key Vault reference URI — pointer, not material). Reference URIs now
//     pass the sanitizer allowlist (`internal/store/sanitize.go::isReferenceURI`).
//
// Backend pool members (FQDN/IP addresses, NIC refs) deferred — AGW backends
// are usually FQDNs which don't map cleanly to ARM IDs. Identity → MSI edges
// covered by the generic consumer resolver.
func resolveApplicationGatewayRelationships(sub *subscription, st *store.Store) error {
	gws, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeNetworkApplicationGateway},
		Limit: util.AllResources,
	})
	if err != nil || len(gws) == 0 {
		return err
	}

	pips, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeNetworkPublicIPAddress},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	pipIndex := make(map[string]string, len(pips))
	for _, p := range pips {
		pipIndex[strings.ToLower(p.NativeID)] = p.ID
	}

	vaults, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeKeyVaultVault},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	vaultByName := make(map[string]string, len(vaults))
	for _, v := range vaults {
		vaultByName[strings.ToLower(nameFromID(v.NativeID))] = v.ID
	}

	for _, g := range gws {
		var attrs struct {
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
		if err := json.Unmarshal([]byte(g.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
			continue
		}

		// AGW → VNet (via subnet path).
		seenVNet := map[string]bool{}
		for _, ipc := range attrs.Properties.GatewayIPConfigurations {
			if ipc.Properties == nil || ipc.Properties.Subnet == nil || ipc.Properties.Subnet.ID == nil {
				continue
			}
			vnetID := vnetIDFromSubnetID(*ipc.Properties.Subnet.ID)
			if vnetID == "" || seenVNet[vnetID] {
				continue
			}
			seenVNet[vnetID] = true
			vnetResourceID := store.ResourceID("azure", sub.ID, TypeNetworkVirtualNetwork, vnetID)
			if _, err := st.GetResource(vnetResourceID); err != nil {
				continue
			}
			if err := st.UpsertRelationship(g.ID, vnetResourceID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert agw→vnet: %w", err)
			}
		}

		// AGW → Public IP.
		seenPIP := map[string]bool{}
		for _, fipc := range attrs.Properties.FrontendIPConfigurations {
			if fipc.Properties == nil || fipc.Properties.PublicIPAddress == nil || fipc.Properties.PublicIPAddress.ID == nil {
				continue
			}
			key := strings.ToLower(*fipc.Properties.PublicIPAddress.ID)
			if seenPIP[key] {
				continue
			}
			seenPIP[key] = true
			toID, ok := pipIndex[key]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(g.ID, toID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert agw→pip: %w", err)
			}
		}

		// AGW → Key Vault (via sslCertificates[].keyVaultSecretId reference URI).
		seenVault := map[string]bool{}
		for _, sslc := range attrs.Properties.SSLCertificates {
			if sslc.Properties == nil || sslc.Properties.KeyVaultSecretID == nil {
				continue
			}
			vaultName := vaultNameFromKeyURI(*sslc.Properties.KeyVaultSecretID)
			if vaultName == "" || seenVault[vaultName] {
				continue
			}
			seenVault[vaultName] = true
			toID, ok := vaultByName[strings.ToLower(vaultName)]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(g.ID, toID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert agw→vault: %w", err)
			}
		}
	}
	return nil
}
