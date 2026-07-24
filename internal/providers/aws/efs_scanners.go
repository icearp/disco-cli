package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEFSFileSystem, Service: "efs", Upstream: "AWS::EFS::FileSystem"})
	registerType(restype.Descriptor{Type: TypeEFSAccessPoint, Service: "efs", Upstream: "AWS::EFS::AccessPoint", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEFSMountTarget, Service: "efs", Upstream: "AWS::EFS::MountTarget"})
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
}

// scanEFS discovers EFS file systems, mount targets, and access points in one
// region. DescribeFileSystems is paginated; mount targets are fetched
// concurrently per file system to minimise wall time.
func scanEFS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := efs.NewFromConfig(acct.cfg, func(o *efs.Options) { o.Region = region })
	return scanEFSAll(ctx, client, acct, region, st, scanID)
}

// scanEFSAll holds the testable scan body.
func scanEFSAll(ctx context.Context, client efsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	// Phase 1: file systems
	fsPager := efs.NewDescribeFileSystemsPaginator(client, &efs.DescribeFileSystemsInput{})
	var fsIDs []string
	var fsBatch []*store.Resource
	for fsPager.HasMorePages() {
		page, err := fsPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "elasticfilesystem:DescribeFileSystems", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("efs:DescribeFileSystems: %w", err)
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
		}
	}
	if len(fsBatch) > 0 {
		n, err := st.UpsertResources(fsBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert EFS file systems: %w", err)
		}
		total += len(fsBatch)
		inserted += n
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
					nativeID := fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:file-system/%s/mount-target/%s",
						region, acct.ID, fsID, sv(mt.MountTargetId))
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
		return 0, 0, err
	}
	if len(mtBatch) > 0 {
		n, err := st.UpsertResources(mtBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert EFS mount targets: %w", err)
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
				return total, inserted, nil
			}
			return 0, 0, fmt.Errorf("efs:DescribeAccessPoints: %w", err)
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
			return 0, 0, fmt.Errorf("upsert EFS access points: %w", err)
		}
		total += len(apBatch)
		inserted += n
	}

	return total, inserted, nil
}
