package azure

import (
	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveVMRelationships) }

func resolveVMRelationships(sub *subscription, st *store.Store) error {
	vms, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeVirtualMachine},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range vms {
		// Extract NIC IDs from the VM properties to find connected VNet/NSG.
		// We only have NIC resource IDs here; the actual VNet/NSG links are on
		// the NIC itself (not scanned in phase 1). Record the NIC relationship
		// so it can be joined later when NICs are added to the scanner.
		_ = r // relationships to NICs would go here
	}
	return nil
}
