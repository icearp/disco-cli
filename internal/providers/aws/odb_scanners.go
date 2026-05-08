package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
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
		},
	})
}

type odbAPI interface {
	ListCloudAutonomousVmClusters(context.Context, *odb.ListCloudAutonomousVmClustersInput, ...func(*odb.Options)) (*odb.ListCloudAutonomousVmClustersOutput, error)
	ListCloudExadataInfrastructures(context.Context, *odb.ListCloudExadataInfrastructuresInput, ...func(*odb.Options)) (*odb.ListCloudExadataInfrastructuresOutput, error)
	ListCloudVmClusters(context.Context, *odb.ListCloudVmClustersInput, ...func(*odb.Options)) (*odb.ListCloudVmClustersOutput, error)
	ListOdbNetworks(context.Context, *odb.ListOdbNetworksInput, ...func(*odb.Options)) (*odb.ListOdbNetworksOutput, error)
	ListOdbPeeringConnections(context.Context, *odb.ListOdbPeeringConnectionsInput, ...func(*odb.Options)) (*odb.ListOdbPeeringConnectionsOutput, error)
}

// scanODB discovers Oracle Database@AWS resources.
func scanODB(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := odb.NewFromConfig(acct.cfg, func(o *odb.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanODBAutonomousVMClusters(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanODBExadataInfras(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanODBVmClusters(ctx, client, acct, region, st, scanID) },
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

func scanODBVmClusters(ctx context.Context, client odbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := odb.NewListCloudVmClustersPaginator(client, &odb.ListCloudVmClustersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isOdbNotOnboarded(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "odb:ListCloudVmClusters", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("odb:ListCloudVmClusters: %w", err)
		}
		for _, c := range out.CloudVmClusters {
			arn := sv(c.CloudVmClusterArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeODBCloudVMCluster, NativeID: arn,
				Name: c.ClusterName, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "odb cloud-vm-clusters")
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
