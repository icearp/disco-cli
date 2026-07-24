package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveECSContainerInstanceRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	instARN := ec2ARN(region, acct.ID, "instance", "i-1")
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, instARN, region, "{}")
	ciARN := "arn:aws:ecs:" + region + ":" + testAccountID + ":container-instance/prod/ci-1"
	ciID := upsertTestResource(t, st, "aws", acct.ID, TypeECSContainerInstance, ciARN, region, `{"Ec2InstanceId":"i-1"}`)

	if err := resolveECSContainerInstanceRelationships(acct, st); err != nil {
		t.Fatalf("resolveECSContainerInstanceRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(ciID)
	assertRelationship(t, rels, ciID, instID, store.RelAttachedTo)
}

func TestResolveECSContainerInstanceRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	ciARN := "arn:aws:ecs:" + region + ":" + testAccountID + ":container-instance/prod/ci-1"
	ciID := upsertTestResource(t, st, "aws", acct.ID, TypeECSContainerInstance, ciARN, region, "{}")
	if err := resolveECSContainerInstanceRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(ciID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveECSTaskRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	clusterARN := "arn:aws:ecs:" + region + ":" + testAccountID + ":cluster/prod"
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeECSCluster, clusterARN, region, "{}")
	tdARN := "arn:aws:ecs:" + region + ":" + testAccountID + ":task-definition/web:3"
	tdID := upsertTestResource(t, st, "aws", acct.ID, TypeECSTaskDefinition, tdARN, region, "{}")
	ciARN := "arn:aws:ecs:" + region + ":" + testAccountID + ":container-instance/prod/ci-1"
	ciID := upsertTestResource(t, st, "aws", acct.ID, TypeECSContainerInstance, ciARN, region, "{}")
	taskARN := "arn:aws:ecs:" + region + ":" + testAccountID + ":task/prod/task-1"
	attrs := `{"ClusterArn":"` + clusterARN + `","TaskDefinitionArn":"` + tdARN + `","ContainerInstanceArn":"` + ciARN + `"}`
	taskID := upsertTestResource(t, st, "aws", acct.ID, TypeECSTask, taskARN, region, attrs)

	if err := resolveECSTaskRelationships(acct, st); err != nil {
		t.Fatalf("resolveECSTaskRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(taskID)
	assertRelationship(t, rels, taskID, clusterID, store.RelAttachedTo)
	assertRelationship(t, rels, taskID, tdID, store.RelUses)
	assertRelationship(t, rels, taskID, ciID, store.RelAttachedTo)
}

func TestResolveECSTaskRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	taskARN := "arn:aws:ecs:" + region + ":" + testAccountID + ":task/prod/task-1"
	taskID := upsertTestResource(t, st, "aws", acct.ID, TypeECSTask, taskARN, region, "{}")
	if err := resolveECSTaskRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(taskID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}
