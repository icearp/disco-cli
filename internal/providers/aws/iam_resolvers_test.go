package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/icearp/disco-cli/store"
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

	rels, err := st.RelationshipsFrom(roleID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, roleID, profileID, "contains")
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

// TestResolveManagedPolicyAttachments_IncludesAWSManaged is the regression ratchet
// ensuring attachment edges reach AWS-managed policies (and service-linked roles),
// not just customer-managed ones. Reads each principal's GAAD AttachedManagedPolicies
// from stored attrs — no API calls.
func TestResolveManagedPolicyAttachments_IncludesAWSManaged(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	awsManagedARN := "arn:aws:iam::aws:policy/AdministratorAccess"
	customerARN := "arn:aws:iam::123456789012:policy/my-custom"
	bogusARN := "arn:aws:iam::aws:policy/NotScanned" // referenced but never scanned
	roleARN := "arn:aws:iam::123456789012:role/app-role"
	slrARN := "arn:aws:iam::123456789012:role/aws-service-role/elasticbeanstalk.amazonaws.com/AWSServiceRoleForElasticBeanstalk"

	// AWS-managed policy + SLR carry ManagedByProvider=true, so they only surface
	// when the resolver reads them with IncludeManaged — upsert directly to set it.
	upsertManaged := func(rtype, nativeID, attrs string) string {
		t.Helper()
		if _, err := st.UpsertResource(&store.Resource{
			Provider: "aws", AccountID: acct.ID, Type: rtype, NativeID: nativeID,
			AttributesJSON: attrs, DiscoveredBy: testScanID, ManagedByProvider: true,
		}); err != nil {
			t.Fatalf("upsert managed %s: %v", nativeID, err)
		}
		return store.ResourceID("aws", acct.ID, nativeID)
	}
	roleAttrs := func(arn string, policyARNs ...string) string {
		amp := make([]iamtypes.AttachedPolicy, len(policyARNs))
		for i, p := range policyARNs {
			amp[i] = iamtypes.AttachedPolicy{PolicyArn: sdkaws.String(p)}
		}
		return mustJSON(iamtypes.RoleDetail{Arn: sdkaws.String(arn), AttachedManagedPolicies: amp})
	}

	awsPolicyID := upsertManaged(TypeIAMPolicy, awsManagedARN, "{}")
	custPolicyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, customerARN, "", "{}")
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", roleAttrs(roleARN, awsManagedARN, customerARN, bogusARN))
	slrID := upsertManaged(TypeIAMServiceLinkedRole, slrARN, roleAttrs(slrARN, awsManagedARN))

	if err := resolveManagedPolicyAttachments(acct, st); err != nil {
		t.Fatalf("resolveManagedPolicyAttachments: %v", err)
	}

	// The AWS-managed policy is attached to both the role and the SLR — the edges
	// the old customer-only resolver never produced. bogusARN yields no edge.
	awsRels, err := st.RelationshipsFrom(awsPolicyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(aws-managed): %v", err)
	}
	assertRelationship(t, awsRels, awsPolicyID, roleID, "attached-to")
	assertRelationship(t, awsRels, awsPolicyID, slrID, "attached-to")
	if len(awsRels) != 2 {
		t.Errorf("aws-managed policy: got %d edges, want 2 (role + SLR; bogus ARN skipped)", len(awsRels))
	}

	// Customer-managed attachment still works (no regression).
	custRels, err := st.RelationshipsFrom(custPolicyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(customer): %v", err)
	}
	assertRelationship(t, custRels, custPolicyID, roleID, "attached-to")
}

// --- resolveUserGroupMemberships (empty store — no API calls made) ---

func TestResolveUserGroupMemberships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	if err := resolveUserGroupMemberships(acct, st); err != nil {
		t.Fatalf("resolveUserGroupMemberships: %v", err)
	}
}

// TestResolveUserGroupMemberships_FromGroupList verifies memberships build from
// the user's stored GAAD GroupList (group names) with no API calls: a group →
// user contains edge appears per scanned group, and an unscanned group name
// skips FK-safe.
func TestResolveUserGroupMemberships_FromGroupList(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	userARN := "arn:aws:iam::123456789012:user/alice"
	adminsARN := "arn:aws:iam::123456789012:group/Admins"
	devsARN := "arn:aws:iam::123456789012:group/division/Devs" // non-default path

	// Groups carry Name (set by the scanner from GAAD GroupDetail); the helper
	// does not set Name, so upsert directly to populate the name index source.
	upsertGroup := func(arn, name string) string {
		t.Helper()
		region := ""
		if _, err := st.UpsertResource(&store.Resource{
			Provider: "aws", AccountID: acct.ID, Type: TypeIAMGroup, NativeID: arn,
			Name: &name, Region: &region, DiscoveredBy: testScanID,
		}); err != nil {
			t.Fatalf("upsert group %s: %v", name, err)
		}
		return store.ResourceID("aws", acct.ID, arn)
	}
	adminsID := upsertGroup(adminsARN, "Admins")
	devsID := upsertGroup(devsARN, "Devs")

	// GroupList names both scanned groups plus one that was never scanned.
	userAttrs := mustJSON(iamtypes.UserDetail{
		Arn:       sdkaws.String(userARN),
		GroupList: []string{"Admins", "Devs", "GhostGroup"},
	})
	userID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMUser, userARN, "", userAttrs)

	if err := resolveUserGroupMemberships(acct, st); err != nil {
		t.Fatalf("resolveUserGroupMemberships: %v", err)
	}

	for _, groupID := range []string{adminsID, devsID} {
		rels, err := st.RelationshipsFrom(groupID)
		if err != nil {
			t.Fatalf("RelationshipsFrom(%s): %v", groupID, err)
		}
		assertRelationship(t, rels, groupID, userID, "contains")
		if len(rels) != 1 {
			t.Errorf("group %s: got %d edges, want 1", groupID, len(rels))
		}
	}
	// GhostGroup was never scanned — no row, so no edge (FK-safe skip, no error).
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

	// Reference the table via an index ARN (child-suffix trimming) and the
	// secret via a versioned ARN (version-segment trimming).
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

// TestResolveIAMRoleCrossAccountTrust verifies a role whose trust policy names
// another AWS account (bare-ID or ARN form) yields one cross-account-trust
// edge per distinct foreign principal, plus a single empty-attribute
// aws:iam:account placeholder per distinct other account.
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
	// Both 333 principals target the same account placeholder, so the
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
	// Each referenced account exists as an empty-attribute aws:iam:account
	// placeholder at its self-node natural key, and the edge points at it.
	for _, other := range []string{"222222222222", "333333333333"} {
		wantID := store.ResourceID("aws", other, "arn:aws:iam::"+other+":root")
		r, err := st.GetResource(wantID)
		if err != nil {
			t.Errorf("missing account placeholder for %s: %v", other, err)
			continue
		}
		if r.AttributesJSON != "{}" {
			t.Errorf("placeholder for %s: attributes = %q, want empty {}", other, r.AttributesJSON)
		}
		assertRelationship(t, rels, roleID, wantID, store.RelCrossAccountTrust)
	}
}

// TestResolveIAMRoleCrossAccountTrust_DoesNotClobberScannedAccount verifies the
// placeholder insert is FK-safe but non-destructive: when the referenced
// account's self-node is already fully populated (scanned), the resolver
// leaves its attributes intact and still points the edge at it.
func TestResolveIAMRoleCrossAccountTrust_DoesNotClobberScannedAccount(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	const other = "222222222222"
	// Seed the foreign account as an already-scanned, populated self-node.
	populated := `{"SummaryMap":{"Users":5}}`
	scannedID := upsertTestResource(t, st, "aws", other, TypeIAMAccount,
		"arn:aws:iam::"+other+":root", "global", populated)

	roleARN := "arn:aws:iam::" + testAccountID + ":role/cross-trusted"
	trustDoc := `{"Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::` + other + `:root"},"Action":"sts:AssumeRole"}]}`
	encoded := url.QueryEscape(trustDoc)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", `{"AssumeRolePolicyDocument":"`+encoded+`"}`)

	if err := resolveIAMRoleCrossAccountTrust(acct, st); err != nil {
		t.Fatalf("resolveIAMRoleCrossAccountTrust: %v", err)
	}

	r, err := st.GetResource(scannedID)
	if err != nil {
		t.Fatalf("GetResource scanned account: %v", err)
	}
	if r.AttributesJSON != populated {
		t.Errorf("scanned account clobbered: attributes = %q, want %q", r.AttributesJSON, populated)
	}
	rels, err := st.RelationshipsFrom(roleID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, roleID, scannedID, store.RelCrossAccountTrust)
}

// TestResolveIAMRoleCrossAccountTrust_SameAccountOnly verifies a role whose
// trust policy only names its own account (or service principals, or wildcard)
// produces no cross-account-trust edges and no account placeholders.
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

// iamTrustRoleAttrs is a role's attributes whose trust policy allows account
// 222222222222, URL-encoded as the SDK returns it.
func iamTrustRoleAttrs() string {
	doc := `{"Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::222222222222:root"},"Action":"sts:AssumeRole"}]}`
	return mustJSON(iamtypes.Role{AssumeRolePolicyDocument: sdkaws.String(url.QueryEscape(doc))})
}

// A role whose attributes, trust-policy escaping or trust-policy JSON does not
// decode is reported, each with its own cause, and the roles listed before
// and after them are still wired. Names fix the listing order (ListResources
// orders by type, then name).
func TestResolveIAMRoleCrossAccountTrust_UndecodableReportedOthersWired(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := func(n string) string { return "arn:aws:iam::" + testAccountID + ":role/" + n }
	before := upsertTestResourceNamed(t, st, TypeIAMRole, roleARN("a"), "", iamTrustRoleAttrs(), "a")
	upsertTestResourceNamed(t, st, TypeIAMRole, roleARN("b1"), "", `{"AssumeRolePolicyDocument":7}`, "b1")
	upsertTestResourceNamed(t, st, TypeIAMRole, roleARN("b2"), "",
		mustJSON(iamtypes.Role{AssumeRolePolicyDocument: sdkaws.String("%zz")}), "b2")
	upsertTestResourceNamed(t, st, TypeIAMRole, roleARN("b3"), "",
		mustJSON(iamtypes.Role{AssumeRolePolicyDocument: sdkaws.String(url.QueryEscape(`{"Statement":`))}), "b3")
	after := upsertTestResourceNamed(t, st, TypeIAMRole, roleARN("c"), "", iamTrustRoleAttrs(), "c")

	err := resolveIAMRoleCrossAccountTrust(acct, st)
	var (
		typeErr   *json.UnmarshalTypeError
		escapeErr url.EscapeError
		syntaxErr *json.SyntaxError
	)
	if !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want b1's JSON type error", err)
	}
	if !errors.As(err, &escapeErr) {
		t.Errorf("err = %v, want b2's URL escape error", err)
	}
	if !errors.As(err, &syntaxErr) {
		t.Errorf("err = %v, want b3's JSON syntax error", err)
	}
	wantID := store.ResourceID("aws", "222222222222", "arn:aws:iam::222222222222:root")
	for _, id := range []string{before, after} {
		rels, err := st.RelationshipsFrom(id)
		if err != nil {
			t.Fatalf("RelationshipsFrom: %v", err)
		}
		assertRelationship(t, rels, id, wantID, store.RelCrossAccountTrust)
	}
}

// With no role producing an edge, a decode error is still returned rather
// than lost in the no-edges early return.
func TestResolveIAMRoleCrossAccountTrust_OnlyUndecodableRoleReported(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, "arn:aws:iam::"+testAccountID+":role/bad", "", `{"AssumeRolePolicyDocument":7}`)

	var typeErr *json.UnmarshalTypeError
	if err := resolveIAMRoleCrossAccountTrust(acct, st); !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want a JSON type error", err)
	}
}

// TestResolveIAMRoleCrossAccountTrust_NoRoles verifies the resolver no-ops
// when no IAM roles are present in the account.
func TestResolveIAMRoleCrossAccountTrust_NoRoles(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	if err := resolveIAMRoleCrossAccountTrust(acct, st); err != nil {
		t.Fatalf("resolveIAMRoleCrossAccountTrust: %v", err)
	}
}

// TestResolveIAMPermissionBoundaries — role + user with PermissionsBoundary set
// emit `bounded-by` edge to in-scope policy. Boundary pointing at unscanned
// policy skips silently. No-boundary skips cleanly.
func TestResolveIAMPermissionBoundaries(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	policyARN := "arn:aws:iam::123456789012:policy/MyBoundary"
	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, policyARN, "", "{}")

	roleARN := "arn:aws:iam::123456789012:role/with-boundary"
	roleAttrs := `{"PermissionsBoundary":{"PermissionsBoundaryArn":"` + policyARN + `","PermissionsBoundaryType":"Policy"}}`
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", roleAttrs)

	userARN := "arn:aws:iam::123456789012:user/no-boundary"
	upsertTestResource(t, st, "aws", acct.ID, TypeIAMUser, userARN, "", "{}")

	roleARNUnscanned := "arn:aws:iam::123456789012:role/unscanned-boundary"
	roleAttrsUnscanned := `{"PermissionsBoundary":{"PermissionsBoundaryArn":"arn:aws:iam::123456789012:policy/Unscanned","PermissionsBoundaryType":"Policy"}}`
	roleIDUnscanned := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARNUnscanned, "", roleAttrsUnscanned)

	if err := resolveIAMPermissionBoundaries(acct, st); err != nil {
		t.Fatalf("resolveIAMPermissionBoundaries: %v", err)
	}

	rels, err := st.RelationshipsFrom(roleID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != policyID || rels[0].Kind != string(store.RelBoundedBy) {
		t.Errorf("role bounded-by edge wrong: %+v", rels)
	}

	relsUnscanned, _ := st.RelationshipsFrom(roleIDUnscanned)
	if len(relsUnscanned) != 0 {
		t.Errorf("unscanned-boundary role should emit zero edges, got %+v", relsUnscanned)
	}
}

// A principal whose attributes do not parse is reported, and the principals
// listed before and after it are still wired. Names a, b, c fix the listing
// order (ListResources orders by type, then name).
func TestResolveIAMPermissionBoundaries_BadAttrsReportedOthersWired(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	policyARN := "arn:aws:iam::" + testAccountID + ":policy/MyBoundary"
	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, policyARN, "", "{}")
	roleARN := func(n string) string { return "arn:aws:iam::" + testAccountID + ":role/" + n }
	attrs := mustJSON(iamtypes.Role{PermissionsBoundary: &iamtypes.AttachedPermissionsBoundary{
		PermissionsBoundaryArn: sdkaws.String(policyARN), PermissionsBoundaryType: iamtypes.PermissionsBoundaryAttachmentTypePolicy,
	}})
	before := upsertTestResourceNamed(t, st, TypeIAMRole, roleARN("a"), "", attrs, "a")
	upsertTestResourceNamed(t, st, TypeIAMRole, roleARN("b"), "", `{"PermissionsBoundary":7}`, "b")
	after := upsertTestResourceNamed(t, st, TypeIAMRole, roleARN("c"), "", attrs, "c")

	var typeErr *json.UnmarshalTypeError
	if err := resolveIAMPermissionBoundaries(acct, st); !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want a JSON type error", err)
	}
	for _, id := range []string{before, after} {
		rels, err := st.RelationshipsFrom(id)
		if err != nil {
			t.Fatalf("RelationshipsFrom: %v", err)
		}
		assertRelationship(t, rels, id, policyID, store.RelBoundedBy)
	}
}

// --- resolveIAMDelegationRequestBoundaries ---

func TestResolveIAMDelegationRequestBoundaries(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, iamTestBoundaryARN, "global", "{}")
	attrs := mustJSON(iamtypes.DelegationRequest{
		DelegationRequestId:           ptrStr("dr-1"),
		RolePermissionRestrictionArns: []string{iamTestBoundaryARN, "arn:aws:iam::" + testAccountID + ":policy/unscanned"},
	})
	reqID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMDelegationRequest, iamTestDelegationNativeID("dr-1"), "global", attrs)
	bareID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMDelegationRequest, iamTestDelegationNativeID("dr-2"), "global", "{}")

	if err := resolveIAMDelegationRequestBoundaries(acct, st); err != nil {
		t.Fatalf("resolveIAMDelegationRequestBoundaries: %v", err)
	}
	rels, err := st.RelationshipsFrom(reqID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != policyID || rels[0].Kind != store.RelBoundedBy {
		t.Errorf("dr-1 edges = %+v, want one bounded-by to the stored boundary only", rels)
	}
	if bare, _ := st.RelationshipsFrom(bareID); len(bare) != 0 {
		t.Errorf("dr-2 with no boundaries has edges %+v, want none", bare)
	}
}

// An AWS-managed boundary policy is stored ManagedByProvider and still gets
// its edge.
func TestResolveIAMDelegationRequestBoundaries_AWSManagedPolicy(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	managedARN := "arn:aws:iam::aws:policy/PowerUserAccess"
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: acct.ID, Type: TypeIAMPolicy, NativeID: managedARN,
		Region: regionGlobal, AttributesJSON: "{}", DiscoveredBy: testScanID, ManagedByProvider: true,
	}); err != nil {
		t.Fatalf("seed managed policy: %v", err)
	}
	attrs := mustJSON(iamtypes.DelegationRequest{RolePermissionRestrictionArns: []string{managedARN}})
	reqID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMDelegationRequest, iamTestDelegationNativeID("dr-1"), "global", attrs)

	if err := resolveIAMDelegationRequestBoundaries(acct, st); err != nil {
		t.Fatalf("resolveIAMDelegationRequestBoundaries: %v", err)
	}
	rels, err := st.RelationshipsFrom(reqID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, reqID, store.ResourceID("aws", acct.ID, managedARN), store.RelBoundedBy)
}

// A row whose attributes do not decode fails the resolver without costing a
// later sibling its edge (ListResources orders by name, so dr-0 comes first).
func TestResolveIAMDelegationRequestBoundaries_DecodeErrorKeepsSiblings(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMPolicy, iamTestBoundaryARN, "global", "{}")
	attrs := mustJSON(iamtypes.DelegationRequest{RolePermissionRestrictionArns: []string{iamTestBoundaryARN}})
	reqID := upsertTestResourceNamed(t, st, TypeIAMDelegationRequest, iamTestDelegationNativeID("dr-1"), "global", attrs, "dr-1")
	upsertTestResourceNamed(t, st, TypeIAMDelegationRequest, iamTestDelegationNativeID("dr-0"), "global", `{"RolePermissionRestrictionArns":"not-a-list"}`, "dr-0")

	var typeErr *json.UnmarshalTypeError
	if err := resolveIAMDelegationRequestBoundaries(acct, st); !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want the dr-0 decode error", err)
	}
	rels, err := st.RelationshipsFrom(reqID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, reqID, policyID, store.RelBoundedBy)
}

// --- resolveIAMDelegationRequestRequestors ---

const iamTestPartnerAccount = "222222222222"

func iamTestPartnerAccountID() string {
	return store.ResourceID("aws", iamTestPartnerAccount, "arn:aws:iam::"+iamTestPartnerAccount+":root")
}

func iamTestSeedDelegationRequest(t *testing.T, st *store.Store, id string, requestor *string) string {
	t.Helper()
	attrs := mustJSON(iamtypes.DelegationRequest{DelegationRequestId: ptrStr(id), RequestorId: requestor})
	return upsertTestResourceNamed(t, st, TypeIAMDelegationRequest, iamTestDelegationNativeID(id), "global", attrs, id)
}

func iamTestAccountRows(t *testing.T, st *store.Store) []store.Resource {
	t.Helper()
	rows, err := st.ListResources(store.ResourceFilter{Types: []string{TypeIAMAccount}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources accounts: %v", err)
	}
	return rows
}

// The only edge from a request is attached-to its requestor account with
// role=requestor, and nothing points back at the request.
func iamTestAssertRequestorEdge(t *testing.T, st *store.Store, reqID string) {
	t.Helper()
	rels, err := st.RelationshipsFrom(reqID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].ToID != iamTestPartnerAccountID() || rels[0].Kind != store.RelAttachedTo {
		t.Fatalf("edges from %s = %+v, want one attached-to the partner account", reqID, rels)
	}
	var edgeAttrs map[string]string
	if err := json.Unmarshal([]byte(sv(rels[0].Attributes)), &edgeAttrs); err != nil || len(edgeAttrs) != 1 || edgeAttrs["role"] != "requestor" {
		t.Errorf("edge attributes = %s (%v), want {\"role\":\"requestor\"}", sv(rels[0].Attributes), err)
	}
	back, err := st.RelationshipsTo(reqID)
	if err != nil {
		t.Fatalf("RelationshipsTo: %v", err)
	}
	if len(back) != 0 {
		t.Errorf("edges into request %s = %+v, want none", reqID, back)
	}
}

func TestResolveIAMDelegationRequestRequestors_ScannedAccountKeptIntact(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	populated := `{"SummaryMap":{"Users":5}}`
	scannedID := upsertTestResource(t, st, "aws", iamTestPartnerAccount, TypeIAMAccount,
		"arn:aws:iam::"+iamTestPartnerAccount+":root", "global", populated)
	reqID := iamTestSeedDelegationRequest(t, st, "dr-1", ptrStr(iamTestPartnerAccount))

	if err := resolveIAMDelegationRequestRequestors(acct, st); err != nil {
		t.Fatalf("resolveIAMDelegationRequestRequestors: %v", err)
	}
	iamTestAssertRequestorEdge(t, st, reqID)
	rows := iamTestAccountRows(t, st)
	if len(rows) != 1 || rows[0].ID != scannedID {
		t.Fatalf("account rows = %+v, want only the scanned account", rows)
	}
	if rows[0].AttributesJSON != populated || rows[0].ReferenceOnly {
		t.Errorf("scanned account changed: attributes %s, referenceOnly %v; want %s, false",
			rows[0].AttributesJSON, rows[0].ReferenceOnly, populated)
	}
}

// Two requests from one partner get one placeholder, and a second resolver run
// neither adds a row nor changes the first.
func TestResolveIAMDelegationRequestRequestors_ExternalAccountPlaceholder(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	req1 := iamTestSeedDelegationRequest(t, st, "dr-1", ptrStr(iamTestPartnerAccount))
	req2 := iamTestSeedDelegationRequest(t, st, "dr-2", ptrStr(iamTestPartnerAccount))

	for run := 1; run <= 2; run++ {
		if err := resolveIAMDelegationRequestRequestors(acct, st); err != nil {
			t.Fatalf("run %d: resolveIAMDelegationRequestRequestors: %v", run, err)
		}
		rows := iamTestAccountRows(t, st)
		if len(rows) != 1 {
			t.Fatalf("run %d: account rows = %+v, want one placeholder", run, rows)
		}
		p := rows[0]
		if p.ID != iamTestPartnerAccountID() || p.AccountID != iamTestPartnerAccount ||
			sv(p.Name) != iamTestPartnerAccount || sv(p.Region) != "global" ||
			p.AttributesJSON != "{}" || !p.ReferenceOnly || p.DiscoveredBy != testScanID {
			t.Errorf("run %d: placeholder = %+v, want the partner self-node, name and account %s, region global, {} attributes, reference-only, discovered by %s",
				run, p, iamTestPartnerAccount, testScanID)
		}
		iamTestAssertRequestorEdge(t, st, req1)
		iamTestAssertRequestorEdge(t, st, req2)
	}
}

func TestResolveIAMDelegationRequestRequestors_NoRequestor(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	reqID := iamTestSeedDelegationRequest(t, st, "dr-1", nil)

	if err := resolveIAMDelegationRequestRequestors(acct, st); err != nil {
		t.Fatalf("resolveIAMDelegationRequestRequestors: %v", err)
	}
	rels, err := st.RelationshipsFrom(reqID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("edges = %+v, want none for a request with no RequestorId", rels)
	}
	if rows := iamTestAccountRows(t, st); len(rows) != 0 {
		t.Errorf("account rows = %+v, want no placeholder", rows)
	}
}

// A request naming the scanned account whose own row was not stored (its
// read failed, or aws:iam was not scanned) gets a reference-only placeholder,
// so its edge names a row that exists; a stored self row is kept intact.
func TestResolveIAMDelegationRequestRequestors_SelfAccount(t *testing.T) {
	selfNative := "arn:aws:iam::" + testAccountID + ":root"
	selfID := store.ResourceID("aws", testAccountID, selfNative)
	for _, scanned := range []bool{false, true} {
		t.Run(fmt.Sprintf("scanned=%v", scanned), func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			populated := `{"SummaryMap":{"Users":5}}`
			if scanned {
				upsertTestResource(t, st, "aws", testAccountID, TypeIAMAccount, selfNative, "global", populated)
			}
			reqID := iamTestSeedDelegationRequest(t, st, "dr-1", ptrStr(testAccountID))

			if err := resolveIAMDelegationRequestRequestors(acct, st); err != nil {
				t.Fatalf("resolveIAMDelegationRequestRequestors: %v", err)
			}
			rels, err := st.RelationshipsFrom(reqID)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			if len(rels) != 1 || rels[0].ToID != selfID || rels[0].Kind != store.RelAttachedTo {
				t.Errorf("edges = %+v, want one attached-to the self account %s", rels, selfID)
			}
			rows := iamTestAccountRows(t, st)
			if len(rows) != 1 || rows[0].ID != selfID {
				t.Fatalf("account rows = %+v, want the self account %s only", rows, selfID)
			}
			if scanned && (rows[0].AttributesJSON != populated || rows[0].ReferenceOnly) {
				t.Errorf("scanned self account changed: attributes %s, referenceOnly %v; want %s, false",
					rows[0].AttributesJSON, rows[0].ReferenceOnly, populated)
			}
			if !scanned && (rows[0].AttributesJSON != "{}" || !rows[0].ReferenceOnly) {
				t.Errorf("self placeholder: attributes %s, referenceOnly %v; want {}, true",
					rows[0].AttributesJSON, rows[0].ReferenceOnly)
			}
		})
	}
}

// A row whose attributes do not decode fails the resolver without costing a
// later sibling its edge or placeholder (ListResources orders by name).
func TestResolveIAMDelegationRequestRequestors_DecodeErrorKeepsSiblings(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResourceNamed(t, st, TypeIAMDelegationRequest, iamTestDelegationNativeID("dr-0"), "global", `{"RequestorId":7}`, "dr-0")
	reqID := iamTestSeedDelegationRequest(t, st, "dr-1", ptrStr(iamTestPartnerAccount))

	var typeErr *json.UnmarshalTypeError
	if err := resolveIAMDelegationRequestRequestors(acct, st); !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want the dr-0 decode error", err)
	}
	iamTestAssertRequestorEdge(t, st, reqID)
}
