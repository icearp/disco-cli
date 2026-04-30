package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/batch"
)

func init() {
	registerService(serviceEntry{
		name: "aws:batch",
		fn:   scanBatch,
		emits: []coverage.TypeDecl{
			{Service: "batch", DiscoType: TypeBatchComputeEnvironment},
			{Service: "batch", DiscoType: TypeBatchJobQueue},
			{Service: "batch", DiscoType: TypeBatchJobDefinition},
			{Service: "batch", DiscoType: TypeBatchSchedulingPolicy},
			{Service: "batch", DiscoType: TypeBatchConsumableResource},
			{Service: "batch", DiscoType: TypeBatchServiceEnvironment},
			{Service: "batch", DiscoType: TypeBatchQuotaShare},
		},
	})
}

// batchAPI is the narrow set of Batch operations called by the scanBatch sub-phases.
type batchAPI interface {
	DescribeComputeEnvironments(context.Context, *batch.DescribeComputeEnvironmentsInput, ...func(*batch.Options)) (*batch.DescribeComputeEnvironmentsOutput, error)
	DescribeJobQueues(context.Context, *batch.DescribeJobQueuesInput, ...func(*batch.Options)) (*batch.DescribeJobQueuesOutput, error)
	DescribeJobDefinitions(context.Context, *batch.DescribeJobDefinitionsInput, ...func(*batch.Options)) (*batch.DescribeJobDefinitionsOutput, error)
	ListSchedulingPolicies(context.Context, *batch.ListSchedulingPoliciesInput, ...func(*batch.Options)) (*batch.ListSchedulingPoliciesOutput, error)
	DescribeSchedulingPolicies(context.Context, *batch.DescribeSchedulingPoliciesInput, ...func(*batch.Options)) (*batch.DescribeSchedulingPoliciesOutput, error)
	ListConsumableResources(context.Context, *batch.ListConsumableResourcesInput, ...func(*batch.Options)) (*batch.ListConsumableResourcesOutput, error)
	DescribeServiceEnvironments(context.Context, *batch.DescribeServiceEnvironmentsInput, ...func(*batch.Options)) (*batch.DescribeServiceEnvironmentsOutput, error)
	ListQuotaShares(context.Context, *batch.ListQuotaSharesInput, ...func(*batch.Options)) (*batch.ListQuotaSharesOutput, error)
}

// scanBatch discovers AWS Batch compute environments, job queues, and
// active job definitions in one region. Three phases run sequentially,
// each Describe* paginator-native with full body on List. Per-phase
// AccessDenied tolerated. Inactive job-definition revisions filtered
// out (Status=ACTIVE) — historical revisions are unbounded and
// graph-irrelevant. Job runs (ListJobs) are event data, deferred per
// the Macie/Detective/SecurityHub event-data precedent.
func scanBatch(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := batch.NewFromConfig(acct.cfg, func(o *batch.Options) { o.Region = region })

	{
		t, i, ferr := scanBatchComputeEnvironments(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanBatchJobQueues(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanBatchJobDefinitions(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanBatchSchedulingPolicies(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanBatchConsumableResources(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanBatchServiceEnvironments(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	{
		t, i, ferr := scanBatchQuotaShares(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	return total, inserted, nil
}

// scanBatchSchedulingPolicies enumerates fair-share / quota-share scheduling
// policies. ListSchedulingPolicies returns ARNs only; DescribeSchedulingPolicies
// fans them in (max 100 per call) for the full body with FairsharePolicy /
// QuotaSharePolicy / Tags. No edges to other resources.
func scanBatchSchedulingPolicies(ctx context.Context, client batchAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var arns []string
	pager := batch.NewListSchedulingPoliciesPaginator(client, &batch.ListSchedulingPoliciesInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "batch:ListSchedulingPolicies", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("batch:ListSchedulingPolicies: %w", perr)
		}
		for _, p := range out.SchedulingPolicies {
			if a := sv(p.Arn); a != "" {
				arns = append(arns, a)
			}
		}
	}
	if len(arns) == 0 {
		return 0, 0, nil
	}
	var rows []*store.Resource
	// DescribeSchedulingPolicies takes up to 100 ARNs per call.
	for start := 0; start < len(arns); start += 100 {
		end := min(start+100, len(arns))
		out, derr := client.DescribeSchedulingPolicies(ctx, &batch.DescribeSchedulingPoliciesInput{Arns: arns[start:end]})
		if derr != nil {
			if isAccessDenied(derr) {
				_ = skipIfAccessDenied(st, "batch:DescribeSchedulingPolicies", acct.ID, region, derr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("batch:DescribeSchedulingPolicies: %w", derr)
		}
		for _, p := range out.SchedulingPolicies {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			name := sv(p.Name)
			rows = append(rows, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBatchSchedulingPolicy,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(p),
				TagsJSON:       mapTagsJSON(p.Tags),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(rows) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(rows)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert batch scheduling policies: %w", uerr)
	}
	return len(rows), n, nil
}

// scanBatchConsumableResources enumerates account-level consumable resource
// pools used to gate per-job concurrency. Summary body carries ARN + name +
// in-use / total quantity; no Describe fan-out needed for graph data.
func scanBatchConsumableResources(ctx context.Context, client batchAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := batch.NewListConsumableResourcesPaginator(client, &batch.ListConsumableResourcesInput{})
	var rows []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "batch:ListConsumableResources", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("batch:ListConsumableResources: %w", perr)
		}
		for _, c := range out.ConsumableResources {
			arn := sv(c.ConsumableResourceArn)
			if arn == "" {
				continue
			}
			name := sv(c.ConsumableResourceName)
			rows = append(rows, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBatchConsumableResource,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(rows) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(rows)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert batch consumable resources: %w", uerr)
	}
	return len(rows), n, nil
}

// scanBatchServiceEnvironments enumerates SageMaker-Training / similar
// managed-service capacity pools attached to job queues. DescribeServiceEnvironments
// with empty filter returns full bodies in one paginated call.
func scanBatchServiceEnvironments(ctx context.Context, client batchAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := batch.NewDescribeServiceEnvironmentsPaginator(client, &batch.DescribeServiceEnvironmentsInput{})
	var rows []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "batch:DescribeServiceEnvironments", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("batch:DescribeServiceEnvironments: %w", perr)
		}
		for _, e := range out.ServiceEnvironments {
			arn := sv(e.ServiceEnvironmentArn)
			if arn == "" {
				continue
			}
			name := sv(e.ServiceEnvironmentName)
			status := string(e.State)
			rows = append(rows, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBatchServiceEnvironment,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(e),
				TagsJSON:       mapTagsJSON(e.Tags),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(rows) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(rows)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert batch service environments: %w", uerr)
	}
	return len(rows), n, nil
}

// scanBatchQuotaShares fans out per-JobQueue: ListQuotaShares requires a
// JobQueue input. Reuses the queues already enumerated by
// scanBatchJobQueues via a fresh DescribeJobQueues paginator (cheap; no
// shared cache on the account struct). Per-queue AccessDenied tolerated.
func scanBatchQuotaShares(ctx context.Context, client batchAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var queueARNs []string
	qpager := batch.NewDescribeJobQueuesPaginator(client, &batch.DescribeJobQueuesInput{})
	for qpager.HasMorePages() {
		out, perr := qpager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "batch:DescribeJobQueues(quota-share fan-out)", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("batch:DescribeJobQueues(quota-share fan-out): %w", perr)
		}
		for _, q := range out.JobQueues {
			if a := sv(q.JobQueueArn); a != "" {
				queueARNs = append(queueARNs, a)
			}
		}
	}
	var rows []*store.Resource
	for _, qarn := range queueARNs {
		qa := qarn
		spager := batch.NewListQuotaSharesPaginator(client, &batch.ListQuotaSharesInput{JobQueue: &qa})
		for spager.HasMorePages() {
			out, perr := spager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "batch:ListQuotaShares", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("batch:ListQuotaShares %s: %w", qa, perr)
			}
			for _, s := range out.QuotaShares {
				arn := sv(s.QuotaShareArn)
				if arn == "" {
					continue
				}
				name := sv(s.QuotaShareName)
				status := string(s.State)
				rows = append(rows, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeBatchQuotaShare,
					NativeID:       arn,
					Name:           &name,
					Region:         &region,
					Status:         &status,
					AttributesJSON: mustJSON(s),
					DiscoveredBy:   scanID,
				})
			}
		}
	}
	if len(rows) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(rows)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert batch quota shares: %w", uerr)
	}
	return len(rows), n, nil
}

func scanBatchComputeEnvironments(ctx context.Context, client batchAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := batch.NewDescribeComputeEnvironmentsPaginator(client, &batch.DescribeComputeEnvironmentsInput{})
	var batchRows []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "batch:DescribeComputeEnvironments", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("batch:DescribeComputeEnvironments: %w", perr)
		}
		for _, c := range out.ComputeEnvironments {
			arn := sv(c.ComputeEnvironmentArn)
			if arn == "" {
				continue
			}
			name := sv(c.ComputeEnvironmentName)
			status := string(c.State)
			batchRows = append(batchRows, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBatchComputeEnvironment,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batchRows) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batchRows)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert batch compute environments: %w", uerr)
	}
	return len(batchRows), n, nil
}

func scanBatchJobQueues(ctx context.Context, client batchAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := batch.NewDescribeJobQueuesPaginator(client, &batch.DescribeJobQueuesInput{})
	var batchRows []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "batch:DescribeJobQueues", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("batch:DescribeJobQueues: %w", perr)
		}
		for _, q := range out.JobQueues {
			arn := sv(q.JobQueueArn)
			if arn == "" {
				continue
			}
			name := sv(q.JobQueueName)
			status := string(q.State)
			batchRows = append(batchRows, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBatchJobQueue,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(q),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batchRows) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batchRows)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert batch job queues: %w", uerr)
	}
	return len(batchRows), n, nil
}

func scanBatchJobDefinitions(ctx context.Context, client batchAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	active := "ACTIVE"
	pager := batch.NewDescribeJobDefinitionsPaginator(client, &batch.DescribeJobDefinitionsInput{Status: &active})
	var batchRows []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "batch:DescribeJobDefinitions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("batch:DescribeJobDefinitions: %w", perr)
		}
		for _, j := range out.JobDefinitions {
			arn := sv(j.JobDefinitionArn)
			if arn == "" {
				continue
			}
			name := sv(j.JobDefinitionName)
			status := sv(j.Status)
			batchRows = append(batchRows, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeBatchJobDefinition,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(j),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batchRows) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batchRows)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert batch job definitions: %w", uerr)
	}
	return len(batchRows), n, nil
}
