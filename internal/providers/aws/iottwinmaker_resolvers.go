package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveIoTTwinMakerChildrenToWorkspace,
		EdgeDecl{TypeIoTTwinMakerComponentType, TypeIoTTwinMakerWorkspace, store.RelAttachedTo},
		EdgeDecl{TypeIoTTwinMakerEntity, TypeIoTTwinMakerWorkspace, store.RelAttachedTo},
		EdgeDecl{TypeIoTTwinMakerScene, TypeIoTTwinMakerWorkspace, store.RelAttachedTo},
		EdgeDecl{TypeIoTTwinMakerSyncJob, TypeIoTTwinMakerWorkspace, store.RelAttachedTo},
	)
}

// iotTMWorkspaceARN extracts the workspace ARN from any per-workspace child
// NativeID of shape `arn:aws:iottwinmaker:r:a:workspace/{wsId}/<seg>/...`.
func iotTMWorkspaceARN(arn string) string {
	const seg = ":workspace/"
	i := strings.Index(arn, seg)
	if i < 0 {
		return ""
	}
	tail := arn[i+len(seg):]
	end := strings.IndexByte(tail, '/')
	if end < 0 {
		return ""
	}
	return arn[:i] + seg + tail[:end]
}

func resolveIoTTwinMakerChildrenToWorkspace(acct *account, st *store.Store) error {
	wsSet, err := scannedIDSet(acct, st, TypeIoTTwinMakerWorkspace)
	if err != nil {
		return err
	}
	for _, t := range []string{
		TypeIoTTwinMakerComponentType,
		TypeIoTTwinMakerEntity,
		TypeIoTTwinMakerScene,
		TypeIoTTwinMakerSyncJob,
	} {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{t}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			ws := iotTMWorkspaceARN(r.NativeID)
			if ws == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeIoTTwinMakerWorkspace, ws)
			if !wsSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert iottwinmaker %s→workspace: %w", t, err)
			}
		}
	}
	return nil
}
