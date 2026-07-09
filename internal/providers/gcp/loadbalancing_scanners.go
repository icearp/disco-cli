package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/compute/v1"
)

func init() {
	registerType(restype.Descriptor{Type: TypeComputeForwardingRule, Service: "compute", Upstream: "compute.googleapis.com/ForwardingRule"})
	registerType(restype.Descriptor{Type: TypeComputeTargetHTTPProxy, Service: "compute", Upstream: "compute.googleapis.com/TargetHttpProxy"})
	registerType(restype.Descriptor{Type: TypeComputeTargetHTTPSProxy, Service: "compute", Upstream: "compute.googleapis.com/TargetHttpsProxy"})
	registerType(restype.Descriptor{Type: TypeComputeURLMap, Service: "compute", Upstream: "compute.googleapis.com/UrlMap"})
	registerType(restype.Descriptor{Type: TypeComputeBackendService, Service: "compute", Upstream: "compute.googleapis.com/BackendService"})
	registerType(restype.Descriptor{Type: TypeComputeRegionBackendService, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeBackendBucket, Service: "compute", Upstream: "compute.googleapis.com/BackendBucket"})
	registerService(serviceEntry{
		name: "gcp:loadbalancing",
		fn:   scanLoadBalancing,
	})
}

// scanLoadBalancing discovers the global GCP load-balancer hot path:
// forwarding rules (global + every region via AggregatedList), the HTTP(S)
// target-proxy variants + URL maps + backend buckets (global-only List),
// and backend services (global + regional via AggregatedList).
//
// TargetTcp/Ssl/GrpcProxies, regional UrlMaps/TargetHTTP(S)Proxies/
// BackendBuckets, SslCertificates/SslPolicies, HealthChecks and friends, and
// InstanceGroups/NetworkEndpointGroups are scanned as separate phases of
// scanCompute (compute_scanners.go) — see compute_lb_ext_scanners.go (Wave 6)
// and compute_instance_group_scanners.go / compute_networking_scanners.go.
//
// All `*.List` calls here are global-scope; `AggregatedList` covers global +
// every region in one paginated walk, so no per-region fan-out tier is
// needed here.
func scanLoadBalancing(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := compute.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("compute client: %w", err)
	}
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanForwardingRules(ctx, svc, p, st, scanID) },
		func() (int, int, error) { return scanTargetHTTPProxies(ctx, svc, p, st, scanID) },
		func() (int, int, error) { return scanTargetHTTPSProxies(ctx, svc, p, st, scanID) },
		func() (int, int, error) { return scanURLMaps(ctx, svc, p, st, scanID) },
		func() (int, int, error) { return scanBackendServices(ctx, svc, p, st, scanID) },
		func() (int, int, error) { return scanBackendBuckets(ctx, svc, p, st, scanID) },
	} {
		t, n, err := phase()
		if err != nil {
			return total, inserted, err
		}
		total += t
		inserted += n
	}
	return total, inserted, nil
}

// upsertWithProjClosure upserts a batch and fans out hierarchy_closure pairs
// to the project parent — centralizes boilerplate shared by the LB
// sub-phases. Equivalent to upsertWithParent with the project's resource ID.
func upsertWithProjClosure(p *project, st *store.Store, batch []*store.Resource) (int, int, error) {
	return upsertWithParent(st, batch, store.ResourceID("gcp", p.ID, TypeProject, p.ID))
}

// upsertWithParent upserts a batch and fans out hierarchy_closure pairs to a
// caller-supplied parent resource id. Use when the parent isn't the project
// (e.g. table→dataset, record-set→managed-zone, entry→cert-map,
// crypto-key→keyring, cluster→instance).
func upsertWithParent(st *store.Store, batch []*store.Resource, parentID string) (int, int, error) {
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, err := st.UpsertResources(batch)
	if err != nil {
		return 0, 0, err
	}
	pairs := make([][2]string, 0, len(batch))
	for _, r := range batch {
		pairs = append(pairs, [2]string{
			store.ResourceID(r.Provider, r.AccountID, r.Type, r.NativeID),
			parentID,
		})
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return len(batch), n, err
	}
	return len(batch), n, nil
}

func scanForwardingRules(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:forwardingRules.aggregatedList",
		svc.ForwardingRules.AggregatedList(p.ID),
		func(page *compute.ForwardingRuleAggregatedList) (int, int, error) {
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
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanTargetHTTPProxies(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:targetHttpProxies.list",
		svc.TargetHttpProxies.List(p.ID),
		func(page *compute.TargetHttpProxyList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
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
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanTargetHTTPSProxies(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:targetHttpsProxies.list",
		svc.TargetHttpsProxies.List(p.ID),
		func(page *compute.TargetHttpsProxyList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
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
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanURLMaps(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:urlMaps.list",
		svc.UrlMaps.List(p.ID),
		func(page *compute.UrlMapList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
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
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanBackendServices splits its combined-scope AggregatedList result into
// TypeComputeBackendService (global) and TypeComputeRegionBackendService
// (regional) — same dual-type split pattern as scanComputeInstanceTemplates
// (compute_instance_group_scanners.go) and scanComputeNetworkFirewallPolicies
// (compute_networking_scanners.go), so the upstream RegionBackendService
// Discovery key gets its own disco type instead of collapsing into the
// global one.
func scanBackendServices(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:backendServices.aggregatedList",
		svc.BackendServices.AggregatedList(p.ID),
		func(page *compute.BackendServiceAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				discoType := TypeComputeBackendService
				var region *string
				if region0 := scopedListRegion(scope); region0 != "" {
					discoType = TypeComputeRegionBackendService
					region = &region0
				}
				for _, bs := range items.BackendServices {
					name := bs.Name
					batch = append(batch, &store.Resource{
						Provider:       "gcp",
						AccountID:      p.ID,
						AccountName:    &p.Name,
						Type:           discoType,
						NativeID:       bs.SelfLink,
						Name:           &name,
						Region:         region,
						CreatedAt:      strp(bs.CreationTimestamp),
						AttributesJSON: mustJSON(bs),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanBackendBuckets(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:backendBuckets.list",
		svc.BackendBuckets.List(p.ID),
		func(page *compute.BackendBucketList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
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
			return upsertWithProjClosure(p, st, batch)
		})
}

// scopedListRegion extracts the region from an AggregatedList scope key.
// Scope keys are either "global" or "regions/{region}" — returns "" for
// global, the region segment otherwise.
func scopedListRegion(scope string) string {
	const prefix = "regions/"
	if len(scope) > len(prefix) && scope[:len(prefix)] == prefix {
		return scope[len(prefix):]
	}
	return ""
}
