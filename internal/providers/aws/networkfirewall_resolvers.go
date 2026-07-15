package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveNetworkFirewallFirewallRelationships,
		EdgeDecl{TypeNetworkFirewallFirewall, TypeNetworkFirewallFirewallPolicy, store.RelUses},
		EdgeDecl{TypeNetworkFirewallFirewall, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeNetworkFirewallFirewall, TypeEC2Subnet, store.RelAttachedTo},
	)
	registerResolver(
		resolveNetworkFirewallPolicyRelationships,
		EdgeDecl{TypeNetworkFirewallFirewallPolicy, TypeNetworkFirewallRuleGroup, store.RelUses},
	)
	registerResolver(
		resolveNetworkFirewallRuleGroupKMS,
		EdgeDecl{TypeNetworkFirewallRuleGroup, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveNetworkFirewallTLSInspectionRefs,
		EdgeDecl{TypeNetworkFirewallTLSInspectionConfiguration, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeNetworkFirewallTLSInspectionConfiguration, TypeACMCertificate, store.RelUses},
	)
}

// resolveNetworkFirewallRuleGroupKMS reads
// RuleGroupResponse.EncryptionConfiguration.KeyId from each rule-group's
// DescribeRuleGroup body and emits a KMS edge.
func resolveNetworkFirewallRuleGroupKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeNetworkFirewallRuleGroup}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RuleGroupResponse *struct {
				EncryptionConfiguration *struct {
					KeyID *string `json:"KeyId"`
				} `json:"EncryptionConfiguration"`
			} `json:"RuleGroupResponse"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.RuleGroupResponse == nil || attrs.RuleGroupResponse.EncryptionConfiguration == nil {
			continue
		}
		ref := sv(attrs.RuleGroupResponse.EncryptionConfiguration.KeyID)
		if ref == "" {
			continue
		}
		if keyID, ok := idx.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
			if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert nfw rule-group→kms: %w", err)
			}
		}
	}
	return nil
}

// resolveNetworkFirewallTLSInspectionRefs wires TLS-inspection configs to
// their CMEK and ACM certs. CertificateAuthority + Certificates[] both
// carry CertificateArn refs.
func resolveNetworkFirewallTLSInspectionRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeNetworkFirewallTLSInspectionConfiguration}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	acmSet, err := scannedIDSet(acct, st, TypeACMCertificate)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			TLSInspectionConfigurationResponse *struct {
				EncryptionConfiguration *struct {
					KeyID *string `json:"KeyId"`
				} `json:"EncryptionConfiguration"`
				CertificateAuthority *struct {
					CertificateArn *string `json:"CertificateArn"`
				} `json:"CertificateAuthority"`
				Certificates []struct {
					CertificateArn *string `json:"CertificateArn"`
				} `json:"Certificates"`
			} `json:"TLSInspectionConfigurationResponse"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.TLSInspectionConfigurationResponse == nil {
			continue
		}
		resp := attrs.TLSInspectionConfigurationResponse
		if resp.EncryptionConfiguration != nil {
			if ref := sv(resp.EncryptionConfiguration.KeyID); ref != "" {
				if keyID, ok := idx.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
					if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert nfw tls→kms: %w", err)
					}
				}
			}
		}
		var certARNs []string
		if resp.CertificateAuthority != nil {
			certARNs = append(certARNs, sv(resp.CertificateAuthority.CertificateArn))
		}
		for _, c := range resp.Certificates {
			certARNs = append(certARNs, sv(c.CertificateArn))
		}
		for _, ca := range certARNs {
			if ca == "" {
				continue
			}
			tgt := store.ResourceID("aws", acct.ID, ca)
			if !acmSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert nfw tls→acm: %w", err)
			}
		}
	}
	return nil
}

// nfFirewallAttrs extracts the fields on DescribeFirewall's response needed
// for edge emission. PascalCase tags match mustJSON's AWS SDK v2 response
// output (no json tags).
type nfFirewallAttrs struct {
	Firewall *struct {
		FirewallPolicyArn *string `json:"FirewallPolicyArn"`
		VpcID             *string `json:"VpcID"`
		SubnetMappings    []struct {
			SubnetID *string `json:"SubnetID"`
		} `json:"SubnetMappings"`
	} `json:"Firewall"`
}

// nfPolicyAttrs extracts the rule-group references from DescribeFirewallPolicy.
// Covers both stateless (priority-ordered) and stateful references.
type nfPolicyAttrs struct {
	FirewallPolicy *struct {
		StatelessRuleGroupReferences []struct {
			ResourceArn *string `json:"ResourceArn"`
		} `json:"StatelessRuleGroupReferences"`
		StatefulRuleGroupReferences []struct {
			ResourceArn *string `json:"ResourceArn"`
		} `json:"StatefulRuleGroupReferences"`
	} `json:"FirewallPolicy"`
}

// resolveNetworkFirewallFirewallRelationships emits edges from each Network
// Firewall to: its firewall policy (uses), its VPC (attached-to), and each
// subnet mapping (attached-to). FK-safe: skips targets absent from the store.
func resolveNetworkFirewallFirewallRelationships(acct *account, st *store.Store) error {
	firewalls, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeNetworkFirewallFirewall},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(firewalls) == 0 {
		return nil
	}

	known, err := networkFirewallFirewallTargetIDSet(acct, st)
	if err != nil {
		return err
	}

	for _, f := range firewalls {
		var attrs nfFirewallAttrs
		if err := json.Unmarshal([]byte(f.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Firewall == nil {
			continue
		}
		region := sv(f.Region)

		if arn := sv(attrs.Firewall.FirewallPolicyArn); arn != "" {
			polID := store.ResourceID("aws", acct.ID, arn)
			if known[polID] {
				if err := st.UpsertRelationship(f.ID, polID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert network-firewall firewall→policy: %w", err)
				}
			}
		}

		if vpc := sv(attrs.Firewall.VpcID); vpc != "" {
			vpcARN := ec2ARN(region, acct.ID, "vpc", vpc)
			vpcID := store.ResourceID("aws", acct.ID, vpcARN)
			if known[vpcID] {
				if err := st.UpsertRelationship(f.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert network-firewall firewall→vpc: %w", err)
				}
			}
		}

		for _, m := range attrs.Firewall.SubnetMappings {
			sn := sv(m.SubnetID)
			if sn == "" {
				continue
			}
			snARN := ec2ARN(region, acct.ID, "subnet", sn)
			snID := store.ResourceID("aws", acct.ID, snARN)
			if !known[snID] {
				continue
			}
			if err := st.UpsertRelationship(f.ID, snID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert network-firewall firewall→subnet: %w", err)
			}
		}
	}
	return nil
}

// resolveNetworkFirewallPolicyRelationships emits edges from each firewall
// policy to the rule groups it references (stateless + stateful). FK-safe.
func resolveNetworkFirewallPolicyRelationships(acct *account, st *store.Store) error {
	policies, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeNetworkFirewallFirewallPolicy},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(policies) == 0 {
		return nil
	}

	rgSet, err := networkFirewallRuleGroupIDSet(acct, st)
	if err != nil {
		return err
	}

	for _, p := range policies {
		var attrs nfPolicyAttrs
		if err := json.Unmarshal([]byte(p.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.FirewallPolicy == nil {
			continue
		}

		refs := make([]string, 0, len(attrs.FirewallPolicy.StatelessRuleGroupReferences)+len(attrs.FirewallPolicy.StatefulRuleGroupReferences))
		for _, r := range attrs.FirewallPolicy.StatelessRuleGroupReferences {
			refs = append(refs, sv(r.ResourceArn))
		}
		for _, r := range attrs.FirewallPolicy.StatefulRuleGroupReferences {
			refs = append(refs, sv(r.ResourceArn))
		}

		for _, arn := range refs {
			if arn == "" {
				continue
			}
			rgID := store.ResourceID("aws", acct.ID, arn)
			if !rgSet[rgID] {
				continue
			}
			if err := st.UpsertRelationship(p.ID, rgID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert network-firewall policy→rule-group: %w", err)
			}
		}
	}
	return nil
}

// networkFirewallFirewallTargetIDSet pre-builds the id set covering all
// resource types referenced from firewall attributes (policy, VPC, subnet).
func networkFirewallFirewallTargetIDSet(acct *account, st *store.Store) (map[string]bool, error) {
	targets, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeNetworkFirewallFirewallPolicy, TypeEC2VPC, TypeEC2Subnet},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(targets))
	for _, r := range targets {
		m[r.ID] = true
	}
	return m, nil
}

func networkFirewallRuleGroupIDSet(acct *account, st *store.Store) (map[string]bool, error) {
	targets, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeNetworkFirewallRuleGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(targets))
	for _, r := range targets {
		m[r.ID] = true
	}
	return m, nil
}
