package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/directconnect"
)

func init() {
	registerService(serviceEntry{
		name: "aws:directconnect",
		fn:   scanDirectConnect,
		emits: []coverage.TypeDecl{
			{Service: "directconnect", DiscoType: TypeDirectConnectConnection},
			{Service: "directconnect", DiscoType: TypeDirectConnectDirectConnectGateway, Leaf: true},
			{Service: "directconnect", DiscoType: TypeDirectConnectDirectConnectGatewayAssociation},
			{Service: "directconnect", DiscoType: TypeDirectConnectLag, Leaf: true},
			{Service: "directconnect", DiscoType: TypeDirectConnectPrivateVirtualInterface},
			{Service: "directconnect", DiscoType: TypeDirectConnectPublicVirtualInterface},
			{Service: "directconnect", DiscoType: TypeDirectConnectTransitVirtualInterface},
		},
	})
}

type dxAPI interface {
	DescribeConnections(context.Context, *directconnect.DescribeConnectionsInput, ...func(*directconnect.Options)) (*directconnect.DescribeConnectionsOutput, error)
	DescribeDirectConnectGateways(context.Context, *directconnect.DescribeDirectConnectGatewaysInput, ...func(*directconnect.Options)) (*directconnect.DescribeDirectConnectGatewaysOutput, error)
	DescribeDirectConnectGatewayAssociations(context.Context, *directconnect.DescribeDirectConnectGatewayAssociationsInput, ...func(*directconnect.Options)) (*directconnect.DescribeDirectConnectGatewayAssociationsOutput, error)
	DescribeLags(context.Context, *directconnect.DescribeLagsInput, ...func(*directconnect.Options)) (*directconnect.DescribeLagsOutput, error)
	DescribeVirtualInterfaces(context.Context, *directconnect.DescribeVirtualInterfacesInput, ...func(*directconnect.Options)) (*directconnect.DescribeVirtualInterfacesOutput, error)
}

// scanDirectConnect discovers DirectConnect resources. Connections, LAGs, and
// VirtualInterfaces are regional. DirectConnectGateway and association
// objects are global; gated to us-east-1 to avoid duplicate upserts across
// regions. APIs return no NextToken — single-call listings.
func scanDirectConnect(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := directconnect.NewFromConfig(acct.cfg, func(o *directconnect.Options) { o.Region = region })

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
