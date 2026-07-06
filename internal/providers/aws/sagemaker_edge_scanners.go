package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "sagemaker", DiscoType: TypeSageMakerDeviceFleet},
		coverage.TypeDecl{Service: "sagemaker", DiscoType: TypeSageMakerDevice},
		coverage.TypeDecl{Service: "sagemaker", DiscoType: TypeSageMakerImage},
		coverage.TypeDecl{Service: "sagemaker", DiscoType: TypeSageMakerImageVersion},
	)
}

// sagemakerEdgeAPI is the narrow surface used by the Edge / images family.
// Image and ImageVersion are parent-child: ListImages enumerates parents,
// per-image ListImageVersions enumerates children.
type sagemakerEdgeAPI interface {
	ListDeviceFleets(context.Context, *sagemaker.ListDeviceFleetsInput, ...func(*sagemaker.Options)) (*sagemaker.ListDeviceFleetsOutput, error)
	DescribeDeviceFleet(context.Context, *sagemaker.DescribeDeviceFleetInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeDeviceFleetOutput, error)
	ListDevices(context.Context, *sagemaker.ListDevicesInput, ...func(*sagemaker.Options)) (*sagemaker.ListDevicesOutput, error)
	DescribeDevice(context.Context, *sagemaker.DescribeDeviceInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeDeviceOutput, error)
	ListImages(context.Context, *sagemaker.ListImagesInput, ...func(*sagemaker.Options)) (*sagemaker.ListImagesOutput, error)
	DescribeImage(context.Context, *sagemaker.DescribeImageInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeImageOutput, error)
	ListImageVersions(context.Context, *sagemaker.ListImageVersionsInput, ...func(*sagemaker.Options)) (*sagemaker.ListImageVersionsOutput, error)
	DescribeImageVersion(context.Context, *sagemaker.DescribeImageVersionInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeImageVersionOutput, error)
}

// scanSageMakerEdge runs all Edge / image phases for one region.
func scanSageMakerEdge(ctx context.Context, client sagemakerEdgeAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func(context.Context, sagemakerEdgeAPI, *account, string, *store.Store, string) (int, int, error){
		scanSageMakerDeviceFleets,
		scanSageMakerDevices,
		scanSageMakerImages,
		scanSageMakerImageVersions,
	} {
		t, i, ferr := phase(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanSageMakerDeviceFleets(ctx context.Context, client sagemakerEdgeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListDeviceFleetsPaginator(client, &sagemaker.ListDeviceFleetsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListDeviceFleets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListDeviceFleets: %w", perr)
		}
		for _, f := range out.DeviceFleetSummaries {
			if f.DeviceFleetName != nil {
				names = append(names, *f.DeviceFleetName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeDeviceFleet(gctx, &sagemaker.DescribeDeviceFleetInput{DeviceFleetName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeDeviceFleet %s: %w", name, derr)
		}
		arn := sv(out.DeviceFleetArn)
		if arn == "" {
			return nil, nil
		}
		fname := sv(out.DeviceFleetName)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeSageMakerDeviceFleet,
			NativeID:       arn,
			Name:           &fname,
			Region:         &region,
			CreatedAt:      tp(out.CreationTime),
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "sagemaker device fleets")
}

// deviceKey carries (DeviceFleetName, DeviceName) — DescribeDevice requires both.
type deviceKey struct{ fleet, name string }

// scanSageMakerDevices lists all devices across the account+region (no
// fleet filter), then fans out DescribeDevice for full body.
func scanSageMakerDevices(ctx context.Context, client sagemakerEdgeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListDevicesPaginator(client, &sagemaker.ListDevicesInput{})
	var keys []deviceKey
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListDevices", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListDevices: %w", perr)
		}
		for _, d := range out.DeviceSummaries {
			if d.DeviceFleetName != nil && d.DeviceName != nil {
				keys = append(keys, deviceKey{*d.DeviceFleetName, *d.DeviceName})
			}
		}
	}
	if len(keys) == 0 {
		return 0, 0, nil
	}
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, k := range keys {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.DescribeDevice(gctx, &sagemaker.DescribeDeviceInput{DeviceFleetName: &k.fleet, DeviceName: &k.name})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("sagemaker:DescribeDevice %s/%s: %w", k.fleet, k.name, derr)
			}
			arn := sv(out.DeviceArn)
			if arn == "" {
				return nil
			}
			dname := sv(out.DeviceName)
			mu.Lock()
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerDevice,
				NativeID:       arn,
				Name:           &dname,
				Region:         &region,
				CreatedAt:      tp(out.RegistrationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			})
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert sagemaker devices: %w", uerr)
	}
	return len(batch), n, nil
}

func scanSageMakerImages(ctx context.Context, client sagemakerEdgeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListImagesPaginator(client, &sagemaker.ListImagesInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListImages", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListImages: %w", perr)
		}
		for _, i := range out.Images {
			if i.ImageName != nil {
				names = append(names, *i.ImageName)
			}
		}
	}
	return sagemakerDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeImage(gctx, &sagemaker.DescribeImageInput{ImageName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("sagemaker:DescribeImage %s: %w", name, derr)
		}
		arn := sv(out.ImageArn)
		if arn == "" {
			return nil, nil
		}
		iname := sv(out.ImageName)
		status := string(out.ImageStatus)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeSageMakerImage,
			NativeID:       arn,
			Name:           &iname,
			Region:         &region,
			Status:         &status,
			CreatedAt:      tp(out.CreationTime),
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "sagemaker images")
}

// imageVersionKey carries (ImageName, Version) — DescribeImageVersion accepts
// an int Version or defaults to latest; ListImageVersions lists every version
// per image, so pass the explicit Version to disambiguate.
type imageVersionKey struct {
	imageName string
	version   int32
}

// scanSageMakerImageVersions enumerates SageMaker images, then per-image pages
// ListImageVersions to build an (image, version) key set for the
// DescribeImageVersion fan-out. Two-stage list (no embedding): each version
// is its own CFN type.
func scanSageMakerImageVersions(ctx context.Context, client sagemakerEdgeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	imagesPager := sagemaker.NewListImagesPaginator(client, &sagemaker.ListImagesInput{})
	var imageNames []string
	for imagesPager.HasMorePages() {
		out, perr := imagesPager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListImages", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListImages: %w", perr)
		}
		for _, i := range out.Images {
			if i.ImageName != nil {
				imageNames = append(imageNames, *i.ImageName)
			}
		}
	}

	var keys []imageVersionKey
	for _, imgName := range imageNames {
		vp := sagemaker.NewListImageVersionsPaginator(client, &sagemaker.ListImageVersionsInput{ImageName: &imgName})
		for vp.HasMorePages() {
			out, perr := vp.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("sagemaker:ListImageVersions %s: %w", imgName, perr)
			}
			for _, v := range out.ImageVersions {
				if v.Version != nil {
					keys = append(keys, imageVersionKey{imageName: imgName, version: *v.Version})
				}
			}
		}
	}
	if len(keys) == 0 {
		return 0, 0, nil
	}
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, k := range keys {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			ver := k.version
			out, derr := client.DescribeImageVersion(gctx, &sagemaker.DescribeImageVersionInput{
				ImageName: &k.imageName,
				Version:   &ver,
			})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("sagemaker:DescribeImageVersion %s/%d: %w", k.imageName, k.version, derr)
			}
			arn := sv(out.ImageVersionArn)
			if arn == "" {
				return nil
			}
			label := fmt.Sprintf("%s:%d", k.imageName, k.version)
			status := string(out.ImageVersionStatus)
			mu.Lock()
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerImageVersion,
				NativeID:       arn,
				Name:           &label,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			})
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert sagemaker image versions: %w", uerr)
	}
	return len(batch), n, nil
}
