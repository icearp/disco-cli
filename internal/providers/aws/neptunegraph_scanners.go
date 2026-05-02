package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/neptunegraph"
)

func init() {
	registerService(serviceEntry{
		name: "aws:neptune-graph",
		fn:   scanNeptuneGraph,
		emits: []coverage.TypeDecl{
			{Service: "neptune-graph", DiscoType: TypeNeptuneGraphGraph},
			{Service: "neptune-graph", DiscoType: TypeNeptuneGraphGraphSnapshot},
			{Service: "neptune-graph", DiscoType: TypeNeptuneGraphPrivateGraphEndpoint},
		},
	})
}

type neptuneGraphAPI interface {
	ListGraphs(context.Context, *neptunegraph.ListGraphsInput, ...func(*neptunegraph.Options)) (*neptunegraph.ListGraphsOutput, error)
	ListGraphSnapshots(context.Context, *neptunegraph.ListGraphSnapshotsInput, ...func(*neptunegraph.Options)) (*neptunegraph.ListGraphSnapshotsOutput, error)
	ListPrivateGraphEndpoints(context.Context, *neptunegraph.ListPrivateGraphEndpointsInput, ...func(*neptunegraph.Options)) (*neptunegraph.ListPrivateGraphEndpointsOutput, error)
}

// scanNeptuneGraph discovers Neptune Analytics graphs, snapshots, and
// per-graph private endpoints. Graph and Snapshot ARNs native; private
// endpoints synthesized as parent graph ARN + path.
func scanNeptuneGraph(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := neptunegraph.NewFromConfig(acct.cfg, func(o *neptunegraph.Options) { o.Region = region })

	graphs, t, i, ferr := scanNGGraphs(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanNGGraphSnapshots(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, g := range graphs {
		t, i, ferr = scanNGPrivateGraphEndpoints(ctx, client, acct, region, st, scanID, g.id, g.arn)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

type ngGraph struct{ id, arn string }

func scanNGGraphs(ctx context.Context, client neptuneGraphAPI, acct *account, region string, st *store.Store, scanID string) ([]ngGraph, int, int, error) {
	pager := neptunegraph.NewListGraphsPaginator(client, &neptunegraph.ListGraphsInput{})
	var batch []*store.Resource
	var graphs []ngGraph
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "neptunegraph:ListGraphs", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("neptunegraph:ListGraphs: %w", err)
		}
		for _, g := range out.Graphs {
			arn := sv(g.Arn)
			id := sv(g.Id)
			if arn == "" || id == "" {
				continue
			}
			graphs = append(graphs, ngGraph{id, arn})
			status := string(g.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNeptuneGraphGraph, NativeID: arn,
				Name: g.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "neptune-graph graphs")
	return graphs, t, i, err
}

func scanNGGraphSnapshots(ctx context.Context, client neptuneGraphAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := neptunegraph.NewListGraphSnapshotsPaginator(client, &neptunegraph.ListGraphSnapshotsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "neptunegraph:ListGraphSnapshots", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("neptunegraph:ListGraphSnapshots: %w", err)
		}
		for _, s := range out.GraphSnapshots {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			status := string(s.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNeptuneGraphGraphSnapshot, NativeID: arn,
				Name: s.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "neptune-graph graph-snapshots")
}

func scanNGPrivateGraphEndpoints(ctx context.Context, client neptuneGraphAPI, acct *account, region string, st *store.Store, scanID string, graphID, graphARN string) (int, int, error) {
	gid := graphID
	pager := neptunegraph.NewListPrivateGraphEndpointsPaginator(client, &neptunegraph.ListPrivateGraphEndpointsInput{GraphIdentifier: &gid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "neptunegraph:ListPrivateGraphEndpoints", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("neptunegraph:ListPrivateGraphEndpoints: %w", err)
		}
		for _, p := range out.PrivateGraphEndpoints {
			vid := sv(p.VpcEndpointId)
			if vid == "" {
				continue
			}
			arn := graphARN + "/private-graph-endpoint/" + vid
			label := vid
			status := string(p.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNeptuneGraphPrivateGraphEndpoint, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "neptune-graph private-graph-endpoints")
}
