package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveACMCertificateRelationships,
		EdgeDecl{TypeACMCertificate, TypeACMPrivateCA, store.RelUses},
	)
}

// resolveACMCertificateRelationships links each private ACM certificate to its
// issuing Private CA. Public (AMAZON_ISSUED) and IMPORTED certificates have no
// CertificateAuthorityArn.
func resolveACMCertificateRelationships(acct *account, st *store.Store) error {
	certs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeACMCertificate},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range certs {
		var attrs struct {
			CertificateAuthorityArn *string `json:"CertificateAuthorityArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || sv(attrs.CertificateAuthorityArn) == "" {
			continue
		}
		caID := store.ResourceID("aws", acct.ID, TypeACMPrivateCA, *attrs.CertificateAuthorityArn)
		if err := st.UpsertRelationship(r.ID, caID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert acm-cert→private-ca: %w", err)
		}
	}
	return nil
}
