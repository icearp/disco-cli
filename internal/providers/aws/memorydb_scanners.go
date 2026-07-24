package aws

import (
	"context"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/memorydb"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMemoryDBACL, Service: "memorydb"})
	registerType(restype.Descriptor{Type: TypeMemoryDBCluster, Service: "memorydb"})
	registerType(restype.Descriptor{Type: TypeMemoryDBMultiRegionCluster, Service: "memorydb", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMemoryDBMultiRegionParameterGroup, Service: "memorydb", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMemoryDBParameterGroup, Service: "memorydb", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMemoryDBReservedNode, Service: "memorydb", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMemoryDBSnapshot, Service: "memorydb", Leaf: true})
	registerType(restype.Descriptor{Type: TypeMemoryDBSubnetGroup, Service: "memorydb"})
	registerType(restype.Descriptor{Type: TypeMemoryDBUser, Service: "memorydb", Leaf: true})
	registerService(serviceEntry{
		name: "aws:memorydb",
		fn:   scanMemoryDB,
	})
}

type memorydbAPI interface {
	DescribeACLs(context.Context, *memorydb.DescribeACLsInput, ...func(*memorydb.Options)) (*memorydb.DescribeACLsOutput, error)
	DescribeClusters(context.Context, *memorydb.DescribeClustersInput, ...func(*memorydb.Options)) (*memorydb.DescribeClustersOutput, error)
	DescribeMultiRegionClusters(context.Context, *memorydb.DescribeMultiRegionClustersInput, ...func(*memorydb.Options)) (*memorydb.DescribeMultiRegionClustersOutput, error)
	DescribeMultiRegionParameterGroups(context.Context, *memorydb.DescribeMultiRegionParameterGroupsInput, ...func(*memorydb.Options)) (*memorydb.DescribeMultiRegionParameterGroupsOutput, error)
	DescribeParameterGroups(context.Context, *memorydb.DescribeParameterGroupsInput, ...func(*memorydb.Options)) (*memorydb.DescribeParameterGroupsOutput, error)
	DescribeReservedNodes(context.Context, *memorydb.DescribeReservedNodesInput, ...func(*memorydb.Options)) (*memorydb.DescribeReservedNodesOutput, error)
	DescribeSnapshots(context.Context, *memorydb.DescribeSnapshotsInput, ...func(*memorydb.Options)) (*memorydb.DescribeSnapshotsOutput, error)
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
		func() (int, int, error) {
			return scanMemDBMultiRegionParameterGroups(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanMemDBParameterGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMemDBReservedNodes(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanMemDBSnapshots(ctx, client, acct, region, st, scanID) },
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
			// Regions without multi-region cluster support reject with
			// InvalidParameterValueException "This API operation is currently
			// unavailable". Per-region availability gap — silent-skip.
			if isAPIErrorWithMessage(err, "InvalidParameterValueException", "currently unavailable") {
				return 0, 0, nil
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

func scanMemDBSnapshots(ctx context.Context, client memorydbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := memorydb.NewDescribeSnapshotsPaginator(client, &memorydb.DescribeSnapshotsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "memorydb:DescribeSnapshots", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("memorydb:DescribeSnapshots: %w", err)
		}
		for _, s := range out.Snapshots {
			arn := sv(s.ARN)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMemoryDBSnapshot, NativeID: arn,
				Name: s.Name, Region: &region, Status: s.Status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "memorydb snapshots")
}

func scanMemDBReservedNodes(ctx context.Context, client memorydbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := memorydb.NewDescribeReservedNodesPaginator(client, &memorydb.DescribeReservedNodesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "memorydb:DescribeReservedNodes", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("memorydb:DescribeReservedNodes: %w", err)
		}
		for _, n := range out.ReservedNodes {
			arn := sv(n.ARN)
			if arn == "" {
				continue
			}
			label := sv(n.ReservationId)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMemoryDBReservedNode, NativeID: arn,
				Name: &label, Region: &region, Status: n.State,
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "memorydb reserved-nodes")
}

// scanMemDBMultiRegionParameterGroups uses a manual NextToken loop —
// DescribeMultiRegionParameterGroups has no SDK paginator. Regions without
// multi-region support reject it the same way as multi-region clusters.
func scanMemDBMultiRegionParameterGroups(ctx context.Context, client memorydbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.DescribeMultiRegionParameterGroups(ctx, &memorydb.DescribeMultiRegionParameterGroupsInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "memorydb:DescribeMultiRegionParameterGroups", acct.ID, region, err)
			}
			if isAPIErrorWithMessage(err, "InvalidParameterValueException", "currently unavailable") {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("memorydb:DescribeMultiRegionParameterGroups: %w", err)
		}
		for _, p := range out.MultiRegionParameterGroups {
			arn := sv(p.ARN)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMemoryDBMultiRegionParameterGroup, NativeID: arn,
				Name: p.Name, Region: &region,
				ManagedByProvider: strings.HasPrefix(sv(p.Name), "default."),
				AttributesJSON:    mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "memorydb multi-region-parameter-groups")
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
