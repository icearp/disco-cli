package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/odb"
)

// isOdbNotOnboarded disambiguates the "isn't onboarded to the service"
// not-subscribed state from a real IAM denial. Oracle Database@AWS requires
// a Marketplace subscription before any list op succeeds.
func isOdbNotOnboarded(err error) bool {
	return isAccessDeniedWithMessage(err, "isn't onboarded to the service")
}

func init() {
	registerService(serviceEntry{
		name: "aws:odb",
		fn:   scanODB,
		emits: []coverage.TypeDecl{
			{Service: "odb", DiscoType: TypeODBCloudAutonomousVMCluster, Leaf: true},
			{Service: "odb", DiscoType: TypeODBCloudExadataInfrastructure, Leaf: true},
			{Service: "odb", DiscoType: TypeODBCloudVMCluster, Leaf: true},
			{Service: "odb", DiscoType: TypeODBOdbNetwork, Leaf: true},
			{Service: "odb", DiscoType: TypeODBOdbPeeringConnection, Leaf: true},
			{Service: "odb", DiscoType: TypeODBAutonomousDatabase},
			{Service: "odb", DiscoType: TypeODBAutonomousDatabaseBackup},
			{Service: "odb", DiscoType: TypeODBDbNode},
		},
	})
}

type odbAPI interface {
	ListCloudAutonomousVmClusters(context.Context, *odb.ListCloudAutonomousVmClustersInput, ...func(*odb.Options)) (*odb.ListCloudAutonomousVmClustersOutput, error)
	ListCloudExadataInfrastructures(context.Context, *odb.ListCloudExadataInfrastructuresInput, ...func(*odb.Options)) (*odb.ListCloudExadataInfrastructuresOutput, error)
	ListCloudVmClusters(context.Context, *odb.ListCloudVmClustersInput, ...func(*odb.Options)) (*odb.ListCloudVmClustersOutput, error)
	ListOdbNetworks(context.Context, *odb.ListOdbNetworksInput, ...func(*odb.Options)) (*odb.ListOdbNetworksOutput, error)
	ListOdbPeeringConnections(context.Context, *odb.ListOdbPeeringConnectionsInput, ...func(*odb.Options)) (*odb.ListOdbPeeringConnectionsOutput, error)
	ListAutonomousDatabases(context.Context, *odb.ListAutonomousDatabasesInput, ...func(*odb.Options)) (*odb.ListAutonomousDatabasesOutput, error)
	ListAutonomousDatabaseBackups(context.Context, *odb.ListAutonomousDatabaseBackupsInput, ...func(*odb.Options)) (*odb.ListAutonomousDatabaseBackupsOutput, error)
	ListDbNodes(context.Context, *odb.ListDbNodesInput, ...func(*odb.Options)) (*odb.ListDbNodesOutput, error)
}

// scanODB discovers Oracle Database@AWS resources.
func scanODB(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := odb.NewFromConfig(acct.cfg, func(o *odb.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanODBAutonomousVMClusters(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanODBExadataInfras(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanODBNetworks(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanODBPeeringConnections(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}

	// Cloud VM clusters first — their ARNs seed the per-cluster ListDbNodes fan-out.
	vmClusters, t, i, ferr := scanODBVmClusters(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	t, i, ferr = scanODBDbNodes(ctx, client, acct, region, st, scanID, vmClusters)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	// Autonomous databases first — their IDs seed the per-database backup fan-out.
	adbIDs, t, i, ferr := scanODBAutonomousDatabases(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	t, i, ferr = scanODBAutonomousDatabaseBackups(ctx, client, acct, region, st, scanID, adbIDs)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanODBAutonomousVMClusters(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := odb.NewListCloudAutonomousVmClustersPaginator(client, &odb.ListCloudAutonomousVmClustersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isOdbNotOnboarded(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "odb:ListCloudAutonomousVmClusters", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("odb:ListCloudAutonomousVmClusters: %w", err)
		}
		for _, c := range out.CloudAutonomousVmClusters {
			arn := sv(c.CloudAutonomousVmClusterArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeODBCloudAutonomousVMCluster, NativeID: arn,
				Name: c.DisplayName, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "odb cloud-autonomous-vm-clusters")
}

func scanODBExadataInfras(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := odb.NewListCloudExadataInfrastructuresPaginator(client, &odb.ListCloudExadataInfrastructuresInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isOdbNotOnboarded(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "odb:ListCloudExadataInfrastructures", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("odb:ListCloudExadataInfrastructures: %w", err)
		}
		for _, c := range out.CloudExadataInfrastructures {
			arn := sv(c.CloudExadataInfrastructureArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeODBCloudExadataInfrastructure, NativeID: arn,
				Name: c.DisplayName, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "odb cloud-exadata-infrastructures")
}

// odbVMCluster captures the (id, arn) pair the ListDbNodes fan-out needs: the
// id satisfies the required CloudVmClusterId input, the arn seeds the synthetic
// db-node NativeID so the resolver can recover the parent cluster.
type odbVMCluster struct {
	id  string
	arn string
}

func scanODBVmClusters(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string) ([]odbVMCluster, int, int, error) {
	pager := odb.NewListCloudVmClustersPaginator(client, &odb.ListCloudVmClustersInput{})
	var batch []*store.Resource
	var clusters []odbVMCluster
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isOdbNotOnboarded(err) {
				return nil, 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "odb:ListCloudVmClusters", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("odb:ListCloudVmClusters: %w", err)
		}
		for _, c := range out.CloudVmClusters {
			arn := sv(c.CloudVmClusterArn)
			if arn == "" {
				continue
			}
			if id := sv(c.CloudVmClusterId); id != "" {
				clusters = append(clusters, odbVMCluster{id: id, arn: arn})
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeODBCloudVMCluster, NativeID: arn,
				Name: c.ClusterName, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "odb cloud-vm-clusters")
	return clusters, t, i, err
}

// scanODBDbNodes fans out ListDbNodes per VM cluster (the op requires a
// CloudVmClusterId). DB nodes carry a real ARN but no parent-cluster field, so
// the NativeID is synthesised as {clusterARN}/db-node/{dbNodeId} to let the
// resolver recover the parent.
func scanODBDbNodes(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string, clusters []odbVMCluster) (int, int, error) {
	var batch []*store.Resource
	for _, cl := range clusters {
		cid := cl.id
		pager := odb.NewListDbNodesPaginator(client, &odb.ListDbNodesInput{CloudVmClusterId: &cid})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "odb:ListDbNodes", acct.ID, region, err)
					break
				}
				return 0, 0, fmt.Errorf("odb:ListDbNodes %s: %w", cid, err)
			}
			for _, n := range out.DbNodes {
				id := sv(n.DbNodeId)
				if id == "" {
					continue
				}
				name := sv(n.Hostname)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeODBDbNode, NativeID: cl.arn + "/db-node/" + id,
					Name: &name, Region: &region,
					AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "odb db-nodes")
}

// scanODBAutonomousDatabases lists Autonomous Databases account-wide and returns
// their ids for the per-database backup fan-out.
func scanODBAutonomousDatabases(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := odb.NewListAutonomousDatabasesPaginator(client, &odb.ListAutonomousDatabasesInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isOdbNotOnboarded(err) {
				return nil, 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "odb:ListAutonomousDatabases", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("odb:ListAutonomousDatabases: %w", err)
		}
		for _, d := range out.AutonomousDatabases {
			arn := sv(d.AutonomousDatabaseArn)
			if arn == "" {
				continue
			}
			if id := sv(d.AutonomousDatabaseId); id != "" {
				ids = append(ids, id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeODBAutonomousDatabase, NativeID: arn,
				Name: d.DisplayName, Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "odb autonomous-databases")
	return ids, t, i, err
}

// scanODBAutonomousDatabaseBackups fans out ListAutonomousDatabaseBackups per
// Autonomous Database (the op requires an AutonomousDatabaseId). Backups carry a
// real ARN; the resolver wires them to the parent via the AutonomousDatabaseId
// attribute.
func scanODBAutonomousDatabaseBackups(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string, adbIDs []string) (int, int, error) {
	var batch []*store.Resource
	for _, id := range adbIDs {
		aid := id
		pager := odb.NewListAutonomousDatabaseBackupsPaginator(client, &odb.ListAutonomousDatabaseBackupsInput{AutonomousDatabaseId: &aid})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "odb:ListAutonomousDatabaseBackups", acct.ID, region, err)
					break
				}
				return 0, 0, fmt.Errorf("odb:ListAutonomousDatabaseBackups %s: %w", aid, err)
			}
			for _, b := range out.AutonomousDatabaseBackups {
				arn := sv(b.AutonomousDatabaseBackupArn)
				if arn == "" {
					continue
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeODBAutonomousDatabaseBackup, NativeID: arn,
					Name: b.DisplayName, Region: &region,
					AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "odb autonomous-database-backups")
}

func scanODBNetworks(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := odb.NewListOdbNetworksPaginator(client, &odb.ListOdbNetworksInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isOdbNotOnboarded(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "odb:ListOdbNetworks", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("odb:ListOdbNetworks: %w", err)
		}
		for _, n := range out.OdbNetworks {
			arn := sv(n.OdbNetworkArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeODBOdbNetwork, NativeID: arn,
				Name: n.DisplayName, Region: &region,
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "odb odb-networks")
}

func scanODBPeeringConnections(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := odb.NewListOdbPeeringConnectionsPaginator(client, &odb.ListOdbPeeringConnectionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isOdbNotOnboarded(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "odb:ListOdbPeeringConnections", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("odb:ListOdbPeeringConnections: %w", err)
		}
		for _, p := range out.OdbPeeringConnections {
			arn := sv(p.OdbPeeringConnectionArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeODBOdbPeeringConnection, NativeID: arn,
				Name: p.DisplayName, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "odb odb-peering-connections")
}
