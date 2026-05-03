package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveLicenseManagerGrantToLicense,
		EdgeDecl{TypeLicenseManagerGrant, TypeLicenseManagerLicense, store.RelAttachedTo},
	)
}

// resolveLicenseManagerGrantToLicense wires each grant to its source license
// via LicenseArn.
func resolveLicenseManagerGrantToLicense(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeLicenseManagerGrant}, Limit: util.AllResources,
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
		tgtID := store.ResourceID("aws", acct.ID, TypeLicenseManagerLicense, l)
		if !licSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert lm grant→license: %w", err)
		}
	}
	return nil
}
