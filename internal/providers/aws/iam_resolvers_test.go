package aws

import (
	"net/url"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// --- resolveInstanceProfileRoles ---

func TestResolveInstanceProfileRoles(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	profileARN := "arn:aws:iam::123456789012:instance-profile/my-profile"
	roleARN := "arn:aws:iam::123456789012:role/my-role"
	attrsJSON := `{"Roles": [{"Arn": "` + roleARN + `"}]}`

	profileID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMInstanceProfile, profileARN, "", attrsJSON)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")

	if err := resolveInstanceProfileRoles(acct, st); err != nil {
		t.Fatalf("resolveInstanceProfileRoles: %v", err)
	}

	rels, err := st.RelationshipsFrom(profileID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, profileID, roleID, "contains")
}

func TestResolveInstanceProfileRoles_NoRoles(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	profileARN := "arn:aws:iam::123456789012:instance-profile/empty-profile"
	upsertTestResource(t, st, "aws", acct.ID, TypeIAMInstanceProfile, profileARN, "", "{}")

	if err := resolveInstanceProfileRoles(acct, st); err != nil {
		t.Fatalf("resolveInstanceProfileRoles: %v", err)
	}
}

// --- resolveInlinePolicyParents ---

func TestResolveInlinePolicyParents_RolePolicy(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	roleARN := "arn:aws:iam::123456789012:role/my-role"
	policyNativeID := roleARN + "/policy/InlinePolicy1"

	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRolePolicy, policyNativeID, "", "{}")

	if err := resolveInlinePolicyParents(acct, st); err != nil {
		t.Fatalf("resolveInlinePolicyParents: %v", err)
	}

	rels, err := st.RelationshipsTo(policyID)
	if err != nil {
		t.Fatalf("RelationshipsTo: %v", err)
	}
	assertRelationship(t, rels, roleID, policyID, "contains")
}

func TestResolveInlinePolicyParents_UserPolicy(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	userARN := "arn:aws:iam::123456789012:user/alice"
	policyNativeID := userARN + "/policy/UserInlinePolicy"

	userID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMUser, userARN, "", "{}")
	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMUserPolicy, policyNativeID, "", "{}")

	if err := resolveInlinePolicyParents(acct, st); err != nil {
		t.Fatalf("resolveInlinePolicyParents: %v", err)
	}

	rels, err := st.RelationshipsTo(policyID)
	if err != nil {
		t.Fatalf("RelationshipsTo: %v", err)
	}
	assertRelationship(t, rels, userID, policyID, "contains")
}

func TestResolveInlinePolicyParents_GroupPolicy(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	groupARN := "arn:aws:iam::123456789012:group/developers"
	policyNativeID := groupARN + "/policy/GroupInlinePolicy"

	groupID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMGroup, groupARN, "", "{}")
	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMGroupPolicy, policyNativeID, "", "{}")

	if err := resolveInlinePolicyParents(acct, st); err != nil {
		t.Fatalf("resolveInlinePolicyParents: %v", err)
	}

	rels, err := st.RelationshipsTo(policyID)
	if err != nil {
		t.Fatalf("RelationshipsTo: %v", err)
	}
	assertRelationship(t, rels, groupID, policyID, "contains")
}

func TestResolveInlinePolicyParents_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	if err := resolveInlinePolicyParents(acct, st); err != nil {
		t.Fatalf("resolveInlinePolicyParents: %v", err)
	}
}

// --- resolveAccessKeyUsers ---

func TestResolveAccessKeyUsers(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	userARN := "arn:aws:iam::123456789012:user/alice"
	keyID := "AKIAIOSFODNN7EXAMPLE"
	keyNativeID := userARN + "/access-key/" + keyID

	userResID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMUser, userARN, "", "{}")
	keyResID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMAccessKey, keyNativeID, "", "{}")

	if err := resolveAccessKeyUsers(acct, st); err != nil {
		t.Fatalf("resolveAccessKeyUsers: %v", err)
	}

	rels, err := st.RelationshipsFrom(userResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, userResID, keyResID, "contains")
}

func TestResolveAccessKeyUsers_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	if err := resolveAccessKeyUsers(acct, st); err != nil {
		t.Fatalf("resolveAccessKeyUsers: %v", err)
	}
}

func TestResolveAccessKeyUsers_MalformedNativeID(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// NativeID without the "/access-key/" delimiter — resolver should skip it.
	upsertTestResource(t, st, "aws", acct.ID, TypeIAMAccessKey, "malformed-native-id", "", "{}")

	if err := resolveAccessKeyUsers(acct, st); err != nil {
		t.Fatalf("resolveAccessKeyUsers: %v", err)
	}
}

// --- resolveMFADeviceToUser ---

func TestResolveMFADeviceToUser(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	userARN := "arn:aws:iam::123456789012:user/alice"
	mfaSerial := "arn:aws:iam::123456789012:mfa/alice"
	mfaAttrs := `{"SerialNumber":"` + mfaSerial + `","User":{"Arn":"` + userARN + `","UserName":"alice"}}`

	userResID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMUser, userARN, "", "{}")
	mfaResID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMVirtualMFADevice, mfaSerial, "", mfaAttrs)

	if err := resolveMFADeviceToUser(acct, st); err != nil {
		t.Fatalf("resolveMFADeviceToUser: %v", err)
	}

	rels, err := st.RelationshipsFrom(userResID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, userResID, mfaResID, "contains")
}

func TestResolveMFADeviceToUser_Unassigned(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Device with no User field — resolver must silently skip it.
	mfaSerial := "arn:aws:iam::123456789012:mfa/unassigned"
	upsertTestResource(t, st, "aws", acct.ID, TypeIAMVirtualMFADevice, mfaSerial, "", `{"SerialNumber":"`+mfaSerial+`"}`)

	if err := resolveMFADeviceToUser(acct, st); err != nil {
		t.Fatalf("resolveMFADeviceToUser (unassigned): %v", err)
	}
}

// --- resolveManagedPolicyAttachments (empty store — no API calls made) ---

func TestResolveManagedPolicyAttachments_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	if err := resolveManagedPolicyAttachments(acct, st); err != nil {
		t.Fatalf("resolveManagedPolicyAttachments: %v", err)
	}
}

// --- resolveUserGroupMemberships (empty store — no API calls made) ---

func TestResolveUserGroupMemberships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	if err := resolveUserGroupMemberships(acct, st); err != nil {
		t.Fatalf("resolveUserGroupMemberships: %v", err)
	}
}

// --- resolveIAMRoleFederatedTrust ---

func TestResolveIAMRoleFederatedTrust(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	samlARN := "arn:aws:iam::123456789012:saml-provider/OktaSAML"
	oidcARN := "arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/ABC"
	roleARN := "arn:aws:iam::123456789012:role/federated-role"

	// Trust policy with two statements: Federated as string (SAML) and as array (OIDC).
	trustDoc := `{"Version":"2012-10-17","Statement":[` +
		`{"Effect":"Allow","Principal":{"Federated":"` + samlARN + `"},"Action":"sts:AssumeRoleWithSAML"},` +
		`{"Effect":"Allow","Principal":{"Federated":["` + oidcARN + `"]},"Action":"sts:AssumeRoleWithWebIdentity"}` +
		`]}`
	attrsJSON := `{"AssumeRolePolicyDocument":"` + url.QueryEscape(trustDoc) + `"}`

	samlID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMSAMLProvider, samlARN, "", "{}")
	oidcID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMOIDCProvider, oidcARN, "", "{}")
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", attrsJSON)

	if err := resolveIAMRoleFederatedTrust(acct, st); err != nil {
		t.Fatalf("resolveIAMRoleFederatedTrust: %v", err)
	}

	rels, err := st.RelationshipsFrom(roleID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, roleID, samlID, string(store.RelAssumes))
	assertRelationship(t, rels, roleID, oidcID, string(store.RelAssumes))
}

func TestResolveIAMRoleFederatedTrust_NoTrust(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	roleARN := "arn:aws:iam::123456789012:role/no-trust"
	upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")

	if err := resolveIAMRoleFederatedTrust(acct, st); err != nil {
		t.Fatalf("resolveIAMRoleFederatedTrust: %v", err)
	}
}

func TestResolveIAMRoleFederatedTrust_NonFederatedPrincipal(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	roleARN := "arn:aws:iam::123456789012:role/ec2-role"
	trustDoc := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`
	attrsJSON := `{"AssumeRolePolicyDocument":"` + url.QueryEscape(trustDoc) + `"}`

	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", attrsJSON)

	if err := resolveIAMRoleFederatedTrust(acct, st); err != nil {
		t.Fatalf("resolveIAMRoleFederatedTrust: %v", err)
	}
	rels, err := st.RelationshipsFrom(roleID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Fatalf("expected 0 relationships, got %d", len(rels))
	}
}

// --- resolveIAMPolicyResources ---

// inlineRolePolicyAttrs builds an AttributesJSON shaped like GetRolePolicyOutput
// — PolicyDocument is URL-encoded as the AWS SDK delivers it.
func inlineRolePolicyAttrs(t *testing.T, doc string) string {
	t.Helper()
	return `{"PolicyDocument":"` + url.QueryEscape(doc) + `"}`
}

// managedPolicyAttrs builds an AttributesJSON shaped like the wrapped form
// emitted by scanIAMPolicies after Pass A (Policy + PolicyVersion siblings).
func managedPolicyAttrs(t *testing.T, doc string) string {
	t.Helper()
	return `{"Policy":{},"PolicyVersion":{"Document":"` + url.QueryEscape(doc) + `"}}`
}

func TestResolveIAMPolicyResources_InlineRolePolicyToS3AndKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	roleARN := "arn:aws:iam::" + testAccountID + ":role/data-reader"
	policyNativeID := roleARN + "/policy/AllowReadBucketAndKMS"
	bucketARN := "arn:aws:s3:::sensitive-bucket"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/abcd-1234"

	doc := `{"Statement":[{"Effect":"Allow","Resource":["` + bucketARN + `/*","` + keyARN + `"]}]}`

	upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRolePolicy, policyNativeID, "", inlineRolePolicyAttrs(t, doc))
	bucketID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, "", "{}")
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveIAMPolicyResources(acct, st); err != nil {
		t.Fatalf("resolveIAMPolicyResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, policyID, bucketID, store.RelUses)
	assertRelationship(t, rels, policyID, keyID, store.RelUses)
}

func TestResolveIAMPolicyResources_ManagedPolicyToDynamoAndSecrets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	policyARN := "arn:aws:iam::" + testAccountID + ":policy/AppDataAccess"
	tableARN := "arn:aws:dynamodb:us-east-1:" + testAccountID + ":table/orders"
	secretARN := "arn:aws:secretsmanager:us-east-1:" + testAccountID + ":secret:prod/db-AbCdEf"

	// Reference the table via an index ARN to exercise child-suffix trimming,
	// and the secret via a versioned ARN to exercise version-segment trimming.
	tableIndexRef := tableARN + "/index/by-customer"
	secretVersionedRef := secretARN + ":AWSCURRENT:abcdefab-1234-1234-1234-abcdefabcdef"

	doc := `{"Statement":[{"Effect":"Allow","Resource":["` + tableIndexRef + `","` + secretVersionedRef + `"]}]}`

	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, policyARN, "", managedPolicyAttrs(t, doc))
	tableID := upsertTestResource(t, st, "aws", acct.ID, TypeDynamoDBTable, tableARN, testRegion, "{}")
	secretID := upsertTestResource(t, st, "aws", acct.ID, TypeSecretsManagerSecret, secretARN, testRegion, "{}")

	if err := resolveIAMPolicyResources(acct, st); err != nil {
		t.Fatalf("resolveIAMPolicyResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, policyID, tableID, store.RelUses)
	assertRelationship(t, rels, policyID, secretID, store.RelUses)
}

// statementList must accept a single object as well as an array.
func TestResolveIAMPolicyResources_StatementSingleObject(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	policyARN := "arn:aws:iam::" + testAccountID + ":policy/SingleStmt"
	bucketARN := "arn:aws:s3:::single-stmt-bucket"
	doc := `{"Statement":{"Effect":"Allow","Resource":["` + bucketARN + `"]}}`

	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, policyARN, "", managedPolicyAttrs(t, doc))
	bucketID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, "", "{}")

	if err := resolveIAMPolicyResources(acct, st); err != nil {
		t.Fatalf("resolveIAMPolicyResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, policyID, bucketID, store.RelUses)
}

// resourceList must accept a single string as well as an array.
func TestResolveIAMPolicyResources_ResourceSingleString(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	policyARN := "arn:aws:iam::" + testAccountID + ":policy/SingleRes"
	bucketARN := "arn:aws:s3:::single-res-bucket"
	doc := `{"Statement":[{"Effect":"Allow","Resource":"` + bucketARN + `"}]}`

	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, policyARN, "", managedPolicyAttrs(t, doc))
	bucketID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, "", "{}")

	if err := resolveIAMPolicyResources(acct, st); err != nil {
		t.Fatalf("resolveIAMPolicyResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, policyID, bucketID, store.RelUses)
}

// Deny statements must not produce edges — only Allow grants positive access.
func TestResolveIAMPolicyResources_DenySkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	policyARN := "arn:aws:iam::" + testAccountID + ":policy/DenyOnly"
	bucketARN := "arn:aws:s3:::deny-bucket"
	doc := `{"Statement":[{"Effect":"Deny","Resource":["` + bucketARN + `"]}]}`

	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, policyARN, "", managedPolicyAttrs(t, doc))
	upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, "", "{}")

	if err := resolveIAMPolicyResources(acct, st); err != nil {
		t.Fatalf("resolveIAMPolicyResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Fatalf("expected 0 relationships, got %d", len(rels))
	}
}

// Cross-account / wildcard / unscanned references must skip silently.
func TestResolveIAMPolicyResources_FKSafeSkip(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	policyARN := "arn:aws:iam::" + testAccountID + ":policy/Phantom"
	otherBucketARN := "arn:aws:s3:::cross-account-bucket" // not scanned
	wildcardARN := "arn:aws:s3:::prod-*"                  // wildcard
	starARN := "*"                                        // pure wildcard
	doc := `{"Statement":[{"Effect":"Allow","Resource":["` + otherBucketARN + `","` + wildcardARN + `","` + starARN + `"]}]}`

	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, policyARN, "", managedPolicyAttrs(t, doc))

	if err := resolveIAMPolicyResources(acct, st); err != nil {
		t.Fatalf("resolveIAMPolicyResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Fatalf("expected 0 relationships (FK-safe skip), got %d", len(rels))
	}
}

// Empty / malformed docs must not panic or error.
func TestResolveIAMPolicyResources_EmptyAndMalformed(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, "arn:aws:iam::"+testAccountID+":policy/empty", "", "{}")
	upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, "arn:aws:iam::"+testAccountID+":policy/no-version", "", `{"Policy":{}}`)
	upsertTestResource(t, st, "aws", acct.ID, TypeIAMRolePolicy,
		"arn:aws:iam::"+testAccountID+":role/r/policy/bad-doc", "",
		`{"PolicyDocument":"%ZZ-not-url-encoded"}`)
	upsertTestResource(t, st, "aws", acct.ID, TypeIAMRolePolicy,
		"arn:aws:iam::"+testAccountID+":role/r/policy/bad-json", "",
		inlineRolePolicyAttrs(t, "{not json"))

	if err := resolveIAMPolicyResources(acct, st); err != nil {
		t.Fatalf("resolveIAMPolicyResources: %v", err)
	}
}

func TestResolveIAMPolicyResources_LambdaWithVersion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	policyARN := "arn:aws:iam::" + testAccountID + ":policy/InvokeFn"
	fnARN := "arn:aws:lambda:us-east-1:" + testAccountID + ":function:order-processor"
	versionedRef := fnARN + ":PROD"

	doc := `{"Statement":[{"Effect":"Allow","Resource":"` + versionedRef + `"}]}`

	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, policyARN, "", managedPolicyAttrs(t, doc))
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")

	if err := resolveIAMPolicyResources(acct, st); err != nil {
		t.Fatalf("resolveIAMPolicyResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, policyID, fnID, store.RelUses)
}

func TestResolveIAMPolicyResources_LogGroupStripsStarSuffix(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	policyARN := "arn:aws:iam::" + testAccountID + ":policy/LambdaExec"
	groupARN := "arn:aws:logs:us-east-1:" + testAccountID + ":log-group:/aws/lambda/order-processor"
	starRef := groupARN + ":*"

	doc := `{"Statement":[{"Effect":"Allow","Resource":"` + starRef + `"}]}`

	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, policyARN, "", managedPolicyAttrs(t, doc))
	groupID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, groupARN, testRegion, "{}")

	if err := resolveIAMPolicyResources(acct, st); err != nil {
		t.Fatalf("resolveIAMPolicyResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, policyID, groupID, store.RelUses)
}

func TestResolveIAMPolicyResources_SNSTopic(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	policyARN := "arn:aws:iam::" + testAccountID + ":policy/Publish"
	topicARN := "arn:aws:sns:us-east-1:" + testAccountID + ":order-events"
	subARN := topicARN + ":7d6e5c4b-3a2f-1e0d-9c8b-7a6e5d4c3b2a"

	// Topic ARN should match; subscription ARN (extra segment) should skip.
	doc := `{"Statement":[{"Effect":"Allow","Resource":["` + topicARN + `","` + subARN + `"]}]}`

	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, policyARN, "", managedPolicyAttrs(t, doc))
	topicID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topicARN, testRegion, "{}")

	if err := resolveIAMPolicyResources(acct, st); err != nil {
		t.Fatalf("resolveIAMPolicyResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 edge (topic only, sub skipped), got %d", len(rels))
	}
	assertRelationship(t, rels, policyID, topicID, store.RelUses)
}

func TestResolveIAMPolicyResources_SQSQueue(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	policyARN := "arn:aws:iam::" + testAccountID + ":policy/SendMsg"
	queueARN := "arn:aws:sqs:us-east-1:" + testAccountID + ":incoming"

	doc := `{"Statement":[{"Effect":"Allow","Resource":"` + queueARN + `"}]}`

	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, policyARN, "", managedPolicyAttrs(t, doc))
	queueID := upsertTestResource(t, st, "aws", acct.ID, TypeSQSQueue, queueARN, testRegion, "{}")

	if err := resolveIAMPolicyResources(acct, st); err != nil {
		t.Fatalf("resolveIAMPolicyResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, policyID, queueID, store.RelUses)
}

func TestResolveIAMPolicyResources_SSMParameter(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	policyARN := "arn:aws:iam::" + testAccountID + ":policy/ReadConfig"
	paramARN := "arn:aws:ssm:us-east-1:" + testAccountID + ":parameter/app/db-host"
	bareNameRef := "/app/db-host" // bare name — must skip (no region context)

	doc := `{"Statement":[{"Effect":"Allow","Resource":["` + paramARN + `","` + bareNameRef + `"]}]}`

	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, policyARN, "", managedPolicyAttrs(t, doc))
	paramID := upsertTestResource(t, st, "aws", acct.ID, TypeSSMParameter, paramARN, testRegion, "{}")

	if err := resolveIAMPolicyResources(acct, st); err != nil {
		t.Fatalf("resolveIAMPolicyResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 edge (full ARN only, bare name skipped), got %d", len(rels))
	}
	assertRelationship(t, rels, policyID, paramID, store.RelUses)
}

func TestResolveIAMPolicyResources_KinesisStreamRejectsConsumer(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	policyARN := "arn:aws:iam::" + testAccountID + ":policy/ReadStream"
	streamARN := "arn:aws:kinesis:us-east-1:" + testAccountID + ":stream/clickstream"
	consumerRef := streamARN + "/consumer/my-app:1700000000"

	doc := `{"Statement":[{"Effect":"Allow","Resource":["` + streamARN + `","` + consumerRef + `"]}]}`

	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, policyARN, "", managedPolicyAttrs(t, doc))
	streamID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisStream, streamARN, testRegion, "{}")

	if err := resolveIAMPolicyResources(acct, st); err != nil {
		t.Fatalf("resolveIAMPolicyResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 edge (stream only, consumer skipped), got %d", len(rels))
	}
	assertRelationship(t, rels, policyID, streamID, store.RelUses)
}

func TestResolveIAMPolicyResources_ECRRepository(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	policyARN := "arn:aws:iam::" + testAccountID + ":policy/PullImage"
	repoARN := "arn:aws:ecr:us-east-1:" + testAccountID + ":repository/orders/api"

	doc := `{"Statement":[{"Effect":"Allow","Resource":"` + repoARN + `"}]}`

	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, policyARN, "", managedPolicyAttrs(t, doc))
	repoID := upsertTestResource(t, st, "aws", acct.ID, TypeECRRepository, repoARN, testRegion, "{}")

	if err := resolveIAMPolicyResources(acct, st); err != nil {
		t.Fatalf("resolveIAMPolicyResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, policyID, repoID, store.RelUses)
}

func TestResolveIAMPolicyResources_IAMRolePassRole(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	policyARN := "arn:aws:iam::" + testAccountID + ":policy/PassExecRole"
	roleARN := "arn:aws:iam::" + testAccountID + ":role/lambda-exec"
	slrARN := "arn:aws:iam::" + testAccountID + ":role/aws-service-role/elasticache.amazonaws.com/AWSServiceRoleForElastiCache"

	doc := `{"Statement":[{"Effect":"Allow","Resource":["` + roleARN + `","` + slrARN + `"]}]}`

	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, policyARN, "", managedPolicyAttrs(t, doc))
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	slrID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMServiceLinkedRole, slrARN, "", "{}")

	if err := resolveIAMPolicyResources(acct, st); err != nil {
		t.Fatalf("resolveIAMPolicyResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, policyID, roleID, store.RelUses)
	assertRelationship(t, rels, policyID, slrID, store.RelUses)
}

func TestResolveIAMPolicyResources_RDSInstanceAndCluster(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	policyARN := "arn:aws:iam::" + testAccountID + ":policy/RDSAccess"
	dbARN := "arn:aws:rds:us-east-1:" + testAccountID + ":db:orders-prod"
	clusterARN := "arn:aws:rds:us-east-1:" + testAccountID + ":cluster:orders-aurora"
	snapshotRef := "arn:aws:rds:us-east-1:" + testAccountID + ":snapshot:orders-2026-01-01"

	doc := `{"Statement":[{"Effect":"Allow","Resource":["` + dbARN + `","` + clusterARN + `","` + snapshotRef + `"]}]}`

	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, policyARN, "", managedPolicyAttrs(t, doc))
	dbID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBInstance, dbARN, testRegion, "{}")
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBCluster, clusterARN, testRegion, "{}")

	if err := resolveIAMPolicyResources(acct, st); err != nil {
		t.Fatalf("resolveIAMPolicyResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 edges (db + cluster, snapshot skipped), got %d", len(rels))
	}
	assertRelationship(t, rels, policyID, dbID, store.RelUses)
	assertRelationship(t, rels, policyID, clusterID, store.RelUses)
}

func TestResolveIAMPolicyResources_SFNStateMachineRejectsIntegration(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	policyARN := "arn:aws:iam::" + testAccountID + ":policy/StartSFN"
	smARN := "arn:aws:states:us-east-1:" + testAccountID + ":stateMachine:OrderFlow"
	integrationRef := "arn:aws:states:::stateMachine:invoke" // synthetic-but-realistic shape

	doc := `{"Statement":[{"Effect":"Allow","Resource":["` + smARN + `","` + integrationRef + `"]}]}`

	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, policyARN, "", managedPolicyAttrs(t, doc))
	smID := upsertTestResource(t, st, "aws", acct.ID, TypeSFNStateMachine, smARN, testRegion, "{}")

	if err := resolveIAMPolicyResources(acct, st); err != nil {
		t.Fatalf("resolveIAMPolicyResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 edge (state-machine only, ::: integration skipped), got %d", len(rels))
	}
	assertRelationship(t, rels, policyID, smID, store.RelUses)
}

func TestResolveIAMPolicyResources_EventBridgeBusAndRule(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	policyARN := "arn:aws:iam::" + testAccountID + ":policy/EBPublish"
	busARN := "arn:aws:events:us-east-1:" + testAccountID + ":event-bus/orders"
	customBusRuleARN := "arn:aws:events:us-east-1:" + testAccountID + ":rule/orders/order-placed"

	doc := `{"Statement":[{"Effect":"Allow","Resource":["` + busARN + `","` + customBusRuleARN + `"]}]}`

	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, policyARN, "", managedPolicyAttrs(t, doc))
	busID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsEventBus, busARN, testRegion, "{}")
	ruleID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsRule, customBusRuleARN, testRegion, "{}")

	if err := resolveIAMPolicyResources(acct, st); err != nil {
		t.Fatalf("resolveIAMPolicyResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, policyID, busID, store.RelUses)
	assertRelationship(t, rels, policyID, ruleID, store.RelUses)
}

func TestResolveIAMPolicyResources_EFSFileSystem(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	policyARN := "arn:aws:iam::" + testAccountID + ":policy/MountEFS"
	fsARN := "arn:aws:elasticfilesystem:us-east-1:" + testAccountID + ":file-system/fs-0123456789abcdef0"
	apRef := "arn:aws:elasticfilesystem:us-east-1:" + testAccountID + ":access-point/fsap-0123456789abcdef0"

	// Access-point ARN must skip — separate scanned type, not handled here.
	doc := `{"Statement":[{"Effect":"Allow","Resource":["` + fsARN + `","` + apRef + `"]}]}`

	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, policyARN, "", managedPolicyAttrs(t, doc))
	fsID := upsertTestResource(t, st, "aws", acct.ID, TypeEFSFileSystem, fsARN, testRegion, "{}")

	if err := resolveIAMPolicyResources(acct, st); err != nil {
		t.Fatalf("resolveIAMPolicyResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 edge (file-system only, access-point skipped), got %d", len(rels))
	}
	assertRelationship(t, rels, policyID, fsID, store.RelUses)
}

// --- resolveIAMRoleCrossAccountTrust (R5) ---

// TestResolveIAMRoleCrossAccountTrust verifies that a role whose trust policy
// names another AWS account (both bare-ID and ARN forms) yields one
// cross-account-trust edge per distinct foreign principal, plus a single
// foreign-account stub resource per distinct other account.
func TestResolveIAMRoleCrossAccountTrust(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	roleARN := "arn:aws:iam::" + testAccountID + ":role/cross-trusted"
	trustDoc := `{
		"Version": "2012-10-17",
		"Statement": [
			{"Effect": "Allow", "Principal": {"AWS": "arn:aws:iam::222222222222:root"}, "Action": "sts:AssumeRole"},
			{"Effect": "Allow", "Principal": {"AWS": ["333333333333", "arn:aws:iam::333333333333:user/bob"]}, "Action": "sts:AssumeRole"},
			{"Effect": "Allow", "Principal": {"AWS": "*"}, "Action": "sts:AssumeRole"},
			{"Effect": "Allow", "Principal": {"AWS": "` + testAccountID + `"}, "Action": "sts:AssumeRole"},
			{"Effect": "Deny", "Principal": {"AWS": "arn:aws:iam::444444444444:root"}, "Action": "sts:AssumeRole"}
		]
	}`
	encoded := url.QueryEscape(trustDoc)
	attrs := `{"AssumeRolePolicyDocument": "` + encoded + `"}`
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", attrs)

	if err := resolveIAMRoleCrossAccountTrust(acct, st); err != nil {
		t.Fatalf("resolveIAMRoleCrossAccountTrust: %v", err)
	}

	rels, err := st.RelationshipsFrom(roleID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	// Expect 2 distinct cross-account-trust edges (one per foreign account: 222 + 333).
	// Both 333 principals target the same foreign-account stub, so the
	// UNIQUE(from_id, to_id, kind) constraint collapses them to one edge.
	// Wildcard, self, and Deny statements all skipped.
	got := 0
	for _, r := range rels {
		if r.Kind == store.RelCrossAccountTrust {
			got++
		}
	}
	if got != 2 {
		t.Fatalf("expected 2 cross-account-trust edges, got %d (rels=%+v)", got, rels)
	}
	// Verify the stub resources exist (foreign accounts 222... and 333...).
	for _, other := range []string{"222222222222", "333333333333"} {
		stubID := store.ResourceID("aws", other, TypeIAMForeignAccount, "arn:aws:iam::"+other+":root")
		if _, err := st.GetResource(stubID); err != nil {
			t.Errorf("missing foreign-account stub for %s: %v", other, err)
		}
	}
}

// TestResolveIAMRoleCrossAccountTrust_SameAccountOnly verifies a role whose
// trust policy only names its own account (or service principals, or wildcard)
// produces no cross-account-trust edges and no foreign-account stubs.
func TestResolveIAMRoleCrossAccountTrust_SameAccountOnly(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	roleARN := "arn:aws:iam::" + testAccountID + ":role/local-only"
	trustDoc := `{
		"Statement": [
			{"Effect": "Allow", "Principal": {"AWS": "arn:aws:iam::` + testAccountID + `:role/admin"}, "Action": "sts:AssumeRole"},
			{"Effect": "Allow", "Principal": {"Service": "ec2.amazonaws.com"}, "Action": "sts:AssumeRole"}
		]
	}`
	encoded := url.QueryEscape(trustDoc)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", `{"AssumeRolePolicyDocument": "`+encoded+`"}`)

	if err := resolveIAMRoleCrossAccountTrust(acct, st); err != nil {
		t.Fatalf("resolveIAMRoleCrossAccountTrust: %v", err)
	}
	rels, err := st.RelationshipsFrom(roleID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	for _, r := range rels {
		if r.Kind == store.RelCrossAccountTrust {
			t.Errorf("unexpected cross-account-trust edge: %+v", r)
		}
	}
}

// TestResolveIAMRoleCrossAccountTrust_NoRoles verifies the resolver is a
// no-op when no IAM roles are present in the account.
func TestResolveIAMRoleCrossAccountTrust_NoRoles(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	if err := resolveIAMRoleCrossAccountTrust(acct, st); err != nil {
		t.Fatalf("resolveIAMRoleCrossAccountTrust: %v", err)
	}
}
