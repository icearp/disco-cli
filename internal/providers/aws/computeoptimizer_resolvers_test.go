package aws

import (
	"encoding/json"
	"testing"

	cotypes "github.com/aws/aws-sdk-go-v2/service/computeoptimizer/types"
	"github.com/icearp/disco-cli/store"
)

func coPrefAttrs(t *testing.T, p cotypes.RecommendationPreferencesDetail) string {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal preference: %v", err)
	}
	return string(b)
}

// coUpsertPref stores p under its scanner NativeID, named so ListResources
// lists the rows in the order the test needs.
func coUpsertPref(t *testing.T, st *store.Store, p cotypes.RecommendationPreferencesDetail, attrs, name string) string {
	t.Helper()
	id := computeOptimizerPreferenceNativeID("aws", testRegion, testAccountID, p.ResourceType, p.Scope.Name, sv(p.Scope.Value))
	return upsertTestResourceNamed(t, st, TypeComputeOptimizerRecommendationPreference, id, testRegion, attrs, name)
}

func coUsesEdges(t *testing.T, st *store.Store, from string) []store.Relationship {
	t.Helper()
	rels, err := st.RelationshipsFrom(from, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	return rels
}

func TestResolveComputeOptimizerPreferenceTargets_LinksScannedResources(t *testing.T) {
	st := newTestStore(t)
	instanceID := upsertTestResource(t, st, "aws", testAccountID, TypeEC2Instance, coInstanceARN, testRegion, "{}")
	groupID := upsertTestResource(t, st, "aws", testAccountID, TypeAutoScalingGroup, coGroupARN, testRegion, "{}")
	unscanned := ec2ARN(testRegion, testAccountID, "instance", "i-unscanned")

	prefs := map[string]cotypes.RecommendationPreferencesDetail{
		"instance":  coPref(cotypes.ResourceTypeEc2Instance, cotypes.ScopeNameResourceArn, coInstanceARN),
		"group":     coPref(cotypes.ResourceTypeAutoScalingGroup, cotypes.ScopeNameResourceArn, coGroupARN),
		"unscanned": coPref(cotypes.ResourceTypeEc2Instance, cotypes.ScopeNameResourceArn, unscanned),
		// An account scope's value is the account id, never a resource.
		"account": coPref(cotypes.ResourceTypeEc2Instance, cotypes.ScopeNameAccountId, testAccountID),
	}
	ids := map[string]string{}
	for name, p := range prefs {
		ids[name] = coUpsertPref(t, st, p, coPrefAttrs(t, p), name)
	}

	if err := resolveComputeOptimizerPreferenceTargets(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, c := range []struct{ from, to string }{{ids["instance"], instanceID}, {ids["group"], groupID}} {
		rels := coUsesEdges(t, st, c.from)
		if len(rels) != 1 {
			t.Errorf("uses edges from %s = %d, want 1", c.from, len(rels))
		}
		assertRelationship(t, rels, c.from, c.to, store.RelUses)
	}
	for _, name := range []string{"unscanned", "account"} {
		if rels := coUsesEdges(t, st, ids[name]); len(rels) != 0 {
			t.Errorf("%s preference has edges %v, want none", name, rels)
		}
	}
}

// A row with no scope gets no edge; a row whose attributes do not decode is
// reported without costing the other rows their edges.
func TestResolveComputeOptimizerPreferenceTargets_NoAttrsAndBadAttrs(t *testing.T) {
	st := newTestStore(t)
	instanceID := upsertTestResource(t, st, "aws", testAccountID, TypeEC2Instance, coInstanceARN, testRegion, "{}")
	empty := coUpsertPref(t, st, coPref(cotypes.ResourceTypeEbsVolume, cotypes.ScopeNameAccountId, testAccountID), "{}", "b-empty")
	bad := coPref(cotypes.ResourceTypeEcsService, cotypes.ScopeNameAccountId, testAccountID)
	coUpsertPref(t, st, bad, `{"Scope":"x"}`, "a-bad")
	linkedPref := coPref(cotypes.ResourceTypeEc2Instance, cotypes.ScopeNameResourceArn, coInstanceARN)
	linked := coUpsertPref(t, st, linkedPref, coPrefAttrs(t, linkedPref), "z-linked")

	if err := resolveComputeOptimizerPreferenceTargets(newTestAccount(testAccountID), st); err == nil {
		t.Error("resolve err = nil, want the a-bad decode error")
	}
	if rels := coUsesEdges(t, st, empty); len(rels) != 0 {
		t.Errorf("preference without attrs has edges %v, want none", rels)
	}
	assertRelationship(t, coUsesEdges(t, st, linked), linked, instanceID, store.RelUses)
}
