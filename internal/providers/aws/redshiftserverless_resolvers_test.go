package aws

import (
	"fmt"
	"testing"

	rsstypes "github.com/aws/aws-sdk-go-v2/service/redshiftserverless/types"
	"github.com/icearp/disco-cli/store"
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

func TestResolveRSSEndpointAccessRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	wgARN := fmt.Sprintf("arn:aws:redshift-serverless:%s:%s:workgroup/wg-guid", testRegion, acct.ID)
	wgID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftServerlessWorkgroup, wgARN, testRegion,
		mustJSON(rsstypes.Workgroup{WorkgroupName: ptrStr("wg1")}))
	epARN := fmt.Sprintf("arn:aws:redshift-serverless:%s:%s:managedvpcendpoint/ep-1", testRegion, acct.ID)
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftServerlessEndpointAccess, epARN, testRegion,
		mustJSON(rsstypes.EndpointAccess{WorkgroupName: ptrStr("wg1")}))
	if err := resolveRSSEndpointAccessRefs(acct, st); err != nil {
		t.Fatalf("resolveRSSEndpointAccessRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(epID)
	assertRelationship(t, rels, epID, wgID, store.RelAttachedTo)
}

func TestResolveRSSEndpointAccessRefs_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	epARN := fmt.Sprintf("arn:aws:redshift-serverless:%s:%s:managedvpcendpoint/ep-1", testRegion, acct.ID)
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftServerlessEndpointAccess, epARN, testRegion, "{}")
	if err := resolveRSSEndpointAccessRefs(acct, st); err != nil {
		t.Fatalf("resolveRSSEndpointAccessRefs: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(epID); len(rels) != 0 {
		t.Fatalf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveRSSRecoveryPointRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	nsARN := rssNamespaceARN(testRegion, acct.ID, "ns1")
	nsID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftServerlessNamespace, nsARN, testRegion, "{}")
	rpARN := fmt.Sprintf("arn:aws:redshift-serverless:%s:%s:recovery-point/rp-1", testRegion, acct.ID)
	rpID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftServerlessRecoveryPoint, rpARN, testRegion,
		mustJSON(rsstypes.RecoveryPoint{NamespaceArn: ptrStr(nsARN)}))
	if err := resolveRSSRecoveryPointRefs(acct, st); err != nil {
		t.Fatalf("resolveRSSRecoveryPointRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rpID)
	assertRelationship(t, rels, rpID, nsID, store.RelAttachedTo)
}

func TestResolveRSSRecoveryPointRefs_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rpARN := fmt.Sprintf("arn:aws:redshift-serverless:%s:%s:recovery-point/rp-1", testRegion, acct.ID)
	rpID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftServerlessRecoveryPoint, rpARN, testRegion, "{}")
	if err := resolveRSSRecoveryPointRefs(acct, st); err != nil {
		t.Fatalf("resolveRSSRecoveryPointRefs: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(rpID); len(rels) != 0 {
		t.Fatalf("expected 0 relationships, got %d", len(rels))
	}
}
