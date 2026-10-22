package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/icearp/disco-cli/store"
)

func init() {
	// Connect types are declared per family file via registerType;
	// scanConnect fans out to family scanners (core, routing, flows,
	// users, integration, workspace, datatable, extra, children, analytics,
	// access).
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

// scanConnectAll runs the older families (scanConnectOlderFamilies) and the
// analytics family concurrently, on a WaitGroup rather than an errgroup so that
// a failure on either side cancels nothing and costs the other no rows.
// Analytics reads no stored parents, so it need not wait for the older side.
// The two sides' errors are joined by phaseErrs, so a transient one is demoted
// to a warning only beside a hard one: joined, it would be buried in the hard
// error instead of reported under its op.
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
	var errs phaseErrs
	errs.add("connect", err)
	errs.add("connect analytics", aerr)
	return total + t, inserted + i, errs.join(st, acct, region)
}

// scanConnectOlderFamilies runs the families that predate analytics, then the
// children and the access family concurrently. Both read parent rows those
// families stored this scan (the children read hours of operation, integration
// associations, evaluation forms and workspaces; the access family reads
// instances, security profiles and routing profiles), never each other's rows.
// Both run whatever the families returned, so after a family failure they list
// the children of only the parents stored before it. On a WaitGroup, a failure
// on one side cancels nothing on the other. The family, children and access
// errors are joined by phaseErrs, which demotes a transient one to a warning
// only beside a hard one.
func scanConnectOlderFamilies(ctx context.Context, client *connect.Client, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, err := scanConnectFamilies(ctx, client, instances, acct, region, st, scanID)
	var (
		wg                  sync.WaitGroup
		ct, ci, at, ai      int
		childErr, accessErr error
	)
	wg.Go(func() { ct, ci, childErr = scanConnectChildren(ctx, client, instances, acct, region, st, scanID) })
	wg.Go(func() { at, ai, accessErr = scanConnectAccess(ctx, client, acct, region, st, scanID) })
	wg.Wait()
	var errs phaseErrs
	errs.add("connect", err)
	errs.add("connect", childErr)
	errs.add("connect access", accessErr)
	return total + ct + at, inserted + ci + ai, errs.join(st, acct, region)
}

// scanConnectFamilies runs the families that predate analytics, stopping at
// the first hard error.
func scanConnectFamilies(ctx context.Context, client *connect.Client, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return runScanners(
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
