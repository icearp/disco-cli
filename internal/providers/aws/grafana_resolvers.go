package aws

import "github.com/icearp/disco-cli/store"

func init() {
	registerResolver(
		noopGrafanaHierarchy,
		EdgeDecl{TypeGrafanaWorkspace, TypeGrafanaWorkspaceServiceAccount, store.RelContains},
		EdgeDecl{TypeGrafanaWorkspaceServiceAccount, TypeGrafanaWorkspaceServiceAccountToken, store.RelContains},
	)
}

// noopGrafanaHierarchy only declares the workspace→service-account→token
// contains edges, which scanGrafana records at scan time via childFanOut.
func noopGrafanaHierarchy(_ *account, _ *store.Store) error {
	return nil
}
