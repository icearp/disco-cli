package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveControlTowerBaselineTarget,
		EdgeDecl{TypeControlTowerEnabledBaseline, TypeOrganizationsAccount, store.RelAttachedTo},
		EdgeDecl{TypeControlTowerEnabledBaseline, TypeOrganizationsOU, store.RelAttachedTo},
	)
}

// controlTowerBaselineAttrs mirrors the verbatim EnabledBaselineSummary
// fields used by the resolver. PascalCase tags match mustJSON of the
// SDK v2 struct.
type controlTowerBaselineAttrs struct {
	TargetIdentifier *string `json:"TargetIdentifier"`
}

// resolveControlTowerBaselineTarget emits an `attached-to` edge from each
// enabled baseline to its target Organizations OU or account. The
// TargetIdentifier is an Organizations ARN (account or OU); both
// resolve via existing Organizations id-sets. FK-safe via per-type id
// sets; targets outside the scanned org tree skip silently.
func resolveControlTowerBaselineTarget(acct *account, st *store.Store) error {
	baselines, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeControlTowerEnabledBaseline},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(baselines) == 0 {
		return nil
	}

	ouIDs, err := resourceIDSet(st, acct.ID, TypeOrganizationsOU)
	if err != nil {
		return err
	}
	acctIDs, err := resourceIDSet(st, acct.ID, TypeOrganizationsAccount)
	if err != nil {
		return err
	}

	for _, b := range baselines {
		var attrs controlTowerBaselineAttrs
		if err := json.Unmarshal([]byte(b.AttributesJSON), &attrs); err != nil {
			continue
		}
		target := sv(attrs.TargetIdentifier)
		if target == "" {
			continue
		}
		// TargetIdentifier is an Organizations ARN.
		// Account ARN: arn:aws:organizations::<mgmt>:account/<o-id>/<acct-id>
		// OU ARN:      arn:aws:organizations::<mgmt>:ou/<o-id>/<ou-id>
		var targetType, targetID string
		switch {
		case strings.Contains(target, ":account/"):
			targetType = TypeOrganizationsAccount
			targetID = store.ResourceID("aws", acct.ID, TypeOrganizationsAccount, target)
			if _, ok := acctIDs[targetID]; !ok {
				continue
			}
		case strings.Contains(target, ":ou/"):
			targetType = TypeOrganizationsOU
			targetID = store.ResourceID("aws", acct.ID, TypeOrganizationsOU, target)
			if _, ok := ouIDs[targetID]; !ok {
				continue
			}
		default:
			continue
		}
		_ = targetType
		if err := st.UpsertRelationship(b.ID, targetID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert controltower baseline→org target: %w", err)
		}
	}
	return nil
}
