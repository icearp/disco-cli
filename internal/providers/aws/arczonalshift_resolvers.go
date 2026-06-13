package aws

import (
	"encoding/json"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveARCZonalShiftRelationships,
		EdgeDecl{TypeARCZonalShiftConfiguration, TypeELBv2LoadBalancer, store.RelAttachedTo},
		EdgeDecl{TypeARCZonalShiftConfiguration, TypeECSService, store.RelAttachedTo},
	)
}

// resolveARCZonalShiftRelationships wires zonal-autoshift configurations to
// their underlying managed resource via `attached-to`. The managed-resource
// ARN is not type-classified at resolver time (could be ALB, ECS service, EC2
// ASG, etc.), so the edge target uses store.ResourceID across the multi-type
// candidate set.
//
// The autoshift-observer-notification-status singleton has no outbound
// ARN-bearing fields (only an enum Status), so it gets no resolver edges.
func resolveARCZonalShiftRelationships(acct *account, st *store.Store) error {
	cfgs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeARCZonalShiftConfiguration},
		Limit: util.AllResources,
	})
	if err != nil || len(cfgs) == 0 {
		return err
	}
	// Candidate target types — managed-resource ARNs may be ALB, ECS service,
	// EC2 ASG, NLB, etc. FK-safe via per-type id sets.
	candidateTypes := []string{
		TypeELBv2LoadBalancer,
		TypeECSService,
	}
	idsByType := make(map[string]map[string]bool, len(candidateTypes))
	for _, t := range candidateTypes {
		ids, err := scannedIDSet(acct, st, t)
		if err != nil {
			return err
		}
		idsByType[t] = ids
	}
	type attrs struct {
		Arn *string `json:"Arn"`
	}
	for _, c := range cfgs {
		var a attrs
		if err := json.Unmarshal([]byte(c.AttributesJSON), &a); err != nil {
			continue
		}
		mARN := sv(a.Arn)
		if mARN == "" {
			continue
		}
		for t, ids := range idsByType {
			id := store.ResourceID("aws", acct.ID, t, mARN)
			if _, ok := ids[id]; !ok {
				continue
			}
			if err := st.UpsertRelationship(c.ID, id, store.RelAttachedTo, "directed", nil); err != nil {
				return err
			}
			break // managed resource ARN matches at most one candidate type
		}
	}
	return nil
}
