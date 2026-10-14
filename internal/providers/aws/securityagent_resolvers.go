package aws

import "github.com/icearp/disco-cli/store"

func init() {
	registerResolver(
		noopSecurityAgentHierarchy,
		EdgeDecl{TypeSecurityAgentAgentSpace, TypeSecurityAgentPentest, store.RelContains},
		EdgeDecl{TypeSecurityAgentAgentSpace, TypeSecurityAgentCodeReview, store.RelContains},
		EdgeDecl{TypeSecurityAgentAgentSpace, TypeSecurityAgentThreatModel, store.RelContains},
		EdgeDecl{TypeSecurityAgentAgentSpace, TypeSecurityAgentMembership, store.RelContains},
		EdgeDecl{TypeSecurityAgentAgentSpace, TypeSecurityAgentIntegratedResource, store.RelContains},
		EdgeDecl{TypeSecurityAgentSecurityRequirementPack, TypeSecurityAgentSecurityRequirement, store.RelContains},
	)
}

// noopSecurityAgentHierarchy only declares the agent space→child and
// pack→requirement contains edges, which the per-parent phases of scanSecurityAgentAll record at scan time
// via childFanOut.
func noopSecurityAgentHierarchy(_ *account, _ *store.Store) error {
	return nil
}
