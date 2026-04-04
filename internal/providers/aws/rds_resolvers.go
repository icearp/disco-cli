package aws

import (
	"encoding/json"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func resolveRDSRelationships(acct *account, st *store.Store) error {
	dbs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{"aws:rds:db-instance"},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range dbs {
		var attrs struct {
			DBSubnetGroup *struct {
				VpcId *string `json:"VpcId"`
			} `json:"DBSubnetGroup"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.DBSubnetGroup != nil && attrs.DBSubnetGroup.VpcId != nil {
			vpcID := store.ResourceID("aws", acct.ID, "aws:ec2:vpc", *attrs.DBSubnetGroup.VpcId)
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert rds→vpc relationship: %w", err)
			}
		}
	}
	return nil
}
