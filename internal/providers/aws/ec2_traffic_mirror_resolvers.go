package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveTrafficMirrorSessionRelationships,
		EdgeDecl{TypeEC2TrafficMirrorSession, TypeEC2TrafficMirrorFilter, store.RelUses},
		EdgeDecl{TypeEC2TrafficMirrorSession, TypeEC2TrafficMirrorTarget, store.RelAttachedTo},
	)
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
			TrafficMirrorFilterID *string `json:"TrafficMirrorFilterID"`
			TrafficMirrorTargetID *string `json:"TrafficMirrorTargetID"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.TrafficMirrorFilterID != nil {
			filterID := store.ResourceID("aws", acct.ID, TypeEC2TrafficMirrorFilter,
				ec2ARN(region, acct.ID, "traffic-mirror-filter", *attrs.TrafficMirrorFilterID))
			if err := st.UpsertRelationship(r.ID, filterID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert traffic-mirror-session→filter relationship: %w", err)
			}
		}
		if attrs.TrafficMirrorTargetID != nil {
			targetID := store.ResourceID("aws", acct.ID, TypeEC2TrafficMirrorTarget,
				ec2ARN(region, acct.ID, "traffic-mirror-target", *attrs.TrafficMirrorTargetID))
			if err := st.UpsertRelationship(r.ID, targetID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert traffic-mirror-session→target relationship: %w", err)
			}
		}
	}
	return nil
}
