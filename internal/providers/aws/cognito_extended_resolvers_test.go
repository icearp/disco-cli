package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
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

func TestResolveCognitoUserPoolRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	lamARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:pre-signup", testRegion, acct.ID)
	lamID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, lamARN, testRegion, "{}")
	senderARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:custom-email", testRegion, acct.ID)
	senderID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, senderARN, testRegion, "{}")
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, fmt.Sprintf(`{"KeyId":"abc-123","Arn":%q}`, keyARN))
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/sns-caller", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	sesARN := fmt.Sprintf("arn:aws:ses:%s:%s:identity/no-reply@example.com", testRegion, acct.ID)
	sesID := upsertTestResource(t, st, "aws", acct.ID, TypeSESEmailIdentity, sesARN, testRegion, "{}")
	acmARN := fmt.Sprintf("arn:aws:acm:%s:%s:certificate/cert-1", testRegion, acct.ID)
	acmID := upsertTestResource(t, st, "aws", acct.ID, TypeACMCertificate, acmARN, testRegion, "{}")

	poolARN := fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/%s_pool", testRegion, acct.ID, testRegion)
	attrs := fmt.Sprintf(`{
		"LambdaConfig":{
			"PreSignUp":%q,
			"CustomEmailSender":{"LambdaArn":%q},
			"KMSKeyID":%q
		},
		"EmailConfiguration":{"SourceArn":%q},
		"SmsConfiguration":{"SnsCallerArn":%q},
		"CustomDomainConfig":{"CertificateArn":%q}
	}`, lamARN, senderARN, keyARN, sesARN, roleARN, acmARN)
	poolID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoUserPool, poolARN, testRegion, attrs)

	if err := resolveCognitoUserPoolRefs(acct, st); err != nil {
		t.Fatalf("resolveCognitoUserPoolRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(poolID)
	assertRelationship(t, rels, poolID, lamID, store.RelUses)
	assertRelationship(t, rels, poolID, senderID, store.RelUses)
	assertRelationship(t, rels, poolID, keyID, store.RelUses)
	assertRelationship(t, rels, poolID, sesID, store.RelUses)
	assertRelationship(t, rels, poolID, roleID, store.RelAssumes)
	assertRelationship(t, rels, poolID, acmID, store.RelUses)
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
