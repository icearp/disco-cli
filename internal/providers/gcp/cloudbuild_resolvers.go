package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveCloudBuildRelationships) }

// resolveCloudBuildRelationships derives trigger -[uses]-> service-account
// edges. The trigger's `serviceAccount` field is documented as
// `projects/{projectId}/serviceAccounts/{ACCOUNT_EMAIL_OR_UNIQUEID}` —
// matches the SA NativeID format directly. Email-form fallback handled the
// same way as R4.10's serverless resolver.
//
// Worker pool edges deferred (worker-pool scanner not landed). GitHub /
// repo connection edges deferred — connection scanner landing alongside.
func resolveCloudBuildRelationships(p *project, st *store.Store) error {
	trs, err := st.ListResources(store.ResourceFilter{
		Provider: "gcp", AccountID: p.ID, Types: []string{TypeCloudBuildTrigger},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(trs) == 0 {
		return nil
	}

	sas, err := st.ListResources(store.ResourceFilter{
		Provider: "gcp", AccountID: p.ID, Types: []string{TypeIAMServiceAccount},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	saIDByNative := make(map[string]string, len(sas))
	saIDByEmail := make(map[string]string, len(sas))
	for _, sa := range sas {
		saIDByNative[sa.NativeID] = sa.ID
		if i := strings.LastIndex(sa.NativeID, "/"); i >= 0 {
			saIDByEmail[sa.NativeID[i+1:]] = sa.ID
		}
	}

	for _, tr := range trs {
		var a struct {
			ServiceAccount string `json:"serviceAccount"`
		}
		if err := json.Unmarshal([]byte(tr.AttributesJSON), &a); err != nil {
			continue
		}
		if a.ServiceAccount == "" {
			continue
		}
		saID, ok := saIDByNative[a.ServiceAccount]
		if !ok {
			// Try email-only form (some triggers store just the email).
			saID, ok = saIDByEmail[a.ServiceAccount]
			if !ok {
				continue
			}
		}
		if err := st.UpsertRelationship(tr.ID, saID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert trigger→SA: %w", err)
		}
	}
	return nil
}
