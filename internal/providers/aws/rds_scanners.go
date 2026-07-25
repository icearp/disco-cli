package aws

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRDSDBCluster, Service: "rds", Upstream: "AWS::RDS::DBCluster", Redact: []redact.Rule{{Path: "MasterUserPassword", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeRDSDBInstance, Service: "rds", Upstream: "AWS::RDS::DBInstance", Redact: []redact.Rule{{Path: "MasterUserPassword", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeRDSGlobalCluster, Service: "rds", Upstream: "AWS::RDS::GlobalCluster"})
	registerType(restype.Descriptor{Type: TypeRDSDBClusterParameterGroup, Service: "rds", Upstream: "AWS::RDS::DBClusterParameterGroup", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRDSDBParameterGroup, Service: "rds", Upstream: "AWS::RDS::DBParameterGroup", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRDSDBSubnetGroup, Service: "rds", Upstream: "AWS::RDS::DBSubnetGroup"})
	registerType(restype.Descriptor{Type: TypeRDSDBSecurityGroup, Service: "rds", Upstream: "AWS::RDS::DBSecurityGroup", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRDSOptionGroup, Service: "rds", Upstream: "AWS::RDS::OptionGroup", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRDSEventSubscription, Service: "rds", Upstream: "AWS::RDS::EventSubscription", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRDSIntegration, Service: "rds", Upstream: "AWS::RDS::Integration"})
	registerType(restype.Descriptor{Type: TypeRDSDBProxy, Service: "rds", Upstream: "AWS::RDS::DBProxy"})
	registerType(restype.Descriptor{Type: TypeRDSDBProxyEndpoint, Service: "rds", Upstream: "AWS::RDS::DBProxyEndpoint"})
	registerType(restype.Descriptor{Type: TypeRDSDBProxyTargetGroup, Service: "rds", Upstream: "AWS::RDS::DBProxyTargetGroup"})
	registerType(restype.Descriptor{Type: TypeRDSDBShardGroup, Service: "rds", Upstream: "AWS::RDS::DBShardGroup"})
	registerType(restype.Descriptor{Type: TypeRDSCustomDBEngineVersion, Service: "rds", Upstream: "AWS::RDS::CustomDBEngineVersion", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRDSSnapshot, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSClusterSnapshot, Service: "rds", Upstream: "AWS::rds::cluster-snapshot"})
	registerType(restype.Descriptor{Type: TypeRDSClusterEndpoint, Service: "rds", Upstream: "AWS::rds::cluster-endpoint"})
	registerType(restype.Descriptor{Type: TypeRDSReservedInstance, Service: "rds", Leaf: true})
	registerType(restype.Descriptor{Type: TypeRDSAutoBackup, Service: "rds", Upstream: "AWS::rds::auto-backup"})
	registerType(restype.Descriptor{Type: TypeRDSClusterAutoBackup, Service: "rds", Upstream: "AWS::rds::cluster-auto-backup"})
	registerType(restype.Descriptor{Type: TypeRDSTenantDatabase, Service: "rds", Upstream: "AWS::rds::tenant-database"})
	registerType(restype.Descriptor{Type: TypeRDSSnapshotTenantDatabase, Service: "rds", Upstream: "AWS::rds::snapshot-tenant-database"})
	registerType(restype.Descriptor{Type: TypeRDSDeployment, Service: "rds", Leaf: true})
	registerService(serviceEntry{
		name: "aws:rds",
		fn:   scanRDS,
	})
}

// rdsAPI is the narrow set of RDS operations called by the scanRDS sub-phases.
type rdsAPI interface {
	DescribeDBInstances(context.Context, *rds.DescribeDBInstancesInput, ...func(*rds.Options)) (*rds.DescribeDBInstancesOutput, error)
	DescribeDBClusters(context.Context, *rds.DescribeDBClustersInput, ...func(*rds.Options)) (*rds.DescribeDBClustersOutput, error)
	DescribeDBClusterParameterGroups(context.Context, *rds.DescribeDBClusterParameterGroupsInput, ...func(*rds.Options)) (*rds.DescribeDBClusterParameterGroupsOutput, error)
	DescribeDBParameterGroups(context.Context, *rds.DescribeDBParameterGroupsInput, ...func(*rds.Options)) (*rds.DescribeDBParameterGroupsOutput, error)
	DescribeDBProxies(context.Context, *rds.DescribeDBProxiesInput, ...func(*rds.Options)) (*rds.DescribeDBProxiesOutput, error)
	DescribeDBProxyEndpoints(context.Context, *rds.DescribeDBProxyEndpointsInput, ...func(*rds.Options)) (*rds.DescribeDBProxyEndpointsOutput, error)
	DescribeDBProxyTargetGroups(context.Context, *rds.DescribeDBProxyTargetGroupsInput, ...func(*rds.Options)) (*rds.DescribeDBProxyTargetGroupsOutput, error)
	DescribeDBSecurityGroups(context.Context, *rds.DescribeDBSecurityGroupsInput, ...func(*rds.Options)) (*rds.DescribeDBSecurityGroupsOutput, error)
	DescribeDBShardGroups(context.Context, *rds.DescribeDBShardGroupsInput, ...func(*rds.Options)) (*rds.DescribeDBShardGroupsOutput, error)
	DescribeDBSubnetGroups(context.Context, *rds.DescribeDBSubnetGroupsInput, ...func(*rds.Options)) (*rds.DescribeDBSubnetGroupsOutput, error)
	DescribeEventSubscriptions(context.Context, *rds.DescribeEventSubscriptionsInput, ...func(*rds.Options)) (*rds.DescribeEventSubscriptionsOutput, error)
	DescribeGlobalClusters(context.Context, *rds.DescribeGlobalClustersInput, ...func(*rds.Options)) (*rds.DescribeGlobalClustersOutput, error)
	DescribeIntegrations(context.Context, *rds.DescribeIntegrationsInput, ...func(*rds.Options)) (*rds.DescribeIntegrationsOutput, error)
	DescribeOptionGroups(context.Context, *rds.DescribeOptionGroupsInput, ...func(*rds.Options)) (*rds.DescribeOptionGroupsOutput, error)
	DescribeDBEngineVersions(context.Context, *rds.DescribeDBEngineVersionsInput, ...func(*rds.Options)) (*rds.DescribeDBEngineVersionsOutput, error)
	DescribeDBSnapshots(context.Context, *rds.DescribeDBSnapshotsInput, ...func(*rds.Options)) (*rds.DescribeDBSnapshotsOutput, error)
	DescribeDBClusterSnapshots(context.Context, *rds.DescribeDBClusterSnapshotsInput, ...func(*rds.Options)) (*rds.DescribeDBClusterSnapshotsOutput, error)
	DescribeDBClusterEndpoints(context.Context, *rds.DescribeDBClusterEndpointsInput, ...func(*rds.Options)) (*rds.DescribeDBClusterEndpointsOutput, error)
	DescribeReservedDBInstances(context.Context, *rds.DescribeReservedDBInstancesInput, ...func(*rds.Options)) (*rds.DescribeReservedDBInstancesOutput, error)
	DescribeDBInstanceAutomatedBackups(context.Context, *rds.DescribeDBInstanceAutomatedBackupsInput, ...func(*rds.Options)) (*rds.DescribeDBInstanceAutomatedBackupsOutput, error)
	DescribeDBClusterAutomatedBackups(context.Context, *rds.DescribeDBClusterAutomatedBackupsInput, ...func(*rds.Options)) (*rds.DescribeDBClusterAutomatedBackupsOutput, error)
	DescribeTenantDatabases(context.Context, *rds.DescribeTenantDatabasesInput, ...func(*rds.Options)) (*rds.DescribeTenantDatabasesOutput, error)
	DescribeDBSnapshotTenantDatabases(context.Context, *rds.DescribeDBSnapshotTenantDatabasesInput, ...func(*rds.Options)) (*rds.DescribeDBSnapshotTenantDatabasesOutput, error)
	DescribeBlueGreenDeployments(context.Context, *rds.DescribeBlueGreenDeploymentsInput, ...func(*rds.Options)) (*rds.DescribeBlueGreenDeploymentsOutput, error)
}

// rdsPager is satisfied by every AWS SDK v2 RDS paginator.
type rdsPager[P any] interface {
	HasMorePages() bool
	NextPage(context.Context, ...func(*rds.Options)) (P, error)
}

// rdsPageScan pages an RDS Describe call, converts each page via toResources,
// and upserts. Access-denied errors skip via skipIfAccessDenied.
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
	return runScanners(
		ctx,
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
		func(ctx context.Context) (int, int, error) {
			return scanDBSnapshots(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanDBClusterSnapshots(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanDBClusterEndpoints(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanReservedDBInstances(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanDBInstanceAutomatedBackups(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanDBClusterAutomatedBackups(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanTenantDatabases(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanDBSnapshotTenantDatabases(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanBlueGreenDeployments(ctx, client, acct, region, st, scanID)
		},
	)
}

// nonRDSEngines are engines that ride rds:Describe* APIs but have their own
// dedicated SDK service + disco type; filtered out so each engine row lands
// under exactly one type (and one scanner owns its resolvers). docdb stays
// even though rds:DescribeDBClusters does NOT currently return it (per
// docdb's own CreateDBCluster.Engine valid values) — cheap guard against AWS
// later surfacing docdb via the shared API.
var nonRDSEngines = map[string]bool{
	"neptune": true,
	"docdb":   true,
}

func scanDBInstances(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeDBInstances", acct, region, st,
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

func scanDBClusters(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeDBClusters", acct, region, st,
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

func scanDBClusterParameterGroups(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeDBClusterParameterGroups", acct, region, st,
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

func scanDBParameterGroups(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeDBParameterGroups", acct, region, st,
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

func scanDBProxies(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeDBProxies", acct, region, st,
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

func scanDBProxyEndpoints(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeDBProxyEndpoints", acct, region, st,
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
func scanDBProxyTargetGroups(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var proxyNames []string
	// Collect proxy names — the toResources function returns nil so no upsert occurs.
	if _, _, err := rdsPageScan(
		ctx, "rds:DescribeDBProxies (for target groups)", acct, region, st,
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
			_, _, err := rdsPageScan(
				ctx, "rds:DescribeDBProxyTargetGroups", acct, region, st,
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

func scanDBSecurityGroups(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeDBSecurityGroups", acct, region, st,
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
					// DBSecurityGroupName "default" is the AWS-managed default
					// EC2-Classic DB security group present in legacy accounts.
					ManagedByProvider: name == "default",
				})
			}
			return out
		},
	)
}

// scanDBShardGroups discovers RDS DB shard groups (Aurora Limitless).
// The SDK has no paginator for this API, so we paginate manually via Marker.
func scanDBShardGroups(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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

func scanDBSubnetGroups(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeDBSubnetGroups", acct, region, st,
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

func scanEventSubscriptions(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeEventSubscriptions", acct, region, st,
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
func scanGlobalClusters(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeGlobalClusters", acct, region, st,
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

func scanIntegrations(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeIntegrations", acct, region, st,
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

func scanOptionGroups(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeOptionGroups", acct, region, st,
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
func scanCustomDBEngineVersions(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	// RDS Custom supports Oracle and SQL Server only. New variants may be added
	// in future SDK releases but this list covers all currently available types.
	customEngines := []string{
		"custom-oracle-ee", "custom-oracle-ee-cdb",
		"custom-sqlserver-ee", "custom-sqlserver-se", "custom-sqlserver-web",
	}
	toResources := func(page *rds.DescribeDBEngineVersionsOutput) []*store.Resource {
		var out []*store.Resource
		for _, ev := range page.DBEngineVersions {
			// Skip standard versions that slip through.
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
		tt, nn, e := rdsPageScan(
			ctx, "rds:DescribeDBEngineVersions (custom)", acct, region, st,
			rds.NewDescribeDBEngineVersionsPaginator(client, &rds.DescribeDBEngineVersionsInput{
				Engine:     aws.String(engine),
				IncludeAll: aws.Bool(true),
			}),
			toResources,
		)
		total += tt
		inserted += nn
		if e != nil {
			// RDS Custom variants are not deployed in every region;
			// AWS surfaces this as InvalidParameterValue. Skip the
			// engine silently and continue with the rest.
			if isAPIErrorWithMessage(e, "InvalidParameterValue", "Unrecognized engine name") {
				continue
			}
			return total, inserted, e
		}
	}
	return
}

func scanDBSnapshots(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeDBSnapshots", acct, region, st,
		rds.NewDescribeDBSnapshotsPaginator(client, &rds.DescribeDBSnapshotsInput{}),
		func(page *rds.DescribeDBSnapshotsOutput) []*store.Resource {
			var out []*store.Resource
			for _, s := range page.DBSnapshots {
				name := sv(s.DBSnapshotIdentifier)
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSSnapshot, NativeID: sv(s.DBSnapshotArn),
					Name: &name, Region: &region, CreatedAt: tp(s.SnapshotCreateTime),
					Status: s.Status, TagsJSON: awsTagsJSON(s.TagList),
					AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

func scanDBClusterSnapshots(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeDBClusterSnapshots", acct, region, st,
		rds.NewDescribeDBClusterSnapshotsPaginator(client, &rds.DescribeDBClusterSnapshotsInput{}),
		func(page *rds.DescribeDBClusterSnapshotsOutput) []*store.Resource {
			var out []*store.Resource
			for _, s := range page.DBClusterSnapshots {
				name := sv(s.DBClusterSnapshotIdentifier)
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSClusterSnapshot, NativeID: sv(s.DBClusterSnapshotArn),
					Name: &name, Region: &region, CreatedAt: tp(s.SnapshotCreateTime),
					Status: s.Status, TagsJSON: awsTagsJSON(s.TagList),
					AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

func scanDBClusterEndpoints(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeDBClusterEndpoints", acct, region, st,
		rds.NewDescribeDBClusterEndpointsPaginator(client, &rds.DescribeDBClusterEndpointsInput{}),
		func(page *rds.DescribeDBClusterEndpointsOutput) []*store.Resource {
			var out []*store.Resource
			for _, ep := range page.DBClusterEndpoints {
				name := sv(ep.DBClusterEndpointIdentifier)
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSClusterEndpoint, NativeID: sv(ep.DBClusterEndpointArn),
					Name: &name, Region: &region, Status: ep.Status,
					AttributesJSON: mustJSON(ep), DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

// scanReservedDBInstances discovers reserved DB instance purchases. These carry
// no graph edges (billing artefacts) so they are leaf resources.
func scanReservedDBInstances(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeReservedDBInstances", acct, region, st,
		rds.NewDescribeReservedDBInstancesPaginator(client, &rds.DescribeReservedDBInstancesInput{}),
		func(page *rds.DescribeReservedDBInstancesOutput) []*store.Resource {
			var out []*store.Resource
			for _, ri := range page.ReservedDBInstances {
				name := sv(ri.ReservedDBInstanceId)
				nativeID := sv(ri.ReservedDBInstanceArn)
				if nativeID == "" {
					nativeID = rdsARN(region, acct.ID, "ri", name)
				}
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSReservedInstance, NativeID: nativeID,
					Name: &name, Region: &region, CreatedAt: tp(ri.StartTime),
					Status: ri.State, AttributesJSON: mustJSON(ri), DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

func scanDBInstanceAutomatedBackups(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeDBInstanceAutomatedBackups", acct, region, st,
		rds.NewDescribeDBInstanceAutomatedBackupsPaginator(client, &rds.DescribeDBInstanceAutomatedBackupsInput{}),
		func(page *rds.DescribeDBInstanceAutomatedBackupsOutput) []*store.Resource {
			var out []*store.Resource
			for _, b := range page.DBInstanceAutomatedBackups {
				name := sv(b.DBInstanceIdentifier)
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSAutoBackup, NativeID: sv(b.DBInstanceAutomatedBackupsArn),
					Name: &name, Region: &region, Status: b.Status,
					AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

func scanDBClusterAutomatedBackups(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeDBClusterAutomatedBackups", acct, region, st,
		rds.NewDescribeDBClusterAutomatedBackupsPaginator(client, &rds.DescribeDBClusterAutomatedBackupsInput{}),
		func(page *rds.DescribeDBClusterAutomatedBackupsOutput) []*store.Resource {
			var out []*store.Resource
			for _, b := range page.DBClusterAutomatedBackups {
				name := sv(b.DBClusterIdentifier)
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSClusterAutoBackup, NativeID: sv(b.DBClusterAutomatedBackupsArn),
					Name: &name, Region: &region, Status: b.Status,
					AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

func scanTenantDatabases(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeTenantDatabases", acct, region, st,
		rds.NewDescribeTenantDatabasesPaginator(client, &rds.DescribeTenantDatabasesInput{}),
		func(page *rds.DescribeTenantDatabasesOutput) []*store.Resource {
			var out []*store.Resource
			for _, td := range page.TenantDatabases {
				name := sv(td.TenantDBName)
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSTenantDatabase, NativeID: sv(td.TenantDatabaseARN),
					Name: &name, Region: &region, Status: td.Status,
					TagsJSON: awsTagsJSON(td.TagList), AttributesJSON: mustJSON(td),
					DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

func scanDBSnapshotTenantDatabases(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeDBSnapshotTenantDatabases", acct, region, st,
		rds.NewDescribeDBSnapshotTenantDatabasesPaginator(client, &rds.DescribeDBSnapshotTenantDatabasesInput{}),
		func(page *rds.DescribeDBSnapshotTenantDatabasesOutput) []*store.Resource {
			var out []*store.Resource
			for _, td := range page.DBSnapshotTenantDatabases {
				name := sv(td.TenantDBName)
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSSnapshotTenantDatabase, NativeID: sv(td.DBSnapshotTenantDatabaseARN),
					Name: &name, Region: &region,
					TagsJSON: awsTagsJSON(td.TagList), AttributesJSON: mustJSON(td),
					DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}

// scanBlueGreenDeployments discovers RDS blue/green deployment workflows. These
// reference a source + target environment but carry no clean single parent ARN,
// so they are leaf resources.
func scanBlueGreenDeployments(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return rdsPageScan(
		ctx, "rds:DescribeBlueGreenDeployments", acct, region, st,
		rds.NewDescribeBlueGreenDeploymentsPaginator(client, &rds.DescribeBlueGreenDeploymentsInput{}),
		func(page *rds.DescribeBlueGreenDeploymentsOutput) []*store.Resource {
			var out []*store.Resource
			for _, d := range page.BlueGreenDeployments {
				id := sv(d.BlueGreenDeploymentIdentifier)
				name := sv(d.BlueGreenDeploymentName)
				out = append(out, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSDeployment, NativeID: rdsARN(region, acct.ID, "deployment", id),
					Name: &name, Region: &region, CreatedAt: tp(d.CreateTime),
					Status: d.Status, TagsJSON: awsTagsJSON(d.TagList),
					AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}
