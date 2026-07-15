package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveResourceGroupsTagSyncTaskToGroup,
		EdgeDecl{TypeResourceGroupsTagSyncTask, TypeResourceGroupsGroup, store.RelAttachedTo},
	)
}

// resolveResourceGroupsTagSyncTaskToGroup wires each tag-sync-task to its
// application group via GroupArn.
func resolveResourceGroupsTagSyncTaskToGroup(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeResourceGroupsTagSyncTask}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	gSet, err := scannedIDSet(acct, st, TypeResourceGroupsGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			GroupArn *string `json:"GroupArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		g := sv(attrs.GroupArn)
		if g == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, g)
		if !gSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert resource-groups task→group: %w", err)
		}
	}
	return nil
}
