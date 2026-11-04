package aws

import (
	"cmp"
	"context"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/pcs"
	pcstypes "github.com/aws/aws-sdk-go-v2/service/pcs/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypePCSCluster, Service: "pcs"})
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
	ListTagsForResource(context.Context, *pcs.ListTagsForResourceInput, ...func(*pcs.Options)) (*pcs.ListTagsForResourceOutput, error)
}

const (
	pcsTagsOp = "pcs:ListTagsForResource"
	// pcsPageCap is the maxResults sent to every listing read here, the
	// maximum of the model's MaxResults range (1 to 100).
	pcsPageCap = 100
)

// pcsNotFound is the not-found code ListTagsForResource models, and the one
// ListComputeNodeGroups and ListQueues model for a cluster deleted since it
// was listed.
var pcsNotFound = []string{"ResourceNotFoundException"}

// scanPCS discovers the AWS Parallel Computing Service clusters of one region
// and each cluster's compute node groups and queues. Every listing runs as
// its own phase, one at a time: a phase's error does not stop the later
// ones; a failed write and ctx's end do (runHostPhases). The child phases
// read the clusters stored this scan, those clusters concurrently. Every
// type has its tags read per row (pcsTags), and the tag denials of the whole
// scan are one warning.
func scanPCS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := pcs.NewFromConfig(acct.cfg, func(o *pcs.Options) { o.Region = region })
	tags := pcsTags(client).sharingDenials()
	var clusters []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "pcs", 0, []hostPhase{
		{op: "pcs:ListClusters", run: func(p *phaseTotals) (failed bool) {
			clusters, failed = scanPCSClusters(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "pcs:ListComputeNodeGroups", run: func(p *phaseTotals) bool {
			return scanPCSComputeNodeGroups(ctx, client, acct, region, st, scanID, clusters, tags, p)
		}},
		{op: "pcs:ListQueues", run: func(p *phaseTotals) bool {
			return scanPCSQueues(ctx, client, acct, region, st, scanID, clusters, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// pcsTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its list element carries. The op's Smithy model
// references the cluster, compute node group and queue resources, the
// Service Reference lists all three under TagResource with
// aws:ResourceTag, and each one's Create* input takes Tags. The op does not
// page. ResourceNotFoundException, its only modeled error, drops a resource
// deleted after it was listed.
func pcsTags(client pcsAPI) rowTagReader {
	return rowTagReader{
		op:       pcsTagsOp,
		notFound: pcsNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &pcs.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// pcsRow is the row of a listed element stored under the ARN it carries,
// with its name, status, creation time and the element verbatim as its
// attributes. An element without an ARN is not stored (arnRow); key names
// it in that warning.
func pcsRow(acct *account, region, scanID, typ, what string, arn, name *string, status string, created *time.Time, key string, element any) (*store.Resource, string, string) {
	return arnRow(what, arn, &key, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn), Name: name, Region: &region,
		Status: nonEmptyPtr(status), CreatedAt: tp(created),
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	})
}

// pcsChildKey names a cluster's element in a warning by the cluster id and
// the element's name.
func pcsChildKey(cluster string, name *string) string {
	return cluster + "/" + cmp.Or(sv(name), noIdentifier)
}

// scanPCSClusters stores the region's clusters with their tags, a page at a
// time, and returns the stored ones that have an Id, the parents of the
// child phases. It reports whether a write failed, after which the scan must
// stop.
func scanPCSClusters(ctx context.Context, client pcsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "pcs:ListClusters", "clusters", "Id",
		func(ctx context.Context, token *string) ([]pcstypes.ClusterSummary, *string, error) {
			out, err := client.ListClusters(ctx, &pcs.ListClustersInput{MaxResults: sdkaws.Int32(pcsPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Clusters, out.NextToken, nil
		},
		func(c pcstypes.ClusterSummary) (*store.Resource, string, string) {
			return pcsRow(acct, region, scanID, TypePCSCluster, "clusters", c.Arn, c.Name, string(c.Status), c.CreatedAt, sv(c.Name), c)
		},
		func(c pcstypes.ClusterSummary) string { return sv(c.Id) },
		tags, p)
}

// scanPCSComputeNodeGroups stores each cluster's compute node groups with
// their tags, each page written as soon as its tags are read; one cluster's
// failure costs the others none of their rows (storeEachParentPagesTags).
func scanPCSComputeNodeGroups(ctx context.Context, client pcsAPI, acct *account, region string, st *store.Store, scanID string, clusters []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeEachParentPagesTags(ctx, st, acct, region, "pcs:ListComputeNodeGroups", pcsNotFound, clusters,
		func(ctx context.Context, cluster string, token *string) ([]pcstypes.ComputeNodeGroupSummary, *string, error) {
			out, err := client.ListComputeNodeGroups(ctx, &pcs.ListComputeNodeGroupsInput{ClusterIdentifier: &cluster, MaxResults: sdkaws.Int32(pcsPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ComputeNodeGroups, out.NextToken, nil
		},
		func(cluster string, g pcstypes.ComputeNodeGroupSummary) (*store.Resource, string, string) {
			return pcsRow(acct, region, scanID, TypePCSComputeNodeGroup, "compute node groups", g.Arn, g.Name, string(g.Status), g.CreatedAt, pcsChildKey(cluster, g.Name), g)
		}, tags, p)
}

// scanPCSQueues stores each cluster's queues with their tags, as
// scanPCSComputeNodeGroups stores compute node groups.
func scanPCSQueues(ctx context.Context, client pcsAPI, acct *account, region string, st *store.Store, scanID string, clusters []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeEachParentPagesTags(ctx, st, acct, region, "pcs:ListQueues", pcsNotFound, clusters,
		func(ctx context.Context, cluster string, token *string) ([]pcstypes.QueueSummary, *string, error) {
			out, err := client.ListQueues(ctx, &pcs.ListQueuesInput{ClusterIdentifier: &cluster, MaxResults: sdkaws.Int32(pcsPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Queues, out.NextToken, nil
		},
		func(cluster string, q pcstypes.QueueSummary) (*store.Resource, string, string) {
			return pcsRow(acct, region, scanID, TypePCSQueue, "queues", q.Arn, q.Name, string(q.Status), q.CreatedAt, pcsChildKey(cluster, q.Name), q)
		}, tags, p)
}
