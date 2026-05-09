package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveKMSGrants_RoleGrantee verifies a uses edge from a grant to the
// IAM role named in GranteePrincipal.
func TestResolveKMSGrants_RoleGrantee(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/grantee-role", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", `{}`)

	grantARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123/grant/g-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"GrantId":"g-1","GranteePrincipal":%q}`, roleARN)
	grantID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSGrant, grantARN, testRegion, attrs)

	if err := resolveKMSGrants(acct, st); err != nil {
		t.Fatalf("resolveKMSGrants: %v", err)
	}
	rels, err := st.RelationshipsFrom(grantID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, grantID, roleID, store.RelUses)
}

// TestResolveKMSGrants_UserAndRetiring verifies edges for both GranteePrincipal
// (IAM user) and RetiringPrincipal (IAM role).
func TestResolveKMSGrants_UserAndRetiring(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	userARN := fmt.Sprintf("arn:aws:iam::%s:user/alice", acct.ID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/retirer", acct.ID)
	userID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMUser, userARN, "", `{}`)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", `{}`)

	grantARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123/grant/g-2", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"GrantId":"g-2","GranteePrincipal":%q,"RetiringPrincipal":%q}`, userARN, roleARN)
	grantID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSGrant, grantARN, testRegion, attrs)

	if err := resolveKMSGrants(acct, st); err != nil {
		t.Fatalf("resolveKMSGrants: %v", err)
	}
	rels, err := st.RelationshipsFrom(grantID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, grantID, userID, store.RelUses)
	assertRelationship(t, rels, grantID, roleID, store.RelUses)
}

// TestResolveKMSGrants_ServicePrincipal verifies no edge + no error for
// service principals (non-ARN).
func TestResolveKMSGrants_ServicePrincipal(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	grantARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123/grant/g-3", testRegion, acct.ID)
	attrs := `{"GrantId":"g-3","GranteePrincipal":"ec2.amazonaws.com"}`
	grantID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSGrant, grantARN, testRegion, attrs)

	if err := resolveKMSGrants(acct, st); err != nil {
		t.Fatalf("resolveKMSGrants: %v", err)
	}
	rels, err := st.RelationshipsFrom(grantID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("unexpected edges for service principal: %+v", rels)
	}
}

// TestResolveKMSGrants_UnscannedPrincipal verifies FK-safe skip for cross-
// account principals not present in the store.
func TestResolveKMSGrants_UnscannedPrincipal(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	grantARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123/grant/g-4", testRegion, acct.ID)
	attrs := `{"GrantId":"g-4","GranteePrincipal":"arn:aws:iam::999999999999:role/foreign"}`
	grantID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSGrant, grantARN, testRegion, attrs)

	if err := resolveKMSGrants(acct, st); err != nil {
		t.Fatalf("resolveKMSGrants: %v", err)
	}
	rels, err := st.RelationshipsFrom(grantID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("unexpected edges for unscanned principal: %+v", rels)
	}
}

// TestResolveKMSGrants_EmptyAttrs verifies no panic/edges when neither
// principal field is set.
func TestResolveKMSGrants_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	grantARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123/grant/g-5", testRegion, acct.ID)
	upsertTestResource(t, st, "aws", acct.ID, TypeKMSGrant, grantARN, testRegion, `{"GrantId":"g-5"}`)

	if err := resolveKMSGrants(acct, st); err != nil {
		t.Fatalf("resolveKMSGrants: %v", err)
	}
}

// TestResolveKMSGrantEncryptionContext_Lambda verifies a uses edge from a
// grant to the Lambda function named in
// `Constraints.EncryptionContextEquals["aws:lambda:FunctionArn"]`.
func TestResolveKMSGrantEncryptionContext_Lambda(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:my-fn", testRegion, acct.ID)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, `{}`)

	grantARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123/grant/g-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"GrantId":"g-1","Constraints":{"EncryptionContextEquals":{"aws:lambda:FunctionArn":%q}}}`, fnARN)
	grantID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSGrant, grantARN, testRegion, attrs)

	if err := resolveKMSGrantEncryptionContext(acct, st); err != nil {
		t.Fatalf("resolveKMSGrantEncryptionContext: %v", err)
	}
	rels, err := st.RelationshipsFrom(grantID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, grantID, fnID, store.RelUses)
}

// TestResolveKMSGrantEncryptionContext_UnscannedFunction confirms FK-safe
// skip when the named function has no row in the local store.
func TestResolveKMSGrantEncryptionContext_UnscannedFunction(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	grantARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123/grant/g-1", testRegion, acct.ID)
	attrs := `{"GrantId":"g-1","Constraints":{"EncryptionContextEquals":{"aws:lambda:FunctionArn":"arn:aws:lambda:us-east-1:131546573061:function:missing"}}}`
	grantID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSGrant, grantARN, testRegion, attrs)

	if err := resolveKMSGrantEncryptionContext(acct, st); err != nil {
		t.Fatalf("resolveKMSGrantEncryptionContext: %v", err)
	}
	rels, _ := st.RelationshipsFrom(grantID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships for unscanned target, got %d", len(rels))
	}
}

// TestResolveKMSGrantEncryptionContext_EBSVolume verifies a uses edge from a
// grant to the EC2 volume named (bare ID) under
// `Constraints.EncryptionContextSubset["aws:ebs:id"]`.
func TestResolveKMSGrantEncryptionContext_EBSVolume(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	volARN := ec2ARN(testRegion, acct.ID, "volume", "vol-abc")
	volID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Volume, volARN, testRegion, `{}`)

	grantARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123/grant/g-ebs", testRegion, acct.ID)
	attrs := `{"GrantId":"g-ebs","Constraints":{"EncryptionContextSubset":{"aws:ebs:id":"vol-abc"}}}`
	grantID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSGrant, grantARN, testRegion, attrs)

	if err := resolveKMSGrantEncryptionContext(acct, st); err != nil {
		t.Fatalf("resolveKMSGrantEncryptionContext: %v", err)
	}
	rels, err := st.RelationshipsFrom(grantID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, grantID, volID, store.RelUses)
}

// TestResolveKMSGrantEncryptionContext_EBSVolumeUnscanned confirms FK-safe
// skip when the named volume has no row in the local store.
func TestResolveKMSGrantEncryptionContext_EBSVolumeUnscanned(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	grantARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123/grant/g-ebs2", testRegion, acct.ID)
	attrs := `{"GrantId":"g-ebs2","Constraints":{"EncryptionContextSubset":{"aws:ebs:id":"vol-missing"}}}`
	grantID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSGrant, grantARN, testRegion, attrs)

	if err := resolveKMSGrantEncryptionContext(acct, st); err != nil {
		t.Fatalf("resolveKMSGrantEncryptionContext: %v", err)
	}
	rels, _ := st.RelationshipsFrom(grantID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// TestResolveKMSGrantEncryptionContext_EmptyConstraints confirms no panic and
// no edges when the grant carries no Constraints block.
func TestResolveKMSGrantEncryptionContext_EmptyConstraints(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	grantARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123/grant/g-1", testRegion, acct.ID)
	grantID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSGrant, grantARN, testRegion, `{"GrantId":"g-1"}`)

	if err := resolveKMSGrantEncryptionContext(acct, st); err != nil {
		t.Fatalf("resolveKMSGrantEncryptionContext: %v", err)
	}
	rels, _ := st.RelationshipsFrom(grantID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
