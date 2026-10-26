package aws

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveApplicationAutoScalingRelationships,
		EdgeDecl{TypeApplicationAutoScalingScalingPolicy, TypeApplicationAutoScalingScalableTarget, store.RelAttachedTo},
		EdgeDecl{TypeApplicationAutoScalingScheduledAction, TypeApplicationAutoScalingScalableTarget, store.RelAttachedTo},
	)
}

// resolveApplicationAutoScalingRelationships emits scaling-policy →
// scalable-target and scheduled-action → scalable-target attached-to edges.
// Both attrs carry (ServiceNamespace, ResourceId, ScalableDimension) which
// uniquely identify the target via applicationAutoScalingScalableTargetNativeID.
//
// scalable-target → underlying-resource edges (per-namespace dispatch into
// ECS service / DynamoDB table / RDS cluster / Lambda alias / ElastiCache /
// SageMaker endpoint / etc) are deferred — each namespace needs a different
// id-shape parser, mirroring the AutoScalingPlans resolver pattern.
func resolveApplicationAutoScalingRelationships(acct *account, st *store.Store) error {
	children, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeApplicationAutoScalingScalingPolicy, TypeApplicationAutoScalingScheduledAction},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(children) == 0 {
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
	// A row that fails to decode is reported at the end so that it costs the
	// other rows none of their edges.
	var decodeErrs []error
	for _, c := range children {
		var a attrs
		if err := json.Unmarshal([]byte(c.AttributesJSON), &a); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s: %w", c.Type, c.NativeID, err))
			continue
		}
		ns := sv(a.ServiceNamespace)
		rid := sv(a.ResourceID)
		dim := sv(a.ScalableDimension)
		if ns == "" || rid == "" || dim == "" {
			continue
		}
		region := sv(c.Region)
		targetNativeID := applicationAutoScalingScalableTargetNativeID(acct.partition(), region, acct.ID, ns, rid, dim)
		targetID := store.ResourceID("aws", acct.ID, targetNativeID)
		if _, ok := targetIDs[targetID]; !ok {
			continue
		}
		if err := st.UpsertRelationship(c.ID, targetID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert %s→scalable-target: %w", c.Type, err)
		}
	}
	return errors.Join(decodeErrs...)
}
