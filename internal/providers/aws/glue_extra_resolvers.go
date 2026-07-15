package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveGlueUserDefinedFunctionRelationships,
		EdgeDecl{TypeGlueUserDefinedFunction, TypeGlueDatabase, store.RelAttachedTo},
	)
}

// resolveGlueUserDefinedFunctionRelationships wires each UDF to its database —
// the database ARN is the NativeID prefix before `/function/`.
func resolveGlueUserDefinedFunctionRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeGlueUserDefinedFunction}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	dbSet, err := scannedIDSet(acct, st, TypeGlueDatabase)
	if err != nil {
		return err
	}
	for _, r := range rows {
		i := strings.Index(r.NativeID, "/function/")
		if i < 0 {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, r.NativeID[:i])
		if dbSet[tgt] {
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert glue udf→database: %w", err)
			}
		}
	}
	return nil
}
