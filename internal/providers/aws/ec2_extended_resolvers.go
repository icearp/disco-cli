package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveEC2VolumeKMS,
		EdgeDecl{TypeEC2Volume, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveEC2ImageKMS,
		EdgeDecl{TypeEC2Image, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveEC2VPNGatewayVPC,
		EdgeDecl{TypeEC2VPNGateway, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(
		resolveEC2NetworkInterfacePermissionENI,
		EdgeDecl{TypeEC2NetworkInterfacePermission, TypeEC2NetworkInterface, store.RelAttachedTo},
	)
	registerResolver(
		resolveEC2TrafficMirrorTargetRefs,
		EdgeDecl{TypeEC2TrafficMirrorTarget, TypeEC2NetworkInterface, store.RelAttachedTo},
		EdgeDecl{TypeEC2TrafficMirrorTarget, TypeELBv2LoadBalancer, store.RelAttachedTo},
	)
	registerResolver(
		resolveEC2TrafficMirrorFilterRuleParent,
		EdgeDecl{TypeEC2TrafficMirrorFilterRule, TypeEC2TrafficMirrorFilter, store.RelAttachedTo},
	)
	registerResolver(
		resolveEC2VerifiedAccessInstanceTrustProvider,
		EdgeDecl{TypeEC2VerifiedAccessInstance, TypeEC2VerifiedAccessTrustProvider, store.RelUses},
	)
	registerResolver(
		resolveEC2ClientVPNAuthorizationRuleEndpoint,
		EdgeDecl{TypeEC2ClientVPNAuthorizationRule, TypeEC2ClientVPNEndpoint, store.RelAttachedTo},
	)
	registerResolver(
		resolveEC2ClientVPNRouteRefs,
		EdgeDecl{TypeEC2ClientVPNRoute, TypeEC2ClientVPNEndpoint, store.RelAttachedTo},
		EdgeDecl{TypeEC2ClientVPNRoute, TypeEC2Subnet, store.RelAttachedTo},
	)
	registerResolver(
		resolveEC2NetworkInsightsPathRefs,
		EdgeDecl{TypeEC2NetworkInsightsPath, TypeEC2NetworkInterface, store.RelUses},
		EdgeDecl{TypeEC2NetworkInsightsPath, TypeEC2Instance, store.RelUses},
	)
	registerResolver(
		resolveEC2CapacityReservationPlacementGroup,
		EdgeDecl{TypeEC2CapacityReservation, TypeEC2PlacementGroup, store.RelAttachedTo},
	)
	registerResolver(
		resolveEC2TGPeeringAttachmentParents,
		EdgeDecl{TypeEC2TransitGatewayPeeringAttachment, TypeEC2TransitGateway, store.RelAttachedTo},
	)
	registerResolver(
		resolveEC2SpotFleetIAM,
		EdgeDecl{TypeEC2SpotFleet, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		resolveEC2FleetLaunchTemplate,
		EdgeDecl{TypeEC2Fleet, TypeEC2LaunchTemplate, store.RelUses},
	)
	registerResolver(
		resolveEC2TGWMeteringPolicyRefs,
		EdgeDecl{TypeEC2TransitGatewayMeteringPolicy, TypeEC2TransitGateway, store.RelAttachedTo},
		EdgeDecl{TypeEC2TransitGatewayMeteringPolicy, TypeEC2TransitGatewayAttachment, store.RelAttachedTo},
	)
	registerResolver(
		resolveEC2VPCEndpointConnectionNotificationRefs,
		EdgeDecl{TypeEC2VPCEndpointConnectionNotification, TypeEC2VPCEndpoint, store.RelAttachedTo},
		EdgeDecl{TypeEC2VPCEndpointConnectionNotification, TypeEC2VPCEndpointService, store.RelAttachedTo},
		EdgeDecl{TypeEC2VPCEndpointConnectionNotification, TypeSNSTopic, store.RelUses},
	)
	registerResolver(
		resolveEC2RouteServerSNS,
		EdgeDecl{TypeEC2RouteServer, TypeSNSTopic, store.RelUses},
	)
	registerResolver(
		resolveEC2CapacityReservationFleetMembers,
		EdgeDecl{TypeEC2CapacityReservationFleet, TypeEC2CapacityReservation, store.RelContains},
	)
}

// resolveEC2VolumeKMS links each EBS volume to the KMS key encrypting it.
func resolveEC2VolumeKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2Volume},
		Limit: util.AllResources,
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
			KmsKeyID *string `json:"KmsKeyId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		ref := sv(attrs.KmsKeyID)
		if ref == "" {
			continue
		}
		if id, ok := kmsIdx.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
			if err := st.UpsertRelationship(r.ID, id, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert ec2-volume→kms: %w", err)
			}
		}
	}
	return nil
}

// resolveEC2ImageKMS links each AMI to KMS keys via per-block-device EBS
// encryption-key refs (BlockDeviceMappings[].Ebs.KmsKeyID).
func resolveEC2ImageKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2Image},
		Limit: util.AllResources,
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
			BlockDeviceMappings []struct {
				Ebs *struct {
					KmsKeyID *string `json:"KmsKeyId"`
				} `json:"Ebs"`
			} `json:"BlockDeviceMappings"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, bdm := range attrs.BlockDeviceMappings {
			if bdm.Ebs == nil {
				continue
			}
			ref := sv(bdm.Ebs.KmsKeyID)
			if ref == "" {
				continue
			}
			id, ok := kmsIdx.resolveKMSKeyID(ref, sv(r.Region), acct.ID)
			if !ok || seen[id] {
				continue
			}
			seen[id] = true
			if err := st.UpsertRelationship(r.ID, id, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert ec2-image→kms: %w", err)
			}
		}
	}
	return nil
}

// resolveEC2VPNGatewayVPC walks each VGW's VpcAttachments[] and emits
// attached-to → vpc.
func resolveEC2VPNGatewayVPC(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2VPNGateway},
		Limit: util.AllResources,
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
		var attrs struct {
			VpcAttachments []struct {
				VpcID *string `json:"VpcId"`
			} `json:"VpcAttachments"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		seen := map[string]bool{}
		for _, va := range attrs.VpcAttachments {
			id := sv(va.VpcID)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", id))
			if !vpcSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ec2-vpn-gateway→vpc: %w", err)
			}
		}
	}
	return nil
}

// resolveEC2NetworkInterfacePermissionENI links an ENI permission row to its
// parent network-interface (NetworkInterfaceID).
func resolveEC2NetworkInterfacePermissionENI(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2NetworkInterfacePermission},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	eniSet, err := scannedIDSet(acct, st, TypeEC2NetworkInterface)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			NetworkInterfaceID *string `json:"NetworkInterfaceId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		eni := sv(attrs.NetworkInterfaceID)
		if eni == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeEC2NetworkInterface, ec2ARN(sv(r.Region), acct.ID, "network-interface", eni))
		if !eniSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert ec2-eni-permission→eni: %w", err)
		}
	}
	return nil
}

// resolveEC2TrafficMirrorTargetRefs links each target to its underlying eni
// or NLB. Only one of the two fields is set per target.
func resolveEC2TrafficMirrorTargetRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2TrafficMirrorTarget},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	eniSet, err := scannedIDSet(acct, st, TypeEC2NetworkInterface)
	if err != nil {
		return err
	}
	nlbSet, err := scannedIDSet(acct, st, TypeELBv2LoadBalancer)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			NetworkInterfaceID     *string `json:"NetworkInterfaceId"`
			NetworkLoadBalancerArn *string `json:"NetworkLoadBalancerArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if eni := sv(attrs.NetworkInterfaceID); eni != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2NetworkInterface, ec2ARN(region, acct.ID, "network-interface", eni))
			if eniSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ec2-tm-target→eni: %w", err)
				}
			}
		}
		if arn := sv(attrs.NetworkLoadBalancerArn); arn != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeELBv2LoadBalancer, arn)
			if nlbSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ec2-tm-target→nlb: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveEC2TrafficMirrorFilterRuleParent links each rule to its filter via
// TrafficMirrorFilterID.
func resolveEC2TrafficMirrorFilterRuleParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2TrafficMirrorFilterRule},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	filterSet, err := scannedIDSet(acct, st, TypeEC2TrafficMirrorFilter)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			TrafficMirrorFilterID *string `json:"TrafficMirrorFilterId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		fid := sv(attrs.TrafficMirrorFilterID)
		if fid == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeEC2TrafficMirrorFilter, ec2ARN(sv(r.Region), acct.ID, "traffic-mirror-filter", fid))
		if !filterSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert ec2-tm-filter-rule→filter: %w", err)
		}
	}
	return nil
}

// resolveEC2VerifiedAccessInstanceTrustProvider walks each VAI's
// VerifiedAccessTrustProviders[] list and emits uses → trust-provider.
func resolveEC2VerifiedAccessInstanceTrustProvider(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2VerifiedAccessInstance},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	tpSet, err := scannedIDSet(acct, st, TypeEC2VerifiedAccessTrustProvider)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			VerifiedAccessTrustProviders []struct {
				VerifiedAccessTrustProviderID *string `json:"VerifiedAccessTrustProviderId"`
			} `json:"VerifiedAccessTrustProviders"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		for _, tp2 := range attrs.VerifiedAccessTrustProviders {
			id := sv(tp2.VerifiedAccessTrustProviderID)
			if id == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2VerifiedAccessTrustProvider, ec2ARN(region, acct.ID, "verified-access-trust-provider", id))
			if !tpSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert ec2-vai→vatp: %w", err)
			}
		}
	}
	return nil
}

// clientVPNEndpointFromChildARN extracts the parent endpoint id from a
// synthetic child ARN of shape
// `arn:aws:ec2:{r}:{a}:client-vpn-{auth-rule|route}/{endpointId}/...`. The
// scanner concatenates with `/` separators so the endpoint id is the first
// segment after the resource kind.
func clientVPNEndpointFromChildARN(arn string) string {
	const slash = "/"
	i := strings.Index(arn, ":client-vpn-")
	if i < 0 {
		return ""
	}
	rest := arn[i+1:]
	j := strings.Index(rest, slash)
	if j < 0 {
		return ""
	}
	tail := rest[j+1:]
	end := strings.Index(tail, slash)
	if end < 0 {
		return tail
	}
	return tail[:end]
}

// resolveEC2ClientVPNAuthorizationRuleEndpoint links each auth rule to its
// parent client-vpn-endpoint by parsing the synthetic child NativeID.
func resolveEC2ClientVPNAuthorizationRuleEndpoint(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2ClientVPNAuthorizationRule},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	epSet, err := scannedIDSet(acct, st, TypeEC2ClientVPNEndpoint)
	if err != nil {
		return err
	}
	for _, r := range rows {
		epID := clientVPNEndpointFromChildARN(r.NativeID)
		if epID == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeEC2ClientVPNEndpoint, ec2ARN(sv(r.Region), acct.ID, "client-vpn-endpoint", epID))
		if !epSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert ec2-cvpn-auth-rule→endpoint: %w", err)
		}
	}
	return nil
}

// resolveEC2ClientVPNRouteRefs links each route to its endpoint (parsed from
// NativeID) and to the target subnet (TargetSubnet attr).
func resolveEC2ClientVPNRouteRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2ClientVPNRoute},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	epSet, err := scannedIDSet(acct, st, TypeEC2ClientVPNEndpoint)
	if err != nil {
		return err
	}
	subnetSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	for _, r := range rows {
		region := sv(r.Region)
		if epID := clientVPNEndpointFromChildARN(r.NativeID); epID != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2ClientVPNEndpoint, ec2ARN(region, acct.ID, "client-vpn-endpoint", epID))
			if epSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ec2-cvpn-route→endpoint: %w", err)
				}
			}
		}
		var attrs struct {
			TargetSubnet *string `json:"TargetSubnet"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if subID := sv(attrs.TargetSubnet); subID != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", subID))
			if subnetSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ec2-cvpn-route→subnet: %w", err)
				}
			}
		}
	}
	return nil
}

// networkInsightsTargetType classifies a NetworkInsightsPath Source/Destination
// field by ID prefix. Only eni- and i- shapes are dispatched; ARNs of other
// resource types (TGW, VPC endpoint, internet-gateway, etc.) are skipped here
// — a follow-up pass can add them once their NativeID synthesis is verified.
func networkInsightsTargetType(s string) (string, string) {
	switch {
	case strings.HasPrefix(s, "eni-"):
		return TypeEC2NetworkInterface, "network-interface"
	case strings.HasPrefix(s, "i-"):
		return TypeEC2Instance, "instance"
	}
	return "", ""
}

// resolveEC2NetworkInsightsPathRefs links each path's Source + Destination to
// the underlying eni or instance row.
func resolveEC2NetworkInsightsPathRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2NetworkInsightsPath},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	eniSet, err := scannedIDSet(acct, st, TypeEC2NetworkInterface)
	if err != nil {
		return err
	}
	instSet, err := scannedIDSet(acct, st, TypeEC2Instance)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Source      *string `json:"Source"`
			Destination *string `json:"Destination"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		for _, ref := range []string{sv(attrs.Source), sv(attrs.Destination)} {
			tgtType, kind := networkInsightsTargetType(ref)
			if tgtType == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, tgtType, ec2ARN(region, acct.ID, kind, ref))
			set := eniSet
			if tgtType == TypeEC2Instance {
				set = instSet
			}
			if !set[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert ec2-nip→%s: %w", tgtType, err)
			}
		}
	}
	return nil
}

// resolveEC2CapacityReservationPlacementGroup links a reservation to the
// placement group its instances land in (PlacementGroupArn).
func resolveEC2CapacityReservationPlacementGroup(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2CapacityReservation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	pgSet, err := scannedIDSet(acct, st, TypeEC2PlacementGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			PlacementGroupArn *string `json:"PlacementGroupArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		arn := sv(attrs.PlacementGroupArn)
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeEC2PlacementGroup, arn)
		if !pgSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert ec2-cr→pg: %w", err)
		}
	}
	return nil
}

// resolveEC2TGPeeringAttachmentParents emits attached-to → transit-gateway
// for both the requester and accepter (when same-account; cross-account
// peerings FK-safe-skip).
func resolveEC2TGPeeringAttachmentParents(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2TransitGatewayPeeringAttachment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	tgSet, err := scannedIDSet(acct, st, TypeEC2TransitGateway)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			RequesterTgwInfo *struct {
				TransitGatewayID *string `json:"TransitGatewayId"`
				Region           *string `json:"Region"`
			} `json:"RequesterTgwInfo"`
			AccepterTgwInfo *struct {
				TransitGatewayID *string `json:"TransitGatewayId"`
				Region           *string `json:"Region"`
			} `json:"AccepterTgwInfo"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		seen := map[string]bool{}
		emit := func(tgID, tgRegion string) error {
			if tgID == "" || tgRegion == "" || seen[tgID] {
				return nil
			}
			seen[tgID] = true
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2TransitGateway, ec2ARN(tgRegion, acct.ID, "transit-gateway", tgID))
			if !tgSet[tgtID] {
				return nil
			}
			return st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil)
		}
		if attrs.RequesterTgwInfo != nil {
			if err := emit(sv(attrs.RequesterTgwInfo.TransitGatewayID), sv(attrs.RequesterTgwInfo.Region)); err != nil {
				return fmt.Errorf("upsert ec2-tg-peering→tg (req): %w", err)
			}
		}
		if attrs.AccepterTgwInfo != nil {
			if err := emit(sv(attrs.AccepterTgwInfo.TransitGatewayID), sv(attrs.AccepterTgwInfo.Region)); err != nil {
				return fmt.Errorf("upsert ec2-tg-peering→tg (acc): %w", err)
			}
		}
	}
	return nil
}

// resolveEC2SpotFleetIAM links a spot-fleet request to its fleet IAM role
// (SpotFleetRequestConfig.IamFleetRole).
func resolveEC2SpotFleetIAM(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2SpotFleet},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			SpotFleetRequestConfig *struct {
				IamFleetRole *string `json:"IamFleetRole"`
			} `json:"SpotFleetRequestConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.SpotFleetRequestConfig == nil {
			continue
		}
		arn := sv(attrs.SpotFleetRequestConfig.IamFleetRole)
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, arn)
		if !roleSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
			return fmt.Errorf("upsert ec2-spot-fleet→iam: %w", err)
		}
	}
	return nil
}

// resolveEC2FleetLaunchTemplate walks each EC2 fleet's LaunchTemplateConfigs[]
// and emits uses → launch-template.
func resolveEC2FleetLaunchTemplate(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2Fleet},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	ltSet, err := scannedIDSet(acct, st, TypeEC2LaunchTemplate)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			LaunchTemplateConfigs []struct {
				LaunchTemplateSpecification *struct {
					LaunchTemplateID *string `json:"LaunchTemplateId"`
				} `json:"LaunchTemplateSpecification"`
			} `json:"LaunchTemplateConfigs"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		seen := map[string]bool{}
		for _, ltc := range attrs.LaunchTemplateConfigs {
			if ltc.LaunchTemplateSpecification == nil {
				continue
			}
			id := sv(ltc.LaunchTemplateSpecification.LaunchTemplateID)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2LaunchTemplate, ec2ARN(region, acct.ID, "launch-template", id))
			if !ltSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert ec2-fleet→launch-template: %w", err)
			}
		}
	}
	return nil
}

// resolveEC2TGWMeteringPolicyRefs links each TGW metering policy to its parent
// transit gateway and to the middlebox attachments the policy meters.
func resolveEC2TGWMeteringPolicyRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2TransitGatewayMeteringPolicy},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	tgSet, err := scannedIDSet(acct, st, TypeEC2TransitGateway)
	if err != nil {
		return err
	}
	attSet, err := scannedIDSet(acct, st, TypeEC2TransitGatewayAttachment)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			TransitGatewayID       *string  `json:"TransitGatewayId"`
			MiddleboxAttachmentIDs []string `json:"MiddleboxAttachmentIds"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if id := sv(attrs.TransitGatewayID); id != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2TransitGateway, ec2ARN(region, acct.ID, "transit-gateway", id))
			if tgSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert tgw-metering-policy→tgw: %w", err)
				}
			}
		}
		for _, aid := range attrs.MiddleboxAttachmentIDs {
			if aid == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2TransitGatewayAttachment, ec2ARN(region, acct.ID, "transit-gateway-attachment", aid))
			if !attSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert tgw-metering-policy→tgw-attachment: %w", err)
			}
		}
	}
	return nil
}

// resolveEC2VPCEndpointConnectionNotificationRefs wires each connection
// notification to the endpoint or service it watches plus the SNS topic that
// receives the events.
func resolveEC2VPCEndpointConnectionNotificationRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2VPCEndpointConnectionNotification},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	vpceSet, err := scannedIDSet(acct, st, TypeEC2VPCEndpoint)
	if err != nil {
		return err
	}
	svcSet, err := scannedIDSet(acct, st, TypeEC2VPCEndpointService)
	if err != nil {
		return err
	}
	snsSet, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			VpcEndpointID             *string `json:"VpcEndpointId"`
			ServiceID                 *string `json:"ServiceId"`
			ConnectionNotificationArn *string `json:"ConnectionNotificationArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if id := sv(attrs.VpcEndpointID); id != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2VPCEndpoint, ec2ARN(region, acct.ID, "vpc-endpoint", id))
			if vpceSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert vpce-conn-notif→vpc-endpoint: %w", err)
				}
			}
		}
		if id := sv(attrs.ServiceID); id != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2VPCEndpointService, ec2ARN(region, acct.ID, "vpc-endpoint-service", id))
			if svcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert vpce-conn-notif→vpce-service: %w", err)
				}
			}
		}
		if topicARN := sv(attrs.ConnectionNotificationArn); topicARN != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeSNSTopic, topicARN)
			if snsSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert vpce-conn-notif→sns: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveEC2RouteServerSNS links each route server to the SNS topic that
// receives BGP status notifications.
func resolveEC2RouteServerSNS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2RouteServer},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	snsSet, err := scannedIDSet(acct, st, TypeSNSTopic)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			SnsTopicArn *string `json:"SnsTopicArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		topicARN := sv(attrs.SnsTopicArn)
		if topicARN == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeSNSTopic, topicARN)
		if !snsSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert route-server→sns: %w", err)
		}
	}
	return nil
}

// resolveEC2CapacityReservationFleetMembers wires each capacity reservation
// fleet to the individual capacity reservations it manages.
func resolveEC2CapacityReservationFleetMembers(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEC2CapacityReservationFleet},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	crSet, err := scannedIDSet(acct, st, TypeEC2CapacityReservation)
	if err != nil {
		return err
	}
	var pairs [][2]string
	for _, r := range rows {
		var attrs struct {
			InstanceTypeSpecifications []struct {
				CapacityReservationID *string `json:"CapacityReservationId"`
			} `json:"InstanceTypeSpecifications"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		seen := map[string]bool{}
		for _, spec := range attrs.InstanceTypeSpecifications {
			id := sv(spec.CapacityReservationID)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			childID := store.ResourceID("aws", acct.ID, TypeEC2CapacityReservation, ec2ARN(region, acct.ID, "capacity-reservation", id))
			if !crSet[childID] {
				continue
			}
			pairs = append(pairs, [2]string{childID, r.ID})
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	return st.RecordHierarchyBatch(pairs)
}
