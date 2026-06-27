package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolveBinaryAuthorizationRelationships) }

// resolveBinaryAuthorizationRelationships derives attestor -[uses]->
// service-account via `userOwnedGrafeasNote.delegationServiceAccountEmail`.
// The delegation SA is the principal the attestor uses to read attestation
// occurrences from Container Analysis — the security-meaningful pivot for
// "who can validate signatures on behalf of this attestor".
//
// Attestor → KMS public key edges deferred — v1 surface stores PGP/PKIX
// public keys inline (not a KMS reference). v1beta1 has KMS-backed
// attestation key support; that variant lands when GCP graduates it.
//
// Policy → cluster admission rules deferred — keyed on cluster-id strings
// (`projects/{p}/clusters/{c}` form) which need GKE NativeID alignment;
// follow-up.
func resolveBinaryAuthorizationRelationships(p *project, st *store.Store) error {
	atts, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeBinAuthAttestor},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(atts) == 0 {
		return nil
	}
	saByEmail, err := buildSAEmailIndex(p, st)
	if err != nil {
		return err
	}
	for _, att := range atts {
		var a struct {
			UserOwnedGrafeasNote struct {
				DelegationServiceAccountEmail string `json:"delegationServiceAccountEmail"`
			} `json:"userOwnedGrafeasNote"`
		}
		if err := json.Unmarshal([]byte(att.AttributesJSON), &a); err != nil {
			continue
		}
		email := a.UserOwnedGrafeasNote.DelegationServiceAccountEmail
		if email == "" {
			continue
		}
		saID, ok := saByEmail[email]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(att.ID, saID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert attestor→SA: %w", err)
		}
	}
	return nil
}
