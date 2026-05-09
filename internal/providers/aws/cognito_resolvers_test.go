package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveCognitoAppClientRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	poolARN := fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/us-east-1_abc123", testRegion, acct.ID)
	clientARN := poolARN + "/client/client-xyz"

	poolID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoUserPool, poolARN, testRegion, "{}")
	clientID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoAppClient, clientARN, testRegion, "{}")

	if err := resolveCognitoAppClientRelationships(acct, st); err != nil {
		t.Fatalf("resolveCognitoAppClientRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(clientID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, clientID, poolID, store.RelAttachedTo)
}

func TestResolveCognitoIdentityPoolRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	ipARN := fmt.Sprintf("arn:aws:cognito-identity:%s:%s:identitypool/us-east-1:abc-guid", testRegion, acct.ID)
	userPoolNative := "us-east-1_pool1"
	userPoolARN := fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/%s", testRegion, acct.ID, userPoolNative)
	clientArnID := "client-aaa"
	clientARN := userPoolARN + "/client/" + clientArnID
	authRole := fmt.Sprintf("arn:aws:iam::%s:role/Cognito-Auth", acct.ID)
	unauthRole := fmt.Sprintf("arn:aws:iam::%s:role/Cognito-Unauth", acct.ID)

	providerName := fmt.Sprintf("cognito-idp.%s.amazonaws.com/%s", testRegion, userPoolNative)
	attrs := fmt.Sprintf(
		`{"Pool":{"CognitoIdentityProviders":[{"ClientId":%q,"ProviderName":%q}]},"Roles":{"authenticated":%q,"unauthenticated":%q}}`,
		clientArnID, providerName, authRole, unauthRole,
	)

	ipID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoIdentityPool, ipARN, testRegion, attrs)
	upID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoUserPool, userPoolARN, testRegion, "{}")
	appID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoAppClient, clientARN, testRegion, "{}")
	authRoleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, authRole, "", "{}")
	unauthRoleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, unauthRole, "", "{}")

	if err := resolveCognitoIdentityPoolRelationships(acct, st); err != nil {
		t.Fatalf("resolveCognitoIdentityPoolRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(ipID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, ipID, authRoleID, store.RelAssumes)
	assertRelationship(t, rels, ipID, unauthRoleID, store.RelAssumes)
	assertRelationship(t, rels, ipID, upID, store.RelUses)
	assertRelationship(t, rels, ipID, appID, store.RelUses)
}
