package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/networkmanager"
	nmtypes "github.com/aws/aws-sdk-go-v2/service/networkmanager/types"
)

func init() {
	registerService(serviceEntry{
		name:   "aws:networkmanager",
		global: true,
		fn:     scanNetworkManager,
		emits: []coverage.TypeDecl{
			{Service: "networkmanager", DiscoType: TypeNetworkManagerGlobalNetwork},
			{Service: "networkmanager", DiscoType: TypeNetworkManagerCoreNetwork},
			{Service: "networkmanager", DiscoType: TypeNetworkManagerSite},
			{Service: "networkmanager", DiscoType: TypeNetworkManagerDevice},
			{Service: "networkmanager", DiscoType: TypeNetworkManagerLink},
			{Service: "networkmanager", DiscoType: TypeNetworkManagerLinkAssociation},
			{Service: "networkmanager", DiscoType: TypeNetworkManagerCustomerGatewayAssociation},
			{Service: "networkmanager", DiscoType: TypeNetworkManagerTransitGatewayRegistration},
			{Service: "networkmanager", DiscoType: TypeNetworkManagerVpcAttachment},
			{Service: "networkmanager", DiscoType: TypeNetworkManagerConnectAttachment},
			{Service: "networkmanager", DiscoType: TypeNetworkManagerSiteToSiteVpnAttachment},
			{Service: "networkmanager", DiscoType: TypeNetworkManagerDirectConnectGatewayAttachment},
			{Service: "networkmanager", DiscoType: TypeNetworkManagerTransitGatewayRouteTableAttachment},
			{Service: "networkmanager", DiscoType: TypeNetworkManagerTransitGatewayPeering},
			{Service: "networkmanager", DiscoType: TypeNetworkManagerConnectPeer},
			{Service: "networkmanager", DiscoType: TypeNetworkManagerCoreNetworkPrefixListAssociation},
		},
	})
}

// networkManagerAPI — narrow surface of NetworkManager ops.
type networkManagerAPI interface {
	DescribeGlobalNetworks(context.Context, *networkmanager.DescribeGlobalNetworksInput, ...func(*networkmanager.Options)) (*networkmanager.DescribeGlobalNetworksOutput, error)
	ListCoreNetworks(context.Context, *networkmanager.ListCoreNetworksInput, ...func(*networkmanager.Options)) (*networkmanager.ListCoreNetworksOutput, error)
	GetSites(context.Context, *networkmanager.GetSitesInput, ...func(*networkmanager.Options)) (*networkmanager.GetSitesOutput, error)
	GetDevices(context.Context, *networkmanager.GetDevicesInput, ...func(*networkmanager.Options)) (*networkmanager.GetDevicesOutput, error)
	GetLinks(context.Context, *networkmanager.GetLinksInput, ...func(*networkmanager.Options)) (*networkmanager.GetLinksOutput, error)
	GetLinkAssociations(context.Context, *networkmanager.GetLinkAssociationsInput, ...func(*networkmanager.Options)) (*networkmanager.GetLinkAssociationsOutput, error)
	GetCustomerGatewayAssociations(context.Context, *networkmanager.GetCustomerGatewayAssociationsInput, ...func(*networkmanager.Options)) (*networkmanager.GetCustomerGatewayAssociationsOutput, error)
	GetTransitGatewayRegistrations(context.Context, *networkmanager.GetTransitGatewayRegistrationsInput, ...func(*networkmanager.Options)) (*networkmanager.GetTransitGatewayRegistrationsOutput, error)
	ListAttachments(context.Context, *networkmanager.ListAttachmentsInput, ...func(*networkmanager.Options)) (*networkmanager.ListAttachmentsOutput, error)
	ListPeerings(context.Context, *networkmanager.ListPeeringsInput, ...func(*networkmanager.Options)) (*networkmanager.ListPeeringsOutput, error)
	ListConnectPeers(context.Context, *networkmanager.ListConnectPeersInput, ...func(*networkmanager.Options)) (*networkmanager.ListConnectPeersOutput, error)
	ListCoreNetworkPrefixListAssociations(context.Context, *networkmanager.ListCoreNetworkPrefixListAssociationsInput, ...func(*networkmanager.Options)) (*networkmanager.ListCoreNetworkPrefixListAssociationsOutput, error)
}

// scanNetworkManager runs only in us-west-2 — NetworkManager is a global
// service with a single home region. Calling from other regions returns
// the same data redundantly.
func scanNetworkManager(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-west-2"
	client := networkmanager.NewFromConfig(acct.cfg, func(o *networkmanager.Options) { o.Region = region })

	globalIDs, t, i, ferr := scanNMGlobalNetworks(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i

	coreIDs, t, i, ferr := scanNMCoreNetworks(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanNMSites(ctx, client, acct, region, st, scanID, globalIDs) },
		func() (int, int, error) { return scanNMDevices(ctx, client, acct, region, st, scanID, globalIDs) },
		func() (int, int, error) { return scanNMLinks(ctx, client, acct, region, st, scanID, globalIDs) },
		func() (int, int, error) {
			return scanNMLinkAssociations(ctx, client, acct, region, st, scanID, globalIDs)
		},
		func() (int, int, error) {
			return scanNMCGWAssociations(ctx, client, acct, region, st, scanID, globalIDs)
		},
		func() (int, int, error) {
			return scanNMTGWRegistrations(ctx, client, acct, region, st, scanID, globalIDs)
		},
		func() (int, int, error) { return scanNMAttachments(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanNMPeerings(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanNMConnectPeers(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanNMPrefixListAssocs(ctx, client, acct, region, st, scanID, coreIDs)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanNMGlobalNetworks(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := networkmanager.NewDescribeGlobalNetworksPaginator(client, &networkmanager.DescribeGlobalNetworksInput{})
	var ids []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "networkmanager:DescribeGlobalNetworks", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("networkmanager:DescribeGlobalNetworks: %w", perr)
		}
		for _, g := range out.GlobalNetworks {
			arn := sv(g.GlobalNetworkArn)
			if arn == "" {
				continue
			}
			id := sv(g.GlobalNetworkId)
			ids = append(ids, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNetworkManagerGlobalNetwork, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "networkmanager global-networks")
	return ids, t, i, err
}

func scanNMCoreNetworks(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := networkmanager.NewListCoreNetworksPaginator(client, &networkmanager.ListCoreNetworksInput{})
	var ids []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "networkmanager:ListCoreNetworks", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("networkmanager:ListCoreNetworks: %w", perr)
		}
		for _, c := range out.CoreNetworks {
			arn := sv(c.CoreNetworkArn)
			if arn == "" {
				continue
			}
			id := sv(c.CoreNetworkId)
			ids = append(ids, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNetworkManagerCoreNetwork, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "networkmanager core-networks")
	return ids, t, i, err
}

func scanNMSites(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string, globalIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, gid := range globalIDs {
		id := gid
		pager := networkmanager.NewGetSitesPaginator(client, &networkmanager.GetSitesInput{GlobalNetworkId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("networkmanager:GetSites %s: %w", gid, perr)
			}
			for _, s := range out.Sites {
				arn := sv(s.SiteArn)
				if arn == "" {
					continue
				}
				label := sv(s.SiteId)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeNetworkManagerSite, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "networkmanager sites")
}

func scanNMDevices(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string, globalIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, gid := range globalIDs {
		id := gid
		pager := networkmanager.NewGetDevicesPaginator(client, &networkmanager.GetDevicesInput{GlobalNetworkId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("networkmanager:GetDevices %s: %w", gid, perr)
			}
			for _, d := range out.Devices {
				arn := sv(d.DeviceArn)
				if arn == "" {
					continue
				}
				label := sv(d.DeviceId)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeNetworkManagerDevice, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "networkmanager devices")
}

func scanNMLinks(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string, globalIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, gid := range globalIDs {
		id := gid
		pager := networkmanager.NewGetLinksPaginator(client, &networkmanager.GetLinksInput{GlobalNetworkId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("networkmanager:GetLinks %s: %w", gid, perr)
			}
			for _, l := range out.Links {
				arn := sv(l.LinkArn)
				if arn == "" {
					continue
				}
				label := sv(l.LinkId)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeNetworkManagerLink, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "networkmanager links")
}

// scanNMLinkAssociations — LinkAssociation has no native ARN. Synth from
// (deviceId, linkId).
func scanNMLinkAssociations(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string, globalIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, gid := range globalIDs {
		id := gid
		pager := networkmanager.NewGetLinkAssociationsPaginator(client, &networkmanager.GetLinkAssociationsInput{GlobalNetworkId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("networkmanager:GetLinkAssociations %s: %w", gid, perr)
			}
			for _, a := range out.LinkAssociations {
				dev := sv(a.DeviceId)
				lnk := sv(a.LinkId)
				if dev == "" || lnk == "" {
					continue
				}
				arn := fmt.Sprintf("arn:aws:networkmanager::%s:global-network/%s/link-association/%s/%s", acct.ID, gid, dev, lnk)
				label := dev + "/" + lnk
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeNetworkManagerLinkAssociation, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "networkmanager link-associations")
}

// scanNMCGWAssociations — CustomerGatewayAssociation synth from (gid,
// cgwArn).
func scanNMCGWAssociations(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string, globalIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, gid := range globalIDs {
		id := gid
		pager := networkmanager.NewGetCustomerGatewayAssociationsPaginator(client, &networkmanager.GetCustomerGatewayAssociationsInput{GlobalNetworkId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("networkmanager:GetCustomerGatewayAssociations %s: %w", gid, perr)
			}
			for _, a := range out.CustomerGatewayAssociations {
				cgw := sv(a.CustomerGatewayArn)
				if cgw == "" {
					continue
				}
				arn := fmt.Sprintf("arn:aws:networkmanager::%s:global-network/%s/cgw-association/%s", acct.ID, gid, cgw)
				label := cgw
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeNetworkManagerCustomerGatewayAssociation, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "networkmanager cgw-associations")
}

// scanNMTGWRegistrations — TransitGatewayRegistration synth from (gid,
// tgwArn).
func scanNMTGWRegistrations(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string, globalIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, gid := range globalIDs {
		id := gid
		pager := networkmanager.NewGetTransitGatewayRegistrationsPaginator(client, &networkmanager.GetTransitGatewayRegistrationsInput{GlobalNetworkId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("networkmanager:GetTransitGatewayRegistrations %s: %w", gid, perr)
			}
			for _, r := range out.TransitGatewayRegistrations {
				tgw := sv(r.TransitGatewayArn)
				if tgw == "" {
					continue
				}
				arn := fmt.Sprintf("arn:aws:networkmanager::%s:global-network/%s/tgw-registration/%s", acct.ID, gid, tgw)
				label := tgw
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeNetworkManagerTransitGatewayRegistration, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "networkmanager tgw-registrations")
}

// scanNMAttachments enumerates all attachments (single ListAttachments
// op covering Connect/VPC/SiteToSiteVpn/DirectConnectGateway/TGWRouteTable)
// and routes each to its disco type via AttachmentType.
func scanNMAttachments(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := networkmanager.NewListAttachmentsPaginator(client, &networkmanager.ListAttachmentsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "networkmanager:ListAttachments", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("networkmanager:ListAttachments: %w", perr)
		}
		for _, a := range out.Attachments {
			id := sv(a.AttachmentId)
			if id == "" {
				continue
			}
			var dtype string
			switch a.AttachmentType {
			case nmtypes.AttachmentTypeConnect:
				dtype = TypeNetworkManagerConnectAttachment
			case nmtypes.AttachmentTypeVpc:
				dtype = TypeNetworkManagerVpcAttachment
			case nmtypes.AttachmentTypeSiteToSiteVpn:
				dtype = TypeNetworkManagerSiteToSiteVpnAttachment
			case nmtypes.AttachmentTypeDirectConnectGateway:
				dtype = TypeNetworkManagerDirectConnectGatewayAttachment
			case nmtypes.AttachmentTypeTransitGatewayRouteTable:
				dtype = TypeNetworkManagerTransitGatewayRouteTableAttachment
			default:
				continue
			}
			arn := fmt.Sprintf("arn:aws:networkmanager::%s:attachment/%s", acct.ID, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: dtype, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "networkmanager attachments")
}

func scanNMPeerings(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := networkmanager.NewListPeeringsPaginator(client, &networkmanager.ListPeeringsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "networkmanager:ListPeerings", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("networkmanager:ListPeerings: %w", perr)
		}
		for _, p := range out.Peerings {
			if p.PeeringType != nmtypes.PeeringTypeTransitGateway {
				continue
			}
			id := sv(p.PeeringId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:networkmanager::%s:peering/%s", acct.ID, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNetworkManagerTransitGatewayPeering, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "networkmanager peerings")
}

func scanNMConnectPeers(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := networkmanager.NewListConnectPeersPaginator(client, &networkmanager.ListConnectPeersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "networkmanager:ListConnectPeers", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("networkmanager:ListConnectPeers: %w", perr)
		}
		for _, c := range out.ConnectPeers {
			id := sv(c.ConnectPeerId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:networkmanager::%s:connect-peer/%s", acct.ID, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNetworkManagerConnectPeer, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "networkmanager connect-peers")
}

func scanNMPrefixListAssocs(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string, coreIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, cid := range coreIDs {
		id := cid
		pager := networkmanager.NewListCoreNetworkPrefixListAssociationsPaginator(client, &networkmanager.ListCoreNetworkPrefixListAssociationsInput{CoreNetworkId: &id})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("networkmanager:ListCoreNetworkPrefixListAssociations %s: %w", cid, perr)
			}
			for _, p := range out.PrefixListAssociations {
				plArn := sv(p.PrefixListArn)
				if plArn == "" {
					continue
				}
				arn := fmt.Sprintf("arn:aws:networkmanager::%s:core-network/%s/prefix-list-association/%s", acct.ID, cid, plArn)
				label := plArn
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeNetworkManagerCoreNetworkPrefixListAssociation, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "networkmanager prefix-list-associations")
}
