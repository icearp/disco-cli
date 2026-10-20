package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/quicksight"
	qstypes "github.com/aws/aws-sdk-go-v2/service/quicksight/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeQuickSightAccount, Service: "quicksight", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeQuickSightActionConnector, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightAgent, Service: "quicksight", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeQuickSightAnalysis, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightApp, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightApprovalPolicy, Service: "quicksight", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeQuickSightAssignment, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightBrand, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightCustomization, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightCustomPermissions, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightDashboard, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightDataSet, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightDataSource, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightDLPSetting, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightFlow, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightFolder, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightFolderMember, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightGroup, Service: "quicksight", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeQuickSightGroupMembership, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightKeyRegistration, Service: "quicksight", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeQuickSightKnowledgeBase, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightLimitsProfile, Service: "quicksight", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeQuickSightNamespace, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightOAuthClientApplication, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightRefreshSchedule, Service: "quicksight", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeQuickSightSpace, Service: "quicksight", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeQuickSightTemplate, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightTemplateAlias, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightTheme, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightThemeAlias, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightTopic, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightTopicRefreshSchedule, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightUser, Service: "quicksight"})
	registerType(restype.Descriptor{Type: TypeQuickSightVPCConnection, Service: "quicksight"})
	registerService(serviceEntry{
		name: "aws:quicksight",
		fn:   scanQuickSight,
	})
}

type quickSightAPI interface {
	ListActionConnectors(context.Context, *quicksight.ListActionConnectorsInput, ...func(*quicksight.Options)) (*quicksight.ListActionConnectorsOutput, error)
	ListAgents(context.Context, *quicksight.ListAgentsInput, ...func(*quicksight.Options)) (*quicksight.ListAgentsOutput, error)
	ListAnalyses(context.Context, *quicksight.ListAnalysesInput, ...func(*quicksight.Options)) (*quicksight.ListAnalysesOutput, error)
	ListBrands(context.Context, *quicksight.ListBrandsInput, ...func(*quicksight.Options)) (*quicksight.ListBrandsOutput, error)
	ListCustomPermissions(context.Context, *quicksight.ListCustomPermissionsInput, ...func(*quicksight.Options)) (*quicksight.ListCustomPermissionsOutput, error)
	ListDashboards(context.Context, *quicksight.ListDashboardsInput, ...func(*quicksight.Options)) (*quicksight.ListDashboardsOutput, error)
	ListDataSets(context.Context, *quicksight.ListDataSetsInput, ...func(*quicksight.Options)) (*quicksight.ListDataSetsOutput, error)
	ListDataSources(context.Context, *quicksight.ListDataSourcesInput, ...func(*quicksight.Options)) (*quicksight.ListDataSourcesOutput, error)
	ListFlows(context.Context, *quicksight.ListFlowsInput, ...func(*quicksight.Options)) (*quicksight.ListFlowsOutput, error)
	ListFolderMembers(context.Context, *quicksight.ListFolderMembersInput, ...func(*quicksight.Options)) (*quicksight.ListFolderMembersOutput, error)
	ListFolders(context.Context, *quicksight.ListFoldersInput, ...func(*quicksight.Options)) (*quicksight.ListFoldersOutput, error)
	ListGroupMemberships(context.Context, *quicksight.ListGroupMembershipsInput, ...func(*quicksight.Options)) (*quicksight.ListGroupMembershipsOutput, error)
	ListGroups(context.Context, *quicksight.ListGroupsInput, ...func(*quicksight.Options)) (*quicksight.ListGroupsOutput, error)
	ListIAMPolicyAssignments(context.Context, *quicksight.ListIAMPolicyAssignmentsInput, ...func(*quicksight.Options)) (*quicksight.ListIAMPolicyAssignmentsOutput, error)
	ListKnowledgeBases(context.Context, *quicksight.ListKnowledgeBasesInput, ...func(*quicksight.Options)) (*quicksight.ListKnowledgeBasesOutput, error)
	ListNamespaces(context.Context, *quicksight.ListNamespacesInput, ...func(*quicksight.Options)) (*quicksight.ListNamespacesOutput, error)
	ListOAuthClientApplications(context.Context, *quicksight.ListOAuthClientApplicationsInput, ...func(*quicksight.Options)) (*quicksight.ListOAuthClientApplicationsOutput, error)
	ListRefreshSchedules(context.Context, *quicksight.ListRefreshSchedulesInput, ...func(*quicksight.Options)) (*quicksight.ListRefreshSchedulesOutput, error)
	ListSpaces(context.Context, *quicksight.ListSpacesInput, ...func(*quicksight.Options)) (*quicksight.ListSpacesOutput, error)
	ListTemplates(context.Context, *quicksight.ListTemplatesInput, ...func(*quicksight.Options)) (*quicksight.ListTemplatesOutput, error)
	ListThemes(context.Context, *quicksight.ListThemesInput, ...func(*quicksight.Options)) (*quicksight.ListThemesOutput, error)
	ListTopics(context.Context, *quicksight.ListTopicsInput, ...func(*quicksight.Options)) (*quicksight.ListTopicsOutput, error)
	ListUsers(context.Context, *quicksight.ListUsersInput, ...func(*quicksight.Options)) (*quicksight.ListUsersOutput, error)
	ListVPCConnections(context.Context, *quicksight.ListVPCConnectionsInput, ...func(*quicksight.Options)) (*quicksight.ListVPCConnectionsOutput, error)
	DescribeAccountSettings(context.Context, *quicksight.DescribeAccountSettingsInput, ...func(*quicksight.Options)) (*quicksight.DescribeAccountSettingsOutput, error)
	DescribeAccountCustomization(context.Context, *quicksight.DescribeAccountCustomizationInput, ...func(*quicksight.Options)) (*quicksight.DescribeAccountCustomizationOutput, error)
	DescribeKeyRegistration(context.Context, *quicksight.DescribeKeyRegistrationInput, ...func(*quicksight.Options)) (*quicksight.DescribeKeyRegistrationOutput, error)
	ListTemplateAliases(context.Context, *quicksight.ListTemplateAliasesInput, ...func(*quicksight.Options)) (*quicksight.ListTemplateAliasesOutput, error)
	ListThemeAliases(context.Context, *quicksight.ListThemeAliasesInput, ...func(*quicksight.Options)) (*quicksight.ListThemeAliasesOutput, error)
	ListTopicRefreshSchedules(context.Context, *quicksight.ListTopicRefreshSchedulesInput, ...func(*quicksight.Options)) (*quicksight.ListTopicRefreshSchedulesOutput, error)
	ListApps(context.Context, *quicksight.ListAppsInput, ...func(*quicksight.Options)) (*quicksight.ListAppsOutput, error)
	ListApprovalPolicies(context.Context, *quicksight.ListApprovalPoliciesInput, ...func(*quicksight.Options)) (*quicksight.ListApprovalPoliciesOutput, error)
	ListDlpSettings(context.Context, *quicksight.ListDlpSettingsInput, ...func(*quicksight.Options)) (*quicksight.ListDlpSettingsOutput, error)
	ListLimitsProfiles(context.Context, *quicksight.ListLimitsProfilesInput, ...func(*quicksight.Options)) (*quicksight.ListLimitsProfilesOutput, error)
}

// qsSoftSkip reports whether err is a QuickSight soft-skip: AccessDenied,
// UnsupportedUserEditionException (not subscribed / edition lacks feature),
// ResourceNotFoundException (region/feature unsupported), PreconditionNotMetException
// (no subscription at all, e.g. ListAgents), or UnauthorizedException (401, edition-gated
// newer ops, e.g. ListFlows). Scoped to QuickSight — a real credential failure still
// surfaces on every other service.
func qsSoftSkip(err error) bool {
	// Newer QuickSight (Q) ops like ListAgents 404 with an HTML body where the
	// feature isn't deployed — SDK can't map it to a typed code (deserialization
	// fails on '<'), so fall back to the HTTP status.
	if isHTTP404(err) {
		return true
	}
	return isAccessDenied(err) || isAPIErrorCode(err, "UnsupportedUserEditionException", "ResourceNotFoundException", "QuickSightUserNotFoundException", "InvalidParameterValueException", "PreconditionNotMetException", "UnauthorizedException")
}

// scanQuickSight runs the app, approval-policy, DLP-setting, limits-profile,
// folder-member and group-membership phases after every older phase, so a
// failure on either side costs the other no rows; the member phases list the
// children of the folders and groups the older phases stored before any
// failure. No side's demotable transient error reaches the join: one transient member
// would make the dispatcher read the whole join as transient and drop every
// phase's counts. The errors are joined and the counts summed.
func scanQuickSight(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := quicksight.NewFromConfig(acct.cfg, func(o *quicksight.Options) { o.Region = region })

	parents, total, inserted, err := scanQSOlderPhases(ctx, client, acct, region, st, scanID)
	errs := []error{demoteTransient(st, "quicksight", acct, region, err)}
	for _, phase := range []struct {
		op  string
		run func() (int, int, error)
	}{
		{qsListAppsOp, func() (int, int, error) { return scanQSApps(ctx, client, acct, region, st, scanID) }},
		{qsListApprovalPoliciesOp, func() (int, int, error) { return scanQSApprovalPolicies(ctx, client, acct, region, st, scanID) }},
		{qsListDlpSettingsOp, func() (int, int, error) { return scanQSDLPSettings(ctx, client, acct, region, st, scanID) }},
		{qsListLimitsProfilesOp, func() (int, int, error) { return scanQSLimitsProfiles(ctx, client, acct, region, st, scanID) }},
	} {
		t, i, perr := phase.run()
		total += t
		inserted += i
		errs = append(errs, demoteTransient(st, phase.op, acct, region, perr))
	}
	// The member phases skip a parent whose call is still transient after the
	// SDK's retries (childFanOutSkipTransient), so no transient error leaves
	// them for demoteTransient to catch.
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanQSFolderMembers(ctx, client, acct, region, st, scanID, parents.folders)
		},
		func() (int, int, error) {
			return scanQSGroupMemberships(ctx, client, acct, region, st, scanID, parents.groups)
		},
	} {
		t, i, perr := phase()
		total += t
		inserted += i
		errs = append(errs, perr)
	}
	return total, inserted, errors.Join(errs...)
}

// qsParents are the folders and groups the older phases stored, handed to the
// member phases that run after every older phase.
type qsParents struct {
	folders []childParent
	groups  []qsGroupRef
}

// scanQSOlderPhases runs the phases that predate the app, approval-policy,
// DLP-setting, limits-profile and member phases, stopping at the first error
// any of them returns, transient ones included. It returns the folders and
// groups stored before it stopped.
func scanQSOlderPhases(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (parents qsParents, total, inserted int, err error) {
	dsIDs, t, i, ferr := scanQSDataSets(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return parents, 0, 0, ferr
	}
	total += t
	inserted += i

	nsARNs, t, i, ferr := scanQSNamespaces(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return parents, total, inserted, ferr
	}
	total += t
	inserted += i

	var templates, themes, topics []childParent
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanQSAnalyses(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanQSDashboards(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanQSDataSources(ctx, client, acct, region, st, scanID) },
		func() (t, i int, err error) {
			parents.folders, t, i, err = scanQSFolders(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (t, i int, err error) {
			templates, t, i, err = scanQSTemplates(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (t, i int, err error) {
			themes, t, i, err = scanQSThemes(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (t, i int, err error) {
			topics, t, i, err = scanQSTopics(ctx, client, acct, region, st, scanID)
			return t, i, err
		},
		func() (int, int, error) { return scanQSVPCConnections(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanQSCustomPermissions(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanQSActionConnectors(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanQSRefreshSchedules(ctx, client, acct, region, st, scanID, dsIDs) },
		func() (int, int, error) { return scanQSAgents(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanQSBrands(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanQSFlows(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanQSKnowledgeBases(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanQSOAuthClientApplications(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanQSSpaces(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanQSAccountSettings(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanQSAccountCustomization(ctx, client, acct, region, st, scanID) },
		func() (t, i int, err error) {
			parents.groups, t, i, err = scanQSGroups(ctx, client, acct, region, st, scanID, nsARNs)
			return t, i, err
		},
		func() (int, int, error) { return scanQSUsers(ctx, client, acct, region, st, scanID, nsARNs) },
		func() (int, int, error) { return scanQSAssignments(ctx, client, acct, region, st, scanID, nsARNs) },
		func() (int, int, error) {
			return scanQSTemplateAliases(ctx, client, acct, region, st, scanID, templates)
		},
		func() (int, int, error) { return scanQSThemeAliases(ctx, client, acct, region, st, scanID, themes) },
		func() (int, int, error) {
			return scanQSTopicRefreshSchedules(ctx, client, acct, region, st, scanID, topics)
		},
		func() (int, int, error) { return scanQSKeyRegistration(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return parents, total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return parents, total, inserted, nil
}

func scanQSAnalyses(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	pager := quicksight.NewListAnalysesPaginator(client, &quicksight.ListAnalysesInput{AwsAccountId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("quicksight:ListAnalyses: %w", perr)
		}
		for _, a := range out.AnalysisSummaryList {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			label := sv(a.Name)
			if label == "" {
				label = sv(a.AnalysisId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightAnalysis, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "quicksight analyses")
}

func scanQSDashboards(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	pager := quicksight.NewListDashboardsPaginator(client, &quicksight.ListDashboardsInput{AwsAccountId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("quicksight:ListDashboards: %w", perr)
		}
		for _, d := range out.DashboardSummaryList {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			label := sv(d.Name)
			if label == "" {
				label = sv(d.DashboardId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightDashboard, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "quicksight dashboards")
}

func scanQSDataSets(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	id := acct.ID
	pager := quicksight.NewListDataSetsPaginator(client, &quicksight.ListDataSetsInput{AwsAccountId: &id})
	var ids []string
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("quicksight:ListDataSets: %w", perr)
		}
		for _, d := range out.DataSetSummaries {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			dsid := sv(d.DataSetId)
			if dsid != "" {
				ids = append(ids, dsid)
			}
			label := sv(d.Name)
			if label == "" {
				label = dsid
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightDataSet, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "quicksight data-sets")
	return ids, t, i, err
}

func scanQSDataSources(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	pager := quicksight.NewListDataSourcesPaginator(client, &quicksight.ListDataSourcesInput{AwsAccountId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("quicksight:ListDataSources: %w", perr)
		}
		for _, d := range out.DataSources {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			label := sv(d.Name)
			if label == "" {
				label = sv(d.DataSourceId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightDataSource, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "quicksight data-sources")
}

// scanQSFolders stores the account's folders and returns the stored ones that
// carry a folder id, as the parents of the folder-member phase; the folders
// stored without one are reported in one warning.
func scanQSFolders(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	id := acct.ID
	pager := quicksight.NewListFoldersPaginator(client, &quicksight.ListFoldersInput{AwsAccountId: &id})
	var parents []childParent
	var unlisted skipTally
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("quicksight:ListFolders: %w", perr)
		}
		for _, f := range out.FolderSummaryList {
			arn := sv(f.Arn)
			if arn == "" {
				continue
			}
			if fid := sv(f.FolderId); fid != "" {
				parents = append(parents, childParent{id: fid, arn: arn})
			} else {
				unlisted.skipped(arn, nil)
			}
			label := sv(f.Name)
			if label == "" {
				label = sv(f.FolderId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightFolder, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "quicksight folders")
	if err != nil {
		return nil, t, i, err
	}
	warnQSParentsUnlisted(st, acct, region, qsListFolderMembersOp, unlisted.count, "folder(s)", "FolderId", unlisted.first)
	return parents, t, i, nil
}

func scanQSTemplates(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	id := acct.ID
	pager := quicksight.NewListTemplatesPaginator(client, &quicksight.ListTemplatesInput{AwsAccountId: &id})
	var parents []childParent
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("quicksight:ListTemplates: %w", perr)
		}
		for _, t := range out.TemplateSummaryList {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			if tid := sv(t.TemplateId); tid != "" {
				parents = append(parents, childParent{id: tid, arn: arn})
			}
			label := sv(t.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightTemplate, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "quicksight templates")
	return parents, t, i, err
}

func scanQSThemes(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	id := acct.ID
	pager := quicksight.NewListThemesPaginator(client, &quicksight.ListThemesInput{AwsAccountId: &id})
	var parents []childParent
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("quicksight:ListThemes: %w", perr)
		}
		for _, th := range out.ThemeSummaryList {
			arn := sv(th.Arn)
			if arn == "" {
				continue
			}
			// ListThemes also returns QuickSight's starter themes (owner account
			// "aws"); aliases exist only on the account's own themes.
			owner, aerr := awsarn.Parse(arn)
			if tid := sv(th.ThemeId); tid != "" && aerr == nil && owner.AccountID == acct.ID {
				parents = append(parents, childParent{id: tid, arn: arn})
			}
			label := sv(th.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightTheme, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(th), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "quicksight themes")
	return parents, t, i, err
}

func scanQSTopics(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	id := acct.ID
	pager := quicksight.NewListTopicsPaginator(client, &quicksight.ListTopicsInput{AwsAccountId: &id})
	var parents []childParent
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("quicksight:ListTopics: %w", perr)
		}
		for _, t := range out.TopicsSummaries {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			tid := sv(t.TopicId)
			if tid != "" {
				parents = append(parents, childParent{id: tid, arn: arn})
			}
			label := sv(t.Name)
			if label == "" {
				label = tid
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightTopic, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "quicksight topics")
	return parents, t, i, err
}

func scanQSVPCConnections(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	pager := quicksight.NewListVPCConnectionsPaginator(client, &quicksight.ListVPCConnectionsInput{AwsAccountId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("quicksight:ListVPCConnections: %w", perr)
		}
		for _, v := range out.VPCConnectionSummaries {
			arn := sv(v.Arn)
			if arn == "" {
				continue
			}
			label := sv(v.Name)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightVPCConnection, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "quicksight vpc-connections")
}

func scanQSCustomPermissions(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	pager := quicksight.NewListCustomPermissionsPaginator(client, &quicksight.ListCustomPermissionsInput{AwsAccountId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("quicksight:ListCustomPermissions: %w", perr)
		}
		for _, c := range out.CustomPermissionsList {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.CustomPermissionsName)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightCustomPermissions, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "quicksight custom-permissions")
}

func scanQSActionConnectors(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	// SDK paginator nils MaxResults when Limit==0; AWS rejects with
	// ValidationException. Force a valid page size (max 100).
	pager := quicksight.NewListActionConnectorsPaginator(client, &quicksight.ListActionConnectorsInput{AwsAccountId: &id}, func(o *quicksight.ListActionConnectorsPaginatorOptions) {
		o.Limit = 100
	})
	var batch []*store.Resource
	for pager.HasMorePages() {
		// ListActionConnectors raises 500 InternalFailure where the (newish)
		// feature isn't deployed. The global 10-attempt adaptive retryer eats
		// ~2m before giving up. Clamp this op's retry budget so soft-skip
		// triggers fast.
		out, perr := pager.NextPage(ctx, func(o *quicksight.Options) { o.RetryMaxAttempts = 2 })
		if perr != nil {
			if qsSoftSkip(perr) || isAPIErrorCode(perr, "InternalFailure") {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("quicksight:ListActionConnectors: %w", perr)
		}
		for _, a := range out.ActionConnectorSummaries {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			label := sv(a.Name)
			if label == "" {
				label = sv(a.ActionConnectorId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightActionConnector, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "quicksight action-connectors")
}

func scanQSRefreshSchedules(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, dsIDs []string) (int, int, error) {
	if len(dsIDs) == 0 {
		return 0, 0, nil
	}
	id := acct.ID
	var batch []*store.Resource
	for _, ds := range dsIDs {
		dsid := ds
		out, err := client.ListRefreshSchedules(ctx, &quicksight.ListRefreshSchedulesInput{AwsAccountId: &id, DataSetId: &dsid})
		if err != nil {
			if qsSoftSkip(err) {
				continue
			}
			return 0, 0, fmt.Errorf("quicksight:ListRefreshSchedules %s: %w", ds, err)
		}
		for _, s := range out.RefreshSchedules {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			label := sv(s.ScheduleId)
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightRefreshSchedule, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "quicksight refresh-schedules")
}

// nsRef pairs a QuickSight namespace's input name with its ARN — the per-namespace
// fan-out ops (groups/users/assignments) key on the name, while assignments (which
// carry no AWS-issued ARN) synthesize a NativeID off the namespace ARN.
type nsRef struct{ name, arn string }

func scanQSNamespaces(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) ([]nsRef, int, int, error) {
	id := acct.ID
	pager := quicksight.NewListNamespacesPaginator(client, &quicksight.ListNamespacesInput{AwsAccountId: &id})
	var refs []nsRef
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("quicksight:ListNamespaces: %w", perr)
		}
		for _, n := range out.Namespaces {
			arn := sv(n.Arn)
			if arn == "" {
				continue
			}
			name := sv(n.Name)
			if name != "" {
				refs = append(refs, nsRef{name: name, arn: arn})
			}
			label := name
			if label == "" {
				label = arn
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightNamespace, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "quicksight namespaces")
	return refs, t, i, err
}

func scanQSAgents(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.ListAgents(ctx, &quicksight.ListAgentsInput{AwsAccountId: &id, NextToken: token})
		if err != nil {
			if qsSoftSkip(err) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("quicksight:ListAgents: %w", err)
		}
		for _, a := range out.AgentSummaries {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			label := sv(a.Name)
			if label == "" {
				label = sv(a.AgentId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightAgent, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if token = out.NextToken; token == nil {
			break
		}
	}
	return upsertBatch(st, batch, "quicksight agents")
}

func scanQSBrands(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	pager := quicksight.NewListBrandsPaginator(client, &quicksight.ListBrandsInput{AwsAccountId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("quicksight:ListBrands: %w", perr)
		}
		for _, b := range out.Brands {
			arn := sv(b.Arn)
			if arn == "" {
				continue
			}
			label := sv(b.BrandName)
			if label == "" {
				label = sv(b.BrandId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightBrand, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "quicksight brands")
}

func scanQSFlows(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	pager := quicksight.NewListFlowsPaginator(client, &quicksight.ListFlowsInput{AwsAccountId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("quicksight:ListFlows: %w", perr)
		}
		for _, f := range out.FlowSummaryList {
			arn := sv(f.Arn)
			if arn == "" {
				continue
			}
			label := sv(f.Name)
			if label == "" {
				label = sv(f.FlowId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightFlow, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "quicksight flows")
}

func scanQSKnowledgeBases(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	pager := quicksight.NewListKnowledgeBasesPaginator(client, &quicksight.ListKnowledgeBasesInput{AwsAccountId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("quicksight:ListKnowledgeBases: %w", perr)
		}
		for _, k := range out.KnowledgeBaseSummaries {
			arn := sv(k.KnowledgeBaseArn)
			if arn == "" {
				continue
			}
			label := sv(k.Name)
			if label == "" {
				label = sv(k.KnowledgeBaseId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightKnowledgeBase, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(k), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "quicksight knowledge-bases")
}

func scanQSOAuthClientApplications(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	pager := quicksight.NewListOAuthClientApplicationsPaginator(client, &quicksight.ListOAuthClientApplicationsInput{AwsAccountId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("quicksight:ListOAuthClientApplications: %w", perr)
		}
		for _, o := range out.OAuthClientApplications {
			arn := sv(o.Arn)
			if arn == "" {
				continue
			}
			label := sv(o.Name)
			if label == "" {
				label = sv(o.OAuthClientApplicationId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightOAuthClientApplication, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(o), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "quicksight oauth-client-applications")
}

func scanQSSpaces(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.ListSpaces(ctx, &quicksight.ListSpacesInput{AwsAccountId: &id, NextToken: token})
		if err != nil {
			if qsSoftSkip(err) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("quicksight:ListSpaces: %w", err)
		}
		for _, s := range out.SpaceSummaries {
			arn := sv(s.SpaceArn)
			if arn == "" {
				continue
			}
			label := sv(s.Name)
			if label == "" {
				label = sv(s.SpaceId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightSpace, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if token = out.NextToken; token == nil {
			break
		}
	}
	return upsertBatch(st, batch, "quicksight spaces")
}

// scanQSAccountSettings upserts the per-(account, region) settings singleton.
// ResourceNotFound (the default un-subscribed state) and the other soft-skip
// codes return (0,0,nil) with no warning.
func scanQSAccountSettings(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	out, err := client.DescribeAccountSettings(ctx, &quicksight.DescribeAccountSettingsInput{AwsAccountId: &id})
	if err != nil {
		if qsSoftSkip(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("quicksight:DescribeAccountSettings: %w", err)
	}
	if out.AccountSettings == nil {
		return 0, 0, nil
	}
	arn := fmt.Sprintf("arn:aws:quicksight:%s:%s:account-settings", region, acct.ID)
	label := sv(out.AccountSettings.AccountName)
	if label == "" {
		label = arn
	}
	batch := []*store.Resource{{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeQuickSightAccount, NativeID: arn,
		Name: &label, Region: &region, AttributesJSON: mustJSON(out.AccountSettings),
		DiscoveredBy: scanID,
	}}
	return upsertBatch(st, batch, "quicksight account-settings")
}

// scanQSAccountCustomization upserts the per-(account, region) customization
// singleton. NotConfigured / ResourceNotFound is the default state → silent skip.
func scanQSAccountCustomization(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	out, err := client.DescribeAccountCustomization(ctx, &quicksight.DescribeAccountCustomizationInput{AwsAccountId: &id})
	if err != nil {
		if qsSoftSkip(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("quicksight:DescribeAccountCustomization: %w", err)
	}
	if out.AccountCustomization == nil {
		return 0, 0, nil
	}
	arn := sv(out.Arn)
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:quicksight:%s:%s:account-customization", region, acct.ID)
	}
	label := arn
	batch := []*store.Resource{{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeQuickSightCustomization, NativeID: arn,
		Name: &label, Region: &region, AttributesJSON: mustJSON(out.AccountCustomization),
		DiscoveredBy: scanID,
	}}
	return upsertBatch(st, batch, "quicksight account-customization")
}

// qsGroupRef is a stored group with the namespace it was listed in: the Group
// element names no namespace, and ListGroupMemberships needs it.
type qsGroupRef struct{ namespace, name, arn string }

// scanQSGroups stores each namespace's groups and returns the stored ones that
// carry a group name, as the parents of the group-membership phase; the groups
// stored without one are reported in one warning.
func scanQSGroups(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, namespaces []nsRef) ([]qsGroupRef, int, int, error) {
	id := acct.ID
	var groups []qsGroupRef
	var unlisted skipTally
	var batch []*store.Resource
	for _, ns := range namespaces {
		name := ns.name
		pager := quicksight.NewListGroupsPaginator(client, &quicksight.ListGroupsInput{AwsAccountId: &id, Namespace: &name})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if qsSoftSkip(perr) {
					break
				}
				return nil, 0, 0, fmt.Errorf("quicksight:ListGroups %s: %w", name, perr)
			}
			for _, g := range out.GroupList {
				arn := sv(g.Arn)
				if arn == "" {
					continue
				}
				if gn := sv(g.GroupName); gn != "" {
					groups = append(groups, qsGroupRef{namespace: name, name: gn, arn: arn})
				} else {
					unlisted.skipped(arn, nil)
				}
				label := sv(g.GroupName)
				if label == "" {
					label = arn
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeQuickSightGroup, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
				})
			}
		}
	}
	t, i, err := upsertBatch(st, batch, "quicksight groups")
	if err != nil {
		return nil, t, i, err
	}
	warnQSParentsUnlisted(st, acct, region, qsListGroupMembershipsOp, unlisted.count, "group(s)", "GroupName", unlisted.first)
	return groups, t, i, nil
}

func scanQSUsers(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, namespaces []nsRef) (int, int, error) {
	id := acct.ID
	var batch []*store.Resource
	for _, ns := range namespaces {
		name := ns.name
		pager := quicksight.NewListUsersPaginator(client, &quicksight.ListUsersInput{AwsAccountId: &id, Namespace: &name})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if qsSoftSkip(perr) {
					break
				}
				return 0, 0, fmt.Errorf("quicksight:ListUsers %s: %w", name, perr)
			}
			for _, u := range out.UserList {
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
					Type: TypeQuickSightUser, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(u), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "quicksight users")
}

func scanQSAssignments(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, namespaces []nsRef) (int, int, error) {
	id := acct.ID
	var batch []*store.Resource
	for _, ns := range namespaces {
		name := ns.name
		pager := quicksight.NewListIAMPolicyAssignmentsPaginator(client, &quicksight.ListIAMPolicyAssignmentsInput{AwsAccountId: &id, Namespace: &name})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if qsSoftSkip(perr) {
					break
				}
				return 0, 0, fmt.Errorf("quicksight:ListIAMPolicyAssignments %s: %w", name, perr)
			}
			for _, a := range out.IAMPolicyAssignments {
				an := sv(a.AssignmentName)
				if an == "" {
					continue
				}
				// Assignments carry no AWS-issued ARN — synthesize off the
				// namespace ARN so the resolver can strip back to the parent.
				nativeID := fmt.Sprintf("%s/assignment/%s", ns.arn, an)
				label := an
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeQuickSightAssignment, NativeID: nativeID,
					Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "quicksight assignments")
}

// qsKeyRegistrationAttrs is DescribeKeyRegistrationOutput minus its response
// envelope (RequestId, Status, ResultMetadata): RequestId differs on every
// call and would version-split the row on every scan. Field names are the SDK's.
type qsKeyRegistrationAttrs struct {
	AwsAccountID    *string `json:"AwsAccountId"`
	KeyRegistration []qstypes.RegisteredCustomerManagedKey
	QDataKey        *qstypes.QDataKey
}

// scanQSKeyRegistration upserts the per-(account, region) customer-managed-key
// registration. It soft-skips through qsSoftSkip like the other account
// singletons, because QuickSight answers unsubscribed or Standard-edition
// accounts with codes outside this op's Smithy model. A row is stored only for
// a customer registration: a registered key, or a CMK as the Q data key (the
// AWS_OWNED default is QuickSight's, not the customer's).
func scanQSKeyRegistration(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	out, err := client.DescribeKeyRegistration(ctx, &quicksight.DescribeKeyRegistrationInput{AwsAccountId: &id})
	if err != nil {
		if qsSoftSkip(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("quicksight:DescribeKeyRegistration: %w", err)
	}
	cmkDataKey := out.QDataKey != nil && out.QDataKey.QDataKeyType == qstypes.QDataKeyTypeCmk
	if len(out.KeyRegistration) == 0 && !cmkDataKey {
		return 0, 0, nil
	}
	arn := fmt.Sprintf("arn:aws:quicksight:%s:%s:key-registration", region, acct.ID)
	label := arn
	batch := []*store.Resource{{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeQuickSightKeyRegistration, NativeID: arn,
		Name: &label, Region: &region, DiscoveredBy: scanID,
		AttributesJSON: mustJSON(qsKeyRegistrationAttrs{
			AwsAccountID: out.AwsAccountId, KeyRegistration: out.KeyRegistration, QDataKey: out.QDataKey,
		}),
	}}
	return upsertBatch(st, batch, "quicksight key-registration")
}

// qsChildSkip is qsSoftSkip minus AccessDenied, which childFanOut turns into a
// once-per-op warning instead: the parents exist, so the account is subscribed
// and a deny is a real IAM gap. Template and theme alias ops accept
// resource-level IAM, so a deny there may cover only some parents;
// ListTopicRefreshSchedules has no resource in the service reference, so a deny
// there is account-wide and every topic is skipped under the one warning.
func qsChildSkip(err error) bool {
	return !isAccessDenied(err) && qsSoftSkip(err)
}

func scanQSTemplateAliases(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, templates []childParent) (int, int, error) {
	id := acct.ID
	return childFanOut(ctx, st, acct, region, "quicksight:ListTemplateAliases", templates, qsChildSkip,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			pager := quicksight.NewListTemplateAliasesPaginator(client, &quicksight.ListTemplateAliasesInput{AwsAccountId: &id, TemplateId: &p.id})
			var rows []*store.Resource
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, a := range out.TemplateAliasList {
					arn := sv(a.Arn)
					if arn == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeQuickSightTemplateAlias, NativeID: arn,
						Name: a.AliasName, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

func scanQSThemeAliases(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, themes []childParent) (int, int, error) {
	id := acct.ID
	return childFanOut(ctx, st, acct, region, "quicksight:ListThemeAliases", themes, qsChildSkip,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			var token *string
			for {
				out, err := client.ListThemeAliases(ctx, &quicksight.ListThemeAliasesInput{AwsAccountId: &id, ThemeId: &p.id, NextToken: token})
				if err != nil {
					return nil, err
				}
				for _, a := range out.ThemeAliasList {
					arn := sv(a.Arn)
					if arn == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeQuickSightThemeAlias, NativeID: arn,
						Name: a.AliasName, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
					})
				}
				if token = out.NextToken; token == nil {
					return rows, nil
				}
			}
		})
}

// scanQSTopicRefreshSchedules lists each topic's per-dataset refresh schedules.
// A schedule carries no ARN and is addressed by (topic, dataset) — the
// Describe/Update/Delete ops take TopicId + DatasetId — so its NativeID is
// {topicARN}/refresh-schedule/{datasetId}.
func scanQSTopicRefreshSchedules(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, topics []childParent) (int, int, error) {
	id := acct.ID
	return childFanOut(ctx, st, acct, region, "quicksight:ListTopicRefreshSchedules", topics, qsChildSkip,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			out, err := client.ListTopicRefreshSchedules(ctx, &quicksight.ListTopicRefreshSchedulesInput{AwsAccountId: &id, TopicId: &p.id})
			if err != nil {
				return nil, err
			}
			var rows []*store.Resource
			for _, s := range out.RefreshSchedules {
				dsid := sv(s.DatasetId)
				if dsid == "" {
					continue
				}
				label := sv(s.DatasetName)
				if label == "" {
					label = dsid
				}
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeQuickSightTopicRefreshSchedule, NativeID: p.arn + "/refresh-schedule/" + dsid,
					Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
				})
			}
			return rows, nil
		})
}

const (
	qsListAppsOp             = "quicksight:ListApps"
	qsListApprovalPoliciesOp = "quicksight:ListApprovalPolicies"
	qsListDlpSettingsOp      = "quicksight:ListDlpSettings"
	qsListLimitsProfilesOp   = "quicksight:ListLimitsProfiles"
	qsListFolderMembersOp    = "quicksight:ListFolderMembers"
	qsListGroupMembershipsOp = "quicksight:ListGroupMemberships"
	// qsPageLimit is the documented MaxResults cap of ListApps,
	// ListApprovalPolicies, ListDlpSettings, ListLimitsProfiles,
	// ListFolderMembers and ListGroupMemberships.
	qsPageLimit = 100
)

// qsListPhase lists one account-level QuickSight collection page by page and
// stores the row toRow builds for each item (nil drops the item). QuickSight
// answers an unsubscribed account, an edition without the feature or a region
// without it with codes outside the op's model (qsSoftSkip), so such an error
// on the first page skips the phase silently, as does an error with one of
// featureOffCodes, the op's own modeled "feature not activated" codes. Once a
// page has succeeded the feature is there: a denial warns and any other error
// is returned, and either way the rows listed so far are stored.
func qsListPhase[Page, Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string, featureOffCodes []string,
	hasMore func() bool, next func(context.Context) (Page, error),
	items func(Page) []Item, toRow func(Item) *store.Resource,
) (int, int, error) {
	var batch []*store.Resource
	var listErr error
	listed := false
	for hasMore() {
		page, err := next(ctx)
		if err != nil {
			switch {
			case !listed && (qsSoftSkip(err) || isAPIErrorCode(err, featureOffCodes...)):
				return 0, 0, nil
			case isAccessDenied(err):
				_ = skipIfAccessDenied(st, op, acct.ID, region, err)
			default:
				listErr = fmt.Errorf("%s: %w", op, err)
			}
			break
		}
		listed = true
		for _, it := range items(page) {
			if r := toRow(it); r != nil {
				batch = append(batch, r)
			}
		}
	}
	t, i, uerr := upsertBatch(st, batch, op)
	return t, i, errors.Join(listErr, uerr)
}

// qsARNInOtherRegion reports whether arn names a region other than region. The
// governance lists (ListApprovalPolicies, ListLimitsProfiles) may be
// account-global, which is unverified; dropping another region's rows stores a
// global row once, in its own region, and changes nothing if the ops are
// regional. An ARN that does not parse, or names no region, is kept: it cannot
// be placed, and dropping it in every region would lose the resource, while
// keeping it costs at most a rewrite per scanned region.
func qsARNInOtherRegion(arn, region string) bool {
	parsed, err := awsarn.Parse(arn)
	return err == nil && parsed.Region != "" && parsed.Region != region
}

func scanQSApps(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	pager := quicksight.NewListAppsPaginator(client, &quicksight.ListAppsInput{AwsAccountId: &id}, func(o *quicksight.ListAppsPaginatorOptions) {
		o.Limit = qsPageLimit
		o.StopOnDuplicateToken = true
	})
	next := func(ctx context.Context) (*quicksight.ListAppsOutput, error) { return pager.NextPage(ctx) }
	return qsListPhase(ctx, st, acct, region, qsListAppsOp, nil, pager.HasMorePages, next,
		func(out *quicksight.ListAppsOutput) []qstypes.AppSummary { return out.AppSummaryList },
		func(a qstypes.AppSummary) *store.Resource {
			arn := sv(a.Arn)
			if arn == "" {
				return nil
			}
			label := cmp.Or(sv(a.Name), sv(a.AppId), arn)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightApp, NativeID: arn, Name: &label, Region: &region,
				CreatedAt: tp(a.CreatedTime), AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}
		})
}

// scanQSApprovalPolicies lists the calling account's approval policies;
// ListApprovalPolicies, unlike the other account-level lists, takes no account id.
func scanQSApprovalPolicies(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := quicksight.NewListApprovalPoliciesPaginator(client, &quicksight.ListApprovalPoliciesInput{}, func(o *quicksight.ListApprovalPoliciesPaginatorOptions) {
		o.Limit = qsPageLimit
		o.StopOnDuplicateToken = true
	})
	next := func(ctx context.Context) (*quicksight.ListApprovalPoliciesOutput, error) { return pager.NextPage(ctx) }
	return qsListPhase(ctx, st, acct, region, qsListApprovalPoliciesOp, nil, pager.HasMorePages, next,
		func(out *quicksight.ListApprovalPoliciesOutput) []qstypes.ApprovalPolicy { return out.Policies },
		func(p qstypes.ApprovalPolicy) *store.Resource {
			arn := sv(p.PolicyArn)
			if arn == "" || qsARNInOtherRegion(arn, region) {
				return nil
			}
			label := cmp.Or(sv(p.Name), sv(p.PolicyId), arn)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightApprovalPolicy, NativeID: arn, Name: &label, Region: &region,
				CreatedAt: tp(p.CreatedAt), AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			}
		})
}

func scanQSDLPSettings(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	pager := quicksight.NewListDlpSettingsPaginator(client, &quicksight.ListDlpSettingsInput{AwsAccountId: &id}, func(o *quicksight.ListDlpSettingsPaginatorOptions) {
		o.Limit = qsPageLimit
		o.StopOnDuplicateToken = true
	})
	next := func(ctx context.Context) (*quicksight.ListDlpSettingsOutput, error) { return pager.NextPage(ctx) }
	// ListDlpSettings models InvalidRequestException as "You don't have this
	// feature activated for your account".
	return qsListPhase(ctx, st, acct, region, qsListDlpSettingsOp, []string{"InvalidRequestException"}, pager.HasMorePages, next,
		func(out *quicksight.ListDlpSettingsOutput) []qstypes.DlpSettingSummary {
			return out.DlpSettingSummaries
		},
		func(d qstypes.DlpSettingSummary) *store.Resource {
			arn := sv(d.Arn)
			if arn == "" {
				return nil
			}
			label := cmp.Or(sv(d.Name), sv(d.DlpSettingId), arn)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightDLPSetting, NativeID: arn, Name: &label, Region: &region,
				Status: nonEmptyPtr(string(d.Status)), CreatedAt: tp(d.CreatedAt),
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			}
		})
}

func scanQSLimitsProfiles(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	pager := quicksight.NewListLimitsProfilesPaginator(client, &quicksight.ListLimitsProfilesInput{AccountId: &id}, func(o *quicksight.ListLimitsProfilesPaginatorOptions) {
		o.Limit = qsPageLimit
		o.StopOnDuplicateToken = true
	})
	next := func(ctx context.Context) (*quicksight.ListLimitsProfilesOutput, error) { return pager.NextPage(ctx) }
	return qsListPhase(ctx, st, acct, region, qsListLimitsProfilesOp, nil, pager.HasMorePages, next,
		func(out *quicksight.ListLimitsProfilesOutput) []qstypes.LimitsProfile { return out.Profiles },
		func(p qstypes.LimitsProfile) *store.Resource {
			arn := sv(p.Arn)
			if arn == "" || qsARNInOtherRegion(arn, region) {
				return nil
			}
			label := cmp.Or(sv(p.ProfileName), sv(p.ProfileId), arn)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightLimitsProfile, NativeID: arn, Name: &label, Region: &region,
				CreatedAt: tp(p.CreatedAt), AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			}
		})
}

// qsMemberNativeID keys a folder or group member row by the member's ARN under
// its parent: {parentARN}/member/{memberARN}. The member ARN goes last because
// it contains "/".
func qsMemberNativeID(parentARN, memberARN string) string {
	return parentARN + "/member/" + memberARN
}

// qsParentGone reports a parent deleted between its listing and its child call.
func qsParentGone(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }

// warnQSParentsUnlisted warns once that count stored parents (what, e.g.
// "folder(s)") lacked the key op needs, so their children were not listed.
func warnQSParentsUnlisted(st *store.Store, acct *account, region, op string, count int, what, key, first string) {
	if count == 0 {
		return
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws", Service: op, Scope: acct.ID + "/" + region,
		Message: fmt.Sprintf("%d %s stored without a %s, so their members were not listed, first %s", count, what, key, first),
	})
}

// scanQSFolderMembers stores one row per asset in each folder the folder phase
// stored. A member carries only an id and an ARN, and each asset kind's Create
// op takes its own id, so ids may repeat across kinds and the row is keyed by
// the member ARN. A folder deleted since it was listed is skipped, a denial
// warns once, and a folder still failing transiently after the SDK's retries
// is skipped under one warning; any other error, the op's edition and
// parameter codes included, is returned: the folder phase already succeeded,
// so the account has folders.
func scanQSFolderMembers(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, folders []childParent) (int, int, error) {
	id := acct.ID
	return childFanOutSkipTransient(ctx, st, acct, region, qsListFolderMembersOp, folders, qsParentGone, byParentARN,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			pager := quicksight.NewListFolderMembersPaginator(client, &quicksight.ListFolderMembersInput{AwsAccountId: &id, FolderId: &p.id},
				func(o *quicksight.ListFolderMembersPaginatorOptions) {
					o.Limit = qsPageLimit
					o.StopOnDuplicateToken = true
				})
			var rows []*store.Resource
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, m := range out.FolderMemberList {
					arn := sv(m.MemberArn)
					if arn == "" {
						continue
					}
					label := cmp.Or(sv(m.MemberId), arn)
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeQuickSightFolderMember, NativeID: qsMemberNativeID(p.arn, arn),
						Name: &label, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanQSGroupMemberships stores one row per member user of each group the group
// phase stored, keyed {groupARN}/member/{userARN}. A group deleted since it was
// listed is skipped, a denial warns once, and a group still failing
// transiently after the SDK's retries is skipped under one warning.
// PreconditionNotMetException and ResourceUnavailableException are modeled for
// the op, but what they mean on this call is unverified: each skips that group
// with one warning per code, so that one group QuickSight will not list cannot
// fail every scan. Any other error is returned.
func scanQSGroupMemberships(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, groups []qsGroupRef) (int, int, error) {
	const why = "QuickSight may refuse the members of a group it cannot list yet or does not manage (unverified)"
	// Keyed by ARN: errors and warnings then name the namespace, which tells
	// apart same-named groups in two namespaces.
	parents := make([]childParent, len(groups))
	refOf := make(map[string]qsGroupRef, len(groups))
	for i, g := range groups {
		parents[i] = childParent{id: g.arn, arn: g.arn}
		refOf[g.arn] = g
	}
	var precondition, unavailable skipTally
	total, inserted, err := childFanOutSkipTransient(ctx, st, acct, region, qsListGroupMembershipsOp, parents, qsParentGone, byParentARN,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			rows, err := listQSGroupMembers(ctx, client, acct, region, scanID, refOf[p.arn])
			if precondition.dropCode("PreconditionNotMetException", p.arn, err) ||
				unavailable.dropCode("ResourceUnavailableException", p.arn, err) {
				return nil, nil
			}
			return rows, err
		})
	precondition.warnCode(st, qsListGroupMembershipsOp, acct, region, "PreconditionNotMetException", "group(s)", why)
	unavailable.warnCode(st, qsListGroupMembershipsOp, acct, region, "ResourceUnavailableException", "group(s)", why)
	return total, inserted, err
}

// listQSGroupMembers returns the member rows of one group.
func listQSGroupMembers(ctx context.Context, client quickSightAPI, acct *account, region, scanID string, group qsGroupRef) ([]*store.Resource, error) {
	id := acct.ID
	pager := quicksight.NewListGroupMembershipsPaginator(client,
		&quicksight.ListGroupMembershipsInput{AwsAccountId: &id, Namespace: &group.namespace, GroupName: &group.name},
		func(o *quicksight.ListGroupMembershipsPaginatorOptions) {
			o.Limit = qsPageLimit
			o.StopOnDuplicateToken = true
		})
	var rows []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, m := range out.GroupMemberList {
			arn := sv(m.Arn)
			if arn == "" {
				continue
			}
			label := cmp.Or(sv(m.MemberName), arn)
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightGroupMembership, NativeID: qsMemberNativeID(group.arn, arn),
				Name: &label, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	return rows, nil
}
