package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/pcs"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypePCSCluster, Service: "pcs", Leaf: true})
	registerType(restype.Descriptor{Type: TypePCSComputeNodeGroup, Service: "pcs"})
	registerType(restype.Descriptor{Type: TypePCSQueue, Service: "pcs"})
	registerService(serviceEntry{
		name: "aws:pcs",
		fn:   scanPCS,
	})
}

type pcsAPI interface {
	ListClusters(context.Context, *pcs.ListClustersInput, ...func(*pcs.Options)) (*pcs.ListClustersOutput, error)
	ListComputeNodeGroups(context.Context, *pcs.ListComputeNodeGroupsInput, ...func(*pcs.Options)) (*pcs.ListComputeNodeGroupsOutput, error)
	ListQueues(context.Context, *pcs.ListQueuesInput, ...func(*pcs.Options)) (*pcs.ListQueuesOutput, error)
}

// scanPCS discovers AWS Parallel Computing Service clusters and per-cluster
// compute node groups + queues. ARNs native on every type.
func scanPCS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := pcs.NewFromConfig(acct.cfg, func(o *pcs.Options) { o.Region = region })

	clusterIDs, t, i, ferr := scanPCSClusters(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, cid := range clusterIDs {
		t, i, ferr = scanPCSComputeNodeGroups(ctx, client, acct, region, st, scanID, cid)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanPCSQueues(ctx, client, acct, region, st, scanID, cid)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanPCSClusters(ctx context.Context, client pcsAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := pcs.NewListClustersPaginator(client, &pcs.ListClustersInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "pcs:ListClusters", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("pcs:ListClusters: %w", err)
		}
		for _, c := range out.Clusters {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			if id := sv(c.Id); id != "" {
				ids = append(ids, id)
			}
			status := string(c.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePCSCluster, NativeID: arn,
				Name: c.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "pcs clusters")
	return ids, t, i, err
}

func scanPCSComputeNodeGroups(ctx context.Context, client pcsAPI, acct *account, region string, st *store.Store, scanID string, clusterID string) (int, int, error) {
	cid := clusterID
	pager := pcs.NewListComputeNodeGroupsPaginator(client, &pcs.ListComputeNodeGroupsInput{ClusterIdentifier: &cid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "pcs:ListComputeNodeGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("pcs:ListComputeNodeGroups: %w", err)
		}
		for _, g := range out.ComputeNodeGroups {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			status := string(g.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePCSComputeNodeGroup, NativeID: arn,
				Name: g.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "pcs compute-node-groups")
}

func scanPCSQueues(ctx context.Context, client pcsAPI, acct *account, region string, st *store.Store, scanID string, clusterID string) (int, int, error) {
	cid := clusterID
	pager := pcs.NewListQueuesPaginator(client, &pcs.ListQueuesInput{ClusterIdentifier: &cid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "pcs:ListQueues", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("pcs:ListQueues: %w", err)
		}
		for _, q := range out.Queues {
			arn := sv(q.Arn)
			if arn == "" {
				continue
			}
			status := string(q.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePCSQueue, NativeID: arn,
				Name: q.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(q), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "pcs queues")
}
