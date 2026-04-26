package aws

import (
	"context"
	"fmt"
	"sync"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/identitystore"
	istypes "github.com/aws/aws-sdk-go-v2/service/identitystore/types"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
	ssotypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerService(serviceEntry{name: "aws:sso-admin", fn: scanSSOAdmin})
}

// scanSSOAdmin discovers IAM Identity Center (SSO) instances, permission
// sets, account-assignments, and the connected Identity Store's users +
// groups. Scoped per region: ListInstances returns instances reachable
// from the calling region only, so empty regions short-circuit immediately
// and the home region of the org's instance is the one that does work.
//
// Phases (each tolerates AccessDenied via skipIfAccessDenied without
// barring later phases — non-management accounts hit AccessDenied on
// every SSO admin op):
//  1. ListInstances → upsert TypeSSOInstance.
//  2. Per instance: ListPermissionSets + DescribePermissionSet fan-out.
//  3. Per (instance, permission-set): ListAccountsForProvisionedPermissionSet
//     → for each account, ListAccountAssignments. Each AccountAssignment
//     row becomes a TypeSSOAccountAssignment with synthesized NativeID
//     (assignments have no AWS-issued ARN — see aws/CLAUDE.md).
//  4. Per instance: ListUsers + ListGroups against the connected Identity
//     Store (instance.IdentityStoreId).
func scanSSOAdmin(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	ssoClient := ssoadmin.NewFromConfig(acct.cfg, func(o *ssoadmin.Options) { o.Region = region })

	instances, t, i, ferr := scanSSOInstances(ctx, ssoClient, acct, region, st, scanID)
	if ferr != nil {
		return 0, 0, ferr
	}
	total += t
	inserted += i
	if len(instances) == 0 {
		return total, inserted, nil
	}

	if t, i, ferr := scanSSOPermissionSets(ctx, ssoClient, acct, region, instances, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	if t, i, ferr := scanSSOAccountAssignments(ctx, ssoClient, acct, region, instances, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	isClient := identitystore.NewFromConfig(acct.cfg, func(o *identitystore.Options) { o.Region = region })
	if t, i, ferr := scanIdentityStoreUsersGroups(ctx, isClient, acct, region, instances, st, scanID); ferr != nil {
		return total, inserted, ferr
	} else {
		total += t
		inserted += i
	}

	return total, inserted, nil
}

func scanSSOInstances(ctx context.Context, client *ssoadmin.Client, acct *account, region string, st *store.Store, scanID string) (instances []ssotypes.InstanceMetadata, total, inserted int, err error) {
	pager := ssoadmin.NewListInstancesPaginator(client, &ssoadmin.ListInstancesInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sso-admin:ListInstances", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("sso-admin:ListInstances: %w", perr)
		}
		instances = append(instances, out.Instances...)
	}
	if len(instances) == 0 {
		return nil, 0, 0, nil
	}

	batch := make([]*store.Resource, 0, len(instances))
	for _, in := range instances {
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeSSOInstance,
			NativeID:       sv(in.InstanceArn),
			Name:           in.Name,
			Region:         &region,
			AttributesJSON: mustJSON(in),
			DiscoveredBy:   scanID,
		})
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return nil, 0, 0, fmt.Errorf("upsert sso instances: %w", uerr)
	}
	return instances, len(batch), n, nil
}

func scanSSOPermissionSets(ctx context.Context, client *ssoadmin.Client, acct *account, region string, instances []ssotypes.InstanceMetadata, st *store.Store, scanID string) (total, inserted int, err error) {
	type psRef struct {
		instanceArn string
		psArn       string
	}
	var refs []psRef
	for _, in := range instances {
		instArn := sv(in.InstanceArn)
		pager := ssoadmin.NewListPermissionSetsPaginator(client, &ssoadmin.ListPermissionSetsInput{InstanceArn: in.InstanceArn})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "sso-admin:ListPermissionSets", acct.ID, region, perr)
					return 0, 0, nil
				}
				return 0, 0, fmt.Errorf("sso-admin:ListPermissionSets: %w", perr)
			}
			for _, arn := range out.PermissionSets {
				refs = append(refs, psRef{instanceArn: instArn, psArn: arn})
			}
		}
	}
	if len(refs) == 0 {
		return 0, 0, nil
	}

	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, ref := range refs {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.DescribePermissionSet(gctx, &ssoadmin.DescribePermissionSetInput{
				InstanceArn: &ref.instanceArn, PermissionSetArn: &ref.psArn,
			})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("sso-admin:DescribePermissionSet %s: %w", ref.psArn, derr)
			}
			if out.PermissionSet == nil {
				return nil
			}
			ps := out.PermissionSet
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSSOPermissionSet,
				NativeID:       sv(ps.PermissionSetArn),
				Name:           ps.Name,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) > 0 {
		n, uerr := st.UpsertResources(batch)
		if uerr != nil {
			return 0, 0, fmt.Errorf("upsert sso permission-sets: %w", uerr)
		}
		total = len(batch)
		inserted = n
	}
	return total, inserted, nil
}

// scanSSOAccountAssignments walks the (instance × permission-set ×
// provisioned-account) cube, calling ListAccountAssignments for each
// (instance, account, permission-set) triple. Result count scales with
// the org's overall assignment fan-out; sized for typical orgs with
// dozens of permission sets and hundreds of accounts.
func scanSSOAccountAssignments(ctx context.Context, client *ssoadmin.Client, acct *account, region string, instances []ssotypes.InstanceMetadata, st *store.Store, scanID string) (total, inserted int, err error) {
	type pair struct {
		instanceArn string
		psArn       string
		accountID   string
	}
	var pairs []pair

	// Step 1: enumerate (instance, permission-set, account) triples by
	// listing accounts each permission-set is provisioned to. Sequential
	// — output drives fan-out, semaphore-based concurrency only buys time
	// once the work-list exists.
	for _, in := range instances {
		instArn := sv(in.InstanceArn)
		psPager := ssoadmin.NewListPermissionSetsPaginator(client, &ssoadmin.ListPermissionSetsInput{InstanceArn: in.InstanceArn})
		for psPager.HasMorePages() {
			psOut, perr := psPager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "sso-admin:ListPermissionSets", acct.ID, region, perr)
					return 0, 0, nil
				}
				return 0, 0, fmt.Errorf("sso-admin:ListPermissionSets: %w", perr)
			}
			for _, psArn := range psOut.PermissionSets {
				acctPager := ssoadmin.NewListAccountsForProvisionedPermissionSetPaginator(client, &ssoadmin.ListAccountsForProvisionedPermissionSetInput{
					InstanceArn: in.InstanceArn, PermissionSetArn: &psArn,
				})
				for acctPager.HasMorePages() {
					aOut, aerr := acctPager.NextPage(ctx)
					if aerr != nil {
						if isAccessDenied(aerr) {
							_ = skipIfAccessDenied(st, "sso-admin:ListAccountsForProvisionedPermissionSet", acct.ID, region, aerr)
							return 0, 0, nil
						}
						return 0, 0, fmt.Errorf("sso-admin:ListAccountsForProvisionedPermissionSet: %w", aerr)
					}
					for _, accountID := range aOut.AccountIds {
						pairs = append(pairs, pair{instanceArn: instArn, psArn: psArn, accountID: accountID})
					}
				}
			}
		}
	}
	if len(pairs) == 0 {
		return 0, 0, nil
	}

	// Step 2: fan-out ListAccountAssignments per triple.
	sem := semaphore.NewWeighted(fanoutHigh)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, p := range pairs {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			pager := ssoadmin.NewListAccountAssignmentsPaginator(client, &ssoadmin.ListAccountAssignmentsInput{
				InstanceArn: &p.instanceArn, AccountId: &p.accountID, PermissionSetArn: &p.psArn,
			})
			for pager.HasMorePages() {
				out, perr := pager.NextPage(gctx)
				if perr != nil {
					if isAccessDenied(perr) {
						return nil
					}
					return fmt.Errorf("sso-admin:ListAccountAssignments: %w", perr)
				}
				for _, a := range out.AccountAssignments {
					nativeID := ssoAssignmentNativeID(p.psArn, sv(a.AccountId), string(a.PrincipalType), sv(a.PrincipalId))
					r := &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeSSOAccountAssignment,
						NativeID:       nativeID,
						Region:         &region,
						AttributesJSON: mustJSON(a),
						DiscoveredBy:   scanID,
					}
					mu.Lock()
					batch = append(batch, r)
					mu.Unlock()
				}
			}
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) > 0 {
		n, uerr := st.UpsertResources(batch)
		if uerr != nil {
			return 0, 0, fmt.Errorf("upsert sso account-assignments: %w", uerr)
		}
		total = len(batch)
		inserted = n
	}
	return total, inserted, nil
}

// ssoAssignmentNativeID synthesizes a stable identifier for an account
// assignment. AWS does not issue a canonical ARN for assignments, so the
// permission-set ARN (which already carries the instance id) is extended
// with the account, principal type, and principal id. Documented under
// "Synthetic NativeIDs" in aws/CLAUDE.md.
func ssoAssignmentNativeID(psArn, accountID, principalType, principalID string) string {
	return psArn + "/account/" + accountID + "/" + principalType + "/" + principalID
}

// identityStoreUserNativeID and identityStoreGroupNativeID synthesize
// ARN-shaped IDs scoped by the instance owner account and identity-store
// id. Identity Store APIs do not return ARNs.
func identityStoreUserNativeID(ownerAccountID, identityStoreID, userID string) string {
	return "arn:aws:identitystore::" + ownerAccountID + ":user/" + identityStoreID + "/" + userID
}

func identityStoreGroupNativeID(ownerAccountID, identityStoreID, groupID string) string {
	return "arn:aws:identitystore::" + ownerAccountID + ":group/" + identityStoreID + "/" + groupID
}

func scanIdentityStoreUsersGroups(ctx context.Context, client *identitystore.Client, acct *account, region string, instances []ssotypes.InstanceMetadata, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, in := range instances {
		identityStoreID := sv(in.IdentityStoreId)
		ownerAccountID := sv(in.OwnerAccountId)
		if identityStoreID == "" {
			continue
		}
		if ownerAccountID == "" {
			ownerAccountID = acct.ID
		}
		if t, i, ferr := scanIdentityStoreUsers(ctx, client, acct, region, identityStoreID, ownerAccountID, st, scanID); ferr != nil {
			return total, inserted, ferr
		} else {
			total += t
			inserted += i
		}
		if t, i, ferr := scanIdentityStoreGroups(ctx, client, acct, region, identityStoreID, ownerAccountID, st, scanID); ferr != nil {
			return total, inserted, ferr
		} else {
			total += t
			inserted += i
		}
	}
	return total, inserted, nil
}

func scanIdentityStoreUsers(ctx context.Context, client *identitystore.Client, acct *account, region, identityStoreID, ownerAccountID string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := identitystore.NewListUsersPaginator(client, &identitystore.ListUsersInput{IdentityStoreId: &identityStoreID})
	var users []istypes.User
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "identitystore:ListUsers", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("identitystore:ListUsers: %w", perr)
		}
		users = append(users, out.Users...)
	}
	if len(users) == 0 {
		return 0, 0, nil
	}
	batch := make([]*store.Resource, 0, len(users))
	for _, u := range users {
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeIdentityStoreUser,
			NativeID:       identityStoreUserNativeID(ownerAccountID, identityStoreID, sv(u.UserId)),
			Name:           u.UserName,
			Region:         &region,
			AttributesJSON: mustJSON(u),
			DiscoveredBy:   scanID,
		})
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert identitystore users: %w", uerr)
	}
	return len(batch), n, nil
}

func scanIdentityStoreGroups(ctx context.Context, client *identitystore.Client, acct *account, region, identityStoreID, ownerAccountID string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := identitystore.NewListGroupsPaginator(client, &identitystore.ListGroupsInput{IdentityStoreId: &identityStoreID})
	var groups []istypes.Group
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "identitystore:ListGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("identitystore:ListGroups: %w", perr)
		}
		groups = append(groups, out.Groups...)
	}
	if len(groups) == 0 {
		return 0, 0, nil
	}
	batch := make([]*store.Resource, 0, len(groups))
	for _, gr := range groups {
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeIdentityStoreGroup,
			NativeID:       identityStoreGroupNativeID(ownerAccountID, identityStoreID, sv(gr.GroupId)),
			Name:           gr.DisplayName,
			Region:         &region,
			AttributesJSON: mustJSON(gr),
			DiscoveredBy:   scanID,
		})
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert identitystore groups: %w", uerr)
	}
	return len(batch), n, nil
}
