package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCloudFormationStack, Service: "cloudformation", Upstream: "AWS::CloudFormation::Stack"})
	registerType(restype.Descriptor{Type: TypeCloudFormationStackSet, Service: "cloudformation", Upstream: "AWS::CloudFormation::StackSet"})
	registerType(restype.Descriptor{Type: TypeCloudFormationGeneratedTemplate, Service: "cloudformation", Upstream: "AWS::cloudformation::generatedtemplate", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudFormationResourceScan, Service: "cloudformation", Upstream: "AWS::cloudformation::resourcescan", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudFormationType, Service: "cloudformation", Upstream: "AWS::cloudformation::type", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudFormationTypeHook, Service: "cloudformation", Upstream: "AWS::cloudformation::typeHook", Leaf: true})
	registerService(serviceEntry{
		name: "aws:cloudformation",
		fn:   scanCloudFormation,
	})
}

// cloudformationAPI is the narrow set of CloudFormation operations called
// by the scanCloudFormation sub-phases.
type cloudformationAPI interface {
	ListStacks(context.Context, *cloudformation.ListStacksInput, ...func(*cloudformation.Options)) (*cloudformation.ListStacksOutput, error)
	ListStackResources(context.Context, *cloudformation.ListStackResourcesInput, ...func(*cloudformation.Options)) (*cloudformation.ListStackResourcesOutput, error)
	DescribeStacks(context.Context, *cloudformation.DescribeStacksInput, ...func(*cloudformation.Options)) (*cloudformation.DescribeStacksOutput, error)
	ListStackSets(context.Context, *cloudformation.ListStackSetsInput, ...func(*cloudformation.Options)) (*cloudformation.ListStackSetsOutput, error)
	ListStackInstances(context.Context, *cloudformation.ListStackInstancesInput, ...func(*cloudformation.Options)) (*cloudformation.ListStackInstancesOutput, error)
	DescribeStackSet(context.Context, *cloudformation.DescribeStackSetInput, ...func(*cloudformation.Options)) (*cloudformation.DescribeStackSetOutput, error)
	ListGeneratedTemplates(context.Context, *cloudformation.ListGeneratedTemplatesInput, ...func(*cloudformation.Options)) (*cloudformation.ListGeneratedTemplatesOutput, error)
	ListResourceScans(context.Context, *cloudformation.ListResourceScansInput, ...func(*cloudformation.Options)) (*cloudformation.ListResourceScansOutput, error)
	ListTypes(context.Context, *cloudformation.ListTypesInput, ...func(*cloudformation.Options)) (*cloudformation.ListTypesOutput, error)
}

// scanCloudFormation runs two phases per region:
//  1. Stacks — ListStacks (active statuses only), then ListStackResources
//     fan-out so each stack carries its full child resource list under
//     AttributesJSON.Resources for the resolver.
//  2. Stack-sets — ListStackSets + DescribeStackSet + ListStackInstances.
//     Only the management or delegated-admin account can list stack-sets;
//     every other account gets AccessDenied or ValidationError, both
//     tolerated and skipped without barring phase 1.
//
// Multi-phase pattern: phase 1 and phase 2 errors both abort. Phase 2's
// expected non-admin failures are caught before reaching the abort.
func scanCloudFormation(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := cloudformation.NewFromConfig(acct.cfg, func(o *cloudformation.Options) { o.Region = region })

	{
		t, i, ferr := scanCloudFormationStacks(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return 0, 0, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanCloudFormationStackSets(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanCloudFormationGeneratedTemplates(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanCloudFormationResourceScans(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanCloudFormationRegistryTypes(ctx, client, acct, region, st, scanID)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
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
	StackSet  *cfntypes.StackSet              `json:"StackSet"`
	Instances []cfntypes.StackInstanceSummary `json:"Instances"`
}

func scanCloudFormationStacks(ctx context.Context, client cloudformationAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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
					// or caller lacks permission for this stack — persist it
					// with an empty resource list rather than dropping it.
					resources = nil
				} else {
					return fmt.Errorf("cloudformation:ListStackResources %s: %w", sv(s.StackName), derr)
				}
			}
			// StackSummary doesn't carry Tags; DescribeStacks does. Best-effort:
			// on AccessDenied/ValidationError, fall through with nil tags
			// rather than failing the whole scan.
			var tags []cfntypes.Tag
			descOut, descErr := client.DescribeStacks(gctx, &cloudformation.DescribeStacksInput{StackName: s.StackId})
			if descErr == nil && len(descOut.Stacks) > 0 {
				tags = descOut.Stacks[0].Tags
			}
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCloudFormationStack,
				NativeID:       sv(s.StackId),
				Name:           s.StackName,
				Region:         &region,
				TagsJSON:       awsTagsJSON(tags),
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

func listAllStackResources(ctx context.Context, client cloudformationAPI, stackName string) ([]cfntypes.StackResourceSummary, error) {
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

func scanCloudFormationStackSets(ctx context.Context, client cloudformationAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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
				if !isAccessDenied(ierr) && !isStackValidationError(ierr) {
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

func listAllStackInstances(ctx context.Context, client cloudformationAPI, stackSetName string) ([]cfntypes.StackInstanceSummary, error) {
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
// "stack does not exist" (race between ListStacks and ListStackResources) and
// "StackSets is not active in this account" (non-admin account hitting the
// stack-set APIs) — both expected/tolerated, distinct from real validation
// bugs in disco's request shapes.
func isStackValidationError(err error) bool {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		return ae.ErrorCode() == "ValidationError"
	}
	return false
}

// scanCloudFormationGeneratedTemplates discovers IaC generator templates
// (ListGeneratedTemplates) — persistent templates produced from a resource
// scan.
func scanCloudFormationGeneratedTemplates(ctx context.Context, client cloudformationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := cloudformation.NewListGeneratedTemplatesPaginator(client, &cloudformation.ListGeneratedTemplatesInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "cloudformation:ListGeneratedTemplates", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("cloudformation:ListGeneratedTemplates: %w", err)
		}
		for _, s := range out.Summaries {
			id := sv(s.GeneratedTemplateId)
			if id == "" {
				continue
			}
			status := string(s.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloudFormationGeneratedTemplate, NativeID: id,
				Name: s.GeneratedTemplateName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), CreatedAt: tp(s.CreationTime), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cloudformation generated-templates")
}

// scanCloudFormationResourceScans discovers account resource scans
// (ListResourceScans) — inventory scans the IaC generator runs over existing
// resources.
func scanCloudFormationResourceScans(ctx context.Context, client cloudformationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := cloudformation.NewListResourceScansPaginator(client, &cloudformation.ListResourceScansInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "cloudformation:ListResourceScans", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("cloudformation:ListResourceScans: %w", err)
		}
		for _, s := range out.ResourceScanSummaries {
			id := sv(s.ResourceScanId)
			if id == "" {
				continue
			}
			status := string(s.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloudFormationResourceScan, NativeID: id,
				Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), CreatedAt: tp(s.StartTime), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cloudformation resource-scans")
}

// scanCloudFormationRegistryTypes discovers the account's registered CFN
// registry types (ListTypes, Visibility=PRIVATE — the account's own private
// types plus activated public ones; PUBLIC would pull the entire global
// catalogue). RESOURCE/MODULE types emit as aws:cloudformation:type, HOOK types
// as aws:cloudformation:type-hook.
func scanCloudFormationRegistryTypes(ctx context.Context, client cloudformationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := cloudformation.NewListTypesPaginator(client, &cloudformation.ListTypesInput{
		Visibility: cfntypes.VisibilityPrivate,
	})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "cloudformation:ListTypes", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("cloudformation:ListTypes: %w", err)
		}
		for _, s := range out.TypeSummaries {
			arn := sv(s.TypeArn)
			if arn == "" {
				continue
			}
			rtype := TypeCloudFormationType
			if s.Type == cfntypes.RegistryTypeHook {
				rtype = TypeCloudFormationTypeHook
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: rtype, NativeID: arn,
				Name: s.TypeName, Region: &region,
				AttributesJSON: mustJSON(s), CreatedAt: tp(s.LastUpdated), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cloudformation registry-types")
}
