package aws

import (
	"testing"
)

// TestResolveAppFlowConnectorProfileRelationships_NoOp documents the
// deferred-edge contract: until sanitize.go gains an ARN exception or the
// scanner stashes CredentialsArn as a sidecar, the resolver intentionally
// emits zero edges. Test guards against accidental edge emission breaking
// the no-op contract.
func TestResolveAppFlowConnectorProfileRelationships_NoOp(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	profileARN := "arn:aws:appflow:us-east-1:123456789012:connectorprofile/sfdc"
	profileID := upsertTestResource(t, st, "aws", acct.ID, TypeAppFlowConnectorProfile, profileARN, region, "{}")

	if err := resolveAppFlowConnectorProfileRelationships(acct, st); err != nil {
		t.Fatalf("resolveAppFlowConnectorProfileRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(profileID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected no edges; got %+v", rels)
	}
}

// TestResolveAppFlowConnectorProfileRelationships_Empty verifies that the
// resolver runs cleanly with zero seeded profiles.
func TestResolveAppFlowConnectorProfileRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	if err := resolveAppFlowConnectorProfileRelationships(acct, st); err != nil {
		t.Fatalf("resolveAppFlowConnectorProfileRelationships: %v", err)
	}
}
