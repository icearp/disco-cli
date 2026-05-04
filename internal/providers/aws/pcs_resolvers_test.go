package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolvePCSChildrenToCluster(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cid := "abc123"
	cARN := fmt.Sprintf("arn:aws:pcs:%s:%s:cluster/%s", testRegion, acct.ID, cid)
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypePCSCluster, cARN, testRegion, "{}")

	cngARN := cARN + "/computenodegroup/cng-1"
	cngAttrs := fmt.Sprintf(`{"ClusterId":%q}`, cid)
	cngID := upsertTestResource(t, st, "aws", acct.ID, TypePCSComputeNodeGroup, cngARN, testRegion, cngAttrs)
	qARN := cARN + "/queue/q-1"
	qAttrs := fmt.Sprintf(`{"ClusterId":%q}`, cid)
	qID := upsertTestResource(t, st, "aws", acct.ID, TypePCSQueue, qARN, testRegion, qAttrs)

	if err := resolvePCSChildrenToCluster(acct, st); err != nil {
		t.Fatalf("resolvePCSChildrenToCluster: %v", err)
	}
	for _, src := range []string{cngID, qID} {
		rels, _ := st.RelationshipsFrom(src)
		assertRelationship(t, rels, src, clusterID, store.RelAttachedTo)
	}
}
