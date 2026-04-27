package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/batch"
)

func init() { registerService(serviceEntry{name: "aws:batch", fn: scanBatch}) }

// scanBatch discovers AWS Batch compute environments, job queues, and
// active job definitions in one region. Three phases run sequentially,
// each Describe* paginator-native with full body on List. Per-phase
// AccessDenied tolerated. Inactive job-definition revisions filtered
// out (Status=ACTIVE) — historical revisions are unbounded and
// graph-irrelevant. Job runs (ListJobs) are event data, deferred per
// the Macie/Detective/SecurityHub event-data precedent.
func scanBatch(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := batch.NewFromConfig(acct.cfg, func(o *batch.Options) { o.Region = region })

	if t, i, ferr := scanBatchComputeEnvironments(ctx, client, acct, region, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	if t, i, ferr := scanBatchJobQueues(ctx, client, acct, region, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	if t, i, ferr := scanBatchJobDefinitions(ctx, client, acct, region, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	return total, inserted, nil
}

func scanBatchComputeEnvironments(ctx context.Context, client *batch.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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

func scanBatchJobQueues(ctx context.Context, client *batch.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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

func scanBatchJobDefinitions(ctx context.Context, client *batch.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
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
