package aws

import (
	"context"
	"fmt"

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
	registerType(restype.Descriptor{Type: TypeAppStreamImage, Service: "appstream", Leaf: true})
	registerType(restype.Descriptor{Type: TypeAppStreamStack, Service: "appstream"})
	registerType(restype.Descriptor{Type: TypeAppStreamStackFleetAssociation, Service: "appstream"})
	registerType(restype.Descriptor{Type: TypeAppStreamStackUserAssociation, Service: "appstream"})
	registerType(restype.Descriptor{Type: TypeAppStreamUser, Service: "appstream", Leaf: true})
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

func scanAppStream(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	if !appStreamSupportedRegions[region] {
		return 0, 0, nil
	}
	client := appstream.NewFromConfig(acct.cfg, func(o *appstream.Options) { o.Region = region })

	stackNames, t, i, ferr := scanASStacks(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i

	entKeys, t, i, ferr := scanASEntitlements(ctx, client, acct, region, st, scanID, stackNames)
	if ferr != nil {
		return total, inserted, ferr
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
		func() (int, int, error) { return scanASImageBuilders(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanASImages(ctx, client, acct, region, st, scanID) },
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
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
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
func scanASImages(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := appstream.NewDescribeImagesPaginator(client, &appstream.DescribeImagesInput{Type: astypes.VisibilityTypePrivate})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "appstream:DescribeImages", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("appstream:DescribeImages: %w", perr)
		}
		for _, img := range out.Images {
			arn := sv(img.Arn)
			if arn == "" {
				continue
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
	return upsertBatch(st, batch, "appstream images")
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

func scanASImageBuilders(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.DescribeImageBuilders(ctx, &appstream.DescribeImageBuildersInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "appstream:DescribeImageBuilders", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("appstream:DescribeImageBuilders: %w", err)
		}
		for _, b := range out.ImageBuilders {
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
				Type: TypeAppStreamImageBuilder, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return upsertBatch(st, batch, "appstream image-builders")
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

// scanASUsers iterates the AuthenticationType values DescribeUsers supports
// — USERPOOL (managed users) and API. SAML is not a valid filter (SAML
// federation users aren't first-class user-pool entries; AWS rejects
// `'SAML' is not a supported authentication type for describing users`).
func scanASUsers(ctx context.Context, client appStreamAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	authTypes := []astypes.AuthenticationType{
		astypes.AuthenticationTypeUserpool,
		astypes.AuthenticationTypeApi,
	}
	var batch []*store.Resource
	for _, at := range authTypes {
		auth := at
		var token *string
		for {
			out, err := client.DescribeUsers(ctx, &appstream.DescribeUsersInput{AuthenticationType: auth, NextToken: token})
			if err != nil {
				if isAccessDenied(err) {
					break
				}
				if isAPIErrorCode(err, "InvalidParameterValueException") {
					break
				}
				return 0, 0, fmt.Errorf("appstream:DescribeUsers %s: %w", auth, err)
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
