package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveSFNRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	smARN := fmt.Sprintf("arn:aws:states:%s:%s:stateMachine:my-workflow", testRegion, acct.ID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/sfn-role", acct.ID)
	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:target-fn", testRegion, acct.ID)
	topicARN := fmt.Sprintf("arn:aws:sns:%s:%s:target-topic", testRegion, acct.ID)
	tableARN := fmt.Sprintf("arn:aws:dynamodb:%s:%s:table/target-table", testRegion, acct.ID)
	lgName := "/aws/vendedlogs/states/my-workflow"
	lgARN := fmt.Sprintf("arn:aws:logs:%s:%s:log-group:%s:*", testRegion, acct.ID, lgName)
	lgNativeID := fmt.Sprintf("arn:aws:logs:%s:%s:log-group:%s", testRegion, acct.ID, lgName)

	// Definition inlines service integrations — resolver extracts via Resource + Parameters scan.
	definition := fmt.Sprintf(`{"StartAt":"Lambda","States":{`+
		`"Lambda":{"Type":"Task","Resource":%q,"Next":"SNS"},`+
		`"SNS":{"Type":"Task","Resource":"arn:aws:states:::sns:publish","Parameters":{"TopicArn":%q},"Next":"DDB"},`+
		`"DDB":{"Type":"Task","Resource":"arn:aws:states:::dynamodb:putItem","Parameters":{"TableName":%q},"End":true}`+
		`}}`, fnARN, topicARN, tableARN)

	attrs := fmt.Sprintf(
		`{"RoleArn":%q,"Definition":%q,"LoggingConfiguration":{"Destinations":[{"CloudWatchLogsLogGroup":{"LogGroupArn":%q}}]}}`,
		roleARN, definition, lgARN,
	)

	smID := upsertTestResource(t, st, "aws", acct.ID, TypeSFNStateMachine, smARN, testRegion, attrs)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")
	topicID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topicARN, testRegion, "{}")
	tableID := upsertTestResource(t, st, "aws", acct.ID, TypeDynamoDBTable, tableARN, testRegion, "{}")
	lgID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, lgNativeID, testRegion, "{}")

	if err := resolveSFNRelationships(acct, st); err != nil {
		t.Fatalf("resolveSFNRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(smID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, smID, roleID, store.RelAssumes)
	assertRelationship(t, rels, smID, lgID, store.RelUses)
	assertRelationship(t, rels, smID, fnID, store.RelRoutesTo)
	assertRelationship(t, rels, smID, topicID, store.RelRoutesTo)
	assertRelationship(t, rels, smID, tableID, store.RelRoutesTo)
}

func TestResolveSFNRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	smARN := fmt.Sprintf("arn:aws:states:%s:%s:stateMachine:bare", testRegion, acct.ID)
	smID := upsertTestResource(t, st, "aws", acct.ID, TypeSFNStateMachine, smARN, testRegion, `{}`)

	if err := resolveSFNRelationships(acct, st); err != nil {
		t.Fatalf("resolveSFNRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(smID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
