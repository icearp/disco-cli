package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveOpenSearchConnections,
		EdgeDecl{TypeOpenSearchOutboundConnection, TypeOpenSearchDomain, store.RelAttachedTo},
		EdgeDecl{TypeOpenSearchInboundConnection, TypeOpenSearchDomain, store.RelAttachedTo},
	)
	registerResolver(
		resolveOpenSearchVpcEndpoints,
		EdgeDecl{TypeOpenSearchVpcEndpoint, TypeOpenSearchDomain, store.RelAttachedTo},
		EdgeDecl{TypeOpenSearchVpcEndpoint, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeOpenSearchVpcEndpoint, TypeEC2Subnet, store.RelUses},
		EdgeDecl{TypeOpenSearchVpcEndpoint, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(
		resolveOpenSearchDataSourceAttachments,
		EdgeDecl{TypeOpenSearchDataSourceAttachment, TypeOpenSearchDomain, store.RelUses},
	)
	// scanOpenSearchDataSourceAttachments records application → attachment via
	// childFanOut; declared here so coverage sees the edge as wired.
	registerResolver(
		noopOpenSearchHierarchy,
		EdgeDecl{TypeOpenSearchApplication, TypeOpenSearchDataSourceAttachment, store.RelContains},
	)
}

func noopOpenSearchHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveOpenSearchConnections wires each cross-cluster connection to the
// domain it is stored under: the NativeID prefix before
// `/outbound-connection/` or `/inbound-connection/`.
func resolveOpenSearchConnections(acct *account, st *store.Store) error {
	domains, err := scannedIDSet(acct, st, TypeOpenSearchDomain)
	if err != nil {
		return err
	}
	for rtype, sep := range map[string]string{
		TypeOpenSearchOutboundConnection: "/outbound-connection/",
		TypeOpenSearchInboundConnection:  "/inbound-connection/",
	} {
		rows, err := listAccountRows(acct, st, rtype)
		if err != nil {
			return err
		}
		for _, r := range rows {
			domainARN, _, ok := strings.Cut(r.NativeID, sep)
			if !ok {
				continue
			}
			domainID := store.ResourceID("aws", acct.ID, domainARN)
			if !domains[domainID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, domainID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert %s→domain: %w", rtype, err)
			}
		}
	}
	return nil
}

// openSearchVpcEndpointAttrs mirrors the VpcEndpoint fields the resolver reads.
type openSearchVpcEndpointAttrs struct {
	DomainArn  *string `json:"DomainArn"`
	VpcOptions *struct {
		VPCId            *string  `json:"VPCId"`
		SubnetIDs        []string `json:"SubnetIds"`
		SecurityGroupIDs []string `json:"SecurityGroupIds"`
	} `json:"VpcOptions"`
}

// openSearchEdge is one candidate edge, emitted only when set holds targetID.
type openSearchEdge struct {
	targetID string
	set      map[string]bool
	kind     string
}

// resolveOpenSearchVpcEndpoints wires each managed VPC endpoint to the domain
// it reaches (DomainArn; only a domain scanned in this account), and to its
// VPC, subnets and security groups (VpcOptions).
func resolveOpenSearchVpcEndpoints(acct *account, st *store.Store) error {
	rows, err := listAccountRows(acct, st, TypeOpenSearchVpcEndpoint)
	if err != nil || len(rows) == 0 {
		return err
	}
	domains, err := scannedIDSet(acct, st, TypeOpenSearchDomain)
	if err != nil {
		return err
	}
	vpcs, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	subnets, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	sgs, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs openSearchVpcEndpointAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			return fmt.Errorf("decode opensearch vpc-endpoint %s: %w", r.NativeID, err)
		}
		edges := []openSearchEdge{{store.ResourceID("aws", acct.ID, sv(attrs.DomainArn)), domains, store.RelAttachedTo}}
		if o := attrs.VpcOptions; o != nil {
			region := sv(r.Region)
			edges = append(edges, openSearchEdge{store.ResourceID("aws", acct.ID, ec2ARNIn(acct.partition(), region, acct.ID, "vpc", sv(o.VPCId))), vpcs, store.RelAttachedTo})
			for _, id := range o.SubnetIDs {
				edges = append(edges, openSearchEdge{store.ResourceID("aws", acct.ID, ec2ARNIn(acct.partition(), region, acct.ID, "subnet", id)), subnets, store.RelUses})
			}
			for _, id := range o.SecurityGroupIDs {
				edges = append(edges, openSearchEdge{store.ResourceID("aws", acct.ID, ec2ARNIn(acct.partition(), region, acct.ID, "security-group", id)), sgs, store.RelUses})
			}
		}
		for _, e := range edges {
			if !e.set[e.targetID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, e.targetID, e.kind, "directed", nil); err != nil {
				return fmt.Errorf("upsert opensearch vpc-endpoint edge: %w", err)
			}
		}
	}
	return nil
}

// resolveOpenSearchDataSourceAttachments wires each data source attachment to
// the domain it attaches (DataSourceArn). An attached serverless collection is
// not linked.
func resolveOpenSearchDataSourceAttachments(acct *account, st *store.Store) error {
	rows, err := listAccountRows(acct, st, TypeOpenSearchDataSourceAttachment)
	if err != nil || len(rows) == 0 {
		return err
	}
	domains, err := scannedIDSet(acct, st, TypeOpenSearchDomain)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			DataSourceArn *string `json:"DataSourceArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			return fmt.Errorf("decode opensearch data-source-attachment %s: %w", r.NativeID, err)
		}
		domainID := store.ResourceID("aws", acct.ID, sv(attrs.DataSourceArn))
		if !domains[domainID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, domainID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert opensearch data-source-attachment→domain: %w", err)
		}
	}
	return nil
}
