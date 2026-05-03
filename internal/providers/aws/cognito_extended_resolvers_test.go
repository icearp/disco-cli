package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestCognitoUserPoolARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:cognito-idp:us-east-1:123:userpool/us-east-1_abc/group/admins", "arn:aws:cognito-idp:us-east-1:123:userpool/us-east-1_abc"},
		{"arn:aws:cognito-idp:us-east-1:123:userpool/us-east-1_abc/branding/mlb-1", "arn:aws:cognito-idp:us-east-1:123:userpool/us-east-1_abc"},
		{"", ""},
		{"arn:aws:cognito-idp:us-east-1:123:userpool/us-east-1_abc", ""},
	}
	for _, c := range cases {
		if got := cognitoUserPoolARNFromChild(c.in); got != c.want {
			t.Errorf("cognitoUserPoolARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveCognitoUserPoolChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	poolARN := fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/%s_abc", testRegion, acct.ID, testRegion)
	poolID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoUserPool, poolARN, testRegion, "{}")
	groupARN := poolARN + "/group/admins"
	groupID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoUserPoolGroup, groupARN, testRegion, "{}")
	domainARN := poolARN + "/domain/login.example.com"
	domainID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoUserPoolDomain, domainARN, testRegion, "{}")

	if err := resolveCognitoUserPoolChildren(acct, st); err != nil {
		t.Fatalf("resolveCognitoUserPoolChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(groupID)
	assertRelationship(t, rels, groupID, poolID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(domainID)
	assertRelationship(t, rels, domainID, poolID, store.RelAttachedTo)
}

func TestResolveCognitoUserPoolGroupRole(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/cognito-group-role", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	groupARN := fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/up-1/group/admins", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"RoleArn":%q}`, roleARN)
	groupID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoUserPoolGroup, groupARN, testRegion, attrs)
	if err := resolveCognitoUserPoolGroupRole(acct, st); err != nil {
		t.Fatalf("resolveCognitoUserPoolGroupRole: %v", err)
	}
	rels, _ := st.RelationshipsFrom(groupID)
	assertRelationship(t, rels, groupID, roleID, store.RelAssumes)
}

func TestResolveCognitoIdentityPoolRoleAttachment(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	idpARN := fmt.Sprintf("arn:aws:cognito-identity:%s:%s:identitypool/%s:abc-123", testRegion, acct.ID, testRegion)
	idpID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoIdentityPool, idpARN, testRegion, "{}")
	authRoleARN := fmt.Sprintf("arn:aws:iam::%s:role/auth", acct.ID)
	authRoleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, authRoleARN, "", "{}")

	attachARN := idpARN + "/roleattachment"
	attrs := fmt.Sprintf(`{"Roles":{"authenticated":%q}}`, authRoleARN)
	attachID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoIdentityPoolRoleAttachment, attachARN, testRegion, attrs)

	if err := resolveCognitoIdentityPoolRoleAttachment(acct, st); err != nil {
		t.Fatalf("resolveCognitoIdentityPoolRoleAttachment: %v", err)
	}
	rels, _ := st.RelationshipsFrom(attachID)
	assertRelationship(t, rels, attachID, idpID, store.RelAttachedTo)
	assertRelationship(t, rels, attachID, authRoleID, store.RelAssumes)
}
