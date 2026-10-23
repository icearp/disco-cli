package aws

import (
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dsql"
	dsqltypes "github.com/aws/aws-sdk-go-v2/service/dsql/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDSQLCluster, Service: "dsql"})
	registerType(restype.Descriptor{Type: TypeDSQLStream, Service: "dsql"})
	registerService(serviceEntry{
		name: "aws:dsql",
		fn:   scanDSQL,
	})
}

const (
	dsqlListClustersOp = "dsql:ListClusters"
	dsqlGetClusterOp   = "dsql:GetCluster"
	dsqlListStreamsOp  = "dsql:ListStreams"
)

// scanDSQL discovers Aurora DSQL clusters and their change-data-capture
// streams. A failed cluster phase still lists the streams of the clusters it
// stored, and one cluster's failed stream listing does not stop the others;
// only a store write failure stops the scan.
func scanDSQL(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := dsql.NewFromConfig(acct.cfg, func(o *dsql.Options) { o.Region = region })

	var phases phaseTotals
	t, i, clusters, cerr := scanDSQLClusters(ctx, client, acct, region, st, scanID)
	// A failed cluster write returns no clusters, so the loop below does not run.
	phases.addPhase(dsqlListClustersOp, t, i, cerr)
	for n, c := range clusters {
		if err := ctx.Err(); err != nil {
			phases.addPhase(dsqlListStreamsOp, 0, 0, fmt.Errorf("%s: %d of %d clusters not read: %w", dsqlListStreamsOp, len(clusters)-n, len(clusters), err))
			break
		}
		t, i, serr := scanDSQLStreams(ctx, client, acct, region, st, scanID, c.id)
		if phases.addPhase(dsqlListStreamsOp, t, i, serr) {
			break
		}
	}
	return phases.result(st, acct, region)
}

type dsqlAPI interface {
	ListClusters(context.Context, *dsql.ListClustersInput, ...func(*dsql.Options)) (*dsql.ListClustersOutput, error)
	GetCluster(context.Context, *dsql.GetClusterInput, ...func(*dsql.Options)) (*dsql.GetClusterOutput, error)
	ListStreams(context.Context, *dsql.ListStreamsInput, ...func(*dsql.Options)) (*dsql.ListStreamsOutput, error)
}

// dsqlListedCluster is a listed cluster and what its GetCluster returned. Both
// got and err are nil when GetCluster was not called because ctx ended.
type dsqlListedCluster struct {
	summary dsqltypes.ClusterSummary
	got     *dsql.GetClusterOutput
	err     error
}

// dsqlClusterReads tallies the GetCluster outcomes that keep or drop a listed
// cluster, so each is reported once after the write.
type dsqlClusterReads struct {
	denied  skipTally
	errs    []error
	stopped error
}

// scanDSQLClusters stores every listed cluster, built from its GetCluster
// body (the only source of its tags), and returns the stored clusters that
// carry an Identifier, the key ListStreams takes. Clusters read before a
// failed ListClusters page are kept. A cluster GetCluster no longer finds
// (ResourceNotFoundException, in the op's model) was deleted since it was
// listed and is skipped silently. A denied, failed or unread GetCluster stores the ListClusters
// element instead, a different shape that carries no tags: a denial is warned
// about once, any other error, a transient one included, is returned after the
// rows are stored.
func scanDSQLClusters(ctx context.Context, client dsqlAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	var reads dsqlClusterReads
	total, inserted, clusters, err := storeParentList(ctx, st, acct, region, dsqlListClustersOp, "clusters", "Identifier",
		func(ctx context.Context, token *string) ([]dsqlListedCluster, *string, error) {
			out, err := client.ListClusters(ctx, &dsql.ListClustersInput{NextToken: token, MaxResults: sdkaws.Int32(100)})
			if err != nil {
				return nil, nil, err
			}
			return readDSQLClusters(ctx, client, out, &reads)
		},
		func(c dsqlListedCluster) (*store.Resource, string, string) {
			return dsqlClusterRow(acct, region, scanID, c, &reads)
		},
		func(c dsqlListedCluster) string { return sv(c.summary.Identifier) })
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, clusters, err
	}
	reads.denied.warnDeniedIn(st, dsqlGetClusterOp, acct, region, "cluster(s) stored as their ListClusters element, without details or tags, because GetCluster was")
	return total, inserted, clusters, errors.Join(append([]error{err, reads.stopped}, reads.errs...)...)
}

// readDSQLClusters calls GetCluster for each cluster of one ListClusters page
// concurrently and drops the clusters it no longer finds. Once ctx ends, the
// clusters not yet read are returned without a GetCluster result, the stop is
// recorded in reads, and no further page is requested.
func readDSQLClusters(ctx context.Context, client dsqlAPI, out *dsql.ListClustersOutput, reads *dsqlClusterReads) ([]dsqlListedCluster, *string, error) {
	read, stop := readEachKey(ctx, out.Clusters, func(ctx context.Context, c dsqltypes.ClusterSummary) dsqlListedCluster {
		g, err := client.GetCluster(ctx, &dsql.GetClusterInput{Identifier: c.Identifier})
		return dsqlListedCluster{summary: c, got: g, err: err}
	})
	got := make([]dsqlListedCluster, 0, len(out.Clusters))
	for _, c := range read {
		if !isAPIErrorCode(c.err, "ResourceNotFoundException") {
			got = append(got, c)
		}
	}
	if stop == nil {
		return got, out.NextToken, nil
	}
	for _, c := range out.Clusters[len(read):] {
		got = append(got, dsqlListedCluster{summary: c})
	}
	reads.stopped = fmt.Errorf("%s: %w", dsqlGetClusterOp, stop)
	if sv(out.NextToken) != "" {
		reads.stopped = fmt.Errorf("%w; the later %s pages were not listed", reads.stopped, dsqlListClustersOp)
	}
	return got, nil, nil
}

// dsqlClusterRow builds a listed cluster's row, as scanDSQLClusters says.
func dsqlClusterRow(acct *account, region, scanID string, c dsqlListedCluster, reads *dsqlClusterReads) (*store.Resource, string, string) {
	arn, label := sv(c.summary.Arn), sv(c.summary.Identifier)
	if arn == "" {
		return nil, "clusters have an empty Arn", label
	}
	attrsJSON := mustJSON(c.summary)
	var (
		tags   map[string]string
		status *string
	)
	switch err := c.err; {
	case c.got != nil:
		attrsJSON, tags, status = mustJSON(c.got), c.got.Tags, nonEmptyPtr(string(c.got.Status))
	case err == nil:
	case isAccessDenied(err):
		reads.denied.skipped(arn, err)
	default:
		reads.errs = append(reads.errs, fmt.Errorf("%s %s: %w", dsqlGetClusterOp, label, err))
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeDSQLCluster, NativeID: arn,
		Name: &label, Region: &region, Status: status,
		AttributesJSON: attrsJSON, TagsJSON: mapTagsJSON(tags), DiscoveredBy: scanID,
	}, "", ""
}

func scanDSQLStreams(ctx context.Context, client dsqlAPI, acct *account, region string, st *store.Store, scanID, clusterID string) (int, int, error) {
	var batch []*store.Resource
	pager := dsql.NewListStreamsPaginator(client, &dsql.ListStreamsInput{ClusterIdentifier: &clusterID})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "dsql:ListStreams", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("dsql:ListStreams: %w", err)
		}
		for _, s := range out.Streams {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			status := string(s.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDSQLStream, NativeID: arn,
				Name: s.StreamIdentifier, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "dsql streams")
}
