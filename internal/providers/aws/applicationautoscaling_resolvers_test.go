package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestResolveApplicationAutoScalingPolicyToTarget verifies that a scaling
// policy's (ServiceNamespace, ResourceId, ScalableDimension) triple
// produces a policy → scalable-target attached-to relationship.
func TestResolveApplicationAutoScalingPolicyToTarget(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	ns := "ecs"
	rid := "service/cluster/svc"
	dim := "ecs:service:DesiredCount"

	targetNative := applicationAutoScalingScalableTargetNativeID(region, acct.ID, ns, rid, dim)
	policyARN := "arn:aws:autoscaling:us-east-1:123456789012:scalingPolicy:abc:resource/ecs/service/cluster/svc:policyName/foo"
	policyAttrs := fmt.Sprintf(`{"ServiceNamespace":"%s","ResourceId":"%s","ScalableDimension":"%s"}`, ns, rid, dim)

	targetID := upsertTestResource(t, st, "aws", acct.ID, TypeApplicationAutoScalingScalableTarget, targetNative, region, "{}")
	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeApplicationAutoScalingScalingPolicy, policyARN, region, policyAttrs)

	if err := resolveApplicationAutoScalingRelationships(acct, st); err != nil {
		t.Fatalf("resolveApplicationAutoScalingRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, policyID, targetID, store.RelAttachedTo)
}

// TestResolveApplicationAutoScalingPolicyToTarget_Missing verifies that an
// unscanned scalable-target produces no edge and no error.
func TestResolveApplicationAutoScalingPolicyToTarget_Missing(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	policyARN := "arn:aws:autoscaling:us-east-1:123456789012:scalingPolicy:abc:resource/ecs/service/cluster/svc:policyName/bar"
	policyAttrs := `{"ServiceNamespace":"ecs","ResourceId":"service/missing/svc","ScalableDimension":"ecs:service:DesiredCount"}`
	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeApplicationAutoScalingScalingPolicy, policyARN, region, policyAttrs)

	if err := resolveApplicationAutoScalingRelationships(acct, st); err != nil {
		t.Fatalf("resolveApplicationAutoScalingRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected no edges; got %+v", rels)
	}
}

// TestResolveApplicationAutoScalingPolicyToTarget_NoAttrs verifies that
// missing attrs fields produce no edge and no panic.
func TestResolveApplicationAutoScalingPolicyToTarget_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	policyARN := "arn:aws:autoscaling:us-east-1:123456789012:scalingPolicy:abc:resource/ecs/service/cluster/svc:policyName/baz"
	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeApplicationAutoScalingScalingPolicy, policyARN, region, "{}")

	if err := resolveApplicationAutoScalingRelationships(acct, st); err != nil {
		t.Fatalf("resolveApplicationAutoScalingRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected no edges; got %+v", rels)
	}
}
