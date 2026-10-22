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

// scanDirectConnect discovers DirectConnect resources. Connections, LAGs,
// interconnects, resiliency groups and VirtualInterfaces are regional.
// DirectConnectGateway and association objects are global; gated to us-east-1
// to avoid duplicate upserts across regions.
func scanDirectConnect(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := directconnect.NewFromConfig(acct.cfg, func(o *directconnect.Options) { o.Region = region })
	return scanDirectConnectAll(ctx, client, acct, region, st, scanID)
}

// scanDirectConnectAll runs the older phases, which stop at their first hard
// error, then the interconnect and resiliency-group phases, so a failure in one
// costs the others no rows. Each side's transient error is demoted to a warning
// only beside a hard error: joined, it would be buried in the hard error
// instead of reported under its op.
func scanDirectConnectAll(ctx context.Context, client dxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, err := scanDXOlderPhases(ctx, client, acct, region, st, scanID)
	ti, ii, ierr := scanDXInterconnects(ctx, client, acct, region, st, scanID)
	tr, ir, rerr := scanDXResiliencyGroups(ctx, client, acct, region, st, scanID)
	var errs phaseErrs
	errs.add("directconnect", err)
	errs.add(dxDescribeInterconnectsOp, ierr)
	errs.add(dxListResiliencyGroupsOp, rerr)
	return total + ti + tr, inserted + ii + ir, errs.join(st, acct, region)
}

// scanDXOlderPhases runs the phases that predate interconnects and resiliency
// groups, stopping at the first hard error.
func scanDXOlderPhases(ctx context.Context, client dxAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanDXConnections(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDXLags(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDXVirtualInterfaces(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}

	gwIDs, t, i, ferr := scanDXGateways(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanDXGatewayAssociations(ctx, client, acct, region, st, scanID, gwIDs)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	return total, inserted, nil
}

func scanDXConnections(ctx context.Context, client dxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.DescribeConnections(ctx, &directconnect.DescribeConnectionsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "directconnect:DescribeConnections", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("directconnect:DescribeConnections: %w", err)
	}
	var batch []*store.Resource
	for _, c := range out.Connections {
		id := sv(c.ConnectionId)
		if id == "" {
			continue
		}
		// Synth: arn:aws:directconnect:{r}:{a}:dxcon/{ConnectionId}.
		arn := fmt.Sprintf("arn:aws:directconnect:%s:%s:dxcon/%s", region, acct.ID, id)
		name := sv(c.ConnectionName)
		if name == "" {
			name = id
		}
		state := string(c.ConnectionState)
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeDirectConnectConnection, NativeID: arn,
			Name: &name, Region: &region, Status: &state,
			AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "directconnect connections")
}

func scanDXLags(ctx context.Context, client dxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.DescribeLags(ctx, &directconnect.DescribeLagsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "directconnect:DescribeLags", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("directconnect:DescribeLags: %w", err)
	}
	var batch []*store.Resource
	for _, l := range out.Lags {
		id := sv(l.LagId)
		if id == "" {
			continue
		}
		arn := fmt.Sprintf("arn:aws:directconnect:%s:%s:dxlag/%s", region, acct.ID, id)
		name := sv(l.LagName)
		if name == "" {
			name = id
		}
		state := string(l.LagState)
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypeDirectConnectLag, NativeID: arn,
			Name: &name, Region: &region, Status: &state,
			AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "directconnect lags")
}

// scanDXVirtualInterfaces fans out by VirtualInterfaceType ("private",
// "public", "transit") into three disco types matching the CFN type split.
func scanDXVirtualInterfaces(ctx context.Context, client dxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.DescribeVirtualInterfaces(ctx, &directconnect.DescribeVirtualInterfacesInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "directconnect:DescribeVirtualInterfaces", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("directconnect:DescribeVirtualInterfaces: %w", err)
	}
	var batch []*store.Resource
	for _, vi := range out.VirtualInterfaces {
		id := sv(vi.VirtualInterfaceId)
		if id == "" {
			continue
		}
		var dt string
		switch sv(vi.VirtualInterfaceType) {
		case "private":
			dt = TypeDirectConnectPrivateVirtualInterface
		case "public":
			dt = TypeDirectConnectPublicVirtualInterface
		case "transit":
			dt = TypeDirectConnectTransitVirtualInterface
		default:
			continue
		}
		arn := fmt.Sprintf("arn:aws:directconnect:%s:%s:dxvif/%s", region, acct.ID, id)
		name := sv(vi.VirtualInterfaceName)
		if name == "" {
			name = id
		}
		state := string(vi.VirtualInterfaceState)
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: dt, NativeID: arn,
			Name: &name, Region: &region, Status: &state,
			AttributesJSON: mustJSON(vi), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "directconnect virtual-interfaces")
}

// scanDXGateways lists DirectConnectGateway objects. Gateways are global;
// gate to us-east-1 to dedupe across multi-region scans.
func scanDXGateways(ctx context.Context, client dxAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	if region != "us-east-1" {
		return nil, 0, 0, nil
	}
	input := &directconnect.DescribeDirectConnectGatewaysInput{}
	var ids []string
	var batch []*store.Resource
	for {
		out, err := client.DescribeDirectConnectGateways(ctx, input)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "directconnect:DescribeDirectConnectGateways", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("directconnect:DescribeDirectConnectGateways: %w", err)
		}
		for _, g := range out.DirectConnectGateways {
			id := sv(g.DirectConnectGatewayId)
			if id == "" {
				continue
			}
			ids = append(ids, id)
			// DX gateway ARNs omit the region segment (global).
			arn := fmt.Sprintf("arn:aws:directconnect::%s:dx-gateway/%s", acct.ID, id)
			name := sv(g.DirectConnectGatewayName)
			if name == "" {
				name = id
			}
			state := string(g.DirectConnectGatewayState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDirectConnectDirectConnectGateway, NativeID: arn,
				Name: &name, Region: &region, Status: &state,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		input.NextToken = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "directconnect gateways")
	return ids, t, i, err
}

// scanDXGatewayAssociations requires DirectConnectGatewayId per call. Fan-out
// across gateway IDs enumerated by scanDXGateways.
func scanDXGatewayAssociations(ctx context.Context, client dxAPI, acct *account, region string, st *store.Store, scanID string, gwIDs []string) (int, int, error) {
	if region != "us-east-1" || len(gwIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, gw := range gwIDs {
		gwID := gw
		input := &directconnect.DescribeDirectConnectGatewayAssociationsInput{
			DirectConnectGatewayId: &gwID,
		}
		for {
			out, err := client.DescribeDirectConnectGatewayAssociations(ctx, input)
			if err != nil {
				if isAccessDenied(err) {
					return 0, 0, skipIfAccessDenied(st, "directconnect:DescribeDirectConnectGatewayAssociations", acct.ID, region, err)
				}
				return 0, 0, fmt.Errorf("directconnect:DescribeDirectConnectGatewayAssociations %s: %w", gwID, err)
			}
			for _, a := range out.DirectConnectGatewayAssociations {
				id := sv(a.AssociationId)
				if id == "" {
					continue
				}
				assocGwID := sv(a.DirectConnectGatewayId)
				arn := fmt.Sprintf("arn:aws:directconnect::%s:dx-gateway/%s/association/%s", acct.ID, assocGwID, id)
				label := id
				state := string(a.AssociationState)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeDirectConnectDirectConnectGatewayAssociation, NativeID: arn,
					Name: &label, Region: &region, Status: &state,
					AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			input.NextToken = out.NextToken
		}
	}
	return upsertBatch(st, batch, "directconnect gateway-associations")
}

const (
	dxDescribeInterconnectsOp           = "directconnect:DescribeInterconnects"
	dxListResiliencyGroupsOp            = "directconnect:ListResiliencyGroups"
	dxGetResiliencyGroupOp              = "directconnect:GetResiliencyGroup"
	dxListResiliencyGroupAssociationsOp = "directconnect:ListResiliencyGroupAssociations"
)

// dxPageLimit is the MaxResults cap the Direct Connect model documents for its
// paginated Describe and List ops.
const dxPageLimit = 100

// scanDXInterconnects stores the partner-owned interconnects of the region. An
// account that is not a Direct Connect partner owns none, and the model gives
// no distinct error for it, so any error but a denial is returned. A denial
// after a successful page keeps the rows already listed.
func scanDXInterconnects(ctx context.Context, client dxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	in := &directconnect.DescribeInterconnectsInput{MaxResults: sdkaws.Int32(dxPageLimit)}
	var (
		batch   []*store.Resource
		pageErr error
	)
	for {
		out, err := client.DescribeInterconnects(ctx, in)
		if err != nil {
			if !isAccessDenied(err) {
				return 0, 0, fmt.Errorf("%s: %w", dxDescribeInterconnectsOp, err)
			}
			_ = skipIfAccessDenied(st, dxDescribeInterconnectsOp, acct.ID, region, err)
			break
		}
		for _, ic := range out.Interconnects {
			id := sv(ic.InterconnectId)
			if id == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDirectConnectInterconnect, NativeID: dxInterconnectARN(region, acct.ID, id),
				Name: ic.InterconnectName, Region: &region, Status: nonEmptyPtr(string(ic.InterconnectState)),
				AttributesJSON: mustJSON(ic), DiscoveredBy: scanID,
			})
		}
		// DescribeInterconnects has no paginator; a repeated token would loop
		// forever.
		var done bool
		if done, pageErr = lastPage(in.NextToken, out.NextToken); done {
			break
		}
		in.NextToken = out.NextToken
	}
	return upsertListed(st, batch, dxDescribeInterconnectsOp, pageErr)
}

// dxResiliencyGroupAttrs is the group's detail with its connection
// associations, sorted because the API documents no order and a reordered
// list would otherwise version the group. Associations is null when listing
// them was denied or ended incomplete, and [] when the group has none.
type dxResiliencyGroupAttrs struct {
	directconnecttypes.ResiliencyGroup
	Associations []directconnecttypes.ResiliencyGroupAssociation
}

// scanDXResiliencyGroups stores the region's resiliency groups, each from its
// GetResiliencyGroup detail (the list summary lacks the tags) with its
// connection associations embedded: an association carries nothing but the
// connection ARN and its state, so it is an edge, not a resource. The model
// gives no not-found code for a group deleted since the list, so a per-group
// error other than a denial or a transient one fails the phase. Groups listed
// as deleted are not fetched.
//
// The group ARN has no region segment, like the dx-gateway ARN whose phase is
// gated to us-east-1, yet this phase scans every region: the model documents
// ListResiliencyGroups as listing "the resiliency groups owned by your Amazon
// Web Services account in the current Amazon Web Services Region". That is not
// yet confirmed against a live account; if one group were listed in two
// regions, its row would flip Region on every scan.
func scanDXResiliencyGroups(ctx context.Context, client dxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	ids, err := listDXResiliencyGroupIDs(ctx, client)
	if isAccessDenied(err) {
		return 0, 0, skipIfAccessDenied(st, dxListResiliencyGroupsOp, acct.ID, region, err)
	}
	// A repeated token returns the ids read before it: their groups are
	// stored and the listing error is returned with them.
	var listErr error
	if err != nil {
		listErr = fmt.Errorf("%s: %w", dxListResiliencyGroupsOp, err)
		if len(ids) == 0 {
			return 0, 0, listErr
		}
	}
	never := func(error) bool { return false }
	assocErrs := &fanOutErrs{st: st, acct: acct, region: region, op: dxListResiliencyGroupAssociationsOp, isSkip: never}
	var (
		getTransient, assocTransient transientTally
		assocIncomplete              incompleteLists
	)
	rows, err := describeFanOut(ctx, st, acct, region, dxGetResiliencyGroupOp, ids, never,
		func(ctx context.Context, id string) (*store.Resource, error) {
			out, err := client.GetResiliencyGroup(ctx, &directconnect.GetResiliencyGroupInput{ResiliencyGroupId: &id})
			if isTransientNetworkError(err) {
				getTransient.skipped(id, err)
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			g := out.ResiliencyGroup
			if g == nil || sv(g.ResiliencyGroupArn) == "" {
				return nil, nil
			}
			// A group whose associations failed transiently is skipped rather
			// than stored without them, which would version it.
			assocs, err := listDXResiliencyGroupAssociations(ctx, client, id)
			if isTransientNetworkError(err) {
				assocTransient.skipped(id, err)
				return nil, nil
			}
			// The listing returns no associations with an error, so a group
			// whose listing ended incomplete is stored with Associations null
			// (not read), never with part of them; that error is returned
			// after every group is stored.
			if err != nil && !assocIncomplete.take(dxListResiliencyGroupAssociationsOp, id, err) {
				if kerr := assocErrs.keyErr(id, err); kerr != nil {
					return nil, kerr
				}
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDirectConnectResiliencyGroup, NativeID: sv(g.ResiliencyGroupArn),
				Name: g.ResiliencyGroupName, Region: &region, Status: nonEmptyPtr(string(g.State)),
				AttributesJSON: mustJSON(dxResiliencyGroupAttrs{ResiliencyGroup: *g, Associations: assocs}),
				DiscoveredBy:   scanID,
			}, nil
		})
	getTransient.warnSkipped(st, dxGetResiliencyGroupOp, acct, region)
	assocTransient.warnSkipped(st, dxListResiliencyGroupAssociationsOp, acct, region)
	if err != nil {
		return 0, 0, errors.Join(listErr, assocIncomplete.err(), err)
	}
	total, inserted, uerr := upsertBatch(st, rows, "directconnect resiliency groups")
	return total, inserted, errors.Join(listErr, assocIncomplete.err(), uerr)
}

// listDXResiliencyGroupIDs returns the ids of the region's groups that are not
// deleted.
func listDXResiliencyGroupIDs(ctx context.Context, client dxAPI) ([]string, error) {
	in := &directconnect.ListResiliencyGroupsInput{MaxResults: sdkaws.Int32(dxPageLimit)}
	var ids []string
	for {
		out, err := client.ListResiliencyGroups(ctx, in)
		if err != nil {
			return nil, err
		}
		for _, g := range out.Items {
			id := sv(g.ResiliencyGroupId)
			if id == "" || g.State == directconnecttypes.ResiliencyGroupStateDeleted {
				continue
			}
			ids = append(ids, id)
		}
		// ListResiliencyGroups has no paginator; a repeated token would loop
		// forever.
		if done, err := lastPage(in.NextToken, out.NextToken); done {
			return ids, err
		}
		in.NextToken = out.NextToken
	}
}

func listDXResiliencyGroupAssociations(ctx context.Context, client dxAPI, groupID string) ([]directconnecttypes.ResiliencyGroupAssociation, error) {
	in := &directconnect.ListResiliencyGroupAssociationsInput{ResiliencyGroupId: &groupID, MaxResults: sdkaws.Int32(dxPageLimit)}
	assocs := []directconnecttypes.ResiliencyGroupAssociation{}
	for {
		out, err := client.ListResiliencyGroupAssociations(ctx, in)
		if err != nil {
			return nil, err
		}
		assocs = append(assocs, out.Items...)
		// ListResiliencyGroupAssociations has no paginator; a repeated token
		// would loop forever.
		done, err := lastPage(in.NextToken, out.NextToken)
		if err != nil {
			return nil, err
		}
		if done {
			break
		}
		in.NextToken = out.NextToken
	}
	slices.SortFunc(assocs, func(a, b directconnecttypes.ResiliencyGroupAssociation) int {
		return cmp.Or(strings.Compare(sv(a.ConnectionArn), sv(b.ConnectionArn)), strings.Compare(string(a.State), string(b.State)))
	})
	return assocs, nil
}
