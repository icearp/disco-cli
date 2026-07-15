package azure

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveVMAvailabilitySetRelationships,
		EdgeDecl{Source: TypeComputeVirtualMachine, Target: TypeComputeAvailabilitySet, Kind: store.RelAttachedTo},
	)
	registerResolver(resolveVMProximityGroupRelationships,
		EdgeDecl{Source: TypeComputeVirtualMachine, Target: TypeComputeProximityPlacementGroup, Kind: store.RelAttachedTo},
	)
	registerResolver(resolveVMExtensionRelationships,
		EdgeDecl{Source: TypeComputeVMExtension, Target: TypeComputeVirtualMachine, Kind: store.RelAttachedTo},
	)
	registerResolver(resolveImageSourceVMRelationships,
		EdgeDecl{Source: TypeComputeImage, Target: TypeComputeVirtualMachine, Kind: store.RelAttachedTo},
	)
	registerResolver(resolveRestorePointCollectionSourceRelationships,
		EdgeDecl{Source: TypeComputeRestorePointCollection, Target: TypeComputeVirtualMachine, Kind: store.RelAttachedTo},
	)
}

// resolveVMAvailabilitySetRelationships adds an attached-to edge from each VM to its
// availability set, from availabilitySet.id in the VM's stored attributes JSON.
func resolveVMAvailabilitySetRelationships(sub *subscription, st *store.Store) error {
	vms, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
		AccountID: sub.ID,
		Types:     []string{TypeComputeVirtualMachine},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}

	var attrs struct {
		Properties *struct {
			AvailabilitySet *struct {
				ID *string `json:"id"`
			} `json:"availabilitySet"`
		} `json:"properties"`
	}

	for _, r := range vms {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.AvailabilitySet == nil || attrs.Properties.AvailabilitySet.ID == nil {
			continue
		}
		availID := store.ResourceID("azure", sub.ID, *attrs.Properties.AvailabilitySet.ID)
		if err := st.UpsertRelationship(r.ID, availID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert vm→availabilitySet relationship: %w", err)
		}
	}
	return nil
}

// resolveVMProximityGroupRelationships adds an attached-to edge from each VM to its
// proximity placement group, from proximityPlacementGroup.id in the VM's stored attributes JSON.
func resolveVMProximityGroupRelationships(sub *subscription, st *store.Store) error {
	vms, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
		AccountID: sub.ID,
		Types:     []string{TypeComputeVirtualMachine},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}

	var attrs struct {
		Properties *struct {
			ProximityPlacementGroup *struct {
				ID *string `json:"id"`
			} `json:"proximityPlacementGroup"`
		} `json:"properties"`
	}

	for _, r := range vms {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.ProximityPlacementGroup == nil || attrs.Properties.ProximityPlacementGroup.ID == nil {
			continue
		}
		ppgID := store.ResourceID("azure", sub.ID, *attrs.Properties.ProximityPlacementGroup.ID)
		if err := st.UpsertRelationship(r.ID, ppgID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert vm→proximityPlacementGroup relationship: %w", err)
		}
	}
	return nil
}

// resolveVMExtensionRelationships derives each extension's parent VM by truncating
// NativeID at "/extensions/". NativeID form: .../virtualMachines/{vm}/extensions/{ext}
func resolveVMExtensionRelationships(sub *subscription, st *store.Store) error {
	exts, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
		AccountID: sub.ID,
		Types:     []string{TypeComputeVMExtension},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range exts {
		vmNativeID := truncateAtSegment(r.NativeID, "/extensions/")
		if vmNativeID == "" {
			continue
		}
		vmID := store.ResourceID("azure", sub.ID, vmNativeID)
		if err := st.UpsertRelationship(r.ID, vmID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert vmExtension→vm relationship: %w", err)
		}
	}
	return nil
}

// resolveImageSourceVMRelationships adds an attached-to edge from each custom image
// to its source VM, from properties.sourceVirtualMachine.id in stored attributes JSON.
func resolveImageSourceVMRelationships(sub *subscription, st *store.Store) error {
	images, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
		AccountID: sub.ID,
		Types:     []string{TypeComputeImage},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}

	var attrs struct {
		Properties *struct {
			SourceVirtualMachine *struct {
				ID *string `json:"id"`
			} `json:"sourceVirtualMachine"`
		} `json:"properties"`
	}

	for _, r := range images {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.SourceVirtualMachine == nil || attrs.Properties.SourceVirtualMachine.ID == nil {
			continue
		}
		vmID := store.ResourceID("azure", sub.ID, *attrs.Properties.SourceVirtualMachine.ID)
		// Source VM may have been deleted after image capture; skip if not in store.
		if _, err := st.GetResource(vmID); err != nil {
			continue
		}
		if err := st.UpsertRelationship(r.ID, vmID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert image→sourceVM relationship: %w", err)
		}
	}
	return nil
}

// resolveRestorePointCollectionSourceRelationships adds an attached-to edge from each
// restore point collection to its source VM, from properties.source.id in stored attributes JSON.
func resolveRestorePointCollectionSourceRelationships(sub *subscription, st *store.Store) error {
	rpcs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
		AccountID: sub.ID,
		Types:     []string{TypeComputeRestorePointCollection},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}

	var attrs struct {
		Properties *struct {
			Source *struct {
				ID *string `json:"id"`
			} `json:"source"`
		} `json:"properties"`
	}

	for _, r := range rpcs {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.Source == nil || attrs.Properties.Source.ID == nil {
			continue
		}
		vmID := store.ResourceID("azure", sub.ID, *attrs.Properties.Source.ID)
		// Source VM may have been deleted; skip if not in store.
		if _, err := st.GetResource(vmID); err != nil {
			continue
		}
		if err := st.UpsertRelationship(r.ID, vmID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert restorePointCollection→sourceVM relationship: %w", err)
		}
	}
	return nil
}
