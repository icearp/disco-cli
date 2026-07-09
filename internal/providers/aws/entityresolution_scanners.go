package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/entityresolution"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEntityResolutionIDMappingWorkflow, Service: "entityresolution", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEntityResolutionIDNamespace, Service: "entityresolution", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEntityResolutionMatchingWorkflow, Service: "entityresolution"})
	registerType(restype.Descriptor{Type: TypeEntityResolutionSchemaMapping, Service: "entityresolution", Leaf: true})
	registerType(restype.Descriptor{Type: TypeEntityResolutionPolicyStatement, Service: "entityresolution"})
	registerService(serviceEntry{
		name: "aws:entityresolution",
		fn:   scanEntityResolution,
	})
}

type entityResolutionAPI interface {
	ListIdMappingWorkflows(context.Context, *entityresolution.ListIdMappingWorkflowsInput, ...func(*entityresolution.Options)) (*entityresolution.ListIdMappingWorkflowsOutput, error)
	ListIdNamespaces(context.Context, *entityresolution.ListIdNamespacesInput, ...func(*entityresolution.Options)) (*entityresolution.ListIdNamespacesOutput, error)
	ListMatchingWorkflows(context.Context, *entityresolution.ListMatchingWorkflowsInput, ...func(*entityresolution.Options)) (*entityresolution.ListMatchingWorkflowsOutput, error)
	GetMatchingWorkflow(context.Context, *entityresolution.GetMatchingWorkflowInput, ...func(*entityresolution.Options)) (*entityresolution.GetMatchingWorkflowOutput, error)
	ListSchemaMappings(context.Context, *entityresolution.ListSchemaMappingsInput, ...func(*entityresolution.Options)) (*entityresolution.ListSchemaMappingsOutput, error)
	GetPolicy(context.Context, *entityresolution.GetPolicyInput, ...func(*entityresolution.Options)) (*entityresolution.GetPolicyOutput, error)
}

// scanEntityResolution discovers EntityResolution workflows, ID namespaces,
// schema mappings, and per-resource policy statements (when present).
func scanEntityResolution(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := entityresolution.NewFromConfig(acct.cfg, func(o *entityresolution.Options) { o.Region = region })

	idMapARNs, t, i, ferr := scanERIdMappingWorkflows(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	nsARNs, t, i, ferr := scanERIdNamespaces(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	mwARNs, t, i, ferr := scanERMatchingWorkflows(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	smARNs, t, i, ferr := scanERSchemaMappings(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	policyTargets := append(append(append(append([]string{}, idMapARNs...), nsARNs...), mwARNs...), smARNs...)
	for _, ta := range policyTargets {
		t, i, ferr = scanERPolicyStatement(ctx, client, acct, region, st, scanID, ta)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanERIdMappingWorkflows(ctx context.Context, client entityResolutionAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := entityresolution.NewListIdMappingWorkflowsPaginator(client, &entityresolution.ListIdMappingWorkflowsInput{})
	var batch []*store.Resource
	var arns []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "entityresolution:ListIdMappingWorkflows", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("entityresolution:ListIdMappingWorkflows: %w", err)
		}
		for _, w := range out.WorkflowSummaries {
			arn := sv(w.WorkflowArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEntityResolutionIDMappingWorkflow, NativeID: arn,
				Name: w.WorkflowName, Region: &region,
				AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "entityresolution id-mapping-workflows")
	return arns, t, i, err
}

func scanERIdNamespaces(ctx context.Context, client entityResolutionAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := entityresolution.NewListIdNamespacesPaginator(client, &entityresolution.ListIdNamespacesInput{})
	var batch []*store.Resource
	var arns []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "entityresolution:ListIdNamespaces", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("entityresolution:ListIdNamespaces: %w", err)
		}
		for _, n := range out.IdNamespaceSummaries {
			arn := sv(n.IdNamespaceArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEntityResolutionIDNamespace, NativeID: arn,
				Name: n.IdNamespaceName, Region: &region,
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "entityresolution id-namespaces")
	return arns, t, i, err
}

func scanERMatchingWorkflows(ctx context.Context, client entityResolutionAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := entityresolution.NewListMatchingWorkflowsPaginator(client, &entityresolution.ListMatchingWorkflowsInput{})
	var batch []*store.Resource
	var arns []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "entityresolution:ListMatchingWorkflows", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("entityresolution:ListMatchingWorkflows: %w", err)
		}
		for _, w := range out.WorkflowSummaries {
			arn := sv(w.WorkflowArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			// Enrich with GetMatchingWorkflow body — RoleArn, KMS, InputSourceConfig
			// (Glue tables), OutputSourceConfig (S3 path) are not on the list-summary.
			attrs := mustJSON(w)
			wname := sv(w.WorkflowName)
			if wname != "" {
				gout, gerr := client.GetMatchingWorkflow(ctx, &entityresolution.GetMatchingWorkflowInput{WorkflowName: &wname})
				if gerr != nil {
					if isAccessDenied(gerr) {
						_ = skipIfAccessDenied(st, "entityresolution:GetMatchingWorkflow", acct.ID, region, gerr)
					}
				} else if gout != nil {
					attrs = mustJSON(gout)
				}
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEntityResolutionMatchingWorkflow, NativeID: arn,
				Name: w.WorkflowName, Region: &region,
				AttributesJSON: attrs, DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "entityresolution matching-workflows")
	return arns, t, i, err
}

func scanERSchemaMappings(ctx context.Context, client entityResolutionAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := entityresolution.NewListSchemaMappingsPaginator(client, &entityresolution.ListSchemaMappingsInput{})
	var batch []*store.Resource
	var arns []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "entityresolution:ListSchemaMappings", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("entityresolution:ListSchemaMappings: %w", err)
		}
		for _, s := range out.SchemaList {
			arn := sv(s.SchemaArn)
			if arn == "" {
				continue
			}
			arns = append(arns, arn)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEntityResolutionSchemaMapping, NativeID: arn,
				Name: s.SchemaName, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "entityresolution schema-mappings")
	return arns, t, i, err
}

// scanERPolicyStatement fetches the resource-based policy attached to a
// workflow/namespace/schema. NativeID = parentARN + "/policy" (policy is
// a sub-resource of the parent).
func scanERPolicyStatement(ctx context.Context, client entityResolutionAPI, acct *account, region string, st *store.Store, scanID string, parentARN string) (int, int, error) {
	out, err := client.GetPolicy(ctx, &entityresolution.GetPolicyInput{Arn: &parentARN})
	if err != nil {
		if isAccessDenied(err) || isAPIErrorCode(err, "ResourceNotFoundException", "ValidationException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("entityresolution:GetPolicy: %w", err)
	}
	if sv(out.Policy) == "" {
		return 0, 0, nil
	}
	arn := parentARN + "/policy"
	label := "policy"
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeEntityResolutionPolicyStatement, NativeID: arn,
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "entityresolution policy-statements")
}
