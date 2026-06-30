package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveOutpostSite,
		EdgeDecl{TypeOutpostsOutpost, TypeOutpostsSite, store.RelAttachedTo},
	)
}

// resolveOutpostSite wires each outpost to its site via the SiteArn attribute
// (which equals the site NativeID). FK-safe against scanned sites.
func resolveOutpostSite(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeOutpostsOutpost},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	siteSet, err := scannedIDSet(acct, st, TypeOutpostsSite)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			SiteArn *string `json:"SiteArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		siteARN := sv(attrs.SiteArn)
		if siteARN == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeOutpostsSite, siteARN)
		if !siteSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert outposts outpost→site: %w", err)
		}
	}
	return nil
}
