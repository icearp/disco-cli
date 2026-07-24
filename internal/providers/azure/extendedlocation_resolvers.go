package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	// resolveExtendedLocationConsumers is a cross-cutting central resolver: its
	// source is EVERY resource type carrying a top-level extendedLocation
	// envelope, so per-type EdgeDecl.Source enumeration is meaningless. Left
	// unannotated deliberately (mirrors resolveManagedIdentityConsumers).
	registerResolver(resolveExtendedLocationConsumers)
	registerResolver(resolveCustomLocationRelationships,
		EdgeDecl{Source: TypeCustomLocation, Target: TypeKubernetesConnectedCluster, Kind: store.RelUses},
		EdgeDecl{Source: TypeCustomLocation, Target: TypeResourceConnectorAppliance, Kind: store.RelUses},
		EdgeDecl{Source: TypeCustomLocation, Target: TypeContainerServiceManagedCluster, Kind: store.RelUses},
	)
}

// resolveExtendedLocationConsumers derives consumer -[uses]-> custom-location
// edges for any Azure resource carrying the top-level ARM `extendedLocation`
// envelope (`{"extendedLocation":{"name":"<customLocationArmId>","type":"CustomLocation"}}`).
// Provider-agnostic like the managed-identity consumer resolver: any scanner
// storing its native SDK response verbatim (Arc data controllers, hybrid
// logical networks, SCVMM/VMware inventory, ...) is picked up automatically.
func resolveExtendedLocationConsumers(sub *subscription, st *store.Store) error {
	clByID, err := nativeIDIndex(sub, st, TypeCustomLocation)
	if err != nil || len(clByID) == 0 {
		return err
	}
	all, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range all {
		if r.Type == TypeCustomLocation || r.AttributesJSON == "" {
			continue
		}
		var attrs struct {
			ExtendedLocation *struct {
				Name *string `json:"name"`
			} `json:"extendedLocation"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil ||
			attrs.ExtendedLocation == nil || attrs.ExtendedLocation.Name == nil {
			continue
		}
		toID, ok := clByID[strings.ToLower(*attrs.ExtendedLocation.Name)]
		if !ok || toID == r.ID {
			continue
		}
		if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert consumer→custom-location: %w", err)
		}
	}
	return nil
}

// resolveCustomLocationRelationships wires a custom location to its backing
// host cluster via properties.hostResourceId — a connected cluster
// (Microsoft.Kubernetes), an Arc appliance (Microsoft.ResourceConnector), or an
// AKS managed cluster.
func resolveCustomLocationRelationships(sub *subscription, st *store.Store) error {
	locations, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeCustomLocation},
		Limit: util.AllResources,
	})
	if err != nil || len(locations) == 0 {
		return err
	}
	hostByID := map[string]string{}
	for _, t := range []string{TypeKubernetesConnectedCluster, TypeResourceConnectorAppliance, TypeContainerServiceManagedCluster} {
		idx, err := nativeIDIndex(sub, st, t)
		if err != nil {
			return err
		}
		for k, v := range idx {
			hostByID[k] = v
		}
	}
	for _, l := range locations {
		var attrs struct {
			Properties *struct {
				HostResourceID *string `json:"hostResourceId"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(l.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
			continue
		}
		if ref := attrs.Properties.HostResourceID; ref != nil {
			if toID, ok := hostByID[strings.ToLower(*ref)]; ok {
				if err := st.UpsertRelationship(l.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert custom-location→host: %w", err)
				}
			}
		}
	}
	return nil
}
