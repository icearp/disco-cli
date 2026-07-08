package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// Resolver Wave R24 (continued — see compute_networking_resolvers.go header):
// the region health-check-chain family from `compute_lb_ext_scanners.go` —
// RegionCompositeHealthCheck, RegionHealthCheckService, RegionHealthSource.
// RegionHealthAggregationPolicy is its own edge target here, not itself a
// source this wave (no outbound field per `go doc`).
//
// All source fields (HealthDestination + HealthSources[] on
// CompositeHealthCheck, HealthChecks[]/NetworkEndpointGroups[]/
// NotificationEndpoints[] on HealthCheckService, HealthAggregationPolicy on
// HealthSource) are full same-API Compute self-link URLs per `go doc`,
// exact-matched against each target's own NativeID — same convention as
// compute_networking_resolvers.go.
//
// `scanComputeRegionHealthCheckServices` (see that scanner's own header)
// conflates BOTH regional- and global-scope HealthCheckService rows under
// the single disco type TypeComputeRegionHealthCheckService (global rows
// just carry a nil Region). Per `go doc compute.HealthCheckService`, a
// regional row's HealthChecks[] must be regional HealthCheck resources and a
// global row's must be global ones — "mix ... is not supported" — so this
// resolver must try both TypeComputeRegionHealthCheck and the global
// TypeComputeHealthCheck as candidate targets (adversarial review caught an
// earlier version silently dropping every global row's edges by only
// checking the regional type). NetworkEndpointGroups[] is a same-shape
// oneof across the NEG family (zonal for regional HCS rows, global
// INTERNET_IP_PORT for global HCS rows), tried across all three NEG types.
func init() {
	registerResolver(resolveRegionCompositeHealthCheckRelationships,
		EdgeDecl{TypeComputeRegionCompositeHealthCheck, TypeComputeForwardingRule, store.RelUses},
		EdgeDecl{TypeComputeRegionCompositeHealthCheck, TypeComputeRegionHealthSource, store.RelUses},
	)
	registerResolver(resolveRegionHealthCheckServiceRelationships,
		EdgeDecl{TypeComputeRegionHealthCheckService, TypeComputeRegionHealthCheck, store.RelUses},
		EdgeDecl{TypeComputeRegionHealthCheckService, TypeComputeHealthCheck, store.RelUses},
		EdgeDecl{TypeComputeRegionHealthCheckService, TypeComputeNetworkEndpointGroup, store.RelUses},
		EdgeDecl{TypeComputeRegionHealthCheckService, TypeComputeRegionNetworkEndpointGroup, store.RelUses},
		EdgeDecl{TypeComputeRegionHealthCheckService, TypeComputeGlobalNetworkEndpointGroup, store.RelUses},
		EdgeDecl{TypeComputeRegionHealthCheckService, TypeComputeRegionNotificationEndpoint, store.RelUses},
	)
	registerResolver(resolveRegionHealthSourceRelationships,
		EdgeDecl{TypeComputeRegionHealthSource, TypeComputeRegionHealthAggregationPolicy, store.RelUses},
	)
}

func resolveRegionCompositeHealthCheckRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeRegionCompositeHealthCheck},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scannedFwdRules, err := scannedIDSet(p, st, TypeComputeForwardingRule)
	if err != nil {
		return err
	}
	scannedHealthSources, err := scannedIDSet(p, st, TypeComputeRegionHealthSource)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			HealthDestination string   `json:"healthDestination"`
			HealthSources     []string `json:"healthSources"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scannedFwdRules, r.ID, "gcp", p.ID, TypeComputeForwardingRule, attrs.HealthDestination, store.RelUses); err != nil {
			return fmt.Errorf("upsert compositeHealthCheck→forwardingRule: %w", err)
		}
		for _, hs := range attrs.HealthSources {
			if err := upsertIfScanned(st, scannedHealthSources, r.ID, "gcp", p.ID, TypeComputeRegionHealthSource, hs, store.RelUses); err != nil {
				return fmt.Errorf("upsert compositeHealthCheck→healthSource: %w", err)
			}
		}
	}
	return nil
}

func resolveRegionHealthCheckServiceRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeRegionHealthCheckService},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	healthCheckTypes := []string{TypeComputeRegionHealthCheck, TypeComputeHealthCheck}
	scannedHealthChecks, err := scannedIDSet(p, st, healthCheckTypes...)
	if err != nil {
		return err
	}
	negTypes := []string{TypeComputeNetworkEndpointGroup, TypeComputeRegionNetworkEndpointGroup, TypeComputeGlobalNetworkEndpointGroup}
	scannedNEGs, err := scannedIDSet(p, st, negTypes...)
	if err != nil {
		return err
	}
	scannedNotificationEndpoints, err := scannedIDSet(p, st, TypeComputeRegionNotificationEndpoint)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			HealthChecks          []string `json:"healthChecks"`
			NetworkEndpointGroups []string `json:"networkEndpointGroups"`
			NotificationEndpoints []string `json:"notificationEndpoints"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, hc := range attrs.HealthChecks {
			if err := upsertIfScannedAny(st, scannedHealthChecks, r.ID, "gcp", p.ID, healthCheckTypes, hc, store.RelUses); err != nil {
				return fmt.Errorf("upsert healthCheckService→healthCheck: %w", err)
			}
		}
		for _, neg := range attrs.NetworkEndpointGroups {
			if err := upsertIfScannedAny(st, scannedNEGs, r.ID, "gcp", p.ID, negTypes, neg, store.RelUses); err != nil {
				return fmt.Errorf("upsert healthCheckService→neg: %w", err)
			}
		}
		for _, ne := range attrs.NotificationEndpoints {
			if err := upsertIfScanned(st, scannedNotificationEndpoints, r.ID, "gcp", p.ID, TypeComputeRegionNotificationEndpoint, ne, store.RelUses); err != nil {
				return fmt.Errorf("upsert healthCheckService→notificationEndpoint: %w", err)
			}
		}
	}
	return nil
}

func resolveRegionHealthSourceRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeRegionHealthSource},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scannedPolicies, err := scannedIDSet(p, st, TypeComputeRegionHealthAggregationPolicy)
	if err != nil {
		return err
	}
	if len(scannedPolicies) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			HealthAggregationPolicy string `json:"healthAggregationPolicy"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scannedPolicies, r.ID, "gcp", p.ID, TypeComputeRegionHealthAggregationPolicy, attrs.HealthAggregationPolicy, store.RelUses); err != nil {
			return fmt.Errorf("upsert healthSource→healthAggregationPolicy: %w", err)
		}
	}
	return nil
}
