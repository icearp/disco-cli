package aws

import "github.com/icearp/disco-cli/store"

func init() {
	registerResolver(
		noopMPAHierarchy,
		EdgeDecl{TypeMPAApprovalTeam, TypeMPAResourcePolicy, store.RelContains},
	)
}

// noopMPAHierarchy only declares the approval team→resource policy contains
// edge, which scanMPAResourcePolicies records at scan time via childFanOut.
func noopMPAHierarchy(_ *account, _ *store.Store) error {
	return nil
}
