package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

func init() {
	// Types are declared per category file via registerType — scanEC2
	// itself upserts nothing, just fans out to category scanners (compute_mgmt,
	// networking, ipam, tgw, …).
	registerService(serviceEntry{name: "aws:ec2", fn: scanEC2})
}

// scanEC2 discovers all EC2 resource types in one region by running all
// category scanners in parallel; each fans out to its own sub-scanners via
// an internal errgroup. The IPAM extended phases and then the late phases
// (scanEC2Late) run after them, whatever they return: runScanners cancels
// every category on its first error, so a failure there would otherwise cost
// the older categories their rows. Every side has its transient error demoted
// to a warning before the join.
func scanEC2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := ec2.NewFromConfig(acct.cfg, func(o *ec2.Options) { o.Region = region })
	var (
		homeIPAM     bool
		ipamPolicies []childParent
	)
	total, inserted, err := runScanners(
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
		func(ctx context.Context) (int, int, error) {
			t, n, home, err := scanEC2IPAM(ctx, client, acct, region, st, scanID)
			homeIPAM = home
			return t, n, err
		},
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
			return scanIPAMPrefixListResolverTargets(ctx, client, acct, region, st, scanID)
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
			t, n, policies, err := scanEC2IPAMExtra(ctx, client, acct, region, st, scanID)
			ipamPolicies = policies
			return t, n, err
		},
		func(ctx context.Context) (int, int, error) {
			return scanEC2LocalGatewayExtra(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanEC2ComputeExtra(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanEC2Secondary(ctx, client, acct, region, st, scanID)
		},
		func(ctx context.Context) (int, int, error) {
			return scanEC2NetworkExtra(ctx, client, acct, region, st, scanID)
		},
	)
	t, n, extErr := scanEC2IPAMExtended(ctx, client, acct, region, st, scanID, homeIPAM, ipamPolicies)
	lt, ln, lateErr := scanEC2Late(ctx, client, acct, region, st, scanID)
	return total + t + lt, inserted + n + ln, errors.Join(demoteTransient(st, "ec2", acct, region, err), extErr, lateErr)
}

// runScanners executes scanner functions concurrently via errgroup, aggregating
// total/inserted counts. Each fn receives the errgroup-derived context. An fn
// whose listing ended incomplete (continuesPastIncomplete) has stored what it
// read and does not cancel the others; its error is returned after they
// finish. Any other error cancels them.
func runScanners(ctx context.Context, fns ...func(context.Context) (int, int, error)) (int, int, error) {
	var (
		t, n       atomic.Int64
		incomplete incompleteLists
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, fn := range fns {
		g.Go(func() error {
			tt, nn, err := fn(gctx)
			t.Add(int64(tt))
			n.Add(int64(nn))
			if continuesPastIncomplete(err) {
				incomplete.record(err)
				return nil
			}
			return err
		})
	}
	// Wait before loading the atomics — Go evaluates return-list expressions
	// left-to-right, so loading them inline with g.Wait() would read them while
	// goroutines are still running and yield 0/0.
	err := g.Wait()
	return int(t.Load()), int(n.Load()), errors.Join(err, incomplete.err())
}

// ec2Pager is satisfied by every AWS SDK v2 EC2 paginator.
type ec2Pager[P any] interface {
	HasMorePages() bool
	NextPage(context.Context, ...func(*ec2.Options)) (P, error)
}

// ec2PageScan runs a paginated EC2 Describe call, converts each page to a
// batch of resources via toResources, and upserts the batch. Access-denied
// errors route through skipIfAccessDenied. Returns total resources seen and
// count newly inserted.
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
			if isEC2RegionFeatureGap(err) {
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

// isEC2RegionFeatureGap reports a per-region feature gap: Describe* ops for
// features not deployed in a region return UnsupportedOperation (e.g. VPC block
// public access), InvalidAction (e.g. Verified Access in unsupported regions),
// or the bare Unsupported code (DescribeCapacityBlocks) — permanent region
// facts, not failures, so callers silent-skip.
func isEC2RegionFeatureGap(err error) bool {
	return isAPIErrorCode(err, "UnsupportedOperation", "InvalidAction", "Unsupported")
}

// errEC2ParentNativeID marks a stored parent whose NativeID lacks the
// ec2ARN `{kind}/{id}` shape its own scanner writes.
var errEC2ParentNativeID = errors.New("ec2 parent NativeID is not an ec2ARN of its kind")

// ec2StoredParents returns the parentType rows this scan stored in region,
// keyed by the id ec2ARN put after kind, except those whose Status is one of
// gone. A late phase reads its parents here because the category that lists
// them runs inside runScanners and hands nothing back.
func ec2StoredParents(st *store.Store, acct *account, region, scanID, parentType, kind string, gone ...string) ([]childParent, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{parentType},
		Regions: []string{region}, SeenBy: scanID, Limit: util.AllResources,
	})
	if err != nil {
		return nil, fmt.Errorf("list %s parents: %w", parentType, err)
	}
	var parents []childParent
	for _, r := range rows {
		_, id, ok := strings.Cut(r.NativeID, ":"+kind+"/")
		if !ok || id == "" {
			return nil, fmt.Errorf("%w: %s %q", errEC2ParentNativeID, parentType, r.NativeID)
		}
		if slices.Contains(gone, sv(r.Status)) {
			continue
		}
		parents = append(parents, childParent{id: id, arn: r.NativeID})
	}
	return parents, nil
}

// ec2IncorrectState is the code EC2 returns for an action its target's state
// does not allow. Whether a pending or deleting parent answers a child listing
// with it is unverified, so a skip on it warns.
const ec2IncorrectState = "IncorrectState"

// neverSkip is the isSkip of a fan-out whose every skip is a warned code.
func neverSkip(error) bool { return false }

// scanEC2Children stores, under each parent, the rows list returns for it. A
// parent failing with one of codes is skipped, with one warning per code; a
// denial or a transient failure is skipped with one warning; anything else
// fails the phase. Rows other parents stored stay.
func scanEC2Children(ctx context.Context, st *store.Store, acct *account, region, op string, parents []childParent,
	what string, codes []skipCode, list func(context.Context, childParent) ([]*store.Resource, error),
) (int, int, error) {
	return childFanOutStoredSkipCodes(ctx, st, acct, region, op, parents, neverSkip, byParentID, what, codes,
		func(ctx context.Context, p childParent) (int, int, error) {
			rows, err := list(ctx, p)
			if err != nil {
				return 0, 0, err
			}
			parentID := store.ResourceID("aws", acct.ID, p.arn)
			children := make([]containedChild, len(rows))
			for i, r := range rows {
				children[i] = containedChild{row: r, parentID: parentID}
			}
			return upsertContained(st, op, children)
		})
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
