package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveCloud9EnvOwner,
		EdgeDecl{TypeCloud9EnvironmentEC2, TypeIAMRole, store.RelAttachedTo},
		EdgeDecl{TypeCloud9EnvironmentEC2, TypeIAMUser, store.RelAttachedTo},
	)
}

// resolveCloud9EnvOwner wires each environment to its owner principal
// (OwnerArn): IAM role, IAM user, or account root (`arn:aws:iam::<acct>:root`,
// skipped — no scanned target). Dispatched by ARN segment substring.
func resolveCloud9EnvOwner(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCloud9EnvironmentEC2}, Limit: util.AllResources,
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
	userSet, err := scannedIDSet(acct, st, TypeIAMUser)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			OwnerArn *string `json:"OwnerArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		oa := sv(attrs.OwnerArn)
		if oa == "" {
			continue
		}
		var tgt string
		switch {
		case strings.Contains(oa, ":role/") || strings.Contains(oa, ":assumed-role/"):
			t := store.ResourceID("aws", acct.ID, TypeIAMRole, oa)
			if !roleSet[t] {
				continue
			}
			tgt = t
		case strings.Contains(oa, ":user/"):
			t := store.ResourceID("aws", acct.ID, TypeIAMUser, oa)
			if !userSet[t] {
				continue
			}
			tgt = t
		default:
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert cloud9 env→owner: %w", err)
		}
	}
	return nil
}
