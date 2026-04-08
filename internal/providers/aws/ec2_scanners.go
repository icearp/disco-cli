package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "aws:ec2", fn: scanEC2}) }

// scanEC2 discovers all EC2 resource types in one region by running all
// category scanners in parallel. Each category scanner fans out to its own
// sub-scanners via an internal errgroup.
func scanEC2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) error {
	client := ec2.NewFromConfig(acct.cfg, func(o *ec2.Options) { o.Region = region })
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return scanEC2Networking(gctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanEC2VPN(gctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanEC2ComputeMgmt(gctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanEC2Observability(gctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanEC2IPAM(gctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanEC2TGWExt(gctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanEC2TrafficMirror(gctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanEC2VerifiedAccess(gctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanEC2LocalGateway(gctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanEC2ClientVPN(gctx, client, acct, region, st, scanID) })
	g.Go(func() error { return scanEC2Ext(gctx, client, acct, region, st, scanID) })
	return g.Wait()
}

// ec2Pager is satisfied by every AWS SDK v2 EC2 paginator.
type ec2Pager[P any] interface {
	HasMorePages() bool
	NextPage(context.Context, ...func(*ec2.Options)) (P, error)
}

// ec2PageScan runs a paginated EC2 Describe call, converts each full page to
// a batch of resources via toResources, and upserts the batch. Access-denied
// errors are handled via skipIfAccessDenied.
func ec2PageScan[P any](
	ctx context.Context,
	iamAction string,
	acct *account,
	region string,
	st *store.Store,
	pager ec2Pager[P],
	toResources func(P) []*store.Resource,
) error {
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied(iamAction, acct.ID, region, err)
			}
			return fmt.Errorf("%s: %w", iamAction, err)
		}
		if batch := toResources(page); len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert %s: %w", iamAction, err)
			}
		}
	}
	return nil
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
