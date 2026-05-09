package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveKafkaConnectConnectorRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pluginARN := fmt.Sprintf("arn:aws:kafkaconnect:%s:%s:custom-plugin/p1/uuid", testRegion, acct.ID)
	pluginID := upsertTestResource(t, st, "aws", acct.ID, TypeKafkaConnectCustomPlugin, pluginARN, testRegion, "{}")
	wcARN := fmt.Sprintf("arn:aws:kafkaconnect:%s:%s:worker-configuration/w1/uuid", testRegion, acct.ID)
	wcID := upsertTestResource(t, st, "aws", acct.ID, TypeKafkaConnectWorkerConfiguration, wcARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/kc", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	cARN := fmt.Sprintf("arn:aws:kafkaconnect:%s:%s:connector/c1/uuid", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"Plugins":[{"CustomPlugin":{"CustomPluginArn":"%s"}}],"WorkerConfiguration":{"WorkerConfigurationArn":"%s"},"ServiceExecutionRoleArn":"%s"}`, pluginARN, wcARN, roleARN)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeKafkaConnectConnector, cARN, testRegion, attrs)
	if err := resolveKafkaConnectConnectorRefs(acct, st); err != nil {
		t.Fatalf("resolveKafkaConnectConnectorRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, pluginID, store.RelUses)
	assertRelationship(t, rels, cID, wcID, store.RelUses)
	assertRelationship(t, rels, cID, roleID, store.RelUses)
}
