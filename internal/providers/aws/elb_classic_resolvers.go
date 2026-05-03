package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveELBClassicRelationships,
		EdgeDecl{TypeELBClassicLoadBalancer, TypeEC2VPC, store.RelAttachedTo},
	)
}

// resolveELBClassicRelationships links each Classic load balancer to its VPC.
// Classic ELBs without a VPC (EC2-Classic) produce no relationship.
func resolveELBClassicRelationships(acct *account, st *store.Store) error {
	lbs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeELBClassicLoadBalancer},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range lbs {
		var attrs struct {
			VPCId *string `json:"VPCId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.VPCId == nil || *attrs.VPCId == "" {
			continue
		}
		vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.VPCId))
		if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert classic-lb→vpc relationship: %w", err)
		}
	}
	return nil
}
