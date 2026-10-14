package aws

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveACMCertificateRelationships,
		EdgeDecl{TypeACMCertificate, TypeACMPrivateCA, store.RelUses},
	)
	registerResolver(
		resolveACMAcmeExternalAccountBindingRoles,
		EdgeDecl{TypeACMAcmeExternalAccountBinding, TypeIAMRole, store.RelUses},
	)
	registerResolver(
		resolveACMAcmeAccountBindings,
		EdgeDecl{TypeACMAcmeAccount, TypeACMAcmeExternalAccountBinding, store.RelUses},
	)
	registerResolver(
		noopACMHierarchy,
		EdgeDecl{TypeACMAcmeEndpoint, TypeACMAcmeExternalAccountBinding, store.RelContains},
		EdgeDecl{TypeACMAcmeEndpoint, TypeACMAcmeAccount, store.RelContains},
		EdgeDecl{TypeACMAcmeEndpoint, TypeACMAcmeDomainValidation, store.RelContains},
	)
}

// noopACMHierarchy emits nothing: the ACME endpoint → external account
// binding, ACME account and domain validation contains edges are written at
// scan time by childFanOut (the endpoint-child phases of scanACM). It is
// registered only so those edges are declared to coverage tooling.
func noopACMHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveACMAcmeAccountBindings links each ACME account to the external
// account binding it registered with; the account keeps its issuance
// authority after the binding is revoked. A row whose attributes do not parse
// is reported in the returned error after the remaining rows are wired.
func resolveACMAcmeAccountBindings(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeACMAcmeAccount},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	bindingSet, err := scannedIDSet(acct, st, TypeACMAcmeExternalAccountBinding)
	if err != nil {
		return err
	}
	var bad []error
	for _, r := range rows {
		var attrs struct {
			AcmeExternalAccountBindingArn *string `json:"AcmeExternalAccountBindingArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			bad = append(bad, fmt.Errorf("acm acme account %s attributes: %w", r.NativeID, err))
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, sv(attrs.AcmeExternalAccountBindingArn))
		if !bindingSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert acm acme-account→eab: %w", err)
		}
	}
	return errors.Join(bad...)
}

// resolveACMAcmeExternalAccountBindingRoles links each ACME external account
// binding to the IAM role that authorizes its ACME clients. A row whose
// attributes do not parse is reported in the returned error after the
// remaining rows are wired.
func resolveACMAcmeExternalAccountBindingRoles(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeACMAcmeExternalAccountBinding},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	var bad []error
	for _, r := range rows {
		var attrs struct {
			RoleArn *string `json:"RoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			bad = append(bad, fmt.Errorf("acm acme external account binding %s attributes: %w", r.NativeID, err))
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, sv(attrs.RoleArn))
		if !roleSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert acm acme-eab→iam-role: %w", err)
		}
	}
	return errors.Join(bad...)
}

// resolveACMCertificateRelationships links each private ACM certificate to its
// issuing Private CA. Public (AMAZON_ISSUED) and IMPORTED certificates have no
// CertificateAuthorityArn.
func resolveACMCertificateRelationships(acct *account, st *store.Store) error {
	certs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeACMCertificate},
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
		caID := store.ResourceID("aws", acct.ID, *attrs.CertificateAuthorityArn)
		if err := st.UpsertRelationship(r.ID, caID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert acm-cert→private-ca: %w", err)
		}
	}
	return nil
}
