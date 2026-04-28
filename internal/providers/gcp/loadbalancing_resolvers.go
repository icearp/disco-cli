package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveLoadBalancingRelationships) }

// resolveLoadBalancingRelationships derives the LB traffic-flow chain:
//
//	forwardingRule -[routes-to]-> targetHttpProxy / targetHttpsProxy / backendService
//	targetHttp(s)Proxy -[routes-to]-> urlMap
//	urlMap -[routes-to]-> backendService (defaultService)
//	urlMap -[routes-to]-> backendBucket  (defaultService)
//
// All target-of-edge fields are full SelfLink URLs in the SDK responses, so
// the lookup is a direct NativeID match against the in-store catalog of LB
// resources. Cross-project / unscanned-resource references skipped.
//
// Deferred:
//   - urlMap pathMatchers / hostRules per-route service edges (same shape as
//     defaultService, but cardinality multiplies by route count).
//   - backendService backends[].group → instance-group / NEG (instance-groups
//     and NEGs not yet scanned; would FK-violate).
//   - urlMap → backendBucket via pathMatchers (same as default, deferred).
//   - SslCertificate / SslPolicy / EdgeSecurityPolicy edges (no scanner yet).
func resolveLoadBalancingRelationships(p *project, st *store.Store) error {
	// Build a NativeID → resource-ID index of everything we just scanned so
	// every edge can be FK-checked before insert. One ListResources call per
	// type keeps the per-resolver overhead bounded vs. issuing a SELECT per
	// edge candidate.
	types := []string{
		TypeComputeTargetHTTPProxy,
		TypeComputeTargetHTTPSProxy,
		TypeComputeURLMap,
		TypeComputeBackendService,
		TypeComputeBackendBucket,
	}
	idByNative := make(map[string]string)
	for _, t := range types {
		rs, err := st.ListResources(store.ResourceFilter{
			Provider: "gcp", AccountID: p.ID, Types: []string{t},
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

	// Forwarding rules → target / backendService.
	frs, err := st.ListResources(store.ResourceFilter{
		Provider: "gcp", AccountID: p.ID, Types: []string{TypeComputeForwardingRule},
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

	// Target HTTP / HTTPS proxies → urlMap.
	for _, t := range []string{TypeComputeTargetHTTPProxy, TypeComputeTargetHTTPSProxy} {
		ps, err := st.ListResources(store.ResourceFilter{
			Provider: "gcp", AccountID: p.ID, Types: []string{t},
			Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, pp := range ps {
			var a struct {
				UrlMap string `json:"urlMap"`
			}
			if err := json.Unmarshal([]byte(pp.AttributesJSON), &a); err != nil {
				continue
			}
			if err := emit(pp.ID, a.UrlMap); err != nil {
				return err
			}
		}
	}

	// URL maps → defaultService (BackendService or BackendBucket).
	ums, err := st.ListResources(store.ResourceFilter{
		Provider: "gcp", AccountID: p.ID, Types: []string{TypeComputeURLMap},
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
	return nil
}
