package azure

import (
	"encoding/json"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveVMAvailabilitySetRelationships)
	registerResolver(resolveVMProximityGroupRelationships)
	registerResolver(resolveSnapshotSourceRelationships)
}

// resolveVMAvailabilitySetRelationships adds an attached-to edge from each VM to
// its availability set, derived from the availabilitySet.id field in the VM's
// stored attributes JSON.
func resolveVMAvailabilitySetRelationships(sub *subscription, st *store.Store) error {
	vms, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
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
		availID := store.ResourceID("azure", sub.ID, TypeComputeAvailabilitySet, *attrs.Properties.AvailabilitySet.ID)
		if err := st.UpsertRelationship(r.ID, availID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert vm→availabilitySet relationship: %w", err)
		}
	}
	return nil
}

// resolveVMProximityGroupRelationships adds an attached-to edge from each VM to
// its proximity placement group, derived from proximityPlacementGroup.id in
// the VM's stored attributes JSON.
func resolveVMProximityGroupRelationships(sub *subscription, st *store.Store) error {
	vms, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
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
		ppgID := store.ResourceID("azure", sub.ID, TypeComputeProximityPlacementGroup, *attrs.Properties.ProximityPlacementGroup.ID)
		if err := st.UpsertRelationship(r.ID, ppgID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert vm→proximityPlacementGroup relationship: %w", err)
		}
	}
	return nil
}

// resolveSnapshotSourceRelationships adds an attached-to edge from each snapshot
// to its source disk, derived from properties.creationData.sourceResourceId in
// the snapshot's stored attributes JSON.
func resolveSnapshotSourceRelationships(sub *subscription, st *store.Store) error {
	snapshots, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeComputeSnapshot},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}

	var attrs struct {
		Properties *struct {
			CreationData *struct {
				SourceResourceID *string `json:"sourceResourceId"`
			} `json:"creationData"`
		} `json:"properties"`
	}

	for _, r := range snapshots {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.CreationData == nil || attrs.Properties.CreationData.SourceResourceID == nil {
			continue
		}
		sourceNativeID := *attrs.Properties.CreationData.SourceResourceID
		// Source may be a managed disk or another snapshot. Try both; skip if
		// neither is in the store (e.g. external or unscanned resource).
		var sourceID string
		for _, rtype := range []string{TypeComputeManagedDisk, TypeComputeSnapshot} {
			candidate := store.ResourceID("azure", sub.ID, rtype, sourceNativeID)
			if _, err := st.GetResource(candidate); err == nil {
				sourceID = candidate
				break
			}
		}
		if sourceID == "" {
			continue
		}
		if err := st.UpsertRelationship(r.ID, sourceID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert snapshot→source relationship: %w", err)
		}
	}
	return nil
}
