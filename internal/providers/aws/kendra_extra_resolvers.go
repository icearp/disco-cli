package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// kendraExtraChildren lists the per-index child types whose NativeID encodes the
// parent index via the dominant `{indexARN}/<kind>/{id}` shape.
var kendraExtraChildren = []struct {
	t, seg string
}{
	{TypeKendraAccessControlConfiguration, "/access-control-configuration/"},
	{TypeKendraExperience, "/experience/"},
	{TypeKendraFeaturedResultsSet, "/featured-results-set/"},
	{TypeKendraQuerySuggestionsBlockList, "/query-suggestions-block-list/"},
	{TypeKendraThesaurus, "/thesaurus/"},
}

func init() {
	registerResolver(
		resolveKendraExtraChildToIndex,
		EdgeDecl{TypeKendraAccessControlConfiguration, TypeKendraIndex, store.RelAttachedTo},
		EdgeDecl{TypeKendraExperience, TypeKendraIndex, store.RelAttachedTo},
		EdgeDecl{TypeKendraFeaturedResultsSet, TypeKendraIndex, store.RelAttachedTo},
		EdgeDecl{TypeKendraQuerySuggestionsBlockList, TypeKendraIndex, store.RelAttachedTo},
		EdgeDecl{TypeKendraThesaurus, TypeKendraIndex, store.RelAttachedTo},
	)
}

// resolveKendraExtraChildToIndex wires each per-index child (access-control
// config, experience, featured-results-set, query-suggestions block-list,
// thesaurus) to its parent index via NativeID strip on the `/<kind>/{id}` tail.
func resolveKendraExtraChildToIndex(acct *account, st *store.Store) error {
	idxSet, err := scannedIDSet(acct, st, TypeKendraIndex)
	if err != nil {
		return err
	}
	for _, child := range kendraExtraChildren {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{child.t}, Limit: util.AllResources,
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
			tgtID := store.ResourceID("aws", acct.ID, parent)
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
