package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// Wave R1 of the resolver-implementation backlog (ROADMAP.md "Resolver
// buildout"): lineage + CMEK edges for the Compute Engine storage-resource
// family added by Wave 1 of the scanner buildout. Every disk-family type
// (Disk, Image, Snapshot, InstantSnapshot, MachineImage) carries "source*"
// self-link fields recording what it was created from, plus an optional
// customer-managed-encryption-key reference — both read directly off the
// already-scanned AttributesJSON, no extra API calls.
func init() {
	registerResolver(resolveComputeStorageLineageRelationships,
		EdgeDecl{TypeComputeDisk, TypeComputeImage, store.RelAttachedTo},
		EdgeDecl{TypeComputeDisk, TypeComputeSnapshot, store.RelAttachedTo},
		EdgeDecl{TypeComputeDisk, TypeComputeRegionSnapshot, store.RelAttachedTo},
		EdgeDecl{TypeComputeDisk, TypeComputeDisk, store.RelAttachedTo},
		EdgeDecl{TypeComputeDisk, TypeComputeRegionDisk, store.RelAttachedTo},
		EdgeDecl{TypeComputeDisk, TypeComputeInstantSnapshot, store.RelAttachedTo},
		EdgeDecl{TypeComputeDisk, TypeComputeRegionInstantSnapshot, store.RelAttachedTo},
		EdgeDecl{TypeComputeDisk, TypeKMSCryptoKey, store.RelUses},
		EdgeDecl{TypeComputeRegionDisk, TypeComputeImage, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionDisk, TypeComputeSnapshot, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionDisk, TypeComputeRegionSnapshot, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionDisk, TypeComputeDisk, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionDisk, TypeComputeRegionDisk, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionDisk, TypeComputeInstantSnapshot, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionDisk, TypeComputeRegionInstantSnapshot, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionDisk, TypeKMSCryptoKey, store.RelUses},
		EdgeDecl{TypeComputeImage, TypeComputeDisk, store.RelAttachedTo},
		EdgeDecl{TypeComputeImage, TypeComputeRegionDisk, store.RelAttachedTo},
		EdgeDecl{TypeComputeImage, TypeComputeSnapshot, store.RelAttachedTo},
		EdgeDecl{TypeComputeImage, TypeComputeRegionSnapshot, store.RelAttachedTo},
		EdgeDecl{TypeComputeImage, TypeComputeImage, store.RelAttachedTo},
		EdgeDecl{TypeComputeImage, TypeKMSCryptoKey, store.RelUses},
		EdgeDecl{TypeComputeSnapshot, TypeComputeDisk, store.RelAttachedTo},
		EdgeDecl{TypeComputeSnapshot, TypeComputeRegionDisk, store.RelAttachedTo},
		EdgeDecl{TypeComputeSnapshot, TypeComputeInstantSnapshot, store.RelAttachedTo},
		EdgeDecl{TypeComputeSnapshot, TypeComputeRegionInstantSnapshot, store.RelAttachedTo},
		EdgeDecl{TypeComputeSnapshot, TypeKMSCryptoKey, store.RelUses},
		EdgeDecl{TypeComputeRegionSnapshot, TypeComputeDisk, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionSnapshot, TypeComputeRegionDisk, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionSnapshot, TypeComputeInstantSnapshot, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionSnapshot, TypeComputeRegionInstantSnapshot, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionSnapshot, TypeKMSCryptoKey, store.RelUses},
		EdgeDecl{TypeComputeInstantSnapshot, TypeComputeDisk, store.RelAttachedTo},
		EdgeDecl{TypeComputeInstantSnapshot, TypeComputeRegionDisk, store.RelAttachedTo},
		EdgeDecl{TypeComputeInstantSnapshot, TypeComputeInstantSnapshotGroup, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionInstantSnapshot, TypeComputeDisk, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionInstantSnapshot, TypeComputeRegionDisk, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionInstantSnapshot, TypeComputeRegionInstantSnapshotGroup, store.RelAttachedTo},
		EdgeDecl{TypeComputeMachineImage, TypeComputeInstance, store.RelAttachedTo},
		EdgeDecl{TypeComputeMachineImage, TypeKMSCryptoKey, store.RelUses},
		EdgeDecl{TypeComputeInstantSnapshotGroup, TypeComputeResourcePolicy, store.RelUses},
		EdgeDecl{TypeComputeRegionInstantSnapshotGroup, TypeComputeResourcePolicy, store.RelUses},
	)
}

// computeStorageLineageTargetTypes enumerates every disco type a "source*"
// self-link on a disk-family resource can point at. Used both to fetch the
// resources whose lineage fields get read, and (for the subset that are
// valid edge targets) to build the scanned-ID existence set lineage edges
// are checked against before upsert — a stale to_id would otherwise sit in
// the relationships table pointing at a cross-project or never-scanned
// resource (no FK enforces this; the guard is a data-quality choice, not a
// constraint requirement).
var computeStorageLineageTargetTypes = []string{
	TypeComputeDisk, TypeComputeRegionDisk,
	TypeComputeImage,
	TypeComputeSnapshot, TypeComputeRegionSnapshot,
	TypeComputeInstantSnapshot, TypeComputeRegionInstantSnapshot,
	TypeComputeInstantSnapshotGroup, TypeComputeRegionInstantSnapshotGroup,
	TypeComputeInstance,
	TypeComputeResourcePolicy,
}

func resolveComputeStorageLineageRelationships(p *project, st *store.Store) error {
	kmsIdx, err := loadKMSCryptoKeyIndex(p, st)
	if err != nil {
		return err
	}
	scanned, err := scannedIDSet(p, st, computeStorageLineageTargetTypes...)
	if err != nil {
		return err
	}

	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID,
		Types: []string{
			TypeComputeDisk, TypeComputeRegionDisk,
			TypeComputeImage, TypeComputeMachineImage,
			TypeComputeSnapshot, TypeComputeRegionSnapshot,
			TypeComputeInstantSnapshot, TypeComputeRegionInstantSnapshot,
		},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			SourceDisk                 string                `json:"sourceDisk"`
			SourceImage                string                `json:"sourceImage"`
			SourceSnapshot             string                `json:"sourceSnapshot"`
			SourceInstantSnapshot      string                `json:"sourceInstantSnapshot"`
			SourceInstantSnapshotGroup string                `json:"sourceInstantSnapshotGroup"`
			SourceInstance             string                `json:"sourceInstance"`
			DiskEncryptionKey          *computeEncryptionKey `json:"diskEncryptionKey"`
			ImageEncryptionKey         *computeEncryptionKey `json:"imageEncryptionKey"`
			SnapshotEncryptionKey      *computeEncryptionKey `json:"snapshotEncryptionKey"`
			MachineImageEncryptionKey  *computeEncryptionKey `json:"machineImageEncryptionKey"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertComputeStorageLineageEdge(st, scanned, r.ID, p.ID, attrs.SourceDisk, computeDiskTypeForSelfLink); err != nil {
			return err
		}
		if err := upsertComputeStorageLineageEdge(st, scanned, r.ID, p.ID, attrs.SourceImage, func(string) string { return TypeComputeImage }); err != nil {
			return err
		}
		if err := upsertComputeStorageLineageEdge(st, scanned, r.ID, p.ID, attrs.SourceSnapshot, computeSnapshotTypeForSelfLink); err != nil {
			return err
		}
		if err := upsertComputeStorageLineageEdge(st, scanned, r.ID, p.ID, attrs.SourceInstantSnapshot, computeInstantSnapshotTypeForSelfLink); err != nil {
			return err
		}
		if err := upsertComputeStorageLineageEdge(st, scanned, r.ID, p.ID, attrs.SourceInstantSnapshotGroup, computeInstantSnapshotGroupTypeForSelfLink); err != nil {
			return err
		}
		if err := upsertComputeStorageLineageEdge(st, scanned, r.ID, p.ID, attrs.SourceInstance, func(string) string { return TypeComputeInstance }); err != nil {
			return err
		}
		for _, key := range []*computeEncryptionKey{
			attrs.DiskEncryptionKey, attrs.ImageEncryptionKey,
			attrs.SnapshotEncryptionKey, attrs.MachineImageEncryptionKey,
		} {
			if key == nil || key.KmsKeyName == "" {
				continue
			}
			keyID, ok := kmsIdx[stripCryptoKeyVersion(key.KmsKeyName)]
			if !ok {
				continue // cross-project key reference — not scanned in this project
			}
			if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert compute storage→cryptoKey: %w", err)
			}
		}
	}

	groups, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID,
		Types: []string{TypeComputeInstantSnapshotGroup, TypeComputeRegionInstantSnapshotGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range groups {
		var attrs struct {
			SourceConsistencyGroup string `json:"sourceConsistencyGroup"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeResourcePolicy, attrs.SourceConsistencyGroup, store.RelUses); err != nil {
			return err
		}
	}
	return nil
}

// computeEncryptionKey mirrors compute.CustomerEncryptionKey's KmsKeyName
// field — the only piece disco's resolvers care about.
type computeEncryptionKey struct {
	KmsKeyName string `json:"kmsKeyName"`
}

// upsertComputeStorageLineageEdge upserts a lineage edge from fromID to the
// resource named by a "source*" self-link, resolving which disco type the
// self-link belongs to via typeFor. No-op when selfLink is empty or the
// target was never scanned (cross-project source, or deleted since).
func upsertComputeStorageLineageEdge(st *store.Store, scanned map[string]bool, fromID, projectID, selfLink string, typeFor func(string) string) error {
	if selfLink == "" {
		return nil
	}
	return upsertIfScanned(st, scanned, fromID, "gcp", projectID, typeFor(selfLink), selfLink, store.RelAttachedTo)
}

// scannedIDSet returns the set of resource IDs among the given disco types
// that were actually scanned into this project — for resolvers that must
// confirm a lineage/reference target exists before upserting an edge to it
// (the relationships table has no FK, so a stale to_id wouldn't error — it
// would just sit there pointing at nothing).
func scannedIDSet(p *project, st *store.Store, types ...string) (map[string]bool, error) {
	rs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: types, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(rs))
	for _, r := range rs {
		m[r.ID] = true
	}
	return m, nil
}

// bareNameIndex maps every scanned resource of the given type to its
// resource ID, keyed by the bare name (the segment after the last "/" in
// its NativeID). Reused wherever a cross-API field references a resource
// by its short/relative name rather than that resource's own
// fully-qualified NativeID convention (see
// project_gcp_cross_api_selflink_mismatch memory) — e.g. Cloud SQL's
// privateNetwork, Cloud Monitoring's UptimeCheckConfig.resourceGroup.groupId,
// Dataproc's subnetworkUri/configBucket.
func bareNameIndex(p *project, st *store.Store, rtype string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{rtype}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		idx[lastSegment(r.NativeID)] = r.ID
	}
	return idx, nil
}

// nativeIDIndex maps every scanned resource of the given type to its
// resource ID, keyed by its full NativeID. Use over bareNameIndex whenever a
// cross-field reference is (or can be reconstructed into) the target's own
// fully-qualified NativeID — bareNameIndex's bare-last-segment key is only
// safe when that segment is unique across the whole project, which does not
// hold for scoped children like Bigtable clusters (unique per-instance, not
// per-project).
func nativeIDIndex(p *project, st *store.Store, rtype string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{rtype}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		idx[r.NativeID] = r.ID
	}
	return idx, nil
}

// computeSnapshotTypeForSelfLink mirrors computeDiskTypeForSelfLink for the
// Snapshot/RegionSnapshot pair.
func computeSnapshotTypeForSelfLink(selfLink string) string {
	if selfLinkIsRegional(selfLink) {
		return TypeComputeRegionSnapshot
	}
	return TypeComputeSnapshot
}

// computeInstantSnapshotTypeForSelfLink mirrors computeDiskTypeForSelfLink
// for the InstantSnapshot/RegionInstantSnapshot pair.
func computeInstantSnapshotTypeForSelfLink(selfLink string) string {
	if selfLinkIsRegional(selfLink) {
		return TypeComputeRegionInstantSnapshot
	}
	return TypeComputeInstantSnapshot
}

// computeInstantSnapshotGroupTypeForSelfLink mirrors computeDiskTypeForSelfLink
// for the InstantSnapshotGroup/RegionInstantSnapshotGroup pair.
func computeInstantSnapshotGroupTypeForSelfLink(selfLink string) string {
	if selfLinkIsRegional(selfLink) {
		return TypeComputeRegionInstantSnapshotGroup
	}
	return TypeComputeInstantSnapshotGroup
}
