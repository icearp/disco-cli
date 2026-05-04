package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveMMIngressPointRefs,
		EdgeDecl{TypeSESMailManagerIngressPoint, TypeSESMailManagerRuleSet, store.RelAttachedTo},
		EdgeDecl{TypeSESMailManagerIngressPoint, TypeSESMailManagerTrafficPolicy, store.RelAttachedTo},
	)
	registerResolver(resolveMMArchiveKMS,
		EdgeDecl{TypeSESMailManagerArchive, TypeKMSKey, store.RelUses},
	)
	registerResolver(resolveMMAddonInstanceToSubscription,
		EdgeDecl{TypeSESMailManagerAddonInstance, TypeSESMailManagerAddonSubscription, store.RelAttachedTo},
	)
}

// mmChildIDByID builds a map from MailManager resource ID (RuleSetId,
// TrafficPolicyId, etc.) → store resource ID, scoped per-region. Each
// row's NativeID is `arn:aws:ses:{region}:{acct}:mailmanager-{kind}/{id}`
// — strip past the last `/` for the lookup key.
func mmChildIDByID(acct *account, st *store.Store, rtype string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{rtype}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, r := range rows {
		// trailing segment after last `/` is the ID.
		nid := r.NativeID
		for i := len(nid) - 1; i >= 0; i-- {
			if nid[i] == '/' {
				out[sv(r.Region)+"|"+nid[i+1:]] = r.ID
				break
			}
		}
	}
	return out, nil
}

// resolveMMIngressPointRefs wires ingress-point → rule-set + traffic-policy
// via RuleSetId + TrafficPolicyId attrs (added by GetIngressPoint enrichment).
func resolveMMIngressPointRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSESMailManagerIngressPoint}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	rsIdx, err := mmChildIDByID(acct, st, TypeSESMailManagerRuleSet)
	if err != nil {
		return err
	}
	tpIdx, err := mmChildIDByID(acct, st, TypeSESMailManagerTrafficPolicy)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RuleSetId       *string `json:"RuleSetId"`
			TrafficPolicyId *string `json:"TrafficPolicyId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if rs := sv(attrs.RuleSetId); rs != "" {
			if tgtID, ok := rsIdx[region+"|"+rs]; ok {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert mm ip→ruleset: %w", err)
				}
			}
		}
		if tp := sv(attrs.TrafficPolicyId); tp != "" {
			if tgtID, ok := tpIdx[region+"|"+tp]; ok {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert mm ip→tp: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveMMArchiveKMS wires archive → KMS key (KmsKeyArn from GetArchive).
func resolveMMArchiveKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSESMailManagerArchive}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KmsKeyArn *string `json:"KmsKeyArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if k := sv(attrs.KmsKeyArn); k != "" {
			if keyID, ok := kmsIdx.resolveKMSKeyID(k, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert mm archive→kms: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveMMAddonInstanceToSubscription wires addon-instance → addon-subscription
// via AddonSubscriptionId — already present in ListAddonInstances summary.
func resolveMMAddonInstanceToSubscription(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSESMailManagerAddonInstance}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	subRows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSESMailManagerAddonSubscription}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	subByID := map[string]string{}
	for _, sr := range subRows {
		var sa struct {
			AddonSubscriptionId *string `json:"AddonSubscriptionId"`
		}
		if err := json.Unmarshal([]byte(sr.AttributesJSON), &sa); err != nil {
			continue
		}
		if id := sv(sa.AddonSubscriptionId); id != "" {
			subByID[sv(sr.Region)+"|"+id] = sr.ID
		}
	}
	for _, r := range rows {
		var attrs struct {
			AddonSubscriptionId *string `json:"AddonSubscriptionId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.AddonSubscriptionId); id != "" {
			if tgtID, ok := subByID[sv(r.Region)+"|"+id]; ok {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert mm ai→sub: %w", err)
				}
			}
		}
	}
	return nil
}
