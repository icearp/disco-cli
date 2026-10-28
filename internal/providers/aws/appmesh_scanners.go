package aws

import (
	"cmp"
	"context"
	"encoding/json"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/appmesh"
	amtypes "github.com/aws/aws-sdk-go-v2/service/appmesh/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAppMeshMesh, Service: "appmesh"})
	registerType(restype.Descriptor{Type: TypeAppMeshGatewayRoute, Service: "appmesh"})
	registerType(restype.Descriptor{Type: TypeAppMeshRoute, Service: "appmesh"})
	registerType(restype.Descriptor{Type: TypeAppMeshVirtualGateway, Service: "appmesh"})
	registerType(restype.Descriptor{Type: TypeAppMeshVirtualNode, Service: "appmesh"})
	registerType(restype.Descriptor{Type: TypeAppMeshVirtualRouter, Service: "appmesh"})
	registerType(restype.Descriptor{Type: TypeAppMeshVirtualService, Service: "appmesh"})
	registerService(serviceEntry{
		name: "aws:appmesh",
		fn:   scanAppMesh,
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
	ListTagsForResource(context.Context, *appmesh.ListTagsForResourceInput, ...func(*appmesh.Options)) (*appmesh.ListTagsForResourceOutput, error)
}

const (
	amTagsOp = "appmesh:ListTagsForResource"
	// amPageCap is the limit maximum of every App Mesh listing read here and
	// of ListTagsForResource.
	amPageCap = 100
	// amNoIdentifier names, in a warning, a listed element with neither an
	// ARN nor a name.
	amNoIdentifier = "(an element with no identifier)"
)

// amGone is the not-found code ListTagsForResource models, and the one every
// per-mesh, per-router and per-gateway listing models for a parent deleted
// since it was listed.
var amGone = []string{"NotFoundException"}

// amParent is a stored mesh, virtual router or virtual gateway: its ARN, which
// its child listings are keyed by, the mesh name and mesh owner those
// listings are sent, and its own name (empty for a mesh).
type amParent struct{ arn, mesh, owner, name string }

// amParents is parents as the childParents their child listings key on, each
// by its ARN, and the parents by that ARN.
func amParents(parents []amParent) ([]childParent, map[string]amParent) {
	keys := make([]childParent, len(parents))
	byARN := make(map[string]amParent, len(parents))
	for i, p := range parents {
		keys[i] = childParent{id: p.arn, arn: p.arn}
		byARN[p.arn] = p
	}
	return keys, byARN
}

// scanAppMesh discovers the App Mesh resources of one region: the meshes,
// each mesh's virtual gateways, nodes, routers and services, each router's
// routes and each gateway's gateway routes. Every listing runs as its own
// phase, one at a time: a phase's error does not stop the later ones; a
// failed write and ctx's end do (runHostPhases). The child phases read the
// parents stored this scan, those parents concurrently. Every type has its
// tags read per row (amTags), whose denials are one warning for the whole
// scan.
//
// ListMeshes can also return meshes another account shares with this one
// through RAM. Every child listing is sent the MeshOwner the mesh was listed
// with, which the model documents as the account that shared the mesh when
// it is not this one. The child listing inputs take no mesh ARN: they name
// the mesh only by MeshName plus MeshOwner.
func scanAppMesh(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := appmesh.NewFromConfig(acct.cfg, func(o *appmesh.Options) { o.Region = region })
	tags := amTags(client, acct.ID).sharingDenials()
	var meshes, routers, gateways []amParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "appmesh", 0, []hostPhase{
		{op: "appmesh:ListMeshes", run: func(p *phaseTotals) (failed bool) {
			meshes, failed = scanAppMeshMeshes(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "appmesh:ListVirtualGateways", run: func(p *phaseTotals) (failed bool) {
			gateways, failed = scanAppMeshVirtualGateways(ctx, client, acct, region, st, scanID, meshes, tags, p)
			return failed
		}},
		{op: "appmesh:ListVirtualNodes", run: func(p *phaseTotals) bool {
			return scanAppMeshVirtualNodes(ctx, client, acct, region, st, scanID, meshes, tags, p)
		}},
		{op: "appmesh:ListVirtualRouters", run: func(p *phaseTotals) (failed bool) {
			routers, failed = scanAppMeshVirtualRouters(ctx, client, acct, region, st, scanID, meshes, tags, p)
			return failed
		}},
		{op: "appmesh:ListVirtualServices", run: func(p *phaseTotals) bool {
			return scanAppMeshVirtualServices(ctx, client, acct, region, st, scanID, meshes, tags, p)
		}},
		{op: "appmesh:ListRoutes", run: func(p *phaseTotals) bool {
			return scanAppMeshRoutes(ctx, client, acct, region, st, scanID, routers, tags, p)
		}},
		{op: "appmesh:ListGatewayRoutes", run: func(p *phaseTotals) bool {
			return scanAppMeshGatewayRoutes(ctx, client, acct, region, st, scanID, gateways, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// amTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its listing returned. The Service Reference lists every
// type read here under ListTagsForResource (gatewayRoute, mesh, route,
// virtualGateway, virtualNode, virtualRouter, virtualService). The op pages
// its tags; every page is read, and a repeated nextToken fails the read.
// NotFoundException, its modeled not-found, drops a resource deleted after it
// was listed.
//
// A row another account owns (amOwnedElsewhere) gives no tags and no error
// on a denial or not-found, so it is kept untagged without a warning. Whether
// App Mesh refuses such reads is unverified live.
func amTags(client appMeshAPI, acctID string) rowTagReader {
	tags := rowTagReader{
		op:       amTagsOp,
		notFound: amGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			tags, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]amtypes.TagRef, *string, error) {
				out, err := client.ListTagsForResource(ctx, &appmesh.ListTagsForResourceInput{ResourceArn: &arn, Limit: aws.Int32(amPageCap), NextToken: token})
				if err != nil {
					return nil, nil, err
				}
				return out.Tags, out.NextToken, nil
			})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(tags, func(t amtypes.TagRef) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
	own := tags.read
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		got, err := own(ctx, r)
		if (isAccessDenied(err) || isAPIErrorCode(err, amGone...)) && amOwnedElsewhere(r, acctID) {
			return nil, nil
		}
		return got, err
	}
	return tags
}

// amOwnedElsewhere reports whether the element r was built from names a
// ResourceOwner other than acctID. It reads ResourceOwner rather than the
// ARN's account (otherAccountGap) because a resource this account creates in
// a mesh another account shares may carry the mesh owner's account in its
// ARN (unverified live) while it is this account's to read. An element whose
// ResourceOwner is missing or unreadable counts as this account's own, so a
// failed read of it is reported rather than silenced.
func amOwnedElsewhere(r *store.Resource, acctID string) bool {
	var el struct{ ResourceOwner string }
	if err := json.Unmarshal([]byte(r.AttributesJSON), &el); err != nil {
		return false
	}
	return el.ResourceOwner != "" && el.ResourceOwner != acctID
}

// amRow is the row of a listed element stored under its ARN and named name.
// An element without an ARN is not stored: one empty NativeID would merge
// every such element into one resource. what names the elements in that
// warning, keyed by key.
func amRow(acct *account, region, scanID, typ, what string, arn, name *string, key string, element any) (*store.Resource, string, string) {
	a := sv(arn)
	if a == "" {
		return nil, what + " have no ARN", cmp.Or(key, amNoIdentifier)
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: a,
		Name: name, Region: &region, AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	}, "", ""
}

// amMeshKey names a mesh child in a warning by its mesh and its own name.
func amMeshKey(mesh, name *string) string {
	if sv(name) == "" {
		return ""
	}
	return sv(mesh) + "/" + sv(name)
}

// scanAppMeshMeshes stores the meshes with their tags and returns the stored
// ones that have a name, the parents of the per-mesh phases. Meshes are few
// (a per-account quota), so their listing is held whole. A mesh is named by
// its MeshName, or by its ARN when it has none. It reports whether the write
// failed, after which the scan must stop.
func scanAppMeshMeshes(ctx context.Context, client appMeshAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]amParent, bool) {
	listed := map[string]amParent{}
	stored, failed := storeParentListTags(ctx, st, acct, region, "appmesh:ListMeshes", "meshes", "MeshName",
		func(ctx context.Context, token *string) ([]amtypes.MeshRef, *string, error) {
			out, err := client.ListMeshes(ctx, &appmesh.ListMeshesInput{Limit: aws.Int32(amPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Meshes, out.NextToken, nil
		},
		func(m amtypes.MeshRef) (*store.Resource, string, string) {
			label := cmp.Or(sv(m.MeshName), sv(m.Arn))
			return amRow(acct, region, scanID, TypeAppMeshMesh, "meshes", m.Arn, &label, sv(m.MeshName), m)
		},
		func(m amtypes.MeshRef) string {
			if sv(m.MeshName) == "" {
				return ""
			}
			listed[sv(m.Arn)] = amParent{arn: sv(m.Arn), mesh: sv(m.MeshName), owner: sv(m.MeshOwner)}
			return sv(m.Arn)
		},
		tags, p)
	meshes := make([]amParent, len(stored))
	for i, s := range stored {
		meshes[i] = listed[s.arn]
	}
	return meshes, failed
}

// amStoredParents returns the stored rows that have a name as parents, each
// looked up in listed; the others are warned about, as stored without their
// children.
func amStoredParents(st *store.Store, acct *account, region, op, what, field string, stored []*store.Resource, listed map[*store.Resource]amParent) []amParent {
	var (
		parents []amParent
		noName  skipTally
	)
	for _, r := range stored {
		if p := listed[r]; p.name != "" {
			parents = append(parents, p)
		} else {
			noName.skipped(r.NativeID, nil)
		}
	}
	warnNoChildID(st, op, acct, region, &noName, what, field)
	return parents
}

// scanAppMeshVirtualGateways stores each mesh's virtual gateways with their
// tags and returns the stored ones that have a name, the parents of the
// gateway route phase. A per-mesh quota bounds them, so they are held
// until one write; one mesh's failure costs the others none of theirs.
func scanAppMeshVirtualGateways(ctx context.Context, client appMeshAPI, acct *account, region string, st *store.Store, scanID string, meshes []amParent, tags rowTagReader, p *phaseTotals) ([]amParent, bool) {
	const op = "appmesh:ListVirtualGateways"
	parents, byARN := amParents(meshes)
	listed := map[*store.Resource]amParent{}
	stored, failed := storeEachParentTags(ctx, st, acct, region, op, amGone, parents,
		func(ctx context.Context, meshARN string, token *string) ([]amtypes.VirtualGatewayRef, *string, error) {
			m := byARN[meshARN]
			out, err := client.ListVirtualGateways(ctx, &appmesh.ListVirtualGatewaysInput{MeshName: &m.mesh, MeshOwner: nonEmptyPtr(m.owner), Limit: aws.Int32(amPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.VirtualGateways, out.NextToken, nil
		},
		func(meshARN string, g amtypes.VirtualGatewayRef) (*store.Resource, string, string) {
			r, why, key := amRow(acct, region, scanID, TypeAppMeshVirtualGateway, "virtual gateways", g.Arn, g.VirtualGatewayName, amMeshKey(g.MeshName, g.VirtualGatewayName), g)
			if r != nil {
				m := byARN[meshARN]
				listed[r] = amParent{arn: r.NativeID, mesh: m.mesh, owner: m.owner, name: sv(g.VirtualGatewayName)}
			}
			return r, why, key
		},
		tags, p)
	if failed {
		return nil, true
	}
	return amStoredParents(st, acct, region, op, "virtual gateways", "VirtualGatewayName", stored, listed), false
}

// scanAppMeshVirtualRouters stores each mesh's virtual routers with their
// tags and returns the stored ones that have a name, the parents of the
// route phase. A per-mesh quota bounds them, so they are held until
// one write; one mesh's failure costs the others none of theirs.
func scanAppMeshVirtualRouters(ctx context.Context, client appMeshAPI, acct *account, region string, st *store.Store, scanID string, meshes []amParent, tags rowTagReader, p *phaseTotals) ([]amParent, bool) {
	const op = "appmesh:ListVirtualRouters"
	parents, byARN := amParents(meshes)
	listed := map[*store.Resource]amParent{}
	stored, failed := storeEachParentTags(ctx, st, acct, region, op, amGone, parents,
		func(ctx context.Context, meshARN string, token *string) ([]amtypes.VirtualRouterRef, *string, error) {
			m := byARN[meshARN]
			out, err := client.ListVirtualRouters(ctx, &appmesh.ListVirtualRoutersInput{MeshName: &m.mesh, MeshOwner: nonEmptyPtr(m.owner), Limit: aws.Int32(amPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.VirtualRouters, out.NextToken, nil
		},
		func(meshARN string, vr amtypes.VirtualRouterRef) (*store.Resource, string, string) {
			r, why, key := amRow(acct, region, scanID, TypeAppMeshVirtualRouter, "virtual routers", vr.Arn, vr.VirtualRouterName, amMeshKey(vr.MeshName, vr.VirtualRouterName), vr)
			if r != nil {
				m := byARN[meshARN]
				listed[r] = amParent{arn: r.NativeID, mesh: m.mesh, owner: m.owner, name: sv(vr.VirtualRouterName)}
			}
			return r, why, key
		},
		tags, p)
	if failed {
		return nil, true
	}
	return amStoredParents(st, acct, region, op, "virtual routers", "VirtualRouterName", stored, listed), false
}

// scanAppMeshVirtualNodes stores each mesh's virtual nodes with their tags. A
// mesh can hold hundreds, so each page is written as soon as its tags are
// read (storeEachParentPagesTags); one mesh's failure costs the others none
// of theirs.
func scanAppMeshVirtualNodes(ctx context.Context, client appMeshAPI, acct *account, region string, st *store.Store, scanID string, meshes []amParent, tags rowTagReader, p *phaseTotals) bool {
	parents, byARN := amParents(meshes)
	return storeEachParentPagesTags(ctx, st, acct, region, "appmesh:ListVirtualNodes", amGone, parents,
		func(ctx context.Context, meshARN string, token *string) ([]amtypes.VirtualNodeRef, *string, error) {
			m := byARN[meshARN]
			out, err := client.ListVirtualNodes(ctx, &appmesh.ListVirtualNodesInput{MeshName: &m.mesh, MeshOwner: nonEmptyPtr(m.owner), Limit: aws.Int32(amPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.VirtualNodes, out.NextToken, nil
		},
		func(_ string, n amtypes.VirtualNodeRef) (*store.Resource, string, string) {
			return amRow(acct, region, scanID, TypeAppMeshVirtualNode, "virtual nodes", n.Arn, n.VirtualNodeName, amMeshKey(n.MeshName, n.VirtualNodeName), n)
		},
		tags, p)
}

// scanAppMeshVirtualServices stores each mesh's virtual services with their
// tags. A mesh can hold hundreds, so each page is written as soon as its tags
// are read (storeEachParentPagesTags); one mesh's failure costs the others
// none of theirs.
func scanAppMeshVirtualServices(ctx context.Context, client appMeshAPI, acct *account, region string, st *store.Store, scanID string, meshes []amParent, tags rowTagReader, p *phaseTotals) bool {
	parents, byARN := amParents(meshes)
	return storeEachParentPagesTags(ctx, st, acct, region, "appmesh:ListVirtualServices", amGone, parents,
		func(ctx context.Context, meshARN string, token *string) ([]amtypes.VirtualServiceRef, *string, error) {
			m := byARN[meshARN]
			out, err := client.ListVirtualServices(ctx, &appmesh.ListVirtualServicesInput{MeshName: &m.mesh, MeshOwner: nonEmptyPtr(m.owner), Limit: aws.Int32(amPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.VirtualServices, out.NextToken, nil
		},
		func(_ string, s amtypes.VirtualServiceRef) (*store.Resource, string, string) {
			return amRow(acct, region, scanID, TypeAppMeshVirtualService, "virtual services", s.Arn, s.VirtualServiceName, amMeshKey(s.MeshName, s.VirtualServiceName), s)
		},
		tags, p)
}

// scanAppMeshRoutes stores each virtual router's routes with their tags. A
// mesh's routers together can hold thousands, so each page is written as soon
// as its tags are read (storeEachParentPagesTags); one router's failure costs
// the others none of theirs.
func scanAppMeshRoutes(ctx context.Context, client appMeshAPI, acct *account, region string, st *store.Store, scanID string, routers []amParent, tags rowTagReader, p *phaseTotals) bool {
	parents, byARN := amParents(routers)
	return storeEachParentPagesTags(ctx, st, acct, region, "appmesh:ListRoutes", amGone, parents,
		func(ctx context.Context, routerARN string, token *string) ([]amtypes.RouteRef, *string, error) {
			vr := byARN[routerARN]
			out, err := client.ListRoutes(ctx, &appmesh.ListRoutesInput{
				MeshName: &vr.mesh, MeshOwner: nonEmptyPtr(vr.owner), VirtualRouterName: &vr.name, Limit: aws.Int32(amPageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Routes, out.NextToken, nil
		},
		func(_ string, r amtypes.RouteRef) (*store.Resource, string, string) {
			return amRow(acct, region, scanID, TypeAppMeshRoute, "routes", r.Arn, r.RouteName, amMeshKey(r.MeshName, r.RouteName), r)
		},
		tags, p)
}

// scanAppMeshGatewayRoutes stores each virtual gateway's gateway routes with
// their tags, each page written as soon as its tags are read
// (storeEachParentPagesTags); one gateway's failure costs the others none of
// theirs.
func scanAppMeshGatewayRoutes(ctx context.Context, client appMeshAPI, acct *account, region string, st *store.Store, scanID string, gateways []amParent, tags rowTagReader, p *phaseTotals) bool {
	parents, byARN := amParents(gateways)
	return storeEachParentPagesTags(ctx, st, acct, region, "appmesh:ListGatewayRoutes", amGone, parents,
		func(ctx context.Context, gatewayARN string, token *string) ([]amtypes.GatewayRouteRef, *string, error) {
			vg := byARN[gatewayARN]
			out, err := client.ListGatewayRoutes(ctx, &appmesh.ListGatewayRoutesInput{
				MeshName: &vg.mesh, MeshOwner: nonEmptyPtr(vg.owner), VirtualGatewayName: &vg.name, Limit: aws.Int32(amPageCap), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.GatewayRoutes, out.NextToken, nil
		},
		func(_ string, r amtypes.GatewayRouteRef) (*store.Resource, string, string) {
			return amRow(acct, region, scanID, TypeAppMeshGatewayRoute, "gateway routes", r.Arn, r.GatewayRouteName, amMeshKey(r.MeshName, r.GatewayRouteName), r)
		},
		tags, p)
}
