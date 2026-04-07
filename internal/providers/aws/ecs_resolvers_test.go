package aws

import (
	"testing"

	"codeburg.org/icearp/disco/internal/store"
)

// TestResolveECSRelationships_ServiceToClusterAndTaskDef verifies that a service's
// cluster ARN and task definition ARN both produce the correct relationships.
func TestResolveECSRelationships_ServiceToClusterAndTaskDef(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clusterARN := "arn:aws:ecs:us-east-1:123456789012:cluster/my-cluster"
	taskDefARN := "arn:aws:ecs:us-east-1:123456789012:task-definition/my-task:1"
	serviceARN := "arn:aws:ecs:us-east-1:123456789012:service/my-cluster/my-service"

	attrsJSON := `{"clusterArn":"` + clusterARN + `","taskDefinition":"` + taskDefARN + `"}`

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
