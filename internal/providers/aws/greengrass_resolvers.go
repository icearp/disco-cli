package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	// Greengrass v2 deployments target an IoT thing or thing-group via
	// the canonical TargetArn field. The shape of TargetArn (`:thing/...`
	// vs `:thinggroup/...`) selects the target type.
	registerResolver(
		resolveGGV2DeploymentTarget,
		EdgeDecl{TypeGreengrassV2Deployment, TypeIoTThing, store.RelUses},
		EdgeDecl{TypeGreengrassV2Deployment, TypeIoTThingGroup, store.RelUses},
	)
}

// resolveGGV2DeploymentTarget links each Greengrass v2 deployment to its
// IoT thing or thing-group target. The scanner stores the SDK Deployment
// struct verbatim; `TargetArn` carries the canonical IoT ARN
// (`arn:aws:iot:{r}:{a}:thing/{name}` or `:thinggroup/{name}`).
//
// Cross-service edge: skip when the IoT side was not scanned (or the
// referenced thing/thing-group is not in the local store) so we don't
// create phantom edges.
func resolveGGV2DeploymentTarget(acct *account, st *store.Store) error {
	deps, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeGreengrassV2Deployment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(deps) == 0 {
		return nil
	}
	thingSet, err := scannedIDSet(acct, st, TypeIoTThing)
	if err != nil {
		return err
	}
	groupSet, err := scannedIDSet(acct, st, TypeIoTThingGroup)
	if err != nil {
		return err
	}
	for _, r := range deps {
		var attrs struct {
			TargetArn *string `json:"TargetArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.TargetArn == nil || *attrs.TargetArn == "" {
			continue
		}
		targetType := iotTargetTypeFromArn(*attrs.TargetArn)
		if targetType == "" {
			continue
		}
		targetID := store.ResourceID("aws", acct.ID, targetType, *attrs.TargetArn)
		switch targetType {
		case TypeIoTThing:
			if !thingSet[targetID] {
				continue
			}
		case TypeIoTThingGroup:
			if !groupSet[targetID] {
				continue
			}
		}
		if err := st.UpsertRelationship(r.ID, targetID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert greengrass-v2-deployment→%s: %w", targetType, err)
		}
	}
	return nil
}

// iotTargetTypeFromArn classifies an IoT ARN as either a thing or a
// thing-group. Returns "" for any other shape (e.g. an unexpected
// service or resource segment) so the caller skips emit.
func iotTargetTypeFromArn(arn string) string {
	switch {
	case strings.Contains(arn, ":thinggroup/"):
		return TypeIoTThingGroup
	case strings.Contains(arn, ":thing/"):
		return TypeIoTThing
	default:
		return ""
	}
}
