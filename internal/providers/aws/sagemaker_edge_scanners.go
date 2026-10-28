package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"

	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSageMakerDeviceFleet, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerDevice, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerImage, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerImageVersion, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerAlias, Service: "sagemaker"})
}

// sagemakerEdgeAPI is the narrow surface used by the Edge / images family.
// Image is the parent of ImageVersion and ImageAlias: ListImages enumerates
// parents, per-image ListImageVersions / ListAliases enumerate children.
type sagemakerEdgeAPI interface {
	sagemakerTagsAPI
	ListDeviceFleets(context.Context, *sagemaker.ListDeviceFleetsInput, ...func(*sagemaker.Options)) (*sagemaker.ListDeviceFleetsOutput, error)
	DescribeDeviceFleet(context.Context, *sagemaker.DescribeDeviceFleetInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeDeviceFleetOutput, error)
	ListDevices(context.Context, *sagemaker.ListDevicesInput, ...func(*sagemaker.Options)) (*sagemaker.ListDevicesOutput, error)
	DescribeDevice(context.Context, *sagemaker.DescribeDeviceInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeDeviceOutput, error)
	ListImages(context.Context, *sagemaker.ListImagesInput, ...func(*sagemaker.Options)) (*sagemaker.ListImagesOutput, error)
	DescribeImage(context.Context, *sagemaker.DescribeImageInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeImageOutput, error)
	ListImageVersions(context.Context, *sagemaker.ListImageVersionsInput, ...func(*sagemaker.Options)) (*sagemaker.ListImageVersionsOutput, error)
	DescribeImageVersion(context.Context, *sagemaker.DescribeImageVersionInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeImageVersionOutput, error)
	ListAliases(context.Context, *sagemaker.ListAliasesInput, ...func(*sagemaker.Options)) (*sagemaker.ListAliasesOutput, error)
}

// scanSageMakerEdge runs all Edge / image phases for one region.
func scanSageMakerEdge(ctx context.Context, client sagemakerEdgeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return runSageMakerPhases(
		sagemakerPhase{"sagemaker:ListDeviceFleets", func() (int, int, error) { return scanSageMakerDeviceFleets(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListDevices", func() (int, int, error) { return scanSageMakerDevices(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListImages", func() (int, int, error) { return scanSageMakerImages(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListImageVersions", func() (int, int, error) { return scanSageMakerImageVersions(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListAliases", func() (int, int, error) { return scanSageMakerImageAliases(ctx, client, acct, region, st, scanID) }},
	)
}

func scanSageMakerDeviceFleets(ctx context.Context, client sagemakerEdgeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.DeviceFleetSummary]{
		listOp: "sagemaker:ListDeviceFleets", describeOp: "sagemaker:DescribeDeviceFleet", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]smtypes.DeviceFleetSummary, *string, error) {
			out, err := client.ListDeviceFleets(ctx, &sagemaker.ListDeviceFleetsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DeviceFleetSummaries, out.NextToken, nil
		},
		name: func(s smtypes.DeviceFleetSummary) string { return sv(s.DeviceFleetName) },
		listed: func(s smtypes.DeviceFleetSummary) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerDeviceFleet, s.DeviceFleetArn, s.DeviceFleetName, s.CreationTime, "", s)
		},
		describe: func(ctx context.Context, s smtypes.DeviceFleetSummary) (*store.Resource, error) {
			name := sv(s.DeviceFleetName)
			out, err := client.DescribeDeviceFleet(ctx, &sagemaker.DescribeDeviceFleetInput{DeviceFleetName: &name})
			if err != nil {
				return nil, err
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
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListDeviceFleets"))
}

// deviceKey carries (DeviceFleetName, DeviceName) — DescribeDevice requires
// both — and the listed summary, stored when the describe fails
// (sagemakerDescribe.listed).
type deviceKey struct {
	fleet, name string
	summary     smtypes.DeviceSummary
}

// scanSageMakerDevices lists all devices across the account+region (no
// fleet filter), then fans out DescribeDevice for full body.
func scanSageMakerDevices(ctx context.Context, client sagemakerEdgeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[deviceKey]{
		listOp: "sagemaker:ListDevices", describeOp: "sagemaker:DescribeDevice", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]deviceKey, *string, error) {
			out, err := client.ListDevices(ctx, &sagemaker.ListDevicesInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			keys := make([]deviceKey, len(out.DeviceSummaries))
			for i, d := range out.DeviceSummaries {
				keys[i] = deviceKey{sv(d.DeviceFleetName), sv(d.DeviceName), d}
			}
			return keys, out.NextToken, nil
		},
		name: func(k deviceKey) string {
			if k.fleet == "" || k.name == "" {
				return ""
			}
			return k.fleet + "/" + k.name
		},
		listed: func(k deviceKey) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerDevice, k.summary.DeviceArn, k.summary.DeviceName, k.summary.RegistrationTime, "", k.summary)
		},
		describe: func(ctx context.Context, k deviceKey) (*store.Resource, error) {
			out, err := client.DescribeDevice(ctx, &sagemaker.DescribeDeviceInput{DeviceFleetName: &k.fleet, DeviceName: &k.name})
			if err != nil {
				return nil, err
			}
			arn := sv(out.DeviceArn)
			if arn == "" {
				return nil, nil
			}
			dname := sv(out.DeviceName)
			return &store.Resource{
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
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListDevices"))
}

func scanSageMakerImages(ctx context.Context, client sagemakerEdgeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.Image]{
		listOp: "sagemaker:ListImages", describeOp: "sagemaker:DescribeImage", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]smtypes.Image, *string, error) {
			out, err := client.ListImages(ctx, &sagemaker.ListImagesInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Images, out.NextToken, nil
		},
		name: func(s smtypes.Image) string { return sv(s.ImageName) },
		listed: func(s smtypes.Image) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerImage, s.ImageArn, s.ImageName, s.CreationTime, string(s.ImageStatus), s)
		},
		describe: func(ctx context.Context, s smtypes.Image) (*store.Resource, error) {
			name := sv(s.ImageName)
			out, err := client.DescribeImage(ctx, &sagemaker.DescribeImageInput{ImageName: &name})
			if err != nil {
				return nil, err
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
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListImages"))
}

// imageVersionKey carries (ImageName, Version) — DescribeImageVersion accepts
// an int Version or defaults to latest; ListImageVersions lists every version
// per image, so pass the explicit Version to disambiguate. summary is the
// listed version; its Version is nil when the listing left it out.
type imageVersionKey struct {
	imageName string
	summary   smtypes.ImageVersion
}

func (k imageVersionKey) name() string {
	if k.summary.Version == nil {
		return ""
	}
	return fmt.Sprintf("%s:%d", k.imageName, *k.summary.Version)
}

// scanSageMakerImageVersions pages ListImageVersions for every image this
// scan stored (readSageMakerChildren), then fans out DescribeImageVersion
// over the (image, version) keys. Two-stage list (no embedding): each version
// is its own CFN type. Reading the stored images, as the aliases phase does,
// keeps a ListImages failure reported once, by the images phase.
//
// Their tags are not read: neither the Service Authorization Reference's
// ListTags resource types nor the ListTags API reference name an image
// version ARN, and CreateImageVersion takes no tags.
func scanSageMakerImageVersions(ctx context.Context, client sagemakerEdgeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "sagemaker:ListImageVersions"
	c, err := readSageMakerChildren(ctx, acct, region, st, scanID, TypeSageMakerImage, op, []string{"ResourceNotFound"},
		sagemakerChildSkips{namelessWhat: "stored image(s)"},
		func(ctx context.Context, image store.Resource) []sagemakerChildList[imageVersionKey] {
			imageName := sv(image.Name)
			if imageName == "" {
				return []sagemakerChildList[imageVersionKey]{{key: image.NativeID, nameless: true}}
			}
			keys, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]imageVersionKey, *string, error) {
				out, err := client.ListImageVersions(ctx, &sagemaker.ListImageVersionsInput{
					ImageName: &imageName, MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token,
				})
				if err != nil {
					return nil, nil, err
				}
				keys := make([]imageVersionKey, len(out.ImageVersions))
				for i, v := range out.ImageVersions {
					keys[i] = imageVersionKey{imageName: imageName, summary: v}
				}
				return keys, out.NextToken, nil
			})
			return []sagemakerChildList[imageVersionKey]{{key: imageName, items: keys, err: err}}
		})
	if err != nil {
		return 0, 0, err
	}
	untagged := sagemakerStoreUntagged(st, acct, region, op)
	total, inserted, err := sagemakerDescribe[imageVersionKey]{
		listOp: op, describeOp: "sagemaker:DescribeImageVersion", notFound: []string{"ResourceNotFound"},
		name: imageVersionKey.name,
		listed: func(k imageVersionKey) *store.Resource {
			label := k.name()
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerImageVersion, k.summary.ImageVersionArn, &label, k.summary.CreationTime, string(k.summary.ImageVersionStatus), k.summary)
		},
		describe: func(ctx context.Context, k imageVersionKey) (*store.Resource, error) {
			out, err := client.DescribeImageVersion(ctx, &sagemaker.DescribeImageVersionInput{ImageName: &k.imageName, Version: k.summary.Version})
			if err != nil {
				return nil, err
			}
			arn := sv(out.ImageVersionArn)
			if arn == "" {
				return nil, nil
			}
			label := k.name()
			status := string(out.ImageVersionStatus)
			return &store.Resource{
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
			}, nil
		},
	}.describeEach(ctx, st, acct, region, c.items, nil, func(rows []*store.Resource, listErr error, warn func()) (int, int, error) {
		return untagged(rows, listErr, func() {
			warn()
			c.warn()
		})
	})
	addOpErrsPhase(&c.p, op, total, inserted, err)
	return opErrsResult(&c.p)
}

// sagemakerImageAliasAttrs is the stored body of an image alias: ListAliases
// returns bare alias strings, so the image they belong to is carried alongside.
type sagemakerImageAliasAttrs struct {
	ImageName string `json:"ImageName"`
	Alias     string `json:"Alias"`
}

// scanSageMakerImageAliases lists the version aliases of every scanned image.
// An alias is unique within its image and can be moved between versions
// (UpdateImageVersion AliasesToAdd/AliasesToDelete), so its identity is
// (image, alias), not the version it currently names; ListAliases without a
// Version returns the aliases of all versions in one call per image. The row
// therefore does not record which version an alias currently names — an accepted
// gap; recovering it would cost one DescribeImageVersion per alias.
func scanSageMakerImageAliases(ctx context.Context, client sagemakerEdgeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return scanSageMakerChildren(ctx, acct, region, st, scanID, TypeSageMakerImage, "sagemaker:ListAliases", []string{"ResourceNotFound"},
		sagemakerChildSkips{emptyWhy: "image aliases are empty", namelessWhat: "stored image(s)"},
		func(ctx context.Context, image store.Resource) []sagemakerChildList[*store.Resource] {
			imageName := sv(image.Name)
			if imageName == "" {
				return []sagemakerChildList[*store.Resource]{{key: image.NativeID, nameless: true}}
			}
			aliases, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]string, *string, error) {
				out, err := client.ListAliases(ctx, &sagemaker.ListAliasesInput{
					ImageName: &imageName, MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token,
				})
				if err != nil {
					return nil, nil, err
				}
				return out.SageMakerImageVersionAliases, out.NextToken, nil
			})
			l := sagemakerChildList[*store.Resource]{key: image.NativeID, err: err}
			for i, alias := range aliases {
				if alias == "" {
					l.skipped = append(l.skipped, fmt.Sprintf("%s #%d", image.NativeID, i+1))
					continue
				}
				l.items = append(l.items, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeSageMakerAlias,
					NativeID:       image.NativeID + "/alias/" + alias,
					Name:           &alias,
					Region:         &region,
					AttributesJSON: mustJSON(sagemakerImageAliasAttrs{ImageName: imageName, Alias: alias}),
					DiscoveredBy:   scanID,
				})
			}
			return []sagemakerChildList[*store.Resource]{l}
		})
}
