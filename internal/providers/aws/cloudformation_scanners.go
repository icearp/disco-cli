package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/smithy-go"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerService(serviceEntry{name: "aws:cloudformation", fn: scanCloudFormation})
}

// scanCloudFormation runs two phases per region:
//  1. Stacks — ListStacks (active statuses only), then ListStackResources
//     fan-out so each stack carries its full child resource list under
//     AttributesJSON.Resources for the resolver to walk.
//  2. Stack-sets — ListStackSets + DescribeStackSet + ListStackInstances.
//     Only the management or delegated-admin account can list stack-sets;
//     in every other account the API returns AccessDenied or
//     ValidationError, both tolerated and skipped without barring phase 1.
//
// Multi-phase pattern: phase 1 errors abort, phase 2 errors abort. Phase
// 2's expected non-admin failures are caught before reaching the abort.
func scanCloudFormation(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := cloudformation.NewFromConfig(acct.cfg, func(o *cloudformation.Options) { o.Region = region })

	if t, i, ferr := scanCloudFormationStacks(ctx, client, acct, region, st, scanID); ferr != nil {
		return 0, 0, ferr
	} else {
		total += t
		inserted += i
	}

	if t, i, ferr := scanCloudFormationStackSets(ctx, client, acct, region, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	return total, inserted, nil
}

// activeStackStatuses is every StackStatus except DELETE_COMPLETE. Deleted
// stacks have no live resources to walk; including them would explode the
// scan with 90 days of historic skeletons returning empty resource lists.
var activeStackStatuses = []cfntypes.StackStatus{
	cfntypes.StackStatusCreateInProgress,
	cfntypes.StackStatusCreateFailed,
	cfntypes.StackStatusCreateComplete,
	cfntypes.StackStatusRollbackInProgress,
	cfntypes.StackStatusRollbackFailed,
	cfntypes.StackStatusRollbackComplete,
	cfntypes.StackStatusDeleteInProgress,
	cfntypes.StackStatusDeleteFailed,
	cfntypes.StackStatusUpdateInProgress,
	cfntypes.StackStatusUpdateCompleteCleanupInProgress,
	cfntypes.StackStatusUpdateComplete,
	cfntypes.StackStatusUpdateFailed,
	cfntypes.StackStatusUpdateRollbackInProgress,
	cfntypes.StackStatusUpdateRollbackFailed,
	cfntypes.StackStatusUpdateRollbackCompleteCleanupInProgress,
	cfntypes.StackStatusUpdateRollbackComplete,
	cfntypes.StackStatusReviewInProgress,
	cfntypes.StackStatusImportInProgress,
	cfntypes.StackStatusImportComplete,
	cfntypes.StackStatusImportRollbackInProgress,
	cfntypes.StackStatusImportRollbackFailed,
	cfntypes.StackStatusImportRollbackComplete,
}

// stackWithResources is the wrapped attrs shape persisted for each stack.
// Resolver reads Resources[] without re-issuing ListStackResources.
type stackWithResources struct {
	Stack     *cfntypes.StackSummary          `json:"Stack"`
	Resources []cfntypes.StackResourceSummary `json:"Resources"`
}

// stackSetWithInstances wraps DescribeStackSet output plus the per-instance
// ListStackInstances results so the resolver can walk deployed stacks.
type stackSetWithInstances struct {
	StackSet  *cfntypes.StackSet             `json:"StackSet"`
	Instances []cfntypes.StackInstanceSummary `json:"Instances"`
}

func scanCloudFormationStacks(ctx context.Context, client *cloudformation.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudformation.NewListStacksPaginator(client, &cloudformation.ListStacksInput{
		StackStatusFilter: activeStackStatuses,
	})
	var summaries []cfntypes.StackSummary
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "cloudformation:ListStacks", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("cloudformation:ListStacks: %w", perr)
		}
		summaries = append(summaries, out.StackSummaries...)
	}
	if len(summaries) == 0 {
		return 0, 0, nil
	}

	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, s := range summaries {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			resources, derr := listAllStackResources(gctx, client, sv(s.StackId))
			if derr != nil {
				if isAccessDenied(derr) || isStackValidationError(derr) {
					// Stack went away between ListStacks and ListStackResources,
					// or caller lacks permission for this specific stack. Persist
					// the stack with empty resource list rather than dropping it.
					resources = nil
				} else {
					return fmt.Errorf("cloudformation:ListStackResources %s: %w", sv(s.StackName), derr)
				}
			}
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCloudFormationStack,
				NativeID:       sv(s.StackId),
				Name:           s.StackName,
				Region:         &region,
				AttributesJSON: mustJSON(stackWithResources{Stack: &s, Resources: resources}),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) > 0 {
		n, uerr := st.UpsertResources(batch)
		if uerr != nil {
			return 0, 0, fmt.Errorf("upsert cloudformation stacks: %w", uerr)
		}
		total = len(batch)
		inserted = n
	}
	return total, inserted, nil
}

func listAllStackResources(ctx context.Context, client *cloudformation.Client, stackName string) ([]cfntypes.StackResourceSummary, error) {
	pager := cloudformation.NewListStackResourcesPaginator(client, &cloudformation.ListStackResourcesInput{StackName: &stackName})
	var out []cfntypes.StackResourceSummary
	for pager.HasMorePages() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			return nil, perr
		}
		out = append(out, page.StackResourceSummaries...)
	}
	return out, nil
}

func scanCloudFormationStackSets(ctx context.Context, client *cloudformation.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := cloudformation.NewListStackSetsPaginator(client, &cloudformation.ListStackSetsInput{
		Status: cfntypes.StackSetStatusActive,
	})
	var summaries []cfntypes.StackSetSummary
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			// Non-admin accounts return AccessDenied or ValidationError
			// ("StackSets is not active in this account"). Tolerate both.
			if isAccessDenied(perr) || isStackValidationError(perr) {
				_ = skipIfAccessDenied(st, "cloudformation:ListStackSets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("cloudformation:ListStackSets: %w", perr)
		}
		summaries = append(summaries, out.Summaries...)
	}
	if len(summaries) == 0 {
		return 0, 0, nil
	}

	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, s := range summaries {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			descOut, derr := client.DescribeStackSet(gctx, &cloudformation.DescribeStackSetInput{StackSetName: s.StackSetName})
			if derr != nil {
				if isAccessDenied(derr) || isStackValidationError(derr) {
					return nil
				}
				return fmt.Errorf("cloudformation:DescribeStackSet %s: %w", sv(s.StackSetName), derr)
			}
			if descOut.StackSet == nil {
				return nil
			}
			instances, ierr := listAllStackInstances(gctx, client, sv(s.StackSetName))
			if ierr != nil {
				if !(isAccessDenied(ierr) || isStackValidationError(ierr)) {
					return fmt.Errorf("cloudformation:ListStackInstances %s: %w", sv(s.StackSetName), ierr)
				}
				instances = nil
			}
			ss := descOut.StackSet
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCloudFormationStackSet,
				NativeID:       sv(ss.StackSetARN),
				Name:           ss.StackSetName,
				Region:         &region,
				AttributesJSON: mustJSON(stackSetWithInstances{StackSet: ss, Instances: instances}),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) > 0 {
		n, uerr := st.UpsertResources(batch)
		if uerr != nil {
			return 0, 0, fmt.Errorf("upsert cloudformation stack-sets: %w", uerr)
		}
		total = len(batch)
		inserted = n
	}
	return total, inserted, nil
}

func listAllStackInstances(ctx context.Context, client *cloudformation.Client, stackSetName string) ([]cfntypes.StackInstanceSummary, error) {
	pager := cloudformation.NewListStackInstancesPaginator(client, &cloudformation.ListStackInstancesInput{StackSetName: &stackSetName})
	var out []cfntypes.StackInstanceSummary
	for pager.HasMorePages() {
		page, perr := pager.NextPage(ctx)
		if perr != nil {
			return nil, perr
		}
		out = append(out, page.Summaries...)
	}
	return out, nil
}

// isStackValidationError matches CloudFormation's catch-all error code for
// "stack does not exist" (race during ListStacks→ListStackResources) and
// "StackSets is not active in this account" (non-admin account hitting the
// stack-set APIs). Both are expected and tolerated, distinct from real
// validation bugs in disco's request shapes.
func isStackValidationError(err error) bool {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		return ae.ErrorCode() == "ValidationError"
	}
	return false
}
