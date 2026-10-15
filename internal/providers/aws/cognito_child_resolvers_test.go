package aws

import (
	"testing"

	cognitoidptypes "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveCognitoReplicaPool(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	primaryARN := cogTestPoolARN(testRegion, "p1")
	westPool := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoUserPool, cogTestPoolARN("us-west-2", "pool"), "us-west-2", "{}")
	scanned := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoUserPoolReplica, primaryARN+"/replica/us-west-2", testRegion,
		mustJSON(cogReplica("us-west-2", cognitoidptypes.ReplicaRoleTypeSecondary, cognitoidptypes.ReplicaStatusTypeActive)))
	unscanned := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoUserPoolReplica, primaryARN+"/replica/eu-west-1", testRegion,
		mustJSON(cogReplica("eu-west-1", cognitoidptypes.ReplicaRoleTypeSecondary, cognitoidptypes.ReplicaStatusTypeActive)))
	noARN := upsertTestResource(t, st, "aws", acct.ID, TypeCognitoUserPoolReplica, primaryARN+"/replica/ap-south-1", testRegion,
		mustJSON(cognitoidptypes.UserPoolReplicaType{}))

	if err := resolveCognitoReplicaPool(acct, st); err != nil {
		t.Fatalf("resolveCognitoReplicaPool: %v", err)
	}
	rels, err := st.RelationshipsFrom(scanned)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, scanned, westPool, store.RelAttachedTo)
	for name, id := range map[string]string{"unscanned pool": unscanned, "no UserPoolArn": noARN} {
		if rels, _ := st.RelationshipsFrom(id); len(rels) != 0 {
			t.Errorf("%s: edges %v; want none", name, rels)
		}
	}
}

func TestResolveCognitoReplicaPool_UndecodableAttrsReturned(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeCognitoUserPoolReplica, cogTestPoolARN(testRegion, "p1")+"/replica/us-west-2", testRegion, `{"UserPoolArn":7}`)
	if err := resolveCognitoReplicaPool(acct, st); err == nil {
		t.Error("err = nil; want the decode error")
	}
}
