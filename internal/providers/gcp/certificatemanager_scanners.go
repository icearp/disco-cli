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

	// Certificate maps + per-map entries.
	t, n, err = runPaginated(ctx, st, p, "certificatemanager:certificateMaps.list",
		svc.Projects.Locations.CertificateMaps.List(parent),
		func(page *certificatemanager.ListCertificateMapsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.CertificateMaps))
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
			pt, pn, perr := upsertWithProjClosure(p, st, batch)
			if perr != nil {
				return pt, pn, perr
			}

			// Per-map entries — sequential because map counts per project are small.
			for _, mn := range mapNames {
				et, en, eerr := runPaginated(ctx, st, p, "certificatemanager:certificateMapEntries.list",
					svc.Projects.Locations.CertificateMaps.CertificateMapEntries.List(mn),
					func(epage *certificatemanager.ListCertificateMapEntriesResponse) (int, int, error) {
						ebatch := make([]*store.Resource, 0, len(epage.CertificateMapEntries))
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
							return 0, 0, nil
						}
						en, eer := st.UpsertResources(ebatch)
						if eer != nil {
							return 0, 0, eer
						}
						pairs := make([][2]string, 0, len(ebatch))
						for _, r := range ebatch {
							pairs = append(pairs, [2]string{
								store.ResourceID(r.Provider, r.AccountID, r.Type, r.NativeID),
								store.ResourceID(r.Provider, r.AccountID, TypeCertManagerMap, mn),
							})
						}
						return len(ebatch), en, st.BatchAddToHierarchyClosure(pairs)
					})
				pt += et
				pn += en
				if eerr != nil {
					return pt, pn, eerr
				}
			}
			return pt, pn, nil
		})
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// DNS authorizations.
	t, n, err = runPaginated(ctx, st, p, "certificatemanager:dnsAuthorizations.list",
		svc.Projects.Locations.DnsAuthorizations.List(parent),
		func(page *certificatemanager.ListDnsAuthorizationsResponse) (int, int, error) {
			batch := make([]*store.Resource, 0, len(page.DnsAuthorizations))
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
			return upsertWithProjClosure(p, st, batch)
		})
	total += t
	inserted += n
	return total, inserted, err
}
