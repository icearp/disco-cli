package gcp

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolveIAMServiceAccountKeyRelationships) }

// resolveIAMServiceAccountKeyRelationships emits a `attached-to` edge from
// every gcp:iam:key to its parent service account. The SA's
// resource name is derivable from the key's NativeID by trimming the
// "/keys/{keyid}" suffix — no API call needed.
func resolveIAMServiceAccountKeyRelationships(p *project, st *store.Store) error {
	keys, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeIAMSAKey},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}

	// Build a set of SA NativeIDs in this project so we can FK-check.
	sas, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeIAMServiceAccount},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	saIDByNative := make(map[string]string, len(sas))
	for _, sa := range sas {
		saIDByNative[sa.NativeID] = sa.ID
	}

	for _, r := range keys {
		// Key NativeID: "projects/{p}/serviceAccounts/{email}/keys/{keyid}".
		idx := strings.Index(r.NativeID, "/keys/")
		if idx < 0 {
			continue
		}
		saNative := r.NativeID[:idx]
		saID, ok := saIDByNative[saNative]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, saID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert sa-key→service-account: %w", err)
		}
	}
	return nil
}
