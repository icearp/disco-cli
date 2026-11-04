package aws

import (
	"context"
	"errors"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/docdbelastic"
	docdbelastictypes "github.com/aws/aws-sdk-go-v2/service/docdbelastic/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDocDBElasticCluster, Service: "doc-db-elastic"})
	registerType(restype.Descriptor{Type: TypeDocDBElasticClusterSnapshot, Service: "doc-db-elastic"})
	registerService(serviceEntry{
		name: "aws:doc-db-elastic",
		fn:   scanDocDBElastic,
	})
}

type docDBElasticAPI interface {
	ListClusters(context.Context, *docdbelastic.ListClustersInput, ...func(*docdbelastic.Options)) (*docdbelastic.ListClustersOutput, error)
	GetCluster(context.Context, *docdbelastic.GetClusterInput, ...func(*docdbelastic.Options)) (*docdbelastic.GetClusterOutput, error)
	ListClusterSnapshots(context.Context, *docdbelastic.ListClusterSnapshotsInput, ...func(*docdbelastic.Options)) (*docdbelastic.ListClusterSnapshotsOutput, error)
	GetClusterSnapshot(context.Context, *docdbelastic.GetClusterSnapshotInput, ...func(*docdbelastic.Options)) (*docdbelastic.GetClusterSnapshotOutput, error)
	ListTagsForResource(context.Context, *docdbelastic.ListTagsForResourceInput, ...func(*docdbelastic.Options)) (*docdbelastic.ListTagsForResourceOutput, error)
}

const (
	docDBElasticListClustersOp  = "docdb-elastic:ListClusters"
	docDBElasticGetClusterOp    = "docdb-elastic:GetCluster"
	docDBElasticListSnapshotsOp = "docdb-elastic:ListClusterSnapshots"
	docDBElasticGetSnapshotOp   = "docdb-elastic:GetClusterSnapshot"
	docDBElasticTagsOp          = "docdb-elastic:ListTagsForResource"
	// docDBElasticPageCap is the MaxResults maximum the model gives both
	// listings (ListClusters 1-100, ListClusterSnapshots 20-100).
	docDBElasticPageCap = 100
)

// docDBElasticNotFound is the not-found code GetCluster, GetClusterSnapshot
// and ListTagsForResource model, for a resource deleted after it was listed.
var docDBElasticNotFound = []string{"ResourceNotFoundException"}

var (
	// errDocDBElasticNoCluster is a GetCluster response without the Cluster
	// its row is built from; the row keeps its list element.
	errDocDBElasticNoCluster = errors.New("docdb-elastic:GetCluster returned no Cluster")
	// errDocDBElasticNoSnapshot is a GetClusterSnapshot response without the
	// Snapshot its row is built from; the row keeps its list element.
	errDocDBElasticNoSnapshot = errors.New("docdb-elastic:GetClusterSnapshot returned no Snapshot")
)

// scanDocDBElastic discovers DocumentDB Elastic clusters and their snapshots,
// each with its tags (docDBElasticTags). The two listings run as phases, one
// at a time: a phase's error does not stop the other; a failed write and
// ctx's end do, and so does a clusters phase that stores nothing and fails
// only with NXDOMAIN, since the region does not serve the service
// (runHostPhases). Each listing is written a page at a time, and the tag
// reads' denials are one warning for the whole scan.
func scanDocDBElastic(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := docdbelastic.NewFromConfig(acct.cfg, func(o *docdbelastic.Options) { o.Region = region })
	tags := docDBElasticTags(client).sharingDenials()
	total, inserted, err := runHostPhases(ctx, st, acct, region, "docdb-elastic", 0, []hostPhase{
		{op: docDBElasticListClustersOp, run: func(p *phaseTotals) bool {
			return scanDocDBElasticClusters(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: docDBElasticListSnapshotsOp, run: func(p *phaseTotals) bool {
			return scanDocDBElasticSnapshots(ctx, client, acct, region, st, scanID, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// docDBElasticTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN the listing returned. The Service Reference lists cluster
// and cluster-snapshot on ListTagsForResource with aws:ResourceTag, and
// CreateCluster, CreateClusterSnapshot and CopyClusterSnapshot take Tags; no
// list element or Get body carries them. The op returns every tag at once.
// ResourceNotFoundException, its modeled not-found, drops a resource deleted
// after it was listed.
func docDBElasticTags(client docDBElasticAPI) rowTagReader {
	return rowTagReader{
		op:       docDBElasticTagsOp,
		notFound: docDBElasticNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &docdbelastic.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// scanDocDBElasticClusters stores the clusters a page at a time, each row's
// attributes replaced with its GetCluster body, which carries the KMS key,
// subnets and security groups the resolver reads. A cluster GetCluster no
// longer finds was deleted since the listing and is dropped; any other
// failure keeps the ClusterInList as the row (rowDetailReader). Name and
// Status come from the ClusterInList. It reports whether a write failed,
// after which the scan must stop.
func scanDocDBElasticClusters(ctx context.Context, client docDBElasticAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	steps := tokenPageSteps{details: rowDetailReader{
		op: docDBElasticGetClusterOp, what: "cluster(s)", notFound: docDBElasticNotFound,
		get: func(ctx context.Context, r *store.Resource) (any, error) {
			arn := r.NativeID
			out, err := client.GetCluster(ctx, &docdbelastic.GetClusterInput{ClusterArn: &arn})
			if err != nil {
				return nil, err
			}
			if out.Cluster == nil {
				return nil, errDocDBElasticNoCluster
			}
			return out.Cluster, nil
		},
	}}
	return storeTokenPagesTagsWith(ctx, st, acct, region, docDBElasticListClustersOp,
		func(ctx context.Context, token *string) ([]docdbelastictypes.ClusterInList, *string, error) {
			out, err := client.ListClusters(ctx, &docdbelastic.ListClustersInput{MaxResults: sdkaws.Int32(docDBElasticPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Clusters, out.NextToken, nil
		},
		func(c docdbelastictypes.ClusterInList) (*store.Resource, string, string) {
			status := string(c.Status)
			return arnRow("clusters", c.ClusterArn, c.ClusterName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDocDBElasticCluster, NativeID: sv(c.ClusterArn),
				Name: c.ClusterName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}, steps, tags, p)
}

// scanDocDBElasticSnapshots stores the cluster snapshots a page at a time,
// each row's attributes replaced with its GetClusterSnapshot body, which
// carries the KMS key, subnets and security groups the list element lacks.
// The listing sends no SnapshotType filter; that an unfiltered listing
// returns both automated and manual snapshots is unverified live.
//
// Automated snapshots are stored and tag-read like manual ones: AWS takes
// them of the customer's own cluster under its backup settings, so they are
// not provider-managed, and the Service Reference does not set them apart;
// whether ListTagsForResource serves them is unverified live (a refusal keeps
// the row untagged and is reported). The pinned SDK has no op that shares a
// snapshot with another account, so every listed snapshot is read as this
// account's own. A snapshot GetClusterSnapshot no longer finds was deleted
// since the listing and is dropped; any other failure keeps the
// ClusterSnapshotInList as the row (rowDetailReader). Name and Status come
// from the ClusterSnapshotInList. It reports whether a write failed, after
// which the scan must stop.
func scanDocDBElasticSnapshots(ctx context.Context, client docDBElasticAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	steps := tokenPageSteps{details: rowDetailReader{
		op: docDBElasticGetSnapshotOp, what: "cluster snapshot(s)", notFound: docDBElasticNotFound,
		get: func(ctx context.Context, r *store.Resource) (any, error) {
			arn := r.NativeID
			out, err := client.GetClusterSnapshot(ctx, &docdbelastic.GetClusterSnapshotInput{SnapshotArn: &arn})
			if err != nil {
				return nil, err
			}
			if out.Snapshot == nil {
				return nil, errDocDBElasticNoSnapshot
			}
			return out.Snapshot, nil
		},
	}}
	return storeTokenPagesTagsWith(ctx, st, acct, region, docDBElasticListSnapshotsOp,
		func(ctx context.Context, token *string) ([]docdbelastictypes.ClusterSnapshotInList, *string, error) {
			out, err := client.ListClusterSnapshots(ctx, &docdbelastic.ListClusterSnapshotsInput{MaxResults: sdkaws.Int32(docDBElasticPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Snapshots, out.NextToken, nil
		},
		func(s docdbelastictypes.ClusterSnapshotInList) (*store.Resource, string, string) {
			status := string(s.Status)
			return arnRow("cluster snapshots", s.SnapshotArn, s.SnapshotName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDocDBElasticClusterSnapshot, NativeID: sv(s.SnapshotArn),
				Name: s.SnapshotName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}, steps, tags, p)
}
