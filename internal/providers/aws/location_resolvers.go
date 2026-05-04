package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveLocationTrackerConsumerRefs,
		EdgeDecl{TypeLocationTrackerConsumer, TypeLocationTracker, store.RelAttachedTo},
		EdgeDecl{TypeLocationTrackerConsumer, TypeLocationGeofenceCollection, store.RelUses},
	)
}

// resolveLocationTrackerConsumerRefs links each tracker-consumer to its parent
// tracker (TrackerName) and to the geofence-collection it forwards positions
// to (ConsumerArn — always a geofence-collection ARN per ListTrackerConsumers
// API contract).
func resolveLocationTrackerConsumerRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLocationTrackerConsumer},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	trackerSet, err := scannedIDSet(acct, st, TypeLocationTracker)
	if err != nil {
		return err
	}
	gcSet, err := scannedIDSet(acct, st, TypeLocationGeofenceCollection)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			TrackerName *string `json:"TrackerName"`
			ConsumerArn *string `json:"ConsumerArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if name := sv(attrs.TrackerName); name != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeLocationTracker, locARN(region, acct.ID, "tracker", name))
			if trackerSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert location tracker-consumer→tracker: %w", err)
				}
			}
		}
		if arn := sv(attrs.ConsumerArn); arn != "" && strings.Contains(arn, ":geofence-collection/") {
			tgtID := store.ResourceID("aws", acct.ID, TypeLocationGeofenceCollection, arn)
			if gcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert location tracker-consumer→geofence-collection: %w", err)
				}
			}
		}
	}
	return nil
}
