package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveECSCapacityProviderToASG(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	asgARN := fmt.Sprintf("arn:aws:autoscaling:%s:%s:autoScalingGroup:abc:autoScalingGroupName/asg-1", testRegion, acct.ID)
	asgID := upsertTestResource(t, st, "aws", acct.ID, TypeAutoScalingGroup, asgARN, testRegion, "{}")
	cpARN := ecsCapacityProviderARN(testRegion, acct.ID, "cp-1")
	attrs := fmt.Sprintf(`{"AutoScalingGroupProvider":{"AutoScalingGroupArn":"%s"}}`, asgARN)
	cpID := upsertTestResource(t, st, "aws", acct.ID, TypeECSCapacityProvider, cpARN, testRegion, attrs)
	if err := resolveECSCapacityProviderToASG(acct, st); err != nil {
		t.Fatalf("resolveECSCapacityProviderToASG: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cpID)
	assertRelationship(t, rels, cpID, asgID, store.RelUses)
}

func TestResolveECSCCPARefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	clARN := fmt.Sprintf("arn:aws:ecs:%s:%s:cluster/cl-1", testRegion, acct.ID)
	clID := upsertTestResource(t, st, "aws", acct.ID, TypeECSCluster, clARN, testRegion, "{}")
	cpARN := ecsCapacityProviderARN(testRegion, acct.ID, "cp-1")
	cpID := upsertTestResource(t, st, "aws", acct.ID, TypeECSCapacityProvider, cpARN, testRegion, "{}")
	ccpaARN := clARN + "/capacity-provider-associations"
	attrs := `{"CapacityProviders":["cp-1"]}`
	ccpaID := upsertTestResource(t, st, "aws", acct.ID, TypeECSClusterCapacityProviderAssociations, ccpaARN, testRegion, attrs)
	if err := resolveECSCCPARefs(acct, st); err != nil {
		t.Fatalf("resolveECSCCPARefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(ccpaID)
	assertRelationship(t, rels, ccpaID, clID, store.RelAttachedTo)
	assertRelationship(t, rels, ccpaID, cpID, store.RelUses)
}

func TestResolveECSTaskSetRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	clARN := fmt.Sprintf("arn:aws:ecs:%s:%s:cluster/cl-1", testRegion, acct.ID)
	clID := upsertTestResource(t, st, "aws", acct.ID, TypeECSCluster, clARN, testRegion, "{}")
	svcARN := fmt.Sprintf("arn:aws:ecs:%s:%s:service/cl-1/svc-1", testRegion, acct.ID)
	svcID := upsertTestResource(t, st, "aws", acct.ID, TypeECSService, svcARN, testRegion, "{}")
	tdARN := fmt.Sprintf("arn:aws:ecs:%s:%s:task-definition/td:1", testRegion, acct.ID)
	tdID := upsertTestResource(t, st, "aws", acct.ID, TypeECSTaskDefinition, tdARN, testRegion, "{}")
	tsARN := fmt.Sprintf("arn:aws:ecs:%s:%s:task-set/cl-1/svc-1/ts-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"ClusterArn":"%s","ServiceArn":"%s","TaskDefinition":"%s"}`, clARN, svcARN, tdARN)
	tsID := upsertTestResource(t, st, "aws", acct.ID, TypeECSTaskSet, tsARN, testRegion, attrs)
	if err := resolveECSTaskSetRefs(acct, st); err != nil {
		t.Fatalf("resolveECSTaskSetRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tsID)
	assertRelationship(t, rels, tsID, clID, store.RelAttachedTo)
	assertRelationship(t, rels, tsID, svcID, store.RelAttachedTo)
	assertRelationship(t, rels, tsID, tdID, store.RelUses)
}
