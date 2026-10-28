package aws

import (
	"cmp"
	"context"
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/memorydb"
	memorydbtypes "github.com/aws/aws-sdk-go-v2/service/memorydb/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeMemoryDBACL, Service: "memorydb"})
	registerType(restype.Descriptor{Type: TypeMemoryDBCluster, Service: "memorydb"})
	registerType(restype.Descriptor{Type: TypeMemoryDBMultiRegionCluster, Service: "memorydb"})
	registerType(restype.Descriptor{Type: TypeMemoryDBMultiRegionParameterGroup, Service: "memorydb", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeMemoryDBParameterGroup, Service: "memorydb"})
	registerType(restype.Descriptor{Type: TypeMemoryDBReservedNode, Service: "memorydb"})
	registerType(restype.Descriptor{Type: TypeMemoryDBSnapshot, Service: "memorydb"})
	registerType(restype.Descriptor{Type: TypeMemoryDBSubnetGroup, Service: "memorydb"})
	registerType(restype.Descriptor{Type: TypeMemoryDBUser, Service: "memorydb"})
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
	ListTags(context.Context, *memorydb.ListTagsInput, ...func(*memorydb.Options)) (*memorydb.ListTagsOutput, error)
}

const (
	memDBTagsOp = "memorydb:ListTags"
	// memDBNoIdentifier names, in a warning, a listed element with neither an
	// ARN nor a name.
	memDBNoIdentifier = "(an element with no identifier)"
	// memDBClusterNotReady is the code ListTags models for a cluster whose
	// state refuses the call.
	memDBClusterNotReady = "InvalidClusterStateFault"
)

// scanMemoryDB discovers the MemoryDB (Redis-compatible) resources of one
// region. Every listing runs as its own phase, one at a time, in the order
// below: a phase's error does not stop the later ones; a failed write and
// ctx's end do (runHostPhases). Each tagged listing writes a page at a
// time. Every type but the multi-region parameter group (not taggable) and
// the reserved node (scanMemDBReservedNodes) has its tags read per row
// (memDBTags), and the tag denials of the whole scan are one warning. The
// Describe ops model no page cap, so no MaxResults is sent.
func scanMemoryDB(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := memorydb.NewFromConfig(acct.cfg, func(o *memorydb.Options) { o.Region = region })
	var notReady memDBClustersNotReady
	tags := notReady.tags(memDBTags(client)).sharingDenials()
	total, inserted, err := runHostPhases(ctx, st, acct, region, "memorydb", 0, []hostPhase{
		{op: "memorydb:DescribeACLs", run: func(p *phaseTotals) bool {
			return scanMemDBACLs(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "memorydb:DescribeClusters", run: func(p *phaseTotals) bool {
			return scanMemDBClusters(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "memorydb:DescribeMultiRegionClusters", run: func(p *phaseTotals) bool {
			return scanMemDBMultiRegionClusters(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "memorydb:DescribeMultiRegionParameterGroups", func() (int, int, error) {
			return scanMemDBMultiRegionParameterGroups(ctx, client, acct, region, st, scanID)
		}),
		{op: "memorydb:DescribeParameterGroups", run: func(p *phaseTotals) bool {
			return scanMemDBParameterGroups(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "memorydb:DescribeReservedNodes", func() (int, int, error) {
			return scanMemDBReservedNodes(ctx, client, acct, region, st, scanID)
		}),
		{op: "memorydb:DescribeSnapshots", run: func(p *phaseTotals) bool {
			return scanMemDBSnapshots(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "memorydb:DescribeSubnetGroups", run: func(p *phaseTotals) bool {
			return scanMemDBSubnetGroups(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "memorydb:DescribeUsers", run: func(p *phaseTotals) bool {
			return scanMemDBUsers(ctx, client, acct, region, st, scanID, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	// A refused row may be one a failed write lost, so nothing is warned
	// about then.
	if !errors.Is(err, store.ErrStoreWrite) {
		notReady.warn(st, acct, region)
	}
	return total, inserted, err
}

// memDBTags reads a row's tags with ListTags, sent the row's NativeID, the
// ARN its listing returned. ListTags returns every tag in one response (it
// takes no page token). Each phase sets notFound to the one not-found code
// ListTags models for its type.
//
// An AWS-owned row (ManagedByProvider: the open-access ACL, a default.*
// parameter group) is not read: every region lists them, whether the op
// accepts their ARNs is unverified, and a refusal would fail every scan. Such
// a row keeps the TagsJSON its builder set.
func memDBTags(client memorydbAPI) rowTagReader {
	return rowTagReader{
		op: memDBTagsOp,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if r.ManagedByProvider {
				return r.TagsJSON, nil
			}
			arn := r.NativeID
			out, err := client.ListTags(ctx, &memorydb.ListTagsInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.TagList, func(t memorydbtypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// memDBClustersNotReady keeps a cluster row untagged when ListTags refuses
// it with InvalidClusterStateFault, the code the op models for a cluster
// whose state refuses the call, and warns once after the scan instead of
// failing it: failing would fail every scan for as long as the cluster stays
// in that state. Which states get it is unverified. The code on any other
// type, and every other error, takes the usual path. The reads run
// concurrently, so the tally keeps the smallest ARN.
type memDBClustersNotReady struct{ skipTally }

// tags is tags with a refused cluster read giving no tags and no error,
// tallied in d.
func (d *memDBClustersNotReady) tags(tags rowTagReader) rowTagReader {
	read := tags.read
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		got, err := read(ctx, r)
		if err != nil && r.Type == TypeMemoryDBCluster && isAPIErrorCode(err, memDBClusterNotReady) {
			d.skippedLeast(r.NativeID, err)
			return nil, nil
		}
		return got, err
	}
	return tags
}

// warn reports the refused clusters once. Call it only once the rows are
// written.
func (d *memDBClustersNotReady) warn(st *store.Store, acct *account, region string) {
	d.warnCode(st, memDBTagsOp, acct, region, memDBClusterNotReady, "cluster(s) stored without tags",
		"the cluster's state refused the tag read")
}

// memDBRow returns r, the row of a listed element stored under its ARN, or
// no row when arn is empty: one empty NativeID would merge every such element
// into one resource. what names the elements in the warning, name the
// element.
func memDBRow(what string, arn, name *string, r *store.Resource) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, what + " have no ARN", cmp.Or(sv(name), memDBNoIdentifier)
	}
	return r, "", ""
}

// memDBMultiRegionGap reports whether err is how a region without
// multi-region support rejects the multi-region listings:
// InvalidParameterValueException "This API operation is currently
// unavailable".
func memDBMultiRegionGap(err error) bool {
	return isAPIErrorWithMessage(err, "InvalidParameterValueException", "currently unavailable")
}

func scanMemDBACLs(ctx context.Context, client memorydbAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	tags.notFound = []string{"ACLNotFoundFault"}
	return storeTokenPagesTags(ctx, st, acct, region, "memorydb:DescribeACLs",
		func(ctx context.Context, token *string) ([]memorydbtypes.ACL, *string, error) {
			out, err := client.DescribeACLs(ctx, &memorydb.DescribeACLsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ACLs, out.NextToken, nil
		},
		func(a memorydbtypes.ACL) (*store.Resource, string, string) {
			return memDBRow("ACLs", a.ARN, a.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMemoryDBACL, NativeID: sv(a.ARN),
				Name: a.Name, Region: &region, Status: a.Status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				// Name "open-access" is the AWS-managed default ACL present
				// in every account.
				ManagedByProvider: sv(a.Name) == "open-access",
			})
		}, tags, p)
}

func scanMemDBClusters(ctx context.Context, client memorydbAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	tags.notFound = []string{"ClusterNotFoundFault"}
	return storeTokenPagesTags(ctx, st, acct, region, "memorydb:DescribeClusters",
		func(ctx context.Context, token *string) ([]memorydbtypes.Cluster, *string, error) {
			out, err := client.DescribeClusters(ctx, &memorydb.DescribeClustersInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Clusters, out.NextToken, nil
		},
		func(c memorydbtypes.Cluster) (*store.Resource, string, string) {
			return memDBRow("clusters", c.ARN, c.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMemoryDBCluster, NativeID: sv(c.ARN),
				Name: c.Name, Region: &region, Status: c.Status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanMemDBMultiRegionClusters stores the multi-region clusters listed in
// region. A multi-region cluster's ARN names no region and every member
// region lists it, so its row has no Region: each region listing it stores
// the same element, and the stores after the first change nothing. That costs
// one tag read per listing region per multi-region cluster, which are few. A
// tag read that fails in one region only (a region-specific deny, say) stores
// different TagsJSON within one scan and version-splits the row; that is
// accepted as rare. A region without multi-region support rejects the first
// page (memDBMultiRegionGap), which is an empty listing; the same answer on a
// later page is an error.
func scanMemDBMultiRegionClusters(ctx context.Context, client memorydbAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	tags.notFound = []string{"MultiRegionClusterNotFoundFault"}
	return storeTokenPagesTags(ctx, st, acct, region, "memorydb:DescribeMultiRegionClusters",
		firstPageGapPages(memDBMultiRegionGap, func(ctx context.Context, token *string) ([]memorydbtypes.MultiRegionCluster, *string, error) {
			out, err := client.DescribeMultiRegionClusters(ctx, &memorydb.DescribeMultiRegionClustersInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.MultiRegionClusters, out.NextToken, nil
		}),
		func(c memorydbtypes.MultiRegionCluster) (*store.Resource, string, string) {
			return memDBRow("multi-region clusters", c.ARN, c.MultiRegionClusterName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMemoryDBMultiRegionCluster, NativeID: sv(c.ARN),
				Name: c.MultiRegionClusterName, Status: c.Status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}, tags, p)
}

func scanMemDBParameterGroups(ctx context.Context, client memorydbAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	tags.notFound = []string{"ParameterGroupNotFoundFault"}
	return storeTokenPagesTags(ctx, st, acct, region, "memorydb:DescribeParameterGroups",
		func(ctx context.Context, token *string) ([]memorydbtypes.ParameterGroup, *string, error) {
			out, err := client.DescribeParameterGroups(ctx, &memorydb.DescribeParameterGroupsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ParameterGroups, out.NextToken, nil
		},
		func(pg memorydbtypes.ParameterGroup) (*store.Resource, string, string) {
			return memDBRow("parameter groups", pg.ARN, pg.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMemoryDBParameterGroup, NativeID: sv(pg.ARN),
				Name: pg.Name, Region: &region,
				AttributesJSON: mustJSON(pg), DiscoveredBy: scanID,
				// Names prefixed "default." (e.g. default.redis7) are the
				// AWS-managed default parameter groups present in every region.
				ManagedByProvider: strings.HasPrefix(sv(pg.Name), "default."),
			})
		}, tags, p)
}

// scanMemDBReservedNodes stores the reserved nodes without tags. TagResource
// accepts a reserved node's ARN, but the Service Reference does not list
// reservednode under ListTags and ListTags models no not-found for it, so a
// tag read is not known to work; a refusal would fail every scan of an
// account with reserved nodes. The listing returns no tags either.
func scanMemDBReservedNodes(ctx context.Context, client memorydbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "memorydb:DescribeReservedNodes",
		func(ctx context.Context, token *string) ([]memorydbtypes.ReservedNode, *string, error) {
			out, err := client.DescribeReservedNodes(ctx, &memorydb.DescribeReservedNodesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ReservedNodes, out.NextToken, nil
		},
		func(n memorydbtypes.ReservedNode) (*store.Resource, string, string) {
			label := sv(n.ReservationId)
			return memDBRow("reserved nodes", n.ARN, n.ReservationId, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMemoryDBReservedNode, NativeID: sv(n.ARN),
				Name: &label, Region: &region, Status: n.State,
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		})
}

func scanMemDBSnapshots(ctx context.Context, client memorydbAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	tags.notFound = []string{"SnapshotNotFoundFault"}
	return storeTokenPagesTags(ctx, st, acct, region, "memorydb:DescribeSnapshots",
		func(ctx context.Context, token *string) ([]memorydbtypes.Snapshot, *string, error) {
			out, err := client.DescribeSnapshots(ctx, &memorydb.DescribeSnapshotsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Snapshots, out.NextToken, nil
		},
		func(s memorydbtypes.Snapshot) (*store.Resource, string, string) {
			return memDBRow("snapshots", s.ARN, s.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMemoryDBSnapshot, NativeID: sv(s.ARN),
				Name: s.Name, Region: &region, Status: s.Status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanMemDBMultiRegionParameterGroups stores the multi-region parameter
// groups, which are not taggable. Their ARN names no region, so the row has
// none. Unlike a multi-region cluster a group names no member regions, and a
// region cannot tell whether a region before it lists the group too, so every
// region listing it stores it; with no Region and the same element, the
// later stores change nothing. A region without multi-region support rejects
// the first page as multi-region clusters do (memDBMultiRegionGap).
func scanMemDBMultiRegionParameterGroups(ctx context.Context, client memorydbAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "memorydb:DescribeMultiRegionParameterGroups",
		firstPageGapPages(memDBMultiRegionGap, func(ctx context.Context, token *string) ([]memorydbtypes.MultiRegionParameterGroup, *string, error) {
			out, err := client.DescribeMultiRegionParameterGroups(ctx, &memorydb.DescribeMultiRegionParameterGroupsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.MultiRegionParameterGroups, out.NextToken, nil
		}),
		func(pg memorydbtypes.MultiRegionParameterGroup) (*store.Resource, string, string) {
			return memDBRow("multi-region parameter groups", pg.ARN, pg.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMemoryDBMultiRegionParameterGroup, NativeID: sv(pg.ARN),
				Name:              pg.Name,
				ManagedByProvider: strings.HasPrefix(sv(pg.Name), "default."),
				AttributesJSON:    mustJSON(pg), DiscoveredBy: scanID,
			})
		})
}

func scanMemDBSubnetGroups(ctx context.Context, client memorydbAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	tags.notFound = []string{"SubnetGroupNotFoundFault"}
	return storeTokenPagesTags(ctx, st, acct, region, "memorydb:DescribeSubnetGroups",
		func(ctx context.Context, token *string) ([]memorydbtypes.SubnetGroup, *string, error) {
			out, err := client.DescribeSubnetGroups(ctx, &memorydb.DescribeSubnetGroupsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.SubnetGroups, out.NextToken, nil
		},
		func(s memorydbtypes.SubnetGroup) (*store.Resource, string, string) {
			return memDBRow("subnet groups", s.ARN, s.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMemoryDBSubnetGroup, NativeID: sv(s.ARN),
				Name: s.Name, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}, tags, p)
}

func scanMemDBUsers(ctx context.Context, client memorydbAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	tags.notFound = []string{"UserNotFoundFault"}
	return storeTokenPagesTags(ctx, st, acct, region, "memorydb:DescribeUsers",
		func(ctx context.Context, token *string) ([]memorydbtypes.User, *string, error) {
			out, err := client.DescribeUsers(ctx, &memorydb.DescribeUsersInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Users, out.NextToken, nil
		},
		func(u memorydbtypes.User) (*store.Resource, string, string) {
			return memDBRow("users", u.ARN, u.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMemoryDBUser, NativeID: sv(u.ARN),
				Name: u.Name, Region: &region, Status: u.Status,
				AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
			})
		}, tags, p)
}
