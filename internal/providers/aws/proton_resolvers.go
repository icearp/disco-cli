package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveProtonServiceInstanceTargets,
		EdgeDecl{TypeProtonServiceInstance, TypeProtonService, store.RelAttachedTo},
	)
	registerResolver(
		resolveProtonEnvTemplateVersionTargets,
		EdgeDecl{TypeProtonEnvironmentTemplateVersion, TypeProtonEnvironmentTemplate, store.RelAttachedTo},
	)
	registerResolver(
		resolveProtonServiceTemplateVersionTargets,
		EdgeDecl{TypeProtonServiceTemplateVersion, TypeProtonServiceTemplate, store.RelAttachedTo},
	)
	registerResolver(
		resolveProtonComponentTargets,
		EdgeDecl{TypeProtonComponent, TypeProtonServiceInstance, store.RelAttachedTo},
	)
	registerResolver(
		resolveProtonEnvironmentTargets,
		EdgeDecl{TypeProtonEnvironment, TypeProtonEnvironmentTemplate, store.RelUses},
	)
}

// protonNameIndex maps (region, Name) → resourceID for scanned rows of rtype.
// Proton names are unique per (account, region); the region key avoids collisions
// across regions. IDs come from scanned rows, so every hit is FK-safe.
func protonNameIndex(acct *account, st *store.Store, rtype string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{rtype},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		if r.Name == nil {
			continue
		}
		idx[sv(r.Region)+"\x00"+*r.Name] = r.ID
	}
	return idx, nil
}

type protonTemplateRefAttrs struct {
	TemplateName *string `json:"TemplateName"`
}

// resolveProtonTemplateVersionTargets is the shared body for both template-
// version → template edges (environment + service flavours share the
// TemplateName-by-(region,name) shape).
func resolveProtonTemplateVersionTargets(acct *account, st *store.Store, versionType, templateType string) error {
	versions, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{versionType},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		return nil
	}
	idx, err := protonNameIndex(acct, st, templateType)
	if err != nil {
		return err
	}
	for _, v := range versions {
		var a protonTemplateRefAttrs
		if err := json.Unmarshal([]byte(v.AttributesJSON), &a); err != nil {
			continue
		}
		name := sv(a.TemplateName)
		if name == "" {
			continue
		}
		tmplID, ok := idx[sv(v.Region)+"\x00"+name]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(v.ID, tmplID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert proton template-version→template: %w", err)
		}
	}
	return nil
}

func resolveProtonEnvTemplateVersionTargets(acct *account, st *store.Store) error {
	return resolveProtonTemplateVersionTargets(acct, st, TypeProtonEnvironmentTemplateVersion, TypeProtonEnvironmentTemplate)
}

func resolveProtonServiceTemplateVersionTargets(acct *account, st *store.Store) error {
	return resolveProtonTemplateVersionTargets(acct, st, TypeProtonServiceTemplateVersion, TypeProtonServiceTemplate)
}

// resolveProtonServiceInstanceTargets emits each service instance → its parent
// service (attached-to) via the instance's ServiceName.
func resolveProtonServiceInstanceTargets(acct *account, st *store.Store) error {
	instances, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeProtonServiceInstance},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		return nil
	}
	idx, err := protonNameIndex(acct, st, TypeProtonService)
	if err != nil {
		return err
	}
	type attrs struct {
		ServiceName *string `json:"ServiceName"`
	}
	for _, si := range instances {
		var a attrs
		if err := json.Unmarshal([]byte(si.AttributesJSON), &a); err != nil {
			continue
		}
		name := sv(a.ServiceName)
		if name == "" {
			continue
		}
		svcID, ok := idx[sv(si.Region)+"\x00"+name]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(si.ID, svcID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert proton service-instance→service: %w", err)
		}
	}
	return nil
}

// resolveProtonComponentTargets emits each component → its service instance
// (attached-to), keyed by (region, ServiceName, InstanceName). Components with
// empty ServiceName/ServiceInstanceName emit no edge.
func resolveProtonComponentTargets(acct *account, st *store.Store) error {
	components, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeProtonComponent},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(components) == 0 {
		return nil
	}
	instances, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeProtonServiceInstance},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	type instAttrs struct {
		ServiceName *string `json:"ServiceName"`
	}
	// Index service instances by (region, serviceName, instanceName).
	idx := make(map[string]string, len(instances))
	for _, si := range instances {
		if si.Name == nil {
			continue
		}
		var a instAttrs
		if err := json.Unmarshal([]byte(si.AttributesJSON), &a); err != nil {
			continue
		}
		svcName := sv(a.ServiceName)
		if svcName == "" {
			continue
		}
		idx[sv(si.Region)+"\x00"+svcName+"\x00"+*si.Name] = si.ID
	}
	type compAttrs struct {
		ServiceName         *string `json:"ServiceName"`
		ServiceInstanceName *string `json:"ServiceInstanceName"`
	}
	for _, c := range components {
		var a compAttrs
		if err := json.Unmarshal([]byte(c.AttributesJSON), &a); err != nil {
			continue
		}
		svcName, instName := sv(a.ServiceName), sv(a.ServiceInstanceName)
		if svcName == "" || instName == "" {
			continue
		}
		instID, ok := idx[sv(c.Region)+"\x00"+svcName+"\x00"+instName]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(c.ID, instID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert proton component→service-instance: %w", err)
		}
	}
	return nil
}

// resolveProtonEnvironmentTargets emits each environment → its environment
// template (uses) via the environment's TemplateName.
func resolveProtonEnvironmentTargets(acct *account, st *store.Store) error {
	environments, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeProtonEnvironment},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(environments) == 0 {
		return nil
	}
	idx, err := protonNameIndex(acct, st, TypeProtonEnvironmentTemplate)
	if err != nil {
		return err
	}
	for _, e := range environments {
		var a protonTemplateRefAttrs
		if err := json.Unmarshal([]byte(e.AttributesJSON), &a); err != nil {
			continue
		}
		name := sv(a.TemplateName)
		if name == "" {
			continue
		}
		tmplID, ok := idx[sv(e.Region)+"\x00"+name]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(e.ID, tmplID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert proton environment→environment-template: %w", err)
		}
	}
	return nil
}
