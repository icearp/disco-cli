package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/appstream"
	astypes "github.com/aws/aws-sdk-go-v2/service/appstream/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAppStreamAppBlock, Service: "appstream"})
	registerType(restype.Descriptor{Type: TypeAppStreamAppBlockBuilder, Service: "appstream"})
	registerType(restype.Descriptor{Type: TypeAppStreamApplication, Service: "appstream"})
	registerType(restype.Descriptor{Type: TypeAppStreamApplicationEntitlementAssociation, Service: "appstream"})
	registerType(restype.Descriptor{Type: TypeAppStreamApplicationFleetAssociation, Service: "appstream"})
	registerType(restype.Descriptor{Type: TypeAppStreamDirectoryConfig, Service: "appstream"})
	registerType(restype.Descriptor{Type: TypeAppStreamEntitlement, Service: "appstream"})
	registerType(restype.Descriptor{Type: TypeAppStreamFleet, Service: "appstream"})
	registerType(restype.Descriptor{Type: TypeAppStreamImageBuilder, Service: "appstream"})
	registerType(restype.Descriptor{Type: TypeAppStreamImage, Service: "appstream"})
	registerType(restype.Descriptor{Type: TypeAppStreamImagePermission, Service: "appstream"})
	registerType(restype.Descriptor{Type: TypeAppStreamSoftwareAssociation, Service: "appstream"})
	registerType(restype.Descriptor{Type: TypeAppStreamStack, Service: "appstream"})
	registerType(restype.Descriptor{Type: TypeAppStreamStackFleetAssociation, Service: "appstream"})
	registerType(restype.Descriptor{Type: TypeAppStreamStackUserAssociation, Service: "appstream"})
	registerType(restype.Descriptor{Type: TypeAppStreamUser, Service: "appstream"})
	registerService(serviceEntry{
		name: "aws:appstream",
		fn:   scanAppStream,
	})
}

type appStreamAPI interface {
	DescribeAppBlocks(context.Context, *appstream.DescribeAppBlocksInput, ...func(*appstream.Options)) (*appstream.DescribeAppBlocksOutput, error)
	DescribeAppBlockBuilders(context.Context, *appstream.DescribeAppBlockBuildersInput, ...func(*appstream.Options)) (*appstream.DescribeAppBlockBuildersOutput, error)
	DescribeApplications(context.Context, *appstream.DescribeApplicationsInput, ...func(*appstream.Options)) (*appstream.DescribeApplicationsOutput, error)
	DescribeApplicationFleetAssociations(context.Context, *appstream.DescribeApplicationFleetAssociationsInput, ...func(*appstream.Options)) (*appstream.DescribeApplicationFleetAssociationsOutput, error)
	DescribeDirectoryConfigs(context.Context, *appstream.DescribeDirectoryConfigsInput, ...func(*appstream.Options)) (*appstream.DescribeDirectoryConfigsOutput, error)
	DescribeEntitlements(context.Context, *appstream.DescribeEntitlementsInput, ...func(*appstream.Options)) (*appstream.DescribeEntitlementsOutput, error)
	DescribeFleets(context.Context, *appstream.DescribeFleetsInput, ...func(*appstream.Options)) (*appstream.DescribeFleetsOutput, error)
	DescribeImageBuilders(context.Context, *appstream.DescribeImageBuildersInput, ...func(*appstream.Options)) (*appstream.DescribeImageBuildersOutput, error)
	DescribeImages(context.Context, *appstream.DescribeImagesInput, ...func(*appstream.Options)) (*appstream.DescribeImagesOutput, error)
	DescribeImagePermissions(context.Context, *appstream.DescribeImagePermissionsInput, ...func(*appstream.Options)) (*appstream.DescribeImagePermissionsOutput, error)
	DescribeSoftwareAssociations(context.Context, *appstream.DescribeSoftwareAssociationsInput, ...func(*appstream.Options)) (*appstream.DescribeSoftwareAssociationsOutput, error)
	DescribeStacks(context.Context, *appstream.DescribeStacksInput, ...func(*appstream.Options)) (*appstream.DescribeStacksOutput, error)
	DescribeUsers(context.Context, *appstream.DescribeUsersInput, ...func(*appstream.Options)) (*appstream.DescribeUsersOutput, error)
	DescribeUserStackAssociations(context.Context, *appstream.DescribeUserStackAssociationsInput, ...func(*appstream.Options)) (*appstream.DescribeUserStackAssociationsOutput, error)
	ListAssociatedFleets(context.Context, *appstream.ListAssociatedFleetsInput, ...func(*appstream.Options)) (*appstream.ListAssociatedFleetsOutput, error)
	ListEntitledApplications(context.Context, *appstream.ListEntitledApplicationsInput, ...func(*appstream.Options)) (*appstream.ListEntitledApplicationsOutput, error)
}

// asEntitlementKey carries (stackName, entitlementName) for the
// per-(stack, entitlement) ListEntitledApplications fan-out.
type asEntitlementKey struct {
	stack       string
	entitlement string
}

// appStreamSupportedRegions enumerates regions where AppStream 2.0 is
// deployed (per AWS service-availability docs). Other regions resolve
// the appstream2.<region>.amazonaws.com endpoint via DNS but TCP-dial
// times out — silent-skip rather than burn retries on every scan.
var appStreamSupportedRegions = map[string]bool{
	"us-east-1":      true,
	"us-east-2":      true,
	"us-west-2":      true,
	"ap-northeast-1": true,
	"ap-northeast-2": true,
	"ap-southeast-1": true,
	"ap-southeast-2": true,
	"ca-central-1":   true,
	"eu-central-1":   true,
	"eu-west-1":      true,
	"eu-west-2":      true,
	"us-gov-east-1":  true,
	"us-gov-west-1":  true,
}

// scanAppStream discovers the AppStream 2.0 resources of one region.
//
// The older phases (stacks through application-entitlement associations) stop
// at their first hard error. The image-permission and software-association
// phases run whatever any other phase returned, over the images and image
// builders stored before a failure, so their failure costs no other phase its
// rows and an older phase's failure does not skip them. Each of the three
// results has its transient error demoted to a warning, since one transient
// member would make the dispatcher read the whole join as transient, and the
// rest are joined; the counts of all three are summed.
func scanAppStream(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	if !appStreamSupportedRegions[region] {
		return 0, 0, nil
	}
	client := appstream.NewFromConfig(acct.cfg, func(o *appstream.Options) { o.Region = region })
	total, inserted, images, builders, err := scanASOlderPhases(ctx, client, acct, region, st, scanID)
	errs := []error{demoteTransient(st, "appstream", acct, region, err)}
	for _, phase := range []struct {
		op  string
		run func() (int, int, error)
	}{
		{asImagePermissionOp, func() (int, int, error) {
			return scanASImagePermissions(ctx, client, acct, region, st, scanID, images)
		}},
		{asSoftwareAssociationOp, func() (int, int, error) {
			return scanASSoftwareAssociations(ctx, client, acct, region, st, scanID, slices.Concat(images, builders))
		}},
	} {
		t, i, perr := phase.run()
		total += t
		inserted += i
		errs = append(errs, demoteTransient(st, phase.op, acct, region, perr))
	}
	return total, inserted, errors.Join(errs...)
}

// scanASOlderPhases runs the phases that predate image permissions and software
// associations, stopping at the first hard error. It also returns the images
// and image builders stored.
func scanASOlderPhases(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, images, builders []childParent, err error) {
	stackNames, t, i, ferr := scanASStacks(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, nil, nil, ferr
	}
	total += t
	inserted += i

	entKeys, t, i, ferr := scanASEntitlements(ctx, client, acct, region, st, scanID, stackNames)
	if ferr != nil {
		return total, inserted, nil, nil, ferr
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanASAppBlocks(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanASAppBlockBuilders(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanASApplications(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanASAppFleetAssocs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanASDirectoryConfigs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanASFleets(ctx, client, acct, region, st, scanID) },
		func() (t, i int, err error) {
			builders, t, i, err = scanASImageBuilders(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (t, i int, err error) {
			images, t, i, err = scanASImages(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) {
			return scanASStackFleetAssocs(ctx, client, acct, region, st, scanID, stackNames)
		},
		func() (int, int, error) {
			return scanASUserStackAssocs(ctx, client, acct, region, st, scanID, stackNames)
		},
		func() (int, int, error) { return scanASUsers(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanASAppEntitlementAssocs(ctx, client, acct, region, st, scanID, entKeys)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, images, builders, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, images, builders, nil
}

func asARN(region, acct, kind, id string) string {
	return fmt.Sprintf("arn:aws:appstream:%s:%s:%s/%s", region, acct, kind, id)
}

func scanASAppBlocks(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.DescribeAppBlocks(ctx, &appstream.DescribeAppBlocksInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "appstream:DescribeAppBlocks", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("appstream:DescribeAppBlocks: %w", err)
		}
		for _, b := range out.AppBlocks {
			arn := sv(b.Arn)
			if arn == "" {
				continue
			}
			label := sv(b.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppStreamAppBlock, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "appstream app-blocks")
}

func scanASAppBlockBuilders(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := appstream.NewDescribeAppBlockBuildersPaginator(client, &appstream.DescribeAppBlockBuildersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "appstream:DescribeAppBlockBuilders", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("appstream:DescribeAppBlockBuilders: %w", perr)
		}
		for _, b := range out.AppBlockBuilders {
			arn := sv(b.Arn)
			if arn == "" {
				continue
			}
			label := sv(b.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppStreamAppBlockBuilder, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "appstream app-block-builders")
}

// scanASImages lists customer (PRIVATE) AppStream images. PUBLIC base images
// are AWS-managed and unbounded (mirrors the AMI Owners=["self"] convention);
// SHARED images belong to other accounts. NativeID = Arn.
// It also returns the images stored that carry a name, which is what
// DescribeImagePermissions keys on.
func scanASImages(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	pager := appstream.NewDescribeImagesPaginator(client, &appstream.DescribeImagesInput{Type: astypes.VisibilityTypePrivate})
	var (
		batch  []*store.Resource
		images []childParent
	)
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "appstream:DescribeImages", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("appstream:DescribeImages: %w", perr)
		}
		for _, img := range out.Images {
			arn := sv(img.Arn)
			if arn == "" {
				continue
			}
			if name := sv(img.Name); name != "" {
				images = append(images, childParent{id: name, arn: arn})
			}
			label := sv(img.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppStreamImage, NativeID: arn,
				Name: &label, Region: &region, CreatedAt: tp(img.CreatedTime),
				AttributesJSON: mustJSON(img), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "appstream images")
	if err != nil {
		return nil, 0, 0, err
	}
	return images, t, i, nil
}

func scanASApplications(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.DescribeApplications(ctx, &appstream.DescribeApplicationsInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "appstream:DescribeApplications", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("appstream:DescribeApplications: %w", err)
		}
		for _, a := range out.Applications {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			label := sv(a.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppStreamApplication, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "appstream applications")
}

// scanASAppFleetAssocs — DescribeApplicationFleetAssociations requires a
// FleetName or ApplicationArn filter; a blanket call returns nothing.
// Collects fleet names via DescribeFleets, then describes associations
// per fleet.
func scanASAppFleetAssocs(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var fleetNames []string
	var ftoken *string
	for {
		fout, err := client.DescribeFleets(ctx, &appstream.DescribeFleetsInput{NextToken: ftoken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("appstream:DescribeFleets (for app-fleet assocs): %w", err)
		}
		for _, f := range fout.Fleets {
			if f.Name != nil {
				fleetNames = append(fleetNames, *f.Name)
			}
		}
		if fout.NextToken == nil || *fout.NextToken == "" {
			break
		}
		ftoken = fout.NextToken
	}
	var batch []*store.Resource
	for _, fn := range fleetNames {
		fname := fn
		var token *string
		for {
			out, err := client.DescribeApplicationFleetAssociations(ctx, &appstream.DescribeApplicationFleetAssociationsInput{FleetName: &fname, NextToken: token})
			if err != nil {
				if isAccessDenied(err) {
					break
				}
				return 0, 0, fmt.Errorf("appstream:DescribeApplicationFleetAssociations %s: %w", fn, err)
			}
			for _, a := range out.ApplicationFleetAssociations {
				appArn := sv(a.ApplicationArn)
				if appArn == "" {
					continue
				}
				arn := asARN(region, acct.ID, "application-fleet-association", fname+"/"+appArn)
				label := fname + "/" + appArn
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAppStreamApplicationFleetAssociation, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			token = out.NextToken
		}
	}
	return upsertBatch(st, batch, "appstream application-fleet-associations")
}

func scanASDirectoryConfigs(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.DescribeDirectoryConfigs(ctx, &appstream.DescribeDirectoryConfigsInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "appstream:DescribeDirectoryConfigs", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("appstream:DescribeDirectoryConfigs: %w", err)
		}
		for _, d := range out.DirectoryConfigs {
			name := sv(d.DirectoryName)
			if name == "" {
				continue
			}
			arn := asARN(region, acct.ID, "directory-config", name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppStreamDirectoryConfig, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "appstream directory-configs")
}

func scanASEntitlements(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string, stackNames []string) ([]asEntitlementKey, int, int, error) {
	var keys []asEntitlementKey
	var batch []*store.Resource
	for _, sn := range stackNames {
		s := sn
		var token *string
		for {
			out, err := client.DescribeEntitlements(ctx, &appstream.DescribeEntitlementsInput{StackName: &s, NextToken: token})
			if err != nil {
				if isAccessDenied(err) || isAPIErrorCode(err, "EntitlementNotFoundException") {
					break
				}
				return nil, 0, 0, fmt.Errorf("appstream:DescribeEntitlements %s: %w", sn, err)
			}
			for _, e := range out.Entitlements {
				name := sv(e.Name)
				if name == "" {
					continue
				}
				keys = append(keys, asEntitlementKey{stack: sn, entitlement: name})
				arn := asARN(region, acct.ID, "entitlement", sn+"/"+name)
				label := name
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAppStreamEntitlement, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			token = out.NextToken
		}
	}
	t, i, err := upsertBatch(st, batch, "appstream entitlements")
	return keys, t, i, err
}

func scanASFleets(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.DescribeFleets(ctx, &appstream.DescribeFleetsInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "appstream:DescribeFleets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("appstream:DescribeFleets: %w", err)
		}
		for _, f := range out.Fleets {
			arn := sv(f.Arn)
			if arn == "" {
				continue
			}
			label := sv(f.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppStreamFleet, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "appstream fleets")
}

// scanASImageBuilders also returns the image builders stored.
func scanASImageBuilders(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	var (
		batch    []*store.Resource
		builders []childParent
		token    *string
	)
	for {
		out, err := client.DescribeImageBuilders(ctx, &appstream.DescribeImageBuildersInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "appstream:DescribeImageBuilders", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("appstream:DescribeImageBuilders: %w", err)
		}
		for _, b := range out.ImageBuilders {
			arn := sv(b.Arn)
			if arn == "" {
				continue
			}
			builders = append(builders, childParent{id: sv(b.Name), arn: arn})
			label := sv(b.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppStreamImageBuilder, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "appstream image-builders")
	if err != nil {
		return nil, 0, 0, err
	}
	return builders, t, i, nil
}

func scanASStacks(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	var names []string
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.DescribeStacks(ctx, &appstream.DescribeStacksInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "appstream:DescribeStacks", acct.ID, region, err)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("appstream:DescribeStacks: %w", err)
		}
		for _, s := range out.Stacks {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			n := sv(s.Name)
			if n != "" {
				names = append(names, n)
			}
			label := n
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppStreamStack, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "appstream stacks")
	return names, t, i, err
}

func scanASStackFleetAssocs(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string, stackNames []string) (int, int, error) {
	var batch []*store.Resource
	for _, sn := range stackNames {
		s := sn
		var token *string
		for {
			out, err := client.ListAssociatedFleets(ctx, &appstream.ListAssociatedFleetsInput{StackName: &s, NextToken: token})
			if err != nil {
				if isAccessDenied(err) {
					break
				}
				return 0, 0, fmt.Errorf("appstream:ListAssociatedFleets %s: %w", sn, err)
			}
			for _, fname := range out.Names {
				if fname == "" {
					continue
				}
				arn := asARN(region, acct.ID, "stack-fleet-association", sn+"/"+fname)
				label := sn + "/" + fname
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAppStreamStackFleetAssociation, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(map[string]string{"StackName": sn, "FleetName": fname}), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			token = out.NextToken
		}
	}
	return upsertBatch(st, batch, "appstream stack-fleet-associations")
}

// scanASUserStackAssocs requires either StackName or UserName as a filter —
// blanket DescribeUserStackAssociations rejects empty input. Fan out per
// stack name enumerated by scanASStacks.
func scanASUserStackAssocs(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string, stackNames []string) (int, int, error) {
	if len(stackNames) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, sn := range stackNames {
		stackName := sn
		var token *string
		for {
			out, err := client.DescribeUserStackAssociations(ctx, &appstream.DescribeUserStackAssociationsInput{
				StackName: &stackName,
				NextToken: token,
			})
			if err != nil {
				if isAccessDenied(err) {
					return 0, 0, skipIfAccessDenied(st, "appstream:DescribeUserStackAssociations", acct.ID, region, err)
				}
				return 0, 0, fmt.Errorf("appstream:DescribeUserStackAssociations %s: %w", stackName, err)
			}
			for _, a := range out.UserStackAssociations {
				user := sv(a.UserName)
				stack := sv(a.StackName)
				if user == "" || stack == "" {
					continue
				}
				arn := asARN(region, acct.ID, "user-stack-association", stack+"/"+string(a.AuthenticationType)+"/"+user)
				label := stack + "/" + user
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAppStreamStackUserAssociation, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			token = out.NextToken
		}
	}
	return upsertBatch(st, batch, "appstream user-stack-associations")
}

// scanASUsers pages through the AppStream user pool.
//
// USERPOOL is the only AuthenticationType DescribeUsers accepts — the SDK's own
// field doc says "You must specify USERPOOL". The enum's other values (API,
// SAML, AWS_AD) belong to the shape shared with CreateUser/EnableUser/etc, and
// none of them names a user pool entry: an API-auth session has no user record
// (CreateStreamingURL mints the URL), and SAML/AWS_AD users are federated. So
// there is nothing to enumerate under them, and asking for one is a 400.
func scanASUsers(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.DescribeUsers(ctx, &appstream.DescribeUsersInput{
			AuthenticationType: astypes.AuthenticationTypeUserpool,
			NextToken:          token,
		})
		if err != nil {
			if isAccessDenied(err) {
				break
			}
			return 0, 0, fmt.Errorf("appstream:DescribeUsers: %w", err)
		}
		for _, u := range out.Users {
			arn := sv(u.Arn)
			if arn == "" {
				continue
			}
			label := sv(u.UserName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppStreamUser, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "appstream users")
}

func scanASAppEntitlementAssocs(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string, keys []asEntitlementKey) (int, int, error) {
	if len(keys) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, k := range keys {
		s := k.stack
		e := k.entitlement
		var token *string
		for {
			out, err := client.ListEntitledApplications(ctx, &appstream.ListEntitledApplicationsInput{StackName: &s, EntitlementName: &e, NextToken: token})
			if err != nil {
				if isAccessDenied(err) {
					break
				}
				return 0, 0, fmt.Errorf("appstream:ListEntitledApplications %s/%s: %w", k.stack, k.entitlement, err)
			}
			for _, a := range out.EntitledApplications {
				appID := sv(a.ApplicationIdentifier)
				if appID == "" {
					continue
				}
				arn := asARN(region, acct.ID, "application-entitlement-association", k.stack+"/"+k.entitlement+"/"+appID)
				label := k.entitlement + "/" + appID
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAppStreamApplicationEntitlementAssociation, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			token = out.NextToken
		}
	}
	return upsertBatch(st, batch, "appstream application-entitlement-associations")
}

const (
	asImagePermissionOp     = "appstream:DescribeImagePermissions"
	asSoftwareAssociationOp = "appstream:DescribeSoftwareAssociations"
	// asImagePermissionPageLimit is the model's cap on MaxResults (0-500).
	asImagePermissionPageLimit = 500
)

// isASParentGone matches an image or image builder deleted between its list
// call and the call that lists its children.
func isASParentGone(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }

// scanASImagePermissions lists the AWS accounts each image is shared with and
// links each grant under its image. Only images this account owns can be
// shared; scanASImages lists exactly those (Type PRIVATE), so every image it
// returns is queried. An image whose call still fails transiently after the
// SDK's retries is skipped, keeping its siblings' rows, and the skips are
// reported in one warning.
func scanASImagePermissions(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string, images []childParent) (int, int, error) {
	var transient transientTally
	total, inserted, err := childFanOut(ctx, st, acct, region, asImagePermissionOp, images, isASParentGone,
		func(ctx context.Context, img childParent) ([]*store.Resource, error) {
			rows, err := listASImagePermissions(ctx, client, acct, region, scanID, img)
			if isTransientNetworkError(err) {
				transient.skipped(img.id, err)
				return nil, nil
			}
			return rows, err
		})
	transient.warnSkipped(st, asImagePermissionOp, acct, region)
	return total, inserted, err
}

// listASImagePermissions lists the share grants of one image, one row per
// shared account as the API reports them. A grant has no ARN, so its NativeID
// is {imageARN}/permission/{sharedAccountId}.
func listASImagePermissions(ctx context.Context, client appStreamAPI, acct *account, region, scanID string, img childParent) ([]*store.Resource, error) {
	var rows []*store.Resource
	pager := appstream.NewDescribeImagePermissionsPaginator(client, &appstream.DescribeImagePermissionsInput{Name: &img.id},
		func(o *appstream.DescribeImagePermissionsPaginatorOptions) { o.Limit = asImagePermissionPageLimit })
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range out.SharedImagePermissionsList {
			sharedAccountID := sv(p.SharedAccountId)
			if sharedAccountID == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppStreamImagePermission, NativeID: img.arn + "/permission/" + sharedAccountID,
				Name: &sharedAccountID, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return rows, nil
}

// scanASSoftwareAssociations lists the license-included applications associated
// with each image and image builder and links each under its owner. A refusal
// with OperationNotPermittedException skips that owner with one warning per
// region, since that response was never observed live. An owner whose call
// still fails transiently after the SDK's retries is skipped, keeping its
// siblings' rows, and the skips are reported in one warning.
func scanASSoftwareAssociations(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string, owners []childParent) (int, int, error) {
	// Keyed by ARN so error and warning messages name the owner unambiguously:
	// an image and an image builder may share a name.
	byARN := make([]childParent, len(owners))
	for i, o := range owners {
		byARN[i] = childParent{id: o.arn, arn: o.arn}
	}
	var (
		transient    transientTally
		notPermitted sync.Once
	)
	total, inserted, err := childFanOut(ctx, st, acct, region, asSoftwareAssociationOp, byARN, isASParentGone,
		func(ctx context.Context, owner childParent) ([]*store.Resource, error) {
			rows, err := listASSoftwareAssociations(ctx, client, acct, region, scanID, owner)
			if isAPIErrorCode(err, "OperationNotPermittedException") {
				notPermitted.Do(func() { warnASSoftwareNotPermitted(st, acct.ID, region, owner.id, err) })
				return nil, nil
			}
			if isTransientNetworkError(err) {
				transient.skipped(owner.id, err)
				return nil, nil
			}
			return rows, err
		})
	transient.warnSkipped(st, asSoftwareAssociationOp, acct, region)
	return total, inserted, err
}

// warnASSoftwareNotPermitted records that DescribeSoftwareAssociations was not
// permitted for owner, so its associations are absent from this scan. Safe
// from fan-out goroutines: ReportWarning only calls OnWarn, which the scan
// runner guards with a mutex.
func warnASSoftwareNotPermitted(st *store.Store, accountID, region, owner string, err error) {
	st.ReportWarning(store.ScanWarning{
		Provider: "aws",
		Service:  asSoftwareAssociationOp,
		Scope:    accountID + "/" + region,
		Message:  "operation not permitted: software associations of " + owner + " not listed this scan (later refusals in this region not reported): " + err.Error(),
	})
}

// listASSoftwareAssociations lists the software associations of one image or
// image builder. An association has no ARN or id of its own; the association
// APIs name it by SoftwareName within its owner, so its NativeID is
// {ownerARN}/software-association/{SoftwareName}.
func listASSoftwareAssociations(ctx context.Context, client appStreamAPI, acct *account, region, scanID string, owner childParent) ([]*store.Resource, error) {
	var rows []*store.Resource
	in := &appstream.DescribeSoftwareAssociationsInput{AssociatedResource: &owner.arn}
	for {
		out, err := client.DescribeSoftwareAssociations(ctx, in)
		if err != nil {
			return nil, err
		}
		for _, a := range out.SoftwareAssociations {
			name := sv(a.SoftwareName)
			if name == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppStreamSoftwareAssociation, NativeID: owner.arn + "/software-association/" + name,
				Name: &name, Status: nonEmptyPtr(string(a.Status)), Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		// DescribeSoftwareAssociations has no paginator; a repeated token would
		// loop forever.
		if next := sv(out.NextToken); next == "" || next == sv(in.NextToken) {
			break
		}
		in.NextToken = out.NextToken
	}
	return rows, nil
}
