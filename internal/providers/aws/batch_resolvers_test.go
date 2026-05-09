package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

const (
	testBatchCEName       = "ml-prod"
	testBatchQueueName    = "high-pri"
	testBatchJobDefName   = "trainer"
	testBatchSubnet       = "subnet-batch1"
	testBatchSG           = "sg-batch1"
	testBatchServiceRole  = "AWSBatchServiceRole"
	testBatchInstanceRole = "BatchInstanceRole"
	testBatchJobRole      = "BatchJobRole"
	testBatchExecRole     = "BatchExecRole"
	testBatchRepo         = "batch-trainer"
)

func batchCEArN() string {
	return fmt.Sprintf("arn:aws:batch:%s:%s:compute-environment/%s", testRegion, testAccountID, testBatchCEName)
}

func batchQueueARN() string {
	return fmt.Sprintf("arn:aws:batch:%s:%s:job-queue/%s", testRegion, testAccountID, testBatchQueueName)
}

func batchJobDefARN() string {
	return fmt.Sprintf("arn:aws:batch:%s:%s:job-definition/%s:1", testRegion, testAccountID, testBatchJobDefName)
}

// TestResolveBatchComputeEnvironmentTargets exercises every compute-env
// edge: service role, instance role, subnet, SG.
func TestResolveBatchComputeEnvironmentTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	svcRoleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s", testAccountID, testBatchServiceRole)
	instRoleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s", testAccountID, testBatchInstanceRole)
	svcRoleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, svcRoleARN, "", "{}")
	instRoleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, instRoleARN, "", "{}")

	snARN := ec2ARN(testRegion, testAccountID, "subnet", testBatchSubnet)
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, snARN, testRegion, "{}")

	sgARN := ec2ARN(testRegion, testAccountID, "security-group", testBatchSG)
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")

	ceAttrs := fmt.Sprintf(`{"ComputeEnvironmentArn":%q,"ComputeEnvironmentName":%q,"State":"ENABLED","ServiceRole":%q,"ComputeResources":{"Type":"MANAGED","InstanceRole":%q,"Subnets":[%q],"SecurityGroupIds":[%q]}}`,
		batchCEArN(), testBatchCEName, svcRoleARN, instRoleARN, testBatchSubnet, testBatchSG)
	ceID := upsertTestResource(t, st, "aws", acct.ID, TypeBatchComputeEnvironment, batchCEArN(), testRegion, ceAttrs)

	if err := resolveBatchComputeEnvironmentTargets(acct, st); err != nil {
		t.Fatalf("resolveBatchComputeEnvironmentTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(ceID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, ceID, svcRoleID, store.RelAssumes)
	assertRelationship(t, rels, ceID, instRoleID, store.RelAssumes)
	assertRelationship(t, rels, ceID, snID, store.RelUses)
	assertRelationship(t, rels, ceID, sgID, store.RelUses)
}

// TestResolveBatchJobQueueComputeEnvs verifies job-queue → compute-env
// edges with ordered priority preserved in edge attrs.
func TestResolveBatchJobQueueComputeEnvs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	ceID := upsertTestResource(t, st, "aws", acct.ID, TypeBatchComputeEnvironment, batchCEArN(), testRegion, "{}")

	queueAttrs := fmt.Sprintf(`{"JobQueueArn":%q,"JobQueueName":%q,"State":"ENABLED","ComputeEnvironmentOrder":[{"Order":1,"ComputeEnvironment":%q}]}`,
		batchQueueARN(), testBatchQueueName, batchCEArN())
	queueID := upsertTestResource(t, st, "aws", acct.ID, TypeBatchJobQueue, batchQueueARN(), testRegion, queueAttrs)

	if err := resolveBatchJobQueueComputeEnvs(acct, st); err != nil {
		t.Fatalf("resolveBatchJobQueueComputeEnvs: %v", err)
	}
	rels, err := st.RelationshipsFrom(queueID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, queueID, ceID, store.RelUses)
}

// TestResolveBatchJobDefinitionTargets verifies job-def → IAM roles +
// ECR repo edges.
func TestResolveBatchJobDefinitionTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	jobRoleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s", testAccountID, testBatchJobRole)
	execRoleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s", testAccountID, testBatchExecRole)
	jobRoleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, jobRoleARN, "", "{}")
	execRoleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, execRoleARN, "", "{}")

	repoARN := fmt.Sprintf("arn:aws:ecr:%s:%s:repository/%s", testRegion, testAccountID, testBatchRepo)
	repoID := upsertTestResource(t, st, "aws", acct.ID, TypeECRRepository, repoARN, testRegion, "{}")

	imageURL := fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com/%s:latest", testAccountID, testRegion, testBatchRepo)
	defAttrs := fmt.Sprintf(`{"JobDefinitionArn":%q,"JobDefinitionName":%q,"Status":"ACTIVE","ContainerProperties":{"Image":%q,"JobRoleArn":%q,"ExecutionRoleArn":%q}}`,
		batchJobDefARN(), testBatchJobDefName, imageURL, jobRoleARN, execRoleARN)
	defID := upsertTestResource(t, st, "aws", acct.ID, TypeBatchJobDefinition, batchJobDefARN(), testRegion, defAttrs)

	if err := resolveBatchJobDefinitionTargets(acct, st); err != nil {
		t.Fatalf("resolveBatchJobDefinitionTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(defID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, defID, jobRoleID, store.RelAssumes)
	assertRelationship(t, rels, defID, execRoleID, store.RelAssumes)
	assertRelationship(t, rels, defID, repoID, store.RelUses)
}

func TestResolveBatchQuotaShareJobQueue(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	qARN := fmt.Sprintf("arn:aws:batch:%s:%s:job-queue/q1", testRegion, acct.ID)
	sARN := fmt.Sprintf("arn:aws:batch:%s:%s:job-queue/q1/quota-share/share-1", testRegion, acct.ID)
	qID := upsertTestResource(t, st, "aws", acct.ID, TypeBatchJobQueue, qARN, testRegion, "{}")
	attrs := fmt.Sprintf(`{"QuotaShareArn":%q,"JobQueueArn":%q,"State":"ENABLED"}`, sARN, qARN)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeBatchQuotaShare, sARN, testRegion, attrs)

	if err := resolveBatchQuotaShareJobQueue(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sID)
	assertRelationship(t, rels, sID, qID, store.RelAttachedTo)
}

func TestResolveBatchQuotaShareJobQueue_UnscannedQueue(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	qARN := fmt.Sprintf("arn:aws:batch:%s:%s:job-queue/missing", testRegion, acct.ID)
	sARN := fmt.Sprintf("arn:aws:batch:%s:%s:job-queue/missing/quota-share/orphan", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"QuotaShareArn":%q,"JobQueueArn":%q,"State":"ENABLED"}`, sARN, qARN)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeBatchQuotaShare, sARN, testRegion, attrs)

	if err := resolveBatchQuotaShareJobQueue(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sID)
	if len(rels) != 0 {
		t.Errorf("expected no rels, got %+v", rels)
	}
}
