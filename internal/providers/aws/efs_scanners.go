package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEFSFileSystem, Service: "efs"})
	registerType(restype.Descriptor{Type: TypeEFSAccessPoint, Service: "efs"})
	registerType(restype.Descriptor{Type: TypeEFSMountTarget, Service: "efs"})
	registerType(restype.Descriptor{Type: TypeEFSReplicationConfiguration, Service: "efs"})
	registerService(serviceEntry{
		name: "aws:efs",
		fn:   scanEFS,
	})
}

// efsAPI is the narrow set of EFS operations called by scanEFSAll.
type efsAPI interface {
	DescribeFileSystems(context.Context, *efs.DescribeFileSystemsInput, ...func(*efs.Options)) (*efs.DescribeFileSystemsOutput, error)
	DescribeMountTargets(context.Context, *efs.DescribeMountTargetsInput, ...func(*efs.Options)) (*efs.DescribeMountTargetsOutput, error)
	DescribeAccessPoints(context.Context, *efs.DescribeAccessPointsInput, ...func(*efs.Options)) (*efs.DescribeAccessPointsOutput, error)
	DescribeReplicationConfigurations(context.Context, *efs.DescribeReplicationConfigurationsInput, ...func(*efs.Options)) (*efs.DescribeReplicationConfigurationsOutput, error)
}

// scanEFS discovers EFS file systems, mount targets, access points and
// replication configurations in one region. DescribeFileSystems is paginated; mount targets are fetched
// concurrently per file system to minimise wall time.
func scanEFS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := efs.NewFromConfig(acct.cfg, func(o *efs.Options) { o.Region = region })
	return scanEFSAll(ctx, client, acct, region, st, scanID)
}

// scanEFSAll holds the testable scan body. Replication configurations run after
// the file system phases whatever they returned. A transient error on either
// side is demoted to a warning only beside a hard error, so it cannot make the
// dispatcher report the other side's hard error as a transient one.
func scanEFSAll(ctx context.Context, client efsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, fileSystems, err := scanEFSFileSystemsAndChildren(ctx, client, acct, region, st, scanID)
	t, i, rerr := scanEFSReplicationConfigurations(ctx, client, acct, region, st, scanID, fileSystems)
	var errs phaseErrs
	errs.add("elasticfilesystem", err)
	errs.add(efsReplicationConfigurationsOp, rerr)
	return total + t, inserted + i, errs.join(st, acct, region)
}

// scanEFSFileSystemsAndChildren runs the file system, mount target and access point
// phases, stopping at the first hard error, and returns the ARNs of the file
// systems it stored.
func scanEFSFileSystemsAndChildren(ctx context.Context, client efsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, fileSystems map[string]bool, err error) {
	// Phase 1: file systems
	fsPager := efs.NewDescribeFileSystemsPaginator(client, &efs.DescribeFileSystemsInput{})
	var fsIDs, fsARNs []string
	var fsBatch []*store.Resource
	for fsPager.HasMorePages() {
		page, err := fsPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "elasticfilesystem:DescribeFileSystems", acct.ID, region, err)
			}
			return 0, 0, nil, fmt.Errorf("efs:DescribeFileSystems: %w", err)
		}
		for i := range page.FileSystems {
			fs := page.FileSystems[i]
			arn := sv(fs.FileSystemArn)
			status := string(fs.LifeCycleState)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEFSFileSystem,
				NativeID:       arn,
				Name:           fs.Name,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(fs.CreationTime),
				AttributesJSON: mustJSON(fs),
				DiscoveredBy:   scanID,
			}
			if fs.Tags != nil {
				j := mustJSON(fs.Tags)
				r.TagsJSON = &j
			}
			fsBatch = append(fsBatch, r)
			fsIDs = append(fsIDs, sv(fs.FileSystemId))
			fsARNs = append(fsARNs, arn)
		}
	}
	if len(fsBatch) > 0 {
		n, err := st.UpsertResources(fsBatch)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("upsert EFS file systems: %w", err)
		}
		total += len(fsBatch)
		inserted += n
	}
	fileSystems = make(map[string]bool, len(fsARNs))
	for _, a := range fsARNs {
		fileSystems[a] = true
	}

	// Phase 2: mount targets (concurrent per file system)
	var (
		mu      sync.Mutex
		mtBatch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, fsID := range fsIDs {
		g.Go(func() error {
			mtPager := efs.NewDescribeMountTargetsPaginator(client, &efs.DescribeMountTargetsInput{
				FileSystemId: &fsID,
			})
			for mtPager.HasMorePages() {
				page, err := mtPager.NextPage(gctx)
				if err != nil {
					if isAccessDenied(err) {
						return nil
					}
					return fmt.Errorf("efs:DescribeMountTargets %s: %w", fsID, err)
				}
				for i := range page.MountTargets {
					mt := page.MountTargets[i]
					// EFS mount targets have no native ARN; synthesise one.
					nativeID := arnFor(acct.partition(), "elasticfilesystem", region, acct.ID,
						"file-system/"+fsID+"/mount-target/"+sv(mt.MountTargetId))
					status := string(mt.LifeCycleState)
					r := &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeEFSMountTarget,
						NativeID:       nativeID,
						Name:           mt.MountTargetId,
						Region:         &region,
						Status:         &status,
						AttributesJSON: mustJSON(mt),
						DiscoveredBy:   scanID,
					}
					mu.Lock()
					mtBatch = append(mtBatch, r)
					mu.Unlock()
				}
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return total, inserted, fileSystems, err
	}
	if len(mtBatch) > 0 {
		n, err := st.UpsertResources(mtBatch)
		if err != nil {
			return total, inserted, fileSystems, fmt.Errorf("upsert EFS mount targets: %w", err)
		}
		total += len(mtBatch)
		inserted += n
	}

	// Phase 3: access points
	apPager := efs.NewDescribeAccessPointsPaginator(client, &efs.DescribeAccessPointsInput{})
	var apBatch []*store.Resource
	for apPager.HasMorePages() {
		page, err := apPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, fileSystems, nil
			}
			return total, inserted, fileSystems, fmt.Errorf("efs:DescribeAccessPoints: %w", err)
		}
		for i := range page.AccessPoints {
			ap := page.AccessPoints[i]
			arn := sv(ap.AccessPointArn)
			status := string(ap.LifeCycleState)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEFSAccessPoint,
				NativeID:       arn,
				Name:           ap.Name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(ap),
				DiscoveredBy:   scanID,
			}
			if ap.Tags != nil {
				j := mustJSON(ap.Tags)
				r.TagsJSON = &j
			}
			apBatch = append(apBatch, r)
		}
	}
	if len(apBatch) > 0 {
		n, err := st.UpsertResources(apBatch)
		if err != nil {
			return total, inserted, fileSystems, fmt.Errorf("upsert EFS access points: %w", err)
		}
		total += len(apBatch)
		inserted += n
	}

	return total, inserted, fileSystems, nil
}

const efsReplicationConfigurationsOp = "elasticfilesystem:DescribeReplicationConfigurations"

// efsReplicationPageLimit is DescribeReplicationConfigurations' documented
// default page size; the model sets no maximum.
const efsReplicationPageLimit = 100

// efsReplicationConfigurationNativeID names a replication configuration after
// its source file system, which has at most one; the API issues no ARN.
func efsReplicationConfigurationNativeID(sourceFileSystemARN string) string {
	return sourceFileSystemARN + "/replication-configuration"
}

// scanEFSReplicationConfigurations lists the region's replication
// configurations in one unfiltered listing and links each under its source
// file system when fileSystems holds that ARN.
func scanEFSReplicationConfigurations(ctx context.Context, client efsAPI, acct *account, region string, st *store.Store, scanID string, fileSystems map[string]bool) (int, int, error) {
	pager := efs.NewDescribeReplicationConfigurationsPaginator(client, &efs.DescribeReplicationConfigurationsInput{},
		func(o *efs.DescribeReplicationConfigurationsPaginatorOptions) { o.Limit = efsReplicationPageLimit })
	var children []containedChild
	for firstPage := true; pager.HasMorePages(); firstPage = false {
		out, err := pager.NextPage(ctx)
		if err != nil {
			// The model documents ReplicationNotFound as "the specified file
			// system does not have a replication configuration". Whether the
			// unfiltered call returns it for a region with none, or an empty
			// list, is unverified; on the first page either means none.
			if firstPage && isAPIErrorCode(err, "ReplicationNotFound") {
				return 0, 0, nil
			}
			return upsertRegionChildren(st, acct, region, efsReplicationConfigurationsOp, children, err)
		}
		for i := range out.Replications {
			if c, ok := efsReplicationChild(acct, region, scanID, fileSystems, out.Replications[i]); ok {
				children = append(children, c)
			}
		}
	}
	return upsertRegionChildren(st, acct, region, efsReplicationConfigurationsOp, children, nil)
}

// efsReplicationChild builds the row for a configuration whose source file
// system is in this account and region. The destination file system's region
// (and account, when cross-account) may also list the configuration
// (unverified); keeping only the source side stores each configuration once.
func efsReplicationChild(acct *account, region, scanID string, fileSystems map[string]bool, rc efstypes.ReplicationConfigurationDescription) (containedChild, bool) {
	srcARN := sv(rc.SourceFileSystemArn)
	parsed, err := arn.Parse(srcARN)
	if err != nil || parsed.Region != region || parsed.AccountID != acct.ID {
		return containedChild{}, false
	}
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeEFSReplicationConfiguration, NativeID: efsReplicationConfigurationNativeID(srcARN),
		Name: rc.SourceFileSystemId, Region: &region, CreatedAt: tp(rc.CreationTime),
		AttributesJSON: mustJSON(rc), DiscoveredBy: scanID,
	}
	// The model allows only one destination per configuration.
	if len(rc.Destinations) > 0 {
		r.Status = nonEmptyPtr(string(rc.Destinations[0].Status))
	}
	c := containedChild{row: r}
	if fileSystems[srcARN] {
		c.parentID = store.ResourceID("aws", acct.ID, srcARN)
	}
	return c, true
}
