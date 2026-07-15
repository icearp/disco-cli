package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveDataZoneGroupProfileRefs,
		EdgeDecl{TypeDataZoneGroupProfile, TypeIAMRole, store.RelUses},
	)
}

// resolveDataZoneGroupProfileRefs wires each IAM-role-session group profile to
// the IAM role it federates through (RolePrincipalArn). SSO group profiles carry
// no role ARN and emit no edge.
func resolveDataZoneGroupProfileRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDataZoneGroupProfile}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RolePrincipalArn *string `json:"RolePrincipalArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		role := sv(attrs.RolePrincipalArn)
		if role == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, role)
		if roleSet[tgtID] {
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert datazone group-profile→role: %w", err)
			}
		}
	}
	return nil
}
