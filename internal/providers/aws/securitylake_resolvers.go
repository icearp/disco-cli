package aws

import "github.com/icearp/disco-cli/store"

func init() {
	registerResolver(
		noopSecurityLakeHierarchy,
		EdgeDecl{TypeSecurityLakeDataLake, TypeSecurityLakeDataLakeSource, store.RelContains},
	)
}

// noopSecurityLakeHierarchy only declares the data lake→data lake source
// contains edge, which scanSLDataLakeSources records at scan time.
func noopSecurityLakeHierarchy(_ *account, _ *store.Store) error {
	return nil
}
