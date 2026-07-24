package gcp

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"google.golang.org/api/certificatemanager/v1"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCertManagerCertificate, Service: "certificatemanager", Upstream: "certificatemanager.googleapis.com/Certificate"})
	registerType(restype.Descriptor{Type: TypeCertManagerMap, Service: "certificatemanager", Upstream: "certificatemanager.googleapis.com/CertificateMap", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCertManagerMapEntry, Service: "certificatemanager", Upstream: "certificatemanager.googleapis.com/CertificateMapEntry"})
	registerType(restype.Descriptor{Type: TypeCertManagerDNSAuth, Service: "certificatemanager", Upstream: "certificatemanager.googleapis.com/DnsAuthorization", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCertManagerIssuanceConfig, Service: "certificatemanager", Upstream: "certificatemanager.googleapis.com/CertificateIssuanceConfig", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCertManagerTrustConfig, Service: "certificatemanager", Upstream: "certificatemanager.googleapis.com/TrustConfig", Leaf: true})
	registerService(serviceEntry{
		name: "gcp:certificatemanager",
		fn:   scanCertificateManager,
	})
}

// scanCertificateManager discovers Certificate Manager resources at the
// `global` location: certificates, certificate maps, certificate-map entries
// (per-map fan-out), DNS authorizations, certificate-issuance configs, and
// trust configs. Per-location fan-out for regional cert resources is
// deferred — global dominates deployment and the regional surface is narrow
// today.
func scanCertificateManager(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := certificatemanager.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("certificatemanager client: %w", err)
	}
	return scanCertificateManagerWithClient(ctx, svc, p, st, scanID)
}

// scanCertificateManagerWithClient is the test seam for
// scanCertificateManager — takes the pre-built client directly so tests can
// point it at a fake server.
func scanCertificateManagerWithClient(ctx context.Context, svc *certificatemanager.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s/locations/global", p.ID)

	// Certificates.
	t, n, err := runPaginated(ctx, st, p, "certificatemanager:certificates.list",
		svc.Projects.Locations.Certificates.List(parent),
		func(page *certificatemanager.ListCertificatesResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.Certificates))
			for _, c := range page.Certificates {
				name := lastSegment(c.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeCertManagerCertificate,
					NativeID:       c.Name,
					Name:           &name,
					CreatedAt:      strp(c.CreateTime),
					AttributesJSON: mustJSON(c),
					DiscoveredBy:   scanID,
				})
			}
			return upsertWithProjClosure(p, st, batch)
		})
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Certificate maps + per-map entries. Nested after Certificates (above)
	// already proved the certificatemanager API enabled for this project —
	// classify once via a manual Pages() call and discard rather than
	// escalate an isAPINotEnabled-shaped error to the whole-service disabled
	// sentinel (same discard pattern applied to every phase below).
	var mapNames []string
	cmerr := svc.Projects.Locations.CertificateMaps.List(parent).Pages(ctx, func(page *certificatemanager.ListCertificateMapsResponse) error {
		batch := make([]*store.Resource, 0, len(page.CertificateMaps))
		for _, m := range page.CertificateMaps {
			if m == nil || m.Name == "" {
				continue
			}
			mapNames = append(mapNames, m.Name)
			name := lastSegment(m.Name)
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeCertManagerMap,
				NativeID:       m.Name,
				Name:           &name,
				CreatedAt:      strp(m.CreateTime),
				AttributesJSON: mustJSON(m),
				DiscoveredBy:   scanID,
			})
		}
		pt, pn, perr := upsertWithProjClosure(p, st, batch)
		total += pt
		inserted += pn
		return perr
	})
	if cmerr != nil {
		if isPermissionDenied(cmerr) {
			_ = skipIfDenied(st, "certificatemanager:certificateMaps.list", p.ID, cmerr)
		} else {
			return total, inserted, cmerr
		}
	} else {
		// Per-map entries — sequential because map counts per project are small.
		for _, mn := range mapNames {
			eerr := svc.Projects.Locations.CertificateMaps.CertificateMapEntries.List(mn).Pages(ctx, func(epage *certificatemanager.ListCertificateMapEntriesResponse) error {
				ebatch := make([]*store.Resource, 0, len(epage.CertificateMapEntries))
				for _, e := range epage.CertificateMapEntries {
					if e == nil || e.Name == "" {
						continue
					}
					name := lastSegment(e.Name)
					ebatch = append(ebatch, &store.Resource{
						Provider:       "gcp",
						AccountID:      p.ID,
						AccountName:    &p.Name,
						Type:           TypeCertManagerMapEntry,
						NativeID:       e.Name,
						Name:           &name,
						CreatedAt:      strp(e.CreateTime),
						AttributesJSON: mustJSON(e),
						DiscoveredBy:   scanID,
					})
				}
				et, en, eerr := upsertWithParent(st, ebatch, store.ResourceID("gcp", p.ID, mn))
				total += et
				inserted += en
				return eerr
			})
			if eerr != nil {
				if isPermissionDenied(eerr) {
					_ = skipIfDenied(st, "certificatemanager:certificateMapEntries.list", p.ID, eerr)
				} else {
					return total, inserted, eerr
				}
			}
		}
	}

	// DNS authorizations.
	daerr := svc.Projects.Locations.DnsAuthorizations.List(parent).Pages(ctx, func(page *certificatemanager.ListDnsAuthorizationsResponse) error {
		batch := make([]*store.Resource, 0, len(page.DnsAuthorizations))
		for _, d := range page.DnsAuthorizations {
			if d == nil || d.Name == "" {
				continue
			}
			name := lastSegment(d.Name)
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeCertManagerDNSAuth,
				NativeID:       d.Name,
				Name:           &name,
				CreatedAt:      strp(d.CreateTime),
				AttributesJSON: mustJSON(d),
				DiscoveredBy:   scanID,
			})
		}
		dt, dn, derr := upsertWithProjClosure(p, st, batch)
		total += dt
		inserted += dn
		return derr
	})
	if daerr != nil {
		if isPermissionDenied(daerr) {
			_ = skipIfDenied(st, "certificatemanager:dnsAuthorizations.list", p.ID, daerr)
		} else {
			return total, inserted, daerr
		}
	}

	// Certificate issuance configs.
	icerr := svc.Projects.Locations.CertificateIssuanceConfigs.List(parent).Pages(ctx, func(page *certificatemanager.ListCertificateIssuanceConfigsResponse) error {
		batch := make([]*store.Resource, 0, len(page.CertificateIssuanceConfigs))
		for _, c := range page.CertificateIssuanceConfigs {
			if c == nil || c.Name == "" {
				continue
			}
			name := lastSegment(c.Name)
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeCertManagerIssuanceConfig,
				NativeID:       c.Name,
				Name:           &name,
				CreatedAt:      strp(c.CreateTime),
				AttributesJSON: mustJSON(c),
				DiscoveredBy:   scanID,
			})
		}
		it, in, ierr := upsertWithProjClosure(p, st, batch)
		total += it
		inserted += in
		return ierr
	})
	if icerr != nil {
		if isPermissionDenied(icerr) {
			_ = skipIfDenied(st, "certificatemanager:certificateIssuanceConfigs.list", p.ID, icerr)
		} else {
			return total, inserted, icerr
		}
	}

	// Trust configs.
	tcerr := svc.Projects.Locations.TrustConfigs.List(parent).Pages(ctx, func(page *certificatemanager.ListTrustConfigsResponse) error {
		batch := make([]*store.Resource, 0, len(page.TrustConfigs))
		for _, tc := range page.TrustConfigs {
			if tc == nil || tc.Name == "" {
				continue
			}
			name := lastSegment(tc.Name)
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeCertManagerTrustConfig,
				NativeID:       tc.Name,
				Name:           &name,
				CreatedAt:      strp(tc.CreateTime),
				AttributesJSON: mustJSON(tc),
				DiscoveredBy:   scanID,
			})
		}
		tt, tn, terr := upsertWithProjClosure(p, st, batch)
		total += tt
		inserted += tn
		return terr
	})
	if tcerr != nil {
		if isPermissionDenied(tcerr) {
			_ = skipIfDenied(st, "certificatemanager:trustConfigs.list", p.ID, tcerr)
		} else {
			return total, inserted, tcerr
		}
	}
	return total, inserted, nil
}
