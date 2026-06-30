package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveSageMakerExperimentTrial(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	expARN := sagemakerARN(testRegion, acct.ID, "experiment", "exp-1")
	expID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerExperiment, expARN, testRegion, "{}")
	trialARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:experiment-trial/trial-1", testRegion, acct.ID)
	trialID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerExperimentTrial, trialARN, testRegion,
		`{"ExperimentName":"exp-1"}`)

	if err := resolveSageMakerExperimentTrial(acct, st); err != nil {
		t.Fatalf("resolveSageMakerExperimentTrial: %v", err)
	}
	rels, err := st.RelationshipsFrom(trialID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, trialID, expID, store.RelAttachedTo)
}

func TestResolveSageMakerExperimentTrial_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	trialARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:experiment-trial/trial-1", testRegion, acct.ID)
	trialID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerExperimentTrial, trialARN, testRegion, "{}")
	if err := resolveSageMakerExperimentTrial(acct, st); err != nil {
		t.Fatalf("resolveSageMakerExperimentTrial: %v", err)
	}
	rels, _ := st.RelationshipsFrom(trialID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveSageMakerHubContent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	hubARN := sagemakerARN(testRegion, acct.ID, "hub", "hub-1")
	hubID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerHub, hubARN, testRegion, "{}")
	hcARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:hub-content/hub-1/Model/content-1", testRegion, acct.ID)
	hcID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerHubContent, hcARN, testRegion,
		`{"HubName":"hub-1"}`)

	if err := resolveSageMakerHubContent(acct, st); err != nil {
		t.Fatalf("resolveSageMakerHubContent: %v", err)
	}
	rels, err := st.RelationshipsFrom(hcID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, hcID, hubID, store.RelAttachedTo)
}

func TestResolveSageMakerHubContent_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	hcARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:hub-content/hub-1/Model/content-1", testRegion, acct.ID)
	hcID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerHubContent, hcARN, testRegion, "{}")
	if err := resolveSageMakerHubContent(acct, st); err != nil {
		t.Fatalf("resolveSageMakerHubContent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(hcID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveSageMakerComputeQuota(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	clusterARN := sagemakerARN(testRegion, acct.ID, "cluster", "cluster-1")
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerCluster, clusterARN, testRegion, "{}")
	cqARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:compute-quota/cq-1", testRegion, acct.ID)
	cqID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerComputeQuota, cqARN, testRegion,
		fmt.Sprintf(`{"ClusterArn":%q}`, clusterARN))

	if err := resolveSageMakerComputeQuota(acct, st); err != nil {
		t.Fatalf("resolveSageMakerComputeQuota: %v", err)
	}
	rels, err := st.RelationshipsFrom(cqID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, cqID, clusterID, store.RelAttachedTo)
}

func TestResolveSageMakerComputeQuota_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cqARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:compute-quota/cq-1", testRegion, acct.ID)
	cqID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerComputeQuota, cqARN, testRegion, "{}")
	if err := resolveSageMakerComputeQuota(acct, st); err != nil {
		t.Fatalf("resolveSageMakerComputeQuota: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cqID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveSageMakerClusterSchedulerConfig(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	clusterARN := sagemakerARN(testRegion, acct.ID, "cluster", "cluster-1")
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerCluster, clusterARN, testRegion, "{}")
	cscARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:cluster-scheduler-config/csc-1", testRegion, acct.ID)
	cscID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerClusterSchedulerConfig, cscARN, testRegion,
		fmt.Sprintf(`{"ClusterArn":%q}`, clusterARN))

	if err := resolveSageMakerClusterSchedulerConfig(acct, st); err != nil {
		t.Fatalf("resolveSageMakerClusterSchedulerConfig: %v", err)
	}
	rels, err := st.RelationshipsFrom(cscID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, cscID, clusterID, store.RelAttachedTo)
}

func TestResolveSageMakerClusterSchedulerConfig_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cscARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:cluster-scheduler-config/csc-1", testRegion, acct.ID)
	cscID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerClusterSchedulerConfig, cscARN, testRegion, "{}")
	if err := resolveSageMakerClusterSchedulerConfig(acct, st); err != nil {
		t.Fatalf("resolveSageMakerClusterSchedulerConfig: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cscID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveSageMakerEdgeDeploymentPlan(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	fleetARN := sagemakerARN(testRegion, acct.ID, "device-fleet", "fleet-1")
	fleetID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerDeviceFleet, fleetARN, testRegion, "{}")
	planARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:edge-deployment/plan-1", testRegion, acct.ID)
	planID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerEdgeDeploymentPlan, planARN, testRegion,
		`{"DeviceFleetName":"fleet-1"}`)

	if err := resolveSageMakerEdgeDeploymentPlan(acct, st); err != nil {
		t.Fatalf("resolveSageMakerEdgeDeploymentPlan: %v", err)
	}
	rels, err := st.RelationshipsFrom(planID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, planID, fleetID, store.RelAttachedTo)
}

func TestResolveSageMakerEdgeDeploymentPlan_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	planARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:edge-deployment/plan-1", testRegion, acct.ID)
	planID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerEdgeDeploymentPlan, planARN, testRegion, "{}")
	if err := resolveSageMakerEdgeDeploymentPlan(acct, st); err != nil {
		t.Fatalf("resolveSageMakerEdgeDeploymentPlan: %v", err)
	}
	rels, _ := st.RelationshipsFrom(planID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
