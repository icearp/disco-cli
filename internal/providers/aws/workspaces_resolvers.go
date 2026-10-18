package aws

import "github.com/icearp/disco-cli/store"

func init() {
	registerResolver(
		noopWorkSpacesHierarchy,
		EdgeDecl{TypeWorkSpacesDirectory, TypeWorkSpacesConnectClientAddIn, store.RelContains},
		EdgeDecl{TypeWorkSpacesConnectionAlias, TypeWorkSpacesConnectionAliasPermission, store.RelContains},
	)
}

// noopWorkSpacesHierarchy emits nothing: the directory → Connect client
// add-in and connection alias → permission contains edges are written at scan
// time by the add-in and alias-permission phases of scanWorkSpaces. It is
// registered only so those edges are declared to coverage tooling.
func noopWorkSpacesHierarchy(_ *account, _ *store.Store) error {
	return nil
}
