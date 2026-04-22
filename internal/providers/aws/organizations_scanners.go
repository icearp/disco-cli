package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	"github.com/aws/aws-sdk-go-v2/service/organizations/types"
)

func init() {
	registerService(serviceEntry{
		name:   "aws:organizations",
		global: true,
		fn:     scanOrganizations,
	})
}

// scanOrganizations discovers the AWS Organizations structure — organization,
// roots, OUs, accounts, and service control policies. Only callable from the
// management account; AccessDenied from member accounts is treated as "not the
// payer" and silently skipped so member-account scans remain clean.
func scanOrganizations(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := organizations.NewFromConfig(acct.cfg)

	// The organization itself. A failure here implies we're not in the payer
	// account (or lack permission) — skip the whole service cleanly.
	descOrg, err := client.DescribeOrganization(ctx, &organizations.DescribeOrganizationInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied("organizations:DescribeOrganization", acct.ID, "", err)
		}
		return 0, 0, fmt.Errorf("organizations:DescribeOrganization: %w", err)
	}
	org := descOrg.Organization
	if org == nil {
		return 0, 0, nil
	}
	orgRes := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeOrganization,
		NativeID:       sv(org.Arn),
		Name:           org.Id,
		AttributesJSON: mustJSON(org),
		DiscoveredBy:   scanID,
	}
	orgID := store.ResourceID("aws", acct.ID, TypeOrganization, orgRes.NativeID)
	n, err := st.UpsertResources([]*store.Resource{orgRes})
	if err != nil {
		return 0, 0, fmt.Errorf("upsert organization: %w", err)
	}
	total++
	inserted += n

	// Closure pairs collected across the whole scan. Self-entry for org root.
	closurePairs := [][2]string{{orgID, orgID}}

	// Roots — the top-level containers under the organization.
	var rootIDs []string
	rootsPager := organizations.NewListRootsPaginator(client, &organizations.ListRootsInput{})
	for rootsPager.HasMorePages() {
		page, err := rootsPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("organizations:ListRoots", acct.ID, "", err)
			}
			return total, inserted, fmt.Errorf("organizations:ListRoots: %w", err)
		}
		var batch []*store.Resource
		for _, root := range page.Roots {
			arn := sv(root.Arn)
			id := sv(root.Id)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeOrganizationsOU,
				NativeID:       arn,
				Name:           root.Name,
				AttributesJSON: mustJSON(root),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
			rid := store.ResourceID("aws", acct.ID, TypeOrganizationsOU, arn)
			rootIDs = append(rootIDs, id)
			closurePairs = append(closurePairs, [2]string{rid, orgID})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert roots: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}

	// OU tree — walk recursively below each root. Map OU native id → ARN so the
	// accounts loop below can resolve an account's parent (which is identified
	// only by native id in ListParents).
	ouARNByNativeID := map[string]string{}
	rootsPager2 := organizations.NewListRootsPaginator(client, &organizations.ListRootsInput{})
	for rootsPager2.HasMorePages() {
		page, err := rootsPager2.NextPage(ctx)
		if err != nil {
			return total, inserted, fmt.Errorf("organizations:ListRoots (refetch): %w", err)
		}
		for _, root := range page.Roots {
			ouARNByNativeID[sv(root.Id)] = sv(root.Arn)
		}
	}

	for _, rootNativeID := range rootIDs {
		ouTotal, ouInserted, ouErr := walkOUs(ctx, client, acct, scanID, st, rootNativeID, ouARNByNativeID[rootNativeID], ouARNByNativeID, &closurePairs)
		if ouErr != nil {
			return total, inserted, ouErr
		}
		total += ouTotal
		inserted += ouInserted
	}

	// Accounts — each has one or more parents (root or OU). Use ListParents per
	// account to resolve the immediate containing OU.
	accountsPager := organizations.NewListAccountsPaginator(client, &organizations.ListAccountsInput{})
	for accountsPager.HasMorePages() {
		page, err := accountsPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("organizations:ListAccounts", acct.ID, "", err)
			}
			return total, inserted, fmt.Errorf("organizations:ListAccounts: %w", err)
		}
		var batch []*store.Resource
		for _, a := range page.Accounts {
			arn := sv(a.Arn)
			parentARN, err := firstParentARN(ctx, client, sv(a.Id), ouARNByNativeID)
			if err != nil {
				return total, inserted, err
			}
			if parentARN != "" {
				pid := store.ResourceID("aws", acct.ID, TypeOrganizationsOU, parentARN)
				accID := store.ResourceID("aws", acct.ID, TypeOrganizationsAccount, arn)
				closurePairs = append(closurePairs, [2]string{accID, pid})
			}
			status := string(a.Status)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeOrganizationsAccount,
				NativeID:       arn,
				Name:           a.Name,
				Status:         &status,
				CreatedAt:      tp(a.JoinedTimestamp),
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert org accounts: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}

	// SCPs — policies, no parent in the hierarchy. Resolver fills in targets.
	policyPager := organizations.NewListPoliciesPaginator(client, &organizations.ListPoliciesInput{
		Filter: types.PolicyTypeServiceControlPolicy,
	})
	for policyPager.HasMorePages() {
		page, err := policyPager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("organizations:ListPolicies", acct.ID, "", err)
			}
			return total, inserted, fmt.Errorf("organizations:ListPolicies: %w", err)
		}
		var batch []*store.Resource
		for _, p := range page.Policies {
			arn := sv(p.Arn)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeOrganizationsSCP,
				NativeID:       arn,
				Name:           p.Name,
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert SCPs: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}

	// Build the closure table now that every resource is in place.
	if err := st.BatchAddToHierarchyClosure(closurePairs); err != nil {
		return total, inserted, fmt.Errorf("org hierarchy closure: %w", err)
	}
	return total, inserted, nil
}

// walkOUs recursively walks children of parentNativeID, upserting each OU and
// accumulating closure pairs. parentARN is the ARN of the parent (root or OU)
// used to compute the parent's stable ResourceID.
func walkOUs(
	ctx context.Context,
	client *organizations.Client,
	acct *account,
	scanID string,
	st *store.Store,
	parentNativeID, parentARN string,
	arnByID map[string]string,
	closurePairs *[][2]string,
) (total, inserted int, err error) {
	pager := organizations.NewListOrganizationalUnitsForParentPaginator(client, &organizations.ListOrganizationalUnitsForParentInput{
		ParentId: &parentNativeID,
	})
	var children []types.OrganizationalUnit
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied("organizations:ListOrganizationalUnitsForParent", acct.ID, "", err)
			}
			return total, inserted, fmt.Errorf("organizations:ListOrganizationalUnitsForParent %s: %w", parentNativeID, err)
		}
		children = append(children, page.OrganizationalUnits...)
	}
	parentResID := store.ResourceID("aws", acct.ID, TypeOrganizationsOU, parentARN)

	var batch []*store.Resource
	for _, ou := range children {
		arn := sv(ou.Arn)
		arnByID[sv(ou.Id)] = arn
		r := &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeOrganizationsOU,
			NativeID:       arn,
			Name:           ou.Name,
			AttributesJSON: mustJSON(ou),
			DiscoveredBy:   scanID,
		}
		batch = append(batch, r)
		childResID := store.ResourceID("aws", acct.ID, TypeOrganizationsOU, arn)
		*closurePairs = append(*closurePairs, [2]string{childResID, parentResID})
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return total, inserted, fmt.Errorf("upsert OUs: %w", err)
		}
		total += len(batch)
		inserted += n
	}
	for _, ou := range children {
		t2, i2, err := walkOUs(ctx, client, acct, scanID, st, sv(ou.Id), sv(ou.Arn), arnByID, closurePairs)
		if err != nil {
			return total, inserted, err
		}
		total += t2
		inserted += i2
	}
	return total, inserted, nil
}

// firstParentARN resolves the first parent of an account via ListParents. The
// native parent id is mapped to its ARN via arnByID (populated during the OU
// walk). Returns "" if the account has no resolvable parent.
func firstParentARN(ctx context.Context, client *organizations.Client, accountID string, arnByID map[string]string) (string, error) {
	out, err := client.ListParents(ctx, &organizations.ListParentsInput{ChildId: &accountID})
	if err != nil {
		if isAccessDenied(err) {
			return "", nil
		}
		return "", fmt.Errorf("organizations:ListParents %s: %w", accountID, err)
	}
	for _, p := range out.Parents {
		if arn, ok := arnByID[sv(p.Id)]; ok {
			return arn, nil
		}
	}
	return "", nil
}
