package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveWAFv2Relationships,
		EdgeDecl{TypeWAFv2WebACL, TypeWAFv2RuleGroup, store.RelUses},
		EdgeDecl{TypeWAFv2WebACL, TypeWAFv2IPSet, store.RelUses},
	)
}

// resolveWAFv2Relationships links each WebACL to the rule groups and IP sets it
// references. Rules may nest arbitrarily; only top-level
// Statement.RuleGroupReferenceStatement / IPSetReferenceStatement are inspected.
func resolveWAFv2Relationships(acct *account, st *store.Store) error {
	acls, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeWAFv2WebACL},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}

	type statement struct {
		RuleGroupReferenceStatement *struct {
			ARN *string `json:"ARN"`
		} `json:"RuleGroupReferenceStatement"`
		IPSetReferenceStatement *struct {
			ARN *string `json:"ARN"`
		} `json:"IPSetReferenceStatement"`
	}
	type rule struct {
		Statement *statement `json:"Statement"`
	}
	type aclAttrs struct {
		Rules []rule `json:"Rules"`
	}

	for _, r := range acls {
		var attrs aclAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		seen := make(map[string]bool)
		upsert := func(targetType, arn string) error {
			if arn == "" {
				return nil
			}
			targetID := store.ResourceID("aws", acct.ID, arn)
			if seen[targetID] {
				return nil
			}
			seen[targetID] = true
			if err := st.UpsertRelationship(r.ID, targetID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert wafv2-acl→%s: %w", targetType, err)
			}
			return nil
		}
		for _, rule := range attrs.Rules {
			if rule.Statement == nil {
				continue
			}
			if rule.Statement.RuleGroupReferenceStatement != nil {
				if err := upsert(TypeWAFv2RuleGroup, sv(rule.Statement.RuleGroupReferenceStatement.ARN)); err != nil {
					return err
				}
			}
			if rule.Statement.IPSetReferenceStatement != nil {
				if err := upsert(TypeWAFv2IPSet, sv(rule.Statement.IPSetReferenceStatement.ARN)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
