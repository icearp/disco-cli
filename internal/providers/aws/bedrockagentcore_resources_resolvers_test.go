package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveBACResourceParents(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Harness + endpoint (parent via bare HarnessId).
	harnessArn := bacARN(testRegion, testAccountID, "harness", "h-1")
	harnessID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCoreHarness, harnessArn, testRegion,
		`{"Arn":"`+harnessArn+`","HarnessId":"h-1"}`)
	epArn := bacARN(testRegion, testAccountID, "harness-endpoint", "h-1/ep-1")
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCoreHarnessEndpoint, epArn, testRegion,
		`{"Arn":"`+epArn+`","HarnessId":"h-1"}`)

	// Registry + record (parent via RegistryArn).
	regArn := bacARN(testRegion, testAccountID, "registry", "r-1")
	regID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCoreRegistry, regArn, testRegion,
		`{"RegistryArn":"`+regArn+`","RegistryId":"r-1"}`)
	recArn := bacARN(testRegion, testAccountID, "registry-record", "r-1/rec-1")
	recID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCoreRegistryRecord, recArn, testRegion,
		`{"RecordArn":"`+recArn+`","RegistryArn":"`+regArn+`"}`)

	// Policy-engine + generation (parent via bare PolicyEngineId).
	engArn := bacARN(testRegion, testAccountID, "policy-engine", "pe-1")
	engID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCorePolicyEngine, engArn, testRegion,
		`{"PolicyEngineArn":"`+engArn+`","PolicyEngineId":"pe-1"}`)
	genArn := bacARN(testRegion, testAccountID, "policy-generation", "pg-1")
	genID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCorePolicyGeneration, genArn, testRegion,
		`{"PolicyGenerationArn":"`+genArn+`","PolicyEngineId":"pe-1"}`)

	for _, fn := range []func(*account, *store.Store) error{
		resolveBACHarnessEndpointParent, resolveBACRegistryRecordParent, resolveBACPolicyGenerationEngine,
	} {
		if err := fn(acct, st); err != nil {
			t.Fatalf("resolve: %v", err)
		}
	}

	epRels, _ := st.RelationshipsFrom(epID)
	assertRelationship(t, epRels, epID, harnessID, store.RelAttachedTo)
	recRels, _ := st.RelationshipsFrom(recID)
	assertRelationship(t, recRels, recID, regID, store.RelAttachedTo)
	genRels, _ := st.RelationshipsFrom(genID)
	assertRelationship(t, genRels, genID, engID, store.RelAttachedTo)
}

// A child whose parent is not scanned, and one with empty attrs, emit no edge.
func TestResolveBACResourceParents_NoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// endpoint referencing an unscanned harness
	epArn := bacARN(testRegion, testAccountID, "harness-endpoint", "h-x/ep-1")
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCoreHarnessEndpoint, epArn, testRegion,
		fmt.Sprintf(`{"Arn":%q,"HarnessId":"h-missing"}`, epArn))
	// record with empty attrs
	recArn := bacARN(testRegion, testAccountID, "registry-record", "r-x/rec-1")
	recID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCoreRegistryRecord, recArn, testRegion, "{}")

	for _, fn := range []func(*account, *store.Store) error{
		resolveBACHarnessEndpointParent, resolveBACRegistryRecordParent,
	} {
		if err := fn(acct, st); err != nil {
			t.Fatalf("resolve: %v", err)
		}
	}
	for _, id := range []string{epID, recID} {
		rels, _ := st.RelationshipsFrom(id)
		if len(rels) != 0 {
			t.Errorf("row %s emitted %d edges, want 0", id, len(rels))
		}
	}
}
