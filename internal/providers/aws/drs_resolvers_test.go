package aws

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/drs"
	drstypes "github.com/aws/aws-sdk-go-v2/service/drs/types"
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
	rels := drsRelsFrom(t, st, riID)
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
	rels := drsRelsFrom(t, st, ssID)
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
	rels := drsRelsFrom(t, st, snID)
	assertRelationship(t, rels, snID, vID, store.RelAttachedTo)
}

// A replication-configuration template and a source server's replication
// configuration each use their scanned KMS key and attach to their scanned
// staging subnet and security groups; an unscanned security group gets no
// edge.
func TestResolveDRSReplicationSettingsRefs(t *testing.T) {
	keyARN := "arn:aws:kms:" + testRegion + ":" + testAccountID + ":key/k-drs"
	subnet, sgs := "subnet-1", []string{"sg-1", "sg-9"}
	for _, tc := range []struct {
		name, typ, nativeID, attrs string
	}{
		{
			name: "template", typ: TypeDRSReplicationConfigurationTemplateResource,
			nativeID: drsARN(testRegion, testAccountID, "replication-configuration-template", "rt-1"),
			attrs: mustJSON(drstypes.ReplicationConfigurationTemplate{
				EbsEncryptionKeyArn: &keyARN, StagingAreaSubnetId: &subnet, ReplicationServersSecurityGroupsIDs: sgs,
			}),
		},
		{
			name: "server configuration", typ: TypeDRSReplicationConfiguration,
			nativeID: drsARN(testRegion, testAccountID, "source-server", "s-1") + "/replication-configuration",
			attrs: mustJSON(drs.GetReplicationConfigurationOutput{
				EbsEncryptionKeyArn: &keyARN, StagingAreaSubnetId: &subnet, ReplicationServersSecurityGroupsIDs: sgs,
			}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
			subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(testRegion, acct.ID, "subnet", subnet), testRegion, "{}")
			sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(testRegion, acct.ID, "security-group", "sg-1"), testRegion, "{}")
			id := upsertTestResource(t, st, "aws", acct.ID, tc.typ, tc.nativeID, testRegion, tc.attrs)

			if err := resolveDRSReplicationSettingsRefs(acct, st); err != nil {
				t.Fatalf("resolveDRSReplicationSettingsRefs: %v", err)
			}
			rels := drsRelsFrom(t, st, id)
			assertRelationship(t, rels, id, kID, store.RelUses)
			assertRelationship(t, rels, id, subID, store.RelAttachedTo)
			assertRelationship(t, rels, id, sgID, store.RelAttachedTo)
			if len(rels) != 3 {
				t.Errorf("edges = %+v, want 3 (no edge to the unscanned sg-9)", rels)
			}
		})
	}
}

// A row whose attributes do not parse is reported without costing the other
// rows their edges; the rows list by name, so "bad" comes first.
func TestResolveDRSReplicationSettingsRefs_DecodeError(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	subnet := "subnet-1"
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(testRegion, acct.ID, "subnet", subnet), testRegion, "{}")
	server := drsARN(testRegion, acct.ID, "source-server", "s-1")
	upsertTestResourceNamed(t, st, TypeDRSReplicationConfiguration, server+"/bad", testRegion, "[1]", "bad")
	goodID := upsertTestResourceNamed(t, st, TypeDRSReplicationConfiguration, server+"/replication-configuration", testRegion,
		mustJSON(drs.GetReplicationConfigurationOutput{StagingAreaSubnetId: &subnet}), "good")

	err := resolveDRSReplicationSettingsRefs(acct, st)
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want the bad row's unmarshal error", err)
	}
	assertRelationship(t, drsRelsFrom(t, st, goodID), goodID, subID, store.RelAttachedTo)
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
	rels := drsRelsFrom(t, st, ltID)
	assertRelationship(t, rels, ltID, bID, store.RelUses)
}

func TestResolveDRS_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	riARN := drsARN(testRegion, acct.ID, "recovery-instance", "ri-1")
	riID := upsertTestResource(t, st, "aws", acct.ID, TypeDRSRecoveryInstanceResource, riARN, testRegion, "{}")
	rtARN := drsARN(testRegion, acct.ID, "replication-configuration-template", "rt-1")
	rtID := upsertTestResource(t, st, "aws", acct.ID, TypeDRSReplicationConfigurationTemplateResource, rtARN, testRegion, "{}")
	rcID := upsertTestResource(t, st, "aws", acct.ID, TypeDRSReplicationConfiguration,
		drsARN(testRegion, acct.ID, "source-server", "s-1")+"/replication-configuration", testRegion, "{}")
	for _, fn := range []func(*account, *store.Store) error{
		resolveDRSRecoveryInstanceRefs, resolveDRSSourceServerRefs, resolveDRSSourceNetworkRefs,
		resolveDRSReplicationSettingsRefs, resolveDRSLaunchTemplateRefs,
	} {
		if err := fn(acct, st); err != nil {
			t.Fatalf("resolver with no attrs: %v", err)
		}
	}
	for _, id := range []string{riID, rtID, rcID} {
		if rels := drsRelsFrom(t, st, id); len(rels) != 0 {
			t.Errorf("%s: emitted %d edges, want 0", id, len(rels))
		}
	}
}

func drsStepAttrs(cfg drstypes.RecoveryPlanStepConfiguration) string {
	return mustJSON(flattenDRSRecoveryPlanStep(drstypes.RecoveryPlanStep{Configuration: cfg}))
}

// A server step uses each scanned source server it names; an unscanned server
// and a wait step get no edge. A row whose attributes do not parse is reported
// without costing the other rows their edges; the rows list by name, so "bad"
// comes first.
func TestResolveDRSRecoveryPlanStepRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	s1 := drsARN(testRegion, acct.ID, "source-server", "s-1")
	s1ID := upsertTestResource(t, st, "aws", acct.ID, TypeDRSSourceServerResource, s1, testRegion, "{}")
	unscanned := drsARN(testRegion, acct.ID, "source-server", "s-9")
	serverStep := drsStepAttrs(&drstypes.RecoveryPlanStepConfigurationMemberServerStepConfiguration{Value: drstypes.ServerStepConfiguration{
		Servers: []drstypes.RecoveryPlanServer{{ServerArn: &s1}, {ServerArn: &unscanned}},
	}})
	stepIDs := map[string]string{}
	for name, attrs := range map[string]string{
		"bad": "[1]", "server-a": serverStep, "server-b": serverStep,
		"wait": drsStepAttrs(&drstypes.RecoveryPlanStepConfigurationMemberWaitStepConfiguration{Value: drstypes.WaitStepConfiguration{WaitDurationMinutes: ptrInt32(5)}}),
	} {
		stepIDs[name] = upsertTestResourceNamed(t, st, TypeDRSRecoveryPlanStep, drsARN(testRegion, acct.ID, "recovery-plan-step", name), testRegion, attrs, name)
	}

	err := resolveDRSRecoveryPlanStepRefs(acct, st)
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want the bad row's unmarshal error", err)
	}
	for _, name := range []string{"server-a", "server-b"} {
		rels := drsRelsFrom(t, st, stepIDs[name])
		assertRelationship(t, rels, stepIDs[name], s1ID, store.RelUses)
		if len(rels) != 1 {
			t.Errorf("%s edges = %+v, want only the scanned s-1", name, rels)
		}
	}
	if rels := drsRelsFrom(t, st, stepIDs["wait"]); len(rels) != 0 {
		t.Errorf("wait step edges = %+v, want none", rels)
	}
}

func TestResolveDRSRecoveryPlanStepRefs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeDRSSourceServerResource, drsARN(testRegion, acct.ID, "source-server", "s-1"), testRegion, "{}")
	stepID := upsertTestResource(t, st, "aws", acct.ID, TypeDRSRecoveryPlanStep, drsARN(testRegion, acct.ID, "recovery-plan-step", "s"), testRegion, "{}")
	if err := resolveDRSRecoveryPlanStepRefs(acct, st); err != nil {
		t.Fatalf("resolveDRSRecoveryPlanStepRefs: %v", err)
	}
	if rels := drsRelsFrom(t, st, stepID); len(rels) != 0 {
		t.Errorf("edges = %+v, want none", rels)
	}
}

func drsRelsFrom(t *testing.T, st *store.Store, id string) []store.Relationship {
	t.Helper()
	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom(%s): %v", id, err)
	}
	return rels
}
