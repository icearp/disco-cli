package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveSupplyChainInstanceChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	instID := "instance-abc123"
	instARN := scnInstanceARN(testRegion, acct.ID, instID)
	instRowID := upsertTestResource(t, st, "aws", acct.ID, TypeSupplyChainInstance, instARN, testRegion, "{}")

	flowARN := fmt.Sprintf("%s/data-integration-flow/myflow", instARN)
	flowAttrs := fmt.Sprintf(`{"InstanceId":%q,"Name":"myflow"}`, instID)
	flowID := upsertTestResource(t, st, "aws", acct.ID, TypeSupplyChainDataIntegrationFlow, flowARN, testRegion, flowAttrs)

	dsARN := fmt.Sprintf("arn:aws:scn:%s:%s:instance/%s/namespace/asc/dataset/orders", testRegion, acct.ID, instID)
	dsAttrs := fmt.Sprintf(`{"InstanceId":%q,"Namespace":"asc","Name":"orders"}`, instID)
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeSupplyChainDataset, dsARN, testRegion, dsAttrs)

	if err := resolveSupplyChainInstanceChildren(acct, st); err != nil {
		t.Fatalf("resolveSupplyChainInstanceChildren: %v", err)
	}
	relsFlow, _ := st.RelationshipsFrom(flowID)
	assertRelationship(t, relsFlow, flowID, instRowID, store.RelAttachedTo)
	relsDS, _ := st.RelationshipsFrom(dsID)
	assertRelationship(t, relsDS, dsID, instRowID, store.RelAttachedTo)
}

func TestResolveSupplyChainInstanceChildren_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	instID := "instance-abc123"
	instARN := scnInstanceARN(testRegion, acct.ID, instID)
	upsertTestResource(t, st, "aws", acct.ID, TypeSupplyChainInstance, instARN, testRegion, "{}")

	// Namespace row with empty attrs must not panic and must yield no edge.
	nsARN := fmt.Sprintf("arn:aws:scn:%s:%s:instance/%s/namespace/asc", testRegion, acct.ID, instID)
	nsID := upsertTestResource(t, st, "aws", acct.ID, TypeSupplyChainNamespace, nsARN, testRegion, "{}")

	if err := resolveSupplyChainInstanceChildren(acct, st); err != nil {
		t.Fatalf("resolveSupplyChainInstanceChildren: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(nsID); len(rels) != 0 {
		t.Errorf("expected no edge for namespace with empty attrs, got %d", len(rels))
	}
}
