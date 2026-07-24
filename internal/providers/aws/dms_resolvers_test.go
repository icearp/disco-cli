package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveDMSEndpointRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	certARN := fmt.Sprintf("arn:aws:dms:%s:%s:cert:abc", testRegion, acct.ID)
	certID := upsertTestResource(t, st, "aws", acct.ID, TypeDMSCertificate, certARN, testRegion, "{}")
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/dms-key", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	epARN := fmt.Sprintf("arn:aws:dms:%s:%s:endpoint:e1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"CertificateArn":%q,"KmsKeyId":%q}`, certARN, keyARN)
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeDMSEndpoint, epARN, testRegion, attrs)
	if err := resolveDMSEndpointRefs(acct, st); err != nil {
		t.Fatalf("resolveDMSEndpointRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(epID)
	assertRelationship(t, rels, epID, certID, store.RelUses)
	assertRelationship(t, rels, epID, keyID, store.RelUses)
}

func TestResolveDMSReplicationInstanceRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rsgARN := dmsReplicationSubnetGroupARNFromName(testRegion, acct.ID, "default")
	rsgID := upsertTestResource(t, st, "aws", acct.ID, TypeDMSReplicationSubnetGroup, rsgARN, testRegion, "{}")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")
	riARN := fmt.Sprintf("arn:aws:dms:%s:%s:rep:r1", testRegion, acct.ID)
	attrs := `{"ReplicationSubnetGroup":{"ReplicationSubnetGroupIdentifier":"default"},"VpcSecurityGroups":[{"VpcSecurityGroupId":"sg-1"}]}`
	riID := upsertTestResource(t, st, "aws", acct.ID, TypeDMSReplicationInstance, riARN, testRegion, attrs)
	if err := resolveDMSReplicationInstanceRefs(acct, st); err != nil {
		t.Fatalf("resolveDMSReplicationInstanceRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(riID)
	assertRelationship(t, rels, riID, rsgID, store.RelAttachedTo)
	assertRelationship(t, rels, riID, sgID, store.RelUses)
}

func TestResolveDMSReplicationSubnetGroupRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	rsgARN := dmsReplicationSubnetGroupARNFromName(testRegion, acct.ID, "g1")
	attrs := `{"VpcId":"vpc-1","Subnets":[{"SubnetIdentifier":"subnet-1"}]}`
	rsgID := upsertTestResource(t, st, "aws", acct.ID, TypeDMSReplicationSubnetGroup, rsgARN, testRegion, attrs)
	if err := resolveDMSReplicationSubnetGroupRefs(acct, st); err != nil {
		t.Fatalf("resolveDMSReplicationSubnetGroupRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rsgID)
	assertRelationship(t, rels, rsgID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, rsgID, subID, store.RelAttachedTo)
}

func TestResolveDMSReplicationTaskRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	riARN := fmt.Sprintf("arn:aws:dms:%s:%s:rep:r1", testRegion, acct.ID)
	riID := upsertTestResource(t, st, "aws", acct.ID, TypeDMSReplicationInstance, riARN, testRegion, "{}")
	srcARN := fmt.Sprintf("arn:aws:dms:%s:%s:endpoint:src", testRegion, acct.ID)
	srcID := upsertTestResource(t, st, "aws", acct.ID, TypeDMSEndpoint, srcARN, testRegion, "{}")
	tskARN := fmt.Sprintf("arn:aws:dms:%s:%s:task:t1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"ReplicationInstanceArn":%q,"SourceEndpointArn":%q}`, riARN, srcARN)
	tskID := upsertTestResource(t, st, "aws", acct.ID, TypeDMSReplicationTask, tskARN, testRegion, attrs)
	if err := resolveDMSReplicationTaskRefs(acct, st); err != nil {
		t.Fatalf("resolveDMSReplicationTaskRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tskID)
	assertRelationship(t, rels, tskID, riID, store.RelAttachedTo)
	assertRelationship(t, rels, tskID, srcID, store.RelUses)
}

func TestResolveDMSMigrationProjectRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ipARN := fmt.Sprintf("arn:aws:dms:%s:%s:instance-profile:ip1", testRegion, acct.ID)
	ipID := upsertTestResource(t, st, "aws", acct.ID, TypeDMSInstanceProfile, ipARN, testRegion, "{}")
	dpARN := fmt.Sprintf("arn:aws:dms:%s:%s:data-provider:dp1", testRegion, acct.ID)
	dpID := upsertTestResource(t, st, "aws", acct.ID, TypeDMSDataProvider, dpARN, testRegion, "{}")
	mpARN := fmt.Sprintf("arn:aws:dms:%s:%s:migration-project:mp1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"InstanceProfileArn":%q,"SourceDataProviderDescriptors":[{"DataProviderArn":%q}]}`, ipARN, dpARN)
	mpID := upsertTestResource(t, st, "aws", acct.ID, TypeDMSMigrationProject, mpARN, testRegion, attrs)
	if err := resolveDMSMigrationProjectRefs(acct, st); err != nil {
		t.Fatalf("resolveDMSMigrationProjectRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mpID)
	assertRelationship(t, rels, mpID, ipID, store.RelAttachedTo)
	assertRelationship(t, rels, mpID, dpID, store.RelUses)
}

func TestResolveDMSEventSubscriptionTopic(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	tARN := fmt.Sprintf("arn:aws:sns:%s:%s:dms-events", testRegion, acct.ID)
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, tARN, testRegion, "{}")
	esARN := fmt.Sprintf("arn:aws:dms:%s:%s:es:es1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"SnsTopicArn":%q}`, tARN)
	esID := upsertTestResource(t, st, "aws", acct.ID, TypeDMSEventSubscription, esARN, testRegion, attrs)
	if err := resolveDMSEventSubscriptionTopic(acct, st); err != nil {
		t.Fatalf("resolveDMSEventSubscriptionTopic: %v", err)
	}
	rels, _ := st.RelationshipsFrom(esID)
	assertRelationship(t, rels, esID, tID, store.RelRoutesTo)
}
