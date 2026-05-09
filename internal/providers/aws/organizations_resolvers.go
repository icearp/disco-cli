package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
)

func init() {
	registerResolver(
		resolveOrganizationsSCPTargets,
		EdgeDecl{TypeOrganizationsSCP, TypeOrganizationsOU, store.RelAttachedTo},
		EdgeDecl{TypeOrganizationsSCP, TypeOrganizationsAccount, store.RelAttachedTo},
	)
	registerResolver(
		resolveOrganizationsDelegatedAdmins,
		EdgeDecl{TypeOrganization, TypeOrganizationsAccount, store.RelAttachedTo},
	)
	registerResolver(
		resolveOrganizationsManagementAccount,
		EdgeDecl{TypeOrganization, TypeOrganizationsAccount, store.RelAttachedTo},
		EdgeDecl{TypeOrganization, TypeIAMForeignAccount, store.RelAttachedTo},
	)
}

// resolveOrganizationsSCPTargets attaches each SCP to the roots, OUs, and
// accounts it applies to. ListTargetsForPolicy returns native ids
// (r-*/ou-*/12-digit account); translate each back to the stable ResourceID
// the scanner used (keyed by ARN) via an index rebuilt from the store.
func resolveOrganizationsSCPTargets(acct *account, st *store.Store) error {
	scps, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeOrganizationsSCP},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(scps) == 0 {
		return nil
	}

	arnByID, typeByID, err := loadOrgTargetIndex(acct, st)
	if err != nil {
		return err
	}

	client := organizations.NewFromConfig(acct.cfg)
	ctx := context.Background()
	for _, scp := range scps {
		policyID := extractPolicyID(scp.NativeID)
		if policyID == nil {
			continue
		}
		pager := organizations.NewListTargetsForPolicyPaginator(client, &organizations.ListTargetsForPolicyInput{
			PolicyId: policyID,
		})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					break
				}
				return fmt.Errorf("organizations:ListTargetsForPolicy %s: %w", scp.NativeID, err)
			}
			for _, tgt := range page.Targets {
				id := sv(tgt.TargetId)
				arn, ok := arnByID[id]
				if !ok {
					continue
				}
				targetResID := store.ResourceID("aws", acct.ID, typeByID[id], arn)
				if err := st.UpsertRelationship(scp.ID, targetResID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert scp→target: %w", err)
				}
			}
		}
	}
	return nil
}

// loadOrgTargetIndex returns two maps keyed by Organizations-native id
// (r-*, ou-*, or 12-digit account): the ARN and the disco type.
func loadOrgTargetIndex(acct *account, st *store.Store) (arnByID, typeByID map[string]string, err error) {
	arnByID = map[string]string{}
	typeByID = map[string]string{}
	for _, t := range []string{TypeOrganizationsOU, TypeOrganizationsAccount} {
		rs, err := st.ListResources(store.ResourceFilter{
			Provider:  "aws",
			AccountID: acct.ID,
			Types:     []string{t},
			Limit:     util.AllResources,
		})
		if err != nil {
			return nil, nil, err
		}
		for _, r := range rs {
			var a struct {
				ID *string `json:"ID"`
			}
			if jerr := json.Unmarshal([]byte(r.AttributesJSON), &a); jerr != nil || a.ID == nil {
				continue
			}
			arnByID[*a.ID] = r.NativeID
			typeByID[*a.ID] = r.Type
		}
	}
	return arnByID, typeByID, nil
}

// resolveOrganizationsDelegatedAdmins emits an `attached-to` edge from the
// organization to each delegated-admin account, with the delegated service
// principals captured in the edge attributes. The hierarchy org→account
// (contains) already lives in the closure table; this adds a distinct
// relationship for privilege-scoped queries ("which accounts admin which
// services?"). Relationship uniqueness is (from, to, kind), so the two
// edges coexist without conflict.
func resolveOrganizationsDelegatedAdmins(acct *account, st *store.Store) error {
	orgs, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeOrganization},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(orgs) == 0 {
		return nil
	}
	org := orgs[0]

	arnByID, _, err := loadOrgTargetIndex(acct, st)
	if err != nil {
		return err
	}

	client := organizations.NewFromConfig(acct.cfg)
	ctx := context.Background()

	var admins []string
	adminPager := organizations.NewListDelegatedAdministratorsPaginator(client,
		&organizations.ListDelegatedAdministratorsInput{})
	for adminPager.HasMorePages() {
		page, err := adminPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil
			}
			return fmt.Errorf("organizations:ListDelegatedAdministrators: %w", err)
		}
		for _, a := range page.DelegatedAdministrators {
			if a.Id != nil {
				admins = append(admins, *a.Id)
			}
		}
	}

	for _, adminID := range admins {
		var services []string
		svcPager := organizations.NewListDelegatedServicesForAccountPaginator(client,
			&organizations.ListDelegatedServicesForAccountInput{AccountId: &adminID})
		for svcPager.HasMorePages() {
			page, err := svcPager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					break
				}
				return fmt.Errorf("organizations:ListDelegatedServicesForAccount %s: %w", adminID, err)
			}
			for _, s := range page.DelegatedServices {
				if s.ServicePrincipal != nil {
					services = append(services, *s.ServicePrincipal)
				}
			}
		}

		// Scanner stores accounts keyed by ARN, not the raw 12-digit ID —
		// look up via the index built from the store.
		acctARN, ok := arnByID[adminID]
		if !ok {
			continue
		}
		acctResID := store.ResourceID("aws", acct.ID, TypeOrganizationsAccount, acctARN)
		attrJSON := mustJSON(map[string]any{"DelegatedServices": services})
		if err := st.UpsertRelationship(org.ID, acctResID, store.RelAttachedTo, "directed", &attrJSON); err != nil {
			return fmt.Errorf("upsert org→delegated-admin: %w", err)
		}
	}
	return nil
}

// resolveOrganizationsManagementAccount links each org row to the AWS account
// identified by Organization.MasterAccountID. When the master account row is
// already in the store (management-account scan), the edge points to the real
// aws:organizations:account row. Otherwise (member-account scan, master not
// scanned by this run) the resolver synthesizes an aws:iam:foreign-account
// stub for the master account ID and points at it. The stub uses the same
// shape as the IAM cross-account-trust resolver, so a later management scan
// that upserts the real account row leaves the stub intact in its own
// account-scoped slice.
func resolveOrganizationsManagementAccount(acct *account, st *store.Store) error {
	orgs, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeOrganization},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(orgs) == 0 {
		return nil
	}
	arnByID, _, err := loadOrgTargetIndex(acct, st)
	if err != nil {
		return err
	}
	type pending struct {
		fromID, masterAcctID string
		realAccount          bool
	}
	var pendings []pending
	stubByAcct := map[string]struct{}{}
	for _, org := range orgs {
		var attrs struct {
			MasterAccountID *string `json:"MasterAccountId"`
		}
		if jerr := json.Unmarshal([]byte(org.AttributesJSON), &attrs); jerr != nil {
			continue
		}
		master := sv(attrs.MasterAccountID)
		if master == "" {
			continue
		}
		if _, ok := arnByID[master]; ok {
			pendings = append(pendings, pending{fromID: org.ID, masterAcctID: master, realAccount: true})
			continue
		}
		stubByAcct[master] = struct{}{}
		pendings = append(pendings, pending{fromID: org.ID, masterAcctID: master, realAccount: false})
	}
	if len(pendings) == 0 {
		return nil
	}
	if len(stubByAcct) > 0 {
		stubs := make([]*store.Resource, 0, len(stubByAcct))
		for other := range stubByAcct {
			nativeID := fmt.Sprintf("arn:aws:iam::%s:root", other)
			name := other
			stubs = append(stubs, &store.Resource{
				Provider:       "aws",
				AccountID:      other,
				Type:           TypeIAMForeignAccount,
				NativeID:       nativeID,
				Name:           &name,
				AttributesJSON: fmt.Sprintf(`{"AccountId":%q,"Synthetic":true}`, other),
				DiscoveredBy:   orgs[0].DiscoveredBy,
			})
		}
		if _, err := st.UpsertResources(stubs); err != nil {
			return fmt.Errorf("upsert org master-account stubs: %w", err)
		}
	}
	edgeAttrs := mustJSON(map[string]string{"role": "management"})
	for _, p := range pendings {
		var toID string
		if p.realAccount {
			toID = store.ResourceID("aws", acct.ID, TypeOrganizationsAccount, arnByID[p.masterAcctID])
		} else {
			toID = store.ResourceID("aws", p.masterAcctID, TypeIAMForeignAccount,
				fmt.Sprintf("arn:aws:iam::%s:root", p.masterAcctID))
		}
		if err := st.UpsertRelationship(p.fromID, toID, store.RelAttachedTo, "directed", &edgeAttrs); err != nil {
			return fmt.Errorf("upsert org→management-account relationship: %w", err)
		}
	}
	return nil
}

// extractPolicyID pulls the p-xxxx identifier out of an SCP ARN of the form
// arn:aws:organizations::ACCOUNT:policy/ORGID/service_control_policy/p-xxxx.
func extractPolicyID(arn string) *string {
	idx := strings.LastIndex(arn, "/")
	if idx < 0 || idx == len(arn)-1 {
		return nil
	}
	id := arn[idx+1:]
	return &id
}
