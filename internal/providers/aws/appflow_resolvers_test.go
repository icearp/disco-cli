package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

// TestResolveAppFlowConnectorProfileToSecret verifies the connector-profile
// → Secrets Manager edge via CredentialsArn (preserved through sanitize.go's
// shape-bounded ARN allowlist despite living under the `credential` denylist
// substring).
func TestResolveAppFlowConnectorProfileToSecret(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	profileARN := "arn:aws:appflow:us-east-1:" + testAccountID + ":connectorprofile/sfdc"
	secretARN := "arn:aws:secretsmanager:us-east-1:" + testAccountID + ":secret:appflow!sfdc-creds-AbCdEf"
	attrs := `{"CredentialsArn":"` + secretARN + `"}`

	pID := upsertTestResource(t, st, "aws", acct.ID, TypeAppFlowConnectorProfile, profileARN, testRegion, attrs)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeSecretsManagerSecret, secretARN, testRegion, "{}")

	if err := resolveAppFlowConnectorProfileRelationships(acct, st); err != nil {
		t.Fatalf("resolveAppFlowConnectorProfileRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, sID, store.RelUses)
}

// TestResolveAppFlowConnectorProfileRelationships_Empty verifies the resolver
// runs cleanly with zero seeded profiles.
func TestResolveAppFlowConnectorProfileRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	if err := resolveAppFlowConnectorProfileRelationships(acct, st); err != nil {
		t.Fatalf("resolveAppFlowConnectorProfileRelationships: %v", err)
	}
}
