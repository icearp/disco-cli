package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(resolveSiteToServerFarm,
		EdgeDecl{Source: TypeAppServiceSite, Target: TypeAppServiceServerFarm, Kind: store.RelUses},
	)
	registerResolver(resolveSiteToHostingEnv,
		EdgeDecl{Source: TypeAppServiceSite, Target: TypeAppServiceEnvironment, Kind: store.RelAttachedTo},
	)
	registerResolver(resolveSlotToSite,
		EdgeDecl{Source: TypeAppServiceSiteSlot, Target: TypeAppServiceSite, Kind: store.RelAttachedTo},
	)
	registerResolver(resolveSlotToServerFarm,
		EdgeDecl{Source: TypeAppServiceSiteSlot, Target: TypeAppServiceServerFarm, Kind: store.RelUses},
	)
	registerResolver(resolveServerFarmToHostingEnv,
		EdgeDecl{Source: TypeAppServiceServerFarm, Target: TypeAppServiceEnvironment, Kind: store.RelAttachedTo},
	)
}

// resolveSiteToServerFarm adds a uses edge from each web app to its App Service
// Plan, derived from properties.serverFarmId in the site's stored attributes JSON.
func resolveSiteToServerFarm(sub *subscription, st *store.Store) error {
	sites, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeAppServiceSite},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}

	var attrs struct {
		Properties *struct {
			ServerFarmID *string `json:"serverFarmId"`
		} `json:"properties"`
	}

	for _, r := range sites {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.ServerFarmID == nil {
			continue
		}
		planID := store.ResourceID("azure", sub.ID, *attrs.Properties.ServerFarmID)
		if err := st.UpsertRelationship(r.ID, planID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert site→serverFarm relationship: %w", err)
		}
	}
	return nil
}

// resolveSiteToHostingEnv adds an attached-to edge from each web app to its
// App Service Environment, derived from properties.hostingEnvironmentProfile.id.
func resolveSiteToHostingEnv(sub *subscription, st *store.Store) error {
	sites, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeAppServiceSite},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}

	var attrs struct {
		Properties *struct {
			HostingEnvironmentProfile *struct {
				ID *string `json:"id"`
			} `json:"hostingEnvironmentProfile"`
		} `json:"properties"`
	}

	for _, r := range sites {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.HostingEnvironmentProfile == nil || attrs.Properties.HostingEnvironmentProfile.ID == nil {
			continue
		}
		aseID := store.ResourceID("azure", sub.ID, *attrs.Properties.HostingEnvironmentProfile.ID)
		if err := st.UpsertRelationship(r.ID, aseID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert site→hostingEnvironment relationship: %w", err)
		}
	}
	return nil
}

// resolveSlotToSite adds an attached-to edge from each deployment slot to its
// parent web app, derived by stripping the /slots/{name} suffix from the slot NativeID.
func resolveSlotToSite(sub *subscription, st *store.Store) error {
	slots, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeAppServiceSiteSlot},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}

	for _, r := range slots {
		siteNativeID := siteIDFromSlotID(r.NativeID)
		if siteNativeID == "" {
			continue
		}
		siteID := store.ResourceID("azure", sub.ID, siteNativeID)
		if err := st.UpsertRelationship(r.ID, siteID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert slot→site relationship: %w", err)
		}
	}
	return nil
}

// resolveSlotToServerFarm adds a uses edge from each deployment slot to its
// App Service Plan, derived from properties.serverFarmId in the slot's attributes JSON.
func resolveSlotToServerFarm(sub *subscription, st *store.Store) error {
	slots, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeAppServiceSiteSlot},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}

	var attrs struct {
		Properties *struct {
			ServerFarmID *string `json:"serverFarmId"`
		} `json:"properties"`
	}

	for _, r := range slots {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.ServerFarmID == nil {
			continue
		}
		planID := store.ResourceID("azure", sub.ID, *attrs.Properties.ServerFarmID)
		if err := st.UpsertRelationship(r.ID, planID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert slot→serverFarm relationship: %w", err)
		}
	}
	return nil
}

// resolveServerFarmToHostingEnv adds an attached-to edge from each App Service
// Plan to its App Service Environment, derived from
// properties.hostingEnvironmentProfile.id in the plan's attributes JSON.
func resolveServerFarmToHostingEnv(sub *subscription, st *store.Store) error {
	plans, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeAppServiceServerFarm},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}

	var attrs struct {
		Properties *struct {
			HostingEnvironmentProfile *struct {
				ID *string `json:"id"`
			} `json:"hostingEnvironmentProfile"`
		} `json:"properties"`
	}

	for _, r := range plans {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.HostingEnvironmentProfile == nil || attrs.Properties.HostingEnvironmentProfile.ID == nil {
			continue
		}
		aseID := store.ResourceID("azure", sub.ID, *attrs.Properties.HostingEnvironmentProfile.ID)
		if err := st.UpsertRelationship(r.ID, aseID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert serverFarm→hostingEnvironment relationship: %w", err)
		}
	}
	return nil
}

// siteIDFromSlotID extracts the parent web app NativeID from a slot NativeID
// by stripping the /slots/{slotName} suffix.
// e.g. .../sites/myApp/slots/staging → .../sites/myApp
func siteIDFromSlotID(slotID string) string {
	lower := strings.ToLower(slotID)
	idx := strings.Index(lower, "/slots/")
	if idx < 0 {
		return ""
	}
	return slotID[:idx]
}
