package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveWSWPortalRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	bsARN := fmt.Sprintf("arn:aws:workspaces-web:%s:%s:browserSettings/bs1", testRegion, acct.ID)
	bsID := upsertTestResource(t, st, "aws", acct.ID, TypeWSWBrowserSettings, bsARN, testRegion, "{}")
	nsARN := fmt.Sprintf("arn:aws:workspaces-web:%s:%s:networkSettings/ns1", testRegion, acct.ID)
	nsID := upsertTestResource(t, st, "aws", acct.ID, TypeWSWNetworkSettings, nsARN, testRegion, "{}")
	pARN := fmt.Sprintf("arn:aws:workspaces-web:%s:%s:portal/p1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"BrowserSettingsArn":%q,"NetworkSettingsArn":%q}`, bsARN, nsARN)
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeWSWPortal, pARN, testRegion, attrs)
	if err := resolveWSWPortalRefs(acct, st); err != nil {
		t.Fatalf("resolveWSWPortalRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, bsID, store.RelUses)
	assertRelationship(t, rels, pID, nsID, store.RelUses)
}

func TestResolveWSWNetworkSettingsRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vARN, testRegion, "{}")
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")
	nsARN := fmt.Sprintf("arn:aws:workspaces-web:%s:%s:networkSettings/ns1", testRegion, acct.ID)
	attrs := `{"VpcId":"vpc-1","SubnetIds":["subnet-1"],"SecurityGroupIds":["sg-1"]}`
	nsID := upsertTestResource(t, st, "aws", acct.ID, TypeWSWNetworkSettings, nsARN, testRegion, attrs)
	if err := resolveWSWNetworkSettingsRefs(acct, st); err != nil {
		t.Fatalf("resolveWSWNetworkSettingsRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(nsID)
	assertRelationship(t, rels, nsID, vID, store.RelAttachedTo)
	assertRelationship(t, rels, nsID, subID, store.RelAttachedTo)
	assertRelationship(t, rels, nsID, sgID, store.RelUses)
}

func TestResolveWSWUserAccessLoggingKinesis(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ksARN := fmt.Sprintf("arn:aws:kinesis:%s:%s:stream/audit", testRegion, acct.ID)
	ksID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisStream, ksARN, testRegion, "{}")
	ualARN := fmt.Sprintf("arn:aws:workspaces-web:%s:%s:userAccessLoggingSettings/ual1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"KinesisStreamArn":%q}`, ksARN)
	ualID := upsertTestResource(t, st, "aws", acct.ID, TypeWSWUserAccessLoggingSettings, ualARN, testRegion, attrs)
	if err := resolveWSWUserAccessLoggingKinesis(acct, st); err != nil {
		t.Fatalf("resolveWSWUserAccessLoggingKinesis: %v", err)
	}
	rels, _ := st.RelationshipsFrom(ualID)
	assertRelationship(t, rels, ualID, ksID, store.RelRoutesTo)
}

func TestResolveWSWIdentityProviderPortal(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pARN := fmt.Sprintf("arn:aws:workspaces-web:%s:%s:portal/abc-uuid", testRegion, acct.ID)
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeWSWPortal, pARN, testRegion, "{}")
	idpARN := fmt.Sprintf("arn:aws:workspaces-web:%s:%s:identityProvider/abc-uuid/idp-uuid", testRegion, acct.ID)
	idpID := upsertTestResource(t, st, "aws", acct.ID, TypeWSWIdentityProvider, idpARN, testRegion, "{}")
	if err := resolveWSWIdentityProviderPortal(acct, st); err != nil {
		t.Fatalf("resolveWSWIdentityProviderPortal: %v", err)
	}
	rels, _ := st.RelationshipsFrom(idpID)
	assertRelationship(t, rels, idpID, pID, store.RelAttachedTo)
}

func TestResolveWSWSettingsKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	bsARN := "arn:aws:workspaces-web:us-east-1:" + testAccountID + ":browserSettings/bs-1"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-wsw"
	attrs := `{"CustomerManagedKey":"` + keyARN + `"}`

	bsID := upsertTestResource(t, st, "aws", acct.ID, TypeWSWBrowserSettings, bsARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveWSWSettingsKMS(acct, st); err != nil {
		t.Fatalf("resolveWSWSettingsKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(bsID)
	assertRelationship(t, rels, bsID, kID, store.RelUses)
}
