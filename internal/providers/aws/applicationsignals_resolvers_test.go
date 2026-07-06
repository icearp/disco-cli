package aws

import (
	"testing"
)

// TestResolveApplicationSignalsRelationships_NoEdges documents the no-op
// contract: SLO + GroupingConfiguration rows carry no cross-resource ARNs,
// so the resolver intentionally emits zero edges. Guards against a future
// SDK adding ARN fields without an audit pass silently breaking this.
func TestResolveApplicationSignalsRelationships_NoEdges(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	sloARN := "arn:aws:application-signals:us-east-1:123456789012:slo/example"
	sloID := upsertTestResource(t, st, "aws", acct.ID, TypeApplicationSignalsSLO, sloARN, region, "{}")

	gcNative := applicationSignalsGroupingConfigurationNativeID(region, acct.ID)
	gcID := upsertTestResource(t, st, "aws", acct.ID, TypeApplicationSignalsGroupingConfiguration, gcNative, region, `{"GroupingAttributeDefinitions":[{"GroupingName":"BU"}]}`)

	if err := resolveApplicationSignalsRelationships(acct, st); err != nil {
		t.Fatalf("resolveApplicationSignalsRelationships: %v", err)
	}
	for _, id := range []string{sloID, gcID} {
		rels, err := st.RelationshipsFrom(id)
		if err != nil {
			t.Fatalf("RelationshipsFrom %s: %v", id, err)
		}
		if len(rels) != 0 {
			t.Errorf("expected no edges for %s; got %+v", id, rels)
		}
	}
}
