package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveSageMakerEndpointConfig(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cfgARN := sagemakerEndpointConfigARN(testRegion, acct.ID, "cfg-1")
	cfgID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerEndpointConfig, cfgARN, testRegion, "{}")
	epARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:endpoint/ep-1", testRegion, acct.ID)
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerEndpoint, epARN, testRegion,
		`{"EndpointConfigName":"cfg-1"}`)

	if err := resolveSageMakerEndpointConfig(acct, st); err != nil {
		t.Fatalf("resolveSageMakerEndpointConfig: %v", err)
	}
	rels, err := st.RelationshipsFrom(epID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, epID, cfgID, store.RelAttachedTo)
}

func TestResolveSageMakerEndpointConfig_UnscannedConfigSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	epARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:endpoint/ep-1", testRegion, acct.ID)
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerEndpoint, epARN, testRegion,
		`{"EndpointConfigName":"missing-cfg"}`)
	if err := resolveSageMakerEndpointConfig(acct, st); err != nil {
		t.Fatalf("resolveSageMakerEndpointConfig: %v", err)
	}
	rels, _ := st.RelationshipsFrom(epID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveSageMakerEndpointConfigModels(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	m1 := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerModel,
		sagemakerModelARN(testRegion, acct.ID, "model-a"), testRegion, "{}")
	m2 := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerModel,
		sagemakerModelARN(testRegion, acct.ID, "model-b"), testRegion, "{}")
	cfgID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerEndpointConfig,
		sagemakerEndpointConfigARN(testRegion, acct.ID, "cfg-1"), testRegion,
		`{"ProductionVariants":[{"ModelName":"model-a"},{"ModelName":"model-b"}]}`)

	if err := resolveSageMakerEndpointConfigModels(acct, st); err != nil {
		t.Fatalf("resolveSageMakerEndpointConfigModels: %v", err)
	}
	rels, err := st.RelationshipsFrom(cfgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, cfgID, m1, store.RelAttachedTo)
	assertRelationship(t, rels, cfgID, m2, store.RelAttachedTo)
}

func TestResolveSageMakerModelRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/sm-role", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	repoARN := fmt.Sprintf("arn:aws:ecr:%s:%s:repository/my-repo", testRegion, acct.ID)
	repoID := upsertTestResource(t, st, "aws", acct.ID, TypeECRRepository, repoARN, testRegion, "{}")
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-001")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-001")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")

	imageURL := fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com/my-repo:latest", acct.ID, testRegion)
	attrs := fmt.Sprintf(`{
		"ExecutionRoleArn":%q,
		"PrimaryContainer":{"Image":%q},
		"VpcConfig":{"Subnets":["subnet-001"],"SecurityGroupIds":["sg-001"]}
	}`, roleARN, imageURL)
	modelID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerModel,
		sagemakerModelARN(testRegion, acct.ID, "model-a"), testRegion, attrs)

	if err := resolveSageMakerModelRefs(acct, st); err != nil {
		t.Fatalf("resolveSageMakerModelRefs: %v", err)
	}
	rels, err := st.RelationshipsFrom(modelID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 4 {
		t.Fatalf("expected 4 relationships (role, repo, subnet, sg), got %d", len(rels))
	}
	assertRelationship(t, rels, modelID, roleID, store.RelAssumes)
	assertRelationship(t, rels, modelID, repoID, store.RelUses)
	assertRelationship(t, rels, modelID, subID, store.RelAttachedTo)
	assertRelationship(t, rels, modelID, sgID, store.RelUses)
}

func TestResolveSageMakerModelRefs_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	modelID := upsertTestResource(t, st, "aws", acct.ID, TypeSageMakerModel,
		sagemakerModelARN(testRegion, acct.ID, "bare"), testRegion, "{}")
	if err := resolveSageMakerModelRefs(acct, st); err != nil {
		t.Fatalf("resolveSageMakerModelRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(modelID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
