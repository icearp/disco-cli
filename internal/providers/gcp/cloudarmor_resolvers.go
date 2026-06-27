package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolveCloudArmorRelationships) }

// resolveCloudArmorRelationships derives backend-service -[uses]-> security-policy
// edges. BackendServices reference their attached Cloud Armor policy via two
// fields:
//
//   - `securityPolicy` (top-level WAF policy applied at the LB edge)
//   - `edgeSecurityPolicy` (separate edge-tier policy for Cloud CDN integration)
//
// Both are full SelfLink URLs and resolve against the in-store policy
// catalog. Backend-bucket → securityPolicy edges deferred — backendBucket
// also has an `edgeSecurityPolicy` field but the security-policy story for
// CDN-fronted buckets is narrow vs. the LB-fronted services.
func resolveCloudArmorRelationships(p *project, st *store.Store) error {
	policies, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeSecurityPolicy},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(policies) == 0 {
		return nil
	}
	policyIDByNative := make(map[string]string, len(policies))
	for _, sp := range policies {
		policyIDByNative[sp.NativeID] = sp.ID
	}

	bss, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeBackendService},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, bs := range bss {
		var a struct {
			SecurityPolicy     string `json:"securityPolicy"`
			EdgeSecurityPolicy string `json:"edgeSecurityPolicy"`
		}
		if err := json.Unmarshal([]byte(bs.AttributesJSON), &a); err != nil {
			continue
		}
		seen := make(map[string]bool, 2)
		for _, ref := range []string{a.SecurityPolicy, a.EdgeSecurityPolicy} {
			if ref == "" || seen[ref] {
				continue
			}
			seen[ref] = true
			policyID, ok := policyIDByNative[ref]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(bs.ID, policyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert backendService→securityPolicy: %w", err)
			}
		}
	}
	return nil
}
