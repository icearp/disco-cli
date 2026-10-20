package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveR53RResolverEndpointVPC,
		EdgeDecl{TypeRoute53ResolverResolverEndpoint, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(
		resolveR53RDNSSECConfigVPC,
		EdgeDecl{TypeRoute53ResolverResolverDNSSECConfig, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(
		resolveR53RQueryLogAssocRefs,
		EdgeDecl{TypeRoute53ResolverResolverQueryLoggingConfigAssociation, TypeRoute53ResolverResolverQueryLoggingConfig, store.RelAttachedTo},
		EdgeDecl{TypeRoute53ResolverResolverQueryLoggingConfigAssociation, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(
		noopR53RHierarchy,
		EdgeDecl{TypeRoute53ResolverFirewallRuleGroup, TypeRoute53ResolverFirewallRule, store.RelContains},
		EdgeDecl{TypeRoute53ResolverResolverEndpoint, TypeRoute53ResolverResolverEndpointIPAddress, store.RelContains},
	)
	registerResolver(
		resolveR53REndpointIPSubnet,
		EdgeDecl{TypeRoute53ResolverResolverEndpointIPAddress, TypeEC2Subnet, store.RelAttachedTo},
	)
	registerResolver(
		resolveR53RFirewallRuleDomainList,
		EdgeDecl{TypeRoute53ResolverFirewallRule, TypeRoute53ResolverFirewallDomainList, store.RelUses},
	)
}

// noopR53RHierarchy only declares the contains edges that
// scanR53RFirewallRules and scanR53RResolverEndpointIPAddresses record at
// scan time.
func noopR53RHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveR53REndpointIPSubnet links each resolver endpoint IP address to the
// subnet it sits in.
func resolveR53REndpointIPSubnet(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRoute53ResolverResolverEndpointIPAddress}, Limit: util.AllResources,
	})
	if err != nil || len(rows) == 0 {
		return err
	}
	subnetRows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2Subnet}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	subnets := make(map[string]bool, len(subnetRows))
	for _, s := range subnetRows {
		subnets[s.ID] = true
	}
	for _, r := range rows {
		var attrs struct {
			SubnetID *string `json:"SubnetId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			return fmt.Errorf("decode r53r endpoint ip address %s: %w", r.NativeID, err)
		}
		subnetID := sv(attrs.SubnetID)
		if subnetID == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, ec2ARN(sv(r.Region), acct.ID, "subnet", subnetID))
		if !subnets[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert r53r endpoint ip address→subnet: %w", err)
		}
	}
	return nil
}

// resolveR53RFirewallRuleDomainList links each firewall rule to the domain
// list it matches. Rules often match AWS-managed lists, so the index includes
// managed rows.
func resolveR53RFirewallRuleDomainList(acct *account, st *store.Store) error {
	rules, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRoute53ResolverFirewallRule}, Limit: util.AllResources,
	})
	if err != nil || len(rules) == 0 {
		return err
	}
	lists, err := r53rDomainListIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rules {
		var attrs struct {
			FirewallDomainListID *string `json:"FirewallDomainListId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			return fmt.Errorf("decode r53r firewall rule %s: %w", r.NativeID, err)
		}
		tgtID, ok := lists[r53rRegionKey(sv(r.Region), sv(attrs.FirewallDomainListID))]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert r53r firewall rule→domain list: %w", err)
		}
	}
	return nil
}

func r53rRegionKey(region, id string) string { return region + "/" + id }

// r53rDomainListIndex maps (region, list Id) to the stored domain list's row
// ID, taking the Id from the list ARN's resource segment. The ARN's account is
// not compared: whether an AWS-managed list's ARN names this account is
// unverified.
func r53rDomainListIndex(acct *account, st *store.Store) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRoute53ResolverFirewallDomainList},
		IncludeManaged: true, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		if _, id, ok := strings.Cut(r.NativeID, ":firewall-domain-list/"); ok {
			idx[r53rRegionKey(sv(r.Region), id)] = r.ID
		}
	}
	return idx, nil
}

func resolveR53RResolverEndpointVPC(acct *account, st *store.Store) error {
	return r53rResolveVPCField(acct, st, TypeRoute53ResolverResolverEndpoint, "HostVPCId")
}

func resolveR53RDNSSECConfigVPC(acct *account, st *store.Store) error {
	return r53rResolveVPCField(acct, st, TypeRoute53ResolverResolverDNSSECConfig, "ResourceId")
}

func r53rResolveVPCField(acct *account, st *store.Store, sourceType, fieldName string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{sourceType}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var raw map[string]any
		if err := json.Unmarshal([]byte(r.AttributesJSON), &raw); err != nil {
			continue
		}
		v, ok := raw[fieldName].(string)
		if !ok || v == "" {
			continue
		}
		region := sv(r.Region)
		vARN := ec2ARN(region, acct.ID, "vpc", v)
		tgtID := store.ResourceID("aws", acct.ID, vARN)
		if !vpcSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert r53r %s→vpc: %w", sourceType, err)
		}
	}
	return nil
}

// resolveR53RQueryLogAssocRefs wires query-log-config-association to its
// query-log-config (by ID lookup) and to the VPC carrying the associated resource.
func resolveR53RQueryLogAssocRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRoute53ResolverResolverQueryLoggingConfigAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	cfgIdx, err := r53rQueryLogConfigIDIndex(acct, st)
	if err != nil {
		return err
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ResolverQueryLogConfigID *string `json:"ResolverQueryLogConfigId"`
			ResourceID               *string `json:"ResourceId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if cid := sv(attrs.ResolverQueryLogConfigID); cid != "" {
			if tgtID, ok := cfgIdx[cid]; ok {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert r53r ql-assoc→config: %w", err)
				}
			}
		}
		if vid := sv(attrs.ResourceID); vid != "" {
			region := sv(r.Region)
			vARN := ec2ARN(region, acct.ID, "vpc", vid)
			tgtID := store.ResourceID("aws", acct.ID, vARN)
			if vpcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert r53r ql-assoc→vpc: %w", err)
				}
			}
		}
	}
	return nil
}

func r53rQueryLogConfigIDIndex(acct *account, st *store.Store) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeRoute53ResolverResolverQueryLoggingConfig},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		var attrs struct {
			ID *string `json:"Id"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.ID); id != "" {
			idx[id] = r.ID
		}
	}
	return idx, nil
}
