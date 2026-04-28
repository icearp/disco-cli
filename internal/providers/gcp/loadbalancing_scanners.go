package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/compute/v1"
)

func init() { registerService(serviceEntry{name: "gcp:loadbalancing", fn: scanLoadBalancing}) }

// scanLoadBalancing discovers the global GCP load-balancer hot path:
// forwarding rules (global + every region via AggregatedList), the four
// target-proxy variants currently resolved (HTTP, HTTPS), URL maps, backend
// services (global + regional via AggregatedList), and backend buckets.
//
// Scope decisions:
//   - TargetTcp/Ssl/GrpcProxies + regional UrlMaps deferred — proxy variants
//     repeat the same scanner shape and the global HTTP(S) path covers the
//     overwhelming majority of L7 LBs. Regional UrlMaps land alongside
//     regional Internal HTTP(S) LB attention in a follow-up.
//   - InstanceGroups + NetworkEndpointGroups intentionally NOT scanned here.
//     They have their own R4 follow-up and the resolver below skips edges
//     that would FK-violate against absent rows.
//   - SslCertificates + SslPolicies + HealthChecks deferred — narrow security
//     value vs. node count; HealthChecks especially produce hundreds of rows
//     in busy projects with no edges of their own.
//
// All `*.List` calls here are global-scope; `AggregatedList` covers global +
// every region in a single paginated walk so we don't need a per-region
// fan-out tier yet.
func scanLoadBalancing(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := compute.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("compute client: %w", err)
	}

	phases := []struct {
		label string
		fn    func() (int, int, error)
	}{
		{"forwardingRules.aggregatedList", func() (int, int, error) { return scanForwardingRules(ctx, svc, p, st, scanID) }},
		{"targetHttpProxies.list", func() (int, int, error) { return scanTargetHTTPProxies(ctx, svc, p, st, scanID) }},
		{"targetHttpsProxies.list", func() (int, int, error) { return scanTargetHTTPSProxies(ctx, svc, p, st, scanID) }},
		{"urlMaps.list", func() (int, int, error) { return scanURLMaps(ctx, svc, p, st, scanID) }},
		{"backendServices.aggregatedList", func() (int, int, error) { return scanBackendServices(ctx, svc, p, st, scanID) }},
		{"backendBuckets.list", func() (int, int, error) { return scanBackendBuckets(ctx, svc, p, st, scanID) }},
	}
	for _, phase := range phases {
		t, n, err := phase.fn()
		if err != nil {
			if isPermissionDenied(err) {
				_ = skipIfDenied(st, "compute:"+phase.label, p.ID, err)
				continue
			}
			return total, inserted, err
		}
		total += t
		inserted += n
	}
	return total, inserted, nil
}

// upsertWithProjClosure upserts a batch and fans out hierarchy_closure pairs
// to the project parent. Centralizes the boilerplate the LB sub-phases all
// share.
func upsertWithProjClosure(p *project, st *store.Store, batch []*store.Resource) (int, int, error) {
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, err
	}
	projParentID := store.ResourceID("gcp", p.ID, TypeProject, p.ID)
	pairs := make([][2]string, 0, len(batch))
	for _, r := range batch {
		pairs = append(pairs, [2]string{
			store.ResourceID(r.Provider, r.AccountID, r.Type, r.NativeID),
			projParentID,
		})
	}
	if err := st.BatchAddToHierarchyClosure(pairs); err != nil {
		return len(batch), n, err
	}
	return len(batch), n, nil
}

func scanForwardingRules(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	err = svc.ForwardingRules.AggregatedList(p.ID).Pages(ctx, func(page *compute.ForwardingRuleAggregatedList) error {
		var batch []*store.Resource
		for scope, items := range page.Items {
			region := scopedListRegion(scope)
			for _, fr := range items.ForwardingRules {
				name := fr.Name
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeComputeForwardingRule,
					NativeID:       fr.SelfLink,
					Name:           &name,
					Region:         strp(region),
					CreatedAt:      strp(fr.CreationTimestamp),
					AttributesJSON: mustJSON(fr),
					DiscoveredBy:   scanID,
				})
			}
		}
		t, n, e := upsertWithProjClosure(p, st, batch)
		total += t
		inserted += n
		return e
	})
	return
}

func scanTargetHTTPProxies(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	err = svc.TargetHttpProxies.List(p.ID).Pages(ctx, func(page *compute.TargetHttpProxyList) error {
		var batch []*store.Resource
		for _, tp := range page.Items {
			name := tp.Name
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeComputeTargetHTTPProxy,
				NativeID:       tp.SelfLink,
				Name:           &name,
				CreatedAt:      strp(tp.CreationTimestamp),
				AttributesJSON: mustJSON(tp),
				DiscoveredBy:   scanID,
			})
		}
		t, n, e := upsertWithProjClosure(p, st, batch)
		total += t
		inserted += n
		return e
	})
	return
}

func scanTargetHTTPSProxies(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	err = svc.TargetHttpsProxies.List(p.ID).Pages(ctx, func(page *compute.TargetHttpsProxyList) error {
		var batch []*store.Resource
		for _, tp := range page.Items {
			name := tp.Name
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeComputeTargetHTTPSProxy,
				NativeID:       tp.SelfLink,
				Name:           &name,
				CreatedAt:      strp(tp.CreationTimestamp),
				AttributesJSON: mustJSON(tp),
				DiscoveredBy:   scanID,
			})
		}
		t, n, e := upsertWithProjClosure(p, st, batch)
		total += t
		inserted += n
		return e
	})
	return
}

func scanURLMaps(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	err = svc.UrlMaps.List(p.ID).Pages(ctx, func(page *compute.UrlMapList) error {
		var batch []*store.Resource
		for _, um := range page.Items {
			name := um.Name
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeComputeURLMap,
				NativeID:       um.SelfLink,
				Name:           &name,
				CreatedAt:      strp(um.CreationTimestamp),
				AttributesJSON: mustJSON(um),
				DiscoveredBy:   scanID,
			})
		}
		t, n, e := upsertWithProjClosure(p, st, batch)
		total += t
		inserted += n
		return e
	})
	return
}

func scanBackendServices(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	err = svc.BackendServices.AggregatedList(p.ID).Pages(ctx, func(page *compute.BackendServiceAggregatedList) error {
		var batch []*store.Resource
		for scope, items := range page.Items {
			region := scopedListRegion(scope)
			for _, bs := range items.BackendServices {
				name := bs.Name
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeComputeBackendService,
					NativeID:       bs.SelfLink,
					Name:           &name,
					Region:         strp(region),
					CreatedAt:      strp(bs.CreationTimestamp),
					AttributesJSON: mustJSON(bs),
					DiscoveredBy:   scanID,
				})
			}
		}
		t, n, e := upsertWithProjClosure(p, st, batch)
		total += t
		inserted += n
		return e
	})
	return
}

func scanBackendBuckets(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	err = svc.BackendBuckets.List(p.ID).Pages(ctx, func(page *compute.BackendBucketList) error {
		var batch []*store.Resource
		for _, bb := range page.Items {
			name := bb.Name
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeComputeBackendBucket,
				NativeID:       bb.SelfLink,
				Name:           &name,
				CreatedAt:      strp(bb.CreationTimestamp),
				AttributesJSON: mustJSON(bb),
				DiscoveredBy:   scanID,
			})
		}
		t, n, e := upsertWithProjClosure(p, st, batch)
		total += t
		inserted += n
		return e
	})
	return
}

// scopedListRegion extracts the region from an AggregatedList scope key.
// Scope keys come in two shapes: "global" or "regions/{region}". For global
// returns "". For regional returns the region segment.
func scopedListRegion(scope string) string {
	const prefix = "regions/"
	if len(scope) > len(prefix) && scope[:len(prefix)] == prefix {
		return scope[len(prefix):]
	}
	return ""
}
