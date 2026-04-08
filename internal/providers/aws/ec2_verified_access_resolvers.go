package aws

import (
	"encoding/json"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveVerifiedAccessGroupRelationships)
	registerResolver(resolveVerifiedAccessEndpointRelationships)
}

// resolveVerifiedAccessGroupRelationships links each Verified Access group to its instance.
func resolveVerifiedAccessGroupRelationships(acct *account, st *store.Store) error {
	groups, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2VerifiedAccessGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range groups {
		var attrs struct {
			VerifiedAccessInstanceId *string `json:"VerifiedAccessInstanceId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.VerifiedAccessInstanceId != nil {
			instID := store.ResourceID("aws", acct.ID, TypeEC2VerifiedAccessInstance,
				ec2ARN(region, acct.ID, "verified-access-instance", *attrs.VerifiedAccessInstanceId))
			if err := st.UpsertRelationship(r.ID, instID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert verified-access-group→instance relationship: %w", err)
			}
		}
	}
	return nil
}

// resolveVerifiedAccessEndpointRelationships links each endpoint to its group.
func resolveVerifiedAccessEndpointRelationships(acct *account, st *store.Store) error {
	eps, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2VerifiedAccessEndpoint},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range eps {
		var attrs struct {
			VerifiedAccessGroupId *string `json:"VerifiedAccessGroupId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.VerifiedAccessGroupId != nil {
			groupID := store.ResourceID("aws", acct.ID, TypeEC2VerifiedAccessGroup,
				ec2ARN(region, acct.ID, "verified-access-group", *attrs.VerifiedAccessGroupId))
			if err := st.UpsertRelationship(r.ID, groupID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert verified-access-endpoint→group relationship: %w", err)
			}
		}
	}
	return nil
}
