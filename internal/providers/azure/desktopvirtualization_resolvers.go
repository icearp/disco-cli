package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveDesktopVirtualizationRelationships,
		EdgeDecl{Source: TypeDVCApplicationGroup, Target: TypeDVCHostPool, Kind: store.RelAttachedTo},
		EdgeDecl{Source: TypeDVCWorkspace, Target: TypeDVCApplicationGroup, Kind: store.RelAttachedTo},
		EdgeDecl{Source: TypeDVCScalingPlan, Target: TypeDVCHostPool, Kind: store.RelAttachedTo},
	)
}

// resolveDesktopVirtualizationRelationships wires the Azure Virtual Desktop
// object graph (all ARM-ID references, matched case-insensitively):
//   - application-group -[attached-to]-> host-pool   (properties.hostPoolArmPath)
//   - workspace         -[attached-to]-> application-group (properties.applicationGroupReferences[])
//   - scaling-plan      -[attached-to]-> host-pool   (properties.hostPoolReferences[].hostPoolArmPath)
func resolveDesktopVirtualizationRelationships(sub *subscription, st *store.Store) error {
	hostPoolByID, err := nativeIDIndex(sub, st, TypeDVCHostPool)
	if err != nil {
		return err
	}
	appGroupByID, err := nativeIDIndex(sub, st, TypeDVCApplicationGroup)
	if err != nil {
		return err
	}

	if err := resolveDVCAppGroupToHostPool(sub, st, hostPoolByID); err != nil {
		return err
	}
	if err := resolveDVCWorkspaceToAppGroup(sub, st, appGroupByID); err != nil {
		return err
	}
	return resolveDVCScalingPlanToHostPool(sub, st, hostPoolByID)
}

func resolveDVCAppGroupToHostPool(sub *subscription, st *store.Store, hostPoolByID map[string]string) error {
	groups, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeDVCApplicationGroup}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, g := range groups {
		var attrs struct {
			Properties *struct {
				HostPoolArmPath *string `json:"hostPoolArmPath"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(g.AttributesJSON), &attrs); err != nil || attrs.Properties == nil || attrs.Properties.HostPoolArmPath == nil {
			continue
		}
		if toID, ok := hostPoolByID[strings.ToLower(*attrs.Properties.HostPoolArmPath)]; ok {
			if err := st.UpsertRelationship(g.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert avd-appgroup→hostpool: %w", err)
			}
		}
	}
	return nil
}

func resolveDVCWorkspaceToAppGroup(sub *subscription, st *store.Store, appGroupByID map[string]string) error {
	workspaces, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeDVCWorkspace}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, w := range workspaces {
		var attrs struct {
			Properties *struct {
				ApplicationGroupReferences []*string `json:"applicationGroupReferences"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(w.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
			continue
		}
		for _, ref := range attrs.Properties.ApplicationGroupReferences {
			if ref == nil {
				continue
			}
			if toID, ok := appGroupByID[strings.ToLower(*ref)]; ok {
				if err := st.UpsertRelationship(w.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert avd-workspace→appgroup: %w", err)
				}
			}
		}
	}
	return nil
}

func resolveDVCScalingPlanToHostPool(sub *subscription, st *store.Store, hostPoolByID map[string]string) error {
	plans, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeDVCScalingPlan}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, p := range plans {
		var attrs struct {
			Properties *struct {
				HostPoolReferences []struct {
					HostPoolArmPath *string `json:"hostPoolArmPath"`
				} `json:"hostPoolReferences"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(p.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
			continue
		}
		seen := map[string]bool{}
		for _, ref := range attrs.Properties.HostPoolReferences {
			if ref.HostPoolArmPath == nil {
				continue
			}
			key := strings.ToLower(*ref.HostPoolArmPath)
			if seen[key] {
				continue
			}
			seen[key] = true
			if toID, ok := hostPoolByID[key]; ok {
				if err := st.UpsertRelationship(p.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert avd-scalingplan→hostpool: %w", err)
				}
			}
		}
	}
	return nil
}
