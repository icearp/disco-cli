package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveTrafficMirrorSessionRelationships)
}

// resolveTrafficMirrorSessionRelationships links each session to its filter and target.
func resolveTrafficMirrorSessionRelationships(acct *account, st *store.Store) error {
	sessions, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2TrafficMirrorSession},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range sessions {
		var attrs struct {
			TrafficMirrorFilterId *string `json:"TrafficMirrorFilterId"`
			TrafficMirrorTargetId *string `json:"TrafficMirrorTargetId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.TrafficMirrorFilterId != nil {
			filterID := store.ResourceID("aws", acct.ID, TypeEC2TrafficMirrorFilter,
				ec2ARN(region, acct.ID, "traffic-mirror-filter", *attrs.TrafficMirrorFilterId))
			if err := st.UpsertRelationship(r.ID, filterID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert traffic-mirror-session→filter relationship: %w", err)
			}
		}
		if attrs.TrafficMirrorTargetId != nil {
			targetID := store.ResourceID("aws", acct.ID, TypeEC2TrafficMirrorTarget,
				ec2ARN(region, acct.ID, "traffic-mirror-target", *attrs.TrafficMirrorTargetId))
			if err := st.UpsertRelationship(r.ID, targetID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert traffic-mirror-session→target relationship: %w", err)
			}
		}
	}
	return nil
}
