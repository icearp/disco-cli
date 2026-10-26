package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/identitystore"
	istypes "github.com/aws/aws-sdk-go-v2/service/identitystore/types"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
	ssotypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSSOInstance, Service: "sso"})
	registerType(restype.Descriptor{Type: TypeSSOPermissionSet, Service: "sso"})
	registerType(restype.Descriptor{Type: TypeSSOAccountAssignment, Service: "sso", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeSSOApplication, Service: "sso"})
	registerType(restype.Descriptor{Type: TypeSSOApplicationAssignment, Service: "sso"})
	registerType(restype.Descriptor{Type: TypeSSOInstanceAccessControlAttributeConfiguration, Service: "sso"})
	registerType(restype.Descriptor{Type: TypeSSOApplicationProvider, Service: "sso", Managed: true, Untaggable: true})
	registerType(restype.Descriptor{Type: TypeSSOTrustedTokenIssuer, Service: "sso"})
	registerType(restype.Descriptor{Type: TypeSSOApplicationAccessScope, Service: "sso"})
	registerType(restype.Descriptor{Type: TypeSSOApplicationAuthenticationMethod, Service: "sso"})
	registerType(restype.Descriptor{Type: TypeSSOApplicationGrant, Service: "sso"})
	registerType(restype.Descriptor{Type: TypeSSORegion, Service: "sso"})
	registerType(restype.Descriptor{Type: TypeSSOManagedPolicy, Service: "sso"})
	registerType(restype.Descriptor{Type: TypeSSOCustomerManagedPolicyReference, Service: "sso"})
	registerType(restype.Descriptor{Type: TypeIdentityStoreUser, Service: "identitystore", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeIdentityStoreGroup, Service: "identitystore", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeIdentityStoreGroupMembership, Service: "identitystore", Untaggable: true})
	registerService(serviceEntry{
		name: "aws:sso-admin",
		fn:   scanSSOAdmin,
	})
}

// ssoadminAPI is the narrow set of SSO Admin operations called by the
// scanSSOAdmin sub-phases.
type ssoadminAPI interface {
	ListInstances(context.Context, *ssoadmin.ListInstancesInput, ...func(*ssoadmin.Options)) (*ssoadmin.ListInstancesOutput, error)
	ListPermissionSets(context.Context, *ssoadmin.ListPermissionSetsInput, ...func(*ssoadmin.Options)) (*ssoadmin.ListPermissionSetsOutput, error)
	DescribePermissionSet(context.Context, *ssoadmin.DescribePermissionSetInput, ...func(*ssoadmin.Options)) (*ssoadmin.DescribePermissionSetOutput, error)
	ListAccountsForProvisionedPermissionSet(context.Context, *ssoadmin.ListAccountsForProvisionedPermissionSetInput, ...func(*ssoadmin.Options)) (*ssoadmin.ListAccountsForProvisionedPermissionSetOutput, error)
	ListAccountAssignments(context.Context, *ssoadmin.ListAccountAssignmentsInput, ...func(*ssoadmin.Options)) (*ssoadmin.ListAccountAssignmentsOutput, error)
	ListApplications(context.Context, *ssoadmin.ListApplicationsInput, ...func(*ssoadmin.Options)) (*ssoadmin.ListApplicationsOutput, error)
	ListApplicationAssignments(context.Context, *ssoadmin.ListApplicationAssignmentsInput, ...func(*ssoadmin.Options)) (*ssoadmin.ListApplicationAssignmentsOutput, error)
	DescribeInstanceAccessControlAttributeConfiguration(context.Context, *ssoadmin.DescribeInstanceAccessControlAttributeConfigurationInput, ...func(*ssoadmin.Options)) (*ssoadmin.DescribeInstanceAccessControlAttributeConfigurationOutput, error)
	ListApplicationProviders(context.Context, *ssoadmin.ListApplicationProvidersInput, ...func(*ssoadmin.Options)) (*ssoadmin.ListApplicationProvidersOutput, error)
	ListTrustedTokenIssuers(context.Context, *ssoadmin.ListTrustedTokenIssuersInput, ...func(*ssoadmin.Options)) (*ssoadmin.ListTrustedTokenIssuersOutput, error)
	ListApplicationAccessScopes(context.Context, *ssoadmin.ListApplicationAccessScopesInput, ...func(*ssoadmin.Options)) (*ssoadmin.ListApplicationAccessScopesOutput, error)
	ListApplicationAuthenticationMethods(context.Context, *ssoadmin.ListApplicationAuthenticationMethodsInput, ...func(*ssoadmin.Options)) (*ssoadmin.ListApplicationAuthenticationMethodsOutput, error)
	ListApplicationGrants(context.Context, *ssoadmin.ListApplicationGrantsInput, ...func(*ssoadmin.Options)) (*ssoadmin.ListApplicationGrantsOutput, error)
	ListRegions(context.Context, *ssoadmin.ListRegionsInput, ...func(*ssoadmin.Options)) (*ssoadmin.ListRegionsOutput, error)
	ListManagedPoliciesInPermissionSet(context.Context, *ssoadmin.ListManagedPoliciesInPermissionSetInput, ...func(*ssoadmin.Options)) (*ssoadmin.ListManagedPoliciesInPermissionSetOutput, error)
	ListCustomerManagedPolicyReferencesInPermissionSet(context.Context, *ssoadmin.ListCustomerManagedPolicyReferencesInPermissionSetInput, ...func(*ssoadmin.Options)) (*ssoadmin.ListCustomerManagedPolicyReferencesInPermissionSetOutput, error)
}

// identitystoreAPI is the narrow set of Identity Store operations called by
// scanIdentityStoreUsersGroups.
type identitystoreAPI interface {
	ListUsers(context.Context, *identitystore.ListUsersInput, ...func(*identitystore.Options)) (*identitystore.ListUsersOutput, error)
	ListGroups(context.Context, *identitystore.ListGroupsInput, ...func(*identitystore.Options)) (*identitystore.ListGroupsOutput, error)
	ListGroupMemberships(context.Context, *identitystore.ListGroupMembershipsInput, ...func(*identitystore.Options)) (*identitystore.ListGroupMembershipsOutput, error)
}

// scanSSOAdmin discovers IAM Identity Center (SSO) instances, permission
// sets, account-assignments, and the connected Identity Store's users +
// groups. Scoped per region: ListInstances only returns instances reachable
// from the calling region, so empty regions short-circuit immediately and
// only the org instance's home region does work.
//
// Phases (each tolerates AccessDenied via skipIfAccessDenied without
// barring later phases — non-management accounts hit AccessDenied on
// every SSO admin op):
//  1. ListInstances → upsert TypeSSOInstance.
//  2. Per instance: ListPermissionSets + DescribePermissionSet fan-out.
//  3. Per (instance, permission-set): ListAccountsForProvisionedPermissionSet
//     → per account, ListAccountAssignments. Each AccountAssignment becomes
//     a TypeSSOAccountAssignment with synthesized NativeID (assignments
//     have no AWS-issued ARN — see aws/CLAUDE.md).
//  4. Per instance: ListUsers + ListGroups against the connected Identity
//     Store (instance.IdentityStoreId).
//  5. scanSSOExtended: applications and the other per-instance surfaces.
//  6. Per permission set stored in phase 2: its AWS managed policies and
//     customer managed policy references, one row per attachment.
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

	// Phases 2-6 each run even when an earlier one failed, and keep their
	// counts when they fail themselves; phase 6 lists only what phase 2 stored.
	// Their errors are joined by phaseErrs, a transient one demoted only beside
	// a hard one: joined, it would be buried in the hard error instead of
	// reported under its op.
	var errs phaseErrs
	add := func(service string, t, i int, err error) {
		total += t
		inserted += i
		errs.add(service, err)
	}

	permissionSets, t, i, ferr := scanSSOPermissionSets(ctx, ssoClient, acct, region, instances, st, scanID)
	add("sso-admin", t, i, ferr)

	t, i, ferr = scanSSOAccountAssignments(ctx, ssoClient, acct, region, instances, st, scanID)
	add("sso-admin", t, i, ferr)

	isClient := identitystore.NewFromConfig(acct.cfg, func(o *identitystore.Options) { o.Region = region })
	t, i, ferr = scanIdentityStoreUsersGroups(ctx, isClient, acct, region, instances, st, scanID)
	add("identitystore", t, i, ferr)

	t, i, ferr = scanSSOExtended(ctx, ssoClient, acct, region, instances, st, scanID)
	add("sso-admin", t, i, ferr)

	// scanSSOPermissionSetPolicies joins its two lists through phaseErrs
	// itself.
	t, i, ferr = scanSSOPermissionSetPolicies(ctx, ssoClient, acct, region, permissionSets, st, scanID)
	total += t
	inserted += i
	errs.add("sso-admin", ferr)

	return total, inserted, errs.join(st, acct, region)
}

func scanSSOInstances(ctx context.Context, client ssoadminAPI, acct *account, region string, st *store.Store, scanID string) (instances []ssotypes.InstanceMetadata, total, inserted int, err error) {
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

// ssoPermissionSet is a permission set and the instance it belongs to, the
// pair every per-permission-set call takes.
type ssoPermissionSet struct{ instanceARN, arn string }

// scanSSOPermissionSets lists and describes each instance's permission sets
// and returns the ones it stored.
func scanSSOPermissionSets(ctx context.Context, client ssoadminAPI, acct *account, region string, instances []ssotypes.InstanceMetadata, st *store.Store, scanID string) ([]ssoPermissionSet, int, int, error) {
	var refs []ssoPermissionSet
	for _, in := range instances {
		instArn := sv(in.InstanceArn)
		pager := ssoadmin.NewListPermissionSetsPaginator(client, &ssoadmin.ListPermissionSetsInput{InstanceArn: in.InstanceArn})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "sso-admin:ListPermissionSets", acct.ID, region, perr)
					return nil, 0, 0, nil
				}
				return nil, 0, 0, fmt.Errorf("sso-admin:ListPermissionSets: %w", perr)
			}
			for _, arn := range out.PermissionSets {
				refs = append(refs, ssoPermissionSet{instanceARN: instArn, arn: arn})
			}
		}
	}
	if len(refs) == 0 {
		return nil, 0, 0, nil
	}

	// SetLimit, not a semaphore acquired on gctx: once one call fails, gctx is
	// cancelled and an Acquire would return context.Canceled in place of that
	// call's error, which g.Wait reports.
	var (
		mu           sync.Mutex
		batch        []*store.Resource
		sets         []ssoPermissionSet
		unidentified int
	)
	// A set deleted after the listing is skipped silently; a denial warns once.
	errs := &fanOutErrs{st: st, acct: acct, region: region, op: "sso-admin:DescribePermissionSet", isSkip: isSSOParentGone}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutMed)
	for _, ref := range refs {
		g.Go(func() error {
			out, derr := client.DescribePermissionSet(gctx, &ssoadmin.DescribePermissionSetInput{
				InstanceArn: &ref.instanceARN, PermissionSetArn: &ref.arn,
			})
			if derr != nil {
				return errs.keyErr(ref.arn, derr)
			}
			ps := out.PermissionSet
			if ps == nil || sv(ps.PermissionSetArn) == "" {
				mu.Lock()
				unidentified++
				mu.Unlock()
				return nil
			}
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
			defer mu.Unlock()
			batch = append(batch, r)
			sets = append(sets, ssoPermissionSet{instanceARN: ref.instanceARN, arn: r.NativeID})
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return nil, 0, 0, werr
	}
	// The model makes PermissionSet and its PermissionSetArn optional. Without
	// the ARN the row has no identity (every such row would share one
	// ResourceID), so it is dropped.
	if unidentified > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: "sso-admin:DescribePermissionSet", Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d permission set(s) described without a PermissionSet or PermissionSetArn were not stored", unidentified),
		})
	}
	if len(batch) == 0 {
		return nil, 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return nil, 0, 0, fmt.Errorf("upsert sso permission-sets: %w", uerr)
	}
	return sets, len(batch), n, nil
}

// scanSSOAccountAssignments walks the (instance × permission-set ×
// provisioned-account) cube, calling ListAccountAssignments per triple.
// Result count scales with the org's assignment fan-out; sized for typical
// orgs with dozens of permission sets and hundreds of accounts.
func scanSSOAccountAssignments(ctx context.Context, client ssoadminAPI, acct *account, region string, instances []ssotypes.InstanceMetadata, st *store.Store, scanID string) (total, inserted int, err error) {
	type pair struct {
		instanceArn string
		psArn       string
		accountID   string
	}
	var pairs []pair

	// Step 1: enumerate (instance, permission-set, account) triples by
	// listing accounts each permission-set is provisioned to. Sequential:
	// output drives fan-out, so concurrency only helps once the work-list
	// exists.
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

	// Step 2: fan-out ListAccountAssignments per triple. SetLimit, as in
	// scanSSOPermissionSets, so g.Wait reports the failed call's own error.
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	errs := &fanOutErrs{st: st, acct: acct, region: region, op: "sso-admin:ListAccountAssignments", isSkip: isSSOParentGone}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(fanoutHigh)
	for _, p := range pairs {
		g.Go(func() error {
			pager := ssoadmin.NewListAccountAssignmentsPaginator(client, &ssoadmin.ListAccountAssignmentsInput{
				InstanceArn: &p.instanceArn, AccountId: &p.accountID, PermissionSetArn: &p.psArn,
			})
			for pager.HasMorePages() {
				out, perr := pager.NextPage(gctx)
				if perr != nil {
					return errs.keyErr(p.psArn+" account "+p.accountID, perr)
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

func scanIdentityStoreUsersGroups(ctx context.Context, client identitystoreAPI, acct *account, region string, instances []ssotypes.InstanceMetadata, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, in := range instances {
		identityStoreID := sv(in.IdentityStoreId)
		ownerAccountID := sv(in.OwnerAccountId)
		if identityStoreID == "" {
			continue
		}
		if ownerAccountID == "" {
			ownerAccountID = acct.ID
		}
		{
			t, i, ferr := scanIdentityStoreUsers(ctx, client, acct, region, identityStoreID, ownerAccountID, st, scanID)
			if ferr != nil {
				return total, inserted, ferr
			}
			total += t
			inserted += i
		}
		{
			t, i, ferr := scanIdentityStoreGroups(ctx, client, acct, region, identityStoreID, ownerAccountID, st, scanID)
			if ferr != nil {
				return total, inserted, ferr
			}
			total += t
			inserted += i
		}
		{
			t, i, ferr := scanIdentityStoreGroupMemberships(ctx, client, acct, region, identityStoreID, ownerAccountID, st, scanID)
			if ferr != nil {
				return total, inserted, ferr
			}
			total += t
			inserted += i
		}
	}
	return total, inserted, nil
}

// scanIdentityStoreGroupMemberships discovers per-group user memberships.
// Synth ARN: arn:aws:identitystore::{ownerAccount}:membership/{identityStoreId}/{membershipId}.
func scanIdentityStoreGroupMemberships(ctx context.Context, client identitystoreAPI, acct *account, region, identityStoreID, ownerAccountID string, st *store.Store, scanID string) (total, inserted int, err error) {
	groupPager := identitystore.NewListGroupsPaginator(client, &identitystore.ListGroupsInput{IdentityStoreId: &identityStoreID})
	var groupIDs []string
	for groupPager.HasMorePages() {
		out, perr := groupPager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "identitystore:ListGroups(memberships)", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("identitystore:ListGroups(memberships): %w", perr)
		}
		for _, g := range out.Groups {
			if g.GroupId != nil {
				groupIDs = append(groupIDs, *g.GroupId)
			}
		}
	}
	var batch []*store.Resource
	for _, gid := range groupIDs {
		groupID := gid
		mpager := identitystore.NewListGroupMembershipsPaginator(client, &identitystore.ListGroupMembershipsInput{
			IdentityStoreId: &identityStoreID,
			GroupId:         &groupID,
		})
		for mpager.HasMorePages() {
			out, perr := mpager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "identitystore:ListGroupMemberships", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("identitystore:ListGroupMemberships g=%s: %w", groupID, perr)
			}
			for _, m := range out.GroupMemberships {
				mid := sv(m.MembershipId)
				if mid == "" {
					continue
				}
				arn := fmt.Sprintf("arn:aws:identitystore::%s:membership/%s/%s", ownerAccountID, identityStoreID, mid)
				label := mid
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeIdentityStoreGroupMembership, NativeID: arn,
					Name: &label, Region: &region,
					AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
				})
			}
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert identitystore group-memberships: %w", uerr)
	}
	return len(batch), n, nil
}

func scanIdentityStoreUsers(ctx context.Context, client identitystoreAPI, acct *account, region, identityStoreID, ownerAccountID string, st *store.Store, scanID string) (total, inserted int, err error) {
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
			NativeID:       identityStoreUserNativeIDIn(acct.partition(), ownerAccountID, identityStoreID, sv(u.UserId)),
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

func scanIdentityStoreGroups(ctx context.Context, client identitystoreAPI, acct *account, region, identityStoreID, ownerAccountID string, st *store.Store, scanID string) (total, inserted int, err error) {
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
			NativeID:       identityStoreGroupNativeIDIn(acct.partition(), ownerAccountID, identityStoreID, sv(gr.GroupId)),
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
