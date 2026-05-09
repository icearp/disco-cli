package aws

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveEC2LaunchTemplateRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	ltARN := ec2ARN(testRegion, acct.ID, "launch-template", "lt-1")
	amiARN := ec2ARN(testRegion, acct.ID, "image", "ami-1")
	ipARN := "arn:aws:iam::" + testAccountID + ":instance-profile/myProfile"
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	snARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-lt"
	attrs := `{"LaunchTemplate":{},"DefaultVersion":{"LaunchTemplateData":{` +
		`"ImageId":"ami-1","KeyName":"prodkey",` +
		`"IamInstanceProfile":{"Arn":"` + ipARN + `"},` +
		`"SecurityGroupIds":["sg-1"],` +
		`"NetworkInterfaces":[{"SubnetId":"subnet-1","Groups":["sg-1"]}],` +
		`"BlockDeviceMappings":[{"Ebs":{"KmsKeyId":"` + keyARN + `"}}]}}}`

	ltID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2LaunchTemplate, ltARN, testRegion, attrs)
	amiID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Image, amiARN, testRegion, "{}")
	ipID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMInstanceProfile, ipARN, testRegion, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, snARN, testRegion, "{}")
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	// Seed key-pair via direct upsert so Name is set (helper omits Name).
	kpName := "prodkey"
	region := testRegion
	kpARN := ec2ARN(testRegion, acct.ID, "key-pair", "key-abcd")
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: acct.ID, Type: TypeEC2KeyPair, NativeID: kpARN,
		Region: &region, Name: &kpName, AttributesJSON: "{}", DiscoveredBy: testScanID,
	}); err != nil {
		t.Fatalf("upsert keypair: %v", err)
	}
	kpID := store.ResourceID("aws", acct.ID, TypeEC2KeyPair, kpARN)

	if err := resolveEC2LaunchTemplateRefs(acct, st); err != nil {
		t.Fatalf("resolveEC2LaunchTemplateRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(ltID)
	assertRelationship(t, rels, ltID, amiID, store.RelUses)
	assertRelationship(t, rels, ltID, ipID, store.RelUses)
	assertRelationship(t, rels, ltID, kpID, store.RelUses)
	assertRelationship(t, rels, ltID, sgID, store.RelAttachedTo)
	assertRelationship(t, rels, ltID, snID, store.RelAttachedTo)
	assertRelationship(t, rels, ltID, kID, store.RelUses)
}
