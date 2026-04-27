package aws

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "aws:rds", fn: scanRDS}) }

// rdsARN constructs a standard RDS ARN. RDS uses ":" as the resource separator
// (e.g. arn:aws:rds:us-east-1:123456789012:cluster:my-cluster), unlike EC2
// which uses "/".
func rdsARN(region, accountID, resource, id string) string {
	return fmt.Sprintf("arn:aws:rds:%s:%s:%s:%s", region, accountID, resource, id)
}

// rdsPager is satisfied by every AWS SDK v2 RDS paginator.
type rdsPager[P any] interface {
	HasMorePages() bool
	NextPage(context.Context, ...func(*rds.Options)) (P, error)
}

// rdsPageScan runs a paginated RDS Describe call, converts each page to a
// batch of resources via toResources, and upserts the batch. Access-denied
// errors are skipped via skipIfAccessDenied.
func rdsPageScan[P any](
	ctx context.Context,
	iamAction string,
	acct *account,
	region string,
	st *store.Store,
	pager rdsPager[P],
	toResources func(P) []*store.Resource,
) (total, inserted int, err error) {
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, iamAction, acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("%s: %w", iamAction, err)
		}
		if batch := toResources(page); len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert %s: %w", iamAction, err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// scanRDS discovers all RDS resources in one region concurrently.
func scanRDS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := rds.NewFromConfig(acct.cfg, func(o *rds.Options) { o.Region = region })
	return runScanners(ctx,
		func(ctx context.Context) (int, int, error) {
			return scanDBInstances(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanDBClusters(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanDBClusterParameterGroups(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanDBParameterGroups(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanDBProxies(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanDBProxyEndpoints(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanDBProxyTargetGroups(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanDBSecurityGroups(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanDBShardGroups(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanDBSubnetGroups(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanEventSubscriptions(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanGlobalClusters(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanIntegrations(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanOptionGroups(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanCustomDBEngineVersions(ctx, client, acct, region, st, scanID)
		},
	)
}

// nonRDSEngines covers engines that ride on the rds:Describe* APIs but
// have their own dedicated SDK service + disco type. Filter these out
// of the RDS scanner so each engine row lands under exactly one type
// (and one scanner owns its resolvers). Kept narrow: docdb is here for
// safety even though rds:DescribeDBClusters does NOT return docdb in
// practice (per docdb's own dedicated CreateDBCluster.Engine valid
// values); the filter is cheap and guards against AWS later choosing
// to surface docdb via the shared API.
var nonRDSEngines = map[string]bool{
	"neptune": true,
	"docdb":   true,
}

func scanDBInstances(ctx context.Context, client *rds.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(ctx, "rds:DescribeDBInstances", acct, region, st,
		rds.NewDescribeDBInstancesPaginator(client, &rds.DescribeDBInstancesInput{}),
		func(page *rds.DescribeDBInstancesOutput) []*store.Resource {
			var out []*store.Resource
			for _, db := range page.DBInstances {
				if nonRDSEngines[sv(db.Engine)] {
					continue
				}
				name := sv(db.DBInstanceIdentifier)
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeRDSDBInstance,
					NativeID:       sv(db.DBInstanceArn),
					Name:           &name,
					Region:         &region,
					Zone:           db.AvailabilityZone,
					CreatedAt:      tp(db.InstanceCreateTime),
					Status:         db.DBInstanceStatus,
					TagsJSON:       awsTagsJSON(db.TagList),
					AttributesJSON: mustJSON(db), DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

func scanDBClusters(ctx context.Context, client *rds.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(ctx, "rds:DescribeDBClusters", acct, region, st,
		rds.NewDescribeDBClustersPaginator(client, &rds.DescribeDBClustersInput{}),
		func(page *rds.DescribeDBClustersOutput) []*store.Resource {
			var out []*store.Resource
			for _, c := range page.DBClusters {
				if nonRDSEngines[sv(c.Engine)] {
					continue
				}
				name := sv(c.DBClusterIdentifier)
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSDBCluster, NativeID: sv(c.DBClusterArn), Name: &name,
					Region: &region, CreatedAt: tp(c.ClusterCreateTime), Status: c.Status,
					TagsJSON: awsTagsJSON(c.TagList), AttributesJSON: mustJSON(c),
					DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

func scanDBClusterParameterGroups(ctx context.Context, client *rds.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(ctx, "rds:DescribeDBClusterParameterGroups", acct, region, st,
		rds.NewDescribeDBClusterParameterGroupsPaginator(client, &rds.DescribeDBClusterParameterGroupsInput{}),
		func(page *rds.DescribeDBClusterParameterGroupsOutput) []*store.Resource {
			var out []*store.Resource
			for _, pg := range page.DBClusterParameterGroups {
				name := sv(pg.DBClusterParameterGroupName)
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSDBClusterParameterGroup, NativeID: sv(pg.DBClusterParameterGroupArn),
					Name: &name, Region: &region, AttributesJSON: mustJSON(pg),
					DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

func scanDBParameterGroups(ctx context.Context, client *rds.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(ctx, "rds:DescribeDBParameterGroups", acct, region, st,
		rds.NewDescribeDBParameterGroupsPaginator(client, &rds.DescribeDBParameterGroupsInput{}),
		func(page *rds.DescribeDBParameterGroupsOutput) []*store.Resource {
			var out []*store.Resource
			for _, pg := range page.DBParameterGroups {
				name := sv(pg.DBParameterGroupName)
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSDBParameterGroup, NativeID: sv(pg.DBParameterGroupArn),
					Name: &name, Region: &region, AttributesJSON: mustJSON(pg),
					DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

func scanDBProxies(ctx context.Context, client *rds.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(ctx, "rds:DescribeDBProxies", acct, region, st,
		rds.NewDescribeDBProxiesPaginator(client, &rds.DescribeDBProxiesInput{}),
		func(page *rds.DescribeDBProxiesOutput) []*store.Resource {
			var out []*store.Resource
			for _, p := range page.DBProxies {
				name := sv(p.DBProxyName)
				status := string(p.Status)
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSDBProxy, NativeID: sv(p.DBProxyArn), Name: &name,
					Region: &region, CreatedAt: tp(p.CreatedDate), Status: &status,
					AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

func scanDBProxyEndpoints(ctx context.Context, client *rds.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(ctx, "rds:DescribeDBProxyEndpoints", acct, region, st,
		rds.NewDescribeDBProxyEndpointsPaginator(client, &rds.DescribeDBProxyEndpointsInput{}),
		func(page *rds.DescribeDBProxyEndpointsOutput) []*store.Resource {
			var out []*store.Resource
			for _, ep := range page.DBProxyEndpoints {
				name := sv(ep.DBProxyEndpointName)
				status := string(ep.Status)
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSDBProxyEndpoint, NativeID: sv(ep.DBProxyEndpointArn),
					Name: &name, Region: &region, Status: &status,
					AttributesJSON: mustJSON(ep), DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

// scanDBProxyTargetGroups discovers RDS DB proxy target groups.
// DescribeDBProxyTargetGroups requires a DBProxyName, so we first collect all
// proxy names, then query target groups per proxy concurrently.
func scanDBProxyTargetGroups(ctx context.Context, client *rds.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var proxyNames []string
	// Collect proxy names — the toResources function returns nil so no upsert occurs.
	if _, _, err := rdsPageScan(ctx, "rds:DescribeDBProxies (for target groups)", acct, region, st,
		rds.NewDescribeDBProxiesPaginator(client, &rds.DescribeDBProxiesInput{}),
		func(page *rds.DescribeDBProxiesOutput) []*store.Resource {
			for _, p := range page.DBProxies {
				proxyNames = append(proxyNames, sv(p.DBProxyName))
			}
			return nil // only collecting names, no upsert needed
		},
	); err != nil {
		return 0, 0, err
	}
	if len(proxyNames) == 0 {
		return
	}

	// Collect target groups per proxy concurrently, then upsert as one batch.
	var mu sync.Mutex
	var batch []*store.Resource
	g, ctx := errgroup.WithContext(ctx)
	for _, name := range proxyNames {
		proxyName := name
		g.Go(func() error {
			// toResources returns nil; resources are collected via closure.
			_, _, err := rdsPageScan(ctx, "rds:DescribeDBProxyTargetGroups", acct, region, st,
				rds.NewDescribeDBProxyTargetGroupsPaginator(client, &rds.DescribeDBProxyTargetGroupsInput{
					DBProxyName: &proxyName,
				}),
				func(page *rds.DescribeDBProxyTargetGroupsOutput) []*store.Resource {
					var out []*store.Resource
					for _, tg := range page.TargetGroups {
						tgName := sv(tg.TargetGroupName)
						out = append(out, &store.Resource{
							Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
							Type: TypeRDSDBProxyTargetGroup, NativeID: sv(tg.TargetGroupArn),
							Name: &tgName, Region: &region, Status: tg.Status,
							AttributesJSON: mustJSON(tg), DiscoveredBy: scanID,
						})
					}
					mu.Lock()
					batch = append(batch, out...)
					mu.Unlock()
					return nil // collected into batch above; skip built-in upsert
				},
			)
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert rds:DescribeDBProxyTargetGroups: %w", err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

func scanDBSecurityGroups(ctx context.Context, client *rds.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(ctx, "rds:DescribeDBSecurityGroups", acct, region, st,
		rds.NewDescribeDBSecurityGroupsPaginator(client, &rds.DescribeDBSecurityGroupsInput{}),
		func(page *rds.DescribeDBSecurityGroupsOutput) []*store.Resource {
			var out []*store.Resource
			for _, sg := range page.DBSecurityGroups {
				name := sv(sg.DBSecurityGroupName)
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSDBSecurityGroup, NativeID: sv(sg.DBSecurityGroupArn),
					Name: &name, Region: &region, AttributesJSON: mustJSON(sg),
					DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

// scanDBShardGroups discovers RDS DB shard groups (Aurora Limitless).
// The SDK has no paginator for this API, so we paginate manually via Marker.
func scanDBShardGroups(ctx context.Context, client *rds.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var marker *string
	for {
		out, apiErr := client.DescribeDBShardGroups(ctx, &rds.DescribeDBShardGroupsInput{Marker: marker})
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return total, inserted, skipIfAccessDenied(st, "rds:DescribeDBShardGroups", acct.ID, region, apiErr)
			}
			return total, inserted, fmt.Errorf("rds:DescribeDBShardGroups: %w", apiErr)
		}
		var batch []*store.Resource
		for _, sg := range out.DBShardGroups {
			name := sv(sg.DBShardGroupIdentifier)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRDSDBShardGroup, NativeID: sv(sg.DBShardGroupArn),
				Name: &name, Region: &region, Status: sg.Status,
				TagsJSON: awsTagsJSON(sg.TagList), AttributesJSON: mustJSON(sg),
				DiscoveredBy: scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert rds:DescribeDBShardGroups: %w", err)
			}
			total += len(batch)
			inserted += n
		}
		if out.Marker == nil {
			return total, inserted, nil
		}
		marker = out.Marker
	}
}

func scanDBSubnetGroups(ctx context.Context, client *rds.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(ctx, "rds:DescribeDBSubnetGroups", acct, region, st,
		rds.NewDescribeDBSubnetGroupsPaginator(client, &rds.DescribeDBSubnetGroupsInput{}),
		func(page *rds.DescribeDBSubnetGroupsOutput) []*store.Resource {
			var out []*store.Resource
			for _, sg := range page.DBSubnetGroups {
				name := sv(sg.DBSubnetGroupName)
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSDBSubnetGroup, NativeID: sv(sg.DBSubnetGroupArn),
					Name: &name, Region: &region, Status: sg.SubnetGroupStatus,
					AttributesJSON: mustJSON(sg), DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

func scanEventSubscriptions(ctx context.Context, client *rds.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(ctx, "rds:DescribeEventSubscriptions", acct, region, st,
		rds.NewDescribeEventSubscriptionsPaginator(client, &rds.DescribeEventSubscriptionsInput{}),
		func(page *rds.DescribeEventSubscriptionsOutput) []*store.Resource {
			var out []*store.Resource
			for _, sub := range page.EventSubscriptionsList {
				name := sv(sub.CustSubscriptionId)
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSEventSubscription, NativeID: sv(sub.EventSubscriptionArn),
					Name: &name, Region: &region, Status: sub.Status,
					AttributesJSON: mustJSON(sub), DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

// scanGlobalClusters discovers RDS global clusters (Aurora Global Database).
// Global clusters are not region-scoped; this is called per-region but
// UpsertResources deduplicates by NativeID (ARN contains no region).
func scanGlobalClusters(ctx context.Context, client *rds.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(ctx, "rds:DescribeGlobalClusters", acct, region, st,
		rds.NewDescribeGlobalClustersPaginator(client, &rds.DescribeGlobalClustersInput{}),
		func(page *rds.DescribeGlobalClustersOutput) []*store.Resource {
			var out []*store.Resource
			for _, gc := range page.GlobalClusters {
				name := sv(gc.GlobalClusterIdentifier)
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSGlobalCluster, NativeID: sv(gc.GlobalClusterArn),
					Name: &name, Status: gc.Status, TagsJSON: awsTagsJSON(gc.TagList),
					AttributesJSON: mustJSON(gc), DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

func scanIntegrations(ctx context.Context, client *rds.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(ctx, "rds:DescribeIntegrations", acct, region, st,
		rds.NewDescribeIntegrationsPaginator(client, &rds.DescribeIntegrationsInput{}),
		func(page *rds.DescribeIntegrationsOutput) []*store.Resource {
			var out []*store.Resource
			for _, intg := range page.Integrations {
				name := sv(intg.IntegrationName)
				status := string(intg.Status)
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSIntegration, NativeID: sv(intg.IntegrationArn),
					Name: &name, Region: &region, CreatedAt: tp(intg.CreateTime), Status: &status,
					TagsJSON: awsTagsJSON(intg.Tags), AttributesJSON: mustJSON(intg),
					DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

func scanOptionGroups(ctx context.Context, client *rds.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(ctx, "rds:DescribeOptionGroups", acct, region, st,
		rds.NewDescribeOptionGroupsPaginator(client, &rds.DescribeOptionGroupsInput{}),
		func(page *rds.DescribeOptionGroupsOutput) []*store.Resource {
			var out []*store.Resource
			for _, og := range page.OptionGroupsList {
				name := sv(og.OptionGroupName)
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSOptionGroup, NativeID: sv(og.OptionGroupArn),
					Name: &name, Region: &region, AttributesJSON: mustJSON(og),
					DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

// scanCustomDBEngineVersions discovers user-created custom DB engine versions.
// The DescribeDBEngineVersions "status" filter does not accept "custom-*"
// values (InvalidParameterValue), so we issue one call per known custom engine
// type instead. This avoids fetching the many hundreds of standard versions.
func scanCustomDBEngineVersions(ctx context.Context, client *rds.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	// RDS Custom supports Oracle and SQL Server only. New variants may be added
	// in future SDK releases but this list covers all currently available types.
	customEngines := []string{
		"custom-oracle-ee", "custom-oracle-ee-cdb",
		"custom-sqlserver-ee", "custom-sqlserver-se", "custom-sqlserver-web",
	}
	toResources := func(page *rds.DescribeDBEngineVersionsOutput) []*store.Resource {
		var out []*store.Resource
		for _, ev := range page.DBEngineVersions {
			// Guard: skip any standard versions that slip through.
			if !strings.HasPrefix(sv(ev.Status), "custom-") {
				continue
			}
			name := sv(ev.Engine) + "/" + sv(ev.EngineVersion)
			out = append(out, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRDSCustomDBEngineVersion, NativeID: sv(ev.DBEngineVersionArn),
				Name: &name, Region: &region, Status: ev.Status,
				TagsJSON: awsTagsJSON(ev.TagList), AttributesJSON: mustJSON(ev),
				DiscoveredBy: scanID,
			})
		}
		return out
	}
	for _, engine := range customEngines {
		tt, nn, e := rdsPageScan(ctx, "rds:DescribeDBEngineVersions (custom)", acct, region, st,
			rds.NewDescribeDBEngineVersionsPaginator(client, &rds.DescribeDBEngineVersionsInput{
				Engine:     aws.String(engine),
				IncludeAll: aws.Bool(true),
			}),
			toResources,
		)
		total += tt
		inserted += nn
		if e != nil {
			return total, inserted, e
		}
	}
	return
}
