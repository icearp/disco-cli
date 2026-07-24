package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveManagedBlockchainProposalNetwork(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	netARN := "arn:aws:managedblockchain:" + testRegion + ":" + acct.ID + ":networks/n-ABC"
	propNativeID := netARN + "/proposal/p-XYZ"

	nID := upsertTestResource(t, st, "aws", acct.ID, TypeManagedBlockchainNetwork, netARN, testRegion, `{"Id":"n-ABC"}`)
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeManagedBlockchainProposal, propNativeID, testRegion, `{"ProposalId":"p-XYZ"}`)

	if err := resolveManagedBlockchainProposalNetwork(acct, st); err != nil {
		t.Fatalf("resolveManagedBlockchainProposalNetwork: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, nID, store.RelAttachedTo)
}

// TestResolveManagedBlockchainProposalNetwork_Unscanned verifies a proposal
// whose parent network was not scanned (no ARN row) emits no edge.
func TestResolveManagedBlockchainProposalNetwork_Unscanned(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	netARN := "arn:aws:managedblockchain:" + testRegion + ":" + acct.ID + ":networks/n-GONE"
	propNativeID := netARN + "/proposal/p-1"
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeManagedBlockchainProposal, propNativeID, testRegion, `{"ProposalId":"p-1"}`)

	if err := resolveManagedBlockchainProposalNetwork(acct, st); err != nil {
		t.Fatalf("resolve unscanned: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	if len(rels) != 0 {
		t.Errorf("expected 0 edges for unscanned network, got %d", len(rels))
	}
}
