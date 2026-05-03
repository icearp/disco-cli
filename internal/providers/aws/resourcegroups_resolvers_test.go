package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveResourceGroupsTagSyncTaskToGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gARN := fmt.Sprintf("arn:aws:resource-groups:%s:%s:group/g1", testRegion, acct.ID)
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeResourceGroupsGroup, gARN, testRegion, "{}")
	tARN := fmt.Sprintf("arn:aws:resource-groups:%s:%s:tag-sync-task/uuid-1", testRegion, acct.ID)
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeResourceGroupsTagSyncTask, tARN, testRegion, fmt.Sprintf(`{"GroupArn":"%s"}`, gARN))
	if err := resolveResourceGroupsTagSyncTaskToGroup(acct, st); err != nil {
		t.Fatalf("resolveResourceGroupsTagSyncTaskToGroup: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tID)
	assertRelationship(t, rels, tID, gID, store.RelAttachedTo)
}
