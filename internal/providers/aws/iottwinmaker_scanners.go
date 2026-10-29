package aws

import (
	"cmp"
	"context"
	"maps"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iottwinmaker"
	itmtypes "github.com/aws/aws-sdk-go-v2/service/iottwinmaker/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTTwinMakerWorkspace, Service: "iottwinmaker"})
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
	ListTagsForResource(context.Context, *iottwinmaker.ListTagsForResourceInput, ...func(*iottwinmaker.Options)) (*iottwinmaker.ListTagsForResourceOutput, error)
}

const (
	itmTagsOp = "iottwinmaker:ListTagsForResource"
	// itmPageCap is the maxResults sent to every listing read here and to
	// ListTagsForResource. They all take the model's one MaxResults shape,
	// whose range is 0 to 200, although some of their docs say 250.
	itmPageCap = 200
)

// itmNotFound is the not-found code ListTagsForResource models.
var itmNotFound = []string{"ResourceNotFoundException"}

// scanIoTTwinMaker discovers the IoT TwinMaker workspaces of one region and
// each workspace's component types, entities, scenes and sync jobs. Every
// listing runs as its own phase, one at a time: a phase's error does not
// stop the later ones; a failed write and ctx's end do (runHostPhases). The
// child phases read the workspaces stored this scan, those workspaces
// concurrently. Every type has its tags read per row (itmTags), and the tag
// denials of the whole scan are one warning.
//
// The child listings model no not-found code, so a workspace deleted after
// its tag read fails them with whatever code the service sends, and that
// error is returned. One deleted before its tag read is dropped as not
// found and is no parent.
func scanIoTTwinMaker(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := iottwinmaker.NewFromConfig(acct.cfg, func(o *iottwinmaker.Options) { o.Region = region })
	tags := itmTags(client).sharingDenials()
	var workspaces []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "iottwinmaker", 0, []hostPhase{
		{op: "iottwinmaker:ListWorkspaces", run: func(p *phaseTotals) (failed bool) {
			workspaces, failed = scanIoTTMWorkspaces(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "iottwinmaker:ListComponentTypes", run: func(p *phaseTotals) bool {
			return scanIoTTMComponentTypes(ctx, client, acct, region, st, scanID, workspaces, tags, p)
		}},
		{op: "iottwinmaker:ListEntities", run: func(p *phaseTotals) bool {
			return scanIoTTMEntities(ctx, client, acct, region, st, scanID, workspaces, tags, p)
		}},
		{op: "iottwinmaker:ListScenes", run: func(p *phaseTotals) bool {
			return scanIoTTMScenes(ctx, client, acct, region, st, scanID, workspaces, tags, p)
		}},
		{op: "iottwinmaker:ListSyncJobs", run: func(p *phaseTotals) bool {
			return scanIoTTMSyncJobs(ctx, client, acct, region, st, scanID, workspaces, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// itmTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its list element carries. The Service Reference lists
// all five types read here (workspace, componentType, entity, scene,
// syncJob) under ListTagsForResource, and each one's Create* input takes
// Tags. The op pages its tags; every page is read, and a repeated nextToken
// fails the read. ResourceNotFoundException, its modeled not-found, drops a
// resource deleted after it was listed.
func itmTags(client iotTwinMakerAPI) rowTagReader {
	return rowTagReader{
		op:       itmTagsOp,
		notFound: itmNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			pages, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]map[string]string, *string, error) {
				out, err := client.ListTagsForResource(ctx, &iottwinmaker.ListTagsForResourceInput{
					ResourceARN: sdkaws.String(r.NativeID), MaxResults: sdkaws.Int32(itmPageCap), NextToken: token,
				})
				if err != nil {
					return nil, nil, err
				}
				return []map[string]string{out.Tags}, out.NextToken, nil
			})
			if err != nil {
				return nil, err
			}
			tags := map[string]string{}
			for _, page := range pages {
				maps.Copy(tags, page)
			}
			return mapTagsJSON(tags), nil
		},
	}
}

// itmRow is the row of a listed element stored under the ARN it carries and
// named name, the element verbatim as its attributes. An element without an
// ARN is not stored (arnRow); key names it in that warning.
func itmRow(acct *account, region, scanID, typ, what string, arn, name *string, key string, element any) (*store.Resource, string, string) {
	return arnRow(what, arn, &key, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn), Name: name, Region: &region,
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	})
}

// itmChildKey names a workspace's element in a warning by the workspace id
// and the element's own id.
func itmChildKey(workspace, id string) string {
	return workspace + "/" + cmp.Or(id, noIdentifier)
}

// scanIoTTMWorkspaces stores the region's workspaces with their tags, a page
// at a time, and returns the stored ones that have a WorkspaceId, the
// parents of the child phases. It reports whether a write failed, after
// which the scan must stop.
func scanIoTTMWorkspaces(ctx context.Context, client iotTwinMakerAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, "iottwinmaker:ListWorkspaces", "workspaces", "WorkspaceId",
		func(ctx context.Context, token *string) ([]itmtypes.WorkspaceSummary, *string, error) {
			out, err := client.ListWorkspaces(ctx, &iottwinmaker.ListWorkspacesInput{MaxResults: sdkaws.Int32(itmPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.WorkspaceSummaries, out.NextToken, nil
		},
		func(w itmtypes.WorkspaceSummary) (*store.Resource, string, string) {
			return itmRow(acct, region, scanID, TypeIoTTwinMakerWorkspace, "workspaces", w.Arn, w.WorkspaceId, sv(w.WorkspaceId), w)
		},
		func(w itmtypes.WorkspaceSummary) string { return sv(w.WorkspaceId) },
		tags, p)
}

// scanIoTTMComponentTypes stores each workspace's component types with their
// tags, each page written as soon as its tags are read; one workspace's
// failure costs the others none of their rows (storeEachParentPagesTags).
func scanIoTTMComponentTypes(ctx context.Context, client iotTwinMakerAPI, acct *account, region string, st *store.Store, scanID string, workspaces []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeEachParentPagesTags(ctx, st, acct, region, "iottwinmaker:ListComponentTypes", nil, workspaces,
		func(ctx context.Context, ws string, token *string) ([]itmtypes.ComponentTypeSummary, *string, error) {
			out, err := client.ListComponentTypes(ctx, &iottwinmaker.ListComponentTypesInput{WorkspaceId: &ws, MaxResults: sdkaws.Int32(itmPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ComponentTypeSummaries, out.NextToken, nil
		},
		func(ws string, c itmtypes.ComponentTypeSummary) (*store.Resource, string, string) {
			return itmRow(acct, region, scanID, TypeIoTTwinMakerComponentType, "component types", c.Arn, c.ComponentTypeId, itmChildKey(ws, sv(c.ComponentTypeId)), c)
		}, tags, p)
}

// scanIoTTMEntities stores each workspace's entities with their tags, as
// scanIoTTMComponentTypes stores component types.
func scanIoTTMEntities(ctx context.Context, client iotTwinMakerAPI, acct *account, region string, st *store.Store, scanID string, workspaces []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeEachParentPagesTags(ctx, st, acct, region, "iottwinmaker:ListEntities", nil, workspaces,
		func(ctx context.Context, ws string, token *string) ([]itmtypes.EntitySummary, *string, error) {
			out, err := client.ListEntities(ctx, &iottwinmaker.ListEntitiesInput{WorkspaceId: &ws, MaxResults: sdkaws.Int32(itmPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.EntitySummaries, out.NextToken, nil
		},
		func(ws string, e itmtypes.EntitySummary) (*store.Resource, string, string) {
			return itmRow(acct, region, scanID, TypeIoTTwinMakerEntity, "entities", e.Arn, e.EntityName, itmChildKey(ws, sv(e.EntityId)), e)
		}, tags, p)
}

// scanIoTTMScenes stores each workspace's scenes with their tags, as
// scanIoTTMComponentTypes stores component types.
func scanIoTTMScenes(ctx context.Context, client iotTwinMakerAPI, acct *account, region string, st *store.Store, scanID string, workspaces []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeEachParentPagesTags(ctx, st, acct, region, "iottwinmaker:ListScenes", nil, workspaces,
		func(ctx context.Context, ws string, token *string) ([]itmtypes.SceneSummary, *string, error) {
			out, err := client.ListScenes(ctx, &iottwinmaker.ListScenesInput{WorkspaceId: &ws, MaxResults: sdkaws.Int32(itmPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.SceneSummaries, out.NextToken, nil
		},
		func(ws string, s itmtypes.SceneSummary) (*store.Resource, string, string) {
			return itmRow(acct, region, scanID, TypeIoTTwinMakerScene, "scenes", s.Arn, s.SceneId, itmChildKey(ws, sv(s.SceneId)), s)
		}, tags, p)
}

// scanIoTTMSyncJobs stores each workspace's sync jobs with their tags, as
// scanIoTTMComponentTypes stores component types. A sync job is identified
// by its sync source within its workspace (GetSyncJob and DeleteSyncJob key
// on syncSource); its row is named by that source, empty when the element
// has none.
func scanIoTTMSyncJobs(ctx context.Context, client iotTwinMakerAPI, acct *account, region string, st *store.Store, scanID string, workspaces []childParent, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeEachParentPagesTags(ctx, st, acct, region, "iottwinmaker:ListSyncJobs", nil, workspaces,
		func(ctx context.Context, ws string, token *string) ([]itmtypes.SyncJobSummary, *string, error) {
			out, err := client.ListSyncJobs(ctx, &iottwinmaker.ListSyncJobsInput{WorkspaceId: &ws, MaxResults: sdkaws.Int32(itmPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.SyncJobSummaries, out.NextToken, nil
		},
		func(ws string, s itmtypes.SyncJobSummary) (*store.Resource, string, string) {
			source := sv(s.SyncSource)
			return itmRow(acct, region, scanID, TypeIoTTwinMakerSyncJob, "sync jobs", s.Arn, &source, itmChildKey(ws, source), s)
		}, tags, p)
}
