package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveCertificateManagerRelationships,
		EdgeDecl{TypeCertManagerMapEntry, TypeCertManagerCertificate, store.RelUses},
		EdgeDecl{TypeComputeTargetHTTPSProxy, TypeCertManagerMap, store.RelUses},
	)
	registerResolver(resolveCertificateRelationships,
		EdgeDecl{TypeCertManagerCertificate, TypeCertManagerDNSAuth, store.RelUses},
		EdgeDecl{TypeCertManagerCertificate, TypeCertManagerIssuanceConfig, store.RelUses},
	)
}

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
//   - Certificate.usedBy[]: reverse-direction pointer (mapEntry/proxy → cert),
//     already covered forward by mapEntry.certificates[] above; its Name is
//     also AIP-122 full-resource-name-with-`//service/`-prefix, a different
//     format than every other field this file matches on.
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

// resolveCertificateRelationships derives certificate -[uses]-> dnsAuthorization
// (one edge per Managed.DNSAuthorizations[] entry) and certificate
// -[uses]-> certificateIssuanceConfig (Managed.IssuanceConfig, private-PKI
// certs only). Both fields store the full resource name verbatim, matched
// directly against the target's NativeID.
func resolveCertificateRelationships(p *project, st *store.Store) error {
	certs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeCertManagerCertificate},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(certs) == 0 {
		return nil
	}

	dnsAuths, err := scannedIDSet(p, st, TypeCertManagerDNSAuth)
	if err != nil {
		return err
	}
	issuanceConfigs, err := scannedIDSet(p, st, TypeCertManagerIssuanceConfig)
	if err != nil {
		return err
	}

	for _, c := range certs {
		var a struct {
			Managed *struct {
				DNSAuthorizations []string `json:"dnsAuthorizations"`
				IssuanceConfig    string   `json:"issuanceConfig"`
			} `json:"managed"`
		}
		if err := json.Unmarshal([]byte(c.AttributesJSON), &a); err != nil || a.Managed == nil {
			continue
		}
		for _, dnsAuthName := range a.Managed.DNSAuthorizations {
			if err := upsertIfScanned(st, dnsAuths, c.ID, "gcp", p.ID, TypeCertManagerDNSAuth, dnsAuthName, store.RelUses); err != nil {
				return fmt.Errorf("upsert certificate→dnsAuthorization: %w", err)
			}
		}
		if a.Managed.IssuanceConfig != "" {
			if err := upsertIfScanned(st, issuanceConfigs, c.ID, "gcp", p.ID, TypeCertManagerIssuanceConfig, a.Managed.IssuanceConfig, store.RelUses); err != nil {
				return fmt.Errorf("upsert certificate→issuanceConfig: %w", err)
			}
		}
	}
	return nil
}
