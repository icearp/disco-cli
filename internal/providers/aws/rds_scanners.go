package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRDSDBCluster, Service: "rds", Redact: []redact.Rule{{Path: "MasterUserPassword", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeRDSDBInstance, Service: "rds", Redact: []redact.Rule{{Path: "MasterUserPassword", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeRDSGlobalCluster, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSDBClusterParameterGroup, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSDBParameterGroup, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSDBSubnetGroup, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSDBSecurityGroup, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSOptionGroup, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSEventSubscription, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSIntegration, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSDBProxy, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSDBProxyEndpoint, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSDBProxyTargetGroup, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSDBShardGroup, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSCustomDBEngineVersion, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSSnapshot, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSClusterSnapshot, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSClusterEndpoint, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSReservedInstance, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSAutoBackup, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSClusterAutoBackup, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSTenantDatabase, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSSnapshotTenantDatabase, Service: "rds"})
	registerType(restype.Descriptor{Type: TypeRDSDeployment, Service: "rds"})
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
	DescribeDBSnapshotAttributes(context.Context, *rds.DescribeDBSnapshotAttributesInput, ...func(*rds.Options)) (*rds.DescribeDBSnapshotAttributesOutput, error)
	DescribeDBClusterSnapshotAttributes(context.Context, *rds.DescribeDBClusterSnapshotAttributesInput, ...func(*rds.Options)) (*rds.DescribeDBClusterSnapshotAttributesOutput, error)
	ListTagsForResource(context.Context, *rds.ListTagsForResourceInput, ...func(*rds.Options)) (*rds.ListTagsForResourceOutput, error)
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
	total, inserted, _, err = rdsPageScanParents(ctx, iamAction, acct, region, st, pager,
		func(page P) ([]*store.Resource, []childParent) { return toResources(page), nil })
	return total, inserted, err
}

// rdsPageScanParents is rdsPageScan whose toRows also names, among the rows it
// builds, the parents a later phase reads; it returns the parents of the
// pages whose rows were upserted.
func rdsPageScanParents[P any](
	ctx context.Context,
	iamAction string,
	acct *account,
	region string,
	st *store.Store,
	pager rdsPager[P],
	toRows func(P) ([]*store.Resource, []childParent),
) (total, inserted int, parents []childParent, err error) {
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, parents, skipIfAccessDenied(st, iamAction, acct.ID, region, err)
			}
			return total, inserted, parents, fmt.Errorf("%s: %w", iamAction, err)
		}
		batch, pageParents := toRows(page)
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, parents, fmt.Errorf("upsert %s: %w", iamAction, err)
			}
			total += len(batch)
			inserted += n
		}
		parents = append(parents, pageParents...)
	}
	return
}

// scanRDS discovers all RDS resources in one region. The listings run as
// concurrent families (runFamilies): DB proxies then their target groups, and
// each other listing on its own. A family's error, a failed write included,
// does not stop the others. The snapshot-attribute phases run after them,
// over only the manual snapshots stored before that point. The types whose
// listing returns no tags have them read with ListTagsForResource (rdsTags),
// fanoutMed reads at a time across every family, and every tag denial of the
// scan is one warning.
func scanRDS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := rds.NewFromConfig(acct.cfg, func(o *rds.Options) { o.Region = region })
	tags := rdsTags(client).boundedBy(make(chan struct{}, fanoutMed)).sharingDenials()
	var defaults rdsDefaultGroupRefusals
	groupTags := defaults.tags(tags)
	var snapshots, clusterSnapshots []childParent
	p := runFamilies(ctx, "rds",
		countedFamily("rds:DescribeDBInstances", func() (int, int, error) {
			return scanDBInstances(ctx, client, acct, region, st, scanID)
		}),
		countedFamily("rds:DescribeDBClusters", func() (int, int, error) {
			return scanDBClusters(ctx, client, acct, region, st, scanID)
		}),
		phaseFamily(func(p *phaseTotals) {
			scanDBClusterParameterGroups(ctx, client, acct, region, st, scanID, groupTags, p)
		}),
		phaseFamily(func(p *phaseTotals) { scanDBParameterGroups(ctx, client, acct, region, st, scanID, groupTags, p) }),
		phaseFamily(func(p *phaseTotals) { scanDBProxiesAndTargetGroups(ctx, client, acct, region, st, scanID, tags, p) }),
		phaseFamily(func(p *phaseTotals) { scanDBProxyEndpoints(ctx, client, acct, region, st, scanID, tags, p) }),
		phaseFamily(func(p *phaseTotals) { scanDBSecurityGroups(ctx, client, acct, region, st, scanID, tags, p) }),
		countedFamily("rds:DescribeDBShardGroups", func() (int, int, error) {
			return scanDBShardGroups(ctx, client, acct, region, st, scanID)
		}),
		phaseFamily(func(p *phaseTotals) { scanDBSubnetGroups(ctx, client, acct, region, st, scanID, tags, p) }),
		phaseFamily(func(p *phaseTotals) { scanEventSubscriptions(ctx, client, acct, region, st, scanID, tags, p) }),
		countedFamily("rds:DescribeGlobalClusters", func() (int, int, error) {
			return scanGlobalClusters(ctx, client, acct, region, st, scanID)
		}),
		countedFamily("rds:DescribeIntegrations", func() (int, int, error) {
			return scanIntegrations(ctx, client, acct, region, st, scanID)
		}),
		phaseFamily(func(p *phaseTotals) { scanOptionGroups(ctx, client, acct, region, st, scanID, groupTags, p) }),
		countedFamily("rds:DescribeDBEngineVersions", func() (int, int, error) {
			return scanCustomDBEngineVersions(ctx, client, acct, region, st, scanID)
		}),
		countedFamily("rds:DescribeDBSnapshots", func() (t, i int, err error) {
			t, i, snapshots, err = scanDBSnapshots(ctx, client, acct, region, st, scanID)
			return t, i, err
		}),
		countedFamily("rds:DescribeDBClusterSnapshots", func() (t, i int, err error) {
			t, i, clusterSnapshots, err = scanDBClusterSnapshots(ctx, client, acct, region, st, scanID)
			return t, i, err
		}),
		phaseFamily(func(p *phaseTotals) { scanDBClusterEndpoints(ctx, client, acct, region, st, scanID, tags, p) }),
		phaseFamily(func(p *phaseTotals) { scanReservedDBInstances(ctx, client, acct, region, st, scanID, tags, p) }),
		countedFamily("rds:DescribeDBInstanceAutomatedBackups", func() (int, int, error) {
			return scanDBInstanceAutomatedBackups(ctx, client, acct, region, st, scanID)
		}),
		countedFamily("rds:DescribeDBClusterAutomatedBackups", func() (int, int, error) {
			return scanDBClusterAutomatedBackups(ctx, client, acct, region, st, scanID)
		}),
		countedFamily("rds:DescribeTenantDatabases", func() (int, int, error) {
			return scanTenantDatabases(ctx, client, acct, region, st, scanID)
		}),
		countedFamily("rds:DescribeDBSnapshotTenantDatabases", func() (int, int, error) {
			return scanDBSnapshotTenantDatabases(ctx, client, acct, region, st, scanID)
		}),
		countedFamily("rds:DescribeBlueGreenDeployments", func() (int, int, error) {
			return scanBlueGreenDeployments(ctx, client, acct, region, st, scanID)
		}),
	)
	tags.warnDenials(st, acct, region)
	writeFailed := errors.Is(opErrs{p.errs}, store.ErrStoreWrite)
	if !writeFailed {
		defaults.warn(st, acct, region)
	}
	// One attribute phase at a time, so at most fanoutMed attribute calls are
	// in flight. Their transient per-snapshot errors are already warnings
	// (childFanOutSkipTransient). A failed write stops the scan, since every
	// later write would fail too.
	if !writeFailed {
		t, i, err := scanDBSnapshotAttributes(ctx, client, acct, region, st, scanID, snapshots)
		if !p.addPhase(rdsSnapshotAttributesOp, t, i, err) {
			t, i, err = scanDBClusterSnapshotAttributes(ctx, client, acct, region, st, scanID, clusterSnapshots)
			p.addPhase(rdsClusterSnapshotAttributesOp, t, i, err)
		}
	}
	p.foldStop(ctx, "rds", 0)
	// Every phase calls the one RDS host, so once any stored rows an NXDOMAIN
	// is a failure of this scan, not a missing endpoint.
	return p.resultLateNXDOMAIN(st, acct, region)
}

const (
	rdsTagsOp = "rds:ListTagsForResource"
	// rdsPageCap is the MaxRecords maximum of every RDS Describe whose rows
	// have their tags read.
	rdsPageCap = 100
	// rdsNoIdentifier names, in a warning, a listed element with neither an
	// ARN nor a name.
	rdsNoIdentifier = "(an element with no identifier)"
)

// rdsTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its listing returned. The Service Reference lists every
// ARN resource type read here under ListTagsForResource (cluster-pg, pg,
// proxy, proxy-endpoint, target-group, secgrp, subgrp, es, og,
// cluster-endpoint, ri). The op returns every tag in one response (it takes
// no page token; its Filters "isn't currently supported").
//
// The op models a not-found code only for some types (rdsTagsNotFound), so a
// row of another type deleted between its listing and its tag read fails the
// read as an error, and the row is kept untagged.
func rdsTags(client rdsAPI) rowTagReader {
	return rowTagReader{
		op: rdsTagsOp,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &rds.ListTagsForResourceInput{ResourceName: &r.NativeID})
			if err != nil {
				return nil, err
			}
			return awsTagsJSON(out.TagList), nil
		},
	}
}

// rdsIsDefaultGroup reports whether name is that of an AWS-created default
// group: parameter and cluster parameter groups are named default.<family>
// (default.mysql8.0), option groups default:<engine>-<major>-<minor>
// (default:mysql-8-0). The prefix alone decides it because the Create and
// Copy ops of DBParameterGroup, DBClusterParameterGroup and OptionGroup allow
// only letters, digits and hyphens, starting with a letter: no name a
// customer gives can contain "." or ":".
func rdsIsDefaultGroup(name string) bool {
	return strings.HasPrefix(name, "default.") || strings.HasPrefix(name, "default:")
}

// rdsDefaultGroupRefusalCodes are the codes a refusal to read a default
// group's tags would come as: the op's generic bad-request codes. Whether
// ListTagsForResource refuses a default group at all, and under which code,
// is unverified: nothing in the model or docs says so, and no live call has
// tested it.
var rdsDefaultGroupRefusalCodes = []string{"InvalidParameterValue", "InvalidParameterCombination"}

// rdsDefaultGroupRefusals keeps the default groups' rows whose tag read was
// refused (rdsDefaultGroupRefusalCodes). Every region lists default groups,
// so such a refusal returned as an error would fail every RDS scan; it keeps
// the row untagged and warns once per scan instead. Any other error on a
// default group, and every error on a non-default one, takes the usual path.
type rdsDefaultGroupRefusals struct{ skipTally }

// tags is tags with a default group's refused read giving no tags and no
// error, tallied in d. The reads run concurrently across families, so the
// tally keeps the smallest ARN, not the first to arrive.
func (d *rdsDefaultGroupRefusals) tags(tags rowTagReader) rowTagReader {
	read := tags.read
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		got, err := read(ctx, r)
		if err != nil && rdsIsDefaultGroup(sv(r.Name)) && isAPIErrorCode(err, rdsDefaultGroupRefusalCodes...) {
			d.skippedLeast(r.NativeID, err)
			return nil, nil
		}
		return got, err
	}
	return tags
}

// warn reports the default groups stored untagged, once, naming the
// smallest one's ARN. Call it only once their rows are written.
func (d *rdsDefaultGroupRefusals) warn(st *store.Store, acct *account, region string) {
	if d.count == 0 {
		return
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws", Service: rdsTagsOp, Scope: acct.ID + "/" + region,
		Message: fmt.Sprintf("%d default parameter or option group(s) stored without tags, first %s: %v", d.count, d.first, d.err),
	})
}

// rdsTagsNotFound is tags dropping a row whose read fails with code, the
// not-found code ListTagsForResource models for that row's type.
func rdsTagsNotFound(tags rowTagReader, code string) rowTagReader {
	tags.notFound = []string{code}
	return tags
}

// rdsARNRow returns r, the row of a listed element stored under its ARN, or
// no row when arn is empty: one empty NativeID would merge every such element
// into one resource. what names the elements in the warning, key the element.
func rdsARNRow(what string, arn *string, key string, r *store.Resource) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, what + " have no ARN", cmp.Or(key, rdsNoIdentifier)
	}
	return r, "", ""
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

func scanDBClusterParameterGroups(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) {
	storeTokenPagesTags(ctx, st, acct, region, "rds:DescribeDBClusterParameterGroups",
		func(ctx context.Context, marker *string) ([]rdstypes.DBClusterParameterGroup, *string, error) {
			out, err := client.DescribeDBClusterParameterGroups(ctx, &rds.DescribeDBClusterParameterGroupsInput{MaxRecords: aws.Int32(rdsPageCap), Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.DBClusterParameterGroups, out.Marker, nil
		},
		func(pg rdstypes.DBClusterParameterGroup) (*store.Resource, string, string) {
			name := sv(pg.DBClusterParameterGroupName)
			return rdsARNRow("DB cluster parameter groups", pg.DBClusterParameterGroupArn, name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRDSDBClusterParameterGroup, NativeID: sv(pg.DBClusterParameterGroupArn),
				Name: &name, Region: &region, AttributesJSON: mustJSON(pg),
				DiscoveredBy: scanID,
			})
		}, tags, p)
}

func scanDBParameterGroups(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) {
	storeTokenPagesTags(ctx, st, acct, region, "rds:DescribeDBParameterGroups",
		func(ctx context.Context, marker *string) ([]rdstypes.DBParameterGroup, *string, error) {
			out, err := client.DescribeDBParameterGroups(ctx, &rds.DescribeDBParameterGroupsInput{MaxRecords: aws.Int32(rdsPageCap), Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.DBParameterGroups, out.Marker, nil
		},
		func(pg rdstypes.DBParameterGroup) (*store.Resource, string, string) {
			name := sv(pg.DBParameterGroupName)
			return rdsARNRow("DB parameter groups", pg.DBParameterGroupArn, name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRDSDBParameterGroup, NativeID: sv(pg.DBParameterGroupArn),
				Name: &name, Region: &region, AttributesJSON: mustJSON(pg),
				DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanDBProxiesAndTargetGroups stores the DB proxies, then the target groups
// of each proxy stored (DescribeDBProxyTargetGroups requires a DBProxyName).
// The proxies are few (a per-region quota), so their listing is held whole.
// A proxy deleted after it was listed (DBProxyNotFoundFault, which the op
// models) has no target groups to read; one proxy's failure costs the others
// none of theirs.
func scanDBProxiesAndTargetGroups(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) {
	proxies, _ := storeParentListTags(ctx, st, acct, region, "rds:DescribeDBProxies", "DB proxies", "DBProxyName",
		func(ctx context.Context, marker *string) ([]rdstypes.DBProxy, *string, error) {
			out, err := client.DescribeDBProxies(ctx, &rds.DescribeDBProxiesInput{MaxRecords: aws.Int32(rdsPageCap), Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.DBProxies, out.Marker, nil
		},
		func(px rdstypes.DBProxy) (*store.Resource, string, string) {
			name := sv(px.DBProxyName)
			status := string(px.Status)
			return rdsARNRow("DB proxies", px.DBProxyArn, name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRDSDBProxy, NativeID: sv(px.DBProxyArn), Name: &name,
				Region: &region, CreatedAt: tp(px.CreatedDate), Status: &status,
				AttributesJSON: mustJSON(px), DiscoveredBy: scanID,
			})
		},
		func(px rdstypes.DBProxy) string { return sv(px.DBProxyName) },
		rdsTagsNotFound(tags, "DBProxyNotFoundFault"), p)
	// After a failed write storeParentListTags returns no proxies, so no
	// target group is listed.
	storeEachParentTags(ctx, st, acct, region, "rds:DescribeDBProxyTargetGroups", []string{"DBProxyNotFoundFault"}, proxies,
		func(ctx context.Context, proxy string, marker *string) ([]rdstypes.DBProxyTargetGroup, *string, error) {
			out, err := client.DescribeDBProxyTargetGroups(ctx, &rds.DescribeDBProxyTargetGroupsInput{DBProxyName: &proxy, MaxRecords: aws.Int32(rdsPageCap), Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.TargetGroups, out.Marker, nil
		},
		func(_ string, tg rdstypes.DBProxyTargetGroup) (*store.Resource, string, string) {
			name := sv(tg.TargetGroupName)
			return rdsARNRow("DB proxy target groups", tg.TargetGroupArn, name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRDSDBProxyTargetGroup, NativeID: sv(tg.TargetGroupArn),
				Name: &name, Region: &region, Status: tg.Status,
				AttributesJSON: mustJSON(tg), DiscoveredBy: scanID,
			})
		},
		rdsTagsNotFound(tags, "DBProxyTargetGroupNotFoundFault"), p)
}

func scanDBProxyEndpoints(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) {
	storeTokenPagesTags(ctx, st, acct, region, "rds:DescribeDBProxyEndpoints",
		func(ctx context.Context, marker *string) ([]rdstypes.DBProxyEndpoint, *string, error) {
			out, err := client.DescribeDBProxyEndpoints(ctx, &rds.DescribeDBProxyEndpointsInput{MaxRecords: aws.Int32(rdsPageCap), Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.DBProxyEndpoints, out.Marker, nil
		},
		func(ep rdstypes.DBProxyEndpoint) (*store.Resource, string, string) {
			name := sv(ep.DBProxyEndpointName)
			status := string(ep.Status)
			return rdsARNRow("DB proxy endpoints", ep.DBProxyEndpointArn, name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRDSDBProxyEndpoint, NativeID: sv(ep.DBProxyEndpointArn),
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(ep), DiscoveredBy: scanID,
			})
		}, rdsTagsNotFound(tags, "DBProxyEndpointNotFoundFault"), p)
}

func scanDBSecurityGroups(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) {
	storeTokenPagesTags(ctx, st, acct, region, "rds:DescribeDBSecurityGroups",
		func(ctx context.Context, marker *string) ([]rdstypes.DBSecurityGroup, *string, error) {
			out, err := client.DescribeDBSecurityGroups(ctx, &rds.DescribeDBSecurityGroupsInput{MaxRecords: aws.Int32(rdsPageCap), Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.DBSecurityGroups, out.Marker, nil
		},
		func(sg rdstypes.DBSecurityGroup) (*store.Resource, string, string) {
			name := sv(sg.DBSecurityGroupName)
			return rdsARNRow("DB security groups", sg.DBSecurityGroupArn, name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRDSDBSecurityGroup, NativeID: sv(sg.DBSecurityGroupArn),
				Name: &name, Region: &region, AttributesJSON: mustJSON(sg),
				DiscoveredBy: scanID,
			})
		}, tags, p)
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

func scanDBSubnetGroups(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) {
	storeTokenPagesTags(ctx, st, acct, region, "rds:DescribeDBSubnetGroups",
		func(ctx context.Context, marker *string) ([]rdstypes.DBSubnetGroup, *string, error) {
			out, err := client.DescribeDBSubnetGroups(ctx, &rds.DescribeDBSubnetGroupsInput{MaxRecords: aws.Int32(rdsPageCap), Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.DBSubnetGroups, out.Marker, nil
		},
		func(sg rdstypes.DBSubnetGroup) (*store.Resource, string, string) {
			name := sv(sg.DBSubnetGroupName)
			return rdsARNRow("DB subnet groups", sg.DBSubnetGroupArn, name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRDSDBSubnetGroup, NativeID: sv(sg.DBSubnetGroupArn),
				Name: &name, Region: &region, Status: sg.SubnetGroupStatus,
				AttributesJSON: mustJSON(sg), DiscoveredBy: scanID,
			})
		}, tags, p)
}

func scanEventSubscriptions(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) {
	storeTokenPagesTags(ctx, st, acct, region, "rds:DescribeEventSubscriptions",
		func(ctx context.Context, marker *string) ([]rdstypes.EventSubscription, *string, error) {
			out, err := client.DescribeEventSubscriptions(ctx, &rds.DescribeEventSubscriptionsInput{MaxRecords: aws.Int32(rdsPageCap), Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.EventSubscriptionsList, out.Marker, nil
		},
		func(sub rdstypes.EventSubscription) (*store.Resource, string, string) {
			name := sv(sub.CustSubscriptionId)
			return rdsARNRow("event subscriptions", sub.EventSubscriptionArn, name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRDSEventSubscription, NativeID: sv(sub.EventSubscriptionArn),
				Name: &name, Region: &region, Status: sub.Status,
				AttributesJSON: mustJSON(sub), DiscoveredBy: scanID,
			})
		}, tags, p)
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

func scanOptionGroups(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) {
	storeTokenPagesTags(ctx, st, acct, region, "rds:DescribeOptionGroups",
		func(ctx context.Context, marker *string) ([]rdstypes.OptionGroup, *string, error) {
			out, err := client.DescribeOptionGroups(ctx, &rds.DescribeOptionGroupsInput{MaxRecords: aws.Int32(rdsPageCap), Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.OptionGroupsList, out.Marker, nil
		},
		func(og rdstypes.OptionGroup) (*store.Resource, string, string) {
			name := sv(og.OptionGroupName)
			return rdsARNRow("option groups", og.OptionGroupArn, name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRDSOptionGroup, NativeID: sv(og.OptionGroupArn),
				Name: &name, Region: &region, AttributesJSON: mustJSON(og),
				DiscoveredBy: scanID,
			})
		}, tags, p)
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

// scanDBSnapshots stores the snapshots the account owns (the op leaves out
// shared and public ones unless asked) and returns the stored manual ones as
// parents for the attribute phase.
func scanDBSnapshots(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, manual []childParent, err error) {
	return rdsPageScanParents(
		ctx, "rds:DescribeDBSnapshots", acct, region, st,
		rds.NewDescribeDBSnapshotsPaginator(client, &rds.DescribeDBSnapshotsInput{}),
		func(page *rds.DescribeDBSnapshotsOutput) (rows []*store.Resource, manual []childParent) {
			for _, s := range page.DBSnapshots {
				name, arn := sv(s.DBSnapshotIdentifier), sv(s.DBSnapshotArn)
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSSnapshot, NativeID: arn,
					Name: &name, Region: &region, CreatedAt: tp(s.SnapshotCreateTime),
					Status: s.Status, TagsJSON: awsTagsJSON(s.TagList),
					AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
				})
				if sv(s.SnapshotType) == rdsManualSnapshot && name != "" && arn != "" {
					manual = append(manual, childParent{id: name, arn: arn})
				}
			}
			return rows, manual
		},
	)
}

// scanDBClusterSnapshots stores the cluster snapshots the account owns (the op
// leaves out shared and public ones unless asked) and returns the stored
// manual ones as parents for the attribute phase.
func scanDBClusterSnapshots(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, manual []childParent, err error) {
	return rdsPageScanParents(
		ctx, "rds:DescribeDBClusterSnapshots", acct, region, st,
		rds.NewDescribeDBClusterSnapshotsPaginator(client, &rds.DescribeDBClusterSnapshotsInput{}),
		func(page *rds.DescribeDBClusterSnapshotsOutput) (rows []*store.Resource, manual []childParent) {
			for _, s := range page.DBClusterSnapshots {
				name, arn := sv(s.DBClusterSnapshotIdentifier), sv(s.DBClusterSnapshotArn)
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeRDSClusterSnapshot, NativeID: arn,
					Name: &name, Region: &region, CreatedAt: tp(s.SnapshotCreateTime),
					Status: s.Status, TagsJSON: awsTagsJSON(s.TagList),
					AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
				})
				if sv(s.SnapshotType) == rdsManualSnapshot && name != "" && arn != "" {
					manual = append(manual, childParent{id: name, arn: arn})
				}
			}
			return rows, manual
		},
	)
}

// scanDBClusterEndpoints stores every endpoint the op lists. Aurora's
// built-in WRITER and READER endpoints are listed without an ARN or an
// identifier: they are no ARN resource of their own (the cluster's row
// carries them as Endpoint and ReaderEndpoint), so they have no tags to read.
// They are stored untagged under the NativeID "", so every built-in endpoint
// of the account collapses into one row (a known defect: they need an id of
// their own). Custom endpoints carry their ARN and have their tags read.
func scanDBClusterEndpoints(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) {
	withARN := tags
	withARN.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		if r.NativeID == "" {
			return nil, nil
		}
		return tags.read(ctx, r)
	}
	storeTokenPagesTags(ctx, st, acct, region, "rds:DescribeDBClusterEndpoints",
		func(ctx context.Context, marker *string) ([]rdstypes.DBClusterEndpoint, *string, error) {
			out, err := client.DescribeDBClusterEndpoints(ctx, &rds.DescribeDBClusterEndpointsInput{MaxRecords: aws.Int32(rdsPageCap), Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.DBClusterEndpoints, out.Marker, nil
		},
		func(ep rdstypes.DBClusterEndpoint) (*store.Resource, string, string) {
			name := sv(ep.DBClusterEndpointIdentifier)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRDSClusterEndpoint, NativeID: sv(ep.DBClusterEndpointArn),
				Name: &name, Region: &region, Status: ep.Status,
				AttributesJSON: mustJSON(ep), DiscoveredBy: scanID,
			}, "", ""
		}, withARN, p)
}

// scanReservedDBInstances discovers reserved DB instance purchases. These carry
// no graph edges (billing artefacts) so they are leaf resources. A purchase
// listed without its ARN is stored, and its tags read, under the ARN the
// Service Reference's ri format gives for its id.
func scanReservedDBInstances(ctx context.Context, client rdsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) {
	storeTokenPagesTags(ctx, st, acct, region, "rds:DescribeReservedDBInstances",
		func(ctx context.Context, marker *string) ([]rdstypes.ReservedDBInstance, *string, error) {
			out, err := client.DescribeReservedDBInstances(ctx, &rds.DescribeReservedDBInstancesInput{MaxRecords: aws.Int32(rdsPageCap), Marker: marker})
			if err != nil {
				return nil, nil, err
			}
			return out.ReservedDBInstances, out.Marker, nil
		},
		func(ri rdstypes.ReservedDBInstance) (*store.Resource, string, string) {
			name := sv(ri.ReservedDBInstanceId)
			nativeID := sv(ri.ReservedDBInstanceArn)
			if nativeID == "" {
				nativeID = rdsARNIn(acct.partition(), region, acct.ID, "ri", name)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeRDSReservedInstance, NativeID: nativeID,
				Name: &name, Region: &region, CreatedAt: tp(ri.StartTime),
				Status: ri.State, AttributesJSON: mustJSON(ri), DiscoveredBy: scanID,
			}, "", ""
		}, tags, p)
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
					TagsJSON: awsTagsJSON(b.TagList), AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
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
					TagsJSON: awsTagsJSON(b.TagList), AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
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
					Type: TypeRDSDeployment, NativeID: rdsARNIn(acct.partition(), region, acct.ID, "deployment", id),
					Name: &name, Region: &region, CreatedAt: tp(d.CreateTime),
					Status: d.Status, TagsJSON: awsTagsJSON(d.TagList),
					AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
				})
			}
			return out
		},
	)
}
