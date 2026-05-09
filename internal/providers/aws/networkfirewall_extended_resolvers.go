package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveNFLoggingConfigToFirewall,
		EdgeDecl{TypeNetworkFirewallLoggingConfiguration, TypeNetworkFirewallFirewall, store.RelAttachedTo},
	)
	registerResolver(
		resolveNFVpcEndpointAssociationRefs,
		EdgeDecl{TypeNetworkFirewallVpcEndpointAssociation, TypeNetworkFirewallFirewall, store.RelAttachedTo},
		EdgeDecl{TypeNetworkFirewallVpcEndpointAssociation, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeNetworkFirewallVpcEndpointAssociation, TypeEC2Subnet, store.RelAttachedTo},
	)
}

// resolveNFLoggingConfigToFirewall wires each logging-configuration to its
// parent firewall via NativeID `{firewallARN}/logging-configuration`.
func resolveNFLoggingConfigToFirewall(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeNetworkFirewallLoggingConfiguration}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	fwSet, err := scannedIDSet(acct, st, TypeNetworkFirewallFirewall)
	if err != nil {
		return err
	}
	for _, r := range rows {
		fwARN := strings.TrimSuffix(r.NativeID, "/logging-configuration")
		if fwARN == r.NativeID {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeNetworkFirewallFirewall, fwARN)
		if !fwSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert nfw log-config→firewall: %w", err)
		}
	}
	return nil
}

// resolveNFVpcEndpointAssociationRefs wires each VPC-endpoint-association to
// its firewall (FirewallArn), VPC (VpcID), and subnet (SubnetMapping.SubnetID).
func resolveNFVpcEndpointAssociationRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeNetworkFirewallVpcEndpointAssociation}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	fwSet, err := scannedIDSet(acct, st, TypeNetworkFirewallFirewall)
	if err != nil {
		return err
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	subnetSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			FirewallArn   *string `json:"FirewallArn"`
			VpcID         *string `json:"VpcId"`
			SubnetMapping *struct {
				SubnetID *string `json:"SubnetId"`
			} `json:"SubnetMapping"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if fa := sv(attrs.FirewallArn); fa != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeNetworkFirewallFirewall, fa)
			if fwSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert nfw vea→firewall: %w", err)
				}
			}
		}
		if v := sv(attrs.VpcID); v != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", v))
			if vpcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert nfw vea→vpc: %w", err)
				}
			}
		}
		if attrs.SubnetMapping != nil {
			if s := sv(attrs.SubnetMapping.SubnetID); s != "" {
				tgtID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", s))
				if subnetSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert nfw vea→subnet: %w", err)
					}
				}
			}
		}
	}
	return nil
}
