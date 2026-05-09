package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveGreengrassVersionsToParent,
		EdgeDecl{TypeGreengrassConnectorDefinitionVersion, TypeGreengrassConnectorDefinition, store.RelAttachedTo},
		EdgeDecl{TypeGreengrassCoreDefinitionVersion, TypeGreengrassCoreDefinition, store.RelAttachedTo},
		EdgeDecl{TypeGreengrassDeviceDefinitionVersion, TypeGreengrassDeviceDefinition, store.RelAttachedTo},
		EdgeDecl{TypeGreengrassFunctionDefinitionVersion, TypeGreengrassFunctionDefinition, store.RelAttachedTo},
		EdgeDecl{TypeGreengrassLoggerDefinitionVersion, TypeGreengrassLoggerDefinition, store.RelAttachedTo},
		EdgeDecl{TypeGreengrassResourceDefinitionVersion, TypeGreengrassResourceDefinition, store.RelAttachedTo},
		EdgeDecl{TypeGreengrassSubscriptionDefinitionVersion, TypeGreengrassSubscriptionDefinition, store.RelAttachedTo},
		EdgeDecl{TypeGreengrassGroupVersion, TypeGreengrassGroup, store.RelAttachedTo},
	)
}

// greengrassVersionParent strips the trailing `/versions/{id}` from a v1
// Greengrass version ARN to recover its parent definition or group ARN.
func greengrassVersionParent(arn string) string {
	const seg = "/versions/"
	i := strings.LastIndex(arn, seg)
	if i < 0 {
		return ""
	}
	return arn[:i]
}

// resolveGreengrassVersionsToParent wires each Greengrass v1 *Version row
// to its parent definition (or group) via NativeID `/versions/{id}` strip.
func resolveGreengrassVersionsToParent(acct *account, st *store.Store) error {
	pairs := []struct {
		child, parent string
	}{
		{TypeGreengrassConnectorDefinitionVersion, TypeGreengrassConnectorDefinition},
		{TypeGreengrassCoreDefinitionVersion, TypeGreengrassCoreDefinition},
		{TypeGreengrassDeviceDefinitionVersion, TypeGreengrassDeviceDefinition},
		{TypeGreengrassFunctionDefinitionVersion, TypeGreengrassFunctionDefinition},
		{TypeGreengrassLoggerDefinitionVersion, TypeGreengrassLoggerDefinition},
		{TypeGreengrassResourceDefinitionVersion, TypeGreengrassResourceDefinition},
		{TypeGreengrassSubscriptionDefinitionVersion, TypeGreengrassSubscriptionDefinition},
		{TypeGreengrassGroupVersion, TypeGreengrassGroup},
	}
	for _, p := range pairs {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{p.child}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			continue
		}
		parentSet, err := scannedIDSet(acct, st, p.parent)
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := greengrassVersionParent(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, p.parent, parent)
			if !parentSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert greengrass %s→%s: %w", p.child, p.parent, err)
			}
		}
	}
	return nil
}
