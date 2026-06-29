package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveOpenSearchDataSourceRelationships,
		EdgeDecl{TypeOpenSearchDataSource, TypeOpenSearchDomain, store.RelAttachedTo},
	)
}

// resolveOpenSearchDataSourceRelationships wires each data source to its parent
// domain — the domain ARN is the NativeID prefix before `/data-source/`.
func resolveOpenSearchDataSourceRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeOpenSearchDataSource}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	domainSet, err := scannedIDSet(acct, st, TypeOpenSearchDomain)
	if err != nil {
		return err
	}
	for _, r := range rows {
		i := strings.Index(r.NativeID, "/data-source/")
		if i < 0 {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeOpenSearchDomain, r.NativeID[:i])
		if domainSet[tgtID] {
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert opensearch data-source→domain: %w", err)
			}
		}
	}
	return nil
}
