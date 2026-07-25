package aws

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/batch"
	batchtypes "github.com/aws/aws-sdk-go-v2/service/batch/types"
	"github.com/icearp/disco-cli/store"
)

// stubBatch is an in-memory batchAPI for the four types added this
// iteration; pre-existing compute-env/queue/job-def phases pass through
// via empty pages.
type stubBatch struct {
	schedPolicies      []batchtypes.SchedulingPolicyListingDetail
	schedPolicyDetails []batchtypes.SchedulingPolicyDetail
	consumables        []batchtypes.ConsumableResourceSummary
	serviceEnvs        []batchtypes.ServiceEnvironmentDetail
	jobQueues          []batchtypes.JobQueueDetail
	quotaShares        map[string][]batchtypes.QuotaShareDetail // keyed by JobQueue input
}

func (s *stubBatch) DescribeComputeEnvironments(_ context.Context, _ *batch.DescribeComputeEnvironmentsInput, _ ...func(*batch.Options)) (*batch.DescribeComputeEnvironmentsOutput, error) {
	return &batch.DescribeComputeEnvironmentsOutput{}, nil
}

func (s *stubBatch) DescribeJobQueues(_ context.Context, _ *batch.DescribeJobQueuesInput, _ ...func(*batch.Options)) (*batch.DescribeJobQueuesOutput, error) {
	return &batch.DescribeJobQueuesOutput{JobQueues: s.jobQueues}, nil
}

func (s *stubBatch) DescribeJobDefinitions(_ context.Context, _ *batch.DescribeJobDefinitionsInput, _ ...func(*batch.Options)) (*batch.DescribeJobDefinitionsOutput, error) {
	return &batch.DescribeJobDefinitionsOutput{}, nil
}

func (s *stubBatch) ListSchedulingPolicies(_ context.Context, _ *batch.ListSchedulingPoliciesInput, _ ...func(*batch.Options)) (*batch.ListSchedulingPoliciesOutput, error) {
	return &batch.ListSchedulingPoliciesOutput{SchedulingPolicies: s.schedPolicies}, nil
}

func (s *stubBatch) DescribeSchedulingPolicies(_ context.Context, _ *batch.DescribeSchedulingPoliciesInput, _ ...func(*batch.Options)) (*batch.DescribeSchedulingPoliciesOutput, error) {
	return &batch.DescribeSchedulingPoliciesOutput{SchedulingPolicies: s.schedPolicyDetails}, nil
}

func (s *stubBatch) ListConsumableResources(_ context.Context, _ *batch.ListConsumableResourcesInput, _ ...func(*batch.Options)) (*batch.ListConsumableResourcesOutput, error) {
	return &batch.ListConsumableResourcesOutput{ConsumableResources: s.consumables}, nil
}

func (s *stubBatch) DescribeServiceEnvironments(_ context.Context, _ *batch.DescribeServiceEnvironmentsInput, _ ...func(*batch.Options)) (*batch.DescribeServiceEnvironmentsOutput, error) {
	return &batch.DescribeServiceEnvironmentsOutput{ServiceEnvironments: s.serviceEnvs}, nil
}

func (s *stubBatch) ListQuotaShares(_ context.Context, in *batch.ListQuotaSharesInput, _ ...func(*batch.Options)) (*batch.ListQuotaSharesOutput, error) {
	q := ""
	if in.JobQueue != nil {
		q = *in.JobQueue
	}
	return &batch.ListQuotaSharesOutput{QuotaShares: s.quotaShares[q]}, nil
}

func TestScanBatchSchedulingPolicies(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	arn := fmt.Sprintf("arn:aws:batch:%s:%s:scheduling-policy/HighPri", testRegion, acct.ID)
	name := "HighPri"
	stub := &stubBatch{
		schedPolicies:      []batchtypes.SchedulingPolicyListingDetail{{Arn: &arn}},
		schedPolicyDetails: []batchtypes.SchedulingPolicyDetail{{Arn: &arn, Name: &name}},
	}
	total, _, err := scanBatchSchedulingPolicies(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 {
		t.Fatalf("total=%d want 1", total)
	}
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, arn)); err != nil {
		t.Errorf("scheduling-policy missing: %v", err)
	}
}

func TestScanBatchConsumableResources(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	arn := fmt.Sprintf("arn:aws:batch:%s:%s:consumable-resource/myres", testRegion, acct.ID)
	name := "myres"
	stub := &stubBatch{
		consumables: []batchtypes.ConsumableResourceSummary{
			{ConsumableResourceArn: &arn, ConsumableResourceName: &name},
		},
	}
	total, _, err := scanBatchConsumableResources(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 {
		t.Fatalf("total=%d want 1", total)
	}
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, arn)); err != nil {
		t.Errorf("consumable resource missing: %v", err)
	}
}

func TestScanBatchServiceEnvironments(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	arn := fmt.Sprintf("arn:aws:batch:%s:%s:service-environment/sm-train", testRegion, acct.ID)
	name := "sm-train"
	stub := &stubBatch{
		serviceEnvs: []batchtypes.ServiceEnvironmentDetail{
			{
				ServiceEnvironmentArn:  &arn,
				ServiceEnvironmentName: &name,
				ServiceEnvironmentType: batchtypes.ServiceEnvironmentTypeSagemakerTraining,
				State:                  batchtypes.ServiceEnvironmentStateEnabled,
			},
		},
	}
	total, _, err := scanBatchServiceEnvironments(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 {
		t.Fatalf("total=%d want 1", total)
	}
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, arn)); err != nil {
		t.Errorf("service environment missing: %v", err)
	}
}

func TestScanBatchQuotaShares(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	qARN := fmt.Sprintf("arn:aws:batch:%s:%s:job-queue/q1", testRegion, acct.ID)
	qName := "q1"
	sARN := fmt.Sprintf("arn:aws:batch:%s:%s:job-queue/q1/quota-share/share-1", testRegion, acct.ID)
	sName := "share-1"
	stub := &stubBatch{
		jobQueues: []batchtypes.JobQueueDetail{{JobQueueArn: &qARN, JobQueueName: &qName}},
		quotaShares: map[string][]batchtypes.QuotaShareDetail{
			qARN: {{
				QuotaShareArn:  &sARN,
				QuotaShareName: &sName,
				JobQueueArn:    &qARN,
				State:          batchtypes.QuotaShareStateEnabled,
			}},
		},
	}
	total, _, err := scanBatchQuotaShares(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 {
		t.Fatalf("total=%d want 1", total)
	}
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, sARN)); err != nil {
		t.Errorf("quota share missing: %v", err)
	}
}
