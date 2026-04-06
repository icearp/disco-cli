package aws

import (
	"encoding/json"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveLambdaRelationships) }

func resolveLambdaRelationships(acct *account, st *store.Store) error {
	fns, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLambdaFunction},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range fns {
		var attrs struct {
			Role *string `json:"Role"` // IAM role ARN
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Role != nil {
			// IAM role NativeID is the ARN; ResourceID is derived from it.
			roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, *attrs.Role)
			if err := st.UpsertRelationship(r.ID, roleID, store.RelAssumes, "directed", nil); err != nil {
				return fmt.Errorf("upsert lambda→role relationship: %w", err)
			}
		}
	}
	return nil
}
