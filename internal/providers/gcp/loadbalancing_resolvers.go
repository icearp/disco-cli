package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveLoadBalancingRelationships,
		EdgeDecl{TypeComputeForwardingRule, TypeComputeTargetHTTPProxy, store.RelRoutesTo},
		EdgeDecl{TypeComputeForwardingRule, TypeComputeTargetHTTPSProxy, store.RelRoutesTo},
		EdgeDecl{TypeComputeForwardingRule, TypeComputeTargetGrpcProxy, store.RelRoutesTo},
		EdgeDecl{TypeComputeForwardingRule, TypeComputeTargetSslProxy, store.RelRoutesTo},
		EdgeDecl{TypeComputeForwardingRule, TypeComputeTargetTcpProxy, store.RelRoutesTo},
		EdgeDecl{TypeComputeForwardingRule, TypeComputeRegionTargetTcpProxy, store.RelRoutesTo},
		EdgeDecl{TypeComputeForwardingRule, TypeComputeBackendService, store.RelRoutesTo},
		EdgeDecl{TypeComputeGlobalForwardingRule, TypeComputeTargetHTTPProxy, store.RelRoutesTo},
		EdgeDecl{TypeComputeGlobalForwardingRule, TypeComputeTargetHTTPSProxy, store.RelRoutesTo},
		EdgeDecl{TypeComputeGlobalForwardingRule, TypeComputeTargetGrpcProxy, store.RelRoutesTo},
		EdgeDecl{TypeComputeGlobalForwardingRule, TypeComputeTargetSslProxy, store.RelRoutesTo},
		EdgeDecl{TypeComputeGlobalForwardingRule, TypeComputeTargetTcpProxy, store.RelRoutesTo},
		EdgeDecl{TypeComputeGlobalForwardingRule, TypeComputeBackendService, store.RelRoutesTo},
		EdgeDecl{TypeComputeTargetHTTPProxy, TypeComputeURLMap, store.RelRoutesTo},
		EdgeDecl{TypeComputeTargetHTTPSProxy, TypeComputeURLMap, store.RelRoutesTo},
		EdgeDecl{TypeComputeRegionTargetHTTPProxy, TypeComputeRegionURLMap, store.RelRoutesTo},
		EdgeDecl{TypeComputeRegionTargetHTTPSProxy, TypeComputeRegionURLMap, store.RelRoutesTo},
		EdgeDecl{TypeComputeTargetGrpcProxy, TypeComputeURLMap, store.RelRoutesTo},
		EdgeDecl{TypeComputeTargetSslProxy, TypeComputeBackendService, store.RelRoutesTo},
		EdgeDecl{TypeComputeTargetTcpProxy, TypeComputeBackendService, store.RelRoutesTo},
		EdgeDecl{TypeComputeRegionTargetTcpProxy, TypeComputeRegionBackendService, store.RelRoutesTo},
		EdgeDecl{TypeComputeURLMap, TypeComputeBackendService, store.RelRoutesTo},
		EdgeDecl{TypeComputeURLMap, TypeComputeBackendBucket, store.RelRoutesTo},
		EdgeDecl{TypeComputeRegionURLMap, TypeComputeRegionBackendService, store.RelRoutesTo},
		EdgeDecl{TypeComputeRegionURLMap, TypeComputeRegionBackendBucket, store.RelRoutesTo},
	)
}

// resolveLoadBalancingRelationships derives the LB traffic-flow chain:
//
//	forwardingRule -[routes-to]-> targetHttp(s)Proxy / targetGrpcProxy / targetSsl|TcpProxy / backendService
//	targetHttp(s)Proxy / targetGrpcProxy -[routes-to]-> urlMap, then -> backendService / backendBucket (defaultService)
//	targetSsl|TcpProxy -[routes-to]-> backendService directly (no urlMap hop)
//
// Global and regional variants of every type in the chain share the exact
// same field names (confirmed via `go doc` against the compute/v1 SDK), so
// each stage's type list just grows rather than needing parallel logic.
//
// All target-of-edge fields are full SelfLink URLs, so lookup is a direct
// NativeID match against the in-store LB catalog. Cross-project /
// unscanned-resource references skipped.
//
// Deferred:
//   - urlMap pathMatchers / hostRules per-route service edges (same shape as
//     defaultService, but cardinality multiplies by route count).
//   - backendService backends[].group → instance-group / NEG (instance-groups
//     and NEGs not yet scanned; would FK-violate).
//   - urlMap → backendBucket via pathMatchers (same as default, deferred).
//   - SslCertificate / SslPolicy / EdgeSecurityPolicy edges (no scanner yet).
//   - RegionHealthCheckService / RegionHealthSource / RegionCompositeHealthCheck
//     edges (separate healthChecks[]/networkEndpointGroups[] fan-out shape,
//     next wave).
//   - forwardingRule → targetPool / targetInstance (Network LB / single-instance
//     LB forwarding rules) — handled by compute_lb_misc_resolvers.go's own
//     TargetPool/TargetInstance resolvers instead, which only cover those
//     types' own outbound edges; the forwardingRule→targetPool inbound edge
//     itself is not yet wired.
func resolveLoadBalancingRelationships(p *project, st *store.Store) error {
	// Build a NativeID → resource-ID index of everything just scanned, so
	// every edge can be FK-checked before insert. One ListResources call per
	// type bounds overhead vs. a SELECT per edge candidate.
	types := []string{
		TypeComputeTargetHTTPProxy, TypeComputeRegionTargetHTTPProxy,
		TypeComputeTargetHTTPSProxy, TypeComputeRegionTargetHTTPSProxy,
		TypeComputeTargetGrpcProxy,
		TypeComputeTargetSslProxy,
		TypeComputeTargetTcpProxy, TypeComputeRegionTargetTcpProxy,
		TypeComputeURLMap, TypeComputeRegionURLMap,
		TypeComputeBackendService, TypeComputeRegionBackendService,
		TypeComputeBackendBucket, TypeComputeRegionBackendBucket,
	}
	idByNative := make(map[string]string)
	for _, t := range types {
		rs, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{t},
			Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rs {
			idByNative[r.NativeID] = r.ID
		}
	}

	emit := func(fromID, toNative string) error {
		if toNative == "" {
			return nil
		}
		toID, ok := idByNative[toNative]
		if !ok {
			return nil
		}
		if err := st.UpsertRelationship(fromID, toID, store.RelRoutesTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert LB edge: %w", err)
		}
		return nil
	}

	// Forwarding rules (global + regional) → target / backendService.
	for _, t := range []string{TypeComputeForwardingRule, TypeComputeGlobalForwardingRule} {
		frs, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{t},
			Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, fr := range frs {
			var a struct {
				Target         string `json:"target"`
				BackendService string `json:"backendService"`
			}
			if err := json.Unmarshal([]byte(fr.AttributesJSON), &a); err != nil {
				continue
			}
			if err := emit(fr.ID, a.Target); err != nil {
				return err
			}
			if err := emit(fr.ID, a.BackendService); err != nil {
				return err
			}
		}
	}

	// Target HTTP(S) / gRPC proxies → urlMap.
	for _, t := range []string{
		TypeComputeTargetHTTPProxy, TypeComputeRegionTargetHTTPProxy,
		TypeComputeTargetHTTPSProxy, TypeComputeRegionTargetHTTPSProxy,
		TypeComputeTargetGrpcProxy,
	} {
		ps, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{t},
			Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, pp := range ps {
			var a struct {
				URLMap string `json:"urlMap"`
			}
			if err := json.Unmarshal([]byte(pp.AttributesJSON), &a); err != nil {
				continue
			}
			if err := emit(pp.ID, a.URLMap); err != nil {
				return err
			}
		}
	}

	// Target SSL / TCP proxies → backendService directly (no urlMap hop —
	// these terminate raw TCP/SSL, not HTTP routing).
	for _, t := range []string{TypeComputeTargetSslProxy, TypeComputeTargetTcpProxy, TypeComputeRegionTargetTcpProxy} {
		ps, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{t},
			Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, pp := range ps {
			var a struct {
				Service string `json:"service"`
			}
			if err := json.Unmarshal([]byte(pp.AttributesJSON), &a); err != nil {
				continue
			}
			if err := emit(pp.ID, a.Service); err != nil {
				return err
			}
		}
	}

	// URL maps (global + regional) → defaultService (BackendService or BackendBucket).
	for _, t := range []string{TypeComputeURLMap, TypeComputeRegionURLMap} {
		ums, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{t},
			Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, um := range ums {
			var a struct {
				DefaultService string `json:"defaultService"`
			}
			if err := json.Unmarshal([]byte(um.AttributesJSON), &a); err != nil {
				continue
			}
			if err := emit(um.ID, a.DefaultService); err != nil {
				return err
			}
		}
	}
	return nil
}
