package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func cfnStackARN(region, acct, name, id string) string {
	return fmt.Sprintf("arn:aws:cloudformation:%s:%s:stack/%s/%s", region, acct, name, id)
}

func cfnStackSetARN(region, acct, name string) string {
	return fmt.Sprintf("arn:aws:cloudformation:%s:%s:stackset/%s:abcd1234", region, acct, name)
}

// TestResolveCloudFormationStackResources covers the happy path: a stack
// containing an S3 bucket, a Lambda function, and an IAM role — all scanned —
// emits three contains edges with NativeIDs synthesized correctly per binding.
func TestResolveCloudFormationStackResources(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	bucketName := "my-bucket"
	bucketARN := "arn:aws:s3:::" + bucketName
	bucketID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, testRegion, `{}`)

	fnName := "my-fn"
	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:%s", testRegion, acct.ID, fnName)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, `{}`)

	roleName := "my-role"
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s", acct.ID, roleName)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, `{}`)

	stackARN := cfnStackARN(testRegion, acct.ID, "demo", "11111111-2222-3333-4444-555555555555")
	attrs := fmt.Sprintf(`{
		"Resources": [
			{"LogicalResourceId":"Bucket","PhysicalResourceId":%q,"ResourceType":"AWS::S3::Bucket","ResourceStatus":"CREATE_COMPLETE"},
			{"LogicalResourceId":"Fn","PhysicalResourceId":%q,"ResourceType":"AWS::Lambda::Function","ResourceStatus":"CREATE_COMPLETE"},
			{"LogicalResourceId":"Role","PhysicalResourceId":%q,"ResourceType":"AWS::IAM::Role","ResourceStatus":"UPDATE_COMPLETE"}
		]
	}`, bucketName, fnName, roleName)
	stackID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFormationStack, stackARN, testRegion, attrs)

	if err := resolveCloudFormationStackResources(acct, st); err != nil {
		t.Fatalf("resolveCloudFormationStackResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(stackID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, stackID, bucketID, store.RelContains)
	assertRelationship(t, rels, stackID, fnID, store.RelContains)
	assertRelationship(t, rels, stackID, roleID, store.RelContains)
}

// TestResolveCloudFormationStackResources_UnmappedType verifies an unmapped
// CloudFormation type silently skips, neither emitting an edge nor erroring.
func TestResolveCloudFormationStackResources_UnmappedType(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	stackARN := cfnStackARN(testRegion, acct.ID, "u", "id")
	attrs := `{"Resources":[{"LogicalResourceId":"Sub","PhysicalResourceId":"my-sub","ResourceType":"AWS::RDS::DBSubnetGroup","ResourceStatus":"CREATE_COMPLETE"}]}`
	stackID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFormationStack, stackARN, testRegion, attrs)

	if err := resolveCloudFormationStackResources(acct, st); err != nil {
		t.Fatalf("resolveCloudFormationStackResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(stackID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("unmapped type should emit no edges, got %d", len(rels))
	}
}

// TestResolveCloudFormationStackResources_EmptyAndFailedStatus verifies that
// rows with empty PhysicalResourceId, CREATE_FAILED, and DELETE_COMPLETE are
// all skipped — these point at non-existent or no-longer-existent targets.
func TestResolveCloudFormationStackResources_EmptyAndFailedStatus(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	bucketARN := "arn:aws:s3:::ghost-bucket"
	upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, testRegion, `{}`)

	stackARN := cfnStackARN(testRegion, acct.ID, "f", "id")
	attrs := `{
		"Resources": [
			{"LogicalResourceId":"Empty","PhysicalResourceId":"","ResourceType":"AWS::S3::Bucket","ResourceStatus":"CREATE_IN_PROGRESS"},
			{"LogicalResourceId":"Failed","PhysicalResourceId":"ghost-bucket","ResourceType":"AWS::S3::Bucket","ResourceStatus":"CREATE_FAILED"},
			{"LogicalResourceId":"Deleted","PhysicalResourceId":"ghost-bucket","ResourceType":"AWS::S3::Bucket","ResourceStatus":"DELETE_COMPLETE"}
		]
	}`
	stackID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFormationStack, stackARN, testRegion, attrs)

	if err := resolveCloudFormationStackResources(acct, st); err != nil {
		t.Fatalf("resolveCloudFormationStackResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(stackID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected zero edges for empty/failed/deleted rows, got %d", len(rels))
	}
}

// TestResolveCloudFormationStackResources_FKSafe verifies cross-account /
// unscanned targets do not cause a FK error or phantom edge.
func TestResolveCloudFormationStackResources_FKSafe(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	stackARN := cfnStackARN(testRegion, acct.ID, "f", "id")
	attrs := `{"Resources":[{"LogicalResourceId":"X","PhysicalResourceId":"unscanned-bucket","ResourceType":"AWS::S3::Bucket","ResourceStatus":"CREATE_COMPLETE"}]}`
	stackID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFormationStack, stackARN, testRegion, attrs)

	if err := resolveCloudFormationStackResources(acct, st); err != nil {
		t.Fatalf("resolveCloudFormationStackResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(stackID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("unscanned target should skip, got %d edges", len(rels))
	}
}

// TestResolveCloudFormationStackResources_SQSURLConversion verifies the queue
// URL → ARN conversion path matches a scanned SQS queue's NativeID.
func TestResolveCloudFormationStackResources_SQSURLConversion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	queueName := "my-queue"
	queueARN := fmt.Sprintf("arn:aws:sqs:%s:%s:%s", testRegion, acct.ID, queueName)
	queueID := upsertTestResource(t, st, "aws", acct.ID, TypeSQSQueue, queueARN, testRegion, `{}`)

	stackARN := cfnStackARN(testRegion, acct.ID, "q", "id")
	queueURL := fmt.Sprintf("https://sqs.%s.amazonaws.com/%s/%s", testRegion, acct.ID, queueName)
	attrs := fmt.Sprintf(`{"Resources":[{"PhysicalResourceId":%q,"ResourceType":"AWS::SQS::Queue","ResourceStatus":"CREATE_COMPLETE"}]}`, queueURL)
	stackID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFormationStack, stackARN, testRegion, attrs)

	if err := resolveCloudFormationStackResources(acct, st); err != nil {
		t.Fatalf("resolveCloudFormationStackResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(stackID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, stackID, queueID, store.RelContains)
}

// TestResolveCloudFormationStackResources_DefaultBusEventRule verifies the
// happy default-bus rule synth path resolves to the scanned rule, while a
// pipe-form custom-bus PhysicalResourceId is rejected (no edge).
func TestResolveCloudFormationStackResources_EventBusRule(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	ruleName := "my-rule"
	ruleARN := fmt.Sprintf("arn:aws:events:%s:%s:rule/%s", testRegion, acct.ID, ruleName)
	ruleID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsRule, ruleARN, testRegion, `{}`)

	stackARN := cfnStackARN(testRegion, acct.ID, "e", "id")
	attrs := fmt.Sprintf(`{"Resources":[
		{"PhysicalResourceId":%q,"ResourceType":"AWS::Events::Rule","ResourceStatus":"CREATE_COMPLETE"},
		{"PhysicalResourceId":"custom-bus|other-rule","ResourceType":"AWS::Events::Rule","ResourceStatus":"CREATE_COMPLETE"}
	]}`, ruleName)
	stackID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFormationStack, stackARN, testRegion, attrs)

	if err := resolveCloudFormationStackResources(acct, st); err != nil {
		t.Fatalf("resolveCloudFormationStackResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(stackID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, stackID, ruleID, store.RelContains)
	if len(rels) != 1 {
		t.Errorf("custom-bus pipe-form rule should skip, got %d edges total", len(rels))
	}
}

// TestResolveCloudFormationStackResources_NestedStack verifies a nested
// AWS::CloudFormation::Stack child emits a stack→stack contains edge.
func TestResolveCloudFormationStackResources_NestedStack(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	childARN := cfnStackARN(testRegion, acct.ID, "child", "child-id")
	childID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFormationStack, childARN, testRegion, `{}`)

	parentARN := cfnStackARN(testRegion, acct.ID, "parent", "parent-id")
	attrs := fmt.Sprintf(`{"Resources":[{"PhysicalResourceId":%q,"ResourceType":"AWS::CloudFormation::Stack","ResourceStatus":"CREATE_COMPLETE"}]}`, childARN)
	parentID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFormationStack, parentARN, testRegion, attrs)

	if err := resolveCloudFormationStackResources(acct, st); err != nil {
		t.Fatalf("resolveCloudFormationStackResources: %v", err)
	}
	rels, err := st.RelationshipsFrom(parentID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, parentID, childID, store.RelContains)
}

// TestResolveCloudFormationStackSetInstances verifies a stack-set emits one
// contains edge per deployed instance whose StackId is in the scan, while
// instances pointing at unscanned stacks skip.
func TestResolveCloudFormationStackSetInstances(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	deployedARN := cfnStackARN("us-west-2", acct.ID, "deployed", "deployed-id")
	deployedID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFormationStack, deployedARN, "us-west-2", `{}`)

	setARN := cfnStackSetARN(testRegion, acct.ID, "my-set")
	attrs := fmt.Sprintf(`{
		"Instances": [
			{"StackId":%q,"Account":%q,"Region":"us-west-2"},
			{"StackId":"arn:aws:cloudformation:us-east-1:999999999999:stack/orphan/orphan-id","Account":"999999999999","Region":"us-east-1"}
		]
	}`, deployedARN, acct.ID)
	setID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudFormationStackSet, setARN, testRegion, attrs)

	if err := resolveCloudFormationStackSetInstances(acct, st); err != nil {
		t.Fatalf("resolveCloudFormationStackSetInstances: %v", err)
	}
	rels, err := st.RelationshipsFrom(setID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, setID, deployedID, store.RelContains)
	if len(rels) != 1 {
		t.Errorf("orphan instance should skip, got %d edges total", len(rels))
	}
}

// TestResolveCloudFormationStackResources_MalformedAttrs verifies a row with
// invalid attrs JSON does not panic or error — it simply yields no edges.
func TestResolveCloudFormationStackResources_MalformedAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	stackARN := cfnStackARN(testRegion, acct.ID, "m", "id")
	upsertTestResource(t, st, "aws", acct.ID, TypeCloudFormationStack, stackARN, testRegion, `not json`)

	if err := resolveCloudFormationStackResources(acct, st); err != nil {
		t.Fatalf("resolveCloudFormationStackResources: %v", err)
	}
}
