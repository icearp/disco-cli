package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveKendraChildToIndex,
		EdgeDecl{TypeKendraDataSource, TypeKendraIndex, store.RelAttachedTo},
		EdgeDecl{TypeKendraFaq, TypeKendraIndex, store.RelAttachedTo},
	)
}

// resolveKendraChildToIndex wires data-source + faq to their parent index via
// NativeID strip on `/data-source/{id}` or `/faq/{id}` tail.
func resolveKendraChildToIndex(acct *account, st *store.Store) error {
	idxSet, err := scannedIDSet(acct, st, TypeKendraIndex)
	if err != nil {
		return err
	}
	for _, child := range []struct {
		t, seg string
	}{
		{TypeKendraDataSource, "/data-source/"},
		{TypeKendraFaq, "/faq/"},
	} {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{child.t}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			i := strings.LastIndex(r.NativeID, child.seg)
			if i <= 0 {
				continue
			}
			parent := r.NativeID[:i]
			tgtID := store.ResourceID("aws", acct.ID, TypeKendraIndex, parent)
			if !idxSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert kendra %s→index: %w", child.t, err)
			}
		}
	}
	return nil
}
