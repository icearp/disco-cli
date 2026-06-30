package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveRTBLinkRoutingRuleToLink,
		EdgeDecl{TypeRTBFabricLinkRoutingRule, TypeRTBFabricLink, store.RelAttachedTo},
	)
}

// resolveRTBLinkRoutingRuleToLink wires each routing rule back to its link. The
// rule NativeID is `{linkARN}/routing-rule/{ruleId}`; trim the suffix to recover
// the parent link ARN.
func resolveRTBLinkRoutingRuleToLink(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeRTBFabricLinkRoutingRule}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	linkSet, err := scannedIDSet(acct, st, TypeRTBFabricLink)
	if err != nil {
		return err
	}
	for _, r := range rows {
		linkARN, _, ok := strings.Cut(r.NativeID, "/routing-rule/")
		if !ok {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeRTBFabricLink, linkARN)
		if !linkSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert rtbfabric routing-rule→link: %w", err)
		}
	}
	return nil
}
