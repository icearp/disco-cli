package aws

import (
	"context"
	"fmt"
	"sync/atomic"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"golang.org/x/sync/errgroup"
)

func init() {
	// EC2 emits are declared per category file via registerExtraEmits — the
	// scanEC2 dispatcher itself upserts no resources, only fans out to the
	// category scanners (compute_mgmt, networking, ipam, tgw, …).
	registerService(serviceEntry{name: "aws:ec2", fn: scanEC2})
}

// scanEC2 discovers all EC2 resource types in one region by running all
// category scanners in parallel. Each category scanner fans out to its own
// sub-scanners via an internal errgroup.
func scanEC2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := ec2.NewFromConfig(acct.cfg, func(o *ec2.Options) { o.Region = region })
	return runScanners(
		ctx,
		func(ctx context.Context) (int, int, error) {
			return scanEC2Networking(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) { return scanEC2VPN(ctx, client, acct, region, st, scanID) },
		func(ctx context.Context) (int, int, error) {
			return scanEC2ComputeMgmt(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanEC2Observability(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) { return scanEC2IPAM(ctx, client, acct, region, st, scanID) },
		func(ctx context.Context) (int, int, error) { return scanEC2TGW(ctx, client, acct, region, st, scanID) },
		func(ctx context.Context) (int, int, error) {
			return scanEC2TrafficMirror(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanEC2VerifiedAccess(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanEC2LocalGateway(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanEC2ClientVPN(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanEC2RouteServer(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanEC2IPAMResolver(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanEC2MiscExtra(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanEC2Inventory(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanEC2TGWExtra(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanEC2IPAMExtra(ctx, client, acct, region, st, scanID)
		},
	)
}

// runScanners executes scanner functions concurrently via errgroup, aggregating
// total/inserted counts. Each fn receives the errgroup-derived context.
func runScanners(ctx context.Context, fns ...func(context.Context) (int, int, error)) (int, int, error) {
	var t, n atomic.Int64
	g, gctx := errgroup.WithContext(ctx)
	for _, fn := range fns {
		g.Go(func() error {
			tt, nn, err := fn(gctx)
			t.Add(int64(tt))
			n.Add(int64(nn))
			return err
		})
	}
	// Block on Wait BEFORE loading the atomics — Go evaluates return-list
	// expressions left-to-right, so loading the atomics inline with g.Wait()
	// reads them while goroutines are still running and yields 0/0.
	err := g.Wait()
	return int(t.Load()), int(n.Load()), err
}

// ec2Pager is satisfied by every AWS SDK v2 EC2 paginator.
type ec2Pager[P any] interface {
	HasMorePages() bool
	NextPage(context.Context, ...func(*ec2.Options)) (P, error)
}

// ec2PageScan runs a paginated EC2 Describe call, converts each full page to
// a batch of resources via toResources, and upserts the batch. Access-denied
// errors are handled via skipIfAccessDenied. Returns total resources seen and
// count of newly inserted resources.
func ec2PageScan[P any](
	ctx context.Context,
	iamAction string,
	acct *account,
	region string,
	st *store.Store,
	pager ec2Pager[P],
	toResources func(P) []*store.Resource,
) (total, inserted int, err error) {
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, iamAction, acct.ID, region, err)
			}
			// Per-region op-availability gap: EC2 Describe* ops for features not
			// deployed in a region return UnsupportedOperation (e.g. VPC block
			// public access) or InvalidAction (e.g. Verified Access in regions
			// where it isn't offered). Both are permanent region facts, not
			// failures — silent-skip.
			if isAPIErrorCode(err, "UnsupportedOperation", "InvalidAction") {
				return total, inserted, nil
			}
			return total, inserted, fmt.Errorf("%s: %w", iamAction, err)
		}
		if batch := toResources(page); len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert %s: %w", iamAction, err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return
}

// ec2TagName extracts the "Name" tag value, returning nil if absent.
func ec2TagName(tags []ec2types.Tag) *string {
	for _, t := range tags {
		if sv(t.Key) == "Name" && t.Value != nil {
			return t.Value
		}
	}
	return nil
}
