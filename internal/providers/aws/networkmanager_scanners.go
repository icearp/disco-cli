package aws

import (
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/networkmanager"
	nmtypes "github.com/aws/aws-sdk-go-v2/service/networkmanager/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeNetworkManagerGlobalNetwork, Service: "networkmanager"})
	registerType(restype.Descriptor{Type: TypeNetworkManagerCoreNetwork, Service: "networkmanager"})
	registerType(restype.Descriptor{Type: TypeNetworkManagerSite, Service: "networkmanager"})
	registerType(restype.Descriptor{Type: TypeNetworkManagerDevice, Service: "networkmanager"})
	registerType(restype.Descriptor{Type: TypeNetworkManagerLink, Service: "networkmanager"})
	registerType(restype.Descriptor{Type: TypeNetworkManagerLinkAssociation, Service: "networkmanager"})
	registerType(restype.Descriptor{Type: TypeNetworkManagerCustomerGatewayAssociation, Service: "networkmanager"})
	registerType(restype.Descriptor{Type: TypeNetworkManagerTransitGatewayRegistration, Service: "networkmanager"})
	registerType(restype.Descriptor{Type: TypeNetworkManagerVpcAttachment, Service: "networkmanager"})
	registerType(restype.Descriptor{Type: TypeNetworkManagerConnectAttachment, Service: "networkmanager"})
	registerType(restype.Descriptor{Type: TypeNetworkManagerSiteToSiteVpnAttachment, Service: "networkmanager"})
	registerType(restype.Descriptor{Type: TypeNetworkManagerDirectConnectGatewayAttachment, Service: "networkmanager"})
	registerType(restype.Descriptor{Type: TypeNetworkManagerTransitGatewayRouteTableAttachment, Service: "networkmanager"})
	registerType(restype.Descriptor{Type: TypeNetworkManagerTransitGatewayPeering, Service: "networkmanager"})
	registerType(restype.Descriptor{Type: TypeNetworkManagerConnectPeer, Service: "networkmanager"})
	registerType(restype.Descriptor{Type: TypeNetworkManagerCoreNetworkPrefixListAssociation, Service: "networkmanager"})
	registerType(restype.Descriptor{Type: TypeNetworkManagerConnection, Service: "networkmanager"})
	registerService(serviceEntry{
		name:   "aws:networkmanager",
		global: true,
		fn:     scanNetworkManager,
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
	GetConnections(context.Context, *networkmanager.GetConnectionsInput, ...func(*networkmanager.Options)) (*networkmanager.GetConnectionsOutput, error)
	ListAttachments(context.Context, *networkmanager.ListAttachmentsInput, ...func(*networkmanager.Options)) (*networkmanager.ListAttachmentsOutput, error)
	ListPeerings(context.Context, *networkmanager.ListPeeringsInput, ...func(*networkmanager.Options)) (*networkmanager.ListPeeringsOutput, error)
	ListConnectPeers(context.Context, *networkmanager.ListConnectPeersInput, ...func(*networkmanager.Options)) (*networkmanager.ListConnectPeersOutput, error)
	ListCoreNetworkPrefixListAssociations(context.Context, *networkmanager.ListCoreNetworkPrefixListAssociationsInput, ...func(*networkmanager.Options)) (*networkmanager.ListCoreNetworkPrefixListAssociationsOutput, error)
}

const (
	nmDescribeGlobalNetworksOp         = "networkmanager:DescribeGlobalNetworks"
	nmListCoreNetworksOp               = "networkmanager:ListCoreNetworks"
	nmGetSitesOp                       = "networkmanager:GetSites"
	nmGetDevicesOp                     = "networkmanager:GetDevices"
	nmGetLinksOp                       = "networkmanager:GetLinks"
	nmGetLinkAssociationsOp            = "networkmanager:GetLinkAssociations"
	nmGetCustomerGatewayAssociationsOp = "networkmanager:GetCustomerGatewayAssociations"
	nmGetTGWRegistrationsOp            = "networkmanager:GetTransitGatewayRegistrations"
	nmGetConnectionsOp                 = "networkmanager:GetConnections"
	nmListAttachmentsOp                = "networkmanager:ListAttachments"
	nmListPeeringsOp                   = "networkmanager:ListPeerings"
	nmListConnectPeersOp               = "networkmanager:ListConnectPeers"
	nmListPrefixListAssocsOp           = "networkmanager:ListCoreNetworkPrefixListAssociations"
)

// nmMaxResults is the documented page cap of every list op scanned here.
var nmMaxResults = sdkaws.Int32(500)

// scanNetworkManager runs only in us-west-2 — NetworkManager is a global,
// single-home-region service; other regions return the same data
// redundantly.
func scanNetworkManager(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-west-2"
	client := networkmanager.NewFromConfig(acct.cfg, func(o *networkmanager.Options) { o.Region = region })
	return scanNMAll(ctx, client, acct, region, st, scanID)
}

// scanNMAll runs every phase and joins their errors; only a store write
// failure stops it. A failed parent phase leaves its child phases fewer
// parents to list.
func scanNMAll(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var errs phaseErrs
	stop := func(op string, t, i int, ferr error) bool {
		total, inserted = total+t, inserted+i
		errs.add(op, ferr)
		return errors.Is(ferr, store.ErrStoreWrite)
	}

	globalIDs, t, i, ferr := scanNMGlobalNetworks(ctx, client, acct, region, st, scanID)
	if stop(nmDescribeGlobalNetworksOp, t, i, ferr) {
		return total, inserted, errs.join(st, acct, region)
	}
	coreIDs, t, i, ferr := scanNMCoreNetworks(ctx, client, acct, region, st, scanID)
	if stop(nmListCoreNetworksOp, t, i, ferr) {
		return total, inserted, errs.join(st, acct, region)
	}
	for _, phase := range []struct {
		op string
		fn func() (int, int, error)
	}{
		{nmGetSitesOp, func() (int, int, error) { return scanNMSites(ctx, client, acct, region, st, scanID, globalIDs) }},
		{nmGetDevicesOp, func() (int, int, error) { return scanNMDevices(ctx, client, acct, region, st, scanID, globalIDs) }},
		{nmGetLinksOp, func() (int, int, error) { return scanNMLinks(ctx, client, acct, region, st, scanID, globalIDs) }},
		{nmGetLinkAssociationsOp, func() (int, int, error) {
			return scanNMLinkAssociations(ctx, client, acct, region, st, scanID, globalIDs)
		}},
		{nmGetCustomerGatewayAssociationsOp, func() (int, int, error) {
			return scanNMCGWAssociations(ctx, client, acct, region, st, scanID, globalIDs)
		}},
		{nmGetTGWRegistrationsOp, func() (int, int, error) {
			return scanNMTGWRegistrations(ctx, client, acct, region, st, scanID, globalIDs)
		}},
		{nmGetConnectionsOp, func() (int, int, error) {
			return scanNMConnections(ctx, client, acct, region, st, scanID, globalIDs)
		}},
		{nmListAttachmentsOp, func() (int, int, error) { return scanNMAttachments(ctx, client, acct, region, st, scanID) }},
		{nmListPeeringsOp, func() (int, int, error) { return scanNMPeerings(ctx, client, acct, region, st, scanID) }},
		{nmListConnectPeersOp, func() (int, int, error) { return scanNMConnectPeers(ctx, client, acct, region, st, scanID) }},
		{nmListPrefixListAssocsOp, func() (int, int, error) {
			return scanNMPrefixListAssocs(ctx, client, acct, region, st, scanID, coreIDs)
		}},
	} {
		t, i, ferr := phase.fn()
		if stop(phase.op, t, i, ferr) {
			break
		}
	}
	return total, inserted, errs.join(st, acct, region)
}

func nmTagsJSON(tags []nmtypes.Tag) *string {
	return kvTagsJSON(tags, func(t nmtypes.Tag) (*string, *string) { return t.Key, t.Value })
}

// nmRow builds the row of a listed NetworkManager element.
func nmRow(acct *account, scanID, rtype, nativeID, name string, element any, tags *string) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: nativeID, Name: &name, Region: regionGlobal,
		AttributesJSON: mustJSON(element), TagsJSON: tags, DiscoveredBy: scanID,
	}
}

// nmReadPages reads every page of one listing. On a failed page it returns the
// items already read with the error, so the caller can keep them. A repeated
// NextToken ends the listing with errRepeatedNextToken.
func nmReadPages[Item any](ctx context.Context, page func(ctx context.Context, token *string) ([]Item, *string, error)) ([]Item, error) {
	var (
		items []Item
		token *string
	)
	for {
		got, next, err := page(ctx, token)
		if err != nil {
			return items, err
		}
		items = append(items, got...)
		done, err := lastPage(token, next)
		if done {
			return items, err
		}
		token = next
	}
}

// nmSkips tallies the listed items not stored, one tally per reason, so each
// reason is warned about once.
type nmSkips struct {
	whys    []string
	tallies map[string]*skipTally
}

func (s *nmSkips) skipped(why, key string) {
	if s.tallies == nil {
		s.tallies = map[string]*skipTally{}
	}
	t, ok := s.tallies[why]
	if !ok {
		t = &skipTally{}
		s.tallies[why] = t
		s.whys = append(s.whys, why)
	}
	t.skipped(key, nil)
}

func (s *nmSkips) warn(st *store.Store, op string, acct *account, region string) {
	for _, why := range s.whys {
		warnNotStored(st, op, acct, region, s.tallies[why], why)
	}
}

// nmBuild turns listed items into rows. row returns nil with the reason and a
// key naming the item when the item cannot be stored.
func nmBuild[Item any](items []Item, row func(Item) (r *store.Resource, why, key string), rows []*store.Resource, skips *nmSkips) []*store.Resource {
	for _, it := range items {
		r, why, key := row(it)
		if r == nil {
			skips.skipped(why, key)
			continue
		}
		rows = append(rows, r)
	}
	return rows
}

// nmStoreList stores the rows of an account-wide listing, those read before a
// failed page included. A denial is warned about rather than returned; any
// other error is returned after the rows are stored. Warnings are reported only
// once the write succeeded, so none claims rows a failed write lost.
func nmStoreList[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string,
	page func(ctx context.Context, token *string) ([]Item, *string, error),
	row func(Item) (r *store.Resource, why, key string),
) (int, int, error) {
	var skips nmSkips
	items, err := nmReadPages(ctx, page)
	rows := nmBuild(items, row, nil, &skips)
	var denied error
	if isAccessDenied(err) {
		denied, err = err, nil
	}
	total, inserted, err := upsertListed(st, rows, op, err)
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, err
	}
	if denied != nil {
		_ = skipIfAccessDenied(st, op, acct.ID, region, denied)
	}
	skips.warn(st, op, acct, region)
	return total, inserted, err
}

// nmStorePerParent lists op's items for each parent in turn and stores the
// rows of every parent read. One parent's failure does not stop the others:
// a parent the service no longer finds (ResourceNotFoundException, deleted
// since it was listed) is skipped silently when nothing was read from it and
// with a warning otherwise; a denied parent is warned about once for the op;
// any other error is returned after the rows are stored. Rows read before a
// failed page are kept in every case.
func nmStorePerParent[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string, parents []string,
	page func(ctx context.Context, parent string, token *string) ([]Item, *string, error),
	row func(parent string, it Item) (r *store.Resource, why, key string),
) (int, int, error) {
	var (
		rows             []*store.Resource
		errs             []error
		denied, vanished skipTally
		skips            nmSkips
	)
	for _, p := range parents {
		if err := ctx.Err(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", op, err))
			break
		}
		items, err := nmReadPages(ctx, func(ctx context.Context, token *string) ([]Item, *string, error) {
			return page(ctx, p, token)
		})
		rows = nmBuild(items, func(it Item) (*store.Resource, string, string) { return row(p, it) }, rows, &skips)
		switch {
		case err == nil:
		case isAPIErrorCode(err, "ResourceNotFoundException") && len(items) == 0:
		case isAPIErrorCode(err, "ResourceNotFoundException"):
			vanished.skipped(p, err)
		case isAccessDenied(err):
			denied.skipped(p, err)
		default:
			errs = append(errs, fmt.Errorf("%s %s: %w", op, p, err))
		}
	}
	total, inserted, uerr := upsertBatch(st, rows, op)
	if uerr != nil {
		return total, inserted, errors.Join(append(errs, uerr)...)
	}
	if denied.count > 0 {
		_ = skipIfAccessDenied(st, op, acct.ID, region, fmt.Errorf("%d parent(s) denied, first %s: %w", denied.count, denied.first, denied.err))
	}
	if vanished.count > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d parent(s) were deleted while their listing was read, so only part of it was stored; first %s: %v", vanished.count, vanished.first, vanished.err),
		})
	}
	skips.warn(st, op, acct, region)
	return total, inserted, errors.Join(errs...)
}

// scanNMGlobalNetworks stores the global networks and returns the ids of
// those stored, for the per-global-network phases.
func scanNMGlobalNetworks(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var (
		ids       []string
		noChildID skipTally
	)
	t, i, err := nmStoreList(ctx, st, acct, region, nmDescribeGlobalNetworksOp,
		func(ctx context.Context, token *string) ([]nmtypes.GlobalNetwork, *string, error) {
			out, err := client.DescribeGlobalNetworks(ctx, &networkmanager.DescribeGlobalNetworksInput{NextToken: token, MaxResults: nmMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.GlobalNetworks, out.NextToken, nil
		},
		func(g nmtypes.GlobalNetwork) (*store.Resource, string, string) {
			arn, id := sv(g.GlobalNetworkArn), sv(g.GlobalNetworkId)
			if arn == "" {
				return nil, "global networks have an empty GlobalNetworkArn, and their children were not read either", id
			}
			if id == "" {
				noChildID.skipped(arn, nil)
			} else {
				ids = append(ids, id)
			}
			return nmRow(acct, scanID, TypeNetworkManagerGlobalNetwork, arn, id, g, nmTagsJSON(g.Tags)), "", ""
		})
	if !errors.Is(err, store.ErrStoreWrite) {
		nmWarnNoChildID(st, nmDescribeGlobalNetworksOp, acct, region, &noChildID, "global network(s)", "GlobalNetworkId")
	}
	return ids, t, i, err
}

// nmWarnNoChildID warns once for the parents stored without the id their
// child listings key on, so those children were not read.
func nmWarnNoChildID(st *store.Store, op string, acct *account, region string, t *skipTally, what, field string) {
	if t.count == 0 {
		return
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
		Message: fmt.Sprintf("%d %s stored with an empty %s, so their children were not read; first %s", t.count, what, field, t.first),
	})
}

// scanNMCoreNetworks stores the core networks and returns the ids of those
// stored, for the prefix-list association phase.
func scanNMCoreNetworks(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var (
		ids       []string
		noChildID skipTally
	)
	t, i, err := nmStoreList(ctx, st, acct, region, nmListCoreNetworksOp,
		func(ctx context.Context, token *string) ([]nmtypes.CoreNetworkSummary, *string, error) {
			out, err := client.ListCoreNetworks(ctx, &networkmanager.ListCoreNetworksInput{NextToken: token, MaxResults: nmMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.CoreNetworks, out.NextToken, nil
		},
		func(c nmtypes.CoreNetworkSummary) (*store.Resource, string, string) {
			arn, id := sv(c.CoreNetworkArn), sv(c.CoreNetworkId)
			if arn == "" {
				return nil, "core networks have an empty CoreNetworkArn, and their children were not read either", id
			}
			if id == "" {
				noChildID.skipped(arn, nil)
			} else {
				ids = append(ids, id)
			}
			return nmRow(acct, scanID, TypeNetworkManagerCoreNetwork, arn, id, c, nmTagsJSON(c.Tags)), "", ""
		})
	if !errors.Is(err, store.ErrStoreWrite) {
		nmWarnNoChildID(st, nmListCoreNetworksOp, acct, region, &noChildID, "core network(s)", "CoreNetworkId")
	}
	return ids, t, i, err
}

func scanNMSites(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string, globalIDs []string) (int, int, error) {
	return nmStorePerParent(ctx, st, acct, region, nmGetSitesOp, globalIDs,
		func(ctx context.Context, gid string, token *string) ([]nmtypes.Site, *string, error) {
			out, err := client.GetSites(ctx, &networkmanager.GetSitesInput{GlobalNetworkId: &gid, NextToken: token, MaxResults: nmMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.Sites, out.NextToken, nil
		},
		func(gid string, s nmtypes.Site) (*store.Resource, string, string) {
			arn := sv(s.SiteArn)
			if arn == "" {
				return nil, "sites have an empty SiteArn (first names their global network)", gid
			}
			return nmRow(acct, scanID, TypeNetworkManagerSite, arn, sv(s.SiteId), s, nmTagsJSON(s.Tags)), "", ""
		})
}

func scanNMDevices(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string, globalIDs []string) (int, int, error) {
	return nmStorePerParent(ctx, st, acct, region, nmGetDevicesOp, globalIDs,
		func(ctx context.Context, gid string, token *string) ([]nmtypes.Device, *string, error) {
			out, err := client.GetDevices(ctx, &networkmanager.GetDevicesInput{GlobalNetworkId: &gid, NextToken: token, MaxResults: nmMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.Devices, out.NextToken, nil
		},
		func(gid string, d nmtypes.Device) (*store.Resource, string, string) {
			arn := sv(d.DeviceArn)
			if arn == "" {
				return nil, "devices have an empty DeviceArn (first names their global network)", gid
			}
			return nmRow(acct, scanID, TypeNetworkManagerDevice, arn, sv(d.DeviceId), d, nmTagsJSON(d.Tags)), "", ""
		})
}

func scanNMLinks(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string, globalIDs []string) (int, int, error) {
	return nmStorePerParent(ctx, st, acct, region, nmGetLinksOp, globalIDs,
		func(ctx context.Context, gid string, token *string) ([]nmtypes.Link, *string, error) {
			out, err := client.GetLinks(ctx, &networkmanager.GetLinksInput{GlobalNetworkId: &gid, NextToken: token, MaxResults: nmMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.Links, out.NextToken, nil
		},
		func(gid string, l nmtypes.Link) (*store.Resource, string, string) {
			arn := sv(l.LinkArn)
			if arn == "" {
				return nil, "links have an empty LinkArn (first names their global network)", gid
			}
			return nmRow(acct, scanID, TypeNetworkManagerLink, arn, sv(l.LinkId), l, nmTagsJSON(l.Tags)), "", ""
		})
}

// scanNMLinkAssociations — LinkAssociation has no native ARN. Synth from
// (deviceId, linkId).
func scanNMLinkAssociations(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string, globalIDs []string) (int, int, error) {
	return nmStorePerParent(ctx, st, acct, region, nmGetLinkAssociationsOp, globalIDs,
		func(ctx context.Context, gid string, token *string) ([]nmtypes.LinkAssociation, *string, error) {
			out, err := client.GetLinkAssociations(ctx, &networkmanager.GetLinkAssociationsInput{GlobalNetworkId: &gid, NextToken: token, MaxResults: nmMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.LinkAssociations, out.NextToken, nil
		},
		func(gid string, a nmtypes.LinkAssociation) (*store.Resource, string, string) {
			dev, lnk := sv(a.DeviceId), sv(a.LinkId)
			if dev == "" || lnk == "" {
				return nil, "link associations have an empty DeviceId or LinkId (first names their global network)", gid
			}
			arn := fmt.Sprintf("arn:aws:networkmanager::%s:global-network/%s/link-association/%s/%s", acct.ID, gid, dev, lnk)
			return nmRow(acct, scanID, TypeNetworkManagerLinkAssociation, arn, dev+"/"+lnk, a, nil), "", ""
		})
}

// scanNMCGWAssociations — CustomerGatewayAssociation synth from (gid,
// cgwArn).
func scanNMCGWAssociations(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string, globalIDs []string) (int, int, error) {
	return nmStorePerParent(ctx, st, acct, region, nmGetCustomerGatewayAssociationsOp, globalIDs,
		func(ctx context.Context, gid string, token *string) ([]nmtypes.CustomerGatewayAssociation, *string, error) {
			out, err := client.GetCustomerGatewayAssociations(ctx, &networkmanager.GetCustomerGatewayAssociationsInput{GlobalNetworkId: &gid, NextToken: token, MaxResults: nmMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.CustomerGatewayAssociations, out.NextToken, nil
		},
		func(gid string, a nmtypes.CustomerGatewayAssociation) (*store.Resource, string, string) {
			cgw := sv(a.CustomerGatewayArn)
			if cgw == "" {
				return nil, "customer gateway associations have an empty CustomerGatewayArn (first names their global network)", gid
			}
			arn := fmt.Sprintf("arn:aws:networkmanager::%s:global-network/%s/cgw-association/%s", acct.ID, gid, cgw)
			return nmRow(acct, scanID, TypeNetworkManagerCustomerGatewayAssociation, arn, cgw, a, nil), "", ""
		})
}

// scanNMTGWRegistrations — TransitGatewayRegistration synth from (gid,
// tgwArn).
func scanNMTGWRegistrations(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string, globalIDs []string) (int, int, error) {
	return nmStorePerParent(ctx, st, acct, region, nmGetTGWRegistrationsOp, globalIDs,
		func(ctx context.Context, gid string, token *string) ([]nmtypes.TransitGatewayRegistration, *string, error) {
			out, err := client.GetTransitGatewayRegistrations(ctx, &networkmanager.GetTransitGatewayRegistrationsInput{GlobalNetworkId: &gid, NextToken: token, MaxResults: nmMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.TransitGatewayRegistrations, out.NextToken, nil
		},
		func(gid string, r nmtypes.TransitGatewayRegistration) (*store.Resource, string, string) {
			tgw := sv(r.TransitGatewayArn)
			if tgw == "" {
				return nil, "transit gateway registrations have an empty TransitGatewayArn (first names their global network)", gid
			}
			arn := fmt.Sprintf("arn:aws:networkmanager::%s:global-network/%s/tgw-registration/%s", acct.ID, gid, tgw)
			return nmRow(acct, scanID, TypeNetworkManagerTransitGatewayRegistration, arn, tgw, r, nil), "", ""
		})
}

// scanNMConnections enumerates device-to-device connections per global
// network (GetConnections requires GlobalNetworkId). Connections carry a
// native ConnectionArn.
func scanNMConnections(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string, globalIDs []string) (int, int, error) {
	return nmStorePerParent(ctx, st, acct, region, nmGetConnectionsOp, globalIDs,
		func(ctx context.Context, gid string, token *string) ([]nmtypes.Connection, *string, error) {
			out, err := client.GetConnections(ctx, &networkmanager.GetConnectionsInput{GlobalNetworkId: &gid, NextToken: token, MaxResults: nmMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.Connections, out.NextToken, nil
		},
		func(gid string, c nmtypes.Connection) (*store.Resource, string, string) {
			arn := sv(c.ConnectionArn)
			if arn == "" {
				return nil, "connections have an empty ConnectionArn (first names their global network)", gid
			}
			return nmRow(acct, scanID, TypeNetworkManagerConnection, arn, sv(c.ConnectionId), c, nmTagsJSON(c.Tags)), "", ""
		})
}

// nmAttachmentTypes maps each AttachmentType to its disco type.
var nmAttachmentTypes = map[nmtypes.AttachmentType]string{
	nmtypes.AttachmentTypeConnect:                  TypeNetworkManagerConnectAttachment,
	nmtypes.AttachmentTypeVpc:                      TypeNetworkManagerVpcAttachment,
	nmtypes.AttachmentTypeSiteToSiteVpn:            TypeNetworkManagerSiteToSiteVpnAttachment,
	nmtypes.AttachmentTypeDirectConnectGateway:     TypeNetworkManagerDirectConnectGatewayAttachment,
	nmtypes.AttachmentTypeTransitGatewayRouteTable: TypeNetworkManagerTransitGatewayRouteTableAttachment,
}

// scanNMAttachments enumerates all attachments (single ListAttachments
// op covering Connect/VPC/SiteToSiteVpn/DirectConnectGateway/TGWRouteTable)
// and routes each to its disco type via AttachmentType. An AttachmentType
// newer than this map is warned about, not stored.
func scanNMAttachments(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return nmStoreList(ctx, st, acct, region, nmListAttachmentsOp,
		func(ctx context.Context, token *string) ([]nmtypes.Attachment, *string, error) {
			out, err := client.ListAttachments(ctx, &networkmanager.ListAttachmentsInput{NextToken: token, MaxResults: nmMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.Attachments, out.NextToken, nil
		},
		func(a nmtypes.Attachment) (*store.Resource, string, string) {
			id := sv(a.AttachmentId)
			if id == "" {
				return nil, "attachments have an empty AttachmentId (first names their core network)", sv(a.CoreNetworkId)
			}
			dtype, ok := nmAttachmentTypes[a.AttachmentType]
			if !ok {
				return nil, "attachments have an AttachmentType disco does not map", fmt.Sprintf("%s (%s)", id, a.AttachmentType)
			}
			arn := fmt.Sprintf("arn:aws:networkmanager::%s:attachment/%s", acct.ID, id)
			return nmRow(acct, scanID, dtype, arn, id, a, nmTagsJSON(a.Tags)), "", ""
		})
}

// scanNMPeerings stores the transit gateway peerings, the only PeeringType
// disco maps; a newer type is warned about, not stored.
func scanNMPeerings(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return nmStoreList(ctx, st, acct, region, nmListPeeringsOp,
		func(ctx context.Context, token *string) ([]nmtypes.Peering, *string, error) {
			out, err := client.ListPeerings(ctx, &networkmanager.ListPeeringsInput{NextToken: token, MaxResults: nmMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.Peerings, out.NextToken, nil
		},
		func(p nmtypes.Peering) (*store.Resource, string, string) {
			id := sv(p.PeeringId)
			if id == "" {
				return nil, "peerings have an empty PeeringId (first names their core network)", sv(p.CoreNetworkId)
			}
			if p.PeeringType != nmtypes.PeeringTypeTransitGateway {
				return nil, "peerings have a PeeringType disco does not map", fmt.Sprintf("%s (%s)", id, p.PeeringType)
			}
			arn := fmt.Sprintf("arn:aws:networkmanager::%s:peering/%s", acct.ID, id)
			return nmRow(acct, scanID, TypeNetworkManagerTransitGatewayPeering, arn, id, p, nmTagsJSON(p.Tags)), "", ""
		})
}

func scanNMConnectPeers(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return nmStoreList(ctx, st, acct, region, nmListConnectPeersOp,
		func(ctx context.Context, token *string) ([]nmtypes.ConnectPeerSummary, *string, error) {
			out, err := client.ListConnectPeers(ctx, &networkmanager.ListConnectPeersInput{NextToken: token, MaxResults: nmMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.ConnectPeers, out.NextToken, nil
		},
		func(c nmtypes.ConnectPeerSummary) (*store.Resource, string, string) {
			id := sv(c.ConnectPeerId)
			if id == "" {
				return nil, "connect peers have an empty ConnectPeerId (first names their core network)", sv(c.CoreNetworkId)
			}
			arn := fmt.Sprintf("arn:aws:networkmanager::%s:connect-peer/%s", acct.ID, id)
			return nmRow(acct, scanID, TypeNetworkManagerConnectPeer, arn, id, c, nmTagsJSON(c.Tags)), "", ""
		})
}

func scanNMPrefixListAssocs(ctx context.Context, client networkManagerAPI, acct *account, region string, st *store.Store, scanID string, coreIDs []string) (int, int, error) {
	return nmStorePerParent(ctx, st, acct, region, nmListPrefixListAssocsOp, coreIDs,
		func(ctx context.Context, cid string, token *string) ([]nmtypes.PrefixListAssociation, *string, error) {
			out, err := client.ListCoreNetworkPrefixListAssociations(ctx, &networkmanager.ListCoreNetworkPrefixListAssociationsInput{CoreNetworkId: &cid, NextToken: token, MaxResults: nmMaxResults})
			if err != nil {
				return nil, nil, err
			}
			return out.PrefixListAssociations, out.NextToken, nil
		},
		func(cid string, p nmtypes.PrefixListAssociation) (*store.Resource, string, string) {
			plArn := sv(p.PrefixListArn)
			if plArn == "" {
				return nil, "prefix list associations have an empty PrefixListArn (first names their core network)", cid
			}
			arn := fmt.Sprintf("arn:aws:networkmanager::%s:core-network/%s/prefix-list-association/%s", acct.ID, cid, plArn)
			return nmRow(acct, scanID, TypeNetworkManagerCoreNetworkPrefixListAssociation, arn, plArn, p, nil), "", ""
		})
}
