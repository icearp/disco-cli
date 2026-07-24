package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/iottwinmaker"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTTwinMakerWorkspace, Service: "iottwinmaker", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIoTTwinMakerComponentType, Service: "iottwinmaker"})
	registerType(restype.Descriptor{Type: TypeIoTTwinMakerEntity, Service: "iottwinmaker"})
	registerType(restype.Descriptor{Type: TypeIoTTwinMakerScene, Service: "iottwinmaker"})
	registerType(restype.Descriptor{Type: TypeIoTTwinMakerSyncJob, Service: "iottwinmaker"})
	registerService(serviceEntry{
		name: "aws:iottwinmaker",
		fn:   scanIoTTwinMaker,
	})
}

type iotTwinMakerAPI interface {
	ListWorkspaces(context.Context, *iottwinmaker.ListWorkspacesInput, ...func(*iottwinmaker.Options)) (*iottwinmaker.ListWorkspacesOutput, error)
	ListComponentTypes(context.Context, *iottwinmaker.ListComponentTypesInput, ...func(*iottwinmaker.Options)) (*iottwinmaker.ListComponentTypesOutput, error)
	ListEntities(context.Context, *iottwinmaker.ListEntitiesInput, ...func(*iottwinmaker.Options)) (*iottwinmaker.ListEntitiesOutput, error)
	ListScenes(context.Context, *iottwinmaker.ListScenesInput, ...func(*iottwinmaker.Options)) (*iottwinmaker.ListScenesOutput, error)
	ListSyncJobs(context.Context, *iottwinmaker.ListSyncJobsInput, ...func(*iottwinmaker.Options)) (*iottwinmaker.ListSyncJobsOutput, error)
}

// scanIoTTwinMaker discovers IoT TwinMaker workspaces and per-workspace
// component types, entities, scenes, and sync jobs.
func scanIoTTwinMaker(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := iottwinmaker.NewFromConfig(acct.cfg, func(o *iottwinmaker.Options) { o.Region = region })

	wsIDs, t, i, ferr := scanIoTTMWorkspaces(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, w := range wsIDs {
		for _, phase := range []func() (int, int, error){
			func() (int, int, error) { return scanIoTTMComponentTypes(ctx, client, acct, region, st, scanID, w) },
			func() (int, int, error) { return scanIoTTMEntities(ctx, client, acct, region, st, scanID, w) },
			func() (int, int, error) { return scanIoTTMScenes(ctx, client, acct, region, st, scanID, w) },
			func() (int, int, error) { return scanIoTTMSyncJobs(ctx, client, acct, region, st, scanID, w) },
		} {
			t, i, perr := phase()
			if perr != nil {
				return total, inserted, perr
			}
			total += t
			inserted += i
		}
	}
	return total, inserted, nil
}

func scanIoTTMWorkspaces(ctx context.Context, client iotTwinMakerAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := iottwinmaker.NewListWorkspacesPaginator(client, &iottwinmaker.ListWorkspacesInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "iottwinmaker:ListWorkspaces", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("iottwinmaker:ListWorkspaces: %w", err)
		}
		for _, w := range out.WorkspaceSummaries {
			arn := sv(w.Arn)
			if arn == "" {
				continue
			}
			if id := sv(w.WorkspaceId); id != "" {
				ids = append(ids, id)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTTwinMakerWorkspace, NativeID: arn,
				Name: w.WorkspaceId, Region: &region,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "iottwinmaker workspaces")
	return ids, t, i, err
}

func scanIoTTMComponentTypes(ctx context.Context, client iotTwinMakerAPI, acct *account, region string, st *store.Store, scanID string, workspaceID string) (int, int, error) {
	wid := workspaceID
	pager := iottwinmaker.NewListComponentTypesPaginator(client, &iottwinmaker.ListComponentTypesInput{WorkspaceId: &wid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "iottwinmaker:ListComponentTypes", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("iottwinmaker:ListComponentTypes: %w", err)
		}
		for _, c := range out.ComponentTypeSummaries {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTTwinMakerComponentType, NativeID: arn,
				Name: c.ComponentTypeId, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iottwinmaker component-types")
}

func scanIoTTMEntities(ctx context.Context, client iotTwinMakerAPI, acct *account, region string, st *store.Store, scanID string, workspaceID string) (int, int, error) {
	wid := workspaceID
	pager := iottwinmaker.NewListEntitiesPaginator(client, &iottwinmaker.ListEntitiesInput{WorkspaceId: &wid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "iottwinmaker:ListEntities", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("iottwinmaker:ListEntities: %w", err)
		}
		for _, e := range out.EntitySummaries {
			arn := sv(e.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTTwinMakerEntity, NativeID: arn,
				Name: e.EntityName, Region: &region,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iottwinmaker entities")
}

func scanIoTTMScenes(ctx context.Context, client iotTwinMakerAPI, acct *account, region string, st *store.Store, scanID string, workspaceID string) (int, int, error) {
	wid := workspaceID
	pager := iottwinmaker.NewListScenesPaginator(client, &iottwinmaker.ListScenesInput{WorkspaceId: &wid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "iottwinmaker:ListScenes", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("iottwinmaker:ListScenes: %w", err)
		}
		for _, s := range out.SceneSummaries {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTTwinMakerScene, NativeID: arn,
				Name: s.SceneId, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iottwinmaker scenes")
}

func scanIoTTMSyncJobs(ctx context.Context, client iotTwinMakerAPI, acct *account, region string, st *store.Store, scanID string, workspaceID string) (int, int, error) {
	wid := workspaceID
	pager := iottwinmaker.NewListSyncJobsPaginator(client, &iottwinmaker.ListSyncJobsInput{WorkspaceId: &wid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "iottwinmaker:ListSyncJobs", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("iottwinmaker:ListSyncJobs: %w", err)
		}
		for _, s := range out.SyncJobSummaries {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			label := sv(s.SyncSource)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTTwinMakerSyncJob, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "iottwinmaker sync-jobs")
}
