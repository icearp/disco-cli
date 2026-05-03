package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveFSxChildrenToFileSystem,
		EdgeDecl{TypeFSxVolume, TypeFSxFileSystem, store.RelAttachedTo},
		EdgeDecl{TypeFSxStorageVirtualMachine, TypeFSxFileSystem, store.RelAttachedTo},
		EdgeDecl{TypeFSxDataRepositoryAssociation, TypeFSxFileSystem, store.RelAttachedTo},
	)
	registerResolver(resolveFSxSnapshotToVolume,
		EdgeDecl{TypeFSxSnapshot, TypeFSxVolume, store.RelAttachedTo},
	)
}

func fsxARN(region, acct, kind, id string) string {
	return fmt.Sprintf("arn:aws:fsx:%s:%s:%s/%s", region, acct, kind, id)
}

// resolveFSxChildrenToFileSystem wires per-FS children — volumes, storage
// virtual machines and data-repository-associations — to their parent
// file-system via FileSystemId.
func resolveFSxChildrenToFileSystem(acct *account, st *store.Store) error {
	fsSet, err := scannedIDSet(acct, st, TypeFSxFileSystem)
	if err != nil {
		return err
	}
	for _, t := range []string{TypeFSxVolume, TypeFSxStorageVirtualMachine, TypeFSxDataRepositoryAssociation} {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{t}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				FileSystemId *string `json:"FileSystemId"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			fsID := sv(attrs.FileSystemId)
			if fsID == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeFSxFileSystem, fsxARN(sv(r.Region), acct.ID, "file-system", fsID))
			if !fsSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert fsx %s→fs: %w", t, err)
			}
		}
	}
	return nil
}

// resolveFSxSnapshotToVolume wires each snapshot to the volume it was taken
// from via VolumeId.
func resolveFSxSnapshotToVolume(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeFSxSnapshot}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	volSet, err := scannedIDSet(acct, st, TypeFSxVolume)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			VolumeId *string `json:"VolumeId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		vid := sv(attrs.VolumeId)
		if vid == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeFSxVolume, fsxARN(sv(r.Region), acct.ID, "volume", vid))
		if !volSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert fsx snapshot→volume: %w", err)
		}
	}
	return nil
}
