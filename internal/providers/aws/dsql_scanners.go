package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/dsql"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDSQLCluster, Service: "dsql", Upstream: "AWS::DSQL::Cluster"})
	registerType(restype.Descriptor{Type: TypeDSQLStream, Service: "dsql"})
	registerService(serviceEntry{
		name: "aws:dsql",
		fn:   scanDSQL,
	})
}

// scanDSQL discovers Aurora DSQL clusters and their change-data-capture streams.
func scanDSQL(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := dsql.NewFromConfig(acct.cfg, func(o *dsql.Options) { o.Region = region })

	clusterIDs, t, i, cerr := scanDSQLClusters(ctx, client, acct, region, st, scanID)
	if cerr != nil {
		return total, inserted, cerr
	}
	total += t
	inserted += i

	for _, cid := range clusterIDs {
		t, i, serr := scanDSQLStreams(ctx, client, acct, region, st, scanID, cid)
		if serr != nil {
			return total, inserted, serr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

type dsqlAPI interface {
	ListClusters(context.Context, *dsql.ListClustersInput, ...func(*dsql.Options)) (*dsql.ListClustersOutput, error)
	GetCluster(context.Context, *dsql.GetClusterInput, ...func(*dsql.Options)) (*dsql.GetClusterOutput, error)
	ListStreams(context.Context, *dsql.ListStreamsInput, ...func(*dsql.Options)) (*dsql.ListStreamsOutput, error)
}

func scanDSQLClusters(ctx context.Context, client dsqlAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var batch []*store.Resource
	var ids []string
	var nextToken *string
	for {
		out, err := client.ListClusters(ctx, &dsql.ListClustersInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "dsql:ListClusters", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("dsql:ListClusters: %w", err)
		}
		for _, c := range out.Clusters {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			ids = append(ids, sv(c.Identifier))
			label := sv(c.Identifier)
			attrsJSON := mustJSON(c)
			if gout, gerr := client.GetCluster(ctx, &dsql.GetClusterInput{Identifier: c.Identifier}); gerr == nil {
				attrsJSON = mustJSON(gout)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDSQLCluster, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: attrsJSON, DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "dsql clusters")
	return ids, t, i, err
}

func scanDSQLStreams(ctx context.Context, client dsqlAPI, acct *account, region string, st *store.Store, scanID, clusterID string) (int, int, error) {
	if clusterID == "" {
		return 0, 0, nil
	}
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
