package aws

import (
	"context"
	"fmt"
	"sync/atomic"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "aws:ec2", fn: scanEC2}) }

// scanEC2 discovers all EC2 resource types in one region by running all
// category scanners in parallel. Each category scanner fans out to its own
// sub-scanners via an internal errgroup.
func scanEC2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := ec2.NewFromConfig(acct.cfg, func(o *ec2.Options) { o.Region = region })
	var t, n atomic.Int64
	add := func(tt, nn int) { t.Add(int64(tt)); n.Add(int64(nn)) }
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		tt, nn, e := scanEC2Networking(gctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error { tt, nn, e := scanEC2VPN(gctx, client, acct, region, st, scanID); add(tt, nn); return e })
	g.Go(func() error {
		tt, nn, e := scanEC2ComputeMgmt(gctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanEC2Observability(gctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error { tt, nn, e := scanEC2IPAM(gctx, client, acct, region, st, scanID); add(tt, nn); return e })
	g.Go(func() error { tt, nn, e := scanEC2TGW(gctx, client, acct, region, st, scanID); add(tt, nn); return e })
	g.Go(func() error {
		tt, nn, e := scanEC2TrafficMirror(gctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanEC2VerifiedAccess(gctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanEC2LocalGateway(gctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	g.Go(func() error {
		tt, nn, e := scanEC2ClientVPN(gctx, client, acct, region, st, scanID)
		add(tt, nn)
		return e
	})
	return int(t.Load()), int(n.Load()), g.Wait()
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
				return total, inserted, skipIfAccessDenied(iamAction, acct.ID, region, err)
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
