package aws

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
	dftypes "github.com/aws/aws-sdk-go-v2/service/devicefarm/types"
)

func TestDFProjectARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:devicefarm:us-west-2:111:devicepool:PROJ-1/POOL-1", "arn:aws:devicefarm:us-west-2:111:project:PROJ-1"},
		{"arn:aws:devicefarm:us-west-2:111:networkprofile:PROJ-2/NP-1", "arn:aws:devicefarm:us-west-2:111:project:PROJ-2"},
		{"arn:aws:devicefarm:us-west-2:111:project:PROJ-1", ""}, // project ARN has no child segment
	}
	for _, c := range cases {
		if got := dfProjectARNFromChild(c.in); got != c.want {
			t.Errorf("dfProjectARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveDeviceFarmProjectChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-west-2"
	projARN := fmt.Sprintf("arn:aws:devicefarm:%s:%s:project:PROJ-1", region, acct.ID)
	projID := upsertTestResource(t, st, "aws", acct.ID, TypeDeviceFarmProject, projARN, region, "{}")
	poolARN := fmt.Sprintf("arn:aws:devicefarm:%s:%s:devicepool:PROJ-1/POOL-1", region, acct.ID)
	poolID := upsertTestResource(t, st, "aws", acct.ID, TypeDeviceFarmDevicePool, poolARN, region, "{}")
	npARN := fmt.Sprintf("arn:aws:devicefarm:%s:%s:networkprofile:PROJ-1/NP-1", region, acct.ID)
	npID := upsertTestResource(t, st, "aws", acct.ID, TypeDeviceFarmNetworkProfile, npARN, region, "{}")

	if err := resolveDeviceFarmProjectChildren(acct, st); err != nil {
		t.Fatalf("resolveDeviceFarmProjectChildren: %v", err)
	}
	rels, _ := st.RelationshipsFrom(poolID)
	assertRelationship(t, rels, poolID, projID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(npID)
	assertRelationship(t, rels, npID, projID, store.RelAttachedTo)
}

func TestResolveDeviceFarmDeviceInstanceProfile(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-west-2"
	ipARN := fmt.Sprintf("arn:aws:devicefarm:%s:%s:instanceprofile:IP-1", region, acct.ID)
	ipID := upsertTestResource(t, st, "aws", acct.ID, TypeDeviceFarmInstanceProfile, ipARN, region, "{}")
	diARN := fmt.Sprintf("arn:aws:devicefarm:%s:%s:deviceinstance:DI-1", region, acct.ID)
	body, _ := json.Marshal(dftypes.DeviceInstance{Arn: ptrStr(diARN), InstanceProfile: &dftypes.InstanceProfile{Arn: ptrStr(ipARN)}})
	diID := upsertTestResource(t, st, "aws", acct.ID, TypeDeviceFarmDeviceInstance, diARN, region, string(body))

	if err := resolveDeviceFarmDeviceInstanceProfile(acct, st); err != nil {
		t.Fatalf("resolveDeviceFarmDeviceInstanceProfile: %v", err)
	}
	rels, _ := st.RelationshipsFrom(diID)
	assertRelationship(t, rels, diID, ipID, store.RelUses)
}

func TestResolveDeviceFarmTestGridProjectVPC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-west-2"
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", "vpc-1"), region, "{}")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", "subnet-1"), region, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", "sg-1"), region, "{}")
	tgARN := fmt.Sprintf("arn:aws:devicefarm:%s:%s:testgrid-project:TG-1", region, acct.ID)
	body, _ := json.Marshal(dftypes.TestGridProject{Arn: ptrStr(tgARN), VpcConfig: &dftypes.TestGridVpcConfig{
		VpcId: ptrStr("vpc-1"), SubnetIds: []string{"subnet-1"}, SecurityGroupIds: []string{"sg-1"},
	}})
	tgID := upsertTestResource(t, st, "aws", acct.ID, TypeDeviceFarmTestGridProject, tgARN, region, string(body))

	if err := resolveDeviceFarmTestGridProjectVPC(acct, st); err != nil {
		t.Fatalf("resolveDeviceFarmTestGridProjectVPC: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tgID)
	assertRelationship(t, rels, tgID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, tgID, subID, store.RelAttachedTo)
	assertRelationship(t, rels, tgID, sgID, store.RelAttachedTo)
}

func TestResolveDeviceFarmProjectRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-west-2"
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/df-exec", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, region, "{}")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", "vpc-1"), region, "{}")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", "subnet-1"), region, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", "sg-1"), region, "{}")
	pARN := fmt.Sprintf("arn:aws:devicefarm:%s:%s:project:PROJ-1", region, acct.ID)
	body, _ := json.Marshal(dftypes.Project{
		Arn: ptrStr(pARN), ExecutionRoleArn: ptrStr(roleARN),
		VpcConfig: &dftypes.VpcConfig{VpcId: ptrStr("vpc-1"), SubnetIds: []string{"subnet-1"}, SecurityGroupIds: []string{"sg-1"}},
	})
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeDeviceFarmProject, pARN, region, string(body))

	if err := resolveDeviceFarmProjectRefs(acct, st); err != nil {
		t.Fatalf("resolveDeviceFarmProjectRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, roleID, store.RelUses)
	assertRelationship(t, rels, pID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, pID, subID, store.RelAttachedTo)
	assertRelationship(t, rels, pID, sgID, store.RelAttachedTo)
}

func TestResolveDeviceFarmProjectRefs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pARN := fmt.Sprintf("arn:aws:devicefarm:us-west-2:%s:project:PROJ-1", acct.ID)
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeDeviceFarmProject, pARN, "us-west-2", "{}")
	if err := resolveDeviceFarmProjectRefs(acct, st); err != nil {
		t.Fatalf("resolveDeviceFarmProjectRefs: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(pID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveDeviceFarmDeviceInstanceProfile_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	diARN := fmt.Sprintf("arn:aws:devicefarm:us-west-2:%s:deviceinstance:DI-1", acct.ID)
	diID := upsertTestResource(t, st, "aws", acct.ID, TypeDeviceFarmDeviceInstance, diARN, "us-west-2", "{}")
	if err := resolveDeviceFarmDeviceInstanceProfile(acct, st); err != nil {
		t.Fatalf("resolveDeviceFarmDeviceInstanceProfile: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(diID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}
