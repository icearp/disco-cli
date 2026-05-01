package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/quicksight"
)

func init() {
	registerService(serviceEntry{
		name: "aws:quicksight",
		fn:   scanQuickSight,
		emits: []coverage.TypeDecl{
			{Service: "quicksight", DiscoType: TypeQuickSightActionConnector},
			{Service: "quicksight", DiscoType: TypeQuickSightAnalysis},
			{Service: "quicksight", DiscoType: TypeQuickSightCustomPermissions},
			{Service: "quicksight", DiscoType: TypeQuickSightDashboard},
			{Service: "quicksight", DiscoType: TypeQuickSightDataSet},
			{Service: "quicksight", DiscoType: TypeQuickSightDataSource},
			{Service: "quicksight", DiscoType: TypeQuickSightFolder},
			{Service: "quicksight", DiscoType: TypeQuickSightRefreshSchedule},
			{Service: "quicksight", DiscoType: TypeQuickSightTemplate},
			{Service: "quicksight", DiscoType: TypeQuickSightTheme},
			{Service: "quicksight", DiscoType: TypeQuickSightTopic},
			{Service: "quicksight", DiscoType: TypeQuickSightVPCConnection},
		},
	})
}

type quickSightAPI interface {
	ListActionConnectors(context.Context, *quicksight.ListActionConnectorsInput, ...func(*quicksight.Options)) (*quicksight.ListActionConnectorsOutput, error)
	ListAnalyses(context.Context, *quicksight.ListAnalysesInput, ...func(*quicksight.Options)) (*quicksight.ListAnalysesOutput, error)
	ListCustomPermissions(context.Context, *quicksight.ListCustomPermissionsInput, ...func(*quicksight.Options)) (*quicksight.ListCustomPermissionsOutput, error)
	ListDashboards(context.Context, *quicksight.ListDashboardsInput, ...func(*quicksight.Options)) (*quicksight.ListDashboardsOutput, error)
	ListDataSets(context.Context, *quicksight.ListDataSetsInput, ...func(*quicksight.Options)) (*quicksight.ListDataSetsOutput, error)
	ListDataSources(context.Context, *quicksight.ListDataSourcesInput, ...func(*quicksight.Options)) (*quicksight.ListDataSourcesOutput, error)
	ListFolders(context.Context, *quicksight.ListFoldersInput, ...func(*quicksight.Options)) (*quicksight.ListFoldersOutput, error)
	ListRefreshSchedules(context.Context, *quicksight.ListRefreshSchedulesInput, ...func(*quicksight.Options)) (*quicksight.ListRefreshSchedulesOutput, error)
	ListTemplates(context.Context, *quicksight.ListTemplatesInput, ...func(*quicksight.Options)) (*quicksight.ListTemplatesOutput, error)
	ListThemes(context.Context, *quicksight.ListThemesInput, ...func(*quicksight.Options)) (*quicksight.ListThemesOutput, error)
	ListTopics(context.Context, *quicksight.ListTopicsInput, ...func(*quicksight.Options)) (*quicksight.ListTopicsOutput, error)
	ListVPCConnections(context.Context, *quicksight.ListVPCConnectionsInput, ...func(*quicksight.Options)) (*quicksight.ListVPCConnectionsOutput, error)
}

// qsSoftSkip — QuickSight returns AccessDenied or
// UnsupportedUserEditionException when the account is not subscribed
// or the edition lacks the feature; ResourceNotFoundException when
// a region/feature is not supported. All treated as soft-skips.
func qsSoftSkip(err error) bool {
	return isAccessDenied(err) || isAPIErrorCode(err, "UnsupportedUserEditionException", "ResourceNotFoundException", "QuickSightUserNotFoundException")
}

func scanQuickSight(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := quicksight.NewFromConfig(acct.cfg, func(o *quicksight.Options) { o.Region = region })

	dsIDs, t, i, ferr := scanQSDataSets(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, ferr
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
	pager := quicksight.NewListActionConnectorsPaginator(client, &quicksight.ListActionConnectorsInput{AwsAccountId: &id})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if qsSoftSkip(perr) {
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
