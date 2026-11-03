package aws

import (
	"context"
	"errors"
	"fmt"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/entityresolution"
	ertypes "github.com/aws/aws-sdk-go-v2/service/entityresolution/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEntityResolutionIDMappingWorkflow, Service: "entityresolution"})
	registerType(restype.Descriptor{Type: TypeEntityResolutionIDNamespace, Service: "entityresolution"})
	registerType(restype.Descriptor{Type: TypeEntityResolutionMatchingWorkflow, Service: "entityresolution"})
	registerType(restype.Descriptor{Type: TypeEntityResolutionSchemaMapping, Service: "entityresolution"})
	registerType(restype.Descriptor{Type: TypeEntityResolutionPolicyStatement, Service: "entityresolution"})
	registerService(serviceEntry{
		name: "aws:entityresolution",
		fn:   scanEntityResolution,
	})
}

const (
	erListIDMappingWorkflowsOp = "entityresolution:ListIdMappingWorkflows"
	erListIDNamespacesOp       = "entityresolution:ListIdNamespaces"
	erListMatchingWorkflowsOp  = "entityresolution:ListMatchingWorkflows"
	erGetMatchingWorkflowOp    = "entityresolution:GetMatchingWorkflow"
	erListSchemaMappingsOp     = "entityresolution:ListSchemaMappings"
	erGetPolicyOp              = "entityresolution:GetPolicy"
	erListTagsForResourceOp    = "entityresolution:ListTagsForResource"
)

// erPageCap is the MaxResults every listing sends: the maximum the model's
// range allows for all four (25).
const erPageCap = 25

// erNotFound is the not-found code ListTagsForResource, GetMatchingWorkflow
// and GetPolicy model, for a resource deleted since it was listed.
var erNotFound = []string{"ResourceNotFoundException"}

// erPolicyInvalidWhy says, in the skip warning, why a policy read refused
// with ValidationException is skipped rather than failed: GetPolicy models
// that code, and which resources the service refuses with it is not known.
const erPolicyInvalidWhy = "the service may refuse a policy read for some resources"

type entityResolutionAPI interface {
	ListIdMappingWorkflows(context.Context, *entityresolution.ListIdMappingWorkflowsInput, ...func(*entityresolution.Options)) (*entityresolution.ListIdMappingWorkflowsOutput, error)
	ListIdNamespaces(context.Context, *entityresolution.ListIdNamespacesInput, ...func(*entityresolution.Options)) (*entityresolution.ListIdNamespacesOutput, error)
	ListMatchingWorkflows(context.Context, *entityresolution.ListMatchingWorkflowsInput, ...func(*entityresolution.Options)) (*entityresolution.ListMatchingWorkflowsOutput, error)
	GetMatchingWorkflow(context.Context, *entityresolution.GetMatchingWorkflowInput, ...func(*entityresolution.Options)) (*entityresolution.GetMatchingWorkflowOutput, error)
	ListSchemaMappings(context.Context, *entityresolution.ListSchemaMappingsInput, ...func(*entityresolution.Options)) (*entityresolution.ListSchemaMappingsOutput, error)
	GetPolicy(context.Context, *entityresolution.GetPolicyInput, ...func(*entityresolution.Options)) (*entityresolution.GetPolicyOutput, error)
	ListTagsForResource(context.Context, *entityresolution.ListTagsForResourceInput, ...func(*entityresolution.Options)) (*entityresolution.ListTagsForResourceOutput, error)
}

// scanEntityResolution discovers the Entity Resolution ID mapping workflows,
// ID namespaces, matching workflows and schema mappings of one region, each
// with its tags, then the resource policy of each of them stored this scan.
// Every listing runs as its own phase, one at a time: a phase's error does
// not stop the later ones; a failed write and ctx's end do, and so does an
// NXDOMAIN of the first listing with nothing stored (the region does not
// serve the service) (runHostPhases). The tag denials of the whole scan are
// one warning.
func scanEntityResolution(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := entityresolution.NewFromConfig(acct.cfg, func(o *entityresolution.Options) { o.Region = region })
	tags := erTags(client).sharingDenials()
	// parents is the ARN of every row stored, the targets of the policy reads.
	var parents []string
	stored := func(rows []*store.Resource) {
		for _, r := range rows {
			parents = append(parents, r.NativeID)
		}
	}
	total, inserted, err := runHostPhases(ctx, st, acct, region, "entityresolution", 0, []hostPhase{
		{op: erListIDMappingWorkflowsOp, run: func(p *phaseTotals) bool {
			return scanERIdMappingWorkflows(ctx, client, acct, region, st, scanID, tags, stored, p)
		}},
		{op: erListIDNamespacesOp, run: func(p *phaseTotals) bool {
			return scanERIdNamespaces(ctx, client, acct, region, st, scanID, tags, stored, p)
		}},
		{op: erListMatchingWorkflowsOp, run: func(p *phaseTotals) bool {
			return scanERMatchingWorkflows(ctx, client, acct, region, st, scanID, tags, stored, p)
		}},
		{op: erListSchemaMappingsOp, run: func(p *phaseTotals) bool {
			return scanERSchemaMappings(ctx, client, acct, region, st, scanID, tags, stored, p)
		}},
		{op: erGetPolicyOp, run: func(p *phaseTotals) bool {
			return scanERPolicyStatements(ctx, client, acct, region, st, scanID, parents, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// erTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN its listing returned. The Service Reference names no
// resource under ListTagsForResource, but lists the ID mapping workflow, ID
// namespace, matching workflow and schema mapping under TagResource, each
// with aws:ResourceTag; the op's model (its VeniceGlobalArn pattern) accepts
// the ARNs of all four, and each Create* input carries tags. The op does not
// page its tags. Every listing returns the caller's own resources, so no row
// is another account's.
func erTags(client entityResolutionAPI) rowTagReader {
	return rowTagReader{
		op:       erListTagsForResourceOp,
		notFound: erNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &entityresolution.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// erRow is the row of a listed element stored under the ARN it carries, the
// element verbatim as the attributes; an element without an ARN is not
// stored (arnRow). what names the listing's elements.
func erRow(acct *account, region, scanID, typ, what string, arn, name *string, created *time.Time, item any) (*store.Resource, string, string) {
	return arnRow(what, arn, name, &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn),
		Name: name, Region: &region, CreatedAt: tp(created),
		AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	})
}

// scanERIdMappingWorkflows stores the ID mapping workflows with their tags,
// a page at a time, handing stored each page's rows once written. It
// reports whether a write failed, after which the scan must stop.
func scanERIdMappingWorkflows(ctx context.Context, client entityResolutionAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, stored func([]*store.Resource), p *phaseTotals) bool {
	return storeTokenPagesTagsWith(ctx, st, acct, region, erListIDMappingWorkflowsOp,
		func(ctx context.Context, token *string) ([]ertypes.IdMappingWorkflowSummary, *string, error) {
			out, err := client.ListIdMappingWorkflows(ctx, &entityresolution.ListIdMappingWorkflowsInput{MaxResults: sdkaws.Int32(erPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.WorkflowSummaries, out.NextToken, nil
		},
		func(w ertypes.IdMappingWorkflowSummary) (*store.Resource, string, string) {
			return erRow(acct, region, scanID, TypeEntityResolutionIDMappingWorkflow, "ID mapping workflows", w.WorkflowArn, w.WorkflowName, w.CreatedAt, w)
		},
		tokenPageSteps{stored: stored}, tags, p)
}

// scanERIdNamespaces stores the ID namespaces with their tags, a page at a
// time, handing stored each page's rows once written. It reports whether a
// write failed, after which the scan must stop.
func scanERIdNamespaces(ctx context.Context, client entityResolutionAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, stored func([]*store.Resource), p *phaseTotals) bool {
	return storeTokenPagesTagsWith(ctx, st, acct, region, erListIDNamespacesOp,
		func(ctx context.Context, token *string) ([]ertypes.IdNamespaceSummary, *string, error) {
			out, err := client.ListIdNamespaces(ctx, &entityresolution.ListIdNamespacesInput{MaxResults: sdkaws.Int32(erPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.IdNamespaceSummaries, out.NextToken, nil
		},
		func(n ertypes.IdNamespaceSummary) (*store.Resource, string, string) {
			return erRow(acct, region, scanID, TypeEntityResolutionIDNamespace, "ID namespaces", n.IdNamespaceArn, n.IdNamespaceName, n.CreatedAt, n)
		},
		tokenPageSteps{stored: stored}, tags, p)
}

// scanERMatchingWorkflows stores the matching workflows a page at a time,
// each row replaced by its GetMatchingWorkflow body, which carries what the
// list element lacks (RoleArn, the input and output source configs the
// resolvers read) and the workflow's tags (tokenPageSteps.detailTags). A
// workflow the Get no longer finds is dropped; any other Get failure keeps
// the list element as the row (rowDetailReader) and reads its tags with
// ListTagsForResource instead. It hands stored each page's rows once
// written, and reports whether a write failed, after which the scan must
// stop.
func scanERMatchingWorkflows(ctx context.Context, client entityResolutionAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, stored func([]*store.Resource), p *phaseTotals) bool {
	steps := tokenPageSteps{
		details: rowDetailReader{op: erGetMatchingWorkflowOp, what: "matching workflow(s)", notFound: erNotFound},
		detailTags: func(ctx context.Context, r *store.Resource) (taggedDetail, error) {
			// The Get takes the workflow's name; a listed workflow without
			// one keeps its list element and has its tags read by ARN.
			name := sv(r.Name)
			if name == "" {
				return taggedDetail{}, nil
			}
			out, err := client.GetMatchingWorkflow(ctx, &entityresolution.GetMatchingWorkflowInput{WorkflowName: &name})
			if err != nil {
				return taggedDetail{}, err
			}
			return taggedDetail{body: out, tags: mapTagsJSON(out.Tags), tagged: true}, nil
		},
		stored: stored,
	}
	return storeTokenPagesTagsWith(ctx, st, acct, region, erListMatchingWorkflowsOp,
		func(ctx context.Context, token *string) ([]ertypes.MatchingWorkflowSummary, *string, error) {
			out, err := client.ListMatchingWorkflows(ctx, &entityresolution.ListMatchingWorkflowsInput{MaxResults: sdkaws.Int32(erPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.WorkflowSummaries, out.NextToken, nil
		},
		func(w ertypes.MatchingWorkflowSummary) (*store.Resource, string, string) {
			return erRow(acct, region, scanID, TypeEntityResolutionMatchingWorkflow, "matching workflows", w.WorkflowArn, w.WorkflowName, w.CreatedAt, w)
		},
		steps, tags, p)
}

// scanERSchemaMappings stores the schema mappings with their tags, a page at
// a time, handing stored each page's rows once written. It reports whether a
// write failed, after which the scan must stop.
func scanERSchemaMappings(ctx context.Context, client entityResolutionAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, stored func([]*store.Resource), p *phaseTotals) bool {
	return storeTokenPagesTagsWith(ctx, st, acct, region, erListSchemaMappingsOp,
		func(ctx context.Context, token *string) ([]ertypes.SchemaMappingSummary, *string, error) {
			out, err := client.ListSchemaMappings(ctx, &entityresolution.ListSchemaMappingsInput{MaxResults: sdkaws.Int32(erPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.SchemaList, out.NextToken, nil
		},
		func(s ertypes.SchemaMappingSummary) (*store.Resource, string, string) {
			return erRow(acct, region, scanID, TypeEntityResolutionSchemaMapping, "schema mappings", s.SchemaArn, s.SchemaName, s.CreatedAt, s)
		},
		tokenPageSteps{stored: stored}, tags, p)
}

// erPolicyRead is one resource's policy read: its row (nil when the resource
// has none), the error of a denied or a ValidationException call, and any
// other error, an NXDOMAIN among them already flattened (lateNXDOMAIN).
type erPolicyRead struct {
	row                  *store.Resource
	denied, invalid, err error
}

// scanERPolicyStatements reads the resource policy of each resource stored
// this scan (parents, their ARNs) concurrently. ResourceNotFoundException,
// the not-found code GetPolicy models, may mean the resource has no policy
// or was deleted since it was listed; either way nothing is stored. A denied
// resource is warned about once for the op, and so is one refused with
// ValidationException (erPolicyInvalidWhy). A read still failing transiently
// after the SDK's retries has no listed row to keep in its place: all such
// reads are tallied into one error (transientTally), kept apart from the hard
// ones so that phaseErrs demotes it beside a hard error. Errors are added to
// p after the other policies are stored; it reports whether the write
// failed, after which the scan must stop.
func scanERPolicyStatements(ctx context.Context, client entityResolutionAPI, acct *account, region string, st *store.Store, scanID string, parents []string, p *phaseTotals) bool {
	reads, stopped := readEachKey(ctx, parents, func(ctx context.Context, arn string) erPolicyRead {
		return readERPolicy(ctx, client, acct, region, scanID, arn)
	})
	var (
		rows            []*store.Resource
		hard            []error
		denied, invalid skipTally
		transients      transientTally
	)
	if stopped != nil {
		hard = append(hard, fmt.Errorf("%s: %w", erGetPolicyOp, stopped))
	}
	for i, r := range reads {
		switch {
		case r.row != nil:
			rows = append(rows, r.row)
		case r.denied != nil:
			denied.skipped(parents[i], r.denied)
		case r.invalid != nil:
			invalid.skipped(parents[i], r.invalid)
		case r.err == nil:
		case transients.drop(parents[i], r.err):
		default:
			hard = append(hard, fmt.Errorf("%s %s: %w", erGetPolicyOp, parents[i], r.err))
		}
	}
	var transient error
	if t := &transients.skipTally; t.count > 0 {
		transient = fmt.Errorf("%s: %d resource(s) whose policy was not read, first %s: %w", erGetPolicyOp, t.count, t.first, t.err)
	}
	total, inserted, err := storeReadRows(st, acct, region, erGetPolicyOp, rows, nil, func() {
		denied.warnDeniedIn(st, erGetPolicyOp, acct, region, "resource(s) whose policy was not read were")
		invalid.warnCode(st, erGetPolicyOp, acct, region, "ValidationException", "resource(s)", erPolicyInvalidWhy)
	})
	return rowTagErrs{errors.Join(hard...), transient}.addPhase(p, erGetPolicyOp, erGetPolicyOp, total, inserted, err)
}

// readERPolicy reads the policy of the resource arn names. Its row's
// NativeID is arn + "/policy": the policy is a sub-resource of the resource.
func readERPolicy(ctx context.Context, client entityResolutionAPI, acct *account, region, scanID, arn string) erPolicyRead {
	out, err := client.GetPolicy(ctx, &entityresolution.GetPolicyInput{Arn: &arn})
	switch {
	case err == nil:
	case isAPIErrorCode(err, erNotFound...):
		return erPolicyRead{}
	case isAccessDenied(err):
		return erPolicyRead{denied: err}
	case isAPIErrorCode(err, "ValidationException"):
		return erPolicyRead{invalid: err}
	default:
		// The listings reached this host, so an NXDOMAIN now is a failure of
		// this scan, not a region gap or a transient read.
		return erPolicyRead{err: lateNXDOMAIN(erGetPolicyOp, err)}
	}
	if sv(out.Policy) == "" {
		return erPolicyRead{}
	}
	label := "policy"
	return erPolicyRead{row: &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeEntityResolutionPolicyStatement, NativeID: arn + "/policy",
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}}
}
