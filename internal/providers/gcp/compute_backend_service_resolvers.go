package gcp

import (
	"encoding/json"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// Resolver Wave R7 of the resolver-implementation backlog (ROADMAP.md
// "Resolver buildout"): the two fields deferred out of Wave R6 as genuinely
// ambiguous — BackendService.HealthChecks (mixes modern HealthCheck with
// legacy HttpHealthCheck/HttpsHealthCheck) and Backend.Group (spans instance
// groups and every NEG scope) — resolved via upsertIfScannedAny's
// first-matching-candidate approach, plus the unambiguous
// (Region)Autoscaler.Target managed-instance-group edge.
func init() {
	registerResolver(resolveBackendServiceRelationships,
		EdgeDecl{TypeComputeBackendService, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionBackendService, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeComputeBackendService, TypeComputeHealthCheck, store.RelUses},
		EdgeDecl{TypeComputeBackendService, TypeComputeHttpHealthCheck, store.RelUses},
		EdgeDecl{TypeComputeBackendService, TypeComputeHttpsHealthCheck, store.RelUses},
		EdgeDecl{TypeComputeRegionBackendService, TypeComputeRegionHealthCheck, store.RelUses},
		EdgeDecl{TypeComputeBackendService, TypeComputeInstanceGroup, store.RelUses},
		EdgeDecl{TypeComputeBackendService, TypeComputeRegionInstanceGroup, store.RelUses},
		EdgeDecl{TypeComputeBackendService, TypeComputeNetworkEndpointGroup, store.RelUses},
		EdgeDecl{TypeComputeBackendService, TypeComputeRegionNetworkEndpointGroup, store.RelUses},
		EdgeDecl{TypeComputeBackendService, TypeComputeGlobalNetworkEndpointGroup, store.RelUses},
		EdgeDecl{TypeComputeRegionBackendService, TypeComputeInstanceGroup, store.RelUses},
		EdgeDecl{TypeComputeRegionBackendService, TypeComputeRegionInstanceGroup, store.RelUses},
		EdgeDecl{TypeComputeRegionBackendService, TypeComputeNetworkEndpointGroup, store.RelUses},
		EdgeDecl{TypeComputeRegionBackendService, TypeComputeRegionNetworkEndpointGroup, store.RelUses},
		EdgeDecl{TypeComputeRegionBackendService, TypeComputeGlobalNetworkEndpointGroup, store.RelUses},
	)
	registerResolver(resolveAutoscalerRelationships,
		EdgeDecl{TypeComputeAutoscaler, TypeComputeInstanceGroupManager, store.RelAttachedTo},
		EdgeDecl{TypeComputeRegionAutoscaler, TypeComputeRegionInstanceGroupManager, store.RelAttachedTo},
	)
}

// backendGroupCandidateTypes lists every disco type a Backend.group self-link
// may name — GCE instance groups (zonal/regional) or a NEG (zonal/regional/
// global). Same candidate list applies to global and regional backend
// services: both LB families support all of these backend kinds depending on
// load-balancing scheme.
var backendGroupCandidateTypes = []string{
	TypeComputeInstanceGroup, TypeComputeRegionInstanceGroup,
	TypeComputeNetworkEndpointGroup, TypeComputeRegionNetworkEndpointGroup, TypeComputeGlobalNetworkEndpointGroup,
}

// resolveBackendServiceRelationships wires BackendService/RegionBackendService
// -> Network (own `network` field), -> each backend's group/NEG
// (`backends[].group`, ambiguous target type, resolved via
// upsertIfScannedAny), and -> each attached health check (`healthChecks[]`).
// HealthChecks candidates are scoped per source type: a global BackendService
// may reference modern HealthCheck or legacy Http(s)HealthCheck; a regional
// RegionBackendService may only reference a RegionHealthCheck in the same
// region (GCP requires health checks referenced by regional resources to be
// regional and co-located — same rule documented on HealthCheckService).
// BackendService -> SecurityPolicy/EdgeSecurityPolicy is out of scope here —
// already covered by cloudarmor_resolvers.go (extended to RegionBackendService
// in this wave).
func resolveBackendServiceRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID,
		Types: []string{TypeComputeBackendService, TypeComputeRegionBackendService},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st,
		TypeComputeNetwork, TypeComputeHealthCheck, TypeComputeHttpHealthCheck, TypeComputeHttpsHealthCheck,
		TypeComputeRegionHealthCheck, TypeComputeInstanceGroup, TypeComputeRegionInstanceGroup,
		TypeComputeNetworkEndpointGroup, TypeComputeRegionNetworkEndpointGroup, TypeComputeGlobalNetworkEndpointGroup,
	)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Network      string   `json:"network"`
			HealthChecks []string `json:"healthChecks"`
			Backends     []struct {
				Group string `json:"group"`
			} `json:"backends"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeComputeNetwork, attrs.Network, store.RelAttachedTo); err != nil {
			return err
		}
		healthCheckCandidates := []string{TypeComputeHealthCheck, TypeComputeHttpHealthCheck, TypeComputeHttpsHealthCheck}
		if r.Type == TypeComputeRegionBackendService {
			healthCheckCandidates = []string{TypeComputeRegionHealthCheck}
		}
		for _, hc := range attrs.HealthChecks {
			if err := upsertIfScannedAny(st, scanned, r.ID, "gcp", p.ID, healthCheckCandidates, hc, store.RelUses); err != nil {
				return err
			}
		}
		for _, b := range attrs.Backends {
			if err := upsertIfScannedAny(st, scanned, r.ID, "gcp", p.ID, backendGroupCandidateTypes, b.Group, store.RelUses); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveAutoscalerRelationships wires Autoscaler -> InstanceGroupManager and
// RegionAutoscaler -> RegionInstanceGroupManager via `target`, unambiguous
// because the autoscaler's own scope (zonal vs regional) determines which
// managed-instance-group type its target must be.
func resolveAutoscalerRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID,
		Types: []string{TypeComputeAutoscaler, TypeComputeRegionAutoscaler},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeComputeInstanceGroupManager, TypeComputeRegionInstanceGroupManager)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Target string `json:"target"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		targetType := TypeComputeInstanceGroupManager
		if r.Type == TypeComputeRegionAutoscaler {
			targetType = TypeComputeRegionInstanceGroupManager
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, targetType, attrs.Target, store.RelAttachedTo); err != nil {
			return err
		}
	}
	return nil
}
