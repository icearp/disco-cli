package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		noopOrgHierarchy,
		EdgeDecl{TypeOrganization, TypeOrganizationsRoot, store.RelContains},
		EdgeDecl{TypeOrganizationsRoot, TypeOrganizationsOU, store.RelContains},
		EdgeDecl{TypeOrganizationsRoot, TypeOrganizationsAccount, store.RelContains},
		EdgeDecl{TypeOrganizationsOU, TypeOrganizationsOU, store.RelContains},
		EdgeDecl{TypeOrganizationsOU, TypeOrganizationsAccount, store.RelContains},
	)
	registerResolver(
		resolveOrganizationsSCPTargets,
		EdgeDecl{TypeOrganizationsSCP, TypeOrganizationsRoot, store.RelAttachedTo},
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
		EdgeDecl{TypeOrganization, TypeIAMAccount, store.RelAttachedTo},
	)
}

// noopOrgHierarchy only declares the org tree's contains edges;
// scanOrganizations records them through RecordHierarchyBatch.
func noopOrgHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveOrganizationsSCPTargets attaches each SCP to the roots, OUs, and
// accounts it applies to. ListTargetsForPolicy returns native ids (r-*/ou-*/
// 12-digit account); translate each to the scanner's ARN-keyed ResourceID via
// an index rebuilt from the store.
func resolveOrganizationsSCPTargets(acct *account, st *store.Store) error {
	scps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
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

	arnByID, _, err := loadOrgTargetIndex(acct, st)
	if err != nil {
		return err
	}

	client := organizations.NewFromConfig(acct.cfg)
	ctx := context.Background()
	// Organizations supports per-policy resource permissions, so each SCP is
	// tried; the denials share one warning so they cannot flood the cap.
	var denied skipTally
	defer denied.warnDenied(st, "organizations:ListTargetsForPolicy", acct.ID, "SCP(s)")
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
					denied.skipped(scp.NativeID, err)
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
				targetResID := store.ResourceID("aws", acct.ID, arn)
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
	for _, t := range []string{TypeOrganizationsRoot, TypeOrganizationsOU, TypeOrganizationsAccount} {
		rs, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"},
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
// organization to each delegated-admin account, with delegated service
// principals in the edge attributes. org→account `contains` already lives in
// the closure table; this adds a distinct relationship for privilege-scoped
// queries ("which accounts admin which services?"). Uniqueness is (from, to,
// kind), so both edges coexist.
func resolveOrganizationsDelegatedAdmins(acct *account, st *store.Store) error {
	orgs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
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
				return skipIfAccessDenied(st, "organizations:ListDelegatedAdministrators", acct.ID, "", err)
			}
			return fmt.Errorf("organizations:ListDelegatedAdministrators: %w", err)
		}
		for _, a := range page.DelegatedAdministrators {
			if a.Id != nil {
				admins = append(admins, *a.Id)
			}
		}
	}

	// Each admin account is tried; the denials share one warning so they
	// cannot flood the cap.
	var deniedAdmins skipTally
	defer deniedAdmins.warnDenied(st, "organizations:ListDelegatedServicesForAccount", acct.ID, "delegated admin account(s)")
	for _, adminID := range admins {
		var services []string
		denied := false
		svcPager := organizations.NewListDelegatedServicesForAccountPaginator(client,
			&organizations.ListDelegatedServicesForAccountInput{AccountId: &adminID})
		for svcPager.HasMorePages() {
			page, err := svcPager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					deniedAdmins.skipped(adminID, err)
					denied = true
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

		// Scanner keys accounts by ARN, not raw 12-digit ID — look up via the store-built index.
		acctARN, ok := arnByID[adminID]
		if !ok {
			continue
		}
		acctResID := store.ResourceID("aws", acct.ID, acctARN)
		// A denied read keeps the edge, which ListDelegatedAdministrators proved,
		// but stores no DelegatedServices: a list, even an empty one, would claim
		// to know which services the account administers.
		var attrs *string
		if !denied {
			attrJSON := mustJSON(map[string]any{"DelegatedServices": services})
			attrs = &attrJSON
		}
		if err := st.UpsertRelationship(org.ID, acctResID, store.RelAttachedTo, "directed", attrs); err != nil {
			return fmt.Errorf("upsert org→delegated-admin: %w", err)
		}
	}
	return nil
}

// resolveOrganizationsManagementAccount links each org row to the AWS account
// identified by Organization.MasterAccountID. If the master account row is
// already in the store (management-account scan), the edge points at the real
// aws:organizations:account row. Otherwise (member-account scan, master not
// scanned this run) it insert-if-absents an empty-attribute aws:iam:account
// placeholder at the master account ID — the same self-node natural key the
// IAM scanner / cross-account-trust resolver use — so a later management scan
// version-populates it.
func resolveOrganizationsManagementAccount(acct *account, st *store.Store) error {
	orgs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
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
	// An org row that does not decode is reported at the end so that it costs
	// the others none of their edges.
	var decodeErrs []error
	for _, org := range orgs {
		var attrs struct {
			MasterAccountID *string `json:"MasterAccountId"`
		}
		if err := json.Unmarshal([]byte(org.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeOrganization, org.NativeID, err))
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
		return errors.Join(decodeErrs...)
	}
	if len(stubByAcct) > 0 {
		placeholders := make([]*store.Resource, 0, len(stubByAcct))
		for other := range stubByAcct {
			placeholders = append(placeholders, iamAccountPlaceholder(acct.partition(), other, orgs[0].DiscoveredBy))
		}
		if _, err := st.InsertResourcesIfAbsent(placeholders); err != nil {
			return fmt.Errorf("insert master-account placeholders: %w", err)
		}
	}
	edgeAttrs := mustJSON(map[string]string{"role": "management"})
	for _, p := range pendings {
		var toID string
		if p.realAccount {
			toID = store.ResourceID("aws", acct.ID, arnByID[p.masterAcctID])
		} else {
			toID = store.ResourceID("aws", p.masterAcctID, iamAccountNativeID(acct.partition(), p.masterAcctID))
		}
		if err := st.UpsertRelationship(p.fromID, toID, store.RelAttachedTo, "directed", &edgeAttrs); err != nil {
			return fmt.Errorf("upsert org→management-account relationship: %w", err)
		}
	}
	return errors.Join(decodeErrs...)
}

// extractPolicyID pulls the p-xxxx identifier out of an SCP ARN of the form
// arn:{p}:organizations::ACCOUNT:policy/ORGID/service_control_policy/p-xxxx.
func extractPolicyID(arn string) *string {
	idx := strings.LastIndex(arn, "/")
	if idx < 0 || idx == len(arn)-1 {
		return nil
	}
	id := arn[idx+1:]
	return &id
}
