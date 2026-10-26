package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/directconnect"
	directconnecttypes "github.com/aws/aws-sdk-go-v2/service/directconnect/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDirectConnectConnection, Service: "directconnect"})
	registerType(restype.Descriptor{Type: TypeDirectConnectDirectConnectGateway, Service: "directconnect"})
	registerType(restype.Descriptor{Type: TypeDirectConnectDirectConnectGatewayAssociation, Service: "directconnect"})
	registerType(restype.Descriptor{Type: TypeDirectConnectInterconnect, Service: "directconnect"})
	registerType(restype.Descriptor{Type: TypeDirectConnectLag, Service: "directconnect"})
	registerType(restype.Descriptor{Type: TypeDirectConnectPrivateVirtualInterface, Service: "directconnect"})
	registerType(restype.Descriptor{Type: TypeDirectConnectPublicVirtualInterface, Service: "directconnect"})
	registerType(restype.Descriptor{Type: TypeDirectConnectResiliencyGroup, Service: "directconnect"})
	registerType(restype.Descriptor{Type: TypeDirectConnectTransitVirtualInterface, Service: "directconnect"})
	registerService(serviceEntry{
		name: "aws:directconnect",
		fn:   scanDirectConnect,
	})
}

type dxAPI interface {
	DescribeConnections(context.Context, *directconnect.DescribeConnectionsInput, ...func(*directconnect.Options)) (*directconnect.DescribeConnectionsOutput, error)
	DescribeDirectConnectGateways(context.Context, *directconnect.DescribeDirectConnectGatewaysInput, ...func(*directconnect.Options)) (*directconnect.DescribeDirectConnectGatewaysOutput, error)
	DescribeDirectConnectGatewayAssociations(context.Context, *directconnect.DescribeDirectConnectGatewayAssociationsInput, ...func(*directconnect.Options)) (*directconnect.DescribeDirectConnectGatewayAssociationsOutput, error)
	DescribeInterconnects(context.Context, *directconnect.DescribeInterconnectsInput, ...func(*directconnect.Options)) (*directconnect.DescribeInterconnectsOutput, error)
	DescribeLags(context.Context, *directconnect.DescribeLagsInput, ...func(*directconnect.Options)) (*directconnect.DescribeLagsOutput, error)
	DescribeVirtualInterfaces(context.Context, *directconnect.DescribeVirtualInterfacesInput, ...func(*directconnect.Options)) (*directconnect.DescribeVirtualInterfacesOutput, error)
	GetResiliencyGroup(context.Context, *directconnect.GetResiliencyGroupInput, ...func(*directconnect.Options)) (*directconnect.GetResiliencyGroupOutput, error)
	ListResiliencyGroupAssociations(context.Context, *directconnect.ListResiliencyGroupAssociationsInput, ...func(*directconnect.Options)) (*directconnect.ListResiliencyGroupAssociationsOutput, error)
	ListResiliencyGroups(context.Context, *directconnect.ListResiliencyGroupsInput, ...func(*directconnect.Options)) (*directconnect.ListResiliencyGroupsOutput, error)
}

const (
	dxDescribeConnectionsOp             = "directconnect:DescribeConnections"
	dxDescribeLagsOp                    = "directconnect:DescribeLags"
	dxDescribeVirtualInterfacesOp       = "directconnect:DescribeVirtualInterfaces"
	dxDescribeGatewaysOp                = "directconnect:DescribeDirectConnectGateways"
	dxDescribeGatewayAssociationsOp     = "directconnect:DescribeDirectConnectGatewayAssociations"
	dxDescribeInterconnectsOp           = "directconnect:DescribeInterconnects"
	dxListResiliencyGroupsOp            = "directconnect:ListResiliencyGroups"
	dxGetResiliencyGroupOp              = "directconnect:GetResiliencyGroup"
	dxListResiliencyGroupAssociationsOp = "directconnect:ListResiliencyGroupAssociations"
)

// dxPageLimit is the MaxResults cap the Direct Connect model documents for its
// paginated Describe and List ops.
const dxPageLimit = 100

// scanDirectConnect discovers DirectConnect resources. Connections, LAGs,
// interconnects, resiliency groups and VirtualInterfaces are regional.
// DirectConnectGateway and association objects are global; gated to one
// region (isOnceRegion) to avoid duplicate upserts.
func scanDirectConnect(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := directconnect.NewFromConfig(acct.cfg, func(o *directconnect.Options) { o.Region = region })
	return scanDirectConnectAll(ctx, client, acct, region, st, scanID)
}

// scanDirectConnectAll runs every Direct Connect phase and joins their errors;
// only a store write failure stops it. The gateway associations are read for
// the gateways the gateway phase stored, so a failed gateway listing leaves
// them fewer gateways rather than none.
func scanDirectConnectAll(ctx context.Context, client dxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		phases phaseTotals
		gwIDs  []string
	)
	for _, phase := range []struct {
		op string
		fn func() (int, int, error)
	}{
		{dxDescribeConnectionsOp, func() (int, int, error) { return scanDXConnections(ctx, client, acct, region, st, scanID) }},
		{dxDescribeLagsOp, func() (int, int, error) { return scanDXLags(ctx, client, acct, region, st, scanID) }},
		{dxDescribeVirtualInterfacesOp, func() (int, int, error) { return scanDXVirtualInterfaces(ctx, client, acct, region, st, scanID) }},
		{dxDescribeGatewaysOp, func() (t, i int, err error) {
			gwIDs, t, i, err = scanDXGateways(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{dxDescribeGatewayAssociationsOp, func() (int, int, error) {
			return scanDXGatewayAssociations(ctx, client, acct, region, st, scanID, gwIDs)
		}},
		{dxDescribeInterconnectsOp, func() (int, int, error) { return scanDXInterconnects(ctx, client, acct, region, st, scanID) }},
		{dxListResiliencyGroupsOp, func() (int, int, error) { return scanDXResiliencyGroups(ctx, client, acct, region, st, scanID) }},
	} {
		t, i, err := phase.fn()
		if phases.addPhase(phase.op, t, i, err) {
			break
		}
	}
	return phases.result(st, acct, region)
}

// dxTagsJSON renders the tags a Direct Connect element carries.
func dxTagsJSON(tags []directconnecttypes.Tag) *string {
	return kvTagsJSON(tags, func(t directconnecttypes.Tag) (*string, *string) { return t.Key, t.Value })
}

// dxName is the element's name, or its id when it has none.
func dxName(name *string, id string) *string {
	s := cmp.Or(sv(name), id)
	return &s
}

func scanDXConnections(ctx context.Context, client dxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, dxDescribeConnectionsOp,
		func(ctx context.Context, token *string) ([]directconnecttypes.Connection, *string, error) {
			out, err := client.DescribeConnections(ctx, &directconnect.DescribeConnectionsInput{MaxResults: sdkaws.Int32(dxPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Connections, out.NextToken, nil
		},
		func(c directconnecttypes.Connection) (*store.Resource, string, string) {
			id := sv(c.ConnectionId)
			if id == "" {
				return nil, "connections have an empty ConnectionId", cmp.Or(sv(c.ConnectionName), "one without a ConnectionName")
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDirectConnectConnection, NativeID: dxConnectionARN(acct.partition(), region, acct.ID, id),
				Name: dxName(c.ConnectionName, id), Region: &region, Status: nonEmptyPtr(string(c.ConnectionState)),
				AttributesJSON: mustJSON(c), TagsJSON: dxTagsJSON(c.Tags), DiscoveredBy: scanID,
			}, "", ""
		})
}

func scanDXLags(ctx context.Context, client dxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, dxDescribeLagsOp,
		func(ctx context.Context, token *string) ([]directconnecttypes.Lag, *string, error) {
			out, err := client.DescribeLags(ctx, &directconnect.DescribeLagsInput{MaxResults: sdkaws.Int32(dxPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Lags, out.NextToken, nil
		},
		func(l directconnecttypes.Lag) (*store.Resource, string, string) {
			id := sv(l.LagId)
			if id == "" {
				return nil, "LAGs have an empty LagId", cmp.Or(sv(l.LagName), "one without a LagName")
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDirectConnectLag, NativeID: dxLagARN(acct.partition(), region, acct.ID, id),
				Name: dxName(l.LagName, id), Region: &region, Status: nonEmptyPtr(string(l.LagState)),
				AttributesJSON: mustJSON(l), TagsJSON: dxTagsJSON(l.Tags), DiscoveredBy: scanID,
			}, "", ""
		})
}

// dxVirtualInterfaceTypes maps the VirtualInterfaceType values the model
// documents ("private", "public", "transit") to the three disco types of the
// CFN type split.
var dxVirtualInterfaceTypes = map[string]string{
	"private": TypeDirectConnectPrivateVirtualInterface,
	"public":  TypeDirectConnectPublicVirtualInterface,
	"transit": TypeDirectConnectTransitVirtualInterface,
}

// scanDXVirtualInterfaces stores each virtual interface under the disco type
// of its VirtualInterfaceType. One of another type has no disco type to be
// stored under, so it is warned about instead.
func scanDXVirtualInterfaces(ctx context.Context, client dxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, dxDescribeVirtualInterfacesOp,
		func(ctx context.Context, token *string) ([]directconnecttypes.VirtualInterface, *string, error) {
			out, err := client.DescribeVirtualInterfaces(ctx, &directconnect.DescribeVirtualInterfacesInput{MaxResults: sdkaws.Int32(dxPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.VirtualInterfaces, out.NextToken, nil
		},
		func(vi directconnecttypes.VirtualInterface) (*store.Resource, string, string) {
			id := sv(vi.VirtualInterfaceId)
			if id == "" {
				return nil, "virtual interfaces have an empty VirtualInterfaceId", cmp.Or(sv(vi.VirtualInterfaceName), "one without a VirtualInterfaceName")
			}
			dt, ok := dxVirtualInterfaceTypes[sv(vi.VirtualInterfaceType)]
			if !ok {
				return nil, "virtual interfaces have a VirtualInterfaceType other than private, public or transit", id + " (" + sv(vi.VirtualInterfaceType) + ")"
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: dt, NativeID: arnFor(acct.partition(), "directconnect", region, acct.ID, "dxvif/"+id),
				Name: dxName(vi.VirtualInterfaceName, id), Region: &region, Status: nonEmptyPtr(string(vi.VirtualInterfaceState)),
				AttributesJSON: mustJSON(vi), TagsJSON: dxTagsJSON(vi.Tags), DiscoveredBy: scanID,
			}, "", ""
		})
}

// scanDXGateways stores the account's Direct Connect gateways and returns the
// ids of those stored, read before a failed page included. Gateways are
// global, so only the scan of Direct Connect's once-region (onceRegion) lists
// them, once per account.
func scanDXGateways(ctx context.Context, client dxAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	if !acct.isOnceRegion("directconnect", region) {
		return nil, 0, 0, nil
	}
	gateways, listErr := readTokenPages(ctx, func(ctx context.Context, token *string) ([]directconnecttypes.DirectConnectGateway, *string, error) {
		out, err := client.DescribeDirectConnectGateways(ctx, &directconnect.DescribeDirectConnectGatewaysInput{MaxResults: sdkaws.Int32(dxPageLimit), NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		return out.DirectConnectGateways, out.NextToken, nil
	})
	var (
		ids   []string
		skips notStoredSkips
	)
	rows := buildListedRows(gateways, func(g directconnecttypes.DirectConnectGateway) (*store.Resource, string, string) {
		id := sv(g.DirectConnectGatewayId)
		if id == "" {
			return nil, "gateways have an empty DirectConnectGatewayId", cmp.Or(sv(g.DirectConnectGatewayName), "one without a DirectConnectGatewayName")
		}
		ids = append(ids, id)
		return &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeDirectConnectDirectConnectGateway, NativeID: dxGatewayARN(acct.partition(), acct.ID, id),
			Name: dxName(g.DirectConnectGatewayName, id), Region: &region, Status: nonEmptyPtr(string(g.DirectConnectGatewayState)),
			AttributesJSON: mustJSON(g), TagsJSON: dxTagsJSON(g.Tags), DiscoveredBy: scanID,
		}, "", ""
	}, nil, &skips)
	t, i, err := storeReadRows(st, acct, region, dxDescribeGatewaysOp, rows, listErr, func() { skips.warn(st, dxDescribeGatewaysOp, acct, region) })
	return ids, t, i, err
}

// scanDXGatewayAssociations reads the associations of each gateway
// scanDXGateways stored. The op models no not-found error for a gateway
// deleted since it was listed, so any per-gateway error but a denial is
// returned after the other gateways' associations are stored.
func scanDXGatewayAssociations(ctx context.Context, client dxAPI, acct *account, region string, st *store.Store, scanID string, gwIDs []string) (int, int, error) {
	return storeTokenPerParent(ctx, st, acct, region, dxDescribeGatewayAssociationsOp, nil, gwIDs,
		func(ctx context.Context, gwID string, token *string) ([]directconnecttypes.DirectConnectGatewayAssociation, *string, error) {
			out, err := client.DescribeDirectConnectGatewayAssociations(ctx, &directconnect.DescribeDirectConnectGatewayAssociationsInput{
				DirectConnectGatewayId: &gwID, MaxResults: sdkaws.Int32(dxPageLimit), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.DirectConnectGatewayAssociations, out.NextToken, nil
		},
		func(gwID string, a directconnecttypes.DirectConnectGatewayAssociation) (*store.Resource, string, string) {
			id := sv(a.AssociationId)
			if id == "" {
				return nil, "gateway associations have an empty AssociationId", "one of gateway " + gwID
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDirectConnectDirectConnectGatewayAssociation, NativeID: dxGatewayARN(acct.partition(), acct.ID, gwID) + "/association/" + id,
				Name: &id, Region: &region, Status: nonEmptyPtr(string(a.AssociationState)),
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}, "", ""
		})
}

// scanDXInterconnects stores the partner-owned interconnects of the region. An
// account that is not a Direct Connect partner owns none, and the model gives
// no distinct error for it, so any error but a denial is returned.
func scanDXInterconnects(ctx context.Context, client dxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, dxDescribeInterconnectsOp,
		func(ctx context.Context, token *string) ([]directconnecttypes.Interconnect, *string, error) {
			out, err := client.DescribeInterconnects(ctx, &directconnect.DescribeInterconnectsInput{MaxResults: sdkaws.Int32(dxPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Interconnects, out.NextToken, nil
		},
		func(ic directconnecttypes.Interconnect) (*store.Resource, string, string) {
			id := sv(ic.InterconnectId)
			if id == "" {
				return nil, "interconnects have an empty InterconnectId", cmp.Or(sv(ic.InterconnectName), "one without an InterconnectName")
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDirectConnectInterconnect, NativeID: dxInterconnectARN(acct.partition(), region, acct.ID, id),
				Name: dxName(ic.InterconnectName, id), Region: &region, Status: nonEmptyPtr(string(ic.InterconnectState)),
				AttributesJSON: mustJSON(ic), TagsJSON: dxTagsJSON(ic.Tags), DiscoveredBy: scanID,
			}, "", ""
		})
}

// dxResiliencyGroupAttrs is the group's detail with its connection
// associations, sorted because the API documents no order and a reordered
// list would otherwise version the group. Associations is null when listing
// them failed, was denied or ended incomplete, and [] when the group has none.
type dxResiliencyGroupAttrs struct {
	directconnecttypes.ResiliencyGroup
	Associations []directconnecttypes.ResiliencyGroupAssociation
}

// scanDXResiliencyGroups stores the region's resiliency groups, each from its
// GetResiliencyGroup detail (the list summary lacks the tags) with its
// connection associations embedded: an association carries nothing but the
// connection ARN and its state, so it is an edge, not a resource. Groups
// listed as deleted are not fetched. A group whose Get fails, or that was not
// read because ctx ended, is stored from its list summary (no tags,
// Associations null): a denial is warned about, any other error, a transient
// one included, is returned after storing, since the model gives no not-found
// code for a group deleted since the list. A group whose associations could
// not be read is stored with Associations null, and that error is returned
// too unless it was a denial.
//
// The group ARN has no region segment, like the dx-gateway ARN whose phase is
// gated to one region, yet this phase scans every region: the model documents
// ListResiliencyGroups as listing "the resiliency groups owned by your Amazon
// Web Services account in the current Amazon Web Services Region". That is not
// yet confirmed against a live account; if one group were listed in two
// regions, its row would flip Region on every scan.
func scanDXResiliencyGroups(ctx context.Context, client dxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	summaries, listErr := readTokenPages(ctx, func(ctx context.Context, token *string) ([]directconnecttypes.ResiliencyGroupSummary, *string, error) {
		out, err := client.ListResiliencyGroups(ctx, &directconnect.ListResiliencyGroupsInput{MaxResults: sdkaws.Int32(dxPageLimit), NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		return out.Items, out.NextToken, nil
	})
	var (
		listSkips                     notStoredSkips
		groups                        []directconnecttypes.ResiliencyGroupSummary
		getDenied, assocDenied, noARN skipTally
		rows                          []*store.Resource
		errs                          []error
	)
	for _, g := range summaries {
		switch {
		case sv(g.ResiliencyGroupId) == "":
			listSkips.skipped("resiliency groups have an empty ResiliencyGroupId", cmp.Or(sv(g.ResiliencyGroupName), "one without a ResiliencyGroupName"))
		case g.State != directconnecttypes.ResiliencyGroupStateDeleted:
			groups = append(groups, g)
		}
	}
	reads, stopped := readEachKey(ctx, groups, func(ctx context.Context, s directconnecttypes.ResiliencyGroupSummary) dxGroupRead {
		return readDXResiliencyGroup(ctx, client, s)
	})
	if stopped != nil {
		errs = append(errs, fmt.Errorf("%s: resiliency groups: %w", dxGetResiliencyGroupOp, stopped))
	}
	for _, s := range groups[len(reads):] {
		reads = append(reads, dxGroupRead{group: dxGroupFromSummary(s)})
	}
	for i, r := range reads {
		id := sv(groups[i].ResiliencyGroupId)
		if r.err != nil {
			errs = append(errs, r.err)
		}
		// A group with no ARN is not stored, so it is warned about only for
		// that, not as stored without its tags or associations.
		arn := dxResiliencyGroupARN(r.group, groups[i])
		if arn == "" {
			noARN.skipped(id, nil)
			continue
		}
		if r.getDenied != nil {
			getDenied.skipped(id, r.getDenied)
		}
		if r.assocDenied != nil {
			assocDenied.skipped(id, r.assocDenied)
		}
		rows = append(rows, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeDirectConnectResiliencyGroup, NativeID: arn,
			Name: r.group.ResiliencyGroupName, Region: &region, Status: nonEmptyPtr(string(r.group.State)),
			AttributesJSON: mustJSON(dxResiliencyGroupAttrs{ResiliencyGroup: r.group, Associations: r.assocs}),
			TagsJSON:       dxTagsJSON(r.group.Tags), DiscoveredBy: scanID,
		})
	}
	return storeReadRows(st, acct, region, dxListResiliencyGroupsOp, rows, listErr,
		func() {
			listSkips.warn(st, dxListResiliencyGroupsOp, acct, region)
			warnNotStored(st, dxGetResiliencyGroupOp, acct, region, &noARN, "resiliency groups have no ResiliencyGroupArn in their detail or summary")
			getDenied.warnDeniedIn(st, dxGetResiliencyGroupOp, acct, region, "resiliency group(s) stored from their list summary were")
			assocDenied.warnDeniedIn(st, dxListResiliencyGroupAssociationsOp, acct, region, "resiliency group(s) stored with Associations null were")
		},
		errs...)
}

// errDXNoGroupBody fails a GetResiliencyGroup that succeeded without a group,
// so the group's tags and associations were not read. It is returned whether
// or not the summary held an ARN to store the group under: either way the
// read failed.
var errDXNoGroupBody = errors.New("GetResiliencyGroup answered without a resiliency group, so its tags and associations were not read; rescan, and report it if it recurs")

// dxGroupRead is one resiliency group's read: the group to store (the Get
// detail, or the summary's fields when the Get failed), its associations (nil
// when not read), the error of each denied call, and any other error, which
// the stored row's missing part is due to.
type dxGroupRead struct {
	group                  directconnecttypes.ResiliencyGroup
	assocs                 []directconnecttypes.ResiliencyGroupAssociation
	getDenied, assocDenied error
	err                    error
}

// readDXResiliencyGroup reads the detail and associations of the group s
// lists. The associations are read only for a group with an ARN to store it
// under.
func readDXResiliencyGroup(ctx context.Context, client dxAPI, s directconnecttypes.ResiliencyGroupSummary) dxGroupRead {
	id := sv(s.ResiliencyGroupId)
	read := dxGroupRead{group: dxGroupFromSummary(s)}
	out, err := client.GetResiliencyGroup(ctx, &directconnect.GetResiliencyGroupInput{ResiliencyGroupId: &id})
	switch {
	case isAccessDenied(err):
		read.getDenied = err
		return read
	case err != nil:
		read.err = fmt.Errorf("%s %s: %w", dxGetResiliencyGroupOp, id, err)
		return read
	case out.ResiliencyGroup == nil:
		read.err = fmt.Errorf("%s %s: %w", dxGetResiliencyGroupOp, id, errDXNoGroupBody)
		return read
	}
	read.group = *out.ResiliencyGroup
	if dxResiliencyGroupARN(read.group, s) == "" {
		return read
	}
	assocs, err := listDXResiliencyGroupAssociations(ctx, client, id)
	switch {
	case err == nil:
		read.assocs = assocs
	case isAccessDenied(err):
		read.assocDenied = err
	default:
		// A throttled or incomplete association read versions the row for
		// this scan rather than losing the group.
		read.err = fmt.Errorf("%s %s: %w", dxListResiliencyGroupAssociationsOp, id, err)
	}
	return read
}

// dxResiliencyGroupARN is the group's NativeID: the ARN of the detail or,
// when the detail lacks it, of the list summary, both the same group's
// ResiliencyGroupArn; "" when neither has one.
func dxResiliencyGroupARN(g directconnecttypes.ResiliencyGroup, s directconnecttypes.ResiliencyGroupSummary) string {
	return cmp.Or(sv(g.ResiliencyGroupArn), sv(s.ResiliencyGroupArn))
}

// dxGroupFromSummary is the detail a list summary carries: every field but
// the tags, which only GetResiliencyGroup returns.
func dxGroupFromSummary(s directconnecttypes.ResiliencyGroupSummary) directconnecttypes.ResiliencyGroup {
	return directconnecttypes.ResiliencyGroup{
		OwnerAccount: s.OwnerAccount, ResiliencyGroupArn: s.ResiliencyGroupArn, ResiliencyGroupId: s.ResiliencyGroupId,
		ResiliencyGroupName: s.ResiliencyGroupName, ResiliencyGroupType: s.ResiliencyGroupType, State: s.State,
	}
}

// listDXResiliencyGroupAssociations returns the group's associations sorted,
// [] when it has none, or nil with the error when the listing failed.
func listDXResiliencyGroupAssociations(ctx context.Context, client dxAPI, groupID string) ([]directconnecttypes.ResiliencyGroupAssociation, error) {
	assocs, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]directconnecttypes.ResiliencyGroupAssociation, *string, error) {
		out, err := client.ListResiliencyGroupAssociations(ctx, &directconnect.ListResiliencyGroupAssociationsInput{
			ResiliencyGroupId: &groupID, MaxResults: sdkaws.Int32(dxPageLimit), NextToken: token,
		})
		if err != nil {
			return nil, nil, err
		}
		return out.Items, out.NextToken, nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(assocs, func(a, b directconnecttypes.ResiliencyGroupAssociation) int {
		return cmp.Or(strings.Compare(sv(a.ConnectionArn), sv(b.ConnectionArn)), strings.Compare(string(a.State), string(b.State)))
	})
	if assocs == nil {
		assocs = []directconnecttypes.ResiliencyGroupAssociation{}
	}
	return assocs, nil
}
