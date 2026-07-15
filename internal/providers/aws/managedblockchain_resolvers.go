package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveManagedBlockchainProposalNetwork,
		EdgeDecl{TypeManagedBlockchainProposal, TypeManagedBlockchainNetwork, store.RelAttachedTo},
	)
}

// resolveManagedBlockchainProposalNetwork emits an `attached-to` edge from
// each governance proposal to its parent network. The proposal NativeID is
// {networkARN}/proposal/{proposalId}; the network ARN is recovered by
// trimming the "/proposal/..." suffix. FK-safe via the scanned-network id
// set — proposals on a network the scanner skipped (no ARN) carry no edge.
func resolveManagedBlockchainProposalNetwork(acct *account, st *store.Store) error {
	proposals, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeManagedBlockchainProposal},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(proposals) == 0 {
		return nil
	}
	networks, err := scannedIDSet(acct, st, TypeManagedBlockchainNetwork)
	if err != nil {
		return err
	}
	for _, p := range proposals {
		i := strings.Index(p.NativeID, "/proposal/")
		if i <= 0 {
			continue
		}
		netARN := p.NativeID[:i]
		netID := store.ResourceID("aws", acct.ID, netARN)
		if !networks[netID] {
			continue
		}
		if err := st.UpsertRelationship(p.ID, netID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert managedblockchain proposal→network: %w", err)
		}
	}
	return nil
}
