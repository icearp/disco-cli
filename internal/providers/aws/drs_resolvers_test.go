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
	ssID := upsertTestResourceNamed(t, st, TypeDRSSourceServerResource, ssARN, testRegion, `{"SourceServerID":"s-1"}`, "s-1")
	// The same id in another region is another server.
	otherSS := upsertTestResourceNamed(t, st, TypeDRSSourceServerResource, drsARN("eu-west-1", acct.ID, "source-server", "s-1"), "eu-west-1", "{}", "s-1")
	riARN := drsARN(testRegion, acct.ID, "recovery-instance", "ri-1")
	riID := upsertTestResource(t, st, "aws", acct.ID, TypeDRSRecoveryInstanceResource, riARN, testRegion, `{"Ec2InstanceID":"i-reco","SourceServerID":"s-1"}`)

	if err := resolveDRSRecoveryInstanceRefs(acct, st); err != nil {
		t.Fatalf("resolveDRSRecoveryInstanceRefs: %v", err)
	}
	rels := drsRelsFrom(t, st, riID)
	assertRelationship(t, rels, riID, eID, store.RelUses)
	assertRelationship(t, rels, riID, ssID, store.RelAttachedTo)
	if len(rels) != 2 {
		t.Errorf("edges = %+v, want 2 (none to %s in another region)", rels, otherSS)
	}
}

// A source server with the same id and region in another account is not
// the one a recovery instance recovers: no edge crosses accounts.
func TestResolveDRSRecoveryInstanceRefs_OtherAccountServer(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	const other = "999999999999"
	name, region := "s-1", testRegion
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: other, Type: TypeDRSSourceServerResource, NativeID: drsARN(testRegion, other, "source-server", "s-1"),
		Name: &name, Region: &region, AttributesJSON: "{}", DiscoveredBy: testScanID,
	}); err != nil {
		t.Fatalf("seed other-account server: %v", err)
	}
	riID := upsertTestResource(t, st, "aws", acct.ID, TypeDRSRecoveryInstanceResource, drsARN(testRegion, acct.ID, "recovery-instance", "ri-1"), testRegion,
		mustJSON(drstypes.RecoveryInstance{SourceServerID: ptrStr("s-1")}))
	if err := resolveDRSRecoveryInstanceRefs(acct, st); err != nil {
		t.Fatalf("resolveDRSRecoveryInstanceRefs: %v", err)
	}
	if rels := drsRelsFrom(t, st, riID); len(rels) != 0 {
		t.Errorf("edges = %+v, want none to the other account's server", rels)
	}
}

func TestResolveDRSSourceServerRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	snARN := drsARN(testRegion, acct.ID, "source-network", "sn-1")
	snID := upsertTestResourceNamed(t, st, TypeDRSSourceNetworkResource, snARN, testRegion, `{"SourceNetworkID":"sn-1"}`, "sn-1")
	otherSN := upsertTestResourceNamed(t, st, TypeDRSSourceNetworkResource, drsARN("eu-west-1", acct.ID, "source-network", "sn-1"), "eu-west-1", "{}", "sn-1")
	ssARN := drsARN(testRegion, acct.ID, "source-server", "s-1")
	ssID := upsertTestResource(t, st, "aws", acct.ID, TypeDRSSourceServerResource, ssARN, testRegion, `{"SourceServerID":"s-1","SourceNetworkID":"sn-1"}`)

	if err := resolveDRSSourceServerRefs(acct, st); err != nil {
		t.Fatalf("resolveDRSSourceServerRefs: %v", err)
	}
	rels := drsRelsFrom(t, st, ssID)
	assertRelationship(t, rels, ssID, snID, store.RelAttachedTo)
	if len(rels) != 1 {
		t.Errorf("edges = %+v, want 1 (none to %s in another region)", rels, otherSN)
	}
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

// A row whose attributes do not parse is reported without costing the rows
// listed before or after it their edges (the rows list by name).
func TestResolveDRSReplicationSettingsRefs_DecodeError(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	subnet := "subnet-1"
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(testRegion, acct.ID, "subnet", subnet), testRegion, "{}")
	good := mustJSON(drs.GetReplicationConfigurationOutput{StagingAreaSubnetId: &subnet})
	assertDRSDecodeErrorKeepsSiblings(t, st, resolveDRSReplicationSettingsRefs, TypeDRSReplicationConfiguration, "source-server", good, subID, store.RelAttachedTo)
}

// assertDRSDecodeErrorKeepsSiblings seeds three rows of rtype named a-good,
// bad and z-good, the bad one with attributes that do not parse, runs
// resolve, and asserts it returns the bad row's decode error and still links
// both good rows (attrs good) to target.
func assertDRSDecodeErrorKeepsSiblings(t *testing.T, st *store.Store, resolve func(*account, *store.Store) error, rtype, kind, good, target, rel string) {
	t.Helper()
	ids := map[string]string{}
	for _, name := range []string{"a-good", "bad", "z-good"} {
		attrs := good
		if name == "bad" {
			attrs = "[1]"
		}
		ids[name] = upsertTestResourceNamed(t, st, rtype, drsARN(testRegion, testAccountID, kind, name), testRegion, attrs, name)
	}
	err := resolve(newTestAccount(testAccountID), st)
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want the bad row's unmarshal error", err)
	}
	for _, name := range []string{"a-good", "z-good"} {
		assertRelationship(t, drsRelsFrom(t, st, ids[name]), ids[name], target, rel)
	}
}

func TestResolveDRS_DecodeErrorKeepsSiblings(t *testing.T) {
	t.Run("recovery instance", func(t *testing.T) {
		st := newTestStore(t)
		ssID := upsertTestResourceNamed(t, st, TypeDRSSourceServerResource, drsARN(testRegion, testAccountID, "source-server", "s-1"), testRegion, "{}", "s-1")
		assertDRSDecodeErrorKeepsSiblings(t, st, resolveDRSRecoveryInstanceRefs, TypeDRSRecoveryInstanceResource, "recovery-instance",
			mustJSON(drstypes.RecoveryInstance{SourceServerID: ptrStr("s-1")}), ssID, store.RelAttachedTo)
	})
	t.Run("source server", func(t *testing.T) {
		st := newTestStore(t)
		snID := upsertTestResourceNamed(t, st, TypeDRSSourceNetworkResource, drsARN(testRegion, testAccountID, "source-network", "sn-1"), testRegion, "{}", "sn-1")
		assertDRSDecodeErrorKeepsSiblings(t, st, resolveDRSSourceServerRefs, TypeDRSSourceServerResource, "source-server",
			mustJSON(drstypes.SourceServer{SourceNetworkID: ptrStr("sn-1")}), snID, store.RelAttachedTo)
	})
	t.Run("source network", func(t *testing.T) {
		st := newTestStore(t)
		vID := upsertTestResource(t, st, "aws", testAccountID, TypeEC2VPC, ec2ARN(testRegion, testAccountID, "vpc", "vpc-1"), testRegion, "{}")
		assertDRSDecodeErrorKeepsSiblings(t, st, resolveDRSSourceNetworkRefs, TypeDRSSourceNetworkResource, "source-network",
			mustJSON(drstypes.SourceNetwork{LaunchedVpcID: ptrStr("vpc-1")}), vID, store.RelAttachedTo)
	})
	t.Run("launch configuration template", func(t *testing.T) {
		st := newTestStore(t)
		bucketARN := "arn:aws:s3:::drs-export-bucket"
		bID := upsertTestResource(t, st, "aws", testAccountID, TypeS3Bucket, bucketARN, "global", "{}")
		assertDRSDecodeErrorKeepsSiblings(t, st, resolveDRSLaunchTemplateRefs, TypeDRSLaunchConfigurationTemplateResource, "launch-configuration-template",
			mustJSON(drstypes.LaunchConfigurationTemplate{ExportBucketArn: &bucketARN}), bID, store.RelUses)
	})
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
// without costing the rows listed before or after it their edges (the rows
// list by name).
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
		"a-server": serverStep, "bad": "[1]", "server-b": serverStep,
		"wait": drsStepAttrs(&drstypes.RecoveryPlanStepConfigurationMemberWaitStepConfiguration{Value: drstypes.WaitStepConfiguration{WaitDurationMinutes: ptrInt32(5)}}),
	} {
		stepIDs[name] = upsertTestResourceNamed(t, st, TypeDRSRecoveryPlanStep, drsARN(testRegion, acct.ID, "recovery-plan-step", name), testRegion, attrs, name)
	}

	err := resolveDRSRecoveryPlanStepRefs(acct, st)
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Errorf("err = %v, want the bad row's unmarshal error", err)
	}
	for _, name := range []string{"a-server", "server-b"} {
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
