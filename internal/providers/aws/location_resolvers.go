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
	registerResolver(resolveLocationKMSRefs,
		EdgeDecl{TypeLocationGeofenceCollection, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeLocationTracker, TypeKMSKey, store.RelUses},
	)
}

// resolveLocationKMSRefs wires geofence-collections + trackers to their
// customer-managed KMS key. KmsKeyId field populated by Phase-1 Describe
// enrichment in scanLocationGeofenceCollections / scanLocationTrackers.
func resolveLocationKMSRefs(acct *account, st *store.Store) error {
	kidx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, ttyp := range []string{TypeLocationGeofenceCollection, TypeLocationTracker} {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{ttyp},
			Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				KmsKeyID *string `json:"KmsKeyId"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			k := sv(attrs.KmsKeyID)
			if k == "" {
				continue
			}
			keyID, ok := kidx.resolveKMSKeyID(k, sv(r.Region), acct.ID)
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert location %s→kms: %w", ttyp, err)
			}
		}
	}
	return nil
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
