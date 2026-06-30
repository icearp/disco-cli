package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolvePersonalizeChildrenToDatasetGroup,
		EdgeDecl{TypePersonalizeFilter, TypePersonalizeDatasetGroup, store.RelAttachedTo},
		EdgeDecl{TypePersonalizeRecommender, TypePersonalizeDatasetGroup, store.RelAttachedTo},
	)
}

// resolvePersonalizeChildrenToDatasetGroup wires filters and recommenders to
// their dataset group via the DatasetGroupArn each summary carries. FK-safe:
// the edge emits only when the dataset group was scanned.
func resolvePersonalizeChildrenToDatasetGroup(acct *account, st *store.Store) error {
	dgSet, err := scannedIDSet(acct, st, TypePersonalizeDatasetGroup)
	if err != nil {
		return err
	}
	if len(dgSet) == 0 {
		return nil
	}
	for _, ctype := range []string{TypePersonalizeFilter, TypePersonalizeRecommender} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{ctype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				DatasetGroupArn *string `json:"DatasetGroupArn"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			dg := sv(attrs.DatasetGroupArn)
			if dg == "" {
				continue
			}
			tgt := store.ResourceID("aws", acct.ID, TypePersonalizeDatasetGroup, dg)
			if !dgSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert personalize %s→dataset-group: %w", ctype, err)
			}
		}
	}
	return nil
}
