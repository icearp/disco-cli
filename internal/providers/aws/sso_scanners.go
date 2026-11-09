package aws

import (
	"context"
	"fmt"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
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
	ListTagsForResource(context.Context, *ssoadmin.ListTagsForResourceInput, ...func(*ssoadmin.Options)) (*ssoadmin.ListTagsForResourceOutput, error)
}

// identitystoreAPI is the narrow set of Identity Store operations called by
// scanIdentityStoreUsersGroups.
type identitystoreAPI interface {
	ListUsers(context.Context, *identitystore.ListUsersInput, ...func(*identitystore.Options)) (*identitystore.ListUsersOutput, error)
	ListGroups(context.Context, *identitystore.ListGroupsInput, ...func(*identitystore.Options)) (*identitystore.ListGroupsOutput, error)
	ListGroupMemberships(context.Context, *identitystore.ListGroupMembershipsInput, ...func(*identitystore.Options)) (*identitystore.ListGroupMembershipsOutput, error)
}

const (
	ssoListInstancesOp         = "sso-admin:ListInstances"
	ssoListPermissionSetsOp    = "sso-admin:ListPermissionSets"
	ssoDescribePermissionSetOp = "sso-admin:DescribePermissionSet"
	ssoTagsOp                  = "ssoadmin:ListTagsForResource"
)

// ssoPageCap is the MaxResults range's maximum (1-100) the model gives every
// SSO Admin listing sent one.
const ssoPageCap = 100

// ssoGone is the not-found code the model lists for DescribePermissionSet,
// ListTagsForResource and the per-parent listings: the resource was deleted
// after it was listed.
var ssoGone = []string{"ResourceNotFoundException"}

// scanSSOAdmin discovers IAM Identity Center (SSO) instances, permission
// sets, account-assignments, and the connected Identity Store's users +
// groups. Scoped per region: ListInstances only returns instances reachable
// from the calling region, so in a region with none every later phase has
// nothing to list.
//
// Phases, run one at a time in this order (runHostPhases): a phase's error
// does not stop the later ones; a failed write and ctx's end do, and so does
// a ListInstances that stores nothing and fails only with NXDOMAIN (the
// region does not serve SSO Admin). The Identity Store phase runs as part of
// the same host, not one of its own: it is called only for an instance
// ListInstances returned, so its NXDOMAIN is a failure of this scan
// (ssoIdentityStoreDNSFailure), never a region gap. A denied listing of
// instances, permission sets, applications or trusted token issuers, or a
// child listing run through childFanOut (a permission set's policies, an
// application's access scopes, authentication methods and grants, an
// instance's regions), is warned about and costs only the rows it would have
// listed and those listed under them. Two denials do not behave so: a denied
// ListAccountsForProvisionedPermissionSet ends phase 3 storing no assignment
// at all, and a denied DescribeInstanceAccessControlAttributeConfiguration
// stores no row and sends no warning.
//  1. ListInstances → TypeSSOInstance.
//  2. Per instance: ListPermissionSets + DescribePermissionSet.
//  3. Per permission set stored in phase 2: ListAccountsForProvisionedPermissionSet
//     → per account, ListAccountAssignments. Each AccountAssignment becomes
//     a TypeSSOAccountAssignment with synthesized NativeID (assignments
//     have no AWS-issued ARN — see aws/CLAUDE.md).
//  4. Per instance: ListUsers + ListGroups against the connected Identity
//     Store (instance.IdentityStoreId).
//  5. scanSSOExtended: applications and the other per-instance surfaces.
//  6. Per permission set stored in phase 2: its AWS managed policies and
//     customer managed policy references, one row per attachment.
//
// Phases 2 and 5 list under the instances phase 1 stored, phases 3 and 6
// under the permission sets phase 2 stored, and phase 4 under every instance
// phase 1 listed, stored or not, as each names its Identity Store. The
// tags of instances, permission sets, applications and trusted token
// issuers are read per row (ssoTags), their denials warned about once for
// the scan.
func scanSSOAdmin(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, error) {
	ssoClient := ssoadmin.NewFromConfig(acct.cfg, func(o *ssoadmin.Options) { o.Region = region })
	isClient := identitystore.NewFromConfig(acct.cfg, func(o *identitystore.Options) { o.Region = region })

	tags := rowTagReader{op: ssoTagsOp, notFound: ssoGone}.sharingDenials()
	var (
		listed, instances []ssotypes.InstanceMetadata
		permissionSets    []ssoPermissionSet
	)
	total, err := runHostPhases(ctx, st, acct, region, "sso-admin", 0, []hostPhase{
		{op: ssoListInstancesOp, run: func(p *phaseTotals) (storeWriteFailed bool) {
			listed, instances, storeWriteFailed = scanSSOInstances(ctx, ssoClient, acct, region, st, scanID, tags, p)
			return storeWriteFailed
		}},
		{op: ssoListPermissionSetsOp, run: func(p *phaseTotals) (storeWriteFailed bool) {
			permissionSets, storeWriteFailed = scanSSOPermissionSets(ctx, ssoClient, acct, region, instances, st, scanID, tags, p)
			return storeWriteFailed
		}},
		countedPhase(0, "sso-admin", func() (int, error) {
			return scanSSOAccountAssignments(ctx, ssoClient, acct, region, permissionSets, st, scanID)
		}),
		countedPhase(0, "identitystore", func() (int, error) {
			t, err := scanIdentityStoreUsersGroups(ctx, isClient, acct, region, listed, st, scanID)
			return t, ssoIdentityStoreDNSFailure(err)
		}),
		{op: ssoListApplicationsOp, run: func(p *phaseTotals) bool {
			return scanSSOExtended(ctx, ssoClient, acct, region, instances, st, scanID, tags, p)
		}},
		// scanSSOPermissionSetPolicies joins its two lists through phaseErrs
		// itself.
		countedPhase(0, "sso-admin", func() (int, error) {
			return scanSSOPermissionSetPolicies(ctx, ssoClient, acct, region, permissionSets, st, scanID)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, err
}

// ssoIdentityStoreDNSFailure is err with an NXDOMAIN flattened to a plain
// error. The Identity Store is called only for an instance ListInstances just
// returned in this region, which names that Identity Store, so its host not
// resolving is a failure of this scan. Kept as NXDOMAIN with no SSO row
// stored, the dispatcher would report the region unavailable and drop the
// missing users, groups and memberships without a word; with rows stored,
// lateNXDOMAIN's message would claim earlier calls to this endpoint that
// never ran.
func ssoIdentityStoreDNSFailure(err error) error {
	if !isDNSNotFound(err) {
		return err
	}
	return fmt.Errorf("identitystore: the Identity Store endpoint did not resolve, although IAM Identity Center listed an instance in this region that names an Identity Store, so its users and groups may be missing; rescan: %v", err)
}

// ssoTags is shared (its op, notFound and the scan's one denial tally)
// reading a row's tags with ListTagsForResource, sent the row's NativeID, the
// ARN its listing returned, and instanceARN, the ARN of the instance it was
// listed under, which the model makes optional and every row has. The
// Service Reference lists all four types read here under
// ListTagsForResource (Application, Instance, PermissionSet,
// TrustedTokenIssuer), and the model's TaggableResourceArn pattern matches
// each one's ARN; CreateInstance, CreatePermissionSet, CreateApplication and
// CreateTrustedTokenIssuer take Tags. The op pages its tags; every page is
// read, and a repeated NextToken fails the read. ResourceNotFoundException,
// its modeled not-found, drops a resource deleted after it was listed.
//
// A row can be another account's: an instance listed by a delegated
// administrator is its owner's, and so is every permission set in it (their
// ARNs carry no account, so the instance's OwnerAccountId is the owner
// signal: untaggedIfOwnerElsewhere); an application or trusted token issuer
// is the account its ARN names (untaggedIfOtherAccount), which need not be
// the instance owner, since member accounts of an organization instance
// create applications in it. Each caller gates its rows so: a denied or
// not-found read of another account's row keeps it untagged without a
// warning. Whether IAM Identity Center refuses those reads is unverified
// live.
func ssoTags(client ssoadminAPI, shared rowTagReader, instanceARN func(*store.Resource) string) rowTagReader {
	shared.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		arn, instance := r.NativeID, instanceARN(r)
		tags, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]ssotypes.Tag, *string, error) {
			out, err := client.ListTagsForResource(ctx, &ssoadmin.ListTagsForResourceInput{ResourceArn: &arn, InstanceArn: &instance, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Tags, out.NextToken, nil
		})
		if err != nil {
			return nil, err
		}
		return kvTagsJSON(tags, func(t ssotypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
	}
	return shared
}

// ssoTagsUnder is ssoTags for the rows listed under the instance instanceARN.
func ssoTagsUnder(client ssoadminAPI, shared rowTagReader, instanceARN string) rowTagReader {
	return ssoTags(client, shared, func(*store.Resource) string { return instanceARN })
}

// scanSSOInstances stores the region's instances with their tags. It returns
// every instance listed, the pages read before a failed one included, and
// those stored: an instance listed without an InstanceArn is not stored
// (warned about), nor one deleted before its tag read. It reports whether
// the write failed, after which the scan must stop.
func scanSSOInstances(ctx context.Context, client ssoadminAPI, acct *account, region string, st *store.Store, scanID string, shared rowTagReader, p *phaseTotals) (listed, stored []ssotypes.InstanceMetadata, storeWriteFailed bool) {
	// Written by the listing's row builds, read only by the tag reads after
	// them.
	byARN := map[string]ssotypes.InstanceMetadata{}
	tags := ssoTags(client, shared, func(r *store.Resource) string { return r.NativeID }).
		untaggedIfOwnerElsewhere(acct.ID, func(r *store.Resource) string { return sv(byARN[r.NativeID].OwnerAccountId) })
	parents, failed := storeParentListTags(ctx, st, acct, region, ssoListInstancesOp, "instances", "InstanceArn",
		func(ctx context.Context, token *string) ([]ssotypes.InstanceMetadata, *string, error) {
			out, err := client.ListInstances(ctx, &ssoadmin.ListInstancesInput{NextToken: token, MaxResults: sdkaws.Int32(ssoPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Instances, out.NextToken, nil
		},
		func(in ssotypes.InstanceMetadata) (*store.Resource, string, string) {
			listed = append(listed, in)
			r, why, key := arnRow("instances", in.InstanceArn, in.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSOInstance, NativeID: sv(in.InstanceArn),
				Name: in.Name, Region: &region,
				AttributesJSON: mustJSON(in), DiscoveredBy: scanID,
			})
			if r != nil {
				byARN[r.NativeID] = in
			}
			return r, why, key
		},
		func(in ssotypes.InstanceMetadata) string { return sv(in.InstanceArn) },
		tags, p)
	if failed {
		return nil, nil, true
	}
	for _, pa := range parents {
		stored = append(stored, byARN[pa.arn])
	}
	return listed, stored, false
}

// ssoPermissionSet is a permission set and the instance it belongs to, the
// pair every per-permission-set call takes.
type ssoPermissionSet struct{ instanceARN, arn string }

// scanSSOPermissionSets lists each instance's permission sets, completes
// each with DescribePermissionSet (its body as the attributes, its Name),
// reads its tags, writes each page as soon as it is read, and returns the
// permission sets stored.
//
// ListPermissionSets returns ARNs only, so a permission set whose describe
// fails other than as not found, or answers without a PermissionSet, is
// stored as its listed element: the same NativeID, its ARN as its
// attributes, no name. The denied describes of every instance are one
// warning, and so are the denied listings, each keeping the pages read
// before it; any other error is returned once the rows are stored. A
// permission set the describe or the tag read finds deleted is dropped. An
// instance whose first page is not found (ResourceNotFoundException, modeled
// for ListPermissionSets) was deleted after ListInstances and has none. One
// instance's failed listing keeps the pages read before it and costs the
// other instances nothing. It reports whether a write failed, after which
// the scan must stop.
func scanSSOPermissionSets(ctx context.Context, client ssoadminAPI, acct *account, region string, instances []ssotypes.InstanceMetadata, st *store.Store, scanID string, shared rowTagReader, p *phaseTotals) (sets []ssoPermissionSet, storeWriteFailed bool) {
	describe := rowDetailReader{op: ssoDescribePermissionSetOp, what: "permission set(s)", notFound: ssoGone}.sharingDetailDenials()
	var listDenied skipTally
	for _, in := range instances {
		instanceARN, owner := sv(in.InstanceArn), sv(in.OwnerAccountId)
		details := describe
		details.get = func(ctx context.Context, r *store.Resource) (any, error) {
			arn := r.NativeID
			out, err := client.DescribePermissionSet(ctx, &ssoadmin.DescribePermissionSetInput{InstanceArn: &instanceARN, PermissionSetArn: &arn})
			if err != nil {
				return nil, err
			}
			if out.PermissionSet == nil {
				return nil, nil // keeps the listed element as the attributes
			}
			r.Name = out.PermissionSet.Name
			return out, nil
		}
		steps := tokenPageSteps{
			details: details,
			stored: func(rows []*store.Resource) {
				for _, r := range rows {
					sets = append(sets, ssoPermissionSet{instanceARN: instanceARN, arn: r.NativeID})
				}
			},
		}
		// The denial ends this instance's listing as deniedPages says and is
		// warned about with the other instances' after the loop, not by the
		// listing.
		denial := listDenial{isDenied: isAccessDenied, unread: "permission sets on later pages and what is listed under them"}
		if storeTokenPagesTagsWith(ctx, st, acct, region, ssoListPermissionSetsOp,
			firstPageGapPages(isSSOParentGone, deniedPages(&denial, func(ctx context.Context, token *string) ([]string, *string, error) {
				out, err := client.ListPermissionSets(ctx, &ssoadmin.ListPermissionSetsInput{InstanceArn: &instanceARN, NextToken: token, MaxResults: sdkaws.Int32(ssoPageCap)})
				if err != nil {
					return nil, nil, err
				}
				return out.PermissionSets, out.NextToken, nil
			})),
			func(arn string) (*store.Resource, string, string) {
				return arnRow("permission sets", &arn, nil, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeSSOPermissionSet, NativeID: arn, Region: &region,
					AttributesJSON: mustJSON(map[string]string{"PermissionSetArn": arn}), DiscoveredBy: scanID,
				})
			},
			steps, ssoTagsUnder(client, shared, instanceARN).untaggedIfOwnerElsewhere(acct.ID, func(*store.Resource) string { return owner }), p) {
			return nil, true
		}
		if denial.err != nil {
			listDenied.skipped(instanceARN, denial.err)
		}
	}
	if listDenied.count > 0 {
		_ = skipIfAccessDenied(st, ssoListPermissionSetsOp, acct.ID, region, fmt.Errorf("%d instance(s) denied, first %s: %w", listDenied.count, listDenied.first, listDenied.err))
	}
	describe.warnDetailDenials(st, acct, region)
	return sets, false
}

// scanSSOAccountAssignments lists, for each permission set phase 2 stored
// (sets), the accounts it is provisioned to, then calls
// ListAccountAssignments per (permission set, account). It reads the stored
// permission sets rather than listing them again, so an instance whose
// listing phase 2 found deleted, denied or failed is not reported twice.
// Result count scales with the org's assignment fan-out; sized for typical
// orgs with dozens of permission sets and hundreds of accounts.
func scanSSOAccountAssignments(ctx context.Context, client ssoadminAPI, acct *account, region string, sets []ssoPermissionSet, st *store.Store, scanID string) (total int, err error) {
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
	for _, ps := range sets {
		acctPager := ssoadmin.NewListAccountsForProvisionedPermissionSetPaginator(client, &ssoadmin.ListAccountsForProvisionedPermissionSetInput{
			InstanceArn: &ps.instanceARN, PermissionSetArn: &ps.arn,
		})
		for acctPager.HasMorePages() {
			aOut, aerr := acctPager.NextPage(ctx)
			if aerr != nil {
				if isAccessDenied(aerr) {
					_ = skipIfAccessDenied(st, "sso-admin:ListAccountsForProvisionedPermissionSet", acct.ID, region, aerr)
					return 0, nil
				}
				return 0, fmt.Errorf("sso-admin:ListAccountsForProvisionedPermissionSet: %w", aerr)
			}
			for _, accountID := range aOut.AccountIds {
				pairs = append(pairs, pair{instanceArn: ps.instanceARN, psArn: ps.arn, accountID: accountID})
			}
		}
	}
	if len(pairs) == 0 {
		return 0, nil
	}

	// Step 2: fan-out ListAccountAssignments per triple. SetLimit, not a
	// semaphore acquired on gctx: once one call fails, gctx is cancelled and
	// an Acquire would return context.Canceled in place of that call's
	// error, which g.Wait reports.
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
		return 0, werr
	}
	if len(batch) > 0 {
		uerr := st.UpsertResources(batch)
		if uerr != nil {
			return 0, fmt.Errorf("upsert sso account-assignments: %w", uerr)
		}
		total = len(batch)
	}
	return total, nil
}

func scanIdentityStoreUsersGroups(ctx context.Context, client identitystoreAPI, acct *account, region string, instances []ssotypes.InstanceMetadata, st *store.Store, scanID string) (total int, err error) {
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
			t, ferr := scanIdentityStoreUsers(ctx, client, acct, region, identityStoreID, ownerAccountID, st, scanID)
			if ferr != nil {
				return total, ferr
			}
			total += t
		}
		{
			t, ferr := scanIdentityStoreGroups(ctx, client, acct, region, identityStoreID, ownerAccountID, st, scanID)
			if ferr != nil {
				return total, ferr
			}
			total += t
		}
		{
			t, ferr := scanIdentityStoreGroupMemberships(ctx, client, acct, region, identityStoreID, ownerAccountID, st, scanID)
			if ferr != nil {
				return total, ferr
			}
			total += t
		}
	}
	return total, nil
}

// scanIdentityStoreGroupMemberships discovers per-group user memberships.
// Synth ARN: arn:{p}:identitystore::{ownerAccount}:membership/{identityStoreId}/{membershipId}.
func scanIdentityStoreGroupMemberships(ctx context.Context, client identitystoreAPI, acct *account, region, identityStoreID, ownerAccountID string, st *store.Store, scanID string) (total int, err error) {
	groupPager := identitystore.NewListGroupsPaginator(client, &identitystore.ListGroupsInput{IdentityStoreId: &identityStoreID})
	var groupIDs []string
	for groupPager.HasMorePages() {
		out, perr := groupPager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "identitystore:ListGroups(memberships)", acct.ID, region, perr)
				return 0, nil
			}
			return 0, fmt.Errorf("identitystore:ListGroups(memberships): %w", perr)
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
				return 0, fmt.Errorf("identitystore:ListGroupMemberships g=%s: %w", groupID, perr)
			}
			for _, m := range out.GroupMemberships {
				mid := sv(m.MembershipId)
				if mid == "" {
					continue
				}
				arn := arnFor(acct.partition(), "identitystore", "", ownerAccountID, "membership/"+identityStoreID+"/"+mid)
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
		return 0, nil
	}
	uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, fmt.Errorf("upsert identitystore group-memberships: %w", uerr)
	}
	return len(batch), nil
}

func scanIdentityStoreUsers(ctx context.Context, client identitystoreAPI, acct *account, region, identityStoreID, ownerAccountID string, st *store.Store, scanID string) (total int, err error) {
	pager := identitystore.NewListUsersPaginator(client, &identitystore.ListUsersInput{IdentityStoreId: &identityStoreID})
	var users []istypes.User
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "identitystore:ListUsers", acct.ID, region, perr)
				return 0, nil
			}
			return 0, fmt.Errorf("identitystore:ListUsers: %w", perr)
		}
		users = append(users, out.Users...)
	}
	if len(users) == 0 {
		return 0, nil
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
	uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, fmt.Errorf("upsert identitystore users: %w", uerr)
	}
	return len(batch), nil
}

func scanIdentityStoreGroups(ctx context.Context, client identitystoreAPI, acct *account, region, identityStoreID, ownerAccountID string, st *store.Store, scanID string) (total int, err error) {
	pager := identitystore.NewListGroupsPaginator(client, &identitystore.ListGroupsInput{IdentityStoreId: &identityStoreID})
	var groups []istypes.Group
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "identitystore:ListGroups", acct.ID, region, perr)
				return 0, nil
			}
			return 0, fmt.Errorf("identitystore:ListGroups: %w", perr)
		}
		groups = append(groups, out.Groups...)
	}
	if len(groups) == 0 {
		return 0, nil
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
	uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, fmt.Errorf("upsert identitystore groups: %w", uerr)
	}
	return len(batch), nil
}
