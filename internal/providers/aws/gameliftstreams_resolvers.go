package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveGameLiftStreamsStreamGroupApplication,
		EdgeDecl{TypeGameLiftStreamsStreamGroup, TypeGameLiftStreamsApplication, store.RelUses},
	)
}

// resolveGameLiftStreamsStreamGroupApplication wires each stream group to its
// default application via StreamGroupSummary.DefaultApplication.Arn.
func resolveGameLiftStreamsStreamGroupApplication(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeGameLiftStreamsStreamGroup}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	appSet, err := scannedIDSet(acct, st, TypeGameLiftStreamsApplication)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DefaultApplication *struct {
				Arn *string `json:"Arn"`
			} `json:"DefaultApplication"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.DefaultApplication == nil {
			continue
		}
		if appARN := sv(attrs.DefaultApplication.Arn); appARN != "" {
			tgtID := store.ResourceID("aws", acct.ID, appARN)
			if appSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert gameliftstreams stream-group→application: %w", err)
				}
			}
		}
	}
	return nil
}
