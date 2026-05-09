package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/appmesh"
)

func init() {
	registerService(serviceEntry{
		name: "aws:appmesh",
		fn:   scanAppMesh,
		emits: []coverage.TypeDecl{
			{Service: "appmesh", DiscoType: TypeAppMeshMesh, Leaf: true},
			{Service: "appmesh", DiscoType: TypeAppMeshGatewayRoute},
			{Service: "appmesh", DiscoType: TypeAppMeshRoute},
			{Service: "appmesh", DiscoType: TypeAppMeshVirtualGateway},
			{Service: "appmesh", DiscoType: TypeAppMeshVirtualNode},
			{Service: "appmesh", DiscoType: TypeAppMeshVirtualRouter},
			{Service: "appmesh", DiscoType: TypeAppMeshVirtualService},
		},
	})
}

type appMeshAPI interface {
	ListMeshes(context.Context, *appmesh.ListMeshesInput, ...func(*appmesh.Options)) (*appmesh.ListMeshesOutput, error)
	ListVirtualGateways(context.Context, *appmesh.ListVirtualGatewaysInput, ...func(*appmesh.Options)) (*appmesh.ListVirtualGatewaysOutput, error)
	ListVirtualNodes(context.Context, *appmesh.ListVirtualNodesInput, ...func(*appmesh.Options)) (*appmesh.ListVirtualNodesOutput, error)
	ListVirtualRouters(context.Context, *appmesh.ListVirtualRoutersInput, ...func(*appmesh.Options)) (*appmesh.ListVirtualRoutersOutput, error)
	ListVirtualServices(context.Context, *appmesh.ListVirtualServicesInput, ...func(*appmesh.Options)) (*appmesh.ListVirtualServicesOutput, error)
	ListRoutes(context.Context, *appmesh.ListRoutesInput, ...func(*appmesh.Options)) (*appmesh.ListRoutesOutput, error)
	ListGatewayRoutes(context.Context, *appmesh.ListGatewayRoutesInput, ...func(*appmesh.Options)) (*appmesh.ListGatewayRoutesOutput, error)
}

// scanAppMesh discovers App Mesh meshes plus per-mesh virtual gateways,
// virtual nodes, virtual routers, virtual services, and the routes /
// gateway-routes that hang off virtual routers and virtual gateways.
func scanAppMesh(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := appmesh.NewFromConfig(acct.cfg, func(o *appmesh.Options) { o.Region = region })

	meshNames, t, i, ferr := scanAppMeshMeshes(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, mn := range meshNames {
		t, i, ferr = scanAppMeshVirtualGateways(ctx, client, acct, region, st, scanID, mn)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanAppMeshVirtualNodes(ctx, client, acct, region, st, scanID, mn)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		routerNames, tt, ii, rerr := scanAppMeshVirtualRouters(ctx, client, acct, region, st, scanID, mn)
		if rerr != nil {
			return total, inserted, rerr
		}
		total += tt
		inserted += ii

		t, i, ferr = scanAppMeshVirtualServices(ctx, client, acct, region, st, scanID, mn)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		gwNames, tt2, ii2, gerr := getAppMeshVirtualGatewayNames(ctx, client, mn)
		if gerr != nil {
			return total, inserted, gerr
		}
		total += tt2
		inserted += ii2

		for _, vr := range routerNames {
			t, i, ferr = scanAppMeshRoutes(ctx, client, acct, region, st, scanID, mn, vr)
			if ferr != nil {
				return total, inserted, ferr
			}
			total += t
			inserted += i
		}
		for _, vg := range gwNames {
			t, i, ferr = scanAppMeshGatewayRoutes(ctx, client, acct, region, st, scanID, mn, vg)
			if ferr != nil {
				return total, inserted, ferr
			}
			total += t
			inserted += i
		}
	}
	return total, inserted, nil
}

func scanAppMeshMeshes(ctx context.Context, client appMeshAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := appmesh.NewListMeshesPaginator(client, &appmesh.ListMeshesInput{})
	var batch []*store.Resource
	var names []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "appmesh:ListMeshes", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("appmesh:ListMeshes: %w", err)
		}
		for _, m := range out.Meshes {
			arn := sv(m.Arn)
			if arn == "" {
				continue
			}
			name := sv(m.MeshName)
			if name != "" {
				names = append(names, name)
			}
			label := name
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppMeshMesh, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "appmesh meshes")
	return names, t, i, err
}

func scanAppMeshVirtualGateways(ctx context.Context, client appMeshAPI, acct *account, region string, st *store.Store, scanID string, meshName string) (int, int, error) {
	mn := meshName
	pager := appmesh.NewListVirtualGatewaysPaginator(client, &appmesh.ListVirtualGatewaysInput{MeshName: &mn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "appmesh:ListVirtualGateways", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("appmesh:ListVirtualGateways: %w", err)
		}
		for _, g := range out.VirtualGateways {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppMeshVirtualGateway, NativeID: arn,
				Name: g.VirtualGatewayName, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "appmesh virtual-gateways")
}

func scanAppMeshVirtualNodes(ctx context.Context, client appMeshAPI, acct *account, region string, st *store.Store, scanID string, meshName string) (int, int, error) {
	mn := meshName
	pager := appmesh.NewListVirtualNodesPaginator(client, &appmesh.ListVirtualNodesInput{MeshName: &mn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "appmesh:ListVirtualNodes", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("appmesh:ListVirtualNodes: %w", err)
		}
		for _, n := range out.VirtualNodes {
			arn := sv(n.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppMeshVirtualNode, NativeID: arn,
				Name: n.VirtualNodeName, Region: &region,
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "appmesh virtual-nodes")
}

func scanAppMeshVirtualRouters(ctx context.Context, client appMeshAPI, acct *account, region string, st *store.Store, scanID string, meshName string) ([]string, int, int, error) {
	mn := meshName
	pager := appmesh.NewListVirtualRoutersPaginator(client, &appmesh.ListVirtualRoutersInput{MeshName: &mn})
	var batch []*store.Resource
	var names []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "appmesh:ListVirtualRouters", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("appmesh:ListVirtualRouters: %w", err)
		}
		for _, r := range out.VirtualRouters {
			arn := sv(r.Arn)
			if arn == "" {
				continue
			}
			if vr := sv(r.VirtualRouterName); vr != "" {
				names = append(names, vr)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppMeshVirtualRouter, NativeID: arn,
				Name: r.VirtualRouterName, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "appmesh virtual-routers")
	return names, t, i, err
}

func scanAppMeshVirtualServices(ctx context.Context, client appMeshAPI, acct *account, region string, st *store.Store, scanID string, meshName string) (int, int, error) {
	mn := meshName
	pager := appmesh.NewListVirtualServicesPaginator(client, &appmesh.ListVirtualServicesInput{MeshName: &mn})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "appmesh:ListVirtualServices", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("appmesh:ListVirtualServices: %w", err)
		}
		for _, s := range out.VirtualServices {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppMeshVirtualService, NativeID: arn,
				Name: s.VirtualServiceName, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "appmesh virtual-services")
}

// getAppMeshVirtualGatewayNames re-pages virtual gateways purely to collect
// their names for the gateway-route per-VG fan-out. Upserts already done in
// scanAppMeshVirtualGateways. Returns (names, total=0, inserted=0, err) so
// the orchestrator's totals stay accurate.
func getAppMeshVirtualGatewayNames(ctx context.Context, client appMeshAPI, meshName string) ([]string, int, int, error) {
	mn := meshName
	pager := appmesh.NewListVirtualGatewaysPaginator(client, &appmesh.ListVirtualGatewaysInput{MeshName: &mn})
	var names []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("appmesh:ListVirtualGateways(names): %w", err)
		}
		for _, g := range out.VirtualGateways {
			if n := sv(g.VirtualGatewayName); n != "" {
				names = append(names, n)
			}
		}
	}
	return names, 0, 0, nil
}

func scanAppMeshRoutes(ctx context.Context, client appMeshAPI, acct *account, region string, st *store.Store, scanID string, meshName, virtualRouterName string) (int, int, error) {
	mn, vr := meshName, virtualRouterName
	pager := appmesh.NewListRoutesPaginator(client, &appmesh.ListRoutesInput{MeshName: &mn, VirtualRouterName: &vr})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "appmesh:ListRoutes", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("appmesh:ListRoutes: %w", err)
		}
		for _, r := range out.Routes {
			arn := sv(r.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppMeshRoute, NativeID: arn,
				Name: r.RouteName, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "appmesh routes")
}

func scanAppMeshGatewayRoutes(ctx context.Context, client appMeshAPI, acct *account, region string, st *store.Store, scanID string, meshName, virtualGatewayName string) (int, int, error) {
	mn, vg := meshName, virtualGatewayName
	pager := appmesh.NewListGatewayRoutesPaginator(client, &appmesh.ListGatewayRoutesInput{MeshName: &mn, VirtualGatewayName: &vg})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "appmesh:ListGatewayRoutes", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("appmesh:ListGatewayRoutes: %w", err)
		}
		for _, r := range out.GatewayRoutes {
			arn := sv(r.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppMeshGatewayRoute, NativeID: arn,
				Name: r.GatewayRouteName, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "appmesh gateway-routes")
}
