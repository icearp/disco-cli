package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveKMSAliases,
		EdgeDecl{TypeKMSAlias, TypeKMSKey, store.RelAttachedTo},
		EdgeDecl{TypeKMSKey, TypeKMSAlias, store.RelContains},
	)
	registerResolver(resolveKMSGrants,
		EdgeDecl{TypeKMSGrant, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeKMSGrant, TypeIAMUser, store.RelUses},
	)
	registerResolver(resolveKMSGrantEncryptionContext,
		EdgeDecl{TypeKMSGrant, TypeLambdaFunction, store.RelUses},
	)
}

// resolveKMSGrants links each KMS grant to its grantee + retiring principal
// (IAM role or user). Service principals like "ec2.amazonaws.com" and
// ephemeral assumed-role/federated session ARNs are skipped — no target
// resource exists. Cross-account principals are FK-safe-skipped.
func resolveKMSGrants(acct *account, st *store.Store) error {
	grants, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeKMSGrant},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(grants) == 0 {
		return nil
	}
	principals, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeIAMRole, TypeIAMUser},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(principals))
	for _, p := range principals {
		known[p.ID] = true
	}
	for _, gr := range grants {
		var a struct {
			GranteePrincipal  *string `json:"GranteePrincipal"`
			RetiringPrincipal *string `json:"RetiringPrincipal"`
		}
		if err := json.Unmarshal([]byte(gr.AttributesJSON), &a); err != nil {
			continue
		}
		for _, p := range []*string{a.GranteePrincipal, a.RetiringPrincipal} {
			arn := sv(p)
			if arn == "" || !strings.HasPrefix(arn, "arn:aws:iam::") {
				continue
			}
			var ptype string
			switch {
			case strings.Contains(arn, ":role/"):
				ptype = TypeIAMRole
			case strings.Contains(arn, ":user/"):
				ptype = TypeIAMUser
			default:
				continue
			}
			pid := store.ResourceID("aws", acct.ID, ptype, arn)
			if !known[pid] {
				continue
			}
			if err := st.UpsertRelationship(gr.ID, pid, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert kms-grant→principal: %w", err)
			}
		}
	}
	return nil
}

// resolveKMSAliases links each alias to the key it points at. TargetKeyID on an
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
			TargetKeyID *string `json:"TargetKeyID"`
		}
		if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil || attrs.TargetKeyID == nil {
			continue
		}
		target := *attrs.TargetKeyID
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
		// Inverse: the key owns its aliases (a key can have multiple aliases).
		if err := st.UpsertRelationship(keyID, a.ID, store.RelContains, "directed", nil); err != nil {
			return fmt.Errorf("upsert kms-key→alias: %w", err)
		}
	}
	return nil
}
