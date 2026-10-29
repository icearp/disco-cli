package aws

import (
	"cmp"
	"context"
	"fmt"
	"strings"

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
	DescribeTags(context.Context, *workspaces.DescribeTagsInput, ...func(*workspaces.Options)) (*workspaces.DescribeTagsOutput, error)
}

// wsPageLimit is the model's cap (1-25) on the page size (Limit or
// MaxResults) the WorkSpaces listings here send.
const wsPageLimit = 25

const wsTagsOp = "workspaces:DescribeTags"

// scanWorkSpaces discovers WorkSpaces workspaces, connection aliases,
// workspaces pools, directories, bundles, images, IP groups, applications,
// account links, the directories' Amazon Connect client add-ins and the
// sharing permissions of the account's own connection aliases. Most types
// synth an ARN from their id; WorkspacesPool exposes PoolArn natively.
//
// Every listing runs as its own phase, one at a time, in the order below
// (runHostPhases): a phase's error does not stop the later ones; a failed
// write and ctx's end do. The add-in and alias-permission phases fan out over
// the directories and owned aliases their parent phases stored. Workspaces,
// connection aliases, pools, directories, bundles, images and IP groups have
// their tags read per row (wsTags), each such listing written a page at a
// time, and the tag denials of the whole scan are one warning.
func scanWorkSpaces(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := workspaces.NewFromConfig(acct.cfg, func(o *workspaces.Options) { o.Region = region })
	tags := wsTags(client).sharingDenials()
	var directories, aliases []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "workspaces", 0, []hostPhase{
		{op: "workspaces:DescribeWorkspaces", run: func(p *phaseTotals) bool {
			return scanWSWorkspaces(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "workspaces:DescribeConnectionAliases", run: func(p *phaseTotals) (failed bool) {
			aliases, failed = scanWSConnectionAliases(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "workspaces:DescribeWorkspacesPools", run: func(p *phaseTotals) bool {
			return scanWSWorkspacesPools(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "workspaces:DescribeWorkspaceDirectories", run: func(p *phaseTotals) (failed bool) {
			directories, failed = scanWSDirectories(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "workspaces:DescribeWorkspaceBundles", run: func(p *phaseTotals) bool {
			return scanWSBundles(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "workspaces:DescribeWorkspaceImages", run: func(p *phaseTotals) bool {
			return scanWSImages(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "workspaces:DescribeIpGroups", run: func(p *phaseTotals) bool {
			return scanWSIpGroups(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "workspaces:DescribeApplications", func() (int, int, error) {
			return scanWSApplications(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "workspaces:ListAccountLinks", func() (int, int, error) {
			return scanWSAccountLinks(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "workspaces:DescribeConnectClientAddIns", func() (int, int, error) {
			return scanWSConnectClientAddIns(ctx, client, acct, region, st, scanID, directories)
		}),
		countedPhase(0, wsDescribeAliasPermissionsOp, func() (int, int, error) {
			return scanWSConnectionAliasPermissions(ctx, client, acct, region, st, scanID, aliases)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// wsTags reads a row's tags with DescribeTags, which takes the resource's id,
// not its ARN (wsResourceID). Its model documents WorkSpaces, registered
// directories, images, custom bundles, IP access control groups and connection
// aliases as the types it takes. Pools are not in that list, which predates
// them, but CreateWorkspacesPool takes Tags and DescribeTags is the service's
// only tag read, so pools are read too; that it accepts a pool id is
// unverified. The op answers with every
// tag at once (no page token); ResourceNotFoundException is the only error
// its model lists. A ManagedByProvider row (an AMAZON bundle) is not read:
// DescribeTags documents custom bundles only.
func wsTags(client workSpacesAPI) rowTagReader {
	return rowTagReader{
		op:       wsTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if r.ManagedByProvider {
				return r.TagsJSON, nil
			}
			id := wsResourceID(r.NativeID)
			out, err := client.DescribeTags(ctx, &workspaces.DescribeTagsInput{ResourceId: &id})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.TagList, func(t wstypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// wsResourceID is the id of the resource a row's NativeID names: the text
// after its last "/". Every tagged type's NativeID is an ARN ending in
// <kind>/<id> (the Service Reference's ARN formats, a pool's PoolArn
// included), and no id pattern in the model allows a "/".
func wsResourceID(nativeID string) string {
	return nativeID[strings.LastIndexByte(nativeID, '/')+1:]
}

// wsOwnedElsewhereTags is tags for a listing whose elements name their owner
// (OwnerAccountId, model-optional): owners holds it, by NativeID, for the rows
// of the page being read. An image or connection alias another account
// shares with this one is stored under this account's ARN form, so its owner
// is taken from owners: such a row whose read is denied or not found is kept
// untagged and silent. A row without OwnerAccountId is taken as this
// account's own.
func wsOwnedElsewhereTags(tags rowTagReader, acctID string, owners map[string]string) rowTagReader {
	return tags.untaggedIfOwnerElsewhere(acctID, func(r *store.Resource) string { return owners[r.NativeID] })
}

// wsRow is a row of a listing whose type has no ARN of its own in the
// element: its NativeID is arn:{p}:workspaces:{region}:{account}:{kind}/{id}
// and its Name label, else id.
func wsRow(acct *account, region, scanID, typ, kind, id, label string, status *string, attrs any) *store.Resource {
	name := cmp.Or(label, id)
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arnFor(acct.partition(), "workspaces", region, acct.ID, kind+"/"+id),
		Name: &name, Region: &region, Status: status,
		AttributesJSON: mustJSON(attrs), DiscoveredBy: scanID,
	}
}

// wsNoID is wsRow's caller's answer for an element without its id: no row,
// the reason, and the element's label (noIdentifier when empty) as the key
// naming it in the warning.
func wsNoID(what, idField, label string) (*store.Resource, string, string) {
	return nil, what + " have no " + idField, cmp.Or(label, noIdentifier)
}

func scanWSWorkspaces(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "workspaces:DescribeWorkspaces",
		func(ctx context.Context, token *string) ([]wstypes.Workspace, *string, error) {
			out, err := client.DescribeWorkspaces(ctx, &workspaces.DescribeWorkspacesInput{Limit: sdkaws.Int32(wsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Workspaces, out.NextToken, nil
		},
		func(w wstypes.Workspace) (*store.Resource, string, string) {
			id := sv(w.WorkspaceId)
			if id == "" {
				return wsNoID("workspaces", "WorkspaceId", sv(w.ComputerName))
			}
			status := string(w.State)
			return wsRow(acct, region, scanID, TypeWorkSpacesWorkspace, "workspace", id, "", &status, w), "", ""
		}, tags, p)
}

// scanWSConnectionAliases also returns the stored aliases whose
// OwnerAccountId is this account, for the permission phase. The permissions
// are the grants an alias's owner made, so they belong under the owner's
// row; whether the list ever returns an alias shared with this account is
// unverified, and such an alias is not handed on, and its tags are read
// under otherAccountGap (wsOwnedElsewhereTags). OwnerAccountId is
// model-optional: an alias without it is not handed on either, and the
// stored ones are counted in one warning.
func scanWSConnectionAliases(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (owned []childParent, storeWriteFailed bool) {
	owners := map[string]string{}
	noOwner := 0
	failed := storeTokenPagesTagsWith(ctx, st, acct, region, "workspaces:DescribeConnectionAliases",
		func(ctx context.Context, token *string) ([]wstypes.ConnectionAlias, *string, error) {
			out, err := client.DescribeConnectionAliases(ctx, &workspaces.DescribeConnectionAliasesInput{Limit: sdkaws.Int32(wsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ConnectionAliases, out.NextToken, nil
		},
		func(a wstypes.ConnectionAlias) (*store.Resource, string, string) {
			id := sv(a.AliasId)
			if id == "" {
				return wsNoID("connection aliases", "AliasId", sv(a.ConnectionString))
			}
			status := string(a.State)
			r := wsRow(acct, region, scanID, TypeWorkSpacesConnectionAlias, "connectionalias", id, sv(a.ConnectionString), &status, a)
			owners[r.NativeID] = sv(a.OwnerAccountId)
			return r, "", ""
		},
		tokenPageSteps{stored: func(rows []*store.Resource) {
			for _, r := range rows {
				switch owners[r.NativeID] {
				case acct.ID:
					owned = append(owned, childParent{id: wsResourceID(r.NativeID), arn: r.NativeID})
				case "":
					noOwner++
				}
			}
			clear(owners)
		}},
		wsOwnedElsewhereTags(tags, acct.ID, owners), p)
	if failed {
		return nil, true
	}
	if noOwner > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: wsDescribeAliasPermissionsOp, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d alias(es) with no OwnerAccountId; permissions not read", noOwner),
		})
	}
	return owned, false
}

// scanWSWorkspacesPools stores the workspaces pools under the PoolArn each
// element carries. Pools is a per-region sub-feature, and closed to new
// customers since 7/30/2026. Both facts arrive as a canned
// AccessDeniedException on the first page — see isWorkSpacesPoolsGap for the
// two phrasings — and read as an empty listing; a real denial warns.
func scanWSWorkspacesPools(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "workspaces:DescribeWorkspacesPools",
		firstPageGapPages(isWorkSpacesPoolsGap, func(ctx context.Context, token *string) ([]wstypes.WorkspacesPool, *string, error) {
			out, err := client.DescribeWorkspacesPools(ctx, &workspaces.DescribeWorkspacesPoolsInput{Limit: sdkaws.Int32(wsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.WorkspacesPools, out.NextToken, nil
		}),
		func(pool wstypes.WorkspacesPool) (*store.Resource, string, string) {
			return arnRow("workspaces pools", pool.PoolArn, pool.PoolName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWorkSpacesWorkspacesPool, NativeID: sv(pool.PoolArn),
				Name: pool.PoolName, Region: &region,
				AttributesJSON: mustJSON(pool), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanWSDirectories also returns the stored directories for the client
// add-in phase.
func scanWSDirectories(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "workspaces:DescribeWorkspaceDirectories", "directories", "DirectoryId",
		func(ctx context.Context, token *string) ([]wstypes.WorkspaceDirectory, *string, error) {
			out, err := client.DescribeWorkspaceDirectories(ctx, &workspaces.DescribeWorkspaceDirectoriesInput{Limit: sdkaws.Int32(wsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Directories, out.NextToken, nil
		},
		func(d wstypes.WorkspaceDirectory) (*store.Resource, string, string) {
			id := sv(d.DirectoryId)
			if id == "" {
				return wsNoID("directories", "DirectoryId", sv(d.DirectoryName))
			}
			status := string(d.State)
			return wsRow(acct, region, scanID, TypeWorkSpacesDirectory, "directory", id, sv(d.DirectoryName), &status, d), "", ""
		},
		func(d wstypes.WorkspaceDirectory) string { return sv(d.DirectoryId) }, tags, p)
}

// scanWSBundles lists the account's own WorkSpace bundles. The request sets no
// Owner, which returns only the account's own bundles (Owner=AMAZON would list
// the public catalogue); any AMAZON-owned bundle that surfaces is flagged
// ManagedByProvider and has no tags read.
func scanWSBundles(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "workspaces:DescribeWorkspaceBundles",
		func(ctx context.Context, token *string) ([]wstypes.WorkspaceBundle, *string, error) {
			out, err := client.DescribeWorkspaceBundles(ctx, &workspaces.DescribeWorkspaceBundlesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Bundles, out.NextToken, nil
		},
		func(b wstypes.WorkspaceBundle) (*store.Resource, string, string) {
			id := sv(b.BundleId)
			if id == "" {
				return wsNoID("bundles", "BundleId", sv(b.Name))
			}
			r := wsRow(acct, region, scanID, TypeWorkSpacesWorkspaceBundle, "workspacebundle", id, sv(b.Name), nil, b)
			r.ManagedByProvider = sv(b.Owner) == "AMAZON"
			return r, "", ""
		}, tags, p)
}

// scanWSImages stores the images DescribeWorkspaceImages lists, asked for no
// ImageType. Whether that includes images other accounts share with this one
// is unverified; an image whose OwnerAccountId names another account has its
// tags read under otherAccountGap (wsOwnedElsewhereTags).
func scanWSImages(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	owners := map[string]string{}
	return storeTokenPagesTagsWith(ctx, st, acct, region, "workspaces:DescribeWorkspaceImages",
		func(ctx context.Context, token *string) ([]wstypes.WorkspaceImage, *string, error) {
			out, err := client.DescribeWorkspaceImages(ctx, &workspaces.DescribeWorkspaceImagesInput{MaxResults: sdkaws.Int32(wsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Images, out.NextToken, nil
		},
		func(im wstypes.WorkspaceImage) (*store.Resource, string, string) {
			id := sv(im.ImageId)
			if id == "" {
				return wsNoID("images", "ImageId", sv(im.Name))
			}
			status := string(im.State)
			r := wsRow(acct, region, scanID, TypeWorkSpacesWorkspaceImage, "workspaceimage", id, sv(im.Name), &status, im)
			owners[r.NativeID] = sv(im.OwnerAccountId)
			return r, "", ""
		},
		tokenPageSteps{stored: func([]*store.Resource) { clear(owners) }},
		wsOwnedElsewhereTags(tags, acct.ID, owners), p)
}

func scanWSIpGroups(ctx context.Context, client workSpacesAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "workspaces:DescribeIpGroups",
		func(ctx context.Context, token *string) ([]wstypes.WorkspacesIpGroup, *string, error) {
			out, err := client.DescribeIpGroups(ctx, &workspaces.DescribeIpGroupsInput{MaxResults: sdkaws.Int32(wsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Result, out.NextToken, nil
		},
		func(g wstypes.WorkspacesIpGroup) (*store.Resource, string, string) {
			id := sv(g.GroupId)
			if id == "" {
				return wsNoID("IP groups", "GroupId", sv(g.GroupName))
			}
			return wsRow(acct, region, scanID, TypeWorkSpacesWorkspaceIPGroup, "workspaceipgroup", id, sv(g.GroupName), nil, g), "", ""
		}, tags, p)
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
			arn := arnFor(acct.partition(), "workspaces", region, acct.ID, "workspaceapplication/"+id)
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
// arn:{p}:workspaces:{region}:{account}:accountlink/{AccountLinkId}, shaped
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
				Type: TypeWorkSpacesAccountLink, NativeID: wsAccountLinkNativeID(acct.partition(), region, acct.ID, id),
				Name: &id, Region: &region, Status: nonEmptyPtr(string(l.AccountLinkStatus)),
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "workspaces account-links")
}

func wsAccountLinkNativeID(partition, region, acctID, linkID string) string {
	return arnFor(partition, "workspaces", region, acctID, "accountlink/"+linkID)
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
