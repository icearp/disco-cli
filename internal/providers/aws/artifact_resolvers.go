package aws

import (
	"encoding/json"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveArtifactCustomerAgreementOrg,
		EdgeDecl{TypeArtifactCustomerAgreement, TypeOrganization, store.RelAttachedTo},
	)
}

// resolveArtifactCustomerAgreementOrg wires customer-agreement → organization
// via the agreement's OrganizationArn (org-wide agreements). FK-safe: a
// standalone-account agreement (no OrganizationArn) or an unscanned org emits
// no edge.
func resolveArtifactCustomerAgreementOrg(acct *account, st *store.Store) error {
	orgSet, err := scannedIDSet(acct, st, TypeOrganization)
	if err != nil {
		return err
	}
	if len(orgSet) == 0 {
		return nil
	}
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeArtifactCustomerAgreement}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			OrganizationArn *string `json:"OrganizationArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		arn := sv(attrs.OrganizationArn)
		if arn == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, arn)
		if !orgSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return err
		}
	}
	return nil
}
