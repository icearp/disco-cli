package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolvePaymentCryptographyAliasToKey,
		EdgeDecl{TypePaymentCryptographyAlias, TypePaymentCryptographyKey, store.RelAttachedTo},
	)
}

// resolvePaymentCryptographyAliasToKey wires alias → key (KeyArn).
func resolvePaymentCryptographyAliasToKey(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypePaymentCryptographyAlias}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	keySet, err := scannedIDSet(acct, st, TypePaymentCryptographyKey)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KeyArn *string `json:"KeyArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if k := sv(attrs.KeyArn); k != "" {
			tgtID := store.ResourceID("aws", acct.ID, k)
			if keySet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert pc alias→key: %w", err)
				}
			}
		}
	}
	return nil
}
