package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/panorama" //nolint:staticcheck // AWS deprecated the Panorama service (EOL, no longer available); scanner retained pending removal
)

func init() {
	registerType(restype.Descriptor{Type: TypePanoramaApplicationInstance, Service: "panorama", Leaf: true})
	registerType(restype.Descriptor{Type: TypePanoramaPackage, Service: "panorama", Leaf: true})
	registerType(restype.Descriptor{Type: TypePanoramaDevice, Service: "panorama", Leaf: true})
	registerService(serviceEntry{
		name: "aws:panorama",
		fn:   scanPanorama,
	})
}

type panoramaAPI interface {
	ListApplicationInstances(context.Context, *panorama.ListApplicationInstancesInput, ...func(*panorama.Options)) (*panorama.ListApplicationInstancesOutput, error)
	ListPackages(context.Context, *panorama.ListPackagesInput, ...func(*panorama.Options)) (*panorama.ListPackagesOutput, error)
	ListDevices(context.Context, *panorama.ListDevicesInput, ...func(*panorama.Options)) (*panorama.ListDevicesOutput, error)
}

// scanPanorama discovers Panorama application instances and packages.
// PackageVersion skip-logged: SDK exposes only DescribePackageVersion, no
// list endpoint.
func scanPanorama(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := panorama.NewFromConfig(acct.cfg, func(o *panorama.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanPanoramaAppInstances(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanPanoramaPackages(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanPanoramaDevices(ctx, client, acct, region, st, scanID) },
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

func scanPanoramaAppInstances(ctx context.Context, client panoramaAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := panorama.NewListApplicationInstancesPaginator(client, &panorama.ListApplicationInstancesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "panorama:ListApplicationInstances", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("panorama:ListApplicationInstances: %w", err)
		}
		for _, a := range out.ApplicationInstances { //nolint:staticcheck // AWS deprecated the Panorama service (EOL, no longer available); scanner retained pending removal
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			status := string(a.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePanoramaApplicationInstance, NativeID: arn,
				Name: a.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "panorama application-instances")
}

func scanPanoramaPackages(ctx context.Context, client panoramaAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := panorama.NewListPackagesPaginator(client, &panorama.ListPackagesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "panorama:ListPackages", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("panorama:ListPackages: %w", err)
		}
		for _, p := range out.Packages { //nolint:staticcheck // AWS deprecated the Panorama service (EOL, no longer available); scanner retained pending removal
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePanoramaPackage, NativeID: arn,
				Name: p.PackageName, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "panorama packages")
}

// scanPanoramaDevices discovers Panorama appliance devices. Device list
// carries no ARN; NativeID is synthesized from DeviceId in the canonical
// panorama device ARN shape.
func scanPanoramaDevices(ctx context.Context, client panoramaAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := panorama.NewListDevicesPaginator(client, &panorama.ListDevicesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "panorama:ListDevices", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("panorama:ListDevices: %w", err)
		}
		for _, d := range out.Devices { //nolint:staticcheck // AWS deprecated the Panorama service (EOL, no longer available); scanner retained pending removal
			id := sv(d.DeviceId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:panorama:%s:%s:device/%s", region, acct.ID, id)
			status := string(d.ProvisioningStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePanoramaDevice, NativeID: arn,
				Name: d.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "panorama devices")
}
