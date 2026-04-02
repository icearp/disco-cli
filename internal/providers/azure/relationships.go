package azure

import (
	"context"
	"encoding/json"
	"math"
	"strings"

	"codeburg.org/icearp/disco/internal/store"
)

const allResources = uint64(math.MaxUint32)

// resolveRelationships is phase 2 for Azure: derive edges between resources
// that have already been written to the DB.
func resolveRelationships(_ context.Context, sub *subscription, st *store.Store) error {
	if err := resolveVMRelationships(sub, st); err != nil {
		return err
	}
	if err := resolveSubnetVNetRelationships(sub, st); err != nil {
		return err
	}
	return resolveAKSRelationships(sub, st)
}

func resolveVMRelationships(sub *subscription, st *store.Store) error {
	vms, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{"azure:compute:virtual-machine"},
		Limit: allResources,
	})
	if err != nil {
		return err
	}
	for _, r := range vms {
		// Extract NIC IDs from the VM properties to find connected VNet/NSG.
		var attrs struct {
			Properties *struct {
				NetworkProfile *struct {
					NetworkInterfaces []struct {
						ID *string `json:"id"`
					} `json:"networkInterfaces"`
				} `json:"networkProfile"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		// We only have NIC resource IDs here; the actual VNet/NSG links are on
		// the NIC itself (not scanned in phase 1). Record the NIC relationship
		// so it can be joined later when NICs are added to the scanner.
		_ = attrs // relationships to NICs would go here
	}
	return nil
}

func resolveSubnetVNetRelationships(sub *subscription, st *store.Store) error {
	subnets, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{"azure:network:subnet"},
		Limit: allResources,
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
			vnetResourceID := store.ResourceID("azure", sub.ID, "azure:network:virtual-network", vnetID)
			_ = st.UpsertRelationship(r.ID, vnetResourceID, store.RelAttachedTo, "directed", nil)
		}
	}
	return nil
}

// vnetIDFromSubnetID extracts the VNet resource ID from a subnet resource ID.
func vnetIDFromSubnetID(subnetID string) string {
	lower := strings.ToLower(subnetID)
	idx := strings.Index(lower, "/subnets/")
	if idx < 0 {
		return ""
	}
	return subnetID[:idx]
}

func resolveAKSRelationships(sub *subscription, st *store.Store) error {
	clusters, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{"azure:containerservice:managed-cluster"},
		Limit: allResources,
	})
	if err != nil {
		return err
	}
	for _, r := range clusters {
		var attrs struct {
			Properties *struct {
				AgentPoolProfiles []struct {
					VnetSubnetID *string `json:"vnetSubnetID"`
				} `json:"agentPoolProfiles"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil {
			continue
		}
		seen := map[string]bool{}
		for _, ap := range attrs.Properties.AgentPoolProfiles {
			if ap.VnetSubnetID == nil {
				continue
			}
			// Extract the VNet ID from the subnet ID.
			vnetID := vnetIDFromSubnetID(*ap.VnetSubnetID)
			if vnetID == "" || seen[vnetID] {
				continue
			}
			seen[vnetID] = true
			vnetResourceID := store.ResourceID("azure", sub.ID, "azure:network:virtual-network", vnetID)
			_ = st.UpsertRelationship(r.ID, vnetResourceID, store.RelAttachedTo, "directed", nil)
		}
	}
	return nil
}
