package aws

import (
	"cmp"
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/batch"
	batchtypes "github.com/aws/aws-sdk-go-v2/service/batch/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeBatchComputeEnvironment, Service: "batch"})
	registerType(restype.Descriptor{Type: TypeBatchJobQueue, Service: "batch"})
	registerType(restype.Descriptor{Type: TypeBatchJobDefinition, Service: "batch"})
	registerType(restype.Descriptor{Type: TypeBatchSchedulingPolicy, Service: "batch"})
	registerType(restype.Descriptor{Type: TypeBatchConsumableResource, Service: "batch"})
	registerType(restype.Descriptor{Type: TypeBatchServiceEnvironment, Service: "batch"})
	registerType(restype.Descriptor{Type: TypeBatchQuotaShare, Service: "batch"})
	registerService(serviceEntry{
		name: "aws:batch",
		fn:   scanBatch,
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

// batchPageLimit is the maxResults cap the Batch model documents for
// DescribeComputeEnvironments, DescribeJobQueues and DescribeJobDefinitions.
const batchPageLimit = 100

// scanBatch discovers AWS Batch compute environments, job queues, job
// definitions, scheduling policies, consumable resources, service
// environments and quota shares in one region. Job runs (ListJobs) are event
// data, deferred per the Macie/Detective/SecurityHub event-data precedent.
func scanBatch(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := batch.NewFromConfig(acct.cfg, func(o *batch.Options) { o.Region = region })
	return scanBatchAll(ctx, client, acct, region, st, scanID)
}

// scanBatchAll runs every Batch phase in turn and joins their errors; only a
// store write failure stops it, since every later write would fail too. The
// quota shares are read for the job queues the job-queue phase stored, so a
// failed queue listing leaves them fewer queues rather than none.
func scanBatchAll(ctx context.Context, client batchAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		phases phaseTotals
		queues []string
	)
	for _, phase := range []struct {
		op string
		fn func() (int, int, error)
	}{
		{"batch:DescribeComputeEnvironments", func() (int, int, error) {
			return scanBatchComputeEnvironments(ctx, client, acct, region, st, scanID)
		}},
		{"batch:DescribeJobQueues", func() (t, i int, err error) {
			queues, t, i, err = scanBatchJobQueues(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{"batch:DescribeJobDefinitions", func() (int, int, error) {
			return scanBatchJobDefinitions(ctx, client, acct, region, st, scanID)
		}},
		{"batch:ListSchedulingPolicies", func() (int, int, error) {
			return scanBatchSchedulingPolicies(ctx, client, acct, region, st, scanID)
		}},
		{"batch:ListConsumableResources", func() (int, int, error) {
			return scanBatchConsumableResources(ctx, client, acct, region, st, scanID)
		}},
		{"batch:DescribeServiceEnvironments", func() (int, int, error) {
			return scanBatchServiceEnvironments(ctx, client, acct, region, st, scanID)
		}},
		{"batch:ListQuotaShares", func() (int, int, error) {
			return scanBatchQuotaShares(ctx, client, acct, region, st, scanID, queues)
		}},
	} {
		t, i, err := phase.fn()
		if phases.addPhase(phase.op, t, i, err) {
			break
		}
	}
	return phases.result(st, acct, region)
}

// scanBatchSchedulingPolicies lists the scheduling policies' ARNs, then reads
// their full bodies (FairsharePolicy, QuotaSharePolicy, Tags) up to 100 ARNs
// per DescribeSchedulingPolicies call. The policies read before a failed
// listing page are still described, and a failed describe call costs only its
// own ARNs: every row read is stored before the errors are returned.
func scanBatchSchedulingPolicies(ctx context.Context, client batchAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed, listErr := readTokenPages(ctx, func(ctx context.Context, token *string) ([]batchtypes.SchedulingPolicyListingDetail, *string, error) {
		out, err := client.ListSchedulingPolicies(ctx, &batch.ListSchedulingPoliciesInput{MaxResults: sdkaws.Int32(batchPageLimit), NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		return out.SchedulingPolicies, out.NextToken, nil
	})
	var (
		arns                 []string
		listSkips, descSkips notStoredSkips
		rows                 []*store.Resource
		descDenied           error
		descErrs             []error
	)
	for _, p := range listed {
		if a := sv(p.Arn); a != "" {
			arns = append(arns, a)
			continue
		}
		listSkips.skipped("scheduling policies have an empty Arn", "one without an Arn")
	}
	// DescribeSchedulingPolicies takes up to 100 ARNs per call.
	for start := 0; start < len(arns); start += 100 {
		chunk := arns[start:min(start+100, len(arns))]
		out, err := client.DescribeSchedulingPolicies(ctx, &batch.DescribeSchedulingPoliciesInput{Arns: chunk})
		if isAccessDenied(err) {
			// The later chunks would be refused the same way, so they are not tried.
			descDenied = fmt.Errorf("scheduling policies not described: %d, first %s (later chunks not tried): %w", len(arns)-start, chunk[0], err)
			break
		}
		if err != nil {
			descErrs = append(descErrs, fmt.Errorf("batch:DescribeSchedulingPolicies %s: %w", chunk[0], err))
			continue
		}
		rows = buildListedRows(out.SchedulingPolicies, func(p batchtypes.SchedulingPolicyDetail) (*store.Resource, string, string) {
			arn := sv(p.Arn)
			if arn == "" {
				return nil, "scheduling policies have an empty Arn", cmp.Or(sv(p.Name), "one without a Name")
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBatchSchedulingPolicy, NativeID: arn, Name: p.Name, Region: &region,
				AttributesJSON: mustJSON(p), TagsJSON: mapTagsJSON(p.Tags), DiscoveredBy: scanID,
			}, "", ""
		}, rows, &descSkips)
	}
	return storeReadRows(st, acct, region, "batch:ListSchedulingPolicies", rows, listErr, func() {
		listSkips.warn(st, "batch:ListSchedulingPolicies", acct, region)
		descSkips.warn(st, "batch:DescribeSchedulingPolicies", acct, region)
		if descDenied != nil {
			_ = skipIfAccessDenied(st, "batch:DescribeSchedulingPolicies", acct.ID, region, descDenied)
		}
	}, descErrs...)
}

// scanBatchConsumableResources stores the account's consumable resource pools,
// which gate per-job concurrency, from their list summaries.
func scanBatchConsumableResources(ctx context.Context, client batchAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "batch:ListConsumableResources",
		func(ctx context.Context, token *string) ([]batchtypes.ConsumableResourceSummary, *string, error) {
			out, err := client.ListConsumableResources(ctx, &batch.ListConsumableResourcesInput{MaxResults: sdkaws.Int32(batchPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ConsumableResources, out.NextToken, nil
		},
		func(c batchtypes.ConsumableResourceSummary) (*store.Resource, string, string) {
			arn := sv(c.ConsumableResourceArn)
			if arn == "" {
				return nil, "consumable resources have an empty ConsumableResourceArn", cmp.Or(sv(c.ConsumableResourceName), "one without a ConsumableResourceName")
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBatchConsumableResource, NativeID: arn, Name: c.ConsumableResourceName, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}, "", ""
		})
}

// scanBatchServiceEnvironments stores the service environments (SageMaker
// Training and similar managed capacity pools job queues can attach).
func scanBatchServiceEnvironments(ctx context.Context, client batchAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "batch:DescribeServiceEnvironments",
		func(ctx context.Context, token *string) ([]batchtypes.ServiceEnvironmentDetail, *string, error) {
			out, err := client.DescribeServiceEnvironments(ctx, &batch.DescribeServiceEnvironmentsInput{MaxResults: sdkaws.Int32(batchPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ServiceEnvironments, out.NextToken, nil
		},
		func(e batchtypes.ServiceEnvironmentDetail) (*store.Resource, string, string) {
			arn := sv(e.ServiceEnvironmentArn)
			if arn == "" {
				return nil, "service environments have an empty ServiceEnvironmentArn", cmp.Or(sv(e.ServiceEnvironmentName), "one without a ServiceEnvironmentName")
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBatchServiceEnvironment, NativeID: arn, Name: e.ServiceEnvironmentName, Region: &region,
				Status: nonEmptyPtr(string(e.State)), AttributesJSON: mustJSON(e), TagsJSON: mapTagsJSON(e.Tags), DiscoveredBy: scanID,
			}, "", ""
		})
}

// scanBatchQuotaShares lists the quota shares of each job queue the job-queue
// phase stored this scan, the queues read concurrently. ListQuotaShares models
// no not-found code, so a queue deleted since it was listed fails the phase
// after the other queues' shares are stored.
func scanBatchQuotaShares(ctx context.Context, client batchAPI, acct *account, region string, st *store.Store, scanID string, queueARNs []string) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, "batch:ListQuotaShares", nil, queueARNs,
		func(ctx context.Context, queue string, token *string) ([]batchtypes.QuotaShareDetail, *string, error) {
			out, err := client.ListQuotaShares(ctx, &batch.ListQuotaSharesInput{JobQueue: &queue, MaxResults: sdkaws.Int32(batchPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.QuotaShares, out.NextToken, nil
		},
		func(_ string, s batchtypes.QuotaShareDetail) (*store.Resource, string, string) {
			arn := sv(s.QuotaShareArn)
			if arn == "" {
				return nil, "quota shares have an empty QuotaShareArn", cmp.Or(sv(s.QuotaShareName), "one without a QuotaShareName")
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBatchQuotaShare, NativeID: arn, Name: s.QuotaShareName, Region: &region,
				Status: nonEmptyPtr(string(s.State)), AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			}, "", ""
		})
}

func scanBatchComputeEnvironments(ctx context.Context, client batchAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "batch:DescribeComputeEnvironments",
		func(ctx context.Context, token *string) ([]batchtypes.ComputeEnvironmentDetail, *string, error) {
			out, err := client.DescribeComputeEnvironments(ctx, &batch.DescribeComputeEnvironmentsInput{MaxResults: sdkaws.Int32(batchPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ComputeEnvironments, out.NextToken, nil
		},
		func(c batchtypes.ComputeEnvironmentDetail) (*store.Resource, string, string) {
			arn := sv(c.ComputeEnvironmentArn)
			if arn == "" {
				return nil, "compute environments have an empty ComputeEnvironmentArn", cmp.Or(sv(c.ComputeEnvironmentName), "one without a ComputeEnvironmentName")
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBatchComputeEnvironment, NativeID: arn, Name: c.ComputeEnvironmentName, Region: &region,
				Status: nonEmptyPtr(string(c.State)), AttributesJSON: mustJSON(c), TagsJSON: mapTagsJSON(c.Tags), DiscoveredBy: scanID,
			}, "", ""
		})
}

// scanBatchJobQueues stores the job queues and returns the ARNs of those
// stored, the parents of the quota-share phase.
func scanBatchJobQueues(ctx context.Context, client batchAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	total, inserted, parents, err := storeParentList(ctx, st, acct, region, "batch:DescribeJobQueues", "job queues", "JobQueueArn",
		func(ctx context.Context, token *string) ([]batchtypes.JobQueueDetail, *string, error) {
			out, err := client.DescribeJobQueues(ctx, &batch.DescribeJobQueuesInput{MaxResults: sdkaws.Int32(batchPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.JobQueues, out.NextToken, nil
		},
		func(q batchtypes.JobQueueDetail) (*store.Resource, string, string) {
			arn := sv(q.JobQueueArn)
			if arn == "" {
				return nil, "job queues have an empty JobQueueArn", cmp.Or(sv(q.JobQueueName), "one without a JobQueueName")
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBatchJobQueue, NativeID: arn, Name: q.JobQueueName, Region: &region,
				Status: nonEmptyPtr(string(q.State)), AttributesJSON: mustJSON(q), TagsJSON: mapTagsJSON(q.Tags), DiscoveredBy: scanID,
			}, "", ""
		},
		func(q batchtypes.JobQueueDetail) string { return sv(q.JobQueueArn) })
	arns := make([]string, len(parents))
	for i, p := range parents {
		arns[i] = p.id
	}
	return arns, total, inserted, err
}

// scanBatchJobDefinitions stores every ACTIVE job definition revision.
// Deregistered (INACTIVE) revisions are not listed; the model says Batch
// deletes them 180 days after deregistration.
func scanBatchJobDefinitions(ctx context.Context, client batchAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, "batch:DescribeJobDefinitions",
		func(ctx context.Context, token *string) ([]batchtypes.JobDefinition, *string, error) {
			out, err := client.DescribeJobDefinitions(ctx, &batch.DescribeJobDefinitionsInput{
				Status: sdkaws.String("ACTIVE"), MaxResults: sdkaws.Int32(batchPageLimit), NextToken: token,
			})
			if err != nil {
				return nil, nil, err
			}
			return out.JobDefinitions, out.NextToken, nil
		},
		func(j batchtypes.JobDefinition) (*store.Resource, string, string) {
			arn := sv(j.JobDefinitionArn)
			if arn == "" {
				return nil, "job definitions have an empty JobDefinitionArn", cmp.Or(sv(j.JobDefinitionName), "one without a JobDefinitionName")
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeBatchJobDefinition, NativeID: arn, Name: j.JobDefinitionName, Region: &region,
				Status: nonEmptyPtr(sv(j.Status)), AttributesJSON: mustJSON(j), TagsJSON: mapTagsJSON(j.Tags), DiscoveredBy: scanID,
			}, "", ""
		})
}
