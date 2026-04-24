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
