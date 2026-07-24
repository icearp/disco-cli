package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveMSKChildrenToCluster(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	clARN := fmt.Sprintf("arn:aws:kafka:%s:%s:cluster/c1/uuid-1", testRegion, acct.ID)
	clID := upsertTestResource(t, st, "aws", acct.ID, TypeMSKCluster, clARN, testRegion, "{}")
	secretARN := fmt.Sprintf("arn:aws:secretsmanager:%s:%s:secret:AmazonMSK_s1-AbCdEf", testRegion, acct.ID)
	secretID := upsertTestResource(t, st, "aws", acct.ID, TypeSecretsManagerSecret, secretARN, testRegion, "{}")
	cpARN := clARN + "/cluster-policy"
	cpID := upsertTestResource(t, st, "aws", acct.ID, TypeMSKClusterPolicy, cpARN, testRegion, "{}")
	bsARN := clARN + "/batch-scram-secret/" + secretARN
	bsID := upsertTestResource(t, st, "aws", acct.ID, TypeMSKBatchScramSecret, bsARN, testRegion, "{}")
	if err := resolveMSKChildrenToCluster(acct, st); err != nil {
		t.Fatalf("resolveMSKChildrenToCluster: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cpID)
	assertRelationship(t, rels, cpID, clID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(bsID)
	assertRelationship(t, rels, bsID, clID, store.RelAttachedTo)
	assertRelationship(t, rels, bsID, secretID, store.RelUses)
}

func TestResolveMSKVpcConnectionRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	clARN := fmt.Sprintf("arn:aws:kafka:%s:%s:cluster/c1/uuid-1", testRegion, acct.ID)
	clID := upsertTestResource(t, st, "aws", acct.ID, TypeMSKCluster, clARN, testRegion, "{}")
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	vcARN := fmt.Sprintf("arn:aws:kafka:%s:%s:vpc-connection/cn1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"TargetClusterArn":"%s","VpcId":"vpc-1"}`, clARN)
	vcID := upsertTestResource(t, st, "aws", acct.ID, TypeMSKVpcConnection, vcARN, testRegion, attrs)
	if err := resolveMSKVpcConnectionRefs(acct, st); err != nil {
		t.Fatalf("resolveMSKVpcConnectionRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vcID)
	assertRelationship(t, rels, vcID, clID, store.RelAttachedTo)
	assertRelationship(t, rels, vcID, vpcID, store.RelAttachedTo)
}

func TestResolveMSKReplicatorRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	clARN := fmt.Sprintf("arn:aws:kafka:%s:%s:cluster/c1/uuid-1", testRegion, acct.ID)
	clID := upsertTestResource(t, st, "aws", acct.ID, TypeMSKCluster, clARN, testRegion, "{}")
	rARN := fmt.Sprintf("arn:aws:kafka:%s:%s:replicator/r1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"KafkaClustersSummary":[{"AmazonMskCluster":{"MskClusterArn":"%s"}}]}`, clARN)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeMSKReplicator, rARN, testRegion, attrs)
	if err := resolveMSKReplicatorRefs(acct, st); err != nil {
		t.Fatalf("resolveMSKReplicatorRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rID)
	assertRelationship(t, rels, rID, clID, store.RelUses)
}
