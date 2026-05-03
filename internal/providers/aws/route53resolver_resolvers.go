package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveR53RResolverConfigVPC,
		EdgeDecl{TypeRoute53ResolverResolverConfig, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(resolveR53RResolverRuleAssoc,
		EdgeDecl{TypeRoute53ResolverResolverRuleAssociation, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeRoute53ResolverResolverRuleAssociation, TypeRoute53ResolverResolverRule, store.RelAttachedTo},
	)
	registerResolver(resolveR53RFirewallRuleGroupAssoc,
		EdgeDecl{TypeRoute53ResolverFirewallRuleGroupAssociation, TypeEC2VPC, store.RelAttachedTo},
	)
}

// resolveR53RResolverConfigVPC links each ResolverConfig to the VPC it
// configures. ResolverConfig has no native ARN; the scanner stores
// `ResourceId` (the VPC ID) so we rebuild the canonical EC2 VPC ARN here.
func resolveR53RResolverConfigVPC(acct *account, st *store.Store) error {
	cfgs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeRoute53ResolverResolverConfig},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	for _, r := range cfgs {
		var attrs struct {
			ResourceID *string `json:"ResourceId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ResourceID == nil || *attrs.ResourceID == "" {
			continue
		}
		vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC,
			ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.ResourceID))
		if !vpcSet[vpcID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert resolver-config→vpc: %w", err)
		}
	}
	return nil
}

// resolveR53RResolverRuleAssoc links each ResolverRuleAssociation to its VPC
// and the ResolverRule it activates.
func resolveR53RResolverRuleAssoc(acct *account, st *store.Store) error {
	assocs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeRoute53ResolverResolverRuleAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	ruleSet, err := scannedIDSet(acct, st, TypeRoute53ResolverResolverRule)
	if err != nil {
		return err
	}
	for _, r := range assocs {
		var attrs struct {
			VPCID          *string `json:"VPCId"`
			ResolverRuleID *string `json:"ResolverRuleId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.VPCID != nil && *attrs.VPCID != "" {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC,
				ec2ARN(region, acct.ID, "vpc", *attrs.VPCID))
			if vpcSet[vpcID] {
				if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert resolver-rule-assoc→vpc: %w", err)
				}
			}
		}
		if attrs.ResolverRuleID != nil && *attrs.ResolverRuleID != "" {
			ruleARN := r53rARN(region, acct.ID, "resolver-rule", *attrs.ResolverRuleID)
			ruleID := store.ResourceID("aws", acct.ID, TypeRoute53ResolverResolverRule, ruleARN)
			if ruleSet[ruleID] {
				if err := st.UpsertRelationship(r.ID, ruleID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert resolver-rule-assoc→rule: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveR53RFirewallRuleGroupAssoc links each FirewallRuleGroupAssociation
// to the VPC it protects. The SDK field is `VpcId` (lowercase d).
func resolveR53RFirewallRuleGroupAssoc(acct *account, st *store.Store) error {
	assocs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeRoute53ResolverFirewallRuleGroupAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	for _, r := range assocs {
		var attrs struct {
			VpcID *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.VpcID == nil || *attrs.VpcID == "" {
			continue
		}
		vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC,
			ec2ARN(sv(r.Region), acct.ID, "vpc", *attrs.VpcID))
		if !vpcSet[vpcID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert firewall-rule-group-assoc→vpc: %w", err)
		}
	}
	return nil
}
