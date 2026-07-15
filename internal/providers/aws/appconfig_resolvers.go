package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveAppConfigChildren,
		EdgeDecl{TypeAppConfigEnvironment, TypeAppConfigApplication, store.RelAttachedTo},
		EdgeDecl{TypeAppConfigConfigurationProfile, TypeAppConfigApplication, store.RelAttachedTo},
		EdgeDecl{TypeAppConfigDeployment, TypeAppConfigApplication, store.RelAttachedTo},
		EdgeDecl{TypeAppConfigDeployment, TypeAppConfigEnvironment, store.RelAttachedTo},
		EdgeDecl{TypeAppConfigHostedConfigurationVersion, TypeAppConfigApplication, store.RelAttachedTo},
		EdgeDecl{TypeAppConfigHostedConfigurationVersion, TypeAppConfigConfigurationProfile, store.RelAttachedTo},
	)
	registerResolver(
		resolveAppConfigExtensionAssociation,
		EdgeDecl{TypeAppConfigExtensionAssociation, TypeAppConfigExtension, store.RelUses},
	)
}

// appconfigAppARNFromChild trims a child NativeID to its parent
// `arn:aws:appconfig:r:a:application/{id}`. Works for direct children
// (environment, configuration-profile) and grandchildren (deployment,
// hosted-configuration-version): application/{id} is always the first
// path segment after the ARN prefix.
func appconfigAppARNFromChild(arn string) string {
	const prefix = "application/"
	i := strings.Index(arn, prefix)
	if i < 0 {
		return ""
	}
	tail := arn[i+len(prefix):]
	end := strings.IndexByte(tail, '/')
	if end < 0 {
		return ""
	}
	return arn[:i] + prefix + tail[:end]
}

// appconfigParentARN strips the trailing `/<kind>/<id>` from a grandchild ARN
// (deployment under environment, hosted-config-version under
// configuration-profile) to recover the immediate parent ARN.
func appconfigParentARN(arn string) string {
	last := strings.LastIndexByte(arn, '/')
	if last < 0 {
		return ""
	}
	mid := strings.LastIndexByte(arn[:last], '/')
	if mid < 0 {
		return ""
	}
	return arn[:mid]
}

func resolveAppConfigChildren(acct *account, st *store.Store) error {
	appSet, err := scannedIDSet(acct, st, TypeAppConfigApplication)
	if err != nil {
		return err
	}
	envSet, err := scannedIDSet(acct, st, TypeAppConfigEnvironment)
	if err != nil {
		return err
	}
	cpSet, err := scannedIDSet(acct, st, TypeAppConfigConfigurationProfile)
	if err != nil {
		return err
	}
	directChildren := []string{
		TypeAppConfigEnvironment,
		TypeAppConfigConfigurationProfile,
		TypeAppConfigDeployment,
		TypeAppConfigHostedConfigurationVersion,
	}
	for _, ctype := range directChildren {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{ctype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := appconfigAppARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, parent)
			if !appSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert appconfig %s→application: %w", ctype, err)
			}
		}
	}
	if err := resolveAppConfigGrandparent(acct, st, TypeAppConfigDeployment, TypeAppConfigEnvironment, envSet); err != nil {
		return err
	}
	return resolveAppConfigGrandparent(acct, st, TypeAppConfigHostedConfigurationVersion, TypeAppConfigConfigurationProfile, cpSet)
}

func resolveAppConfigGrandparent(acct *account, st *store.Store, childType, parentType string, parentSet map[string]bool) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{childType}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range rows {
		parent := appconfigParentARN(r.NativeID)
		if parent == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, parent)
		if !parentSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert appconfig %s→%s: %w", childType, parentType, err)
		}
	}
	return nil
}

// resolveAppConfigExtensionAssociation links each extension-association to
// its extension via `ExtensionArn` from `ListExtensionAssociations` items.
// `ResourceArn` (the attached-to target) skipped — refs span every AWS
// service, dispatch table not worth the breadth.
func resolveAppConfigExtensionAssociation(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppConfigExtensionAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	extSet, err := scannedIDSet(acct, st, TypeAppConfigExtension)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ExtensionArn *string `json:"ExtensionArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		arn := sv(attrs.ExtensionArn)
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, arn)
		if !extSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert appconfig ext-assoc→extension: %w", err)
		}
	}
	return nil
}
