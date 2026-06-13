package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// VPC Lattice resolvers wire the cross-resource edges that the per-service
// scanners cannot derive from a single SDK call. Each resolver reads attrs
// JSON (PascalCase, raw SDK marshal) plus, where AWS encodes a parent in the
// child's ARN path (Listener under Service, Rule under Listener), parses
// the ARN directly. All edges are FK-safe via scannedIDSet — refs to
// unscanned targets (e.g. cross-account VPC, foreign service) silently skip.
func init() {
	registerResolver(
		resolveVPCLatticeSNVA,
		EdgeDecl{TypeVpcLatticeServiceNetworkVpcAssociation, TypeVpcLatticeServiceNetwork, store.RelAttachedTo},
		EdgeDecl{TypeVpcLatticeServiceNetworkVpcAssociation, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(
		resolveVPCLatticeSNSA,
		EdgeDecl{TypeVpcLatticeServiceNetworkServiceAssociation, TypeVpcLatticeServiceNetwork, store.RelAttachedTo},
		EdgeDecl{TypeVpcLatticeServiceNetworkServiceAssociation, TypeVpcLatticeService, store.RelAttachedTo},
	)
	registerResolver(
		resolveVPCLatticeSNRA,
		EdgeDecl{TypeVpcLatticeServiceNetworkResourceAssociation, TypeVpcLatticeServiceNetwork, store.RelAttachedTo},
		EdgeDecl{TypeVpcLatticeServiceNetworkResourceAssociation, TypeVpcLatticeResourceConfiguration, store.RelAttachedTo},
	)
	registerResolver(
		resolveVPCLatticeTargetGroup,
		EdgeDecl{TypeVpcLatticeTargetGroup, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeVpcLatticeTargetGroup, TypeVpcLatticeService, store.RelAttachedTo},
	)
	registerResolver(
		resolveVPCLatticeListenerService,
		EdgeDecl{TypeVpcLatticeListener, TypeVpcLatticeService, store.RelAttachedTo},
	)
	registerResolver(
		resolveVPCLatticeRuleListener,
		EdgeDecl{TypeVpcLatticeRule, TypeVpcLatticeListener, store.RelAttachedTo},
	)
	registerResolver(
		resolveVPCLatticeAuthPolicyParent,
		EdgeDecl{TypeVpcLatticeAuthPolicy, TypeVpcLatticeService, store.RelAttachedTo},
		EdgeDecl{TypeVpcLatticeAuthPolicy, TypeVpcLatticeServiceNetwork, store.RelAttachedTo},
	)
	registerResolver(
		resolveVPCLatticeResourcePolicyParent,
		EdgeDecl{TypeVpcLatticeResourcePolicy, TypeVpcLatticeService, store.RelAttachedTo},
		EdgeDecl{TypeVpcLatticeResourcePolicy, TypeVpcLatticeServiceNetwork, store.RelAttachedTo},
	)
	registerResolver(
		resolveVPCLatticeResourceGateway,
		EdgeDecl{TypeVpcLatticeResourceGateway, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeVpcLatticeResourceGateway, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeVpcLatticeResourceGateway, TypeEC2SecurityGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveVPCLatticeResourceConfigurationGateway,
		EdgeDecl{TypeVpcLatticeResourceConfiguration, TypeVpcLatticeResourceGateway, store.RelAttachedTo},
	)
}

// resolveVPCLatticeSNVA links each ServiceNetworkVpcAssociation to its
// service-network (by ARN) and VPC (by bare VpcId, synthesized into an
// ec2:vpc ARN for canonical lookup).
func resolveVPCLatticeSNVA(acct *account, st *store.Store) error {
	rs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeVpcLatticeServiceNetworkVpcAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	netSet, err := scannedIDSet(acct, st, TypeVpcLatticeServiceNetwork)
	if err != nil {
		return err
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var attrs struct {
			ServiceNetworkArn *string `json:"ServiceNetworkArn"`
			VpcID             *string `json:"VpcId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.ServiceNetworkArn != nil && *attrs.ServiceNetworkArn != "" {
			netID := store.ResourceID("aws", acct.ID, TypeVpcLatticeServiceNetwork, *attrs.ServiceNetworkArn)
			if netSet[netID] {
				if err := st.UpsertRelationship(r.ID, netID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert snva→service-network: %w", err)
				}
			}
		}
		if attrs.VpcID != nil && *attrs.VpcID != "" {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC,
				ec2ARN(region, acct.ID, "vpc", *attrs.VpcID))
			if vpcSet[vpcID] {
				if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert snva→vpc: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveVPCLatticeSNSA links each ServiceNetworkServiceAssociation to its
// service-network and service (both by ARN — the SDK summary carries them).
func resolveVPCLatticeSNSA(acct *account, st *store.Store) error {
	rs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeVpcLatticeServiceNetworkServiceAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	netSet, err := scannedIDSet(acct, st, TypeVpcLatticeServiceNetwork)
	if err != nil {
		return err
	}
	svcSet, err := scannedIDSet(acct, st, TypeVpcLatticeService)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var attrs struct {
			ServiceNetworkArn *string `json:"ServiceNetworkArn"`
			ServiceArn        *string `json:"ServiceArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ServiceNetworkArn != nil && *attrs.ServiceNetworkArn != "" {
			netID := store.ResourceID("aws", acct.ID, TypeVpcLatticeServiceNetwork, *attrs.ServiceNetworkArn)
			if netSet[netID] {
				if err := st.UpsertRelationship(r.ID, netID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert snsa→service-network: %w", err)
				}
			}
		}
		if attrs.ServiceArn != nil && *attrs.ServiceArn != "" {
			svcID := store.ResourceID("aws", acct.ID, TypeVpcLatticeService, *attrs.ServiceArn)
			if svcSet[svcID] {
				if err := st.UpsertRelationship(r.ID, svcID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert snsa→service: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveVPCLatticeSNRA links each ServiceNetworkResourceAssociation to its
// service-network and resource-configuration.
func resolveVPCLatticeSNRA(acct *account, st *store.Store) error {
	rs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeVpcLatticeServiceNetworkResourceAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	netSet, err := scannedIDSet(acct, st, TypeVpcLatticeServiceNetwork)
	if err != nil {
		return err
	}
	rcSet, err := scannedIDSet(acct, st, TypeVpcLatticeResourceConfiguration)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var attrs struct {
			ServiceNetworkArn        *string `json:"ServiceNetworkArn"`
			ResourceConfigurationArn *string `json:"ResourceConfigurationArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ServiceNetworkArn != nil && *attrs.ServiceNetworkArn != "" {
			netID := store.ResourceID("aws", acct.ID, TypeVpcLatticeServiceNetwork, *attrs.ServiceNetworkArn)
			if netSet[netID] {
				if err := st.UpsertRelationship(r.ID, netID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert snra→service-network: %w", err)
				}
			}
		}
		if attrs.ResourceConfigurationArn != nil && *attrs.ResourceConfigurationArn != "" {
			rcID := store.ResourceID("aws", acct.ID, TypeVpcLatticeResourceConfiguration, *attrs.ResourceConfigurationArn)
			if rcSet[rcID] {
				if err := st.UpsertRelationship(r.ID, rcID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert snra→resource-configuration: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveVPCLatticeTargetGroup links each TargetGroup to its VPC (via bare
// VpcIdentifier) and to any Services that reference it (ServiceArns[]).
// LAMBDA / ALB-typed target groups have no VpcIdentifier; the empty-check
// handles that case naturally.
func resolveVPCLatticeTargetGroup(acct *account, st *store.Store) error {
	rs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeVpcLatticeTargetGroup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	svcSet, err := scannedIDSet(acct, st, TypeVpcLatticeService)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var attrs struct {
			VpcIdentifier *string  `json:"VpcIdentifier"`
			ServiceArns   []string `json:"ServiceArns"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.VpcIdentifier != nil && *attrs.VpcIdentifier != "" {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC,
				ec2ARN(region, acct.ID, "vpc", *attrs.VpcIdentifier))
			if vpcSet[vpcID] {
				if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert target-group→vpc: %w", err)
				}
			}
		}
		for _, sa := range attrs.ServiceArns {
			if sa == "" {
				continue
			}
			svcID := store.ResourceID("aws", acct.ID, TypeVpcLatticeService, sa)
			if !svcSet[svcID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, svcID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert target-group→service: %w", err)
			}
		}
	}
	return nil
}

// resolveVPCLatticeListenerService derives the parent service ARN from the
// listener's own ARN. The Listener ARN shape is
// `arn:aws:vpc-lattice:r:a:service/{svc-id}/listener/{lst-id}` — strip the
// `/listener/...` suffix to recover the service ARN.
func resolveVPCLatticeListenerService(acct *account, st *store.Store) error {
	rs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeVpcLatticeListener},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	svcSet, err := scannedIDSet(acct, st, TypeVpcLatticeService)
	if err != nil {
		return err
	}
	for _, r := range rs {
		svcARN, ok := vlListenerParentService(r.NativeID)
		if !ok {
			continue
		}
		svcID := store.ResourceID("aws", acct.ID, TypeVpcLatticeService, svcARN)
		if !svcSet[svcID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, svcID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert listener→service: %w", err)
		}
	}
	return nil
}

// resolveVPCLatticeRuleListener derives the parent listener ARN from the
// rule's own ARN. Rule ARN shape:
// `arn:aws:vpc-lattice:r:a:service/{svc-id}/listener/{lst-id}/rule/{rule-id}`.
// Strip the `/rule/...` suffix to recover the listener ARN.
func resolveVPCLatticeRuleListener(acct *account, st *store.Store) error {
	rs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeVpcLatticeRule},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	lstSet, err := scannedIDSet(acct, st, TypeVpcLatticeListener)
	if err != nil {
		return err
	}
	for _, r := range rs {
		lstARN, ok := vlRuleParentListener(r.NativeID)
		if !ok {
			continue
		}
		lstID := store.ResourceID("aws", acct.ID, TypeVpcLatticeListener, lstARN)
		if !lstSet[lstID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, lstID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert rule→listener: %w", err)
		}
	}
	return nil
}

// resolveVPCLatticeAuthPolicyParent links each AuthPolicy synthetic resource
// to its parent service or service-network. Scanner-built NativeID is
// `{parentARN}/auth-policy`; strip the suffix and dispatch by the parent
// ARN's resource segment (`service/...` vs `servicenetwork/...`).
func resolveVPCLatticeAuthPolicyParent(acct *account, st *store.Store) error {
	return resolveVPCLatticePolicyParent(acct, st, TypeVpcLatticeAuthPolicy, "/auth-policy")
}

// resolveVPCLatticeResourcePolicyParent — same parent dispatch as auth-policy,
// for the `{parentARN}/resource-policy` synthetic ARN suffix.
func resolveVPCLatticeResourcePolicyParent(acct *account, st *store.Store) error {
	return resolveVPCLatticePolicyParent(acct, st, TypeVpcLatticeResourcePolicy, "/resource-policy")
}

// resolveVPCLatticePolicyParent factors the auth-policy / resource-policy
// parent walk: both synthesize NativeID as `{parentARN}/{suffix}` where the
// parent is either a service or a service-network. ARN resource segment
// distinguishes them — `:service/...` for service (lattice service ARN), or
// `:servicenetwork/...` for service-network.
func resolveVPCLatticePolicyParent(acct *account, st *store.Store, rtype, suffix string) error {
	rs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{rtype},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	svcSet, err := scannedIDSet(acct, st, TypeVpcLatticeService)
	if err != nil {
		return err
	}
	netSet, err := scannedIDSet(acct, st, TypeVpcLatticeServiceNetwork)
	if err != nil {
		return err
	}
	for _, r := range rs {
		parentARN := strings.TrimSuffix(r.NativeID, suffix)
		if parentARN == r.NativeID {
			continue
		}
		// Dispatch on resource segment. Service ARNs contain `:service/`;
		// service-network ARNs contain `:servicenetwork/`.
		var ptype string
		switch {
		case strings.Contains(parentARN, ":service/"):
			ptype = TypeVpcLatticeService
		case strings.Contains(parentARN, ":servicenetwork/"):
			ptype = TypeVpcLatticeServiceNetwork
		default:
			continue
		}
		pID := store.ResourceID("aws", acct.ID, ptype, parentARN)
		var ok bool
		if ptype == TypeVpcLatticeService {
			ok = svcSet[pID]
		} else {
			ok = netSet[pID]
		}
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, pID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert %s→parent: %w", rtype, err)
		}
	}
	return nil
}

// resolveVPCLatticeResourceGateway links each ResourceGateway to its VPC,
// subnets, and security groups — all bare IDs in attrs that get synthesized
// into canonical EC2 ARNs for lookup.
func resolveVPCLatticeResourceGateway(acct *account, st *store.Store) error {
	rs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeVpcLatticeResourceGateway},
		Limit: util.AllResources,
	})
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
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var attrs struct {
			VpcIdentifier    *string  `json:"VpcIdentifier"`
			SubnetIDs        []string `json:"SubnetIds"`
			SecurityGroupIDs []string `json:"SecurityGroupIds"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if attrs.VpcIdentifier != nil && *attrs.VpcIdentifier != "" {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC,
				ec2ARN(region, acct.ID, "vpc", *attrs.VpcIdentifier))
			if vpcSet[vpcID] {
				if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert resource-gateway→vpc: %w", err)
				}
			}
		}
		for _, sn := range attrs.SubnetIDs {
			if sn == "" {
				continue
			}
			snID := store.ResourceID("aws", acct.ID, TypeEC2Subnet,
				ec2ARN(region, acct.ID, "subnet", sn))
			if !subnetSet[snID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, snID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert resource-gateway→subnet: %w", err)
			}
		}
		for _, sg := range attrs.SecurityGroupIDs {
			if sg == "" {
				continue
			}
			sgID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup,
				ec2ARN(region, acct.ID, "security-group", sg))
			if !sgSet[sgID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, sgID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert resource-gateway→security-group: %w", err)
			}
		}
	}
	return nil
}

// resolveVPCLatticeResourceConfigurationGateway links each ResourceConfiguration
// to the ResourceGateway that hosts it. Attrs carries `ResourceGatewayId` as
// a bare ID; the gateway's NativeID is the full ARN, so we must look up via
// scannedIDSet keyed by ARN — but we have only the bare ID here. Walk the
// gateway list once, build an `id → ARN` index, then resolve.
func resolveVPCLatticeResourceConfigurationGateway(acct *account, st *store.Store) error {
	rs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeVpcLatticeResourceConfiguration},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	gwIndex, err := vlResourceGatewayIDIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var attrs struct {
			ResourceGatewayID *string `json:"ResourceGatewayId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ResourceGatewayID == nil || *attrs.ResourceGatewayID == "" {
			continue
		}
		gwResourceID, ok := gwIndex[*attrs.ResourceGatewayID]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, gwResourceID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert resource-config→resource-gateway: %w", err)
		}
	}
	return nil
}

// vlResourceGatewayIDIndex maps each scanned ResourceGateway's bare ID
// (rgw-xxx) to its disco resource ID. Built by parsing the gateway ARN —
// shape `arn:aws:vpc-lattice:r:a:resourcegateway/{rgw-id}`.
func vlResourceGatewayIDIndex(acct *account, st *store.Store) (map[string]string, error) {
	rs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{TypeVpcLatticeResourceGateway},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rs))
	for _, r := range rs {
		// Pull the bare gateway id from the trailing path segment.
		i := strings.LastIndex(r.NativeID, "/")
		if i < 0 {
			continue
		}
		bareID := r.NativeID[i+1:]
		if bareID == "" {
			continue
		}
		idx[bareID] = r.ID
	}
	return idx, nil
}

// vlListenerParentService recovers the parent service ARN from a listener
// ARN by stripping the `/listener/{id}` suffix. Returns ("", false) if the
// shape doesn't match.
func vlListenerParentService(listenerARN string) (string, bool) {
	i := strings.Index(listenerARN, "/listener/")
	if i < 0 {
		return "", false
	}
	return listenerARN[:i], true
}

// vlRuleParentListener recovers the parent listener ARN from a rule ARN by
// stripping the `/rule/{id}` suffix. Returns ("", false) if the shape
// doesn't match.
func vlRuleParentListener(ruleARN string) (string, bool) {
	i := strings.Index(ruleARN, "/rule/")
	if i < 0 {
		return "", false
	}
	return ruleARN[:i], true
}

func init() {
	registerResolver(
		resolveVPCLatticeServiceCert,
		EdgeDecl{TypeVpcLatticeService, TypeACMCertificate, store.RelUses},
	)
}

// resolveVPCLatticeServiceCert wires each lattice service to its custom-
// domain ACM certificate (CertificateArn). GetService body shape.
func resolveVPCLatticeServiceCert(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeVpcLatticeService}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	acmSet, err := scannedIDSet(acct, st, TypeACMCertificate)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			CertificateArn *string `json:"CertificateArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		ca := sv(attrs.CertificateArn)
		if ca == "" {
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, TypeACMCertificate, ca)
		if !acmSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert vpclattice-service→acm: %w", err)
		}
	}
	return nil
}
