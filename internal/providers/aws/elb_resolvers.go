package aws

import (
	"encoding/json"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func resolveELBRelationships(acct *account, st *store.Store) error {
	lbs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{"aws:elasticloadbalancing:load-balancer"},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range lbs {
		var attrs struct {
			Lb *struct {
				VpcId *string `json:"VpcId"`
			} `json:"lb"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Lb != nil && attrs.Lb.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, "aws:ec2:vpc", *attrs.Lb.VpcId)
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert elb→vpc relationship: %w", err)
			}
		}
	}
	return nil
}
