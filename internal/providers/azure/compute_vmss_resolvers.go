package azure

import (
	"encoding/json"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveVMSSProximityGroupRelationships)
	registerResolver(resolveVMSSExtensionRelationships)
	registerResolver(resolveVMSSVMRelationships)
	registerResolver(resolveVMSSVMExtensionRelationships)
}

// resolveVMSSProximityGroupRelationships adds an attached-to edge from each VMSS
// to its proximity placement group, derived from properties.proximityPlacementGroup.id
// in the VMSS's stored attributes JSON.
func resolveVMSSProximityGroupRelationships(sub *subscription, st *store.Store) error {
	vmsses, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeComputeVMSS},
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

	for _, r := range vmsses {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.ProximityPlacementGroup == nil || attrs.Properties.ProximityPlacementGroup.ID == nil {
			continue
		}
		ppgID := store.ResourceID("azure", sub.ID, TypeComputeProximityPlacementGroup, *attrs.Properties.ProximityPlacementGroup.ID)
		if err := st.UpsertRelationship(r.ID, ppgID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert vmss→proximityPlacementGroup relationship: %w", err)
		}
	}
	return nil
}

// resolveVMSSExtensionRelationships derives the parent VMSS for each VMSS extension
// by truncating the extension's NativeID at "/extensions/".
// NativeID form: .../virtualMachineScaleSets/{vmss}/extensions/{ext}
func resolveVMSSExtensionRelationships(sub *subscription, st *store.Store) error {
	exts, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeComputeVMSSExtension},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range exts {
		vmssNativeID := truncateAtSegment(r.NativeID, "/extensions/")
		if vmssNativeID == "" {
			continue
		}
		vmssID := store.ResourceID("azure", sub.ID, TypeComputeVMSS, vmssNativeID)
		if err := st.UpsertRelationship(r.ID, vmssID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert vmssExtension→vmss relationship: %w", err)
		}
	}
	return nil
}

// resolveVMSSVMRelationships derives the parent VMSS for each VMSS VM instance
// by truncating the VM's NativeID at "/virtualMachines/".
// NativeID form: .../virtualMachineScaleSets/{vmss}/virtualMachines/{instance}
func resolveVMSSVMRelationships(sub *subscription, st *store.Store) error {
	vms, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeComputeVMSSVM},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range vms {
		vmssNativeID := truncateAtSegment(r.NativeID, "/virtualMachines/")
		if vmssNativeID == "" {
			continue
		}
		vmssID := store.ResourceID("azure", sub.ID, TypeComputeVMSS, vmssNativeID)
		if err := st.UpsertRelationship(r.ID, vmssID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert vmssVM→vmss relationship: %w", err)
		}
	}
	return nil
}

// resolveVMSSVMExtensionRelationships derives the parent VMSS VM for each VMSS VM
// extension by truncating the extension's NativeID at "/extensions/".
// NativeID form: .../virtualMachines/{instance}/extensions/{ext}
func resolveVMSSVMExtensionRelationships(sub *subscription, st *store.Store) error {
	exts, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeComputeVMSSVMExtension},
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
		vmID := store.ResourceID("azure", sub.ID, TypeComputeVMSSVM, vmNativeID)
		if err := st.UpsertRelationship(r.ID, vmID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert vmssVMExtension→vmssVM relationship: %w", err)
		}
	}
	return nil
}
