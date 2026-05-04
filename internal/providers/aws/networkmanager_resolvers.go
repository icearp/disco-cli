package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveNMSiteRefs,
		EdgeDecl{TypeNetworkManagerSite, TypeNetworkManagerGlobalNetwork, store.RelAttachedTo},
	)
	registerResolver(resolveNMDeviceRefs,
		EdgeDecl{TypeNetworkManagerDevice, TypeNetworkManagerGlobalNetwork, store.RelAttachedTo},
		EdgeDecl{TypeNetworkManagerDevice, TypeNetworkManagerSite, store.RelAttachedTo},
	)
	registerResolver(resolveNMLinkRefs,
		EdgeDecl{TypeNetworkManagerLink, TypeNetworkManagerGlobalNetwork, store.RelAttachedTo},
		EdgeDecl{TypeNetworkManagerLink, TypeNetworkManagerSite, store.RelAttachedTo},
	)
	registerResolver(resolveNMLinkAssociationRefs,
		EdgeDecl{TypeNetworkManagerLinkAssociation, TypeNetworkManagerDevice, store.RelAttachedTo},
		EdgeDecl{TypeNetworkManagerLinkAssociation, TypeNetworkManagerLink, store.RelAttachedTo},
	)
	registerResolver(resolveNMCoreNetworkRefs,
		EdgeDecl{TypeNetworkManagerCoreNetwork, TypeNetworkManagerGlobalNetwork, store.RelAttachedTo},
	)
	registerResolver(resolveNMTGWRegistrationRefs,
		EdgeDecl{TypeNetworkManagerTransitGatewayRegistration, TypeNetworkManagerGlobalNetwork, store.RelAttachedTo},
		EdgeDecl{TypeNetworkManagerTransitGatewayRegistration, TypeEC2TransitGateway, store.RelAttachedTo},
	)
	registerResolver(resolveNMCGWAssociationRefs,
		EdgeDecl{TypeNetworkManagerCustomerGatewayAssociation, TypeNetworkManagerGlobalNetwork, store.RelAttachedTo},
		EdgeDecl{TypeNetworkManagerCustomerGatewayAssociation, TypeEC2CustomerGateway, store.RelAttachedTo},
	)
	registerResolver(resolveNMVpcAttachmentRefs,
		EdgeDecl{TypeNetworkManagerVpcAttachment, TypeNetworkManagerCoreNetwork, store.RelAttachedTo},
		EdgeDecl{TypeNetworkManagerVpcAttachment, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeNetworkManagerVpcAttachment, TypeEC2Subnet, store.RelAttachedTo},
	)
	registerResolver(resolveNMConnectAttachmentRefs,
		EdgeDecl{TypeNetworkManagerConnectAttachment, TypeNetworkManagerCoreNetwork, store.RelAttachedTo},
	)
	registerResolver(resolveNMSiteToSiteVpnAttachmentRefs,
		EdgeDecl{TypeNetworkManagerSiteToSiteVpnAttachment, TypeNetworkManagerCoreNetwork, store.RelAttachedTo},
	)
	registerResolver(resolveNMDirectConnectGatewayAttachmentRefs,
		EdgeDecl{TypeNetworkManagerDirectConnectGatewayAttachment, TypeNetworkManagerCoreNetwork, store.RelAttachedTo},
	)
	registerResolver(resolveNMTGWRouteTableAttachmentRefs,
		EdgeDecl{TypeNetworkManagerTransitGatewayRouteTableAttachment, TypeNetworkManagerCoreNetwork, store.RelAttachedTo},
	)
	registerResolver(resolveNMTransitGatewayPeeringRefs,
		EdgeDecl{TypeNetworkManagerTransitGatewayPeering, TypeNetworkManagerCoreNetwork, store.RelAttachedTo},
		EdgeDecl{TypeNetworkManagerTransitGatewayPeering, TypeEC2TransitGateway, store.RelAttachedTo},
	)
	registerResolver(resolveNMConnectPeerRefs,
		EdgeDecl{TypeNetworkManagerConnectPeer, TypeNetworkManagerCoreNetwork, store.RelAttachedTo},
	)
	registerResolver(resolveNMCorePLAssocRefs,
		EdgeDecl{TypeNetworkManagerCoreNetworkPrefixListAssociation, TypeNetworkManagerCoreNetwork, store.RelAttachedTo},
		EdgeDecl{TypeNetworkManagerCoreNetworkPrefixListAssociation, TypeEC2PrefixList, store.RelAttachedTo},
	)
}

// resolveNMCorePLAssocRefs wires core-network-prefix-list-association →
// core-network (NativeID strip on `/prefix-list-association/`) and EC2 prefix-
// list (PrefixListArn).
func resolveNMCorePLAssocRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeNetworkManagerCoreNetworkPrefixListAssociation}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	cnSet, err := scannedIDSet(acct, st, TypeNetworkManagerCoreNetwork)
	if err != nil {
		return err
	}
	plSet, err := scannedIDSet(acct, st, TypeEC2PrefixList)
	if err != nil {
		return err
	}
	const seg = "/prefix-list-association/"
	for _, r := range rows {
		i := strings.Index(r.NativeID, seg)
		if i > 0 {
			parent := r.NativeID[:i]
			tgtID := store.ResourceID("aws", acct.ID, TypeNetworkManagerCoreNetwork, parent)
			if cnSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert nm pla→cn: %w", err)
				}
			}
		}
		var attrs struct {
			PrefixListArn *string `json:"PrefixListArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if pl := sv(attrs.PrefixListArn); pl != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2PrefixList, pl)
			if plSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert nm pla→pl: %w", err)
				}
			}
		}
	}
	return nil
}

// nmGlobalNetworkID rebuilds the canonical NetworkManager GlobalNetwork ARN
// from a raw GlobalNetworkId. NetworkManager is a global service — ARN region
// segment is empty.
func nmGlobalNetworkID(acctID, gnID string) string {
	return fmt.Sprintf("arn:aws:networkmanager::%s:global-network/%s", acctID, gnID)
}

// nmSiteID rebuilds the canonical NetworkManager Site ARN from a raw SiteId.
// Sites nest under their global network in the ARN path.
func nmSiteID(acctID, gnID, siteID string) string {
	return fmt.Sprintf("arn:aws:networkmanager::%s:site/%s/%s", acctID, gnID, siteID)
}

// nmDeviceID rebuilds the canonical NetworkManager Device ARN.
func nmDeviceID(acctID, gnID, devID string) string {
	return fmt.Sprintf("arn:aws:networkmanager::%s:device/%s/%s", acctID, gnID, devID)
}

// nmLinkID rebuilds the canonical NetworkManager Link ARN.
func nmLinkID(acctID, gnID, linkID string) string {
	return fmt.Sprintf("arn:aws:networkmanager::%s:link/%s/%s", acctID, gnID, linkID)
}

// nmCoreNetworkID rebuilds the canonical NetworkManager CoreNetwork ARN.
func nmCoreNetworkID(acctID, coreID string) string {
	return fmt.Sprintf("arn:aws:networkmanager::%s:core-network/%s", acctID, coreID)
}

// listNMResources lists all NetworkManager rows of a given disco type for the
// account. Tiny convenience wrapper to keep resolver bodies linear.
func listNMResources(acct *account, st *store.Store, rtype string) ([]store.Resource, error) {
	return st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID,
		Types: []string{rtype},
		Limit: util.AllResources,
	})
}

// resolveNMSiteRefs links each Site to its parent GlobalNetwork.
func resolveNMSiteRefs(acct *account, st *store.Store) error {
	rows, err := listNMResources(acct, st, TypeNetworkManagerSite)
	if err != nil {
		return err
	}
	gnSet, err := scannedIDSet(acct, st, TypeNetworkManagerGlobalNetwork)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			GlobalNetworkID *string `json:"GlobalNetworkId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.GlobalNetworkID == nil || *attrs.GlobalNetworkID == "" {
			continue
		}
		gnID := store.ResourceID("aws", acct.ID, TypeNetworkManagerGlobalNetwork,
			nmGlobalNetworkID(acct.ID, *attrs.GlobalNetworkID))
		if !gnSet[gnID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, gnID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert nm-site→global-network: %w", err)
		}
	}
	return nil
}

// resolveNMDeviceRefs links each Device to its GlobalNetwork and (optionally)
// its Site.
func resolveNMDeviceRefs(acct *account, st *store.Store) error {
	rows, err := listNMResources(acct, st, TypeNetworkManagerDevice)
	if err != nil {
		return err
	}
	gnSet, err := scannedIDSet(acct, st, TypeNetworkManagerGlobalNetwork)
	if err != nil {
		return err
	}
	siteSet, err := scannedIDSet(acct, st, TypeNetworkManagerSite)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			GlobalNetworkID *string `json:"GlobalNetworkId"`
			SiteID          *string `json:"SiteId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		gn := sv(attrs.GlobalNetworkID)
		if gn == "" {
			continue
		}
		gnID := store.ResourceID("aws", acct.ID, TypeNetworkManagerGlobalNetwork,
			nmGlobalNetworkID(acct.ID, gn))
		if gnSet[gnID] {
			if err := st.UpsertRelationship(r.ID, gnID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert nm-device→global-network: %w", err)
			}
		}
		if attrs.SiteID != nil && *attrs.SiteID != "" {
			siteID := store.ResourceID("aws", acct.ID, TypeNetworkManagerSite,
				nmSiteID(acct.ID, gn, *attrs.SiteID))
			if siteSet[siteID] {
				if err := st.UpsertRelationship(r.ID, siteID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert nm-device→site: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveNMLinkRefs links each Link to its GlobalNetwork and Site.
func resolveNMLinkRefs(acct *account, st *store.Store) error {
	rows, err := listNMResources(acct, st, TypeNetworkManagerLink)
	if err != nil {
		return err
	}
	gnSet, err := scannedIDSet(acct, st, TypeNetworkManagerGlobalNetwork)
	if err != nil {
		return err
	}
	siteSet, err := scannedIDSet(acct, st, TypeNetworkManagerSite)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			GlobalNetworkID *string `json:"GlobalNetworkId"`
			SiteID          *string `json:"SiteId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		gn := sv(attrs.GlobalNetworkID)
		if gn == "" {
			continue
		}
		gnID := store.ResourceID("aws", acct.ID, TypeNetworkManagerGlobalNetwork,
			nmGlobalNetworkID(acct.ID, gn))
		if gnSet[gnID] {
			if err := st.UpsertRelationship(r.ID, gnID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert nm-link→global-network: %w", err)
			}
		}
		if attrs.SiteID != nil && *attrs.SiteID != "" {
			siteID := store.ResourceID("aws", acct.ID, TypeNetworkManagerSite,
				nmSiteID(acct.ID, gn, *attrs.SiteID))
			if siteSet[siteID] {
				if err := st.UpsertRelationship(r.ID, siteID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert nm-link→site: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveNMLinkAssociationRefs links each LinkAssociation to its Device + Link.
// LinkAssociation has no native ARN; the scanner stores DeviceId + LinkId in
// the embedded SDK struct.
func resolveNMLinkAssociationRefs(acct *account, st *store.Store) error {
	rows, err := listNMResources(acct, st, TypeNetworkManagerLinkAssociation)
	if err != nil {
		return err
	}
	devSet, err := scannedIDSet(acct, st, TypeNetworkManagerDevice)
	if err != nil {
		return err
	}
	linkSet, err := scannedIDSet(acct, st, TypeNetworkManagerLink)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			GlobalNetworkID *string `json:"GlobalNetworkId"`
			DeviceID        *string `json:"DeviceId"`
			LinkID          *string `json:"LinkId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		gn := sv(attrs.GlobalNetworkID)
		if gn == "" {
			continue
		}
		if attrs.DeviceID != nil && *attrs.DeviceID != "" {
			devID := store.ResourceID("aws", acct.ID, TypeNetworkManagerDevice,
				nmDeviceID(acct.ID, gn, *attrs.DeviceID))
			if devSet[devID] {
				if err := st.UpsertRelationship(r.ID, devID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert nm-link-association→device: %w", err)
				}
			}
		}
		if attrs.LinkID != nil && *attrs.LinkID != "" {
			linkID := store.ResourceID("aws", acct.ID, TypeNetworkManagerLink,
				nmLinkID(acct.ID, gn, *attrs.LinkID))
			if linkSet[linkID] {
				if err := st.UpsertRelationship(r.ID, linkID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert nm-link-association→link: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveNMCoreNetworkRefs links each CoreNetwork to its parent GlobalNetwork.
func resolveNMCoreNetworkRefs(acct *account, st *store.Store) error {
	rows, err := listNMResources(acct, st, TypeNetworkManagerCoreNetwork)
	if err != nil {
		return err
	}
	gnSet, err := scannedIDSet(acct, st, TypeNetworkManagerGlobalNetwork)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			GlobalNetworkID *string `json:"GlobalNetworkId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.GlobalNetworkID == nil || *attrs.GlobalNetworkID == "" {
			continue
		}
		gnID := store.ResourceID("aws", acct.ID, TypeNetworkManagerGlobalNetwork,
			nmGlobalNetworkID(acct.ID, *attrs.GlobalNetworkID))
		if !gnSet[gnID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, gnID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert nm-core-network→global-network: %w", err)
		}
	}
	return nil
}

// resolveNMTGWRegistrationRefs links each TransitGatewayRegistration to its
// GlobalNetwork plus the cross-service EC2 TransitGateway it registers.
// TransitGatewayArn from the SDK is the canonical EC2 TGW ARN — used directly
// as NativeID.
func resolveNMTGWRegistrationRefs(acct *account, st *store.Store) error {
	rows, err := listNMResources(acct, st, TypeNetworkManagerTransitGatewayRegistration)
	if err != nil {
		return err
	}
	gnSet, err := scannedIDSet(acct, st, TypeNetworkManagerGlobalNetwork)
	if err != nil {
		return err
	}
	tgwSet, err := scannedIDSet(acct, st, TypeEC2TransitGateway)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			GlobalNetworkID   *string `json:"GlobalNetworkId"`
			TransitGatewayArn *string `json:"TransitGatewayArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.GlobalNetworkID != nil && *attrs.GlobalNetworkID != "" {
			gnID := store.ResourceID("aws", acct.ID, TypeNetworkManagerGlobalNetwork,
				nmGlobalNetworkID(acct.ID, *attrs.GlobalNetworkID))
			if gnSet[gnID] {
				if err := st.UpsertRelationship(r.ID, gnID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert nm-tgw-registration→global-network: %w", err)
				}
			}
		}
		if attrs.TransitGatewayArn != nil && *attrs.TransitGatewayArn != "" {
			tgwID := store.ResourceID("aws", acct.ID, TypeEC2TransitGateway, *attrs.TransitGatewayArn)
			if tgwSet[tgwID] {
				if err := st.UpsertRelationship(r.ID, tgwID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert nm-tgw-registration→tgw: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveNMCGWAssociationRefs links each CustomerGatewayAssociation to its
// GlobalNetwork plus the cross-service EC2 CustomerGateway. CustomerGatewayArn
// from the SDK is the canonical EC2 CGW ARN — used directly as NativeID.
func resolveNMCGWAssociationRefs(acct *account, st *store.Store) error {
	rows, err := listNMResources(acct, st, TypeNetworkManagerCustomerGatewayAssociation)
	if err != nil {
		return err
	}
	gnSet, err := scannedIDSet(acct, st, TypeNetworkManagerGlobalNetwork)
	if err != nil {
		return err
	}
	cgwSet, err := scannedIDSet(acct, st, TypeEC2CustomerGateway)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			GlobalNetworkID    *string `json:"GlobalNetworkId"`
			CustomerGatewayArn *string `json:"CustomerGatewayArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.GlobalNetworkID != nil && *attrs.GlobalNetworkID != "" {
			gnID := store.ResourceID("aws", acct.ID, TypeNetworkManagerGlobalNetwork,
				nmGlobalNetworkID(acct.ID, *attrs.GlobalNetworkID))
			if gnSet[gnID] {
				if err := st.UpsertRelationship(r.ID, gnID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert nm-cgw-association→global-network: %w", err)
				}
			}
		}
		if attrs.CustomerGatewayArn != nil && *attrs.CustomerGatewayArn != "" {
			cgwID := store.ResourceID("aws", acct.ID, TypeEC2CustomerGateway, *attrs.CustomerGatewayArn)
			if cgwSet[cgwID] {
				if err := st.UpsertRelationship(r.ID, cgwID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert nm-cgw-association→cgw: %w", err)
				}
			}
		}
	}
	return nil
}

// nmAttachmentCoreNetworkEdge emits a single attachment→core-network edge,
// honouring the FK-safe id-set check. Shared body for the five attachment
// resolvers — all wrap an `Attachment` SDK struct that carries
// `CoreNetworkId`.
//
// The scanner stores the bare `Attachment` SDK struct as AttributesJSON for
// the simple Vpc / SiteToSiteVpn / Connect / DirectConnectGateway /
// TransitGatewayRouteTable rows (each surfaces via ListAttachments rather
// than a sub-shape Describe), so the field is at the top level.
func nmAttachmentCoreNetworkEdge(acct *account, st *store.Store, r store.Resource, coreSet map[string]bool, label string) error {
	var attrs struct {
		CoreNetworkID  *string `json:"CoreNetworkId"`
		CoreNetworkArn *string `json:"CoreNetworkArn"`
	}
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		return nil //nolint:nilerr // skip un-parseable attrs row, FK-safe.
	}
	cn := sv(attrs.CoreNetworkID)
	if cn == "" {
		return nil
	}
	cnID := store.ResourceID("aws", acct.ID, TypeNetworkManagerCoreNetwork,
		nmCoreNetworkID(acct.ID, cn))
	if !coreSet[cnID] {
		return nil
	}
	if err := st.UpsertRelationship(r.ID, cnID, store.RelAttachedTo, "directed", nil); err != nil {
		return fmt.Errorf("upsert %s→core-network: %w", label, err)
	}
	return nil
}

// resolveNMVpcAttachmentRefs links each VpcAttachment to its CoreNetwork plus
// the cross-service EC2 VPC (via Attachment.ResourceArn) and Subnets (via
// SubnetArns).
func resolveNMVpcAttachmentRefs(acct *account, st *store.Store) error {
	rows, err := listNMResources(acct, st, TypeNetworkManagerVpcAttachment)
	if err != nil {
		return err
	}
	coreSet, err := scannedIDSet(acct, st, TypeNetworkManagerCoreNetwork)
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
		// VpcAttachment rows are stored from ListAttachments which yields
		// `Attachment`. The CoreNetworkId / ResourceArn fields live at the
		// top level of that struct.
		if err := nmAttachmentCoreNetworkEdge(acct, st, r, coreSet, "nm-vpc-attachment"); err != nil {
			return err
		}
		var attrs struct {
			ResourceArn *string  `json:"ResourceArn"`
			SubnetArns  []string `json:"SubnetArns"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ResourceArn != nil && *attrs.ResourceArn != "" {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, *attrs.ResourceArn)
			if vpcSet[vpcID] {
				if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert nm-vpc-attachment→vpc: %w", err)
				}
			}
		}
		for _, subARN := range attrs.SubnetArns {
			if subARN == "" {
				continue
			}
			subID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, subARN)
			if !subnetSet[subID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, subID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert nm-vpc-attachment→subnet: %w", err)
			}
		}
	}
	return nil
}

// resolveNMConnectAttachmentRefs links each ConnectAttachment to its
// CoreNetwork.
func resolveNMConnectAttachmentRefs(acct *account, st *store.Store) error {
	return nmSimpleAttachmentResolver(acct, st, TypeNetworkManagerConnectAttachment, "nm-connect-attachment")
}

// resolveNMSiteToSiteVpnAttachmentRefs links each SiteToSiteVpnAttachment to
// its CoreNetwork.
func resolveNMSiteToSiteVpnAttachmentRefs(acct *account, st *store.Store) error {
	return nmSimpleAttachmentResolver(acct, st, TypeNetworkManagerSiteToSiteVpnAttachment, "nm-s2s-vpn-attachment")
}

// resolveNMDirectConnectGatewayAttachmentRefs links each
// DirectConnectGatewayAttachment to its CoreNetwork.
func resolveNMDirectConnectGatewayAttachmentRefs(acct *account, st *store.Store) error {
	return nmSimpleAttachmentResolver(acct, st, TypeNetworkManagerDirectConnectGatewayAttachment, "nm-dxgw-attachment")
}

// resolveNMTGWRouteTableAttachmentRefs links each
// TransitGatewayRouteTableAttachment to its CoreNetwork.
func resolveNMTGWRouteTableAttachmentRefs(acct *account, st *store.Store) error {
	return nmSimpleAttachmentResolver(acct, st, TypeNetworkManagerTransitGatewayRouteTableAttachment, "nm-tgw-rt-attachment")
}

// nmSimpleAttachmentResolver runs the CoreNetwork edge for any attachment
// type whose AttributesJSON is the bare `Attachment` SDK struct (i.e.
// produced by ListAttachments).
func nmSimpleAttachmentResolver(acct *account, st *store.Store, rtype, label string) error {
	rows, err := listNMResources(acct, st, rtype)
	if err != nil {
		return err
	}
	coreSet, err := scannedIDSet(acct, st, TypeNetworkManagerCoreNetwork)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := nmAttachmentCoreNetworkEdge(acct, st, r, coreSet, label); err != nil {
			return err
		}
	}
	return nil
}

// resolveNMTransitGatewayPeeringRefs links each TransitGatewayPeering to the
// embedded Peering's CoreNetwork plus the cross-service EC2 TransitGateway.
// Scanner stores the SDK `Peering` struct (top-level CoreNetworkId), and the
// Peering response from ListPeerings does NOT carry TransitGatewayArn — that
// lives on the GetTransitGatewayPeering wrapper which the scanner does not
// fetch. Without it, we can only emit the CoreNetwork edge.
func resolveNMTransitGatewayPeeringRefs(acct *account, st *store.Store) error {
	rows, err := listNMResources(acct, st, TypeNetworkManagerTransitGatewayPeering)
	if err != nil {
		return err
	}
	coreSet, err := scannedIDSet(acct, st, TypeNetworkManagerCoreNetwork)
	if err != nil {
		return err
	}
	tgwSet, err := scannedIDSet(acct, st, TypeEC2TransitGateway)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := nmAttachmentCoreNetworkEdge(acct, st, r, coreSet, "nm-tgw-peering"); err != nil {
			return err
		}
		// `ResourceArn` on Peering carries the peer-side TGW ARN when
		// PeeringType=TransitGateway. Use directly as EC2 TGW NativeID.
		var attrs struct {
			ResourceArn *string `json:"ResourceArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ResourceArn == nil || *attrs.ResourceArn == "" {
			continue
		}
		tgwID := store.ResourceID("aws", acct.ID, TypeEC2TransitGateway, *attrs.ResourceArn)
		if !tgwSet[tgwID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgwID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert nm-tgw-peering→tgw: %w", err)
		}
	}
	return nil
}

// resolveNMConnectPeerRefs links each ConnectPeer to its CoreNetwork. The SDK
// `ConnectPeer` struct exposes CoreNetworkId at the top level.
func resolveNMConnectPeerRefs(acct *account, st *store.Store) error {
	rows, err := listNMResources(acct, st, TypeNetworkManagerConnectPeer)
	if err != nil {
		return err
	}
	coreSet, err := scannedIDSet(acct, st, TypeNetworkManagerCoreNetwork)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := nmAttachmentCoreNetworkEdge(acct, st, r, coreSet, "nm-connect-peer"); err != nil {
			return err
		}
	}
	return nil
}
