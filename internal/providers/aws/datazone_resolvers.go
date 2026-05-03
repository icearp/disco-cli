package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveDataZoneChildrenToDomain,
		EdgeDecl{TypeDataZoneDomainUnit, TypeDataZoneDomain, store.RelAttachedTo},
		EdgeDecl{TypeDataZoneProject, TypeDataZoneDomain, store.RelAttachedTo},
		EdgeDecl{TypeDataZoneProjectProfile, TypeDataZoneDomain, store.RelAttachedTo},
		EdgeDecl{TypeDataZoneProjectMembership, TypeDataZoneDomain, store.RelAttachedTo},
		EdgeDecl{TypeDataZoneEnvironment, TypeDataZoneDomain, store.RelAttachedTo},
		EdgeDecl{TypeDataZoneEnvironmentProfile, TypeDataZoneDomain, store.RelAttachedTo},
		EdgeDecl{TypeDataZoneEnvironmentActions, TypeDataZoneDomain, store.RelAttachedTo},
		EdgeDecl{TypeDataZoneEnvironmentBlueprintConfiguration, TypeDataZoneDomain, store.RelAttachedTo},
		EdgeDecl{TypeDataZoneSubscriptionTarget, TypeDataZoneDomain, store.RelAttachedTo},
		EdgeDecl{TypeDataZoneDataSource, TypeDataZoneDomain, store.RelAttachedTo},
		EdgeDecl{TypeDataZoneConnection, TypeDataZoneDomain, store.RelAttachedTo},
	)
	registerResolver(resolveDataZoneEnvActionsToEnvironment,
		EdgeDecl{TypeDataZoneEnvironmentActions, TypeDataZoneEnvironment, store.RelAttachedTo},
		EdgeDecl{TypeDataZoneSubscriptionTarget, TypeDataZoneEnvironment, store.RelAttachedTo},
	)
}

// datazoneDomainARNFromChild extracts `arn:aws:datazone:r:a:domain/{id}`
// from any child NativeID of shape `…:domain/{id}/<kind>/<rest>`.
func datazoneDomainARNFromChild(arn string) string {
	const prefix = "domain/"
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

// resolveDataZoneChildrenToDomain wires every per-domain child kind to its
// parent domain via `domain/{id}` segment lookup.
func resolveDataZoneChildrenToDomain(acct *account, st *store.Store) error {
	domSet, err := scannedIDSet(acct, st, TypeDataZoneDomain)
	if err != nil {
		return err
	}
	childTypes := []string{
		TypeDataZoneDomainUnit,
		TypeDataZoneProject,
		TypeDataZoneProjectProfile,
		TypeDataZoneProjectMembership,
		TypeDataZoneEnvironment,
		TypeDataZoneEnvironmentProfile,
		TypeDataZoneEnvironmentActions,
		TypeDataZoneEnvironmentBlueprintConfiguration,
		TypeDataZoneSubscriptionTarget,
		TypeDataZoneDataSource,
		TypeDataZoneConnection,
	}
	for _, ctype := range childTypes {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{ctype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			parent := datazoneDomainARNFromChild(r.NativeID)
			if parent == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, TypeDataZoneDomain, parent)
			if !domSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert datazone %s→domain: %w", ctype, err)
			}
		}
	}
	return nil
}

// resolveDataZoneEnvActionsToEnvironment wires environment-action and
// subscription-target rows to their parent environment by parsing the
// `<kind>/{envID}/{id}` tail on the synthesized NativeID. Scanner builds
// `…:domain/{d}/environment-action/{envID}/{actionID}` and
// `…:domain/{d}/subscription-target/{envID}/{targetID}`.
func resolveDataZoneEnvActionsToEnvironment(acct *account, st *store.Store) error {
	envSet, err := scannedIDSet(acct, st, TypeDataZoneEnvironment)
	if err != nil {
		return err
	}
	cases := []struct {
		ctype string
		seg   string
	}{
		{TypeDataZoneEnvironmentActions, "/environment-action/"},
		{TypeDataZoneSubscriptionTarget, "/subscription-target/"},
	}
	for _, c := range cases {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{c.ctype}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			i := strings.Index(r.NativeID, c.seg)
			if i < 0 {
				continue
			}
			tail := r.NativeID[i+len(c.seg):]
			end := strings.IndexByte(tail, '/')
			if end < 0 {
				continue
			}
			envID := tail[:end]
			domARN := datazoneDomainARNFromChild(r.NativeID)
			if domARN == "" {
				continue
			}
			envARN := domARN + "/environment/" + envID
			tgtID := store.ResourceID("aws", acct.ID, TypeDataZoneEnvironment, envARN)
			if !envSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert datazone %s→env: %w", c.ctype, err)
			}
		}
	}
	return nil
}
