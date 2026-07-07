package gcp

import (
	"context"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/compute/v1"
)

// Wave 6 of the GCP type-coverage buildout (docs/gcp-type-coverage.md): the
// remaining load-balancing / health-check / SSL-TLS domain, on top of the
// pre-existing "gcp:loadbalancing" service (ForwardingRule, TargetHTTP(S)Proxy,
// URLMap, BackendService, BackendBucket). New phases of "gcp:compute". No
// resolver this wave — same networking-domain resolver follow-up noted in
// compute_networking_scanners.go.
func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeGlobalForwardingRule},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeHealthCheck, Leaf: true},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeRegionHealthCheck, Leaf: true},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeRegionCompositeHealthCheck},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeRegionHealthAggregationPolicy, Leaf: true},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeRegionHealthCheckService},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeRegionHealthSource},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeRegionNotificationEndpoint, Leaf: true},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeHttpHealthCheck, Leaf: true},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeHttpsHealthCheck, Leaf: true},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeSslCertificate, Leaf: true},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeRegionSslCertificate, Leaf: true},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeSslPolicy, Leaf: true},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeRegionSslPolicy, Leaf: true},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeTargetSslProxy},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeTargetTcpProxy},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeRegionTargetTcpProxy},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeTargetGrpcProxy},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeRegionTargetHTTPProxy},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeRegionTargetHTTPSProxy},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeRegionURLMap},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeRegionBackendBucket},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeTargetInstance},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeTargetPool},
	)
}

// scanComputeGlobalForwardingRules reuses the shared compute.ForwardingRule /
// ForwardingRuleList SDK types — GCP has no separate GlobalForwardingRule
// struct, just a global-scoped List service returning the same item shape as
// the already-scanned regional forwarding rules.
func scanComputeGlobalForwardingRules(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:globalForwardingRules.list",
		svc.GlobalForwardingRules.List(p.ID),
		func(page *compute.ForwardingRuleList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, fr := range page.Items {
				batch = append(batch, &store.Resource{
					Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
					Type: TypeComputeGlobalForwardingRule, NativeID: fr.SelfLink, Name: &fr.Name,
					CreatedAt: strp(fr.CreationTimestamp), AttributesJSON: mustJSON(fr),
					DiscoveredBy: scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanComputeHealthChecks covers both HealthCheck (global) and
// RegionHealthCheck (regional) via one combined-scope AggregatedList call —
// same dual-type split as Wave 2/3/4's InstanceTemplate/PublicDelegatedPrefix/
// NetworkFirewallPolicy. The separate RegionHealthChecksService.List is
// redundant with this and intentionally unused.
func scanComputeHealthChecks(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:healthChecks.aggregatedList",
		svc.HealthChecks.AggregatedList(p.ID),
		func(page *compute.HealthChecksAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				discoType := TypeComputeHealthCheck
				var region *string
				if region0 := scopedListRegion(scope); region0 != "" {
					discoType = TypeComputeRegionHealthCheck
					region = &region0
				}
				for _, hc := range items.HealthChecks {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: discoType, NativeID: hc.SelfLink, Name: &hc.Name,
						Region:         region,
						CreatedAt:      strp(hc.CreationTimestamp),
						AttributesJSON: mustJSON(hc),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeRegionCompositeHealthChecks(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:regionCompositeHealthChecks.aggregatedList",
		svc.RegionCompositeHealthChecks.AggregatedList(p.ID),
		func(page *compute.CompositeHealthCheckAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				region := scopedListRegion(scope)
				if region == "" {
					continue
				}
				for _, chc := range items.CompositeHealthChecks {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeRegionCompositeHealthCheck, NativeID: chc.SelfLink, Name: &chc.Name,
						Region:         &region,
						CreatedAt:      strp(chc.CreationTimestamp),
						AttributesJSON: mustJSON(chc),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanComputeRegionHealthAggregationPolicies' AggregatedList spans
// regional+global scopes, but the ledger tracks one disco type — same
// opportunistic-Region shape as Wave 4's NetworkAttachment/ServiceAttachment.
func scanComputeRegionHealthAggregationPolicies(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:regionHealthAggregationPolicies.aggregatedList",
		svc.RegionHealthAggregationPolicies.AggregatedList(p.ID),
		func(page *compute.HealthAggregationPolicyAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				var region *string
				if region0 := scopedListRegion(scope); region0 != "" {
					region = &region0
				}
				for _, hap := range items.HealthAggregationPolicies {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeRegionHealthAggregationPolicy, NativeID: hap.SelfLink, Name: &hap.Name,
						Region:         region,
						CreatedAt:      strp(hap.CreationTimestamp),
						AttributesJSON: mustJSON(hap),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanComputeRegionHealthCheckServices — same opportunistic-Region shape as
// above. Note the scoped-list slice field is "Resources", not
// "HealthCheckServices" (SDK naming irregularity, confirmed against the
// vendored source).
func scanComputeRegionHealthCheckServices(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:regionHealthCheckServices.aggregatedList",
		svc.RegionHealthCheckServices.AggregatedList(p.ID),
		func(page *compute.HealthCheckServiceAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				var region *string
				if region0 := scopedListRegion(scope); region0 != "" {
					region = &region0
				}
				for _, hcs := range items.Resources {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeRegionHealthCheckService, NativeID: hcs.SelfLink, Name: &hcs.Name,
						Region:         region,
						CreatedAt:      strp(hcs.CreationTimestamp),
						AttributesJSON: mustJSON(hcs),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeRegionHealthSources(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:regionHealthSources.aggregatedList",
		svc.RegionHealthSources.AggregatedList(p.ID),
		func(page *compute.HealthSourceAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				region := scopedListRegion(scope)
				if region == "" {
					continue
				}
				for _, hs := range items.HealthSources {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeRegionHealthSource, NativeID: hs.SelfLink, Name: &hs.Name,
						Region:         &region,
						CreatedAt:      strp(hs.CreationTimestamp),
						AttributesJSON: mustJSON(hs),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanComputeRegionNotificationEndpoints — same opportunistic-Region shape;
// scoped-list slice field is also "Resources" (matches
// scanComputeRegionHealthCheckServices' irregularity).
func scanComputeRegionNotificationEndpoints(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:regionNotificationEndpoints.aggregatedList",
		svc.RegionNotificationEndpoints.AggregatedList(p.ID),
		func(page *compute.NotificationEndpointAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				var region *string
				if region0 := scopedListRegion(scope); region0 != "" {
					region = &region0
				}
				for _, ne := range items.Resources {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeRegionNotificationEndpoint, NativeID: ne.SelfLink, Name: &ne.Name,
						Region:         region,
						CreatedAt:      strp(ne.CreationTimestamp),
						AttributesJSON: mustJSON(ne),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeHttpHealthChecks(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:httpHealthChecks.list",
		svc.HttpHealthChecks.List(p.ID),
		func(page *compute.HttpHealthCheckList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, hc := range page.Items {
				batch = append(batch, &store.Resource{
					Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
					Type: TypeComputeHttpHealthCheck, NativeID: hc.SelfLink, Name: &hc.Name,
					CreatedAt: strp(hc.CreationTimestamp), AttributesJSON: mustJSON(hc),
					DiscoveredBy: scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeHttpsHealthChecks(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:httpsHealthChecks.list",
		svc.HttpsHealthChecks.List(p.ID),
		func(page *compute.HttpsHealthCheckList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, hc := range page.Items {
				batch = append(batch, &store.Resource{
					Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
					Type: TypeComputeHttpsHealthCheck, NativeID: hc.SelfLink, Name: &hc.Name,
					CreatedAt: strp(hc.CreationTimestamp), AttributesJSON: mustJSON(hc),
					DiscoveredBy: scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanComputeSslCertificates covers both SslCertificate (global) and
// RegionSslCertificate (regional) via one combined-scope AggregatedList call.
func scanComputeSslCertificates(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:sslCertificates.aggregatedList",
		svc.SslCertificates.AggregatedList(p.ID),
		func(page *compute.SslCertificateAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				discoType := TypeComputeSslCertificate
				var region *string
				if region0 := scopedListRegion(scope); region0 != "" {
					discoType = TypeComputeRegionSslCertificate
					region = &region0
				}
				for _, cert := range items.SslCertificates {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: discoType, NativeID: cert.SelfLink, Name: &cert.Name,
						Region:         region,
						CreatedAt:      strp(cert.CreationTimestamp),
						AttributesJSON: mustJSON(cert),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanComputeSslPolicies covers both SslPolicy (global) and RegionSslPolicy
// (regional) via one combined-scope AggregatedList call.
func scanComputeSslPolicies(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:sslPolicies.aggregatedList",
		svc.SslPolicies.AggregatedList(p.ID),
		func(page *compute.SslPoliciesAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				discoType := TypeComputeSslPolicy
				var region *string
				if region0 := scopedListRegion(scope); region0 != "" {
					discoType = TypeComputeRegionSslPolicy
					region = &region0
				}
				for _, pol := range items.SslPolicies {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: discoType, NativeID: pol.SelfLink, Name: &pol.Name,
						Region:         region,
						CreatedAt:      strp(pol.CreationTimestamp),
						AttributesJSON: mustJSON(pol),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeTargetSslProxies(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:targetSslProxies.list",
		svc.TargetSslProxies.List(p.ID),
		func(page *compute.TargetSslProxyList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, tsp := range page.Items {
				batch = append(batch, &store.Resource{
					Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
					Type: TypeComputeTargetSslProxy, NativeID: tsp.SelfLink, Name: &tsp.Name,
					CreatedAt: strp(tsp.CreationTimestamp), AttributesJSON: mustJSON(tsp),
					DiscoveredBy: scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanComputeTargetTcpProxies covers both TargetTcpProxy (global) and
// RegionTargetTcpProxy (regional) via one combined-scope AggregatedList call.
func scanComputeTargetTcpProxies(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:targetTcpProxies.aggregatedList",
		svc.TargetTcpProxies.AggregatedList(p.ID),
		func(page *compute.TargetTcpProxyAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				discoType := TypeComputeTargetTcpProxy
				var region *string
				if region0 := scopedListRegion(scope); region0 != "" {
					discoType = TypeComputeRegionTargetTcpProxy
					region = &region0
				}
				for _, tp := range items.TargetTcpProxies {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: discoType, NativeID: tp.SelfLink, Name: &tp.Name,
						Region:         region,
						CreatedAt:      strp(tp.CreationTimestamp),
						AttributesJSON: mustJSON(tp),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeTargetGrpcProxies(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:targetGrpcProxies.list",
		svc.TargetGrpcProxies.List(p.ID),
		func(page *compute.TargetGrpcProxyList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, tgp := range page.Items {
				batch = append(batch, &store.Resource{
					Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
					Type: TypeComputeTargetGrpcProxy, NativeID: tgp.SelfLink, Name: &tgp.Name,
					CreatedAt: strp(tgp.CreationTimestamp), AttributesJSON: mustJSON(tgp),
					DiscoveredBy: scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanComputeRegionTargetHTTPProxies/HTTPSProxies/URLMaps/BackendBuckets have
// no combined-scope AggregatedList — their existing global scanners
// (loadbalancing_scanners.go) use plain List, so the regional variant needs
// its own per-region fan-out against a genuinely separate RegionX service,
// reusing the same item/list SDK types the global scanners already import.
func scanComputeRegionTargetHTTPProxies(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return gcpRegionFanoutScan(ctx, p, st, fanoutMed, "compute:regionTargetHttpProxies.list",
		func(region string) pager[compute.TargetHttpProxyList] {
			return svc.RegionTargetHttpProxies.List(p.ID, region)
		},
		func(page *compute.TargetHttpProxyList) []*compute.TargetHttpProxy { return page.Items },
		func(tp *compute.TargetHttpProxy, region string) *store.Resource {
			return &store.Resource{
				Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
				Type: TypeComputeRegionTargetHTTPProxy, NativeID: tp.SelfLink, Name: &tp.Name,
				Region:         &region,
				CreatedAt:      strp(tp.CreationTimestamp),
				AttributesJSON: mustJSON(tp),
				DiscoveredBy:   scanID,
			}
		})
}

func scanComputeRegionTargetHTTPSProxies(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return gcpRegionFanoutScan(ctx, p, st, fanoutMed, "compute:regionTargetHttpsProxies.list",
		func(region string) pager[compute.TargetHttpsProxyList] {
			return svc.RegionTargetHttpsProxies.List(p.ID, region)
		},
		func(page *compute.TargetHttpsProxyList) []*compute.TargetHttpsProxy { return page.Items },
		func(tp *compute.TargetHttpsProxy, region string) *store.Resource {
			return &store.Resource{
				Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
				Type: TypeComputeRegionTargetHTTPSProxy, NativeID: tp.SelfLink, Name: &tp.Name,
				Region:         &region,
				CreatedAt:      strp(tp.CreationTimestamp),
				AttributesJSON: mustJSON(tp),
				DiscoveredBy:   scanID,
			}
		})
}

func scanComputeRegionURLMaps(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return gcpRegionFanoutScan(ctx, p, st, fanoutMed, "compute:regionUrlMaps.list",
		func(region string) pager[compute.UrlMapList] { return svc.RegionUrlMaps.List(p.ID, region) },
		func(page *compute.UrlMapList) []*compute.UrlMap { return page.Items },
		func(um *compute.UrlMap, region string) *store.Resource {
			return &store.Resource{
				Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
				Type: TypeComputeRegionURLMap, NativeID: um.SelfLink, Name: &um.Name,
				Region:         &region,
				CreatedAt:      strp(um.CreationTimestamp),
				AttributesJSON: mustJSON(um),
				DiscoveredBy:   scanID,
			}
		})
}

func scanComputeRegionBackendBuckets(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return gcpRegionFanoutScan(ctx, p, st, fanoutMed, "compute:regionBackendBuckets.list",
		func(region string) pager[compute.BackendBucketList] {
			return svc.RegionBackendBuckets.List(p.ID, region)
		},
		func(page *compute.BackendBucketList) []*compute.BackendBucket { return page.Items },
		func(bb *compute.BackendBucket, region string) *store.Resource {
			return &store.Resource{
				Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
				Type: TypeComputeRegionBackendBucket, NativeID: bb.SelfLink, Name: &bb.Name,
				Region:         &region,
				CreatedAt:      strp(bb.CreationTimestamp),
				AttributesJSON: mustJSON(bb),
				DiscoveredBy:   scanID,
			}
		})
}

func scanComputeTargetInstances(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:targetInstances.aggregatedList",
		svc.TargetInstances.AggregatedList(p.ID),
		func(page *compute.TargetInstanceAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for _, items := range page.Items {
				for _, ti := range items.TargetInstances {
					r := &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeTargetInstance, NativeID: ti.SelfLink, Name: &ti.Name,
						CreatedAt: strp(ti.CreationTimestamp), AttributesJSON: mustJSON(ti),
						DiscoveredBy: scanID,
					}
					if ti.Zone != "" {
						zone := lastSegment(ti.Zone)
						r.Zone = &zone
						region := zoneToRegion(zone)
						r.Region = &region
					}
					batch = append(batch, r)
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeTargetPools(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:targetPools.aggregatedList",
		svc.TargetPools.AggregatedList(p.ID),
		func(page *compute.TargetPoolAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				region := scopedListRegion(scope)
				if region == "" {
					continue
				}
				for _, tp := range items.TargetPools {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeTargetPool, NativeID: tp.SelfLink, Name: &tp.Name,
						Region:         &region,
						CreatedAt:      strp(tp.CreationTimestamp),
						AttributesJSON: mustJSON(tp),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}
