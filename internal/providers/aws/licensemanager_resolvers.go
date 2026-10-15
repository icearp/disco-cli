package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveLicenseManagerGrantToLicense,
		EdgeDecl{TypeLicenseManagerGrant, TypeLicenseManagerLicense, store.RelAttachedTo},
	)
	registerResolver(
		resolveLicenseManagerReceivedLicenseToLicense,
		EdgeDecl{TypeLicenseManagerReceivedLicense, TypeLicenseManagerLicense, store.RelAttachedTo},
	)
	registerResolver(
		resolveLicenseManagerTokenRelationships,
		EdgeDecl{TypeLicenseManagerToken, TypeLicenseManagerLicense, store.RelUses},
		EdgeDecl{TypeLicenseManagerToken, TypeLicenseManagerReceivedLicense, store.RelUses},
		EdgeDecl{TypeLicenseManagerToken, TypeIAMRole, store.RelUses},
	)
}

// resolveLicenseManagerGrantToLicense wires each grant to its source license
// via LicenseArn.
func resolveLicenseManagerGrantToLicense(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeLicenseManagerGrant}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	licSet, err := scannedIDSet(acct, st, TypeLicenseManagerLicense)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			LicenseArn *string `json:"LicenseArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		l := sv(attrs.LicenseArn)
		if l == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, l)
		if !licSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert lm grant→license: %w", err)
		}
	}
	return nil
}

// lmLicenseRefAttrs is the part of a received license (GrantedLicense) or a
// token (TokenData) the resolvers read.
type lmLicenseRefAttrs struct {
	LicenseArn *string  `json:"LicenseArn"`
	RoleArns   []string `json:"RoleArns"`
}

// listLMLicenseRefRows lists the rows of rtype in acct with their decoded
// license references.
func listLMLicenseRefRows(acct *account, st *store.Store, rtype string) ([]store.Resource, []lmLicenseRefAttrs, error) {
	rows, err := listAccountRows(acct, st, rtype)
	if err != nil {
		return nil, nil, err
	}
	attrs := make([]lmLicenseRefAttrs, len(rows))
	for i, r := range rows {
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs[i]); err != nil {
			return nil, nil, fmt.Errorf("decode %s %s attributes: %w", rtype, r.NativeID, err)
		}
	}
	return rows, attrs, nil
}

// upsertIfScanned links from to the resource ref names in acct when that
// resource was scanned.
func upsertIfScanned(acct *account, st *store.Store, scanned map[string]bool, fromID, ref, kind, label string) error {
	if ref == "" {
		return nil
	}
	tgtID := store.ResourceID("aws", acct.ID, ref)
	if !scanned[tgtID] {
		return nil
	}
	if err := st.UpsertRelationship(fromID, tgtID, kind, "directed", nil); err != nil {
		return fmt.Errorf("upsert %s: %w", label, err)
	}
	return nil
}

// resolveLicenseManagerReceivedLicenseToLicense wires a received license to
// the license it was granted from when this account issued that license too
// (a self-grant); a license issued by another account is not a row here.
func resolveLicenseManagerReceivedLicenseToLicense(acct *account, st *store.Store) error {
	rows, attrs, err := listLMLicenseRefRows(acct, st, TypeLicenseManagerReceivedLicense)
	if err != nil || len(rows) == 0 {
		return err
	}
	licSet, err := scannedIDSet(acct, st, TypeLicenseManagerLicense)
	if err != nil {
		return err
	}
	for i, r := range rows {
		if err := upsertIfScanned(acct, st, licSet, r.ID, sv(attrs[i].LicenseArn), store.RelAttachedTo, "lm received-license→license"); err != nil {
			return err
		}
	}
	return nil
}

// resolveLicenseManagerTokenRelationships wires each token to its license and
// to the IAM roles embedded in it, where those were scanned in this account.
// CreateToken is called by the license's consumer, so the license is usually
// another account's and is a row here only as this account's received
// license; the license row itself exists only when the account issued it.
func resolveLicenseManagerTokenRelationships(acct *account, st *store.Store) error {
	rows, attrs, err := listLMLicenseRefRows(acct, st, TypeLicenseManagerToken)
	if err != nil || len(rows) == 0 {
		return err
	}
	licSet, err := scannedIDSet(acct, st, TypeLicenseManagerLicense)
	if err != nil {
		return err
	}
	receivedSet, err := scannedIDSet(acct, st, TypeLicenseManagerReceivedLicense)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for i, r := range rows {
		licenseArn := sv(attrs[i].LicenseArn)
		if err := upsertIfScanned(acct, st, licSet, r.ID, licenseArn, store.RelUses, "lm token→license"); err != nil {
			return err
		}
		if licenseArn != "" {
			if err := upsertIfScanned(acct, st, receivedSet, r.ID, lmReceivedLicenseNativeID(licenseArn), store.RelUses, "lm token→received-license"); err != nil {
				return err
			}
		}
		for _, role := range attrs[i].RoleArns {
			if err := upsertIfScanned(acct, st, roleSet, r.ID, role, store.RelUses, "lm token→iam-role"); err != nil {
				return err
			}
		}
	}
	return nil
}
