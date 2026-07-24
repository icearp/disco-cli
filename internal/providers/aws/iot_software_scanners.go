package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTSoftwarePackage, Service: "iot", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIoTSoftwarePackageVersion, Service: "iot", Leaf: true})
}

type iotSoftwareAPI interface {
	ListPackages(context.Context, *iot.ListPackagesInput, ...func(*iot.Options)) (*iot.ListPackagesOutput, error)
	GetPackage(context.Context, *iot.GetPackageInput, ...func(*iot.Options)) (*iot.GetPackageOutput, error)
	ListPackageVersions(context.Context, *iot.ListPackageVersionsInput, ...func(*iot.Options)) (*iot.ListPackageVersionsOutput, error)
	GetPackageVersion(context.Context, *iot.GetPackageVersionInput, ...func(*iot.Options)) (*iot.GetPackageVersionOutput, error)
}

func scanIoTSoftware(ctx context.Context, client iotSoftwareAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	t1, i1, e1 := scanIoTSoftwarePackages(ctx, client, acct, region, st, scanID)
	if e1 != nil {
		return 0, 0, e1
	}
	t2, i2, e2 := scanIoTSoftwarePackageVersions(ctx, client, acct, region, st, scanID)
	if e2 != nil {
		return t1, i1, e2
	}
	return t1 + t2, i1 + i2, nil
}

func scanIoTSoftwarePackages(ctx context.Context, client iotSoftwareAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListPackagesPaginator(client, &iot.ListPackagesInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListPackages", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListPackages: %w", perr)
		}
		for _, p := range out.PackageSummaries {
			if p.PackageName != nil {
				names = append(names, *p.PackageName)
			}
		}
	}
	return iotDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.GetPackage(gctx, &iot.GetPackageInput{PackageName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:GetPackage %s: %w", name, derr)
		}
		arn := sv(out.PackageArn)
		if arn == "" {
			return nil, nil
		}
		pname := sv(out.PackageName)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeIoTSoftwarePackage,
			NativeID:       arn,
			Name:           &pname,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "iot software packages")
}

// scanIoTSoftwarePackageVersions enumerates per-package versions then fans
// out GetPackageVersion concurrently.
func scanIoTSoftwarePackageVersions(ctx context.Context, client iotSoftwareAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListPackagesPaginator(client, &iot.ListPackagesInput{})
	var pkgs []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListPackages", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListPackages: %w", perr)
		}
		for _, p := range out.PackageSummaries {
			if p.PackageName != nil {
				pkgs = append(pkgs, *p.PackageName)
			}
		}
	}
	if len(pkgs) == 0 {
		return 0, 0, nil
	}
	type key struct{ pkg, ver string }
	var keys []key
	for _, pkg := range pkgs {
		pkg := pkg
		vp := iot.NewListPackageVersionsPaginator(client, &iot.ListPackageVersionsInput{PackageName: &pkg})
		for vp.HasMorePages() {
			out, perr := vp.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("iot:ListPackageVersions %s: %w", pkg, perr)
			}
			for _, v := range out.PackageVersionSummaries {
				if v.VersionName != nil {
					keys = append(keys, key{pkg, *v.VersionName})
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
			out, derr := client.GetPackageVersion(gctx, &iot.GetPackageVersionInput{PackageName: &k.pkg, VersionName: &k.ver})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("iot:GetPackageVersion %s/%s: %w", k.pkg, k.ver, derr)
			}
			arn := sv(out.PackageVersionArn)
			if arn == "" {
				return nil
			}
			label := fmt.Sprintf("%s:%s", k.pkg, k.ver)
			status := string(out.Status)
			mu.Lock()
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTSoftwarePackageVersion,
				NativeID:       arn,
				Name:           &label,
				Region:         &region,
				Status:         &status,
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
		return 0, 0, fmt.Errorf("upsert iot software-package-versions: %w", uerr)
	}
	return len(batch), n, nil
}
