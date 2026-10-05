package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/iotsitewise"
	"github.com/aws/aws-sdk-go-v2/service/iotsitewise/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// isIoTSiteWiseFeatureUnsupported reports whether err is the per-region
// `InvalidRequestException: Feature not supported yet` shape IoT SiteWise
// returns when a sub-API isn't available in-region (e.g. ListComputationModels:
// works in us-east-1/eu-west-1, rejects in us-west-2) — distinct from a real
// validation error, which carries a different message body.
func isIoTSiteWiseFeatureUnsupported(err error) bool {
	return isAPIErrorWithMessage(err, "InvalidRequestException", "Feature not supported")
}

func init() {
	registerType(restype.Descriptor{Type: TypeIoTSWAccessPolicy, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWApplication, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWAsset, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWAssetModel, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWComputationModel, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWDashboard, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWDataset, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWGateway, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWPipeline, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWPortal, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWProject, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWTask, Service: "iotsitewise"})
	registerType(restype.Descriptor{Type: TypeIoTSWWorkspace, Service: "iotsitewise"})
	registerService(serviceEntry{
		name: "aws:iotsitewise",
		fn:   scanIoTSiteWise,
	})
}

type iotSWAPI interface {
	ListAccessPolicies(context.Context, *iotsitewise.ListAccessPoliciesInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListAccessPoliciesOutput, error)
	ListAssets(context.Context, *iotsitewise.ListAssetsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListAssetsOutput, error)
	ListAssetModels(context.Context, *iotsitewise.ListAssetModelsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListAssetModelsOutput, error)
	DescribeAssetModel(context.Context, *iotsitewise.DescribeAssetModelInput, ...func(*iotsitewise.Options)) (*iotsitewise.DescribeAssetModelOutput, error)
	ListComputationModels(context.Context, *iotsitewise.ListComputationModelsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListComputationModelsOutput, error)
	DescribeComputationModel(context.Context, *iotsitewise.DescribeComputationModelInput, ...func(*iotsitewise.Options)) (*iotsitewise.DescribeComputationModelOutput, error)
	ListDashboards(context.Context, *iotsitewise.ListDashboardsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListDashboardsOutput, error)
	ListDatasets(context.Context, *iotsitewise.ListDatasetsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListDatasetsOutput, error)
	DescribeDataset(context.Context, *iotsitewise.DescribeDatasetInput, ...func(*iotsitewise.Options)) (*iotsitewise.DescribeDatasetOutput, error)
	ListGateways(context.Context, *iotsitewise.ListGatewaysInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListGatewaysOutput, error)
	ListPortals(context.Context, *iotsitewise.ListPortalsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListPortalsOutput, error)
	ListProjects(context.Context, *iotsitewise.ListProjectsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListProjectsOutput, error)
	ListWorkspaces(context.Context, *iotsitewise.ListWorkspacesInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListWorkspacesOutput, error)
	ListPipelines(context.Context, *iotsitewise.ListPipelinesInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListPipelinesOutput, error)
	ListTasks(context.Context, *iotsitewise.ListTasksInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListTasksOutput, error)
	ListApplications(context.Context, *iotsitewise.ListApplicationsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListApplicationsOutput, error)
}

func iotSWARN(region, acct, kind, id string) string {
	return fmt.Sprintf("arn:aws:iotsitewise:%s:%s:%s/%s", region, acct, kind, id)
}

func scanIoTSiteWise(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := iotsitewise.NewFromConfig(acct.cfg, func(o *iotsitewise.Options) { o.Region = region })

	// Phase 1: Asset Models (collect IDs).
	modelIDs, t, i, ferr := scanIoTSWAssetModels(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	// Phase 2: Assets per model (Filter=ALL).
	for _, mid := range modelIDs {
		t, i, perr := scanIoTSWAssets(ctx, client, acct, region, st, scanID, mid)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}

	// Phase 3: Top-level computation models, gateways, datasets.
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanIoTSWComputationModels(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTSWGateways(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTSWDatasets(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}

	// Phase 4: Portals (collect IDs for projects + access-policies).
	portalIDs, t, i, ferr := scanIoTSWPortals(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	// Phase 5: per-Portal: Projects (collect IDs), AccessPolicies(PORTAL).
	var projectIDs []string
	for _, pid := range portalIDs {
		pjs, t, i, perr := scanIoTSWProjects(ctx, client, acct, region, st, scanID, pid)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
		projectIDs = append(projectIDs, pjs...)

		t, i, perr = scanIoTSWAccessPolicies(ctx, client, acct, region, st, scanID, types.ResourceTypePortal, pid)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}

	// Phase 6: per-Project: Dashboards, AccessPolicies(PROJECT).
	for _, pjid := range projectIDs {
		t, i, perr := scanIoTSWDashboards(ctx, client, acct, region, st, scanID, pjid)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i

		t, i, perr = scanIoTSWAccessPolicies(ctx, client, acct, region, st, scanID, types.ResourceTypeProject, pjid)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}

	// Phase 7: Workspaces, then applications, pipelines and tasks.
	t, i, err = scanIoTSWWorkspaceFamily(ctx, client, acct, region, st, scanID)
	return total + t, inserted + i, err
}

func scanIoTSWAssetModels(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := iotsitewise.NewListAssetModelsPaginator(client, &iotsitewise.ListAssetModelsInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotsitewise:ListAssetModels", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("iotsitewise:ListAssetModels: %w", perr)
		}
		for _, m := range out.AssetModelSummaries {
			arn := sv(m.Arn)
			id := sv(m.Id)
			if arn == "" {
				continue
			}
			label := sv(m.Name)
			if label == "" {
				label = id
			}
			if id != "" {
				ids = append(ids, id)
			}
			// Enrich via DescribeAssetModel — AssetModelHierarchies[].ChildAssetModelId
			// isn't in the list-summary shape; fall back to summary on per-row failure.
			attrs := mustJSON(m)
			if id != "" {
				mid := id
				dout, derr := client.DescribeAssetModel(ctx, &iotsitewise.DescribeAssetModelInput{AssetModelId: &mid})
				if derr != nil {
					if isAccessDenied(derr) {
						_ = skipIfAccessDenied(st, "iotsitewise:DescribeAssetModel", acct.ID, region, derr)
					}
				} else if dout != nil {
					attrs = mustJSON(dout)
				}
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTSWAssetModel, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: attrs, DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "iotsitewise asset-models")
	return ids, t, i, err
}

func scanIoTSWAssets(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID, modelID string) (int, int, error) {
	mid := modelID
	pager := iotsitewise.NewListAssetsPaginator(client, &iotsitewise.ListAssetsInput{
		AssetModelId: &mid,
		Filter:       types.ListAssetsFilterAll,
	})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotsitewise:ListAssets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotsitewise:ListAssets: %w", perr)
		}
		for _, a := range out.AssetSummaries {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			label := sv(a.Name)
			if label == "" {
				label = sv(a.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTSWAsset, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iotsitewise assets")
}

func scanIoTSWComputationModels(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iotsitewise.NewListComputationModelsPaginator(client, &iotsitewise.ListComputationModelsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isIoTSiteWiseFeatureUnsupported(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotsitewise:ListComputationModels", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotsitewise:ListComputationModels: %w", perr)
		}
		for _, c := range out.ComputationModelSummaries {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			label := sv(c.Name)
			if label == "" {
				label = sv(c.Id)
			}
			// Enrich via DescribeComputationModel — DataBinding refs to asset-models/
			// assets aren't in the list-summary shape; fall back to summary on per-row failure.
			attrs := mustJSON(c)
			if cid := c.Id; cid != nil {
				dout, derr := client.DescribeComputationModel(ctx, &iotsitewise.DescribeComputationModelInput{ComputationModelId: cid})
				if derr != nil {
					if isAccessDenied(derr) {
						_ = skipIfAccessDenied(st, "iotsitewise:DescribeComputationModel", acct.ID, region, derr)
					}
				} else if dout != nil {
					attrs = mustJSON(dout)
				}
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTSWComputationModel, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: attrs, DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iotsitewise computation-models")
}

func scanIoTSWGateways(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iotsitewise.NewListGatewaysPaginator(client, &iotsitewise.ListGatewaysInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotsitewise:ListGateways", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotsitewise:ListGateways: %w", perr)
		}
		for _, g := range out.GatewaySummaries {
			id := sv(g.GatewayId)
			if id == "" {
				continue
			}
			arn := iotSWARN(region, acct.ID, "gateway", id)
			label := sv(g.GatewayName)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTSWGateway, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iotsitewise gateways")
}

func scanIoTSWDatasets(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	// SourceType is required; only one defined value (KENDRA).
	pager := iotsitewise.NewListDatasetsPaginator(client, &iotsitewise.ListDatasetsInput{
		SourceType: types.DatasetSourceTypeKendra,
	})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isIoTSiteWiseFeatureUnsupported(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotsitewise:ListDatasets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotsitewise:ListDatasets: %w", perr)
		}
		for _, d := range out.DatasetSummaries {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			label := sv(d.Name)
			if label == "" {
				label = sv(d.Id)
			}
			// Enrich via DescribeDataset — Source.SourceDetail.Kendra.{KnowledgeBaseArn,RoleArn}
			// isn't in the list-summary shape; fall back to summary on per-row failure.
			attrs := mustJSON(d)
			did := d.Id
			if did != nil {
				dout, derr := client.DescribeDataset(ctx, &iotsitewise.DescribeDatasetInput{DatasetId: did})
				if derr != nil {
					if isAccessDenied(derr) {
						_ = skipIfAccessDenied(st, "iotsitewise:DescribeDataset", acct.ID, region, derr)
					}
				} else if dout != nil {
					attrs = mustJSON(dout)
				}
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTSWDataset, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: attrs, DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iotsitewise datasets")
}

func scanIoTSWPortals(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := iotsitewise.NewListPortalsPaginator(client, &iotsitewise.ListPortalsInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotsitewise:ListPortals", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("iotsitewise:ListPortals: %w", perr)
		}
		for _, p := range out.PortalSummaries {
			id := sv(p.Id)
			if id == "" {
				continue
			}
			arn := iotSWARN(region, acct.ID, "portal", id)
			label := sv(p.Name)
			if label == "" {
				label = id
			}
			ids = append(ids, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTSWPortal, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "iotsitewise portals")
	return ids, t, i, err
}

func scanIoTSWProjects(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID, portalID string) ([]string, int, int, error) {
	pid := portalID
	pager := iotsitewise.NewListProjectsPaginator(client, &iotsitewise.ListProjectsInput{PortalId: &pid})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotsitewise:ListProjects", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("iotsitewise:ListProjects: %w", perr)
		}
		for _, p := range out.ProjectSummaries {
			id := sv(p.Id)
			if id == "" {
				continue
			}
			arn := iotSWARN(region, acct.ID, "project", id)
			label := sv(p.Name)
			if label == "" {
				label = id
			}
			ids = append(ids, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTSWProject, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "iotsitewise projects")
	if err == nil && len(ids) > 0 {
		portalRowID := store.ResourceID("aws", acct.ID, iotSWARN(region, acct.ID, "portal", pid))
		pairs := make([][2]string, 0, len(ids))
		for _, projID := range ids {
			childID := store.ResourceID("aws", acct.ID, iotSWARN(region, acct.ID, "project", projID))
			pairs = append(pairs, [2]string{childID, portalRowID})
		}
		if herr := st.RecordHierarchyBatch(pairs); herr != nil {
			return ids, t, i, fmt.Errorf("record portal→project hierarchy: %w", herr)
		}
	}
	return ids, t, i, err
}

func scanIoTSWDashboards(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID, projectID string) (int, int, error) {
	pid := projectID
	pager := iotsitewise.NewListDashboardsPaginator(client, &iotsitewise.ListDashboardsInput{ProjectId: &pid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotsitewise:ListDashboards", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotsitewise:ListDashboards: %w", perr)
		}
		for _, d := range out.DashboardSummaries {
			id := sv(d.Id)
			if id == "" {
				continue
			}
			arn := iotSWARN(region, acct.ID, "dashboard", id)
			label := sv(d.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTSWDashboard, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "iotsitewise dashboards")
	if err == nil && len(batch) > 0 {
		projectRowID := store.ResourceID("aws", acct.ID, iotSWARN(region, acct.ID, "project", pid))
		pairs := make([][2]string, 0, len(batch))
		for _, b := range batch {
			pairs = append(pairs, [2]string{store.ResourceID("aws", acct.ID, b.NativeID), projectRowID})
		}
		if herr := st.RecordHierarchyBatch(pairs); herr != nil {
			return t, i, fmt.Errorf("record project→dashboard hierarchy: %w", herr)
		}
	}
	return t, i, err
}

func scanIoTSWAccessPolicies(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string, rType types.ResourceType, resourceID string) (int, int, error) {
	rid := resourceID
	pager := iotsitewise.NewListAccessPoliciesPaginator(client, &iotsitewise.ListAccessPoliciesInput{
		ResourceType: rType,
		ResourceId:   &rid,
	})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotsitewise:ListAccessPolicies", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iotsitewise:ListAccessPolicies: %w", perr)
		}
		for _, p := range out.AccessPolicySummaries {
			id := sv(p.Id)
			if id == "" {
				continue
			}
			arn := iotSWARN(region, acct.ID, "access-policy", id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTSWAccessPolicy, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iotsitewise access-policies")
}

// isIoTSWWorkspaceChildSkip drops a workspace deleted between ListWorkspaces and
// its child list, and a child sub-API not yet rolled out in-region: SiteWise
// rolls sub-APIs out per region independently (see isIoTSiteWiseFeatureUnsupported).
func isIoTSWWorkspaceChildSkip(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException") || isIoTSiteWiseFeatureUnsupported(err)
}

// scanIoTSWWorkspaceFamily runs the account-level applications listing before
// the per-workspace fan-outs so a failing workspace child op cannot cost it.
func scanIoTSWWorkspaceFamily(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	workspaces, total, inserted, err := scanIoTSWWorkspaces(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanIoTSWApplications(ctx, client, acct, region, st, scanID, workspaces)
		},
		func() (int, int, error) { return scanIoTSWPipelines(ctx, client, acct, region, st, scanID, workspaces) },
		func() (int, int, error) { return scanIoTSWTasks(ctx, client, acct, region, st, scanID, workspaces) },
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

// scanIoTSWWorkspaces returns each stored workspace as a childParent keyed by
// name, which is what the workspace child list ops take.
func scanIoTSWWorkspaces(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	pager := iotsitewise.NewListWorkspacesPaginator(client, &iotsitewise.ListWorkspacesInput{})
	var batch []*store.Resource
	var parents []childParent
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isIoTSiteWiseFeatureUnsupported(perr) {
				break
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotsitewise:ListWorkspaces", acct.ID, region, perr)
				break
			}
			return nil, 0, 0, fmt.Errorf("iotsitewise:ListWorkspaces: %w", perr)
		}
		for _, w := range out.WorkspaceSummaries {
			arn, name := sv(w.Arn), sv(w.Name)
			if arn == "" {
				continue
			}
			var state string
			if w.Status != nil {
				state = string(w.Status.State)
			}
			parents = append(parents, childParent{id: name, arn: arn})
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTSWWorkspace, NativeID: arn,
				Name: &name, Status: nonEmptyPtr(state), CreatedAt: tp(w.CreatedAt),
				Region: &region, AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "iotsitewise workspaces")
	if err != nil {
		return nil, 0, 0, err
	}
	return parents, t, i, nil
}

func scanIoTSWPipelines(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string, workspaces []childParent) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "iotsitewise:ListPipelines", workspaces, isIoTSWWorkspaceChildSkip,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			pager := iotsitewise.NewListPipelinesPaginator(client, &iotsitewise.ListPipelinesInput{WorkspaceName: &p.id})
			var rows []*store.Resource
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, pl := range out.PipelineSummaries {
					arn := sv(pl.PipelineArn)
					if arn == "" {
						continue
					}
					var state string
					if pl.Status != nil {
						state = string(pl.Status.State)
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeIoTSWPipeline, NativeID: arn,
						Name: pl.PipelineName, Status: nonEmptyPtr(state), CreatedAt: tp(pl.CreatedAt),
						Region: &region, AttributesJSON: mustJSON(pl), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

func scanIoTSWTasks(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string, workspaces []childParent) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "iotsitewise:ListTasks", workspaces, isIoTSWWorkspaceChildSkip,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			pager := iotsitewise.NewListTasksPaginator(client, &iotsitewise.ListTasksInput{WorkspaceName: &p.id})
			var rows []*store.Resource
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, tk := range out.TaskSummaries {
					arn := sv(tk.TaskArn)
					if arn == "" {
						continue
					}
					var state string
					if tk.Status != nil {
						state = string(tk.Status.State)
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeIoTSWTask, NativeID: arn,
						Name: tk.TaskName, Status: nonEmptyPtr(state), CreatedAt: tp(tk.CreatedAt),
						Region: &region, AttributesJSON: mustJSON(tk), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanIoTSWApplications lists account-wide (ListApplications takes no workspace)
// and links each application under its workspace when that workspace was listed
// this scan.
func scanIoTSWApplications(ctx context.Context, client iotSWAPI, acct *account, region string, st *store.Store, scanID string, workspaces []childParent) (int, int, error) {
	workspaceARN := make(map[string]string, len(workspaces))
	for _, w := range workspaces {
		workspaceARN[w.id] = w.arn
	}
	pager := iotsitewise.NewListApplicationsPaginator(client, &iotsitewise.ListApplicationsInput{})
	var batch []*store.Resource
	var pairs [][2]string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isIoTSiteWiseFeatureUnsupported(perr) {
				break
			}
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iotsitewise:ListApplications", acct.ID, region, perr)
				break
			}
			return 0, 0, fmt.Errorf("iotsitewise:ListApplications: %w", perr)
		}
		for _, a := range out.Applications {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			label := sv(a.Name)
			if label == "" {
				label = sv(a.Id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTSWApplication, NativeID: arn,
				Name: &label, Status: nonEmptyPtr(string(a.Status)), CreatedAt: tp(a.CreatedAt),
				Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
			if parentARN, ok := workspaceARN[sv(a.WorkspaceName)]; ok {
				pairs = append(pairs, [2]string{store.ResourceID("aws", acct.ID, arn), store.ResourceID("aws", acct.ID, parentARN)})
			}
		}
	}
	t, i, err := upsertBatch(st, batch, "iotsitewise applications")
	if err != nil {
		return 0, 0, err
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return 0, 0, fmt.Errorf("record workspace→application hierarchy: %w", err)
	}
	return t, i, nil
}
