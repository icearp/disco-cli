package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCloudFormationStack, Service: "cloudformation"})
	registerType(restype.Descriptor{Type: TypeCloudFormationStackSet, Service: "cloudformation"})
	registerType(restype.Descriptor{Type: TypeCloudFormationGeneratedTemplate, Service: "cloudformation", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeCloudFormationResourceScan, Service: "cloudformation", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeCloudFormationType, Service: "cloudformation", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeCloudFormationTypeHook, Service: "cloudformation"})
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

const (
	cfnListStacksOp             = "cloudformation:ListStacks"
	cfnListGeneratedTemplatesOp = "cloudformation:ListGeneratedTemplates"
	cfnListResourceScansOp      = "cloudformation:ListResourceScans"
	cfnListTypesOp              = "cloudformation:ListTypes"
	cfnListStackSetsOp          = "cloudformation:ListStackSets"
	cfnDescribeStackSetOp       = "cloudformation:DescribeStackSet"
	cfnListStackInstancesOp     = "cloudformation:ListStackInstances"
	cfnListStackResourcesOp     = "cloudformation:ListStackResources"
	cfnDescribeStacksOp         = "cloudformation:DescribeStacks"
	// cfnPageCap is the MaxResults maximum of ListStackSets,
	// ListStackInstances, ListGeneratedTemplates and ListTypes (the model's
	// range 1-100) and of ListResourceScans (its doc: "The maximum value is
	// 100"; the model sets no range).
	cfnPageCap = 100
)

// scanCloudFormation runs five independent phases per region: stacks (each
// with its ListStackResources list under AttributesJSON.Resources for the
// resolver), stack sets (DescribeStackSet + ListStackInstances), IaC
// generator templates, resource scans and private registry types. Each phase
// runs whatever another returns, except after a store write failure; their
// errors are joined by phaseErrs.
func scanCloudFormation(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := cloudformation.NewFromConfig(acct.cfg, func(o *cloudformation.Options) { o.Region = region })
	var phases phaseTotals
	for _, phase := range []struct {
		op string
		fn func(context.Context, cloudformationAPI, *account, string, *store.Store, string) (int, int, error)
	}{
		{cfnListStacksOp, scanCloudFormationStacks},
		{cfnListStackSetsOp, scanCloudFormationStackSets},
		{cfnListGeneratedTemplatesOp, scanCloudFormationGeneratedTemplates},
		{cfnListResourceScansOp, scanCloudFormationResourceScans},
		{cfnListTypesOp, scanCloudFormationRegistryTypes},
	} {
		t, i, err := phase.fn(ctx, client, acct, region, st, scanID)
		if phases.addPhase(phase.op, t, i, err) {
			break
		}
	}
	return phases.result(st, acct, region)
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

// scanCloudFormationStacks stores every stack ListStacks lists in an
// activeStackStatuses status, the stacks read before a failed page included,
// each with its resource summaries and the Tags DescribeStacks returns (the
// summary carries none). No stack's failure drops it or cancels another: a
// denied ListStackResources or DescribeStacks stores the stack without its
// resources or tags and is warned about once per op; any other error stores
// the stack with what was read and is returned after the write. Neither op
// models an error code, so a stack deleted since it was listed is an error.
// A stack left unread because ctx ended is stored from its summary alone, and
// the phase returns ctx's error.
func scanCloudFormationStacks(ctx context.Context, client cloudformationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	summaries, listErr := readTokenPages(ctx, func(ctx context.Context, token *string) ([]cfntypes.StackSummary, *string, error) {
		out, err := client.ListStacks(ctx, &cloudformation.ListStacksInput{StackStatusFilter: activeStackStatuses, NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		return out.StackSummaries, out.NextToken, nil
	})
	var (
		skips                           notStoredSkips
		stacks                          []cfntypes.StackSummary
		resourcesDenied, describeDenied skipTally
		rows                            []*store.Resource
		errs                            []error
	)
	for _, s := range summaries {
		if sv(s.StackId) != "" {
			stacks = append(stacks, s)
		} else {
			skips.skipped("stacks have an empty StackId", cmp.Or(sv(s.StackName), "one without a StackName"))
		}
	}
	reads, stopped := readEachKey(ctx, stacks, func(ctx context.Context, s cfntypes.StackSummary) cfnStackRead {
		return readCloudFormationStack(ctx, client, acct, region, scanID, s)
	})
	if stopped != nil {
		errs = append(errs, fmt.Errorf("%s: stacks: %w", cfnListStackResourcesOp, stopped))
	}
	// A stack not read once ctx ended is still stored, from its summary.
	for _, s := range stacks[len(reads):] {
		rows = append(rows, cloudFormationStackRow(acct, region, scanID, s, nil, nil))
	}
	for i, r := range reads {
		id := sv(stacks[i].StackId)
		rows = append(rows, r.row)
		if r.resourcesDenied != nil {
			resourcesDenied.skipped(id, r.resourcesDenied)
		}
		if r.describeDenied != nil {
			describeDenied.skipped(id, r.describeDenied)
		}
		if r.err != nil {
			errs = append(errs, r.err)
		}
	}
	return storeReadRows(st, acct, region, cfnListStacksOp, rows, listErr, func() {
		resourcesDenied.warnDeniedIn(st, cfnListStackResourcesOp, acct, region, "stack(s) stored without their resources were")
		describeDenied.warnDeniedIn(st, cfnDescribeStacksOp, acct, region, "stack(s) stored without their tags were")
		skips.warn(st, cfnListStacksOp, acct, region)
	}, errs...)
}

// cfnStackRead is one stack's read: its row, always built, the error of each
// denied call, whose part the row lacks, and any other error, whose part
// holds what was read before it.
type cfnStackRead struct {
	row                             *store.Resource
	resourcesDenied, describeDenied error
	err                             error
}

func readCloudFormationStack(ctx context.Context, client cloudformationAPI, acct *account, region, scanID string, s cfntypes.StackSummary) cfnStackRead {
	id := sv(s.StackId)
	var (
		read cfnStackRead
		errs []error
	)
	resources, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]cfntypes.StackResourceSummary, *string, error) {
		out, err := client.ListStackResources(ctx, &cloudformation.ListStackResourcesInput{StackName: &id, NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		return out.StackResourceSummaries, out.NextToken, nil
	})
	switch {
	case err == nil:
	case isAccessDenied(err):
		read.resourcesDenied = err
	default:
		errs = append(errs, fmt.Errorf("%s %s: %w", cfnListStackResourcesOp, id, err))
	}
	var tags []cfntypes.Tag
	out, err := client.DescribeStacks(ctx, &cloudformation.DescribeStacksInput{StackName: &id})
	switch {
	case err == nil && len(out.Stacks) > 0:
		tags = out.Stacks[0].Tags
	case err == nil:
		errs = append(errs, fmt.Errorf("%s %s: %w", cfnDescribeStacksOp, id, errCFNStackNotDescribed))
	case isAccessDenied(err):
		read.describeDenied = err
	default:
		errs = append(errs, fmt.Errorf("%s %s: %w", cfnDescribeStacksOp, id, err))
	}
	read.row = cloudFormationStackRow(acct, region, scanID, s, resources, tags)
	read.err = errors.Join(errs...)
	return read
}

// cloudFormationStackRow is the row of the stack s summarises, with the
// resources and tags read for it.
func cloudFormationStackRow(acct *account, region, scanID string, s cfntypes.StackSummary, resources []cfntypes.StackResourceSummary, tags []cfntypes.Tag) *store.Resource {
	return &store.Resource{
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
}

// errCFNStackNotDescribed marks a DescribeStacks answer that holds no stack
// for the stack id asked about, so the stack was stored without its tags.
var errCFNStackNotDescribed = errors.New("the answer held no stack, so the stack was stored without its tags; rescan, and report it if it recurs")

// scanCloudFormationStackSets stores each ACTIVE stack set listed (CallAs
// unset: the account acting as itself, not as a delegated administrator) as
// its DescribeStackSet body, Tags included, wrapped with its stack instance
// summaries. The sets read before a failed list page are kept. Per set: a set
// the service no longer finds (StackSetNotFoundException, the code both
// per-set ops model) was deleted since it was listed and is skipped; a denied
// DescribeStackSet drops the set, and a denied ListStackInstances keeps it
// with the instances read, each warned about once per op; any other
// DescribeStackSet error drops the set, and any other ListStackInstances
// error keeps it with the instances read, both returned after the other sets
// are stored.
func scanCloudFormationStackSets(ctx context.Context, client cloudformationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	summaries, listErr := readTokenPages(ctx, func(ctx context.Context, token *string) ([]cfntypes.StackSetSummary, *string, error) {
		out, err := client.ListStackSets(ctx, &cloudformation.ListStackSetsInput{
			Status:     cfntypes.StackSetStatusActive,
			MaxResults: sdkaws.Int32(cfnPageCap),
			NextToken:  token,
		})
		if err != nil {
			return nil, nil, err
		}
		return out.Summaries, out.NextToken, nil
	})
	var (
		listSkips, describeSkips        notStoredSkips
		names                           []string
		describeDenied, instancesDenied skipTally
		rows                            []*store.Resource
		errs                            []error
	)
	for _, s := range summaries {
		if name := sv(s.StackSetName); name != "" {
			names = append(names, name)
		} else {
			listSkips.skipped("stack sets have an empty StackSetName", cmp.Or(sv(s.StackSetId), "one without a StackSetId"))
		}
	}
	reads, stopped := readEachKey(ctx, names, func(ctx context.Context, name string) cfnStackSetRead {
		return readCloudFormationStackSet(ctx, client, acct, region, scanID, name)
	})
	if stopped != nil {
		errs = append(errs, fmt.Errorf("%s: stack sets: %w", cfnDescribeStackSetOp, stopped))
	}
	for i, r := range reads {
		name := names[i]
		if r.row != nil {
			rows = append(rows, r.row)
		}
		if r.describeDenied != nil {
			describeDenied.skipped(name, r.describeDenied)
		}
		if r.instancesDenied != nil {
			instancesDenied.skipped(name, r.instancesDenied)
		}
		if r.notStored != "" {
			describeSkips.skipped(r.notStored, name)
		}
		if r.err != nil {
			errs = append(errs, r.err)
		}
	}
	return storeReadRows(st, acct, region, cfnListStackSetsOp, rows, listErr, func() {
		describeDenied.warnDeniedIn(st, cfnDescribeStackSetOp, acct, region, "stack set(s) not stored were")
		instancesDenied.warnDeniedIn(st, cfnListStackInstancesOp, acct, region, "stack set(s) stored without all their instances were")
		listSkips.warn(st, cfnListStackSetsOp, acct, region)
		describeSkips.warn(st, cfnDescribeStackSetOp, acct, region)
	}, errs...)
}

// cfnStackSetRead is one set's read: its row (nil when the set is not
// stored), the error of each denied call, why a described set cannot be
// stored, and any other error.
type cfnStackSetRead struct {
	row                             *store.Resource
	describeDenied, instancesDenied error
	notStored                       string
	err                             error
}

// readCloudFormationStackSet reads one set. It builds no row for a set
// deleted since it was listed, a denied or failed DescribeStackSet, or a
// described set that cannot be stored; a failed ListStackInstances keeps the
// row with the instances read.
func readCloudFormationStackSet(ctx context.Context, client cloudformationAPI, acct *account, region, scanID, name string) cfnStackSetRead {
	out, err := client.DescribeStackSet(ctx, &cloudformation.DescribeStackSetInput{StackSetName: &name})
	switch {
	case err == nil:
	case isAPIErrorCode(err, "StackSetNotFoundException"):
		return cfnStackSetRead{}
	case isAccessDenied(err):
		return cfnStackSetRead{describeDenied: err}
	default:
		return cfnStackSetRead{err: fmt.Errorf("%s %s: %w", cfnDescribeStackSetOp, name, err)}
	}
	ss := out.StackSet
	if ss == nil {
		return cfnStackSetRead{notStored: "stack sets were described without a StackSet"}
	}
	arn := sv(ss.StackSetARN)
	if arn == "" {
		return cfnStackSetRead{notStored: "stack sets were described without a StackSetARN"}
	}
	var read cfnStackSetRead
	instances, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]cfntypes.StackInstanceSummary, *string, error) {
		out, err := client.ListStackInstances(ctx, &cloudformation.ListStackInstancesInput{
			StackSetName: &name,
			MaxResults:   sdkaws.Int32(cfnPageCap),
			NextToken:    token,
		})
		if err != nil {
			return nil, nil, err
		}
		return out.Summaries, out.NextToken, nil
	})
	switch {
	case err == nil:
	case isAPIErrorCode(err, "StackSetNotFoundException"):
		return cfnStackSetRead{}
	case isAccessDenied(err):
		read.instancesDenied = err
	default:
		read.err = fmt.Errorf("%s %s: %w", cfnListStackInstancesOp, name, err)
	}
	read.row = &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeCloudFormationStackSet,
		NativeID:       arn,
		Name:           ss.StackSetName,
		Region:         &region,
		AttributesJSON: mustJSON(stackSetWithInstances{StackSet: ss, Instances: instances}),
		TagsJSON:       awsTagsJSON(ss.Tags),
		DiscoveredBy:   scanID,
	}
	return read
}

// scanCloudFormationGeneratedTemplates stores the IaC generator templates
// (ListGeneratedTemplates): persistent templates produced from a resource
// scan.
func scanCloudFormationGeneratedTemplates(ctx context.Context, client cloudformationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, cfnListGeneratedTemplatesOp,
		func(ctx context.Context, token *string) ([]cfntypes.TemplateSummary, *string, error) {
			out, err := client.ListGeneratedTemplates(ctx, &cloudformation.ListGeneratedTemplatesInput{MaxResults: sdkaws.Int32(cfnPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Summaries, out.NextToken, nil
		},
		func(s cfntypes.TemplateSummary) (*store.Resource, string, string) {
			id := sv(s.GeneratedTemplateId)
			if id == "" {
				return nil, "generated templates have an empty GeneratedTemplateId", cmp.Or(sv(s.GeneratedTemplateName), "one without a GeneratedTemplateName")
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloudFormationGeneratedTemplate, NativeID: id,
				Name: s.GeneratedTemplateName, Region: &region, Status: nonEmptyPtr(string(s.Status)),
				AttributesJSON: mustJSON(s), CreatedAt: tp(s.CreationTime), DiscoveredBy: scanID,
			}, "", ""
		})
}

// scanCloudFormationResourceScans stores the account's resource scans
// (ListResourceScans, ScanTypeFilter unset so every scan type is listed):
// inventory scans the IaC generator runs over existing resources.
func scanCloudFormationResourceScans(ctx context.Context, client cloudformationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, cfnListResourceScansOp,
		func(ctx context.Context, token *string) ([]cfntypes.ResourceScanSummary, *string, error) {
			out, err := client.ListResourceScans(ctx, &cloudformation.ListResourceScansInput{MaxResults: sdkaws.Int32(cfnPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ResourceScanSummaries, out.NextToken, nil
		},
		func(s cfntypes.ResourceScanSummary) (*store.Resource, string, string) {
			id := sv(s.ResourceScanId)
			if id == "" {
				return nil, "resource scans have an empty ResourceScanId", "one without a ResourceScanId"
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloudFormationResourceScan, NativeID: id,
				Region: &region, Status: nonEmptyPtr(string(s.Status)),
				AttributesJSON: mustJSON(s), CreatedAt: tp(s.StartTime), DiscoveredBy: scanID,
			}, "", ""
		})
}

// scanCloudFormationRegistryTypes stores the account's registered CFN
// registry types (ListTypes, Visibility=PRIVATE — the account's own private
// types plus activated public ones; PUBLIC would pull the entire global
// catalogue). RESOURCE/MODULE types emit as aws:cloudformation:type, HOOK types
// as aws:cloudformation:type-hook.
func scanCloudFormationRegistryTypes(ctx context.Context, client cloudformationAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, cfnListTypesOp,
		func(ctx context.Context, token *string) ([]cfntypes.TypeSummary, *string, error) {
			out, err := client.ListTypes(ctx, &cloudformation.ListTypesInput{
				Visibility: cfntypes.VisibilityPrivate,
				MaxResults: sdkaws.Int32(cfnPageCap),
				NextToken:  token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.TypeSummaries, out.NextToken, nil
		},
		func(s cfntypes.TypeSummary) (*store.Resource, string, string) {
			arn := sv(s.TypeArn)
			if arn == "" {
				return nil, "registry types have an empty TypeArn", cmp.Or(sv(s.TypeName), "one without a TypeName")
			}
			rtype := TypeCloudFormationType
			if s.Type == cfntypes.RegistryTypeHook {
				rtype = TypeCloudFormationTypeHook
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: rtype, NativeID: arn,
				Name: s.TypeName, Region: &region,
				AttributesJSON: mustJSON(s), CreatedAt: tp(s.LastUpdated), DiscoveredBy: scanID,
			}, "", ""
		})
}
