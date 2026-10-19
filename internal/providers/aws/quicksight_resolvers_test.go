package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveQuickSightVPCConnectionRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vARN, testRegion, "{}")
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/QS", acct.ID)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	vcARN := fmt.Sprintf("arn:aws:quicksight:%s:%s:vpcConnection/vc1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"VPCId":"vpc-1","SecurityGroupIds":["sg-1"],"RoleArn":%q,"NetworkInterfaces":[{"SubnetId":"subnet-1"}]}`, roleARN)
	vcID := upsertTestResource(t, st, "aws", acct.ID, TypeQuickSightVPCConnection, vcARN, testRegion, attrs)
	if err := resolveQuickSightVPCConnectionRefs(acct, st); err != nil {
		t.Fatalf("resolveQuickSightVPCConnectionRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vcID)
	assertRelationship(t, rels, vcID, vID, store.RelAttachedTo)
	assertRelationship(t, rels, vcID, subID, store.RelAttachedTo)
	assertRelationship(t, rels, vcID, sgID, store.RelUses)
	assertRelationship(t, rels, vcID, rID, store.RelAssumes)
}

func TestResolveQuickSightRefreshScheduleParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dsARN := fmt.Sprintf("arn:aws:quicksight:%s:%s:dataset/ds1", testRegion, acct.ID)
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeQuickSightDataSet, dsARN, testRegion, "{}")
	rsARN := dsARN + "/refresh-schedule/sch1"
	rsID := upsertTestResource(t, st, "aws", acct.ID, TypeQuickSightRefreshSchedule, rsARN, testRegion, "{}")
	if err := resolveQuickSightRefreshScheduleParent(acct, st); err != nil {
		t.Fatalf("resolveQuickSightRefreshScheduleParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rsID)
	assertRelationship(t, rels, rsID, dsID, store.RelAttachedTo)
}

func TestResolveQSDataSourceRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	dsARN := "arn:aws:quicksight:us-east-1:" + testAccountID + ":datasource/ds-1"
	secARN := "arn:aws:secretsmanager:us-east-1:" + testAccountID + ":secret:qs-rds"
	vcARN := "arn:aws:quicksight:us-east-1:" + testAccountID + ":vpcConnection/vc-1"
	attrs := `{"SecretArn":"` + secARN + `","VpcConnectionProperties":{"VpcConnectionArn":"` + vcARN + `"}}`

	dID := upsertTestResource(t, st, "aws", acct.ID, TypeQuickSightDataSource, dsARN, testRegion, attrs)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeSecretsManagerSecret, secARN, testRegion, "{}")
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeQuickSightVPCConnection, vcARN, testRegion, "{}")

	if err := resolveQSDataSourceRefs(acct, st); err != nil {
		t.Fatalf("resolveQSDataSourceRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dID)
	assertRelationship(t, rels, dID, sID, store.RelUses)
	assertRelationship(t, rels, dID, vID, store.RelAttachedTo)
}

func TestResolveQuickSightNamespaceMembers(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	nsARN := fmt.Sprintf("arn:aws:quicksight:%s:%s:namespace/default", testRegion, acct.ID)
	nsID := upsertTestResource(t, st, "aws", acct.ID, TypeQuickSightNamespace, nsARN, testRegion, "{}")

	grpARN := fmt.Sprintf("arn:aws:quicksight:%s:%s:group/default/analysts", testRegion, acct.ID)
	grpID := upsertTestResource(t, st, "aws", acct.ID, TypeQuickSightGroup, grpARN, testRegion, "{}")
	usrARN := fmt.Sprintf("arn:aws:quicksight:%s:%s:user/default/alice", testRegion, acct.ID)
	usrID := upsertTestResource(t, st, "aws", acct.ID, TypeQuickSightUser, usrARN, testRegion, "{}")
	asgARN := nsARN + "/assignment/policy1"
	asgID := upsertTestResource(t, st, "aws", acct.ID, TypeQuickSightAssignment, asgARN, testRegion, "{}")

	if err := resolveQuickSightNamespaceMembers(acct, st); err != nil {
		t.Fatalf("resolveQuickSightNamespaceMembers: %v", err)
	}
	rels, _ := st.RelationshipsFrom(grpID)
	assertRelationship(t, rels, grpID, nsID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(usrID)
	assertRelationship(t, rels, usrID, nsID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(asgID)
	assertRelationship(t, rels, asgID, nsID, store.RelAttachedTo)
}

// assertQSAttachedTo fails unless src (a NativeID) has exactly the attached-to
// targets given by NativeID.
func assertQSAttachedTo(t *testing.T, st *store.Store, src string, targets ...string) {
	t.Helper()
	srcID := store.ResourceID("aws", testAccountID, src)
	rels, err := st.RelationshipsFrom(srcID, store.RelAttachedTo)
	if err != nil {
		t.Fatalf("RelationshipsFrom(%s): %v", src, err)
	}
	if len(rels) != len(targets) {
		t.Errorf("%s has %d attached-to edges; want %d", src, len(rels), len(targets))
	}
	for _, tgt := range targets {
		assertRelationship(t, rels, srcID, store.ResourceID("aws", testAccountID, tgt), store.RelAttachedTo)
	}
}

func TestResolveQuickSightFolderMemberTargets(t *testing.T) {
	folder := qsTestARN("folder", "f1")
	for _, tc := range []struct{ kind, typ string }{
		{"analysis", TypeQuickSightAnalysis},
		{"dashboard", TypeQuickSightDashboard},
		{"dataset", TypeQuickSightDataSet},
		{"datasource", TypeQuickSightDataSource},
		{"topic", TypeQuickSightTopic},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			st := newTestStore(t)
			assetARN := qsTestARN(tc.kind, "a1")
			upsertTestResource(t, st, "aws", testAccountID, tc.typ, assetARN, testRegion, "{}")
			linked := folder + "/member/" + assetARN
			upsertTestResource(t, st, "aws", testAccountID, TypeQuickSightFolderMember, linked, testRegion, mustJSON(qsFolderMember(tc.kind, "a1")))
			unscanned := folder + "/member/" + qsTestARN(tc.kind, "a2")
			upsertTestResource(t, st, "aws", testAccountID, TypeQuickSightFolderMember, unscanned, testRegion, mustJSON(qsFolderMember(tc.kind, "a2")))
			noAttrs := folder + "/member/empty"
			upsertTestResource(t, st, "aws", testAccountID, TypeQuickSightFolderMember, noAttrs, testRegion, "{}")

			if err := resolveQuickSightFolderMemberTargets(newTestAccount(testAccountID), st); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			assertQSAttachedTo(t, st, linked, assetARN)
			assertQSAttachedTo(t, st, unscanned)
			assertQSAttachedTo(t, st, noAttrs)
		})
	}
}

func TestResolveQuickSightGroupMembershipUser(t *testing.T) {
	st := newTestStore(t)
	group := qsTestARN("group", "default/admins")
	userARN := qsTestARN("user", "default/alice")
	upsertTestResource(t, st, "aws", testAccountID, TypeQuickSightUser, userARN, testRegion, "{}")
	linked := group + "/member/" + userARN
	upsertTestResource(t, st, "aws", testAccountID, TypeQuickSightGroupMembership, linked, testRegion, mustJSON(qsGroupMember("alice")))
	unscanned := group + "/member/" + qsTestARN("user", "default/bob")
	upsertTestResource(t, st, "aws", testAccountID, TypeQuickSightGroupMembership, unscanned, testRegion, mustJSON(qsGroupMember("bob")))
	noAttrs := group + "/member/empty"
	upsertTestResource(t, st, "aws", testAccountID, TypeQuickSightGroupMembership, noAttrs, testRegion, "{}")

	if err := resolveQuickSightGroupMembershipUser(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	assertQSAttachedTo(t, st, linked, userARN)
	assertQSAttachedTo(t, st, unscanned)
	assertQSAttachedTo(t, st, noAttrs)
}

// A row that fails to decode is returned, and costs the other rows no edge.
func TestQSARNFieldEdge_DecodeErrorKeepsOtherEdges(t *testing.T) {
	st := newTestStore(t)
	group := qsTestARN("group", "default/admins")
	userARN := qsTestARN("user", "default/alice")
	upsertTestResource(t, st, "aws", testAccountID, TypeQuickSightUser, userARN, testRegion, "{}")
	good := group + "/member/" + userARN
	upsertTestResource(t, st, "aws", testAccountID, TypeQuickSightGroupMembership, good, testRegion, mustJSON(qsGroupMember("alice")))
	upsertTestResource(t, st, "aws", testAccountID, TypeQuickSightGroupMembership, group+"/member/bad", testRegion, `{"Arn":7}`)

	err := resolveQuickSightGroupMembershipUser(newTestAccount(testAccountID), st)
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Errorf("err = %v; want the attribute decode error", err)
	}
	assertQSAttachedTo(t, st, good, userARN)
}

func TestQuickSightMemberResolvers_NoRows(t *testing.T) {
	st := newTestStore(t)
	for name, resolve := range map[string]func(*account, *store.Store) error{
		"folder member":    resolveQuickSightFolderMemberTargets,
		"group membership": resolveQuickSightGroupMembershipUser,
	} {
		if err := resolve(newTestAccount(testAccountID), st); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestResolveQuickSightNamespaceMembers_NoRows guards the empty/no-attrs case.
func TestResolveQuickSightNamespaceMembers_NoRows(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	if err := resolveQuickSightNamespaceMembers(acct, st); err != nil {
		t.Fatalf("resolveQuickSightNamespaceMembers empty: %v", err)
	}
}
