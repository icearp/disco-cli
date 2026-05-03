package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveRSSNamespaceRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	kARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc", testRegion, acct.ID)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, kARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/rs-svc", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	nsARN := rssNamespaceARN(testRegion, acct.ID, "ns1")
	attrs := fmt.Sprintf(`{"KmsKeyId":"%s","IamRoles":["%s"]}`, kARN, roleARN)
	nsID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftServerlessNamespace, nsARN, testRegion, attrs)
	if err := resolveRSSNamespaceRefs(acct, st); err != nil {
		t.Fatalf("resolveRSSNamespaceRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(nsID)
	assertRelationship(t, rels, nsID, kID, store.RelUses)
	assertRelationship(t, rels, nsID, roleID, store.RelUses)
}

func TestResolveRSSWorkgroupRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	nsARN := rssNamespaceARN(testRegion, acct.ID, "ns1")
	nsID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftServerlessNamespace, nsARN, testRegion, "{}")
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")
	wgARN := fmt.Sprintf("arn:aws:redshift-serverless:%s:%s:workgroup/wg1", testRegion, acct.ID)
	attrs := `{"NamespaceName":"ns1","SubnetIds":["subnet-1"],"SecurityGroupIds":["sg-1"]}`
	wgID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftServerlessWorkgroup, wgARN, testRegion, attrs)
	if err := resolveRSSWorkgroupRefs(acct, st); err != nil {
		t.Fatalf("resolveRSSWorkgroupRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(wgID)
	assertRelationship(t, rels, wgID, nsID, store.RelAttachedTo)
	assertRelationship(t, rels, wgID, subID, store.RelAttachedTo)
	assertRelationship(t, rels, wgID, sgID, store.RelAttachedTo)
}

func TestResolveRSSSnapshotRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	nsARN := rssNamespaceARN(testRegion, acct.ID, "ns1")
	nsID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftServerlessNamespace, nsARN, testRegion, "{}")
	kARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc", testRegion, acct.ID)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, kARN, testRegion, "{}")
	snARN := fmt.Sprintf("arn:aws:redshift-serverless:%s:%s:snapshot/sn1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"NamespaceName":"ns1","KmsKeyId":"%s"}`, kARN)
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftServerlessSnapshot, snARN, testRegion, attrs)
	if err := resolveRSSSnapshotRefs(acct, st); err != nil {
		t.Fatalf("resolveRSSSnapshotRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(snID)
	assertRelationship(t, rels, snID, nsID, store.RelAttachedTo)
	assertRelationship(t, rels, snID, kID, store.RelUses)
}
