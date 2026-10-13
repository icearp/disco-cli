package aws

import "github.com/icearp/disco-cli/store"

func init() {
	registerResolver(
		noopIMIHierarchy,
		EdgeDecl{TypeIoTManagedIntegrationsCloudConnector, TypeIoTManagedIntegrationsConnectorDestination, store.RelContains},
		EdgeDecl{TypeIoTManagedIntegrationsManagedThing, TypeIoTManagedIntegrationsManagedThingAccountAssociation, store.RelContains},
	)
}

// noopIMIHierarchy emits nothing: the cloud-connector → connector-destination
// and managed-thing → managed-thing-account-association contains edges are
// written at scan time via RecordHierarchyBatch (scanIMIList for
// scanIMIConnectorDestinations, and scanIMIManagedThingAccountAssociations).
// It is registered only so those edges are declared to coverage tooling.
func noopIMIHierarchy(_ *account, _ *store.Store) error {
	return nil
}
