package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/workspacesinstances"
)

func init() {
	registerType(restype.Descriptor{Type: TypeWorkspacesInstancesWorkspaceInstance, Service: "workspaces-instances", Leaf: true})
	registerService(serviceEntry{
		name: "aws:workspaces-instances",
		fn:   scanWorkspacesInstances,
	})
}

// workspacesInstancesAPI narrows the SDK client to the ops the scanner needs.
// ListRegions satisfies NewListRegionsPaginator, ListWorkspaceInstances
// satisfies NewListWorkspaceInstancesPaginator (paginator constructors only
// require the underlying op method) — a test stub can drive both.
type workspacesInstancesAPI interface {
	ListRegions(context.Context, *workspacesinstances.ListRegionsInput, ...func(*workspacesinstances.Options)) (*workspacesinstances.ListRegionsOutput, error)
	ListWorkspaceInstances(context.Context, *workspacesinstances.ListWorkspaceInstancesInput, ...func(*workspacesinstances.Options)) (*workspacesinstances.ListWorkspaceInstancesOutput, error)
}

// scanWorkspacesInstances discovers WorkspacesInstances workspace instances.
// Volume and VolumeAssociation skip-logged: SDK has no list endpoints; the
// underlying volumes are EC2 EBS volumes covered separately.
func scanWorkspacesInstances(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	enabled, rerr := workspacesInstancesEnabledRegions(ctx, acct)
	if rerr != nil {
		if isAccessDenied(rerr) {
			return 0, 0, skipIfAccessDenied(st, "workspacesinstances:ListRegions", acct.ID, region, rerr)
		}
		return 0, 0, fmt.Errorf("workspacesinstances:ListRegions: %w", rerr)
	}
	client := workspacesinstances.NewFromConfig(acct.cfg, func(o *workspacesinstances.Options) { o.Region = region })
	return scanWorkspacesInstancesIn(ctx, client, enabled, acct, region, st, scanID)
}

// scanWorkspacesInstancesIn is the testable core. It silent-skips regions
// where the service isn't enabled: an inactive endpoint kills the connection
// with an HTTP/2 GOAWAY (non-replayable POST body — every retry fails) instead
// of a clean error, so the enabled set is resolved up front via ListRegions.
func scanWorkspacesInstancesIn(ctx context.Context, client workspacesInstancesAPI, enabled map[string]bool, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	if !enabled[region] {
		return 0, 0, nil
	}

	pager := workspacesinstances.NewListWorkspaceInstancesPaginator(client, &workspacesinstances.ListWorkspaceInstancesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "workspacesinstances:ListWorkspaceInstances", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("workspacesinstances:ListWorkspaceInstances: %w", perr)
		}
		for _, w := range out.WorkspaceInstances {
			id := sv(w.WorkspaceInstanceId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:workspaces-instances:%s:%s:workspace-instance/%s", region, acct.ID, id)
			label := id
			status := string(w.ProvisionState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWorkspacesInstancesWorkspaceInstance, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "workspaces-instances workspace-instances")
}

// workspacesInstancesEnabledRegions returns the set of regions where
// WorkspacesInstances is enabled for this account, via the service's own
// ListRegions API. Computed once per account (result is account-global) and
// cached — concurrent per-region scanners share it.
//
// Pinned to us-east-1: a regional endpoint where the service is NOT enabled
// sends an HTTP/2 GOAWAY instead of a clean error, so the enabled set can't be
// discovered from an arbitrary region. ListRegions is an account-global
// control call.
func workspacesInstancesEnabledRegions(ctx context.Context, acct *account) (map[string]bool, error) {
	acct.wsiRegionsOnce.Do(func() {
		client := workspacesinstances.NewFromConfig(acct.cfg, func(o *workspacesinstances.Options) { o.Region = "us-east-1" })
		acct.wsiRegions, acct.wsiRegionsErr = listWorkspacesInstancesRegions(ctx, client)
	})
	return acct.wsiRegions, acct.wsiRegionsErr
}

// listWorkspacesInstancesRegions pages ListRegions into a lookup set.
func listWorkspacesInstancesRegions(ctx context.Context, client workspacesInstancesAPI) (map[string]bool, error) {
	set := map[string]bool{}
	p := workspacesinstances.NewListRegionsPaginator(client, &workspacesinstances.ListRegionsInput{})
	for p.HasMorePages() {
		out, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range out.Regions {
			if n := sv(r.RegionName); n != "" {
				set[n] = true
			}
		}
	}
	return set, nil
}
