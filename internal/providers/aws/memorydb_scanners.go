package aws

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"
)

func init() {
	registerService(serviceEntry{
		name: "aws:memorydb",
		fn:   scanMemoryDB,
		emits: []coverage.TypeDecl{
			{Service: "memorydb", DiscoType: TypeMemoryDBACL},
			{Service: "memorydb", DiscoType: TypeMemoryDBCluster},
			{Service: "memorydb", DiscoType: TypeMemoryDBMultiRegionCluster, Leaf: true},
			{Service: "memorydb", DiscoType: TypeMemoryDBParameterGroup, Leaf: true},
			{Service: "memorydb", DiscoType: TypeMemoryDBSubnetGroup},
			{Service: "memorydb", DiscoType: TypeMemoryDBUser, Leaf: true},
		},
	})
}

type memorydbAPI interface {
	DescribeACLs(context.Context, *memorydb.DescribeACLsInput, ...func(*memorydb.Options)) (*memorydb.DescribeACLsOutput, error)
	DescribeClusters(context.Context, *memorydb.DescribeClustersInput, ...func(*memorydb.Options)) (*memorydb.DescribeClustersOutput, error)
	DescribeMultiRegionClusters(context.Context, *memorydb.DescribeMultiRegionClustersInput, ...func(*memorydb.Options)) (*memorydb.DescribeMultiRegionClustersOutput, error)
	DescribeParameterGroups(context.Context, *memorydb.DescribeParameterGroupsInput, ...func(*memorydb.Options)) (*memorydb.DescribeParameterGroupsOutput, error)
	DescribeSubnetGroups(context.Context, *memorydb.DescribeSubnetGroupsInput, ...func(*memorydb.Options)) (*memorydb.DescribeSubnetGroupsOutput, error)
	DescribeUsers(context.Context, *memorydb.DescribeUsersInput, ...func(*memorydb.Options)) (*memorydb.DescribeUsersOutput, error)
}

// scanMemoryDB discovers six MemoryDB (Redis-compatible) resource types via
// the dedicated SDK. ARNs come natively on each Describe response.
func scanMemoryDB(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := memorydb.NewFromConfig(acct.cfg, func(o *memorydb.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanMemDBACLs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMemDBClusters(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMemDBMultiRegionClusters(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMemDBParameterGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMemDBSubnetGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMemDBUsers(ctx, client, acct, region, st, scanID) },
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

func scanMemDBACLs(ctx context.Context, client memorydbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := memorydb.NewDescribeACLsPaginator(client, &memorydb.DescribeACLsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "memorydb:DescribeACLs", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("memorydb:DescribeACLs: %w", err)
		}
		for _, a := range out.ACLs {
			arn := sv(a.ARN)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMemoryDBACL, NativeID: arn,
				Name: a.Name, Region: &region, Status: a.Status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				// Name "open-access" is the AWS-managed default ACL present
				// in every account.
				ManagedByProvider: sv(a.Name) == "open-access",
			})
		}
	}
	return upsertBatch(st, batch, "memorydb acls")
}

func scanMemDBClusters(ctx context.Context, client memorydbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := memorydb.NewDescribeClustersPaginator(client, &memorydb.DescribeClustersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "memorydb:DescribeClusters", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("memorydb:DescribeClusters: %w", err)
		}
		for _, c := range out.Clusters {
			arn := sv(c.ARN)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMemoryDBCluster, NativeID: arn,
				Name: c.Name, Region: &region, Status: c.Status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "memorydb clusters")
}

func scanMemDBMultiRegionClusters(ctx context.Context, client memorydbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := memorydb.NewDescribeMultiRegionClustersPaginator(client, &memorydb.DescribeMultiRegionClustersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "memorydb:DescribeMultiRegionClusters", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("memorydb:DescribeMultiRegionClusters: %w", err)
		}
		for _, c := range out.MultiRegionClusters {
			arn := sv(c.ARN)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMemoryDBMultiRegionCluster, NativeID: arn,
				Name: c.MultiRegionClusterName, Region: &region, Status: c.Status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "memorydb multi-region-clusters")
}

func scanMemDBParameterGroups(ctx context.Context, client memorydbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := memorydb.NewDescribeParameterGroupsPaginator(client, &memorydb.DescribeParameterGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "memorydb:DescribeParameterGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("memorydb:DescribeParameterGroups: %w", err)
		}
		for _, p := range out.ParameterGroups {
			arn := sv(p.ARN)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMemoryDBParameterGroup, NativeID: arn,
				Name: p.Name, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				// Names prefixed "default." (e.g. default.redis7) are the
				// AWS-managed default parameter groups present in every region.
				ManagedByProvider: strings.HasPrefix(sv(p.Name), "default."),
			})
		}
	}
	return upsertBatch(st, batch, "memorydb parameter-groups")
}

func scanMemDBSubnetGroups(ctx context.Context, client memorydbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := memorydb.NewDescribeSubnetGroupsPaginator(client, &memorydb.DescribeSubnetGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "memorydb:DescribeSubnetGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("memorydb:DescribeSubnetGroups: %w", err)
		}
		for _, s := range out.SubnetGroups {
			arn := sv(s.ARN)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMemoryDBSubnetGroup, NativeID: arn,
				Name: s.Name, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "memorydb subnet-groups")
}

func scanMemDBUsers(ctx context.Context, client memorydbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := memorydb.NewDescribeUsersPaginator(client, &memorydb.DescribeUsersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "memorydb:DescribeUsers", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("memorydb:DescribeUsers: %w", err)
		}
		for _, u := range out.Users {
			arn := sv(u.ARN)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMemoryDBUser, NativeID: arn,
				Name: u.Name, Region: &region, Status: u.Status,
				AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
				// Name "default" is the AWS-managed default user present in
				// every account.
				ManagedByProvider: sv(u.Name) == "default",
			})
		}
	}
	return upsertBatch(st, batch, "memorydb users")
}
