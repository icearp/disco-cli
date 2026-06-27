package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveContainerAppEnvironments,
		EdgeDecl{Source: TypeAppContainersContainerApp, Target: TypeAppContainersManagedEnvironment, Kind: store.RelAttachedTo},
		EdgeDecl{Source: TypeAppContainersManagedEnvironment, Target: TypeNetworkVirtualNetwork, Kind: store.RelAttachedTo},
	)
	registerResolver(resolveContainerAppRegistries,
		EdgeDecl{Source: TypeAppContainersContainerApp, Target: TypeContainerRegistryRegistry, Kind: store.RelUses},
	)
	registerResolver(resolveContainerInstanceVNets,
		EdgeDecl{Source: TypeContainerInstanceContainerGroup, Target: TypeNetworkVirtualNetwork, Kind: store.RelAttachedTo},
	)
}

// resolveContainerAppEnvironments derives:
//   - app -[attached-to]-> managed-environment via properties.managedEnvironmentId
//   - environment -[attached-to]-> VNet via properties.vnetConfiguration.infrastructureSubnetId
//
// Both edges are case-insensitive on the ARM ID.
func resolveContainerAppEnvironments(sub *subscription, st *store.Store) error {
	envs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeAppContainersManagedEnvironment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	envIndex := make(map[string]string, len(envs))
	for _, e := range envs {
		envIndex[strings.ToLower(e.NativeID)] = e.ID
	}

	apps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeAppContainersContainerApp},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, a := range apps {
		var attrs struct {
			Properties *struct {
				ManagedEnvironmentID *string `json:"managedEnvironmentId"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil || attrs.Properties == nil || attrs.Properties.ManagedEnvironmentID == nil {
			continue
		}
		toID, ok := envIndex[strings.ToLower(*attrs.Properties.ManagedEnvironmentID)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(a.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert containerapp→env: %w", err)
		}
	}

	for _, e := range envs {
		var attrs struct {
			Properties *struct {
				VnetConfiguration *struct {
					InfrastructureSubnetID *string `json:"infrastructureSubnetId"`
					RuntimeSubnetID        *string `json:"runtimeSubnetId"`
				} `json:"vnetConfiguration"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(e.AttributesJSON), &attrs); err != nil || attrs.Properties == nil || attrs.Properties.VnetConfiguration == nil {
			continue
		}
		seen := map[string]bool{}
		for _, subnet := range []*string{attrs.Properties.VnetConfiguration.InfrastructureSubnetID, attrs.Properties.VnetConfiguration.RuntimeSubnetID} {
			if subnet == nil {
				continue
			}
			vnetID := vnetIDFromSubnetID(*subnet)
			if vnetID == "" || seen[vnetID] {
				continue
			}
			seen[vnetID] = true
			vnetResourceID := store.ResourceID("azure", sub.ID, TypeNetworkVirtualNetwork, vnetID)
			if _, err := st.GetResource(vnetResourceID); err != nil {
				continue
			}
			if err := st.UpsertRelationship(e.ID, vnetResourceID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert env→vnet: %w", err)
			}
		}
	}
	return nil
}

// resolveContainerAppRegistries derives container-app -[uses]-> ACR via the
// properties.configuration.registries[].server reference. The server field is
// the ACR login-server FQDN (e.g. "myreg.azurecr.io"); the resolver matches
// the leading subdomain against a per-sub registry-name index.
func resolveContainerAppRegistries(sub *subscription, st *store.Store) error {
	apps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeAppContainersContainerApp},
		Limit: util.AllResources,
	})
	if err != nil || len(apps) == 0 {
		return err
	}
	registries, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeContainerRegistryRegistry},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	regByName := make(map[string]string, len(registries))
	for _, reg := range registries {
		regByName[strings.ToLower(nameFromID(reg.NativeID))] = reg.ID
	}
	if len(regByName) == 0 {
		return nil
	}
	for _, a := range apps {
		var attrs struct {
			Properties *struct {
				Configuration *struct {
					Registries []struct {
						Server *string `json:"server"`
					} `json:"registries"`
				} `json:"configuration"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil || attrs.Properties == nil || attrs.Properties.Configuration == nil {
			continue
		}
		seen := map[string]bool{}
		for _, reg := range attrs.Properties.Configuration.Registries {
			if reg.Server == nil {
				continue
			}
			host := strings.ToLower(*reg.Server)
			// ACR login-server suffixes across clouds.
			var name string
			for _, suffix := range []string{".azurecr.io", ".azurecr.us", ".azurecr.cn", ".azurecr.de"} {
				if strings.HasSuffix(host, suffix) {
					name = host[:len(host)-len(suffix)]
					break
				}
			}
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			toID, ok := regByName[name]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(a.ID, toID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert containerapp→acr: %w", err)
			}
		}
	}
	return nil
}

// resolveContainerInstanceVNets derives container-group -[attached-to]-> VNet
// edges via properties.subnetIds[].id.
func resolveContainerInstanceVNets(sub *subscription, st *store.Store) error {
	groups, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeContainerInstanceContainerGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, g := range groups {
		var attrs struct {
			Properties *struct {
				SubnetIDs []struct {
					ID *string `json:"id"`
				} `json:"subnetIds"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(g.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
			continue
		}
		seen := map[string]bool{}
		for _, s := range attrs.Properties.SubnetIDs {
			if s.ID == nil {
				continue
			}
			vnetID := vnetIDFromSubnetID(*s.ID)
			if vnetID == "" || seen[vnetID] {
				continue
			}
			seen[vnetID] = true
			vnetResourceID := store.ResourceID("azure", sub.ID, TypeNetworkVirtualNetwork, vnetID)
			if _, err := st.GetResource(vnetResourceID); err != nil {
				continue
			}
			if err := st.UpsertRelationship(g.ID, vnetResourceID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert aci→vnet: %w", err)
			}
		}
	}
	return nil
}
