package aws

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
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
	ListTagsForResource(context.Context, *appstream.ListTagsForResourceInput, ...func(*appstream.Options)) (*appstream.ListTagsForResourceOutput, error)
}

// asEntitlementKey carries (stackName, entitlementName) for the
// per-(stack, entitlement) ListEntitledApplications fan-out.
type asEntitlementKey struct {
	stack       string
	entitlement string
}

// appStreamCommercialRegions enumerates the commercial regions where AppStream
// 2.0 is deployed (per AWS service-availability docs). Other regions resolve
// the appstream2.<region>.amazonaws.com endpoint via DNS but TCP-dial times
// out — silent-skip rather than burn retries on every scan. It is kept by
// hand for the aws partition because the SDK endpoint table lists commercial
// regions this list excludes; see appStreamRegionSupported for the other
// partitions.
var appStreamCommercialRegions = map[string]bool{
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
}

// appStreamRegionSupported reports whether AppStream is scanned in region: the
// commercial allowlist for aws accounts, the SDK endpoint table for every
// other partition.
func appStreamRegionSupported(acct *account, region string) bool {
	if acct.partition() == "aws" {
		return appStreamCommercialRegions[region]
	}
	return sdkTableAllows("aws:appstream", region)
}

const (
	asTagsOp = "appstream:ListTagsForResource"
	// asNoIdentifier names, in a warning, a listed element with neither an
	// ARN nor a name.
	asNoIdentifier = "(an element with no identifier)"
	// asPageCap is the page cap of DescribeImages (its model's MaxResults
	// range) and of DescribeAppBlockBuilders (its MaxResults doc).
	// DescribeAppBlocks, DescribeApplications, DescribeImageBuilders and
	// DescribeApplicationFleetAssociations take MaxResults but model no range
	// or documented cap for it, so none is sent; DescribeStacks and
	// DescribeFleets have no MaxResults member.
	asPageCap = 25
)

// scanAppStream discovers the AppStream 2.0 resources of one region. Every
// listing runs as its own phase, one at a time, in the order below
// (runHostPhases): a phase's error does not stop the later ones; a failed
// write and ctx's end do. A child phase gets the parents its parent phase
// stored: the stacks for entitlements and the stack associations, the fleets
// for application-fleet associations, the images and image builders for
// image permissions and software associations. Stacks, app blocks, app block
// builders, applications, fleets, image builders and images have their tags
// read per row (asTags), each such listing written a page at a time, and the
// tag denials of the whole scan are one warning.
func scanAppStream(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	if !appStreamRegionSupported(acct, region) {
		return 0, 0, nil
	}
	client := appstream.NewFromConfig(acct.cfg, func(o *appstream.Options) { o.Region = region })
	tags := asTags(client).sharingDenials()
	var (
		stacks, fleets, builders, images []childParent
		entKeys                          []asEntitlementKey
	)
	total, inserted, err := runHostPhases(ctx, st, acct, region, "appstream", 0, []hostPhase{
		{op: "appstream:DescribeStacks", run: func(p *phaseTotals) (failed bool) {
			stacks, failed = scanASStacks(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(0, "appstream:DescribeEntitlements", func() (t, i int, err error) {
			entKeys, t, i, err = scanASEntitlements(ctx, client, acct, region, st, scanID, childParentIDs(stacks))
			return t, i, err
		}),
		{op: "appstream:DescribeAppBlocks", run: func(p *phaseTotals) bool {
			return scanASAppBlocks(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "appstream:DescribeAppBlockBuilders", run: func(p *phaseTotals) bool {
			return scanASAppBlockBuilders(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "appstream:DescribeApplications", run: func(p *phaseTotals) bool {
			return scanASApplications(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "appstream:DescribeFleets", run: func(p *phaseTotals) (failed bool) {
			fleets, failed = scanASFleets(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(0, "appstream:DescribeApplicationFleetAssociations", func() (int, int, error) {
			return scanASAppFleetAssocs(ctx, client, acct, region, st, scanID, childParentIDs(fleets))
		}),
		countedPhase(0, "appstream:DescribeDirectoryConfigs", func() (int, int, error) {
			return scanASDirectoryConfigs(ctx, client, acct, region, st, scanID)
		}),
		{op: "appstream:DescribeImageBuilders", run: func(p *phaseTotals) (failed bool) {
			builders, failed = scanASImageBuilders(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "appstream:DescribeImages", run: func(p *phaseTotals) (failed bool) {
			images, failed = scanASImages(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(0, "appstream:ListAssociatedFleets", func() (int, int, error) {
			return scanASStackFleetAssocs(ctx, client, acct, region, st, scanID, childParentIDs(stacks))
		}),
		countedPhase(0, "appstream:DescribeUserStackAssociations", func() (int, int, error) {
			return scanASUserStackAssocs(ctx, client, acct, region, st, scanID, childParentIDs(stacks))
		}),
		countedPhase(0, "appstream:DescribeUsers", func() (int, int, error) {
			return scanASUsers(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "appstream:ListEntitledApplications", func() (int, int, error) {
			return scanASAppEntitlementAssocs(ctx, client, acct, region, st, scanID, entKeys)
		}),
		countedPhase(0, asImagePermissionOp, func() (int, int, error) {
			return scanASImagePermissions(ctx, client, acct, region, st, scanID, images)
		}),
		countedPhase(0, asSoftwareAssociationOp, func() (int, int, error) {
			return scanASSoftwareAssociations(ctx, client, acct, region, st, scanID, slices.Concat(images, builders))
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// asTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its listing returned. The op returns every tag in one
// response (it takes no page token); ResourceNotFoundException, the one error
// it models, means the resource was deleted after it was listed.
func asTags(client appStreamAPI) rowTagReader {
	return rowTagReader{
		op:       asTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &appstream.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// asRow returns r, the row of a listed element stored under its ARN, or no
// row when arn is empty: one empty NativeID would merge every such element
// into one resource. what names the elements in the warning, name the
// element.
func asRow(what string, arn, name *string, r *store.Resource) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, what + " have no ARN", cmp.Or(sv(name), asNoIdentifier)
	}
	return r, "", ""
}

// asLabel is a row's Name: the element's name, else its ARN.
func asLabel(name *string, arn string) *string {
	label := cmp.Or(sv(name), arn)
	return &label
}

func scanASAppBlocks(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "appstream:DescribeAppBlocks",
		func(ctx context.Context, token *string) ([]astypes.AppBlock, *string, error) {
			out, err := client.DescribeAppBlocks(ctx, &appstream.DescribeAppBlocksInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AppBlocks, out.NextToken, nil
		},
		func(b astypes.AppBlock) (*store.Resource, string, string) {
			return asRow("app blocks", b.Arn, b.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppStreamAppBlock, NativeID: sv(b.Arn),
				Name: asLabel(b.Name, sv(b.Arn)), Region: &region, AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}, tags, p)
}

func scanASAppBlockBuilders(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "appstream:DescribeAppBlockBuilders",
		func(ctx context.Context, token *string) ([]astypes.AppBlockBuilder, *string, error) {
			out, err := client.DescribeAppBlockBuilders(ctx, &appstream.DescribeAppBlockBuildersInput{MaxResults: sdkaws.Int32(asPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AppBlockBuilders, out.NextToken, nil
		},
		func(b astypes.AppBlockBuilder) (*store.Resource, string, string) {
			return asRow("app block builders", b.Arn, b.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppStreamAppBlockBuilder, NativeID: sv(b.Arn),
				Name: asLabel(b.Name, sv(b.Arn)), Region: &region, AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanASImages stores customer (PRIVATE) AppStream images. PUBLIC base images
// are AWS-managed and unbounded (mirrors the AMI Owners=["self"] convention);
// SHARED images belong to other accounts. Every image listed is this
// account's own, so each has its tags read. NativeID = Arn. It returns the
// images stored that carry a name, which is what DescribeImagePermissions
// keys on.
func scanASImages(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "appstream:DescribeImages", "images", "Name",
		func(ctx context.Context, token *string) ([]astypes.Image, *string, error) {
			out, err := client.DescribeImages(ctx, &appstream.DescribeImagesInput{Type: astypes.VisibilityTypePrivate, MaxResults: sdkaws.Int32(asPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Images, out.NextToken, nil
		},
		func(img astypes.Image) (*store.Resource, string, string) {
			return asRow("images", img.Arn, img.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppStreamImage, NativeID: sv(img.Arn),
				Name: asLabel(img.Name, sv(img.Arn)), Region: &region, CreatedAt: tp(img.CreatedTime),
				AttributesJSON: mustJSON(img), DiscoveredBy: scanID,
			})
		},
		func(img astypes.Image) string { return sv(img.Name) }, tags, p)
}

func scanASApplications(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "appstream:DescribeApplications",
		func(ctx context.Context, token *string) ([]astypes.Application, *string, error) {
			out, err := client.DescribeApplications(ctx, &appstream.DescribeApplicationsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Applications, out.NextToken, nil
		},
		func(a astypes.Application) (*store.Resource, string, string) {
			return asRow("applications", a.Arn, a.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppStreamApplication, NativeID: sv(a.Arn),
				Name: asLabel(a.Name, sv(a.Arn)), Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanASAppFleetAssocs — DescribeApplicationFleetAssociations requires a
// FleetName or ApplicationArn filter; a blanket call returns nothing. It
// describes the associations of each fleet the fleet phase stored, the
// fleets read concurrently (storeTokenEachParent): one fleet's failure keeps
// the rows of the others and those read before it, a denial warns once, and
// a repeated token is an error. The op models no not-found code, so a fleet
// deleted since it was listed fails like any other error.
func scanASAppFleetAssocs(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string, fleetNames []string) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, "appstream:DescribeApplicationFleetAssociations", nil, fleetNames,
		func(ctx context.Context, fleet string, token *string) ([]astypes.ApplicationFleetAssociation, *string, error) {
			out, err := client.DescribeApplicationFleetAssociations(ctx, &appstream.DescribeApplicationFleetAssociationsInput{FleetName: &fleet, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ApplicationFleetAssociations, out.NextToken, nil
		},
		func(fleet string, a astypes.ApplicationFleetAssociation) (*store.Resource, string, string) {
			appArn := sv(a.ApplicationArn)
			if appArn == "" {
				return nil, "application-fleet associations have no application ARN", fleet
			}
			label := fleet + "/" + appArn
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type:     TypeAppStreamApplicationFleetAssociation,
				NativeID: asARN(acct.partition(), region, acct.ID, "application-fleet-association", label),
				Name:     &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}, "", ""
		})
}

func asARN(partition, region, acct, kind, id string) string {
	return arnFor(partition, "appstream", region, acct, kind+"/"+id)
}

// scanASFleets stores the fleets and returns those stored with a name, which
// DescribeApplicationFleetAssociations keys on.
func scanASFleets(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "appstream:DescribeFleets", "fleets", "Name",
		func(ctx context.Context, token *string) ([]astypes.Fleet, *string, error) {
			out, err := client.DescribeFleets(ctx, &appstream.DescribeFleetsInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Fleets, out.NextToken, nil
		},
		func(f astypes.Fleet) (*store.Resource, string, string) {
			// An ARN-less fleet is warned about and not stored, so its application
			// associations are not read either; the model requires Fleet.Arn.
			return asRow("fleets", f.Arn, f.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppStreamFleet, NativeID: sv(f.Arn),
				Name: asLabel(f.Name, sv(f.Arn)), Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		},
		func(f astypes.Fleet) string { return sv(f.Name) }, tags, p)
}

// scanASImageBuilders stores the image builders a page at a time with their
// tags and returns every one stored, as owners for the software-association
// phase, which keys on the ARN each has.
func scanASImageBuilders(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (builders []childParent, storeWriteFailed bool) {
	steps := tokenPageSteps{stored: func(rows []*store.Resource) {
		for _, r := range rows {
			builders = append(builders, childParent{id: r.NativeID, arn: r.NativeID})
		}
	}}
	failed := storeTokenPagesTagsWith(ctx, st, acct, region, "appstream:DescribeImageBuilders",
		func(ctx context.Context, token *string) ([]astypes.ImageBuilder, *string, error) {
			out, err := client.DescribeImageBuilders(ctx, &appstream.DescribeImageBuildersInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ImageBuilders, out.NextToken, nil
		},
		func(b astypes.ImageBuilder) (*store.Resource, string, string) {
			return asRow("image builders", b.Arn, b.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppStreamImageBuilder, NativeID: sv(b.Arn),
				Name: asLabel(b.Name, sv(b.Arn)), Region: &region, AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}, steps, tags, p)
	return builders, failed
}

// scanASStacks stores the stacks and returns those stored with a name, which
// their child listings key on.
func scanASStacks(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "appstream:DescribeStacks", "stacks", "Name",
		func(ctx context.Context, token *string) ([]astypes.Stack, *string, error) {
			out, err := client.DescribeStacks(ctx, &appstream.DescribeStacksInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Stacks, out.NextToken, nil
		},
		func(s astypes.Stack) (*store.Resource, string, string) {
			return asRow("stacks", s.Arn, s.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAppStreamStack, NativeID: sv(s.Arn),
				Name: asLabel(s.Name, sv(s.Arn)), Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		},
		func(s astypes.Stack) string { return sv(s.Name) }, tags, p)
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
			arn := asARN(acct.partition(), region, acct.ID, "directory-config", name)
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
				arn := asARN(acct.partition(), region, acct.ID, "entitlement", sn+"/"+name)
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
				arn := asARN(acct.partition(), region, acct.ID, "stack-fleet-association", sn+"/"+fname)
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
				arn := asARN(acct.partition(), region, acct.ID, "user-stack-association", stack+"/"+string(a.AuthenticationType)+"/"+user)
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
				arn := asARN(acct.partition(), region, acct.ID, "application-entitlement-association", k.stack+"/"+k.entitlement+"/"+appID)
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
	return childFanOutSkipTransient(ctx, st, acct, region, asImagePermissionOp, images, isASParentGone, byParentID,
		func(ctx context.Context, img childParent) ([]*store.Resource, error) {
			return listASImagePermissions(ctx, client, acct, region, scanID, img)
		})
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
	var notPermitted sync.Once
	return childFanOutSkipTransient(ctx, st, acct, region, asSoftwareAssociationOp, byARN, isASParentGone, byParentID,
		func(ctx context.Context, owner childParent) ([]*store.Resource, error) {
			rows, err := listASSoftwareAssociations(ctx, client, acct, region, scanID, owner)
			if isAPIErrorCode(err, "OperationNotPermittedException") {
				notPermitted.Do(func() { warnASSoftwareNotPermitted(st, acct.ID, region, owner.id, err) })
				return nil, nil
			}
			return rows, err
		})
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
		if done, err := lastPage(in.NextToken, out.NextToken); done {
			return rows, err
		}
		in.NextToken = out.NextToken
	}
}
