package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

var baseFnARN = fmt.Sprintf("arn:aws:lambda:%s:%s:function:my-fn", testRegion, testAccountID)

// TestResolveLambdaRelationships verifies that a Lambda function's IAM role ARN
// is correctly extracted from AttributesJSON and produces an assumes relationship.
func TestResolveLambdaRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	roleARN := "arn:aws:iam::" + testAccountID + ":role/my-lambda-role"
	fnARN := baseFnARN
	attrsJSON := `{"Role": "` + roleARN + `"}`

	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, "", attrsJSON)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")

	if err := resolveLambdaRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(fnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	if rels[0].ToID != roleID || rels[0].Kind != store.RelAssumes {
		t.Errorf("expected fn -[assumes]-> role, got %+v", rels[0])
	}
}

// TestResolveLambdaRelationships_NoRole verifies that a function without a Role
// field produces no relationships and no error.
func TestResolveLambdaRelationships_NoRole(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, baseFnARN+"-bare", "", "{}")

	if err := resolveLambdaRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(fnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// TestResolveLambdaAliasRelationships verifies that an alias is linked to its
// function via an attached-to edge.
func TestResolveLambdaAliasRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	aliasARN := baseFnARN + ":prod"
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, baseFnARN, "", "{}")
	aliasID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaAlias, aliasARN, "", "{}")

	if err := resolveLambdaAliasRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaAliasRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(aliasID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, aliasID, fnID, store.RelAttachedTo)
}

// TestResolveLambdaAliasRelationships_NoAliases verifies no error when there
// are no alias resources.
func TestResolveLambdaAliasRelationships_NoAliases(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	if err := resolveLambdaAliasRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaAliasRelationships: %v", err)
	}
}

// TestResolveLambdaVersionRelationships verifies that a published version is
// linked to its function via an attached-to edge.
func TestResolveLambdaVersionRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	versionARN := baseFnARN + ":3"
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, baseFnARN, "", "{}")
	versionID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaVersion, versionARN, "", "{}")

	if err := resolveLambdaVersionRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaVersionRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(versionID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, versionID, fnID, store.RelAttachedTo)
}

// TestResolveLambdaVersionRelationships_NoVersions verifies no error when there
// are no version resources.
func TestResolveLambdaVersionRelationships_NoVersions(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	if err := resolveLambdaVersionRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaVersionRelationships: %v", err)
	}
}

// TestResolveLambdaESMRelationships verifies that an event source mapping is
// linked to its target function via an attached-to edge.
func TestResolveLambdaESMRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	esmARN := fmt.Sprintf("arn:aws:lambda:%s:%s:event-source-mapping:uuid-1234", testRegion, testAccountID)
	attrsJSON := fmt.Sprintf(`{"FunctionArn": %q}`, baseFnARN+":prod")

	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, baseFnARN, "", "{}")
	esmID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaESM, esmARN, "", attrsJSON)

	if err := resolveLambdaESMRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaESMRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(esmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, esmID, fnID, store.RelAttachedTo)
}

// TestResolveLambdaESMRelationships_NoFunctionArn verifies that an ESM without
// a FunctionArn produces no relationships and no error.
func TestResolveLambdaESMRelationships_NoFunctionArn(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	esmARN := fmt.Sprintf("arn:aws:lambda:%s:%s:event-source-mapping:uuid-5678", testRegion, testAccountID)
	esmID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaESM, esmARN, "", "{}")

	if err := resolveLambdaESMRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaESMRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(esmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// TestResolveLambdaEventInvokeConfigRelationships verifies that an event invoke
// config is linked to its function via an attached-to edge.
func TestResolveLambdaEventInvokeConfigRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	configARN := baseFnARN + ":prod"
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, baseFnARN, "", "{}")
	configID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaEventInvokeConfig, configARN, "", "{}")

	if err := resolveLambdaEventInvokeConfigRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaEventInvokeConfigRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(configID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, configID, fnID, store.RelAttachedTo)
}

// TestResolveLambdaEventInvokeConfigRelationships_NoConfigs verifies no error
// when there are no event invoke config resources.
func TestResolveLambdaEventInvokeConfigRelationships_NoConfigs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	if err := resolveLambdaEventInvokeConfigRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaEventInvokeConfigRelationships: %v", err)
	}
}

// TestResolveLambdaFunctionURLRelationships verifies that a function URL config
// is linked to its function via an attached-to edge.
func TestResolveLambdaFunctionURLRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	urlARN := baseFnARN + ":myalias"
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, baseFnARN, "", "{}")
	urlID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaURL, urlARN, "", "{}")

	if err := resolveLambdaFunctionURLRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaFunctionURLRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(urlID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, urlID, fnID, store.RelAttachedTo)
}

// TestResolveLambdaFunctionURLRelationships_NoURLs verifies no error when there
// are no function URL resources.
func TestResolveLambdaFunctionURLRelationships_NoURLs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	if err := resolveLambdaFunctionURLRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaFunctionURLRelationships: %v", err)
	}
}

// TestResolveLambdaCodeSigningConfigRelationships verifies that a function with
// a CodeSigningConfigArn is linked to the config via a "uses" edge.
func TestResolveLambdaCodeSigningConfigRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	cscARN := fmt.Sprintf("arn:aws:lambda:%s:%s:code-signing-config:csc-abc123", testRegion, testAccountID)
	attrsJSON := fmt.Sprintf(`{"CodeSigningConfigArn": %q}`, cscARN)

	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, baseFnARN, "", attrsJSON)
	cscID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaCodeSigningConfig, cscARN, "", "{}")

	if err := resolveLambdaCodeSigningConfigRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaCodeSigningConfigRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(fnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, fnID, cscID, store.RelUses)
}

// TestResolveLambdaCodeSigningConfigRelationships_NoCSC verifies that a function
// without a CodeSigningConfigArn produces no relationships and no error.
func TestResolveLambdaCodeSigningConfigRelationships_NoCSC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, baseFnARN+"-nocsc", "", "{}")

	if err := resolveLambdaCodeSigningConfigRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaCodeSigningConfigRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(fnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// TestResolveLambdaLayerRelationships verifies that a function using a layer
// version is linked to that layer version via a "uses" edge.
func TestResolveLambdaLayerRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	layerARN := fmt.Sprintf("arn:aws:lambda:%s:%s:layer:my-layer:2", testRegion, testAccountID)
	attrsJSON := fmt.Sprintf(`{"Layers": [{"Arn": %q}]}`, layerARN)

	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, baseFnARN, "", attrsJSON)
	layerID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaLayerVersion, layerARN, "", "{}")

	if err := resolveLambdaLayerRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaLayerRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(fnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, fnID, layerID, store.RelUses)
}

// TestResolveLambdaLayerRelationships_FKSafe verifies that a function
// referencing an unscanned (e.g. AWS-managed, foreign-account) layer ARN
// emits no edge and returns no error. Regression guard for the FK
// constraint failure that occurs when a function uses a layer not
// returned by ListLayers in the caller account.
func TestResolveLambdaLayerRelationships_FKSafe(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Layer in a different (AWS-managed) account, never scanned.
	foreignLayer := "arn:aws:lambda:us-east-1:017000801446:layer:AWSAppConfigExtension:97"
	attrsJSON := fmt.Sprintf(`{"Layers":[{"Arn":%q}]}`, foreignLayer)

	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, baseFnARN+"-foreignlayer", testRegion, attrsJSON)

	if err := resolveLambdaLayerRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaLayerRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(fnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships (foreign layer unscanned), got %d", len(rels))
	}
}

// TestResolveLambdaLayerRelationships_NoLayers verifies that a function without
// layers produces no relationships and no error.
func TestResolveLambdaLayerRelationships_NoLayers(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, baseFnARN+"-nolayer", "", "{}")

	if err := resolveLambdaLayerRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaLayerRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(fnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// TestResolveLambdaRelationships_KMSAndVPC verifies that a VPC-attached
// function with a customer-managed KMS key emits edges to KMS, each subnet,
// and each security group.
func TestResolveLambdaRelationships_KMSAndVPC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:vpc-fn", testRegion, testAccountID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abcd", testRegion, testAccountID)
	attrsJSON := `{"KMSKeyArn":"` + keyARN + `","VpcConfig":{"SubnetIds":["subnet-aaa"],"SecurityGroupIds":["sg-bbb"]}}`

	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, attrsJSON)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet,
		ec2ARN(testRegion, acct.ID, "subnet", "subnet-aaa"), testRegion, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup,
		ec2ARN(testRegion, acct.ID, "security-group", "sg-bbb"), testRegion, "{}")

	if err := resolveLambdaRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(fnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, fnID, keyID, store.RelUses)
	assertRelationship(t, rels, fnID, subnetID, store.RelAttachedTo)
	assertRelationship(t, rels, fnID, sgID, store.RelUses)
}

// TestResolveLambdaESMRelationships_SQSSource verifies that an ESM with an SQS
// EventSourceArn produces an edge to the queue.
func TestResolveLambdaESMRelationships_SQSSource(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fnARN := baseFnARN
	queueARN := fmt.Sprintf("arn:aws:sqs:%s:%s:my-queue", testRegion, testAccountID)
	esmARN := fmt.Sprintf("arn:aws:lambda:%s:%s:event-source-mapping:abc", testRegion, testAccountID)
	attrs := `{"FunctionArn":"` + fnARN + `","EventSourceArn":"` + queueARN + `"}`

	esmID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaESM, esmARN, testRegion, attrs)
	upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")
	queueID := upsertTestResource(t, st, "aws", acct.ID, TypeSQSQueue, queueARN, testRegion, "{}")

	if err := resolveLambdaESMRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaESMRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(esmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, esmID, queueID, store.RelUses)
}

// TestResolveLambdaESMRelationships_KinesisSource verifies ESM with a Kinesis
// EventSourceArn produces an edge to the stream.
func TestResolveLambdaESMRelationships_KinesisSource(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	streamARN := fmt.Sprintf("arn:aws:kinesis:%s:%s:stream/events", testRegion, testAccountID)
	esmARN := fmt.Sprintf("arn:aws:lambda:%s:%s:event-source-mapping:k1", testRegion, testAccountID)
	attrs := `{"FunctionArn":"` + baseFnARN + `","EventSourceArn":"` + streamARN + `"}`

	esmID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaESM, esmARN, testRegion, attrs)
	upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, baseFnARN, testRegion, "{}")
	streamID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisStream, streamARN, testRegion, "{}")

	if err := resolveLambdaESMRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaESMRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(esmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, esmID, streamID, store.RelUses)
}

// TestResolveLambdaESMRelationships_MSKSource verifies ESM with an MSK
// EventSourceArn produces an edge to the cluster.
func TestResolveLambdaESMRelationships_MSKSource(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clusterARN := fmt.Sprintf("arn:aws:kafka:%s:%s:cluster/events-cluster/abc-123", testRegion, testAccountID)
	esmARN := fmt.Sprintf("arn:aws:lambda:%s:%s:event-source-mapping:k2", testRegion, testAccountID)
	attrs := `{"FunctionArn":"` + baseFnARN + `","EventSourceArn":"` + clusterARN + `"}`

	esmID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaESM, esmARN, testRegion, attrs)
	upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, baseFnARN, testRegion, "{}")
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeMSKCluster, clusterARN, testRegion, "{}")

	if err := resolveLambdaESMRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaESMRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(esmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, esmID, clusterID, store.RelUses)
}

// TestLambdaStripQualifier verifies the qualifier-stripping helper.
func TestLambdaStripQualifier(t *testing.T) {
	tests := []struct {
		arn  string
		want string
	}{
		{
			"arn:aws:lambda:us-east-1:123456789012:function:my-fn:prod",
			"arn:aws:lambda:us-east-1:123456789012:function:my-fn",
		},
		{
			"arn:aws:lambda:us-east-1:123456789012:function:my-fn:3",
			"arn:aws:lambda:us-east-1:123456789012:function:my-fn",
		},
		{
			"arn:aws:lambda:us-east-1:123456789012:function:my-fn:$LATEST",
			"arn:aws:lambda:us-east-1:123456789012:function:my-fn",
		},
		{
			// Already unqualified — returned unchanged.
			"arn:aws:lambda:us-east-1:123456789012:function:my-fn",
			"arn:aws:lambda:us-east-1:123456789012:function:my-fn",
		},
	}
	for _, tt := range tests {
		got := lambdaStripQualifier(tt.arn)
		if got != tt.want {
			t.Errorf("lambdaStripQualifier(%q) = %q, want %q", tt.arn, got, tt.want)
		}
	}
}

// TestResolveLambdaEFSRelationships verifies Function → EFS access point edge.
func TestResolveLambdaEFSRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fnARN := "arn:aws:lambda:us-east-1:" + testAccountID + ":function:fs-fn"
	apARN := "arn:aws:elasticfilesystem:us-east-1:" + testAccountID + ":access-point/fsap-abc123"
	attrs := `{"Role":"arn:aws:iam::` + testAccountID + `:role/r","FileSystemConfigs":[{"Arn":"` + apARN + `"}]}`

	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, attrs)
	apID := upsertTestResource(t, st, "aws", acct.ID, TypeEFSAccessPoint, apARN, testRegion, "{}")
	roleARN := "arn:aws:iam::" + testAccountID + ":role/r"
	upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")

	if err := resolveLambdaRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaFunctionRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(fnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, fnID, apID, store.RelUses)
}

// TestResolveLambdaRelationships_DLQSQS verifies Function → SQS dead-letter
// target edge.
func TestResolveLambdaRelationships_DLQSQS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fnARN := "arn:aws:lambda:us-east-1:" + testAccountID + ":function:dlq-sqs-fn"
	queueARN := "arn:aws:sqs:us-east-1:" + testAccountID + ":dlq"
	attrs := `{"DeadLetterConfig":{"TargetArn":"` + queueARN + `"}}`

	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, attrs)
	qID := upsertTestResource(t, st, "aws", acct.ID, TypeSQSQueue, queueARN, testRegion, "{}")

	if err := resolveLambdaRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(fnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, fnID, qID, store.RelUses)
}

// TestResolveLambdaRelationships_DLQSNS verifies Function → SNS dead-letter
// target edge.
func TestResolveLambdaRelationships_DLQSNS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fnARN := "arn:aws:lambda:us-east-1:" + testAccountID + ":function:dlq-sns-fn"
	topicARN := "arn:aws:sns:us-east-1:" + testAccountID + ":dlq-topic"
	attrs := `{"DeadLetterConfig":{"TargetArn":"` + topicARN + `"}}`

	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, attrs)
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topicARN, testRegion, "{}")

	if err := resolveLambdaRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(fnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, fnID, tID, store.RelUses)
}

// TestResolveLambdaRelationships_ImageECR verifies Function → ECR repository
// edge for image-package functions.
func TestResolveLambdaRelationships_ImageECR(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fnARN := "arn:aws:lambda:us-east-1:" + testAccountID + ":function:image-fn"
	imageURI := testAccountID + ".dkr.ecr.us-east-1.amazonaws.com/my-repo:v1"
	repoARN := "arn:aws:ecr:us-east-1:" + testAccountID + ":repository/my-repo"
	attrs := `{"PackageType":"Image","Code":{"ImageUri":"` + imageURI + `"}}`

	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeECRRepository, repoARN, testRegion, "{}")

	if err := resolveLambdaRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(fnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, fnID, rID, store.RelUses)
}

// TestResolveLambdaEventInvokeConfigRelationships_Destinations verifies
// destination dispatch to SQS, SNS, EventBridge bus, and Lambda function.
func TestResolveLambdaEventInvokeConfigRelationships_Destinations(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	cfgARN := baseFnARN + ":3"
	queueARN := "arn:aws:sqs:" + testRegion + ":" + testAccountID + ":invoke-fail"
	topicARN := "arn:aws:sns:" + testRegion + ":" + testAccountID + ":invoke-success"
	busARN := "arn:aws:events:" + testRegion + ":" + testAccountID + ":event-bus/custom"
	dlfnARN := "arn:aws:lambda:" + testRegion + ":" + testAccountID + ":function:downstream"
	attrs := `{"DestinationConfig":{"OnSuccess":{"Destination":"` + topicARN + `"},"OnFailure":{"Destination":"` + queueARN + `"}}}`

	cfgID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaEventInvokeConfig, cfgARN, testRegion, attrs)
	upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, baseFnARN, testRegion, "{}")
	qID := upsertTestResource(t, st, "aws", acct.ID, TypeSQSQueue, queueARN, testRegion, "{}")
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topicARN, testRegion, "{}")
	// bus + downstream fn included as scanned targets even though not
	// referenced by this fixture's DestinationConfig — exercises the id-set
	// load path.
	upsertTestResource(t, st, "aws", acct.ID, TypeEventsEventBus, busARN, testRegion, "{}")
	upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, dlfnARN, testRegion, "{}")

	if err := resolveLambdaEventInvokeConfigRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaEventInvokeConfigRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(cfgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, cfgID, qID, store.RelUses)
	assertRelationship(t, rels, cfgID, tID, store.RelUses)
}

// TestResolveLambdaESMRelationships_MQSource verifies ESM with an Amazon MQ
// EventSourceArn produces an edge to the broker.
func TestResolveLambdaESMRelationships_MQSource(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	brokerARN := fmt.Sprintf("arn:aws:mq:%s:%s:broker:my-broker:b-abc", testRegion, testAccountID)
	esmARN := fmt.Sprintf("arn:aws:lambda:%s:%s:event-source-mapping:mq1", testRegion, testAccountID)
	attrs := `{"FunctionArn":"` + baseFnARN + `","EventSourceArn":"` + brokerARN + `"}`

	esmID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaESM, esmARN, testRegion, attrs)
	upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, baseFnARN, testRegion, "{}")
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeMQBroker, brokerARN, testRegion, "{}")

	if err := resolveLambdaESMRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaESMRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(esmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, esmID, bID, store.RelUses)
}

// TestResolveLambdaPermissionRelationships verifies that a permission row
// links to its parent function via AttachedTo and to the SourceArn (S3
// bucket here) referenced by an Allow statement's Condition block.
func TestResolveLambdaPermissionRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fnARN := baseFnARN
	permARN := fnARN + "/policy"
	bucketARN := "arn:aws:s3:::my-source-bucket"
	policyJSON := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"s3.amazonaws.com"},"Action":"lambda:InvokeFunction","Resource":"` + fnARN + `","Condition":{"ArnLike":{"AWS:SourceArn":"` + bucketARN + `"}}}]}`
	attrs := fmt.Sprintf(`{"Policy": %q}`, policyJSON)

	permID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaPermission, permARN, testRegion, attrs)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, "", "{}")

	if err := resolveLambdaPermissionRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaPermissionRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(permID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, permID, fnID, store.RelAttachedTo)
	assertRelationship(t, rels, permID, bID, store.RelUses)
}

// TestResolveLambdaPermissionRelationships_NoCondition verifies that an
// account-principal permission (Principal.AWS, no SourceArn) emits only the
// AttachedTo edge.
func TestResolveLambdaPermissionRelationships_NoCondition(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fnARN := baseFnARN + "-acct"
	permARN := fnARN + "/policy"
	policyJSON := `{"Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::222222222222:root"},"Action":"lambda:InvokeFunction","Resource":"` + fnARN + `"}]}`
	attrs := fmt.Sprintf(`{"Policy": %q}`, policyJSON)

	permID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaPermission, permARN, testRegion, attrs)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")

	if err := resolveLambdaPermissionRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaPermissionRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(permID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, permID, fnID, store.RelAttachedTo)
}

// TestResolveLambdaLayerVersionPermissionRelationships verifies the
// AttachedTo edge to the parent layer version. Cross-account principals
// are not edge-bearing.
func TestResolveLambdaLayerVersionPermissionRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	layerARN := fmt.Sprintf("arn:aws:lambda:%s:%s:layer:my-layer:5", testRegion, testAccountID)
	permARN := layerARN + "/policy"

	permID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaLayerVersionPermission, permARN, testRegion, "{}")
	layerID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaLayerVersion, layerARN, testRegion, "{}")

	if err := resolveLambdaLayerVersionPermissionRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaLayerVersionPermissionRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(permID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, permID, layerID, store.RelAttachedTo)
}

// TestResolveLambdaESMRelationships_DocDBSource verifies ESM with a
// DocumentDB cluster EventSourceArn produces an edge to the cluster.
// DocDB cluster ARNs use the rds: prefix (historical artefact).
func TestResolveLambdaESMRelationships_DocDBSource(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clusterARN := fmt.Sprintf("arn:aws:rds:%s:%s:cluster:doc-db-clu", testRegion, testAccountID)
	esmARN := fmt.Sprintf("arn:aws:lambda:%s:%s:event-source-mapping:doc1", testRegion, testAccountID)
	attrs := `{"FunctionArn":"` + baseFnARN + `","EventSourceArn":"` + clusterARN + `"}`

	esmID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaESM, esmARN, testRegion, attrs)
	upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, baseFnARN, testRegion, "{}")
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeDocDBCluster, clusterARN, testRegion, "{}")

	if err := resolveLambdaESMRelationships(acct, st); err != nil {
		t.Fatalf("resolveLambdaESMRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(esmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, esmID, cID, store.RelUses)
}
