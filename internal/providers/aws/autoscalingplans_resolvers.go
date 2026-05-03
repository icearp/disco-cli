package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveAutoScalingPlansRelationships,
		EdgeDecl{TypeAutoScalingPlansScalingPlan, TypeAutoScalingGroup, store.RelAttachedTo},
	)
}

// resolveAutoScalingPlansRelationships emits edges from each scaling plan
// to its scalable target resources. ScalingInstructions[] entries identify
// targets via opaque resource-id strings (`autoScalingGroup/<name>`,
// `service/<cluster>/<svc>`, `table/<name>`, `cluster:<id>`, etc.). Today
// only the AutoScalingGroup form is wired (the family disco scans densest);
// other forms (ECS service, DynamoDB table, Aurora cluster, Spot Fleet,
// Lambda concurrency, etc.) skip without phantom edges.
func resolveAutoScalingPlansRelationships(acct *account, st *store.Store) error {
	plans, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAutoScalingPlansScalingPlan},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(plans) == 0 {
		return nil
	}
	asgs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAutoScalingGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	asgIDByName := make(map[string]string, len(asgs))
	for _, a := range asgs {
		if a.Name != nil {
			asgIDByName[*a.Name] = a.ID
		}
	}

	type instruction struct {
		ServiceNamespace  *string `json:"ServiceNamespace"`
		ResourceID        *string `json:"ResourceId"`
		ScalableDimension *string `json:"ScalableDimension"`
	}
	type planAttrs struct {
		ScalingInstructions []instruction `json:"ScalingInstructions"`
	}
	for _, p := range plans {
		var a planAttrs
		if err := json.Unmarshal([]byte(p.AttributesJSON), &a); err != nil {
			continue
		}
		for _, ins := range a.ScalingInstructions {
			ns := sv(ins.ServiceNamespace)
			rid := sv(ins.ResourceID)
			if ns != "autoscaling" || !strings.HasPrefix(rid, "autoScalingGroup/") {
				continue
			}
			asgName := strings.TrimPrefix(rid, "autoScalingGroup/")
			asgID, ok := asgIDByName[asgName]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(p.ID, asgID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert scaling-plan→asg: %w", err)
			}
		}
	}
	return nil
}
