package azure

import (
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveCloudServiceRoleRelationships)
	registerResolver(resolveCloudServiceRoleInstanceRelationships)
}

// resolveCloudServiceRoleRelationships derives the parent cloud service for each
// cloud service role by truncating the role's NativeID at "/roles/".
// NativeID form: .../cloudServices/{service}/roles/{role}
func resolveCloudServiceRoleRelationships(sub *subscription, st *store.Store) error {
	roles, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeComputeCloudServiceRole},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range roles {
		csNativeID := truncateAtSegment(r.NativeID, "/roles/")
		if csNativeID == "" {
			continue
		}
		csID := store.ResourceID("azure", sub.ID, TypeComputeCloudService, csNativeID)
		if err := st.UpsertRelationship(r.ID, csID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert cloudServiceRole→cloudService relationship: %w", err)
		}
	}
	return nil
}

// resolveCloudServiceRoleInstanceRelationships derives the parent cloud service for
// each role instance by truncating the role instance's NativeID at "/roleInstances/".
// NativeID form: .../cloudServices/{service}/roleInstances/{instance}
func resolveCloudServiceRoleInstanceRelationships(sub *subscription, st *store.Store) error {
	instances, err := st.ListResources(store.ResourceFilter{
		Provider:  "azure",
		AccountID: sub.ID,
		Types:     []string{TypeComputeCloudServiceRoleInstance},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range instances {
		csNativeID := truncateAtSegment(r.NativeID, "/roleInstances/")
		if csNativeID == "" {
			continue
		}
		csID := store.ResourceID("azure", sub.ID, TypeComputeCloudService, csNativeID)
		if err := st.UpsertRelationship(r.ID, csID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert cloudServiceRoleInstance→cloudService relationship: %w", err)
		}
	}
	return nil
}
