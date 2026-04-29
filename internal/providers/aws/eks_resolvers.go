package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveEKSRelationships) }

func resolveEKSRelationships(acct *account, st *store.Store) error {
	clusters, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEKSCluster},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range clusters {
		var attrs struct {
			ResourcesVpcConfig *struct {
				VpcID *string `json:"VpcID"`
			} `json:"ResourcesVpcConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ResourcesVpcConfig != nil && attrs.ResourcesVpcConfig.VpcID != nil {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.ResourcesVpcConfig.VpcID))
			if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert eks→vpc relationship: %w", err)
			}
		}
	}
	return nil
}
