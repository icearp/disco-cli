package aws

import "github.com/icearp/disco-cli/store"

func init() {
	registerResolver(
		noopRAMHierarchy,
		EdgeDecl{TypeRAMResourceShare, TypeRAMResourceShareAssociation, store.RelContains},
	)
}

// noopRAMHierarchy only declares the share→association contains edge, which
// scanRAMResourceShareAssociations records through the hierarchy closure, so
// coverage tooling sees it as wired.
func noopRAMHierarchy(_ *account, _ *store.Store) error {
	return nil
}
