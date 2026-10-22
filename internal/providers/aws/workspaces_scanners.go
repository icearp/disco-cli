package aws

import (
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/workspaces"
	wstypes "github.com/aws/aws-sdk-go-v2/service/workspaces/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeWorkSpacesWorkspace, Service: "work-spaces"})
	registerType(restype.Descriptor{Type: TypeWorkSpacesConnectionAlias, Service: "work-spaces"})
	registerType(restype.Descriptor{Type: TypeWorkSpacesWorkspacesPool, Service: "work-spaces"})
	registerType(restype.Descriptor{Type: TypeWorkSpacesDirectory, Service: "work-spaces"})
	registerType(restype.Descriptor{Type: TypeWorkSpacesWorkspaceBundle, Service: "work-spaces"})
	registerType(restype.Descriptor{Type: TypeWorkSpacesWorkspaceImage, Service: "work-spaces"})
	registerType(restype.Descriptor{Type: TypeWorkSpacesWorkspaceIPGroup, Service: "work-spaces"})
	registerType(restype.Descriptor{Type: TypeWorkSpacesWorkspaceApplication, Service: "work-spaces"})
	registerType(restype.Descriptor{Type: TypeWorkSpacesAccountLink, Service: "work-spaces"})
	registerType(restype.Descriptor{Type: TypeWorkSpacesConnectClientAddIn, Service: "work-spaces"})
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
	ListAccountLinks(context.Context, *workspaces.ListAccountLinksInput, ...func(*workspaces.Options)) (*workspaces.ListAccountLinksOutput, error)
	DescribeConnectClientAddIns(context.Context, *workspaces.DescribeConnectClientAddInsInput, ...func(*workspaces.Options)) (*workspaces.DescribeConnectClientAddInsOutput, error)
	DescribeConnectionAliasPermissions(context.Context, *workspaces.DescribeConnectionAliasPermissionsInput, ...func(*workspaces.Options)) (*workspaces.DescribeConnectionAliasPermissionsOutput, error)
}

// wsPageLimit is the model's cap on MaxResults (Limit, 1-25) for the
// account-link, client add-in and connection-alias permission lists.
const wsPageLimit = 25

// scanWorkSpaces discovers WorkSpaces workspaces, connection aliases,
// workspaces pools, directories, bundles, images, IP groups, applications,
// account links, the directories' Amazon Connect client add-ins and the
// sharing permissions of the account's own connection aliases. Most types
// synth an ARN from their id; WorkspacesPool exposes PoolArn natively.
//
// The older phases (workspaces through applications) stop at their first hard
// error. The account-link, add-in and alias-permission phases run whatever
// any other phase returned, so their failure costs no other phase its rows
// and an older phase's failure does not skip them. Each result has its
// transient error demoted to a warning, since one transient member would make
// the dispatcher read the whole join as transient, and the rest are joined;
// the counts of all of them are summed. The add-in and alias-permission
// phases fan out over the directories and aliases stored this scan, so they
// list none when the older phases stopped before or at the phase that lists
// their parents.
func scanWorkSpaces(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := workspaces.NewFromConfig(acct.cfg, func(o *workspaces.Options) { o.Region = region })
	total, inserted, parents, err := scanWSOlderPhases(ctx, client, acct, region, st, scanID)
	errs := []error{demoteTransient(st, "workspaces", acct, region, err)}
	for _, phase := range []struct {
		op  string
		run func() (int, int, error)
	}{
		{"workspaces:ListAccountLinks", func() (int, int, error) { return scanWSAccountLinks(ctx, client, acct, region, st, scanID) }},
		{"workspaces:DescribeConnectClientAddIns", func() (int, int, error) {
			return scanWSConnectClientAddIns(ctx, client, acct, region, st, scanID, parents.directories)
		}},
		{wsDescribeAliasPermissionsOp, func() (int, int, error) {
			return scanWSConnectionAliasPermissions(ctx, client, acct, region, st, scanID, parents.aliases)
		}},
	} {
		t, i, perr := phase.run()
		total += t
		inserted += i
		errs = append(errs, demoteTransient(st, phase.op, acct, region, perr))
	}
	return total, inserted, errors.Join(errs...)
}

// wsParents are the parents the older phases stored, for the child phases
// that fan out over them.
type wsParents struct {
	directories []childParent
	// aliases holds only the connection aliases the account owns.
	aliases []childParent
}

// scanWSOlderPhases runs the phases that predate account links and add-ins,
// stopping at the first hard error. It also returns the parents stored before
// that error.
func scanWSOlderPhases(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, parents wsParents, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanWSWorkspaces(ctx, client, acct, region, st, scanID) },
		func() (t, i int, err error) {
			t, i, parents.aliases, err = scanWSConnectionAliases(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) { return scanWSWorkspacesPools(ctx, client, acct, region, st, scanID) },
		func() (t, i int, err error) {
			t, i, parents.directories, err = scanWSDirectories(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) { return scanWSBundles(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWSImages(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWSIpGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanWSApplications(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, parents, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, parents, nil
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

// scanWSConnectionAliases also returns the stored aliases whose
// OwnerAccountId is this account, for the permission phase. The permissions
// are the grants an alias's owner made, so they belong under the owner's
// row; whether the list ever returns an alias shared with this account is
// unverified, and such an alias is not handed on. OwnerAccountId is
// model-optional: an alias without it is not handed on either, and the
// stored ones are counted in one warning.
func scanWSConnectionAliases(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	var batch []*store.Resource
	var owned []childParent
	noOwner := 0
	var nextToken *string
	for {
		out, err := client.DescribeConnectionAliases(ctx, &workspaces.DescribeConnectionAliasesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "workspaces:DescribeConnectionAliases", acct.ID, region, err)
			}
			return 0, 0, nil, fmt.Errorf("workspaces:DescribeConnectionAliases: %w", err)
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
			switch sv(a.OwnerAccountId) {
			case acct.ID:
				owned = append(owned, childParent{id: id, arn: arn})
			case "":
				noOwner++
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	total, inserted, err := upsertBatch(st, batch, "workspaces connection-aliases")
	if err != nil {
		return 0, 0, nil, err
	}
	if noOwner > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: wsDescribeAliasPermissionsOp, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d alias(es) with no OwnerAccountId; permissions not read", noOwner),
		})
	}
	return total, inserted, owned, nil
}

func scanWSWorkspacesPools(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.DescribeWorkspacesPools(ctx, &workspaces.DescribeWorkspacesPoolsInput{NextToken: nextToken})
		if err != nil {
			// Pools is a per-region sub-feature, and closed to new customers
			// since 7/30/2026. Both facts arrive as a canned
			// AccessDeniedException — see isWorkSpacesPoolsGap for the two
			// phrasings. Silent-skip those; warn on real denials.
			if isWorkSpacesPoolsGap(err) {
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

// scanWSDirectories also returns the stored directories for the client
// add-in phase.
func scanWSDirectories(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	pager := workspaces.NewDescribeWorkspaceDirectoriesPaginator(client, &workspaces.DescribeWorkspaceDirectoriesInput{})
	var batch []*store.Resource
	var parents []childParent
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "workspaces:DescribeWorkspaceDirectories", acct.ID, region, err)
			}
			return 0, 0, nil, fmt.Errorf("workspaces:DescribeWorkspaceDirectories: %w", err)
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
			parents = append(parents, childParent{id: id, arn: arn})
		}
	}
	total, inserted, err := upsertBatch(st, batch, "workspaces directories")
	if err != nil {
		return 0, 0, nil, err
	}
	return total, inserted, parents, nil
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

// scanWSAccountLinks lists the account's BYOL account links in every status
// the API returns. A link has no ARN (the Service Reference documents none),
// so its NativeID is the synthetic
// arn:aws:workspaces:{region}:{account}:accountlink/{AccountLinkId}, shaped
// like the file's other synthesized ARNs. A LINK_NOT_FOUND element names a
// link that does not exist and is skipped. A denial on any page warns once
// and keeps the links already listed. Account linking is not offered in every
// region (AWS GovCloud has none), so a first page the endpoint does not serve
// is a silent gap.
func scanWSAccountLinks(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "workspaces:ListAccountLinks"
	pager := workspaces.NewListAccountLinksPaginator(client, &workspaces.ListAccountLinksInput{},
		func(o *workspaces.ListAccountLinksPaginatorOptions) { o.Limit = wsPageLimit })
	var batch []*store.Resource
	for firstPage := true; pager.HasMorePages(); firstPage = false {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if firstPage && isOpNotDeployed(err) {
				return 0, 0, nil
			}
			if !isAccessDenied(err) {
				return 0, 0, fmt.Errorf("%s: %w", op, err)
			}
			_ = skipIfAccessDenied(st, op, acct.ID, region, err)
			break
		}
		for _, l := range out.AccountLinks {
			id := sv(l.AccountLinkId)
			if id == "" || l.AccountLinkStatus == wstypes.AccountLinkStatusEnumLinkNotFound {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWorkSpacesAccountLink, NativeID: wsAccountLinkNativeID(region, acct.ID, id),
				Name: &id, Region: &region, Status: nonEmptyPtr(string(l.AccountLinkStatus)),
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "workspaces account-links")
}

func wsAccountLinkNativeID(region, acctID, linkID string) string {
	return fmt.Sprintf("arn:aws:workspaces:%s:%s:accountlink/%s", region, acctID, linkID)
}

// isWSDirectoryGone matches a directory deregistered between its list call
// and the call that lists its client add-ins.
func isWSDirectoryGone(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }

// scanWSConnectClientAddIns lists the client add-ins of every directory and
// links each under its directory. A directory whose call still fails
// transiently after the SDK's retries is skipped, keeping its siblings' rows,
// and the skips are reported in one warning.
func scanWSConnectClientAddIns(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string, directories []childParent) (int, int, error) {
	const op = "workspaces:DescribeConnectClientAddIns"
	return childFanOutSkipTransient(ctx, st, acct, region, op, directories, isWSDirectoryGone, byParentID,
		func(ctx context.Context, dir childParent) ([]*store.Resource, error) {
			return listWSConnectClientAddIns(ctx, client, acct, region, scanID, dir)
		})
}

// listWSConnectClientAddIns lists the Amazon Connect client add-ins of one
// directory. An add-in has no ARN, so its NativeID is
// {directoryARN}/connect-client-add-in/{AddInId}. Its URL is the Connect
// instance endpoint the client opens, not a credential, so it needs no Redact
// rule.
func listWSConnectClientAddIns(ctx context.Context, client workSpacesAPI, acct *account, region, scanID string, dir childParent) ([]*store.Resource, error) {
	var rows []*store.Resource
	in := &workspaces.DescribeConnectClientAddInsInput{ResourceId: &dir.id, MaxResults: sdkaws.Int32(wsPageLimit)}
	for {
		out, err := client.DescribeConnectClientAddIns(ctx, in)
		if err != nil {
			return nil, err
		}
		for _, a := range out.AddIns {
			id := sv(a.AddInId)
			if id == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWorkSpacesConnectClientAddIn, NativeID: dir.arn + "/connect-client-add-in/" + id,
				Name: a.Name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		// DescribeConnectClientAddIns has no paginator; a repeated token would
		// loop forever.
		if done, err := lastPage(in.NextToken, out.NextToken); done {
			return rows, err
		}
		in.NextToken = out.NextToken
	}
}
