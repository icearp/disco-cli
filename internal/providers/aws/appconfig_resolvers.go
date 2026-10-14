package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
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
		EdgeDecl{TypeAppConfigExperimentDefinition, TypeAppConfigApplication, store.RelAttachedTo},
	)
	registerResolver(
		resolveAppConfigExperimentDefinitions,
		EdgeDecl{TypeAppConfigExperimentDefinition, TypeAppConfigEnvironment, store.RelUses},
		EdgeDecl{TypeAppConfigExperimentDefinition, TypeAppConfigConfigurationProfile, store.RelUses},
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
		TypeAppConfigExperimentDefinition,
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

// resolveAppConfigExperimentDefinitions links each experiment definition to
// the environment it runs in and the configuration profile holding its flag.
// Both are named by id within the definition's own application, so their
// NativeIDs are rebuilt under the application ARN the definition's NativeID
// starts with.
func resolveAppConfigExperimentDefinitions(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppConfigExperimentDefinition},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	envSet, err := scannedIDSet(acct, st, TypeAppConfigEnvironment)
	if err != nil {
		return err
	}
	cpSet, err := scannedIDSet(acct, st, TypeAppConfigConfigurationProfile)
	if err != nil {
		return err
	}
	for _, r := range rows {
		appARN := appconfigAppARNFromChild(r.NativeID)
		if appARN == "" {
			continue
		}
		var attrs struct {
			EnvironmentID          *string `json:"EnvironmentId"`
			ConfigurationProfileID *string `json:"ConfigurationProfileId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			return fmt.Errorf("decode appconfig experiment-definition %s attributes: %w", r.NativeID, err)
		}
		for _, ref := range []struct {
			kind, nativeID string
			set            map[string]bool
		}{
			{"environment", acEnvironmentNativeID(appARN, sv(attrs.EnvironmentID)), envSet},
			{"configurationprofile", acProfileNativeID(appARN, sv(attrs.ConfigurationProfileID)), cpSet},
		} {
			tgtID := store.ResourceID("aws", acct.ID, ref.nativeID)
			if !ref.set[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert appconfig experiment-definition→%s: %w", ref.kind, err)
			}
		}
	}
	return nil
}
