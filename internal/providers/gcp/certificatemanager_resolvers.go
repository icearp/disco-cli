package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolveCertificateManagerRelationships) }

// resolveCertificateManagerRelationships derives two edge classes:
//
//  1. certificateMapEntry -[uses]-> certificate (one edge per entry, capped
//     at 4 by API contract).
//  2. targetHttpsProxy -[uses]-> certificateMap via the proxy's
//     `certificateMap` field.
//
// Certificate names match the SDK certificates[] entries verbatim
// (`projects/*/locations/*/certificates/*`), so lookup is a direct NativeID
// match; targetHttpsProxy.certificateMap likewise stores the full resource
// name. Cross-project / unscanned-resource references skipped.
//
// Deferred:
//   - DNS authorization → managed certificate edge: ManagedCertificate.dnsAuthorizations[]
//     references DNS auths but is on the certificate side; same-pattern
//     follow-up, lower priority than the map-entry chain.
//   - targetSslProxy / targetHttpsProxy older-surface SslCertificate (compute
//     SslCertificates resource) — separate older API not yet scanned.
func resolveCertificateManagerRelationships(p *project, st *store.Store) error {
	certs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeCertManagerCertificate},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	certIDByNative := make(map[string]string, len(certs))
	for _, c := range certs {
		certIDByNative[c.NativeID] = c.ID
	}

	// MapEntry → certificate.
	if len(certIDByNative) > 0 {
		entries, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeCertManagerMapEntry},
			Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, e := range entries {
			var a struct {
				Certificates []string `json:"certificates"`
			}
			if err := json.Unmarshal([]byte(e.AttributesJSON), &a); err != nil {
				continue
			}
			for _, certName := range a.Certificates {
				certID, ok := certIDByNative[certName]
				if !ok {
					continue
				}
				if err := st.UpsertRelationship(e.ID, certID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert mapEntry→certificate: %w", err)
				}
			}
		}
	}

	// targetHttpsProxy → certificateMap.
	maps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeCertManagerMap},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(maps) == 0 {
		return nil
	}
	mapIDByNative := make(map[string]string, len(maps))
	for _, m := range maps {
		mapIDByNative[m.NativeID] = m.ID
	}
	proxies, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeTargetHTTPSProxy},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, pr := range proxies {
		var a struct {
			CertificateMap string `json:"certificateMap"`
		}
		if err := json.Unmarshal([]byte(pr.AttributesJSON), &a); err != nil {
			continue
		}
		mapID, ok := mapIDByNative[a.CertificateMap]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(pr.ID, mapID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert targetHttpsProxy→certificateMap: %w", err)
		}
	}
	return nil
}
