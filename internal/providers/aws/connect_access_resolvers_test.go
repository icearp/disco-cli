package aws

import (
	"testing"

	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveConnectLambdaFunctionTarget(t *testing.T) {
	st := newTestStore(t)
	fnARN := connectTestFunctionARN + "fn"
	upsertTestResource(t, st, "aws", testAccountID, TypeLambdaFunction, fnARN, testRegion, "{}")
	linked := connectTestInstanceARN + "/lambda-function/" + fnARN
	qualified := connectTestInstanceARN + "/lambda-function/" + fnARN + ":live"
	unscanned := connectTestInstanceARN + "/lambda-function/" + connectTestFunctionARN + "other"
	empty := connectTestInstanceARN + "/lambda-function/empty"
	for id, fn := range map[string]string{linked: fnARN, qualified: fnARN + ":live", unscanned: connectTestFunctionARN + "other"} {
		upsertTestResource(t, st, "aws", testAccountID, TypeConnectLambdaFunction, id, testRegion, mustJSON(connectLambdaFunctionAttrs{FunctionArn: fn}))
	}
	upsertTestResource(t, st, "aws", testAccountID, TypeConnectLambdaFunction, empty, testRegion, "{}")
	if err := resolveConnectLambdaFunctionTarget(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	fnID := store.ResourceID("aws", testAccountID, fnARN)
	src := store.ResourceID("aws", testAccountID, linked)
	rels, err := st.RelationshipsFrom(src, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, src, fnID, store.RelUses)
	qsrc := store.ResourceID("aws", testAccountID, qualified)
	qrels, err := st.RelationshipsFrom(qsrc, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, qrels, qsrc, fnID, store.RelUses)
	for _, id := range []string{unscanned, empty} {
		rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, id), store.RelUses)
		if err != nil || len(rels) != 0 {
			t.Errorf("%s: edges = %v (err %v), want none", id, rels, err)
		}
	}
}

func TestResolveConnectRoutingProfileQueueTarget(t *testing.T) {
	st := newTestStore(t)
	queueARN := connectTestInstanceARN + "/queue/q-1"
	upsertTestResource(t, st, "aws", testAccountID, TypeConnectQueue, queueARN, testRegion, "{}")
	profile := connectTestInstanceARN + "/routing-profile/rp-1"
	linked := profile + "/queue/q-1/channel/VOICE"
	unscanned := profile + "/queue/q-2/channel/VOICE"
	upsertTestResource(t, st, "aws", testAccountID, TypeConnectRoutingProfileQueue, linked, testRegion,
		mustJSON(cttypes.RoutingProfileQueueConfigSummary{QueueId: ptrStr("q-1"), QueueArn: &queueARN, Channel: cttypes.ChannelVoice}))
	upsertTestResource(t, st, "aws", testAccountID, TypeConnectRoutingProfileQueue, unscanned, testRegion,
		mustJSON(cttypes.RoutingProfileQueueConfigSummary{QueueId: ptrStr("q-2"), QueueArn: ptrStr(connectTestInstanceARN + "/queue/q-2"), Channel: cttypes.ChannelVoice}))
	if err := resolveConnectRoutingProfileQueueTarget(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	src := store.ResourceID("aws", testAccountID, linked)
	rels, err := st.RelationshipsFrom(src, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, src, store.ResourceID("aws", testAccountID, queueARN), store.RelUses)
	if rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, unscanned), store.RelUses); err != nil || len(rels) != 0 {
		t.Errorf("unscanned queue: edges = %v (err %v), want none", rels, err)
	}
}

// A row that fails to decode is returned, and costs the other rows no edge.
func TestConnectARNFieldEdge_DecodeErrorKeepsOtherEdges(t *testing.T) {
	st := newTestStore(t)
	queueARN := connectTestInstanceARN + "/queue/q-1"
	upsertTestResource(t, st, "aws", testAccountID, TypeConnectQueue, queueARN, testRegion, "{}")
	good := connectTestInstanceARN + "/routing-profile/rp-1/queue/q-1/channel/VOICE"
	upsertTestResource(t, st, "aws", testAccountID, TypeConnectRoutingProfileQueue, good, testRegion,
		mustJSON(cttypes.RoutingProfileQueueConfigSummary{QueueArn: &queueARN}))
	upsertTestResource(t, st, "aws", testAccountID, TypeConnectRoutingProfileQueue, connectTestInstanceARN+"/routing-profile/rp-1/queue/q-9/channel/CHAT", testRegion,
		`{"QueueArn":7}`)
	if err := resolveConnectRoutingProfileQueueTarget(newTestAccount(testAccountID), st); err == nil {
		t.Error("err = nil, want the decode error")
	}
	src := store.ResourceID("aws", testAccountID, good)
	rels, err := st.RelationshipsFrom(src, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, src, store.ResourceID("aws", testAccountID, queueARN), store.RelUses)
}

func TestConnectAccessResolvers_NoRows(t *testing.T) {
	st := newTestStore(t)
	for name, resolve := range map[string]func(*account, *store.Store) error{
		"lambda": resolveConnectLambdaFunctionTarget,
		"queue":  resolveConnectRoutingProfileQueueTarget,
	} {
		if err := resolve(newTestAccount(testAccountID), st); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
