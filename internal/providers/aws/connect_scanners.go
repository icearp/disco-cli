package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/icearp/disco-cli/store"
)

func init() {
	// Connect types are declared per family file via registerType;
	// scanConnect fans out to family scanners (core, routing, flows,
	// users, integration, workspace, datatable, extra, children, analytics).
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
	return scanConnectAll(ctx, client, instances, acct, region, st, scanID)
}

// scanConnectAll runs the older families, which stop at their first hard
// error, and the analytics family concurrently, on a WaitGroup rather than an
// errgroup so that a failure on either side cancels nothing and costs the
// other no rows. Analytics reads no stored parents, so it need not wait for
// the older side. The older side's transient error is demoted to a warning:
// one transient member would make the dispatcher read the whole join as
// transient, hiding the analytics family's hard error and discarding every
// family's counts.
func scanConnectAll(ctx context.Context, client *connect.Client, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		wg                    sync.WaitGroup
		total, inserted, t, i int
		err, aerr             error
	)
	wg.Go(func() {
		total, inserted, err = scanConnectOlderFamilies(ctx, client, instances, acct, region, st, scanID)
	})
	wg.Go(func() { t, i, aerr = scanConnectAnalytics(ctx, client, instances, acct, region, st, scanID) })
	wg.Wait()
	return total + t, inserted + i, errors.Join(demoteTransient(st, "connect", acct, region, err), aerr)
}

// scanConnectOlderFamilies runs the families that predate analytics, then the
// children, which read the parent rows those families stored this scan.
func scanConnectOlderFamilies(ctx context.Context, client *connect.Client, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	total, inserted, err = runScanners(
		ctx,
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
		func(ctx context.Context) (int, int, error) {
			return scanConnectDataTable(ctx, client, instances, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanConnectExtra(ctx, client, instances, acct, region, st, scanID)
		},
	)
	if err != nil {
		return total, inserted, err
	}
	// Sequenced after every family above: the children read the parent rows
	// those families stored this scan.
	t, i, err := scanConnectChildren(ctx, client, instances, acct, region, st, scanID)
	return total + t, inserted + i, err
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
			// Per-region availability gap: regions without Connect instance
			// enumeration reject ListInstances with InvalidRequestException —
			// treat as no instances.
			if isAPIErrorCode(perr, "InvalidRequestException") {
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
