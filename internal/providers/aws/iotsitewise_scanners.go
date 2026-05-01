package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/iotsitewise"
	"github.com/aws/aws-sdk-go-v2/service/iotsitewise/types"
)

func init() {
	registerService(serviceEntry{
		name: "aws:iotsitewise",
		fn:   scanIoTSiteWise,
		emits: []coverage.TypeDecl{
			{Service: "iotsitewise", DiscoType: TypeIoTSWAccessPolicy},
			{Service: "iotsitewise", DiscoType: TypeIoTSWAsset},
			{Service: "iotsitewise", DiscoType: TypeIoTSWAssetModel},
			{Service: "iotsitewise", DiscoType: TypeIoTSWComputationModel},
			{Service: "iotsitewise", DiscoType: TypeIoTSWDashboard},
			{Service: "iotsitewise", DiscoType: TypeIoTSWDataset},
			{Service: "iotsitewise", DiscoType: TypeIoTSWGateway},
			{Service: "iotsitewise", DiscoType: TypeIoTSWPortal},
			{Service: "iotsitewise", DiscoType: TypeIoTSWProject},
		},
	})
}

type iotSWAPI interface {
	ListAccessPolicies(context.Context, *iotsitewise.ListAccessPoliciesInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListAccessPoliciesOutput, error)
	ListAssets(context.Context, *iotsitewise.ListAssetsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListAssetsOutput, error)
	ListAssetModels(context.Context, *iotsitewise.ListAssetModelsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListAssetModelsOutput, error)
	ListComputationModels(context.Context, *iotsitewise.ListComputationModelsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListComputationModelsOutput, error)
	ListDashboards(context.Context, *iotsitewise.ListDashboardsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListDashboardsOutput, error)
	ListDatasets(context.Context, *iotsitewise.ListDatasetsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListDatasetsOutput, error)
	ListGateways(context.Context, *iotsitewise.ListGatewaysInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListGatewaysOutput, error)
	ListPortals(context.Context, *iotsitewise.ListPortalsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListPortalsOutput, error)
	ListProjects(context.Context, *iotsitewise.ListProjectsInput, ...func(*iotsitewise.Options)) (*iotsitewise.ListProjectsOutput, error)
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
	return total, inserted, nil
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
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTSWAssetModel, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
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
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTSWComputationModel, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
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
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTSWDataset, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
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
	return upsertBatch(st, batch, "iotsitewise dashboards")
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
