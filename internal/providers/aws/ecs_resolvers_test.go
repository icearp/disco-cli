package aws

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveECSRelationships_ServiceToClusterAndTaskDef verifies that a service's
// cluster ARN and task definition ARN both produce the correct relationships.
func TestResolveECSRelationships_ServiceToClusterAndTaskDef(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clusterARN := "arn:aws:ecs:us-east-1:123456789012:cluster/my-cluster"
	taskDefARN := "arn:aws:ecs:us-east-1:123456789012:task-definition/my-task:1"
	serviceARN := "arn:aws:ecs:us-east-1:123456789012:service/my-cluster/my-service"

	attrsJSON := `{"ClusterArn":"` + clusterARN + `","TaskDefinition":"` + taskDefARN + `"}`

	svcID := upsertTestResource(t, st, "aws", acct.ID, TypeECSService, serviceARN, testRegion, attrsJSON)
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeECSCluster, clusterARN, testRegion, "{}")
	tdID := upsertTestResource(t, st, "aws", acct.ID, TypeECSTaskDefinition, taskDefARN, testRegion, "{}")

	if err := resolveECSRelationships(acct, st); err != nil {
		t.Fatalf("resolveECSRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(svcID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}

	byTo := make(map[string]store.Relationship, 2)
	for _, rel := range rels {
		byTo[rel.ToID] = rel
	}

	if rel, ok := byTo[clusterID]; !ok || rel.Kind != store.RelAttachedTo {
		t.Errorf("expected service -[attached-to]-> cluster; rels: %+v", rels)
	}
	if rel, ok := byTo[tdID]; !ok || rel.Kind != store.RelUses {
		t.Errorf("expected service -[uses]-> task-definition; rels: %+v", rels)
	}
}

// TestResolveECSRelationships_NoAttrs verifies that a service with empty attrs
// produces no relationships and no error.
func TestResolveECSRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	serviceARN := "arn:aws:ecs:us-east-1:123456789012:service/bare/bare-service"
	svcID := upsertTestResource(t, st, "aws", acct.ID, TypeECSService, serviceARN, testRegion, "{}")

	if err := resolveECSRelationships(acct, st); err != nil {
		t.Fatalf("resolveECSRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(svcID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// TestResolveECSRelationships_NetworkConfig verifies service → subnet / SG
// edges from awsvpc NetworkConfiguration.
func TestResolveECSRelationships_NetworkConfig(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	serviceARN := "arn:aws:ecs:us-east-1:123456789012:service/c/awsvpc-svc"
	attrs := `{"NetworkConfiguration":{"AwsvpcConfiguration":{"Subnets":["subnet-1"],"SecurityGroups":["sg-1"]}}}`
	svcID := upsertTestResource(t, st, "aws", acct.ID, TypeECSService, serviceARN, testRegion, attrs)
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet,
		ec2ARN(testRegion, acct.ID, "subnet", "subnet-1"), testRegion, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup,
		ec2ARN(testRegion, acct.ID, "security-group", "sg-1"), testRegion, "{}")

	if err := resolveECSRelationships(acct, st); err != nil {
		t.Fatalf("resolveECSRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(svcID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, svcID, subnetID, store.RelAttachedTo)
	assertRelationship(t, rels, svcID, sgID, store.RelUses)
}

// TestResolveECSTaskDefinitionRelationships verifies IAM task/execution role
// edges.
func TestResolveECSTaskDefinitionRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	tdARN := "arn:aws:ecs:us-east-1:123456789012:task-definition/app:1"
	taskRole := "arn:aws:iam::123456789012:role/TaskRole"
	execRole := "arn:aws:iam::123456789012:role/ExecRole"
	attrs := `{"TaskRoleArn":"` + taskRole + `","ExecutionRoleArn":"` + execRole + `"}`
	tdID := upsertTestResource(t, st, "aws", acct.ID, TypeECSTaskDefinition, tdARN, testRegion, attrs)
	taskRoleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, taskRole, "", "{}")
	execRoleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, execRole, "", "{}")

	if err := resolveECSTaskDefinitionRelationships(acct, st); err != nil {
		t.Fatalf("resolveECSTaskDefinitionRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(tdID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, tdID, taskRoleID, store.RelAssumes)
	assertRelationship(t, rels, tdID, execRoleID, store.RelAssumes)
}
