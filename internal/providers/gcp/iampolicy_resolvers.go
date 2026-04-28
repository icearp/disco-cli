package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveIAMPolicyRelationships) }

// resolveIAMPolicyRelationships walks each gcp:iam:policy resource's bindings
// and emits a `uses` edge from the policy to every service account named in
// the bindings whose SA resource exists in the store. The role granted is
// stamped on the edge attributes so downstream queries (e.g. "what roles does
// this SA hold across the org?") can filter without re-parsing the policy.
//
// Members of types other than serviceAccount: (user, group, domain, allUsers,
// allAuthenticatedUsers) are intentionally skipped — they have no resource
// rows in the store yet, and FK-violating edges would fail the upsert. Edges
// to those principals land when the Entra-equivalent GCP identity scanner
// (R4 follow-up: workspace users / groups, workforce pools) lands.
func resolveIAMPolicyRelationships(p *project, st *store.Store) error {
	policies, err := st.ListResources(store.ResourceFilter{
		Provider: "gcp", AccountID: p.ID, Types: []string{TypeIAMPolicy},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(policies) == 0 {
		return nil
	}

	// Build a set of service-account resource IDs that exist in the store so
	// we can FK-check before emitting an edge. SA NativeID = full resource
	// name "projects/{id}/serviceAccounts/{email}".
	sas, err := st.ListResources(store.ResourceFilter{
		Provider: "gcp", AccountID: p.ID, Types: []string{TypeIAMServiceAccount},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	saByEmail := make(map[string]string, len(sas)) // email → resource ID
	for _, sa := range sas {
		// NativeID example: "projects/foo/serviceAccounts/svc@foo.iam.gserviceaccount.com"
		if i := strings.LastIndex(sa.NativeID, "/"); i >= 0 {
			email := sa.NativeID[i+1:]
			saByEmail[email] = sa.ID
		}
	}

	for _, r := range policies {
		var policy struct {
			Bindings []struct {
				Role    string   `json:"role"`
				Members []string `json:"members"`
			} `json:"bindings"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &policy); err != nil {
			continue
		}
		for _, b := range policy.Bindings {
			for _, m := range b.Members {
				email, ok := strings.CutPrefix(m, "serviceAccount:")
				if !ok {
					continue
				}
				saID, ok := saByEmail[email]
				if !ok {
					// Cross-project SA or not yet scanned — skip to keep
					// the edge FK-safe.
					continue
				}
				attrs := mustJSON(map[string]string{"role": b.Role})
				if err := st.UpsertRelationship(r.ID, saID, store.RelUses, "directed", &attrs); err != nil {
					return fmt.Errorf("upsert policy→service-account: %w", err)
				}
			}
		}
	}
	return nil
}
