package aws

import "github.com/icearp/disco-cli/store"

func init() {
	registerResolver(
		noopServerlessRepoHierarchy,
		EdgeDecl{TypeServerlessRepoApplication, TypeServerlessRepoApplicationVersion, store.RelContains},
		EdgeDecl{TypeServerlessRepoApplication, TypeServerlessRepoApplicationPolicy, store.RelContains},
	)
}

// noopServerlessRepoHierarchy only declares the application -> version and
// application -> policy contains edges, which the scanner records itself via
// childFanOut.
func noopServerlessRepoHierarchy(_ *account, _ *store.Store) error {
	return nil
}
