package gcp

import (
	"context"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"google.golang.org/api/compute/v1"
)

// Wave 3 of the GCP type-coverage buildout (docs/gcp-type-coverage.md): the
// Compute Engine addressing domain. New phases of the existing "gcp:compute"
// service. No resolver this wave — Address/PublicDelegatedPrefix reference
// their consumers via bare self-link strings (Address.Users[]) whose target
// type can't be determined without parsing the URL path for every possible
// consumer kind (instance, forwarding rule, ...); left as a follow-up rather
// than guessing.
func init() {
	registerType(restype.Descriptor{Type: TypeComputeAddress, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeGlobalAddress, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputePublicAdvertisedPrefix, Service: "compute", Leaf: true})
	registerType(restype.Descriptor{Type: TypeComputePublicDelegatedPrefix, Service: "compute"})
	registerType(restype.Descriptor{Type: TypeComputeGlobalPublicDelegatedPrefix, Service: "compute"})
}

func scanComputeAddresses(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:addresses.aggregatedList",
		svc.Addresses.AggregatedList(p.ID),
		func(page *compute.AddressAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				region := scopedListRegion(scope)
				if region == "" {
					continue
				}
				for _, a := range items.Addresses {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeAddress, NativeID: a.SelfLink, Name: &a.Name,
						Region:         &region,
						CreatedAt:      strp(a.CreationTimestamp),
						Status:         strp(a.Status),
						AttributesJSON: mustJSON(a),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeGlobalAddresses(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:globalAddresses.list",
		svc.GlobalAddresses.List(p.ID),
		func(page *compute.AddressList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, a := range page.Items {
				batch = append(batch, &store.Resource{
					Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
					Type: TypeComputeGlobalAddress, NativeID: a.SelfLink, Name: &a.Name,
					CreatedAt:      strp(a.CreationTimestamp),
					Status:         strp(a.Status),
					AttributesJSON: mustJSON(a),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputePublicAdvertisedPrefixes(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:publicAdvertisedPrefixes.list",
		svc.PublicAdvertisedPrefixes.List(p.ID),
		func(page *compute.PublicAdvertisedPrefixList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, pp := range page.Items {
				batch = append(batch, &store.Resource{
					Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
					Type: TypeComputePublicAdvertisedPrefix, NativeID: pp.SelfLink, Name: &pp.Name,
					CreatedAt:      strp(pp.CreationTimestamp),
					Status:         strp(pp.Status),
					AttributesJSON: mustJSON(pp),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

// scanComputePublicDelegatedPrefixes covers both PublicDelegatedPrefix
// (regional) and GlobalPublicDelegatedPrefix (global) via a single
// AggregatedList call — same combined-scope shape as
// scanComputeInstanceTemplates in Wave 2.
func scanComputePublicDelegatedPrefixes(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:publicDelegatedPrefixes.aggregatedList",
		svc.PublicDelegatedPrefixes.AggregatedList(p.ID),
		func(page *compute.PublicDelegatedPrefixAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				discoType := TypeComputeGlobalPublicDelegatedPrefix
				var region *string
				if region0 := scopedListRegion(scope); region0 != "" {
					discoType = TypeComputePublicDelegatedPrefix
					region = &region0
				}
				for _, pp := range items.PublicDelegatedPrefixes {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: discoType, NativeID: pp.SelfLink, Name: &pp.Name,
						Region:         region,
						CreatedAt:      strp(pp.CreationTimestamp),
						Status:         strp(pp.Status),
						AttributesJSON: mustJSON(pp),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}
