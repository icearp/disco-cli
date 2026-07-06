package azure

import (
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveDedicatedHostRelationships,
		EdgeDecl{Source: TypeComputeDedicatedHost, Target: TypeComputeHostGroup, Kind: store.RelAttachedTo},
	)
	registerResolver(resolveCapacityReservationRelationships,
		EdgeDecl{Source: TypeComputeCapacityReservation, Target: TypeComputeCapacityReservationGroup, Kind: store.RelAttachedTo},
	)
}

// resolveDedicatedHostRelationships derives each host's parent host group by
// truncating NativeID at "/hosts/" (form: .../hostGroups/{group}/hosts/{host}).
func resolveDedicatedHostRelationships(sub *subscription, st *store.Store) error {
	hosts, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
		AccountID: sub.ID,
		Types:     []string{TypeComputeDedicatedHost},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range hosts {
		hgNativeID := truncateAtSegment(r.NativeID, "/hosts/")
		if hgNativeID == "" {
			continue
		}
		hgID := store.ResourceID("azure", sub.ID, TypeComputeHostGroup, hgNativeID)
		if err := st.UpsertRelationship(r.ID, hgID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert dedicatedHost→hostGroup relationship: %w", err)
		}
	}
	return nil
}

// resolveCapacityReservationRelationships derives each reservation's parent CRG by
// truncating NativeID at "/capacityReservations/" (form: .../capacityReservationGroups/{group}/capacityReservations/{reservation}).
func resolveCapacityReservationRelationships(sub *subscription, st *store.Store) error {
	reservations, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"},
		AccountID: sub.ID,
		Types:     []string{TypeComputeCapacityReservation},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range reservations {
		crgNativeID := truncateAtSegment(r.NativeID, "/capacityReservations/")
		if crgNativeID == "" {
			continue
		}
		crgID := store.ResourceID("azure", sub.ID, TypeComputeCapacityReservationGroup, crgNativeID)
		if err := st.UpsertRelationship(r.ID, crgID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert capacityReservation→CRG relationship: %w", err)
		}
	}
	return nil
}
