package azure

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveSnapshotSourceRelationships)
	registerResolver(resolveDiskEncryptionSetRelationships)
	registerResolver(resolveDiskSourceRelationships)
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

// resolveDiskEncryptionSetRelationships adds an attached-to edge from each
// managed disk to its disk encryption set, derived from
// properties.encryption.diskEncryptionSetId in the disk's stored attributes.
func resolveDiskEncryptionSetRelationships(sub *subscription, st *store.Store) error {
	disks, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeComputeManagedDisk},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}

	var attrs struct {
		Properties *struct {
			Encryption *struct {
				DiskEncryptionSetID *string `json:"diskEncryptionSetId"`
			} `json:"encryption"`
		} `json:"properties"`
	}

	for _, r := range disks {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.Encryption == nil || attrs.Properties.Encryption.DiskEncryptionSetID == nil {
			continue
		}
		desID := store.ResourceID("azure", sub.ID, TypeComputeDiskEncryptionSet, *attrs.Properties.Encryption.DiskEncryptionSetID)
		// DES may be in another subscription or not yet scanned; skip if not in store.
		if _, err := st.GetResource(desID); err != nil {
			continue
		}
		if err := st.UpsertRelationship(r.ID, desID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert disk→diskEncryptionSet relationship: %w", err)
		}
	}
	return nil
}

// resolveDiskSourceRelationships adds an attached-to edge from each managed
// disk to its source disk or snapshot, derived from
// properties.creationData.sourceResourceId in the disk's stored attributes.
func resolveDiskSourceRelationships(sub *subscription, st *store.Store) error {
	disks, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeComputeManagedDisk},
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

	for _, r := range disks {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.CreationData == nil || attrs.Properties.CreationData.SourceResourceID == nil {
			continue
		}
		sourceNativeID := *attrs.Properties.CreationData.SourceResourceID
		// Source may be a managed disk or a snapshot. Try both; skip if
		// neither is in the store.
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
			return fmt.Errorf("upsert disk→source relationship: %w", err)
		}
	}
	return nil
}
