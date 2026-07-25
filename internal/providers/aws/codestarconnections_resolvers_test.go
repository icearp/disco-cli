package aws

import (
	"encoding/json"
	"fmt"
	"testing"

	csctypes "github.com/aws/aws-sdk-go-v2/service/codestarconnections/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveCSCRepositoryLinkRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	connARN := fmt.Sprintf("arn:aws:codestar-connections:%s:%s:connection/abc-123", testRegion, acct.ID)
	connID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeStarConnectionsConnection, connARN, testRegion, "{}")
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/k-1", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	rlARN := fmt.Sprintf("arn:aws:codestar-connections:%s:%s:repository-link/r-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"ConnectionArn":"%s","EncryptionKeyArn":"%s","RepositoryLinkId":"r-1"}`, connARN, keyARN)
	rlID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeStarConnectionsRepositoryLink, rlARN, testRegion, attrs)
	if err := resolveCSCRepositoryLinkRefs(acct, st); err != nil {
		t.Fatalf("resolveCSCRepositoryLinkRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rlID)
	assertRelationship(t, rels, rlID, connID, store.RelAttachedTo)
	assertRelationship(t, rels, rlID, keyID, store.RelUses)
}

func TestResolveCSCHostNetwork(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-1"), testRegion, "{}")
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(testRegion, acct.ID, "subnet", "subnet-1"), testRegion, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(testRegion, acct.ID, "security-group", "sg-1"), testRegion, "{}")

	hostARN := fmt.Sprintf("arn:aws:codestar-connections:%s:%s:host/h-1", testRegion, acct.ID)
	hostBody, _ := json.Marshal(csctypes.Host{
		HostArn: ptrStr(hostARN),
		Name:    ptrStr("ghes"),
		VpcConfiguration: &csctypes.VpcConfiguration{
			VpcId:            ptrStr("vpc-1"),
			SubnetIds:        []string{"subnet-1"},
			SecurityGroupIds: []string{"sg-1"},
		},
	})
	hostID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeStarConnectionsHost, hostARN, testRegion, string(hostBody))

	if err := resolveCSCHostNetwork(acct, st); err != nil {
		t.Fatalf("resolveCSCHostNetwork: %v", err)
	}
	rels, _ := st.RelationshipsFrom(hostID)
	assertRelationship(t, rels, hostID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, hostID, snID, store.RelAttachedTo)
	assertRelationship(t, rels, hostID, sgID, store.RelUses)
}

func TestResolveCSCHostNetwork_NoVpcConfig(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	hostARN := fmt.Sprintf("arn:aws:codestar-connections:%s:%s:host/h-1", testRegion, acct.ID)
	hostBody, _ := json.Marshal(csctypes.Host{HostArn: ptrStr(hostARN), Name: ptrStr("cloud-hosted")})
	hostID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeStarConnectionsHost, hostARN, testRegion, string(hostBody))

	if err := resolveCSCHostNetwork(acct, st); err != nil {
		t.Fatalf("resolveCSCHostNetwork: %v", err)
	}
	rels, _ := st.RelationshipsFrom(hostID)
	if len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0 (no VpcConfiguration)", len(rels))
	}
}

func TestResolveCSCSyncConfigurationRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rlARN := fmt.Sprintf("arn:aws:codestar-connections:%s:%s:repository-link/r-1", testRegion, acct.ID)
	rlID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeStarConnectionsRepositoryLink, rlARN, testRegion, `{"RepositoryLinkId":"r-1"}`)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/sync-role", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	scARN := fmt.Sprintf("arn:aws:codestar-connections:%s:%s:sync-configuration/r-1/CFN_STACK_SYNC/myapp", testRegion, acct.ID)
	scAttrs := fmt.Sprintf(`{"RepositoryLinkId":"r-1","RoleArn":"%s"}`, roleARN)
	scID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeStarConnectionsSyncConfiguration, scARN, testRegion, scAttrs)
	if err := resolveCSCSyncConfigurationRefs(acct, st); err != nil {
		t.Fatalf("resolveCSCSyncConfigurationRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(scID)
	assertRelationship(t, rels, scID, rlID, store.RelAttachedTo)
	assertRelationship(t, rels, scID, roleID, store.RelUses)
}
