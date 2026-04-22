package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveKMSAliases) }

// resolveKMSAliases links each alias to the key it points at. TargetKeyId on an
// AliasListEntry is either a bare KeyId or a full ARN; the bare form is resolved
// by rebuilding the key ARN from the alias's region.
func resolveKMSAliases(acct *account, st *store.Store) error {
	aliases, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeKMSAlias},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	keys, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeKMSKey},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	knownKey := make(map[string]bool, len(keys))
	for _, k := range keys {
		knownKey[k.ID] = true
	}
	for _, a := range aliases {
		var attrs struct {
			TargetKeyId *string `json:"TargetKeyId"`
		}
		if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil || attrs.TargetKeyId == nil {
			continue
		}
		target := *attrs.TargetKeyId
		if !strings.HasPrefix(target, "arn:") {
			target = fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", sv(a.Region), acct.ID, target)
		}
		keyID := store.ResourceID("aws", acct.ID, TypeKMSKey, target)
		if !knownKey[keyID] {
			continue
		}
		if err := st.UpsertRelationship(a.ID, keyID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert kms-alias→key: %w", err)
		}
	}
	return nil
}
