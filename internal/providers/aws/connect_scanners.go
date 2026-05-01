package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
)

func init() {
	// Connect emits are declared per family file via registerExtraEmits —
	// the scanConnect dispatcher fans out to family scanners (core,
	// routing, flows, users, integration, workspace, datatable). Most
	// Connect resources are per-Instance scoped, so the dispatcher fetches
	// the instance list once and passes it to each family scanner.
	registerService(serviceEntry{name: "aws:connect", fn: scanConnect})
}

// scanConnect discovers all Connect resource types in one region. Connect
// resources are mostly per-Instance scoped, so we fetch instances first
// and pass them to each family. Families that own account-scoped
// resources (TrafficDistributionGroup) ignore the instance list.
func scanConnect(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := connect.NewFromConfig(acct.cfg, func(o *connect.Options) { o.Region = region })

	instances, ierr := listConnectInstances(ctx, client, acct, region, st)
	if ierr != nil {
		return 0, 0, ierr
	}

	return runScanners(ctx,
		func(ctx context.Context) (int, int, error) {
			return scanConnectCore(ctx, client, instances, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanConnectRouting(ctx, client, instances, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanConnectUsers(ctx, client, instances, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanConnectFlows(ctx, client, instances, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanConnectIntegration(ctx, client, instances, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanConnectWorkspace(ctx, client, instances, acct, region, st, scanID)
		},
	)
}

// listConnectInstances pages ListInstances once. Returns nil on access-denied
// (no instances; family scanners short-circuit). Other errors propagate.
func listConnectInstances(ctx context.Context, client connectInstanceLister, acct *account, region string, st *store.Store) ([]cttypes.InstanceSummary, error) {
	pager := connect.NewListInstancesPaginator(client, &connect.ListInstancesInput{})
	var all []cttypes.InstanceSummary
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "connect:ListInstances", acct.ID, region, perr)
				return nil, nil
			}
			return nil, fmt.Errorf("connect:ListInstances: %w", perr)
		}
		all = append(all, out.InstanceSummaryList...)
	}
	return all, nil
}

// connectInstanceLister narrows ListInstances for the dispatcher's pre-pass.
type connectInstanceLister interface {
	ListInstances(context.Context, *connect.ListInstancesInput, ...func(*connect.Options)) (*connect.ListInstancesOutput, error)
}
