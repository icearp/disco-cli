package gcp

import (
	"encoding/json"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// Resolver Wave R8 of the resolver-implementation backlog (ROADMAP.md
// "Resolver buildout"): the first non-compute wave. CryptoKey's own edges
// (primary version, external backend) and KeyHandle's provisioned-key edge —
// both read straight off already-scanned AttributesJSON.
func init() {
	registerResolver(resolveCryptoKeyRelationships,
		EdgeDecl{TypeKMSCryptoKey, TypeKMSCryptoKeyVersion, store.RelUses},
		EdgeDecl{TypeKMSCryptoKey, TypeKMSEkmConnection, store.RelUses},
		EdgeDecl{TypeKMSCryptoKey, TypeKMSSingleTenantHsmInstance, store.RelUses},
	)
	registerResolver(resolveKeyHandleRelationships,
		EdgeDecl{TypeKMSKeyHandle, TypeKMSCryptoKey, store.RelUses},
	)
	registerResolver(resolveCryptoKeyVersionRelationships,
		EdgeDecl{TypeKMSCryptoKeyVersion, TypeKMSImportJob, store.RelUses},
	)
}

// resolveCryptoKeyRelationships wires CryptoKey -> its primary
// CryptoKeyVersion (`primary.name`) and -> the external backend hosting its
// key material (`cryptoKeyBackend`, an EkmConnection or
// SingleTenantHsmInstance resource name — the two are mutually exclusive per
// protection level, so a candidate search picks whichever is actually
// scanned).
func resolveCryptoKeyRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeKMSCryptoKey},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeKMSCryptoKeyVersion, TypeKMSEkmConnection, TypeKMSSingleTenantHsmInstance)
	if err != nil {
		return err
	}
	backendCandidates := []string{TypeKMSEkmConnection, TypeKMSSingleTenantHsmInstance}
	for _, r := range rows {
		var attrs struct {
			Primary *struct {
				Name string `json:"name"`
			} `json:"primary"`
			CryptoKeyBackend string `json:"cryptoKeyBackend"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Primary != nil {
			if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeKMSCryptoKeyVersion, attrs.Primary.Name, store.RelUses); err != nil {
				return err
			}
		}
		if err := upsertIfScannedAny(st, scanned, r.ID, "gcp", p.ID, backendCandidates, attrs.CryptoKeyBackend, store.RelUses); err != nil {
			return err
		}
	}
	return nil
}

// resolveCryptoKeyVersionRelationships wires CryptoKeyVersion -> the
// ImportJob used in its most recent import (`importJob`), when the key
// material was imported rather than generated in place.
func resolveCryptoKeyVersionRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeKMSCryptoKeyVersion},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeKMSImportJob)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ImportJob string `json:"importJob"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeKMSImportJob, attrs.ImportJob, store.RelUses); err != nil {
			return err
		}
	}
	return nil
}

// resolveKeyHandleRelationships wires KeyHandle -> the CryptoKey it
// provisioned (`kmsKey`).
func resolveKeyHandleRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeKMSKeyHandle},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeKMSCryptoKey)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KmsKey string `json:"kmsKey"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeKMSCryptoKey, attrs.KmsKey, store.RelUses); err != nil {
			return err
		}
	}
	return nil
}
