package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/workspaces"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeWorkSpacesWorkspace, Service: "work-spaces", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWorkSpacesConnectionAlias, Service: "work-spaces", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWorkSpacesWorkspacesPool, Service: "work-spaces", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWorkSpacesDirectory, Service: "work-spaces", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWorkSpacesWorkspaceBundle, Service: "work-spaces", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWorkSpacesWorkspaceImage, Service: "work-spaces", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWorkSpacesWorkspaceIPGroup, Service: "work-spaces", Leaf: true})
	registerType(restype.Descriptor{Type: TypeWorkSpacesWorkspaceApplication, Service: "work-spaces", Leaf: true})
	registerService(serviceEntry{
		name: "aws:work-spaces",
		fn:   scanWorkSpaces,
	})
}

type workSpacesAPI interface {
	DescribeWorkspaces(context.Context, *workspaces.DescribeWorkspacesInput, ...func(*workspaces.Options)) (*workspaces.DescribeWorkspacesOutput, error)
	DescribeConnectionAliases(context.Context, *workspaces.DescribeConnectionAliasesInput, ...func(*workspaces.Options)) (*workspaces.DescribeConnectionAliasesOutput, error)
	DescribeWorkspacesPools(context.Context, *workspaces.DescribeWorkspacesPoolsInput, ...func(*workspaces.Options)) (*workspaces.DescribeWorkspacesPoolsOutput, error)
	DescribeWorkspaceDirectories(context.Context, *workspaces.DescribeWorkspaceDirectoriesInput, ...func(*workspaces.Options)) (*workspaces.DescribeWorkspaceDirectoriesOutput, error)
	DescribeWorkspaceBundles(context.Context, *workspaces.DescribeWorkspaceBundlesInput, ...func(*workspaces.Options)) (*workspaces.DescribeWorkspaceBundlesOutput, error)
	DescribeWorkspaceImages(context.Context, *workspaces.DescribeWorkspaceImagesInput, ...func(*workspaces.Options)) (*workspaces.DescribeWorkspaceImagesOutput, error)
	DescribeIpGroups(context.Context, *workspaces.DescribeIpGroupsInput, ...func(*workspaces.Options)) (*workspaces.DescribeIpGroupsOutput, error)
	DescribeApplications(context.Context, *workspaces.DescribeApplicationsInput, ...func(*workspaces.Options)) (*workspaces.DescribeApplicationsOutput, error)
}

// scanWorkSpaces discovers WorkSpaces workspaces, connection aliases, and
// workspaces pools. Workspaces and aliases synth ARN from ID;
// WorkspacesPool exposes PoolArn natively.
func scanWorkSpaces(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := workspaces.NewFromConfig(acct.cfg, func(o *workspaces.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanWSWorkspaces(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWSConnectionAliases(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWSWorkspacesPools(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWSDirectories(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWSBundles(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWSImages(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWSIpGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWSApplications(ctx, client, acct, region, st, scanID) },
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

func scanWSWorkspaces(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := workspaces.NewDescribeWorkspacesPaginator(client, &workspaces.DescribeWorkspacesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "workspaces:DescribeWorkspaces", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("workspaces:DescribeWorkspaces: %w", err)
		}
		for _, w := range out.Workspaces {
			id := sv(w.WorkspaceId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:workspaces:%s:%s:workspace/%s", region, acct.ID, id)
			label := id
			status := string(w.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWorkSpacesWorkspace, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "workspaces")
}

func scanWSConnectionAliases(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.DescribeConnectionAliases(ctx, &workspaces.DescribeConnectionAliasesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "workspaces:DescribeConnectionAliases", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("workspaces:DescribeConnectionAliases: %w", err)
		}
		for _, a := range out.ConnectionAliases {
			id := sv(a.AliasId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:workspaces:%s:%s:connectionalias/%s", region, acct.ID, id)
			label := sv(a.ConnectionString)
			if label == "" {
				label = id
			}
			status := string(a.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWorkSpacesConnectionAlias, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "workspaces connection-aliases")
}

func scanWSWorkspacesPools(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.DescribeWorkspacesPools(ctx, &workspaces.DescribeWorkspacesPoolsInput{NextToken: nextToken})
		if err != nil {
			// WorkSpaces Pools is a per-region sub-feature. Where not launched,
			// AWS returns a canned AccessDeniedException pointing at the
			// workspaces-access-control docs URL — distinct from a real IAM
			// denial, which carries the "is not authorized to perform:
			// <action>" SDK-formatted body. Silent-skip the canned shape;
			// warn on real denials.
			if isAccessDeniedWithMessage(err, "workspaces-access-control.html") {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "workspaces:DescribeWorkspacesPools", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("workspaces:DescribeWorkspacesPools: %w", err)
		}
		for _, p := range out.WorkspacesPools {
			arn := sv(p.PoolArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWorkSpacesWorkspacesPool, NativeID: arn,
				Name: p.PoolName, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "workspaces workspaces-pools")
}

func scanWSDirectories(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := workspaces.NewDescribeWorkspaceDirectoriesPaginator(client, &workspaces.DescribeWorkspaceDirectoriesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "workspaces:DescribeWorkspaceDirectories", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("workspaces:DescribeWorkspaceDirectories: %w", err)
		}
		for _, d := range out.Directories {
			id := sv(d.DirectoryId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:workspaces:%s:%s:directory/%s", region, acct.ID, id)
			label := sv(d.DirectoryName)
			if label == "" {
				label = id
			}
			status := string(d.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWorkSpacesDirectory, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "workspaces directories")
}

// scanWSBundles lists the account's own WorkSpace bundles. DescribeWorkspaceBundles
// takes no Owner field, so an empty request returns only account-owned bundles
// (not the AMAZON public catalogue); any AMAZON-owned bundle that surfaces is
// flagged ManagedByProvider.
func scanWSBundles(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := workspaces.NewDescribeWorkspaceBundlesPaginator(client, &workspaces.DescribeWorkspaceBundlesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "workspaces:DescribeWorkspaceBundles", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("workspaces:DescribeWorkspaceBundles: %w", err)
		}
		for _, b := range out.Bundles {
			id := sv(b.BundleId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:workspaces:%s:%s:workspacebundle/%s", region, acct.ID, id)
			label := sv(b.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWorkSpacesWorkspaceBundle, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
				ManagedByProvider: sv(b.Owner) == "AMAZON",
			})
		}
	}
	return upsertBatch(st, batch, "workspaces workspace-bundles")
}

func scanWSImages(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.DescribeWorkspaceImages(ctx, &workspaces.DescribeWorkspaceImagesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "workspaces:DescribeWorkspaceImages", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("workspaces:DescribeWorkspaceImages: %w", err)
		}
		for _, im := range out.Images {
			id := sv(im.ImageId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:workspaces:%s:%s:workspaceimage/%s", region, acct.ID, id)
			label := sv(im.Name)
			if label == "" {
				label = id
			}
			status := string(im.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWorkSpacesWorkspaceImage, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(im), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "workspaces workspace-images")
}

func scanWSIpGroups(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.DescribeIpGroups(ctx, &workspaces.DescribeIpGroupsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "workspaces:DescribeIpGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("workspaces:DescribeIpGroups: %w", err)
		}
		for _, g := range out.Result {
			id := sv(g.GroupId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:workspaces:%s:%s:workspaceipgroup/%s", region, acct.ID, id)
			label := sv(g.GroupName)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWorkSpacesWorkspaceIPGroup, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "workspaces workspace-ip-groups")
}

// scanWSApplications lists WorkSpace applications available to the account.
// AMAZON-owned (AWS-published) applications are flagged ManagedByProvider so
// they hide from the default resource view.
func scanWSApplications(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := workspaces.NewDescribeApplicationsPaginator(client, &workspaces.DescribeApplicationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "workspaces:DescribeApplications", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("workspaces:DescribeApplications: %w", err)
		}
		for _, a := range out.Applications {
			id := sv(a.ApplicationId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:workspaces:%s:%s:workspaceapplication/%s", region, acct.ID, id)
			label := sv(a.Name)
			if label == "" {
				label = id
			}
			status := string(a.State)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWorkSpacesWorkspaceApplication, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				ManagedByProvider: sv(a.Owner) == "AMAZON",
			})
		}
	}
	return upsertBatch(st, batch, "workspaces workspace-applications")
}
