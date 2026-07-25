package aws

import (
	"fmt"
	"testing"

	outpoststypes "github.com/aws/aws-sdk-go-v2/service/outposts/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveOutpostSite(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	siteARN := fmt.Sprintf("arn:aws:outposts:%s:%s:site/os-1", testRegion, acct.ID)
	siteID := upsertTestResource(t, st, "aws", acct.ID, TypeOutpostsSite, siteARN, testRegion, "{}")
	opARN := fmt.Sprintf("arn:aws:outposts:%s:%s:outpost/op-1", testRegion, acct.ID)
	opAttrs := mustJSON(outpoststypes.Outpost{OutpostArn: &opARN, SiteArn: &siteARN})
	opID := upsertTestResource(t, st, "aws", acct.ID, TypeOutpostsOutpost, opARN, testRegion, opAttrs)
	if err := resolveOutpostSite(acct, st); err != nil {
		t.Fatalf("resolveOutpostSite: %v", err)
	}
	rels, _ := st.RelationshipsFrom(opID)
	assertRelationship(t, rels, opID, siteID, store.RelAttachedTo)
}

func TestResolveOutpostSite_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	opARN := fmt.Sprintf("arn:aws:outposts:%s:%s:outpost/op-1", testRegion, acct.ID)
	opID := upsertTestResource(t, st, "aws", acct.ID, TypeOutpostsOutpost, opARN, testRegion, "{}")
	if err := resolveOutpostSite(acct, st); err != nil {
		t.Fatalf("resolveOutpostSite: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(opID); len(rels) != 0 {
		t.Errorf("expected no relationships, got %d", len(rels))
	}
}
