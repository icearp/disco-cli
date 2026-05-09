package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveGlueTriggerWorkflow(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	wfARN := glueResourceARN(testRegion, acct.ID, "workflow", "etl-flow")
	wfID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueWorkflow, wfARN, testRegion, "{}")
	jobARN := glueResourceARN(testRegion, acct.ID, "job", "etl-job")
	jobID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueJob, jobARN, testRegion, "{}")
	trgARN := glueResourceARN(testRegion, acct.ID, "trigger", "trg-1")
	attrs := `{"WorkflowName":"etl-flow","Actions":[{"JobName":"etl-job"}]}`
	trgID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueTrigger, trgARN, testRegion, attrs)
	if err := resolveGlueTriggerWorkflow(acct, st); err != nil {
		t.Fatalf("resolveGlueTriggerWorkflow: %v", err)
	}
	rels, _ := st.RelationshipsFrom(trgID)
	assertRelationship(t, rels, trgID, wfID, store.RelAttachedTo)
	assertRelationship(t, rels, trgID, jobID, store.RelRoutesTo)
}

func TestResolveGlueDevEndpointRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/glue-dev", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-x")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-y")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")
	deARN := glueResourceARN(testRegion, acct.ID, "devEndpoint", "de-1")
	attrs := fmt.Sprintf(`{"RoleArn":%q,"SubnetId":"subnet-x","SecurityGroupIds":["sg-y"]}`, roleARN)
	deID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueDevEndpoint, deARN, testRegion, attrs)
	if err := resolveGlueDevEndpointRefs(acct, st); err != nil {
		t.Fatalf("resolveGlueDevEndpointRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(deID)
	assertRelationship(t, rels, deID, roleID, store.RelAssumes)
	assertRelationship(t, rels, deID, subID, store.RelAttachedTo)
	assertRelationship(t, rels, deID, sgID, store.RelUses)
}

func TestResolveGlueMLTransformRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/mlt", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	dbARN := glueResourceARN(testRegion, acct.ID, "database", "sales")
	dbID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueDatabase, dbARN, testRegion, "{}")
	tblARN := glueResourceARN(testRegion, acct.ID, "table", "sales/orders")
	tblID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueTable, tblARN, testRegion, "{}")
	mltARN := glueResourceARN(testRegion, acct.ID, "mlTransform", "mlt-1")
	attrs := fmt.Sprintf(`{"Role":%q,"InputRecordTables":[{"DatabaseName":"sales","TableName":"orders"}]}`, roleARN)
	mltID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueMLTransform, mltARN, testRegion, attrs)
	if err := resolveGlueMLTransformRefs(acct, st); err != nil {
		t.Fatalf("resolveGlueMLTransformRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mltID)
	assertRelationship(t, rels, mltID, roleID, store.RelAssumes)
	assertRelationship(t, rels, mltID, dbID, store.RelUses)
	assertRelationship(t, rels, mltID, tblID, store.RelUses)
}

func TestResolveGlueConnectionRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-c")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	connARN := glueResourceARN(testRegion, acct.ID, "connection", "conn-1")
	attrs := `{"PhysicalConnectionRequirements":{"SubnetId":"subnet-c","SecurityGroupIdList":[]}}`
	connID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueConnection, connARN, testRegion, attrs)
	if err := resolveGlueConnectionRefs(acct, st); err != nil {
		t.Fatalf("resolveGlueConnectionRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(connID)
	assertRelationship(t, rels, connID, subID, store.RelAttachedTo)
}

func TestResolveGlueSchemaRegistry(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	regARN := glueResourceARN(testRegion, acct.ID, "registry", "default")
	regID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueRegistry, regARN, testRegion, "{}")
	schARN := glueResourceARN(testRegion, acct.ID, "schema", "default/avro1")
	attrs := fmt.Sprintf(`{"RegistryId":{"RegistryArn":%q}}`, regARN)
	schID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueSchema, schARN, testRegion, attrs)
	if err := resolveGlueSchemaRegistry(acct, st); err != nil {
		t.Fatalf("resolveGlueSchemaRegistry: %v", err)
	}
	rels, _ := st.RelationshipsFrom(schID)
	assertRelationship(t, rels, schID, regID, store.RelAttachedTo)
}

func TestResolveGlueSecurityConfigKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/glue-sc-key", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	scARN := glueResourceARN(testRegion, acct.ID, "securityConfiguration", "sc-1")
	attrs := fmt.Sprintf(`{"EncryptionConfiguration":{"S3Encryption":[{"KmsKeyArn":%q}],"CloudWatchEncryption":{"KmsKeyArn":%q}}}`, keyARN, keyARN)
	scID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueSecurityConfiguration, scARN, testRegion, attrs)
	if err := resolveGlueSecurityConfigKMS(acct, st); err != nil {
		t.Fatalf("resolveGlueSecurityConfigKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(scID)
	if len(rels) != 1 {
		t.Errorf("expected 1 dedup'd KMS edge, got %d", len(rels))
	}
	assertRelationship(t, rels, scID, keyID, store.RelUses)
}

func TestResolveGlueDataCatalogEncryptionKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/cat-key", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	dceARN := fmt.Sprintf("arn:aws:glue:%s:%s:data-catalog-encryption-settings", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"DataCatalogEncryptionSettings":{"EncryptionAtRest":{"SseAwsKmsKeyId":%q}}}`, keyARN)
	dceID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueDataCatalogEncryptionSettings, dceARN, testRegion, attrs)
	if err := resolveGlueDataCatalogEncryptionKMS(acct, st); err != nil {
		t.Fatalf("resolveGlueDataCatalogEncryptionKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dceID)
	assertRelationship(t, rels, dceID, keyID, store.RelUses)
}

func TestResolveGlueWorkflowGraphNodes(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	jobARN := fmt.Sprintf("arn:aws:glue:%s:%s:job/etl-1", testRegion, acct.ID)
	jobID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueJob, jobARN, testRegion, "{}")
	trgARN := fmt.Sprintf("arn:aws:glue:%s:%s:trigger/t-1", testRegion, acct.ID)
	trgID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueTrigger, trgARN, testRegion, "{}")
	wfARN := fmt.Sprintf("arn:aws:glue:%s:%s:workflow/wf-1", testRegion, acct.ID)
	wfAttrs := `{"Graph":{"Nodes":[{"Name":"etl-1","Type":"JOB"},{"Name":"t-1","Type":"TRIGGER"}]}}`
	wfID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueWorkflow, wfARN, testRegion, wfAttrs)
	if err := resolveGlueWorkflowGraphNodes(acct, st); err != nil {
		t.Fatalf("resolveGlueWorkflowGraphNodes: %v", err)
	}
	rels, _ := st.RelationshipsFrom(wfID)
	assertRelationship(t, rels, wfID, jobID, store.RelContains)
	assertRelationship(t, rels, wfID, trgID, store.RelContains)
}

func TestResolveGlueIdentityCenterRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ssoARN := "arn:aws:sso:::instance/ssoins-abc"
	ssoID := upsertTestResource(t, st, "aws", acct.ID, TypeSSOInstance, ssoARN, "", "{}")
	icARN := fmt.Sprintf("arn:aws:glue:%s:%s:identity-center-configuration", testRegion, acct.ID)
	icAttrs := fmt.Sprintf(`{"InstanceArn":%q}`, ssoARN)
	icID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueIdentityCenterConfiguration, icARN, testRegion, icAttrs)
	if err := resolveGlueIdentityCenterRefs(acct, st); err != nil {
		t.Fatalf("resolveGlueIdentityCenterRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(icID)
	assertRelationship(t, rels, icID, ssoID, store.RelUses)
}
