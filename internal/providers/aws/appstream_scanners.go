package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/appstream"
	astypes "github.com/aws/aws-sdk-go-v2/service/appstream/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:appstream",
		fn:   scanAppStream,
		emits: []coverage.TypeDecl{
			{Service: "appstream", DiscoType: TypeAppStreamAppBlock},
			{Service: "appstream", DiscoType: TypeAppStreamAppBlockBuilder},
			{Service: "appstream", DiscoType: TypeAppStreamApplication},
			{Service: "appstream", DiscoType: TypeAppStreamApplicationEntitlementAssociation},
			{Service: "appstream", DiscoType: TypeAppStreamApplicationFleetAssociation},
			{Service: "appstream", DiscoType: TypeAppStreamDirectoryConfig},
			{Service: "appstream", DiscoType: TypeAppStreamEntitlement},
			{Service: "appstream", DiscoType: TypeAppStreamFleet},
			{Service: "appstream", DiscoType: TypeAppStreamImageBuilder},
			{Service: "appstream", DiscoType: TypeAppStreamStack},
			{Service: "appstream", DiscoType: TypeAppStreamStackFleetAssociation},
			{Service: "appstream", DiscoType: TypeAppStreamStackUserAssociation},
			{Service: "appstream", DiscoType: TypeAppStreamUser},
		},
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

// scanASAppFleetAssocs — DescribeApplicationFleetAssociations without a
// FleetName/ApplicationArn filter returns nothing; AWS requires one of
// the two. Skip global pass and run per-fleet inside scanASFleets is one
// option; simpler approach: call DescribeFleets to collect fleet names,
// then per-fleet describe assocs.
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

// scanASUsers iterates the AuthenticationType values DescribeUsers actually
// supports — USERPOOL (managed users) and API. SAML is not a valid filter
// (SAML federation users are not first-class user-pool entries; AWS rejects
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
