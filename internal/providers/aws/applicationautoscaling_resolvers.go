package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveApplicationAutoScalingRelationships,
		EdgeDecl{TypeApplicationAutoScalingScalingPolicy, TypeApplicationAutoScalingScalableTarget, store.RelAttachedTo},
	)
}

// resolveApplicationAutoScalingRelationships emits scaling-policy →
// scalable-target attached-to edges. The scaling-policy attrs carry
// (ServiceNamespace, ResourceId, ScalableDimension) which uniquely identify
// the parent target via applicationAutoScalingScalableTargetNativeID.
//
// scalable-target → underlying-resource edges (per-namespace dispatch into
// ECS service / DynamoDB table / RDS cluster / Lambda alias / ElastiCache /
// SageMaker endpoint / etc) are deferred — each namespace needs a different
// id-shape parser, mirroring the AutoScalingPlans resolver pattern.
func resolveApplicationAutoScalingRelationships(acct *account, st *store.Store) error {
	policies, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeApplicationAutoScalingScalingPolicy},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(policies) == 0 {
		return nil
	}
	targetIDs, err := scannedIDSet(acct, st, TypeApplicationAutoScalingScalableTarget)
	if err != nil {
		return err
	}
	type attrs struct {
		ServiceNamespace  *string `json:"ServiceNamespace"`
		ResourceID        *string `json:"ResourceId"`
		ScalableDimension *string `json:"ScalableDimension"`
	}
	for _, p := range policies {
		var a attrs
		if err := json.Unmarshal([]byte(p.AttributesJSON), &a); err != nil {
			continue
		}
		ns := sv(a.ServiceNamespace)
		rid := sv(a.ResourceID)
		dim := sv(a.ScalableDimension)
		if ns == "" || rid == "" || dim == "" {
			continue
		}
		region := sv(p.Region)
		targetNativeID := applicationAutoScalingScalableTargetNativeID(region, acct.ID, ns, rid, dim)
		targetID := store.ResourceID("aws", acct.ID, TypeApplicationAutoScalingScalableTarget, targetNativeID)
		if _, ok := targetIDs[targetID]; !ok {
			continue
		}
		if err := st.UpsertRelationship(p.ID, targetID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert app-autoscaling-policy→target: %w", err)
		}
	}
	return nil
}
