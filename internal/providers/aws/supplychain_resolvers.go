package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveSupplyChainInstanceChildren,
		EdgeDecl{TypeSupplyChainDataIntegrationFlow, TypeSupplyChainInstance, store.RelAttachedTo},
		EdgeDecl{TypeSupplyChainDataset, TypeSupplyChainInstance, store.RelAttachedTo},
		EdgeDecl{TypeSupplyChainNamespace, TypeSupplyChainInstance, store.RelAttachedTo},
	)
}

// resolveSupplyChainInstanceChildren wires data-integration-flows, datasets,
// and data-lake namespaces to their parent instance via each row's
// InstanceId. FK-safe: emits only when the instance was scanned.
func resolveSupplyChainInstanceChildren(acct *account, st *store.Store) error {
	instSet, err := scannedIDSet(acct, st, TypeSupplyChainInstance)
	if err != nil {
		return err
	}
	if len(instSet) == 0 {
		return nil
	}
	for _, ctype := range []string{
		TypeSupplyChainDataIntegrationFlow,
		TypeSupplyChainDataset,
		TypeSupplyChainNamespace,
	} {
		rows, lerr := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{ctype},
			Limit: util.AllResources,
		})
		if lerr != nil {
			return lerr
		}
		for _, r := range rows {
			var attrs struct {
				InstanceID *string `json:"InstanceId"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			instID := sv(attrs.InstanceID)
			if instID == "" {
				continue
			}
			instARN := scnInstanceARN(sv(r.Region), acct.ID, instID)
			tgt := store.ResourceID("aws", acct.ID, instARN)
			if !instSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert scn %s→instance: %w", ctype, err)
			}
		}
	}
	return nil
}
