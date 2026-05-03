package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveSESEmailIdentityConfigSet,
		EdgeDecl{TypeSESEmailIdentity, TypeSESConfigurationSet, store.RelUses},
	)
}

// sesEmailIdentityAttrs mirrors the verbatim GetEmailIdentityOutput fields
// used by the resolver. PascalCase tags match mustJSON of the SDK v2 struct.
type sesEmailIdentityAttrs struct {
	ConfigurationSetName *string `json:"ConfigurationSetName"`
}

// resolveSESEmailIdentityConfigSet emits a `uses` edge from each email
// identity to its default configuration set, when the config set is also
// scanned in the same (account, region). Identities without a default
// config-set or referencing an unscanned name skip silently. FK-safe via
// scanned config-set id set; cross-region refs intentionally not supported
// (config sets are region-scoped and SES v2 enforces same-region).
func resolveSESEmailIdentityConfigSet(acct *account, st *store.Store) error {
	identities, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeSESEmailIdentity},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(identities) == 0 {
		return nil
	}

	cfgIDs, err := resourceIDSet(st, acct.ID, TypeSESConfigurationSet)
	if err != nil {
		return err
	}
	if len(cfgIDs) == 0 {
		return nil
	}

	for _, ident := range identities {
		var attrs sesEmailIdentityAttrs
		if err := json.Unmarshal([]byte(ident.AttributesJSON), &attrs); err != nil {
			continue
		}
		cfgName := sv(attrs.ConfigurationSetName)
		if cfgName == "" || ident.Region == nil {
			continue
		}
		cfgARN := sesConfigurationSetARN(*ident.Region, acct.ID, cfgName)
		cfgID := store.ResourceID("aws", acct.ID, TypeSESConfigurationSet, cfgARN)
		if _, ok := cfgIDs[cfgID]; !ok {
			continue
		}
		if err := st.UpsertRelationship(ident.ID, cfgID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert ses email-identity→config-set: %w", err)
		}
	}
	return nil
}
