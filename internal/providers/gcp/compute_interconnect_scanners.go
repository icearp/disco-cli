package gcp

import (
	"context"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/compute/v1"
)

// Wave 5 of the GCP type-coverage buildout (docs/gcp-type-coverage.md): the
// Compute Engine Interconnect domain. New phases of the existing
// "gcp:compute" service. No resolver this wave — InterconnectAttachment
// references its Router/Interconnect via bare self-link strings; wiring
// deferred alongside the rest of the networking-domain resolver follow-up
// noted in compute_networking_scanners.go.
func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeInterconnect},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeInterconnectAttachment},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeInterconnectGroup},
		coverage.TypeDecl{Service: "compute", DiscoType: TypeComputeInterconnectAttachmentGroup},
	)
}

func scanComputeInterconnects(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:interconnects.list",
		svc.Interconnects.List(p.ID),
		func(page *compute.InterconnectList) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, ic := range page.Items {
				batch = append(batch, &store.Resource{
					Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
					Type: TypeComputeInterconnect, NativeID: ic.SelfLink, Name: &ic.Name,
					CreatedAt: strp(ic.CreationTimestamp), AttributesJSON: mustJSON(ic),
					DiscoveredBy: scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeInterconnectAttachments(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:interconnectAttachments.aggregatedList",
		svc.InterconnectAttachments.AggregatedList(p.ID),
		func(page *compute.InterconnectAttachmentAggregatedList) (int, int, error) {
			var batch []*store.Resource
			for scope, items := range page.Items {
				region := scopedListRegion(scope)
				if region == "" {
					continue
				}
				for _, ia := range items.InterconnectAttachments {
					batch = append(batch, &store.Resource{
						Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
						Type: TypeComputeInterconnectAttachment, NativeID: ia.SelfLink, Name: &ia.Name,
						Region:         &region,
						CreatedAt:      strp(ia.CreationTimestamp),
						AttributesJSON: mustJSON(ia),
						DiscoveredBy:   scanID,
					})
				}
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeInterconnectGroups(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:interconnectGroups.list",
		svc.InterconnectGroups.List(p.ID),
		func(page *compute.InterconnectGroupsListResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, ig := range page.Items {
				batch = append(batch, &store.Resource{
					Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
					Type: TypeComputeInterconnectGroup, NativeID: ig.SelfLink, Name: &ig.Name,
					CreatedAt: strp(ig.CreationTimestamp), AttributesJSON: mustJSON(ig),
					DiscoveredBy: scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}

func scanComputeInterconnectAttachmentGroups(ctx context.Context, svc *compute.Service, p *project, st *store.Store, scanID string) (int, int, error) {
	return runPaginated(ctx, st, p, "compute:interconnectAttachmentGroups.list",
		svc.InterconnectAttachmentGroups.List(p.ID),
		func(page *compute.InterconnectAttachmentGroupsListResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Items))
			for _, iag := range page.Items {
				batch = append(batch, &store.Resource{
					Provider: "gcp", AccountID: p.ID, AccountName: &p.Name,
					Type: TypeComputeInterconnectAttachmentGroup, NativeID: iag.SelfLink, Name: &iag.Name,
					CreatedAt: strp(iag.CreationTimestamp), AttributesJSON: mustJSON(iag),
					DiscoveredBy: scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
}
