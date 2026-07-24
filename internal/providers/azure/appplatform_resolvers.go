package azure

import (
	"encoding/json"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(resolveAppPlatformRelationships,
		EdgeDecl{Source: TypeAppPlatformService, Target: TypeNetworkVirtualNetwork, Kind: store.RelAttachedTo},
	)
}

// resolveAppPlatformRelationships derives Azure Spring Apps service
// -[attached-to]-> VNet edges via the VNet-injection subnet references
// (networkProfile.serviceRuntimeSubnetId + appSubnetId).
func resolveAppPlatformRelationships(sub *subscription, st *store.Store) error {
	services, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeAppPlatformService},
		Limit: util.AllResources,
	})
	if err != nil || len(services) == 0 {
		return err
	}
	vnetByID, err := nativeIDIndex(sub, st, TypeNetworkVirtualNetwork)
	if err != nil {
		return err
	}
	for _, s := range services {
		var attrs struct {
			Properties *struct {
				NetworkProfile *struct {
					ServiceRuntimeSubnetID *string `json:"serviceRuntimeSubnetId"`
					AppSubnetID            *string `json:"appSubnetId"`
				} `json:"networkProfile"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(s.AttributesJSON), &attrs); err != nil || attrs.Properties == nil || attrs.Properties.NetworkProfile == nil {
			continue
		}
		seen := map[string]bool{}
		for _, subnetID := range []*string{attrs.Properties.NetworkProfile.ServiceRuntimeSubnetID, attrs.Properties.NetworkProfile.AppSubnetID} {
			if subnetID == nil {
				continue
			}
			vnetID := vnetIDFromSubnetID(*subnetID)
			if vnetID == "" || seen[strings.ToLower(vnetID)] {
				continue
			}
			seen[strings.ToLower(vnetID)] = true
			if err := upsertVNetAttachment(st, s.ID, *subnetID, vnetByID); err != nil {
				return err
			}
		}
	}
	return nil
}
