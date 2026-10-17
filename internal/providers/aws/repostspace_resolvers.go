package aws

import "github.com/icearp/disco-cli/store"

func init() {
	registerResolver(
		noopRepostspaceHierarchy,
		EdgeDecl{TypeRepostspaceSpace, TypeRepostspaceChannel, store.RelContains},
	)
}

// noopRepostspaceHierarchy only declares the space→channel contains edge,
// which scanRepostspaceChannels records at scan time via childFanOut.
func noopRepostspaceHierarchy(_ *account, _ *store.Store) error {
	return nil
}
