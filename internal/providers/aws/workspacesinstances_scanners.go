package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/workspacesinstances"
)

func init() {
	registerService(serviceEntry{
		name: "aws:workspaces-instances",
		fn:   scanWorkspacesInstances,
		emits: []coverage.TypeDecl{
			{Service: "workspaces-instances", DiscoType: TypeWorkspacesInstancesWorkspaceInstance, Leaf: true},
		},
	})
}

// scanWorkspacesInstances discovers WorkspacesInstances workspace instances.
// Volume and VolumeAssociation skip-logged: SDK has no list endpoints; the
// underlying volumes are EC2 EBS volumes covered separately.
func scanWorkspacesInstances(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := workspacesinstances.NewFromConfig(acct.cfg, func(o *workspacesinstances.Options) { o.Region = region })

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
