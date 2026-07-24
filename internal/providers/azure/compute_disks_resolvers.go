package azure

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(resolveSnapshotSourceRelationships,
		EdgeDecl{Source: TypeComputeSnapshot, Target: TypeComputeManagedDisk, Kind: store.RelAttachedTo},
		EdgeDecl{Source: TypeComputeSnapshot, Target: TypeComputeSnapshot, Kind: store.RelAttachedTo},
	)
	registerResolver(resolveDiskEncryptionSetRelationships,
		EdgeDecl{Source: TypeComputeManagedDisk, Target: TypeComputeDiskEncryptionSet, Kind: store.RelAttachedTo},
	)
	registerResolver(resolveDiskSourceRelationships,
		EdgeDecl{Source: TypeComputeManagedDisk, Target: TypeComputeManagedDisk, Kind: store.RelAttachedTo},
		EdgeDecl{Source: TypeComputeManagedDisk, Target: TypeComputeSnapshot, Kind: store.RelAttachedTo},
	)
}

// resolveSnapshotSourceRelationships adds an attached-to edge from each snapshot to
// its source disk, read from properties.creationData.sourceResourceId in the
// snapshot's stored attributes JSON.
func resolveSnapshotSourceRelationships(sub *subscription, st *store.Store) error {
	snapshots, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
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
		// Source may be a managed disk or another snapshot; one lookup by
		// native id finds it regardless of type (skip if not in the store).
		sourceID := store.ResourceID("azure", sub.ID, sourceNativeID)
		if _, err := st.GetResource(sourceID); err != nil {
			continue
		}
		if err := st.UpsertRelationship(r.ID, sourceID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert snapshot→source relationship: %w", err)
		}
	}
	return nil
}

// resolveDiskEncryptionSetRelationships adds an attached-to edge from each managed
// disk to its disk encryption set, read from properties.encryption.diskEncryptionSetId
// in the disk's stored attributes.
func resolveDiskEncryptionSetRelationships(sub *subscription, st *store.Store) error {
	disks, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
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
		desID := store.ResourceID("azure", sub.ID, *attrs.Properties.Encryption.DiskEncryptionSetID)
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

// resolveDiskSourceRelationships adds an attached-to edge from each managed disk to
// its source disk or snapshot, read from properties.creationData.sourceResourceId in
// the disk's stored attributes.
func resolveDiskSourceRelationships(sub *subscription, st *store.Store) error {
	disks, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
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
		// Source may be a managed disk or a snapshot; one lookup by native
		// id finds it regardless of type (skip if not in the store).
		sourceID := store.ResourceID("azure", sub.ID, sourceNativeID)
		if _, err := st.GetResource(sourceID); err != nil {
			continue
		}
		if err := st.UpsertRelationship(r.ID, sourceID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert disk→source relationship: %w", err)
		}
	}
	return nil
}
