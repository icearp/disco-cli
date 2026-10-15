package aws

import "github.com/icearp/disco-cli/store"

func init() {
	registerResolver(
		noopWorkSpacesHierarchy,
		EdgeDecl{TypeWorkSpacesDirectory, TypeWorkSpacesConnectClientAddIn, store.RelContains},
	)
}

// noopWorkSpacesHierarchy emits nothing: the directory → Connect client
// add-in contains edges are written at scan time by childFanOut (the add-in
// phase of scanWorkSpaces). It is registered only so those edges are declared
// to coverage tooling.
func noopWorkSpacesHierarchy(_ *account, _ *store.Store) error {
	return nil
}
