package aws

import (
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/greengrassv2/types"
	"github.com/icearp/disco-cli/store"
)

// ggv2DeploymentAttrs marshals the real SDK Deployment struct so the
// resolver test exercises the same JSON shape `mustJSON` produces in
// production. PascalCase keys are guaranteed by SDK marshalling.
func ggv2DeploymentAttrs(t *testing.T, d types.Deployment) string {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("ggv2DeploymentAttrs: %v", err)
	}
	return string(b)
}

func TestResolveGGV2DeploymentTarget_Thing(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	thingArn := "arn:aws:iot:us-east-1:123456789012:thing/core-dev-1"
	thingID := upsertTestResource(t, st, "aws", testAccountID, TypeIoTThing,
		thingArn, testRegion, "{}")

	depArn := "arn:aws:greengrass:us-east-1:123456789012:deployments:dep-aaa"
	target := thingArn
	depAttrs := ggv2DeploymentAttrs(t, types.Deployment{TargetArn: &target})
	depID := upsertTestResource(t, st, "aws", testAccountID, TypeGreengrassV2Deployment,
		depArn, testRegion, depAttrs)

	if err := resolveGGV2DeploymentTarget(acct, st); err != nil {
		t.Fatalf("resolveGGV2DeploymentTarget: %v", err)
	}
	rels, err := st.RelationshipsFrom(depID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, depID, thingID, store.RelUses)
}

func TestResolveGGV2DeploymentTarget_ThingGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	groupArn := "arn:aws:iot:us-east-1:123456789012:thinggroup/fleet-A"
	groupID := upsertTestResource(t, st, "aws", testAccountID, TypeIoTThingGroup,
		groupArn, testRegion, "{}")

	depArn := "arn:aws:greengrass:us-east-1:123456789012:deployments:dep-bbb"
	target := groupArn
	depAttrs := ggv2DeploymentAttrs(t, types.Deployment{TargetArn: &target})
	depID := upsertTestResource(t, st, "aws", testAccountID, TypeGreengrassV2Deployment,
		depArn, testRegion, depAttrs)

	if err := resolveGGV2DeploymentTarget(acct, st); err != nil {
		t.Fatalf("resolveGGV2DeploymentTarget: %v", err)
	}
	rels, err := st.RelationshipsFrom(depID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, depID, groupID, store.RelUses)
}

func TestResolveGGV2DeploymentTarget_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	depArn := "arn:aws:greengrass:us-east-1:123456789012:deployments:dep-empty"
	depID := upsertTestResource(t, st, "aws", testAccountID, TypeGreengrassV2Deployment,
		depArn, testRegion, "{}")

	if err := resolveGGV2DeploymentTarget(acct, st); err != nil {
		t.Fatalf("resolveGGV2DeploymentTarget: %v", err)
	}
	rels, err := st.RelationshipsFrom(depID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected zero edges from deployment with empty attrs, got %d", len(rels))
	}
}

func TestResolveGGV2DeploymentTarget_UnscannedTarget(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Deployment references an IoT thing that was not scanned (no
	// matching resource row). FK-safe: resolver must skip emit.
	depArn := "arn:aws:greengrass:us-east-1:123456789012:deployments:dep-orphan"
	target := "arn:aws:iot:us-east-1:123456789012:thing/never-scanned"
	depAttrs := ggv2DeploymentAttrs(t, types.Deployment{TargetArn: &target})
	depID := upsertTestResource(t, st, "aws", testAccountID, TypeGreengrassV2Deployment,
		depArn, testRegion, depAttrs)

	if err := resolveGGV2DeploymentTarget(acct, st); err != nil {
		t.Fatalf("resolveGGV2DeploymentTarget: %v", err)
	}
	rels, err := st.RelationshipsFrom(depID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected zero edges to unscanned target, got %d", len(rels))
	}
}
