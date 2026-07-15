package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveTextractAdapterVersion,
		EdgeDecl{TypeTextractAdapterVersion, TypeTextractAdapter, store.RelAttachedTo},
	)
}

// resolveTextractAdapterVersion wires each adapter version to its parent
// adapter via AdapterId on the version's list-summary shape.
func resolveTextractAdapterVersion(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeTextractAdapterVersion}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	adapterSet, err := scannedIDSet(acct, st, TypeTextractAdapter)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			AdapterID *string `json:"AdapterId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		aid := sv(attrs.AdapterID)
		if aid == "" {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, textractAdapterNativeID(sv(r.Region), acct.ID, aid))
		if !adapterSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert textract adapter-version→adapter: %w", err)
		}
	}
	return nil
}
