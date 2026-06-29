package aws

import (
	"encoding/json"
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	bactypes "github.com/aws/aws-sdk-go-v2/service/bedrockagentcorecontrol/types"
)

func bacAttrs(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal bac attrs: %v", err)
	}
	return string(b)
}

func TestResolveBACPaymentEdges(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	roleArn := fmt.Sprintf("arn:aws:iam::%s:role/payment-role", testAccountID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleArn, "", `{"Arn":"`+roleArn+`"}`)

	mgrArn := bacARN(testRegion, testAccountID, "payment-manager", "pm-1")
	mgrID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCorePaymentManager, mgrArn, testRegion,
		bacAttrs(t, bactypes.PaymentManagerSummary{
			PaymentManagerArn: &mgrArn, PaymentManagerId: sdkaws.String("pm-1"), RoleArn: &roleArn,
		}))

	connArn := bacARN(testRegion, testAccountID, "payment-connector", "pm-1/pc-1")
	connID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCorePaymentConnector, connArn, testRegion,
		bacAttrs(t, bactypes.PaymentConnectorSummary{PaymentConnectorId: sdkaws.String("pc-1")}))

	if err := resolveBACPaymentConnectorParent(acct, st); err != nil {
		t.Fatalf("connector parent: %v", err)
	}
	if err := resolveBACPaymentManagerRole(acct, st); err != nil {
		t.Fatalf("manager role: %v", err)
	}

	connRels, _ := st.RelationshipsFrom(connID)
	assertRelationship(t, connRels, connID, mgrID, store.RelAttachedTo)
	mgrRels, _ := st.RelationshipsFrom(mgrID)
	assertRelationship(t, mgrRels, mgrID, roleID, store.RelAssumes)
}

// A manager whose RoleArn points at an unscanned role, a manager with no
// RoleArn, and a connector referencing an unscanned manager all emit no edge.
func TestResolveBACPaymentEdges_NoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// manager with RoleArn pointing at a role that was never scanned (FK-safe path)
	unscannedRole := fmt.Sprintf("arn:aws:iam::%s:role/never-scanned", testAccountID)
	mgrArn := bacARN(testRegion, testAccountID, "payment-manager", "pm-1")
	mgrID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCorePaymentManager, mgrArn, testRegion,
		bacAttrs(t, bactypes.PaymentManagerSummary{
			PaymentManagerArn: &mgrArn, PaymentManagerId: sdkaws.String("pm-1"), RoleArn: &unscannedRole,
		}))

	connArn := bacARN(testRegion, testAccountID, "payment-connector", "pm-missing/pc-1")
	connID := upsertTestResource(t, st, "aws", acct.ID, TypeBedrockAgentCorePaymentConnector, connArn, testRegion,
		bacAttrs(t, bactypes.PaymentConnectorSummary{PaymentConnectorId: sdkaws.String("pc-1")}))

	if err := resolveBACPaymentConnectorParent(acct, st); err != nil {
		t.Fatalf("connector parent: %v", err)
	}
	if err := resolveBACPaymentManagerRole(acct, st); err != nil {
		t.Fatalf("manager role: %v", err)
	}
	for _, id := range []string{mgrID, connID} {
		rels, _ := st.RelationshipsFrom(id)
		if len(rels) != 0 {
			t.Errorf("row %s emitted %d edges, want 0", id, len(rels))
		}
	}
}
