package azure

import (
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveDedicatedHostRelationships)
	registerResolver(resolveCapacityReservationRelationships)
}

// resolveDedicatedHostRelationships derives the parent host group for each dedicated
// host by truncating the host's NativeID at "/hosts/".
// NativeID form: .../hostGroups/{group}/hosts/{host}
func resolveDedicatedHostRelationships(sub *subscription, st *store.Store) error {
	hosts, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
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

// resolveCapacityReservationRelationships derives the parent capacity reservation
// group for each capacity reservation by truncating the reservation's NativeID at
// "/capacityReservations/".
// NativeID form: .../capacityReservationGroups/{group}/capacityReservations/{reservation}
func resolveCapacityReservationRelationships(sub *subscription, st *store.Store) error {
	reservations, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
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
