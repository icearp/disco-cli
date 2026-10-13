package aws

import "github.com/icearp/disco-cli/store"

func init() {
	registerResolver(
		noopMGNHierarchy,
		EdgeDecl{TypeMGNSourceServer, TypeMGNSourceServerAction, store.RelContains},
		EdgeDecl{TypeMGNLaunchConfigurationTemplate, TypeMGNTemplateAction, store.RelContains},
	)
}

// noopMGNHierarchy emits nothing: the source-server → source-server-action and
// launch-configuration-template → template-action contains edges are written
// at scan time by childFanOut (the mgn:ListSourceServerActions and
// mgn:ListTemplateActions phases of scanMGNWithClient). It is registered only
// so those edges are declared to coverage tooling.
func noopMGNHierarchy(_ *account, _ *store.Store) error {
	return nil
}
