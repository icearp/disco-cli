package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// Resolver Wave R24 (continued — see compute_networking_resolvers.go header):
// PublicDelegatedPrefix/GlobalPublicDelegatedPrefix, deferred by
// `compute_addressing_scanners.go`'s own header at scanner-landing time
// alongside Address (Address.Users[] stays unresolved — its target kind
// can't be determined without parsing the URL path, still a genuine gap).
//
// ParentPrefix is a full self-link URL that per `go doc` names "Either
// PublicAdvertisedPrefix or PublicDelegatedPrefix" — a genuine 3-way oneof
// (both PublicDelegatedPrefix scopes plus the org/billing-scoped
// PublicAdvertisedPrefix, which IS scanned as its own project-level type
// today). Tried via upsertIfScannedAny across all three; an unmatched parent
// is a normal skip (parent scoped to a different project, or a rare
// same-name segment collision across the two PDP scopes — self-link URLs
// are exact-matched here, so no bareNameIndex ambiguity applies).
func init() {
	registerResolver(resolvePublicDelegatedPrefixRelationships,
		EdgeDecl{TypeComputePublicDelegatedPrefix, TypeComputePublicAdvertisedPrefix, store.RelUses},
		EdgeDecl{TypeComputePublicDelegatedPrefix, TypeComputePublicDelegatedPrefix, store.RelUses},
		EdgeDecl{TypeComputePublicDelegatedPrefix, TypeComputeGlobalPublicDelegatedPrefix, store.RelUses},
		EdgeDecl{TypeComputeGlobalPublicDelegatedPrefix, TypeComputePublicAdvertisedPrefix, store.RelUses},
		EdgeDecl{TypeComputeGlobalPublicDelegatedPrefix, TypeComputePublicDelegatedPrefix, store.RelUses},
		EdgeDecl{TypeComputeGlobalPublicDelegatedPrefix, TypeComputeGlobalPublicDelegatedPrefix, store.RelUses},
	)
}

func resolvePublicDelegatedPrefixRelationships(p *project, st *store.Store) error {
	pdpTypes := []string{TypeComputePublicDelegatedPrefix, TypeComputeGlobalPublicDelegatedPrefix}
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: pdpTypes,
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	parentTypes := []string{TypeComputePublicAdvertisedPrefix, TypeComputePublicDelegatedPrefix, TypeComputeGlobalPublicDelegatedPrefix}
	scannedParents, err := scannedIDSet(p, st, parentTypes...)
	if err != nil {
		return err
	}
	if len(scannedParents) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			ParentPrefix string `json:"parentPrefix"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScannedAny(st, scannedParents, r.ID, "gcp", p.ID, parentTypes, attrs.ParentPrefix, store.RelUses); err != nil {
			return fmt.Errorf("upsert publicDelegatedPrefix→parent: %w", err)
		}
	}
	return nil
}
