package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveWAFRegionalWebACLAssociations,
		EdgeDecl{TypeWAFRegionalWebACLAssociation, TypeWAFRegionalWebACL, store.RelAttachedTo},
	)
}

// resolveWAFRegionalWebACLAssociations links each web-ACL association row to
// the regional web-ACL protecting the resource, rebuilding the synthetic
// web-ACL ARN from WebACLId + Region.
func resolveWAFRegionalWebACLAssociations(acct *account, st *store.Store) error {
	assocs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeWAFRegionalWebACLAssociation}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}

	type assocAttrs struct {
		WebACLId string `json:"WebACLId"`
	}
	for _, r := range assocs {
		var a assocAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		if a.WebACLId == "" {
			continue
		}
		target := wafRegionalARN(sv(r.Region), acct.ID, "webacl", a.WebACLId)
		targetID := store.ResourceID("aws", acct.ID, TypeWAFRegionalWebACL, target)
		if err := st.UpsertRelationship(r.ID, targetID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert wafregional-association→web-acl: %w", err)
		}
	}
	return nil
}
