package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func drsARN(region, acct, kind, id string) string {
	return "arn:aws:drs:" + region + ":" + acct + ":" + kind + "/" + id
}

func TestResolveDRSRecoveryInstanceRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	ec2ARNv := ec2ARN(testRegion, acct.ID, "instance", "i-reco")
	eID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, ec2ARNv, testRegion, "{}")
	ssARN := drsARN(testRegion, acct.ID, "source-server", "s-1")
	ssID := upsertTestResource(t, st, "aws", acct.ID, TypeDRSSourceServerResource, ssARN, testRegion, `{"SourceServerID":"s-1"}`)
	riARN := drsARN(testRegion, acct.ID, "recovery-instance", "ri-1")
	riID := upsertTestResource(t, st, "aws", acct.ID, TypeDRSRecoveryInstanceResource, riARN, testRegion, `{"Ec2InstanceID":"i-reco","SourceServerID":"s-1"}`)

	if err := resolveDRSRecoveryInstanceRefs(acct, st); err != nil {
		t.Fatalf("resolveDRSRecoveryInstanceRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(riID)
	assertRelationship(t, rels, riID, eID, store.RelUses)
	assertRelationship(t, rels, riID, ssID, store.RelAttachedTo)
}

func TestResolveDRSSourceServerRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	snARN := drsARN(testRegion, acct.ID, "source-network", "sn-1")
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeDRSSourceNetworkResource, snARN, testRegion, `{"SourceNetworkID":"sn-1"}`)
	ssARN := drsARN(testRegion, acct.ID, "source-server", "s-1")
	ssID := upsertTestResource(t, st, "aws", acct.ID, TypeDRSSourceServerResource, ssARN, testRegion, `{"SourceServerID":"s-1","SourceNetworkID":"sn-1"}`)

	if err := resolveDRSSourceServerRefs(acct, st); err != nil {
		t.Fatalf("resolveDRSSourceServerRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(ssID)
	assertRelationship(t, rels, ssID, snID, store.RelAttachedTo)
}

func TestResolveDRSSourceNetworkRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-launched")
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	snARN := drsARN(testRegion, acct.ID, "source-network", "sn-1")
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeDRSSourceNetworkResource, snARN, testRegion, `{"LaunchedVpcID":"vpc-launched"}`)

	if err := resolveDRSSourceNetworkRefs(acct, st); err != nil {
		t.Fatalf("resolveDRSSourceNetworkRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(snID)
	assertRelationship(t, rels, snID, vID, store.RelAttachedTo)
}

func TestResolveDRSReplicationTemplateRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	keyARN := "arn:aws:kms:" + testRegion + ":" + testAccountID + ":key/k-drs"
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")
	rtARN := drsARN(testRegion, acct.ID, "replication-configuration-template", "rt-1")
	attrs := `{"EbsEncryptionKeyArn":"` + keyARN + `","StagingAreaSubnetId":"subnet-1","ReplicationServersSecurityGroupsIDs":["sg-1"]}`
	rtID := upsertTestResource(t, st, "aws", acct.ID, TypeDRSReplicationConfigurationTemplateResource, rtARN, testRegion, attrs)

	if err := resolveDRSReplicationTemplateRefs(acct, st); err != nil {
		t.Fatalf("resolveDRSReplicationTemplateRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rtID)
	assertRelationship(t, rels, rtID, kID, store.RelUses)
	assertRelationship(t, rels, rtID, subID, store.RelAttachedTo)
	assertRelationship(t, rels, rtID, sgID, store.RelAttachedTo)
}

func TestResolveDRSLaunchTemplateRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	bucketARN := "arn:aws:s3:::drs-export-bucket"
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, "global", "{}")
	ltARN := drsARN(testRegion, acct.ID, "launch-configuration-template", "lt-1")
	ltID := upsertTestResource(t, st, "aws", acct.ID, TypeDRSLaunchConfigurationTemplateResource, ltARN, testRegion, `{"ExportBucketArn":"`+bucketARN+`"}`)

	if err := resolveDRSLaunchTemplateRefs(acct, st); err != nil {
		t.Fatalf("resolveDRSLaunchTemplateRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(ltID)
	assertRelationship(t, rels, ltID, bID, store.RelUses)
}

func TestResolveDRS_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	riARN := drsARN(testRegion, acct.ID, "recovery-instance", "ri-1")
	riID := upsertTestResource(t, st, "aws", acct.ID, TypeDRSRecoveryInstanceResource, riARN, testRegion, "{}")
	rtARN := drsARN(testRegion, acct.ID, "replication-configuration-template", "rt-1")
	rtID := upsertTestResource(t, st, "aws", acct.ID, TypeDRSReplicationConfigurationTemplateResource, rtARN, testRegion, "{}")
	for _, fn := range []func(*account, *store.Store) error{
		resolveDRSRecoveryInstanceRefs, resolveDRSSourceServerRefs, resolveDRSSourceNetworkRefs,
		resolveDRSReplicationTemplateRefs, resolveDRSLaunchTemplateRefs,
	} {
		if err := fn(acct, st); err != nil {
			t.Fatalf("resolver with no attrs: %v", err)
		}
	}
	for _, id := range []string{riID, rtID} {
		if rels, _ := st.RelationshipsFrom(id); len(rels) != 0 {
			t.Errorf("%s: emitted %d edges, want 0", id, len(rels))
		}
	}
}
