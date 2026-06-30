package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/quicksight"
)

func init() {
	registerService(serviceEntry{
		name: "aws:quicksight",
		fn:   scanQuickSight,
		emits: []coverage.TypeDecl{
			{Service: "quicksight", DiscoType: TypeQuickSightAccount, Leaf: true},
			{Service: "quicksight", DiscoType: TypeQuickSightActionConnector, Leaf: true},
			{Service: "quicksight", DiscoType: TypeQuickSightAgent, Leaf: true},
			{Service: "quicksight", DiscoType: TypeQuickSightAnalysis, Leaf: true},
			{Service: "quicksight", DiscoType: TypeQuickSightAssignment},
			{Service: "quicksight", DiscoType: TypeQuickSightBrand, Leaf: true},
			{Service: "quicksight", DiscoType: TypeQuickSightCustomization, Leaf: true},
			{Service: "quicksight", DiscoType: TypeQuickSightCustomPermissions, Leaf: true},
			{Service: "quicksight", DiscoType: TypeQuickSightDashboard, Leaf: true},
			{Service: "quicksight", DiscoType: TypeQuickSightDataSet, Leaf: true},
			{Service: "quicksight", DiscoType: TypeQuickSightDataSource},
			{Service: "quicksight", DiscoType: TypeQuickSightFlow, Leaf: true},
			{Service: "quicksight", DiscoType: TypeQuickSightFolder, Leaf: true},
			{Service: "quicksight", DiscoType: TypeQuickSightGroup},
			{Service: "quicksight", DiscoType: TypeQuickSightKnowledgeBase, Leaf: true},
			{Service: "quicksight", DiscoType: TypeQuickSightNamespace, Leaf: true},
			{Service: "quicksight", DiscoType: TypeQuickSightOAuthClientApplication, Leaf: true},
			{Service: "quicksight", DiscoType: TypeQuickSightRefreshSchedule},
			{Service: "quicksight", DiscoType: TypeQuickSightSpace, Leaf: true},
			{Service: "quicksight", DiscoType: TypeQuickSightTemplate, Leaf: true},
			{Service: "quicksight", DiscoType: TypeQuickSightTheme, Leaf: true},
			{Service: "quicksight", DiscoType: TypeQuickSightTopic, Leaf: true},
			{Service: "quicksight", DiscoType: TypeQuickSightUser},
			{Service: "quicksight", DiscoType: TypeQuickSightVPCConnection},
		},
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
	ListFolders(context.Context, *quicksight.ListFoldersInput, ...func(*quicksight.Options)) (*quicksight.ListFoldersOutput, error)
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
}

// qsSoftSkip — QuickSight returns AccessDenied or
// UnsupportedUserEditionException when the account is not subscribed
// or the edition lacks the feature; ResourceNotFoundException when
// a region/feature is not supported. All treated as soft-skips.
func qsSoftSkip(err error) bool {
	return isAccessDenied(err) || isAPIErrorCode(err, "UnsupportedUserEditionException", "ResourceNotFoundException", "QuickSightUserNotFoundException", "InvalidParameterValueException")
}

func scanQuickSight(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := quicksight.NewFromConfig(acct.cfg, func(o *quicksight.Options) { o.Region = region })

	dsIDs, t, i, ferr := scanQSDataSets(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i

	nsARNs, t, i, ferr := scanQSNamespaces(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanQSAnalyses(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanQSDashboards(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanQSDataSources(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanQSFolders(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanQSTemplates(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanQSThemes(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanQSTopics(ctx, client, acct, region, st, scanID) },
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
		func() (int, int, error) { return scanQSGroups(ctx, client, acct, region, st, scanID, nsARNs) },
		func() (int, int, error) { return scanQSUsers(ctx, client, acct, region, st, scanID, nsARNs) },
		func() (int, int, error) { return scanQSAssignments(ctx, client, acct, region, st, scanID, nsARNs) },
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

func scanQSFolders(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	pager := quicksight.NewListFoldersPaginator(client, &quicksight.ListFoldersInput{AwsAccountId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("quicksight:ListFolders: %w", perr)
		}
		for _, f := range out.FolderSummaryList {
			arn := sv(f.Arn)
			if arn == "" {
				continue
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
	return upsertBatch(st, batch, "quicksight folders")
}

func scanQSTemplates(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	pager := quicksight.NewListTemplatesPaginator(client, &quicksight.ListTemplatesInput{AwsAccountId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("quicksight:ListTemplates: %w", perr)
		}
		for _, t := range out.TemplateSummaryList {
			arn := sv(t.Arn)
			if arn == "" {
				continue
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
	return upsertBatch(st, batch, "quicksight templates")
}

func scanQSThemes(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	pager := quicksight.NewListThemesPaginator(client, &quicksight.ListThemesInput{AwsAccountId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("quicksight:ListThemes: %w", perr)
		}
		for _, th := range out.ThemeSummaryList {
			arn := sv(th.Arn)
			if arn == "" {
				continue
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
	return upsertBatch(st, batch, "quicksight themes")
}

func scanQSTopics(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	id := acct.ID
	pager := quicksight.NewListTopicsPaginator(client, &quicksight.ListTopicsInput{AwsAccountId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("quicksight:ListTopics: %w", perr)
		}
		for _, t := range out.TopicsSummaries {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			label := sv(t.Name)
			if label == "" {
				label = sv(t.TopicId)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeQuickSightTopic, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "quicksight topics")
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
		// ListActionConnectors raises 500 InternalFailure in regions where
		// the (newish) feature is not deployed. The global 10-attempt
		// adaptive retryer eats ~2m before giving up. Clamp this op's
		// retry budget so the soft-skip path triggers fast.
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
		ManagedByProvider: true, DiscoveredBy: scanID,
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
		ManagedByProvider: true, DiscoveredBy: scanID,
	}}
	return upsertBatch(st, batch, "quicksight account-customization")
}

func scanQSGroups(ctx context.Context, client quickSightAPI, acct *account, region string, st *store.Store, scanID string, namespaces []nsRef) (int, int, error) {
	id := acct.ID
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
				return 0, 0, fmt.Errorf("quicksight:ListGroups %s: %w", name, perr)
			}
			for _, g := range out.GroupList {
				arn := sv(g.Arn)
				if arn == "" {
					continue
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
	return upsertBatch(st, batch, "quicksight groups")
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
