package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/certificatemanager/v1"
)

func init() {
	registerService(serviceEntry{name: "gcp:certificatemanager", fn: scanCertificateManager})
}

// scanCertificateManager discovers Certificate Manager resources at the
// `global` location: certificates, certificate maps, certificate-map entries
// (per-map fan-out), and DNS authorizations. Per-location fan-out (regional
// cert resources) is deferred — global is the dominant deployment scope and
// the regional surface is narrow today.
func scanCertificateManager(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := certificatemanager.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("certificatemanager client: %w", err)
	}
	parent := fmt.Sprintf("projects/%s/locations/global", p.ID)

	// Certificates.
	if err := svc.Projects.Locations.Certificates.List(parent).Pages(ctx, func(page *certificatemanager.ListCertificatesResponse) error {
		var batch []*store.Resource
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
		t, n, e := upsertWithProjClosure(p, st, batch)
		total += t
		inserted += n
		return e
	}); err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "certificatemanager:certificates.list", p.ID, err)
		}
		return 0, 0, err
	}

	// Certificate maps + per-map entries.
	if err := svc.Projects.Locations.CertificateMaps.List(parent).Pages(ctx, func(page *certificatemanager.ListCertificateMapsResponse) error {
		var batch []*store.Resource
		var mapNames []string
		for _, m := range page.CertificateMaps {
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
			mapNames = append(mapNames, m.Name)
		}
		t, n, e := upsertWithProjClosure(p, st, batch)
		total += t
		inserted += n
		if e != nil {
			return e
		}

		// Per-map entries — sequential because map counts per project are small.
		for _, mn := range mapNames {
			if err := svc.Projects.Locations.CertificateMaps.CertificateMapEntries.List(mn).Pages(ctx, func(epage *certificatemanager.ListCertificateMapEntriesResponse) error {
				var ebatch []*store.Resource
				for _, e := range epage.CertificateMapEntries {
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
				if len(ebatch) == 0 {
					return nil
				}
				en, eer := st.UpsertResources(ebatch)
				if eer != nil {
					return eer
				}
				total += len(ebatch)
				inserted += en
				// Closure: entry → parent map.
				pairs := make([][2]string, 0, len(ebatch))
				for _, r := range ebatch {
					pairs = append(pairs, [2]string{
						store.ResourceID(r.Provider, r.AccountID, r.Type, r.NativeID),
						store.ResourceID(r.Provider, r.AccountID, TypeCertManagerMap, mn),
					})
				}
				return st.BatchAddToHierarchyClosure(pairs)
			}); err != nil {
				if isPermissionDenied(err) {
					_ = skipIfDenied(st, "certificatemanager:certificateMapEntries.list", p.ID, err)
					continue
				}
				return err
			}
		}
		return nil
	}); err != nil {
		if isPermissionDenied(err) {
			return total, inserted, skipIfDenied(st, "certificatemanager:certificateMaps.list", p.ID, err)
		}
		return total, inserted, err
	}

	// DNS authorizations.
	if err := svc.Projects.Locations.DnsAuthorizations.List(parent).Pages(ctx, func(page *certificatemanager.ListDnsAuthorizationsResponse) error {
		var batch []*store.Resource
		for _, d := range page.DnsAuthorizations {
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
		t, n, e := upsertWithProjClosure(p, st, batch)
		total += t
		inserted += n
		return e
	}); err != nil {
		if isPermissionDenied(err) {
			return total, inserted, skipIfDenied(st, "certificatemanager:dnsAuthorizations.list", p.ID, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}
