package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

const (
	testOSDomain      = "search-prod"
	testOSVPC         = "vpc-os1111"
	testOSSubnet      = "subnet-os2222"
	testOSSG          = "sg-os3333"
	testOSKMSKeyID    = "11112222-3333-4444-5555-666677778888"
	testOSUserPool    = "us-east-1_OSpool"
	testOSIdentityID  = "us-east-1:abc-guid-123"
	testOSCognitoRole = "OSCognitoAccessRole"
	testOSLogGroup    = "/aws/opensearch/search-prod/index-slow"
)

func opensearchDomainARN() string {
	return fmt.Sprintf("arn:aws:es:%s:%s:domain/%s", testRegion, testAccountID, testOSDomain)
}

// TestResolveOpenSearchDomainTargets_HappyPath exercises every domain
// edge: VPC, subnet, security group, KMS, Cognito user-pool + identity-
// pool, IAM role, log group.
func TestResolveOpenSearchDomainTargets_HappyPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, testAccountID, "vpc", testOSVPC), testRegion, "{}")
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(testRegion, testAccountID, "subnet", testOSSubnet), testRegion, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(testRegion, testAccountID, "security-group", testOSSG), testRegion, "{}")

	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", testRegion, testAccountID, testOSKMSKeyID)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion,
		fmt.Sprintf(`{"KeyId":%q,"Arn":%q}`, testOSKMSKeyID, keyARN))

	upARN := fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/%s", testRegion, testAccountID, testOSUserPool)
	upID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoUserPool, upARN, testRegion, "{}")

	ipARN := fmt.Sprintf("arn:aws:cognito-identity:%s:%s:identitypool/%s", testRegion, testAccountID, testOSIdentityID)
	ipID := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoIdentityPool, ipARN, testRegion, "{}")

	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s", testAccountID, testOSCognitoRole)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")

	lgARN := fmt.Sprintf("arn:aws:logs:%s:%s:log-group:%s", testRegion, testAccountID, testOSLogGroup)
	lgID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, lgARN, testRegion, "{}")

	// SDK attaches `:*` suffix on log-group ARNs returned by DescribeDomain.
	domainAttrs := fmt.Sprintf(`{
		"DomainName":%q,
		"VPCOptions":{"VPCId":%q,"SubnetIds":[%q],"SecurityGroupIds":[%q]},
		"EncryptionAtRestOptions":{"Enabled":true,"KmsKeyId":%q},
		"CognitoOptions":{"Enabled":true,"UserPoolId":%q,"IdentityPoolId":%q,"RoleArn":%q},
		"LogPublishingOptions":{"INDEX_SLOW_LOGS":{"CloudWatchLogsLogGroupArn":"%s:*","Enabled":true}}
	}`, testOSDomain, testOSVPC, testOSSubnet, testOSSG, keyARN, testOSUserPool, testOSIdentityID, roleARN, lgARN)
	domainID := upsertTestResource(t, st, "aws", acct.ID, TypeOpenSearchDomain, opensearchDomainARN(), testRegion, domainAttrs)

	if err := resolveOpenSearchDomainTargets(acct, st); err != nil {
		t.Fatalf("resolveOpenSearchDomainTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(domainID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, domainID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, domainID, snID, store.RelUses)
	assertRelationship(t, rels, domainID, sgID, store.RelUses)
	assertRelationship(t, rels, domainID, kID, store.RelUses)
	assertRelationship(t, rels, domainID, upID, store.RelUses)
	assertRelationship(t, rels, domainID, ipID, store.RelUses)
	assertRelationship(t, rels, domainID, roleID, store.RelAssumes)
	assertRelationship(t, rels, domainID, lgID, store.RelUses)
}

// TestResolveOpenSearchDomainTargets_NonVPC verifies public domains (no
// VPCOptions) skip cleanly.
func TestResolveOpenSearchDomainTargets_NonVPC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	domainID := upsertTestResource(t, st, "aws", acct.ID, TypeOpenSearchDomain, opensearchDomainARN(), testRegion,
		`{"DomainName":"public-domain"}`)

	if err := resolveOpenSearchDomainTargets(acct, st); err != nil {
		t.Fatalf("resolveOpenSearchDomainTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(domainID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected zero edges for non-VPC domain, got %d", len(rels))
	}
}
