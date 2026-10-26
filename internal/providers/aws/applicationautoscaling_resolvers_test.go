package aws

import (
	"encoding/json"
	"fmt"
	"testing"

	aastypes "github.com/aws/aws-sdk-go-v2/service/applicationautoscaling/types"
	"github.com/icearp/disco-cli/store"
)

// TestResolveApplicationAutoScalingScheduledActionToTarget verifies a
// scheduled action's target triple produces an attached-to edge to the
// stored scalable target, and none to another target in the namespace.
func TestResolveApplicationAutoScalingScheduledActionToTarget(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	targetNative := applicationAutoScalingScalableTargetNativeID("aws", region, acct.ID, "ecs", "service/cluster/web", "ecs:service:DesiredCount")
	otherNative := applicationAutoScalingScalableTargetNativeID("aws", region, acct.ID, "ecs", "service/cluster/api", "ecs:service:DesiredCount")
	targetID := upsertTestResource(t, st, "aws", acct.ID, TypeApplicationAutoScalingScalableTarget, targetNative, region, "{}")
	upsertTestResource(t, st, "aws", acct.ID, TypeApplicationAutoScalingScalableTarget, otherNative, region, "{}")

	attrs, err := json.Marshal(aastypes.ScheduledAction{
		ScheduledActionARN: ptrStr(aasTestActionWeb),
		ServiceNamespace:   aastypes.ServiceNamespaceEcs,
		ResourceId:         ptrStr("service/cluster/web"),
		ScalableDimension:  aastypes.ScalableDimensionECSServiceDesiredCount,
	})
	if err != nil {
		t.Fatal(err)
	}
	actionID := upsertTestResource(t, st, "aws", acct.ID, TypeApplicationAutoScalingScheduledAction, aasTestActionWeb, region, string(attrs))

	if err := resolveApplicationAutoScalingRelationships(acct, st); err != nil {
		t.Fatalf("resolveApplicationAutoScalingRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(actionID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("edges from scheduled action = %+v, want exactly one", rels)
	}
	assertRelationship(t, rels, actionID, targetID, store.RelAttachedTo)
}

// TestResolveApplicationAutoScalingScheduledActionToTarget_NoAttrs verifies
// a scheduled action without its target triple produces no edge, even to a
// target whose NativeID was built from empty parts.
func TestResolveApplicationAutoScalingScheduledActionToTarget_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	targetNative := applicationAutoScalingScalableTargetNativeID("aws", testRegion, acct.ID, "", "", "")
	upsertTestResource(t, st, "aws", acct.ID, TypeApplicationAutoScalingScalableTarget, targetNative, testRegion, "{}")
	actionID := upsertTestResource(t, st, "aws", acct.ID, TypeApplicationAutoScalingScheduledAction, aasTestActionWeb, testRegion, "{}")

	if err := resolveApplicationAutoScalingRelationships(acct, st); err != nil {
		t.Fatalf("resolveApplicationAutoScalingRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(actionID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected no edges; got %+v", rels)
	}
}

// TestResolveApplicationAutoScalingRelationships_DecodeErrorReturned verifies
// an undecodable child is reported and costs a child listed after it none of
// its edges.
func TestResolveApplicationAutoScalingRelationships_DecodeErrorReturned(t *testing.T) {
	for _, typ := range []string{TypeApplicationAutoScalingScalingPolicy, TypeApplicationAutoScalingScheduledAction} {
		t.Run(typ, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			upsertTestResourceNamed(t, st, typ, "bad", testRegion, `{"ResourceId":7}`, "a-bad")
			targetID := upsertTestResource(t, st, "aws", acct.ID, TypeApplicationAutoScalingScalableTarget,
				applicationAutoScalingScalableTargetNativeID("aws", testRegion, acct.ID, "ecs", "service/cluster/web", "ecs:service:DesiredCount"), testRegion, "{}")
			goodID := upsertTestResourceNamed(t, st, typ, "good", testRegion,
				`{"ServiceNamespace":"ecs","ResourceId":"service/cluster/web","ScalableDimension":"ecs:service:DesiredCount"}`, "b-good")

			if err := resolveApplicationAutoScalingRelationships(acct, st); err == nil {
				t.Fatal("resolve err = nil, want the decode error")
			}
			rels, err := st.RelationshipsFrom(goodID)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			assertRelationship(t, rels, goodID, targetID, store.RelAttachedTo)
		})
	}
}

// TestResolveApplicationAutoScalingPolicyToTarget verifies a scaling
// policy's (ServiceNamespace, ResourceId, ScalableDimension) triple
// produces a policy → scalable-target attached-to relationship.
func TestResolveApplicationAutoScalingPolicyToTarget(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	ns := "ecs"
	rid := "service/cluster/svc"
	dim := "ecs:service:DesiredCount"

	targetNative := applicationAutoScalingScalableTargetNativeID("aws", region, acct.ID, ns, rid, dim)
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

// TestResolveApplicationAutoScalingPolicyToTarget_Missing verifies an
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

// TestResolveApplicationAutoScalingPolicyToTarget_NoAttrs verifies
// missing attrs fields produce no edge and no panic, even to a target whose
// NativeID was built from empty parts.
func TestResolveApplicationAutoScalingPolicyToTarget_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion
	upsertTestResource(t, st, "aws", acct.ID, TypeApplicationAutoScalingScalableTarget,
		applicationAutoScalingScalableTargetNativeID("aws", region, acct.ID, "", "", ""), region, "{}")

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
