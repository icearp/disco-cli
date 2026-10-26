package aws

import (
	"encoding/json"
	"testing"

	artypes "github.com/aws/aws-sdk-go-v2/service/agentregistrycontrol/types"
	"github.com/icearp/disco-cli/store"
)

func arRecordAttrs(t *testing.T, sources ...artypes.ProvenanceSummary) string {
	t.Helper()
	rec := arRecord("r1", "c1")
	rec.ProvenanceSummaryList = sources
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	return string(b)
}

func TestResolveAgentRegistryRecordSources_LinksScannedSources(t *testing.T) {
	st := newTestStore(t)
	runtimeARN := bacARN("aws", testRegion, testAccountID, "runtime", "rt-1")
	gatewayARN := bacARN("aws", testRegion, testAccountID, "gateway", "gw-1")
	unscannedARN := bacARN("aws", testRegion, testAccountID, "runtime", "rt-unscanned")
	runtimeID := upsertTestResource(t, st, "aws", testAccountID, TypeBedrockAgentCoreRuntime, runtimeARN, testRegion, "{}")
	gatewayID := upsertTestResource(t, st, "aws", testAccountID, TypeBedrockAgentCoreGateway, gatewayARN, testRegion, "{}")
	fromRuntime := upsertTestResource(t, st, "aws", testAccountID, TypeAgentRegistryRegistryRecord, arRecordARN("r1", "c1"), testRegion,
		arRecordAttrs(t, artypes.ProvenanceSummary{Relation: artypes.ProvenanceRelationDetectedFrom, SourceId: &runtimeARN, SourceType: artypes.SourceTypeAwsBedrockAgentcoreRuntime}))
	fromGateway := upsertTestResource(t, st, "aws", testAccountID, TypeAgentRegistryRegistryRecord, arRecordARN("r1", "c2"), testRegion,
		arRecordAttrs(t, artypes.ProvenanceSummary{Relation: artypes.ProvenanceRelationDetectedFrom, SourceId: &gatewayARN, SourceType: artypes.SourceTypeAwsBedrockAgentcoreGateway}))
	fromUnscanned := upsertTestResource(t, st, "aws", testAccountID, TypeAgentRegistryRegistryRecord, arRecordARN("r1", "c3"), testRegion,
		arRecordAttrs(t, artypes.ProvenanceSummary{Relation: artypes.ProvenanceRelationDetectedFrom, SourceId: &unscannedARN, SourceType: artypes.SourceTypeAwsBedrockAgentcoreRuntime}))

	if err := resolveAgentRegistryRecordSources(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, c := range []struct{ from, to string }{{fromRuntime, runtimeID}, {fromGateway, gatewayID}} {
		rels, err := st.RelationshipsFrom(c.from, store.RelUses)
		if err != nil {
			t.Fatalf("RelationshipsFrom: %v", err)
		}
		if len(rels) != 1 {
			t.Errorf("uses edges from %s = %d, want 1", c.from, len(rels))
		}
		assertRelationship(t, rels, c.from, c.to, store.RelUses)
	}
	rels, err := st.RelationshipsFrom(fromUnscanned, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("record with an unscanned source has edges %v, want none", rels)
	}
}

// A record created through the API has no provenance and gets no edge; a
// row whose attributes do not decode is reported without costing the other
// rows their edges.
func TestResolveAgentRegistryRecordSources_NoProvenanceAndBadAttrs(t *testing.T) {
	st := newTestStore(t)
	runtimeARN := bacARN("aws", testRegion, testAccountID, "runtime", "rt-1")
	runtimeID := upsertTestResource(t, st, "aws", testAccountID, TypeBedrockAgentCoreRuntime, runtimeARN, testRegion, "{}")
	plain := upsertTestResource(t, st, "aws", testAccountID, TypeAgentRegistryRegistryRecord, arRecordARN("r1", "c1"), testRegion, arRecordAttrs(t))
	empty := upsertTestResource(t, st, "aws", testAccountID, TypeAgentRegistryRegistryRecord, arRecordARN("r1", "c2"), testRegion, "{}")
	// Named so the bad row is listed before the linked one (ListResources
	// orders by name): a resolver that stops at the bad row loses the edge.
	upsertTestResourceNamed(t, st, TypeAgentRegistryRegistryRecord, arRecordARN("r1", "c3"), testRegion, `{"ProvenanceSummaryList":"x"}`, "a-bad")
	linked := upsertTestResourceNamed(t, st, TypeAgentRegistryRegistryRecord, arRecordARN("r1", "c4"), testRegion,
		arRecordAttrs(t, artypes.ProvenanceSummary{Relation: artypes.ProvenanceRelationDetectedFrom, SourceId: &runtimeARN}), "z-linked")

	if err := resolveAgentRegistryRecordSources(newTestAccount(testAccountID), st); err == nil {
		t.Error("resolve err = nil, want the c3 decode error")
	}
	for _, id := range []string{plain, empty} {
		rels, err := st.RelationshipsFrom(id, store.RelUses)
		if err != nil {
			t.Fatalf("RelationshipsFrom: %v", err)
		}
		if len(rels) != 0 {
			t.Errorf("record without provenance has edges %v, want none", rels)
		}
	}
	rels, err := st.RelationshipsFrom(linked, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, linked, runtimeID, store.RelUses)
}
