package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/workmail"
	workmailtypes "github.com/aws/aws-sdk-go-v2/service/workmail/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeWorkMailOrganization, Service: "workmail"})
	registerType(restype.Descriptor{Type: TypeWorkMailUser, Service: "workmail"})
	registerType(restype.Descriptor{Type: TypeWorkMailGroup, Service: "workmail"})
	registerType(restype.Descriptor{Type: TypeWorkMailResource, Service: "workmail"})
	registerType(restype.Descriptor{Type: TypeWorkMailMailDomain, Service: "workmail"})
	registerType(restype.Descriptor{Type: TypeWorkMailAccessControlRule, Service: "workmail"})
	registerType(restype.Descriptor{Type: TypeWorkMailAvailabilityConfiguration, Service: "workmail"})
	registerType(restype.Descriptor{Type: TypeWorkMailImpersonationRole, Service: "workmail"})
	registerType(restype.Descriptor{Type: TypeWorkMailMobileDeviceAccessRule, Service: "workmail"})
	registerType(restype.Descriptor{Type: TypeWorkMailMobileDeviceAccessOverride, Service: "workmail"})
	registerType(restype.Descriptor{Type: TypeWorkMailPersonalAccessToken, Service: "workmail"})
	registerType(restype.Descriptor{Type: TypeWorkMailMailboxPermission, Service: "workmail"})
	registerService(serviceEntry{
		name: "aws:workmail",
		fn:   scanWorkMail,
	})
}

// workMailPageSize is the documented MaxResults cap of every paginated
// WorkMail list op scanned here.
const workMailPageSize int32 = 100

type workMailAPI interface {
	ListOrganizations(context.Context, *workmail.ListOrganizationsInput, ...func(*workmail.Options)) (*workmail.ListOrganizationsOutput, error)
	ListUsers(context.Context, *workmail.ListUsersInput, ...func(*workmail.Options)) (*workmail.ListUsersOutput, error)
	ListGroups(context.Context, *workmail.ListGroupsInput, ...func(*workmail.Options)) (*workmail.ListGroupsOutput, error)
	ListResources(context.Context, *workmail.ListResourcesInput, ...func(*workmail.Options)) (*workmail.ListResourcesOutput, error)
	ListMailDomains(context.Context, *workmail.ListMailDomainsInput, ...func(*workmail.Options)) (*workmail.ListMailDomainsOutput, error)
	ListAccessControlRules(context.Context, *workmail.ListAccessControlRulesInput, ...func(*workmail.Options)) (*workmail.ListAccessControlRulesOutput, error)
	ListAvailabilityConfigurations(context.Context, *workmail.ListAvailabilityConfigurationsInput, ...func(*workmail.Options)) (*workmail.ListAvailabilityConfigurationsOutput, error)
	ListImpersonationRoles(context.Context, *workmail.ListImpersonationRolesInput, ...func(*workmail.Options)) (*workmail.ListImpersonationRolesOutput, error)
	ListMobileDeviceAccessRules(context.Context, *workmail.ListMobileDeviceAccessRulesInput, ...func(*workmail.Options)) (*workmail.ListMobileDeviceAccessRulesOutput, error)
	ListMobileDeviceAccessOverrides(context.Context, *workmail.ListMobileDeviceAccessOverridesInput, ...func(*workmail.Options)) (*workmail.ListMobileDeviceAccessOverridesOutput, error)
	ListPersonalAccessTokens(context.Context, *workmail.ListPersonalAccessTokensInput, ...func(*workmail.Options)) (*workmail.ListPersonalAccessTokensOutput, error)
	ListMailboxPermissions(context.Context, *workmail.ListMailboxPermissionsInput, ...func(*workmail.Options)) (*workmail.ListMailboxPermissionsOutput, error)
}

// scanWorkMail discovers WorkMail organizations and, per organization, its
// users, groups, resources, mail domains, access control rules, availability
// configurations, impersonation roles, mobile device access rules and
// overrides, personal access tokens (metadata only: the list API never
// returns a token's secret) and the mailbox permissions of its users and
// resources.
func scanWorkMail(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := workmail.NewFromConfig(acct.cfg, func(o *workmail.Options) { o.Region = region })
	return scanWorkMailIn(ctx, client, acct, region, st, scanID)
}

// scanWorkMailIn lists the organizations, then runs every child phase over
// them even when one fails, so one phase's error costs no other phase its
// rows. The mailbox permission phase runs last, because it fans out over the
// users and resources the earlier phases stored.
func scanWorkMailIn(ctx context.Context, client workMailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	orgs, total, inserted, err := scanWorkMailOrganizations(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	s := &workMailScan{client: client, acct: acct, region: region, st: st, scanID: scanID, orgs: orgs, rejected: map[string]bool{}}
	var errs []error
	for _, phase := range []func(context.Context) (int, int, error){
		s.users, s.groups, s.resources, s.mailDomains, s.accessControlRules, s.availabilityConfigurations,
		s.impersonationRoles, s.mobileDeviceAccessRules, s.mobileDeviceAccessOverrides, s.personalAccessTokens,
		s.mailboxPermissions,
	} {
		t, i, perr := phase(ctx)
		total += t
		inserted += i
		errs = append(errs, perr)
	}
	return total, inserted, errors.Join(errs...)
}

// scanWorkMailOrganizations stores every organization, keyed by the
// organization ARN AWS documents (ListOrganizations returns only the id), and
// returns them as child fan-out parents. The organization State is an
// undocumented free-form string, so no organization is filtered out by it;
// the child phases handle the state rejection the API models instead.
func scanWorkMailOrganizations(ctx context.Context, client workMailAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	var (
		batch   []*store.Resource
		parents []childParent
	)
	pager := workmail.NewListOrganizationsPaginator(client, &workmail.ListOrganizationsInput{MaxResults: sdkaws.Int32(workMailPageSize)})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "workmail:ListOrganizations", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("workmail:ListOrganizations: %w", err)
		}
		for _, o := range out.OrganizationSummaries {
			id := sv(o.OrganizationId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:workmail:%s:%s:organization/%s", region, acct.ID, id)
			label := sv(o.Alias)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeWorkMailOrganization, NativeID: arn,
				Name: &label, Region: &region, Status: nonEmptyPtr(sv(o.State)),
				AttributesJSON: mustJSON(o), DiscoveredBy: scanID,
			})
			parents = append(parents, childParent{id: id, arn: arn})
		}
	}
	total, inserted, err := upsertBatch(st, batch, "workmail organizations")
	if err != nil {
		return nil, 0, 0, err
	}
	return parents, total, inserted, nil
}

// workMailScan is the per-region state the organization child phases share.
type workMailScan struct {
	client workMailAPI
	acct   *account
	region string
	st     *store.Store
	scanID string
	orgs   []childParent

	mu sync.Mutex
	// rejected holds the organizations whose state refused a member listing
	// this scan; later phases skip them, so each is warned about once.
	rejected map[string]bool
	// mailboxes holds the ENABLED users and resources the users and resources
	// phases stored, the parents of the mailbox permission phase.
	mailboxes []workMailMailbox
}

// workMailMailbox is a stored user or resource whose mailbox permissions are
// listed: entity.id is its entity id, entity.arn its row's NativeID.
type workMailMailbox struct{ org, entity childParent }

// listableOrgs returns the organizations no earlier phase saw rejected.
func (s *workMailScan) listableOrgs() []childParent {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []childParent
	for _, o := range s.orgs {
		if !s.rejected[o.id] {
			out = append(out, o)
		}
	}
	return out
}

// rejectOrg records an OrganizationStateException: the organization exists
// but its state (for example, still being created or being deleted) refuses
// listing its members. That is not a deletion to skip silently, so it warns,
// and drops the organization from the phases after this one.
func (s *workMailScan) rejectOrg(op string, org childParent, err error) {
	s.mu.Lock()
	s.rejected[org.id] = true
	s.mu.Unlock()
	s.st.ReportWarning(store.ScanWarning{
		Provider: "aws",
		Service:  op,
		Scope:    s.acct.ID + "/" + s.region,
		Message:  "organization " + org.id + " state refuses listing its members; its children are not listed this scan: " + err.Error(),
	})
}

// fanOut runs list for every listable organization through childFanOut. An
// organization deleted since it was listed is skipped silently; one whose
// state refuses the call is warned about and rejected.
//
// A transient network error is warned about here rather than returned:
// scanWorkMailIn joins the phase errors, and the dispatcher classifies a join
// by any member, so one throttled phase would make the whole service a
// transient skip, discarding the other phases' counts and downgrading their
// hard errors. A store write error is never transient (store formats the
// cause with %s), and childFanOut returns one error, never a join.
func (s *workMailScan) fanOut(ctx context.Context, op string, list func(context.Context, childParent) ([]*store.Resource, error)) (int, int, error) {
	return s.fanOutThen(ctx, op, list, nil)
}

// fanOutThen is fanOut that also runs stored, when not nil, once every row
// list returned has been stored. It does not run when childFanOut fails,
// because childFanOut then stores nothing.
func (s *workMailScan) fanOutThen(ctx context.Context, op string, list func(context.Context, childParent) ([]*store.Resource, error), stored func()) (int, int, error) {
	total, inserted, err := childFanOut(ctx, s.st, s.acct, s.region, op, s.listableOrgs(), isWorkMailOrgGone,
		func(ctx context.Context, org childParent) ([]*store.Resource, error) {
			rows, err := list(ctx, org)
			if isAPIErrorCode(err, "OrganizationStateException") {
				s.rejectOrg(op, org, err)
				return nil, nil
			}
			return rows, err
		})
	// childFanOut stores nothing when it fails, and an organization it skips
	// returned no rows.
	if err == nil && stored != nil {
		stored()
	}
	if isTransientNetworkError(err) {
		return total, inserted, skipIfTransient(s.st, op, s.acct.ID, s.region, err)
	}
	return total, inserted, err
}

// fanOutMailboxes is fanOut for the users and resources phases: list also
// returns the ENABLED entities among its rows, which are added to s.mailboxes
// once the phase has stored its rows.
func (s *workMailScan) fanOutMailboxes(ctx context.Context, op string, list func(context.Context, childParent) ([]*store.Resource, []childParent, error)) (int, int, error) {
	var (
		mu      sync.Mutex
		pending []workMailMailbox
	)
	return s.fanOutThen(ctx, op,
		func(ctx context.Context, org childParent) ([]*store.Resource, error) {
			rows, enabled, err := list(ctx, org)
			if err != nil {
				return nil, err
			}
			mu.Lock()
			defer mu.Unlock()
			for _, e := range enabled {
				pending = append(pending, workMailMailbox{org: org, entity: e})
			}
			return rows, nil
		},
		func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.mailboxes = append(s.mailboxes, pending...)
		})
}

// The NativeID kinds of the entities a mailbox permission names, shared by
// the scanner and the grantee resolver.
const (
	workMailUserKind     = "user"
	workMailGroupKind    = "group"
	workMailResourceKind = "resource"
)

// workMailChildNativeID keys a WorkMail child {parentARN}/{kind}/{id}.
func workMailChildNativeID(parentARN, kind, id string) string {
	return parentARN + "/" + kind + "/" + id
}

// workMailOrgARN returns the organization ARN that a WorkMail child NativeID
// ({orgARN}/{kind}/{id}...) starts with. An organization id holds no "/" (its
// model pattern is ^m-[0-9a-f]{32}$).
func workMailOrgARN(nativeID string) (orgARN string, ok bool) {
	const marker = ":organization/"
	prefix, path, ok := strings.Cut(nativeID, marker)
	if !ok {
		return "", false
	}
	orgID, _, ok := strings.Cut(path, "/")
	if !ok || orgID == "" {
		return "", false
	}
	return prefix + marker + orgID, true
}

// isWorkMailOrgGone matches an organization deleted between ListOrganizations
// and the child list call.
func isWorkMailOrgGone(err error) bool {
	return isAPIErrorCode(err, "OrganizationNotFoundException")
}

// row builds a child row keyed by workMailChildNativeID under parent: no
// WorkMail child list op returns an ARN.
func (s *workMailScan) row(typ string, parent childParent, kind, id string, name, status, createdAt *string, attrs any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: s.acct.ID, AccountName: &s.acct.Name,
		Type: typ, NativeID: workMailChildNativeID(parent.arn, kind, id),
		Name: name, Region: &s.region, Status: status, CreatedAt: createdAt,
		AttributesJSON: mustJSON(attrs), DiscoveredBy: s.scanID,
	}
}

// users stores every user except those in the DELETED state, which the
// EntityState enum lets ListUsers return; disabled users are kept.
func (s *workMailScan) users(ctx context.Context) (int, int, error) {
	return s.fanOutMailboxes(ctx, "workmail:ListUsers", func(ctx context.Context, org childParent) ([]*store.Resource, []childParent, error) {
		var (
			rows    []*store.Resource
			enabled []childParent
		)
		pager := workmail.NewListUsersPaginator(s.client, &workmail.ListUsersInput{OrganizationId: &org.id, MaxResults: sdkaws.Int32(workMailPageSize)})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, nil, err
			}
			for _, u := range out.Users {
				id := sv(u.Id)
				if id == "" || u.State == workmailtypes.EntityStateDeleted {
					continue
				}
				row := s.row(TypeWorkMailUser, org, workMailUserKind, id, u.Name, nonEmptyPtr(string(u.State)), nil, u)
				rows = append(rows, row)
				if u.State == workmailtypes.EntityStateEnabled {
					enabled = append(enabled, childParent{id: id, arn: row.NativeID})
				}
			}
		}
		return rows, enabled, nil
	})
}

// groups stores every group except deleted ones, as users does.
func (s *workMailScan) groups(ctx context.Context) (int, int, error) {
	return s.fanOut(ctx, "workmail:ListGroups", func(ctx context.Context, org childParent) ([]*store.Resource, error) {
		var rows []*store.Resource
		pager := workmail.NewListGroupsPaginator(s.client, &workmail.ListGroupsInput{OrganizationId: &org.id, MaxResults: sdkaws.Int32(workMailPageSize)})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, g := range out.Groups {
				id := sv(g.Id)
				if id == "" || g.State == workmailtypes.EntityStateDeleted {
					continue
				}
				rows = append(rows, s.row(TypeWorkMailGroup, org, workMailGroupKind, id, g.Name, nonEmptyPtr(string(g.State)), nil, g))
			}
		}
		return rows, nil
	})
}

// resources stores every resource (room or equipment) except deleted ones,
// as users does.
func (s *workMailScan) resources(ctx context.Context) (int, int, error) {
	return s.fanOutMailboxes(ctx, "workmail:ListResources", func(ctx context.Context, org childParent) ([]*store.Resource, []childParent, error) {
		var (
			rows    []*store.Resource
			enabled []childParent
		)
		pager := workmail.NewListResourcesPaginator(s.client, &workmail.ListResourcesInput{OrganizationId: &org.id, MaxResults: sdkaws.Int32(workMailPageSize)})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, nil, err
			}
			for _, r := range out.Resources {
				id := sv(r.Id)
				if id == "" || r.State == workmailtypes.EntityStateDeleted {
					continue
				}
				row := s.row(TypeWorkMailResource, org, workMailResourceKind, id, r.Name, nonEmptyPtr(string(r.State)), nil, r)
				rows = append(rows, row)
				if r.State == workmailtypes.EntityStateEnabled {
					enabled = append(enabled, childParent{id: id, arn: row.NativeID})
				}
			}
		}
		return rows, enabled, nil
	})
}

func (s *workMailScan) mailDomains(ctx context.Context) (int, int, error) {
	return s.fanOut(ctx, "workmail:ListMailDomains", func(ctx context.Context, org childParent) ([]*store.Resource, error) {
		var rows []*store.Resource
		pager := workmail.NewListMailDomainsPaginator(s.client, &workmail.ListMailDomainsInput{OrganizationId: &org.id, MaxResults: sdkaws.Int32(workMailPageSize)})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, d := range out.MailDomains {
				name := sv(d.DomainName)
				if name == "" {
					continue
				}
				rows = append(rows, s.row(TypeWorkMailMailDomain, org, "maildomain", name, d.DomainName, nil, nil, d))
			}
		}
		return rows, nil
	})
}

// accessControlRules keys each rule by its Name, the only identifier the API
// gives it (unique within the organization). The op is not paginated.
func (s *workMailScan) accessControlRules(ctx context.Context) (int, int, error) {
	return s.fanOut(ctx, "workmail:ListAccessControlRules", func(ctx context.Context, org childParent) ([]*store.Resource, error) {
		out, err := s.client.ListAccessControlRules(ctx, &workmail.ListAccessControlRulesInput{OrganizationId: &org.id})
		if err != nil {
			return nil, err
		}
		var rows []*store.Resource
		for _, r := range out.Rules {
			name := sv(r.Name)
			if name == "" {
				continue
			}
			rows = append(rows, s.row(TypeWorkMailAccessControlRule, org, "accesscontrolrule", name, r.Name, nil, tp(r.DateCreated), r))
		}
		return rows, nil
	})
}

// availabilityConfigurations keys each configuration by its DomainName (one
// per domain). The list element carries the EWS provider's endpoint and user
// name but never its password (RedactedEwsAvailabilityProvider).
func (s *workMailScan) availabilityConfigurations(ctx context.Context) (int, int, error) {
	return s.fanOut(ctx, "workmail:ListAvailabilityConfigurations", func(ctx context.Context, org childParent) ([]*store.Resource, error) {
		var rows []*store.Resource
		pager := workmail.NewListAvailabilityConfigurationsPaginator(s.client, &workmail.ListAvailabilityConfigurationsInput{OrganizationId: &org.id, MaxResults: sdkaws.Int32(workMailPageSize)})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, c := range out.AvailabilityConfigurations {
				domain := sv(c.DomainName)
				if domain == "" {
					continue
				}
				rows = append(rows, s.row(TypeWorkMailAvailabilityConfiguration, org, "availabilityconfiguration", domain, c.DomainName, nil, tp(c.DateCreated), c))
			}
		}
		return rows, nil
	})
}

func (s *workMailScan) impersonationRoles(ctx context.Context) (int, int, error) {
	return s.fanOut(ctx, "workmail:ListImpersonationRoles", func(ctx context.Context, org childParent) ([]*store.Resource, error) {
		var rows []*store.Resource
		pager := workmail.NewListImpersonationRolesPaginator(s.client, &workmail.ListImpersonationRolesInput{OrganizationId: &org.id, MaxResults: sdkaws.Int32(workMailPageSize)})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, r := range out.Roles {
				id := sv(r.ImpersonationRoleId)
				if id == "" {
					continue
				}
				rows = append(rows, s.row(TypeWorkMailImpersonationRole, org, "impersonationrole", id, r.Name, nil, tp(r.DateCreated), r))
			}
		}
		return rows, nil
	})
}

// mobileDeviceAccessRules lists each organization's rules; the op is not
// paginated.
func (s *workMailScan) mobileDeviceAccessRules(ctx context.Context) (int, int, error) {
	return s.fanOut(ctx, "workmail:ListMobileDeviceAccessRules", func(ctx context.Context, org childParent) ([]*store.Resource, error) {
		out, err := s.client.ListMobileDeviceAccessRules(ctx, &workmail.ListMobileDeviceAccessRulesInput{OrganizationId: &org.id})
		if err != nil {
			return nil, err
		}
		var rows []*store.Resource
		for _, r := range out.Rules {
			id := sv(r.MobileDeviceAccessRuleId)
			if id == "" {
				continue
			}
			rows = append(rows, s.row(TypeWorkMailMobileDeviceAccessRule, org, "mobiledeviceaccessrule", id, r.Name, nil, tp(r.DateCreated), r))
		}
		return rows, nil
	})
}

// mobileDeviceAccessOverrides lists every override in the organization (the
// user and device filters are optional) and keys each by its user and device,
// the pair the API identifies an override by.
func (s *workMailScan) mobileDeviceAccessOverrides(ctx context.Context) (int, int, error) {
	return s.fanOut(ctx, "workmail:ListMobileDeviceAccessOverrides", func(ctx context.Context, org childParent) ([]*store.Resource, error) {
		var rows []*store.Resource
		pager := workmail.NewListMobileDeviceAccessOverridesPaginator(s.client, &workmail.ListMobileDeviceAccessOverridesInput{OrganizationId: &org.id, MaxResults: sdkaws.Int32(workMailPageSize)})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, o := range out.Overrides {
				user, device := sv(o.UserId), sv(o.DeviceId)
				if user == "" || device == "" {
					continue
				}
				rows = append(rows, s.row(TypeWorkMailMobileDeviceAccessOverride, org, "mobiledeviceaccessoverride", user+"/"+device, nil, nil, tp(o.DateCreated), o))
			}
		}
		return rows, nil
	})
}

// personalAccessTokens lists every token in the organization (the user filter
// is optional). The summary carries no token secret.
func (s *workMailScan) personalAccessTokens(ctx context.Context) (int, int, error) {
	return s.fanOut(ctx, "workmail:ListPersonalAccessTokens", func(ctx context.Context, org childParent) ([]*store.Resource, error) {
		var rows []*store.Resource
		pager := workmail.NewListPersonalAccessTokensPaginator(s.client, &workmail.ListPersonalAccessTokensInput{OrganizationId: &org.id, MaxResults: sdkaws.Int32(workMailPageSize)})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			for _, t := range out.PersonalAccessTokenSummaries {
				id := sv(t.PersonalAccessTokenId)
				if id == "" {
					continue
				}
				rows = append(rows, s.row(TypeWorkMailPersonalAccessToken, org, "personalaccesstoken", id, t.Name, nil, tp(t.DateCreated), t))
			}
		}
		return rows, nil
	})
}

// mailboxPermissions stores one row per grantee of each mailbox, keyed
// {mailboxNativeID}/mailboxpermission/{granteeID} because the API returns no
// ARN. The granted permissions (FULL_ACCESS, SEND_AS, SEND_ON_BEHALF) are left
// out of the key, so a change to them is a change on the grantee's row.
//
// The mailboxes are the ENABLED users and resources the users and resources
// phases stored (EntityId takes a user or resource id, not a group), outside
// the organizations a phase saw rejected. DISABLED entities are not asked:
// whether the op answers for a deregistered entity is unobserved, and its
// grants cannot be used while it is DISABLED. The phase makes one call per
// page of each mailbox's grants, within the service's shared serviceTimeout;
// running out of it is a hard error and, like any other, drops every
// organization's grants.
//
// A mailbox deleted, or in an organization deleted, since it was listed is
// skipped silently (EntityNotFoundException, OrganizationNotFoundException).
// A mailbox whose organization's state refuses the call
// (OrganizationStateException) is skipped with one warning for the phase, as
// is one whose call still fails transiently after the SDK's retries. Every
// other error fails the phase, EntityStateException included, as the op does
// not model it; since childFanOut stores nothing after a hard error, one such
// error (say, for a user disabled between its listing and this call) drops
// the grants of every organization in the region this scan.
func (s *workMailScan) mailboxPermissions(ctx context.Context) (int, int, error) {
	const op = "workmail:ListMailboxPermissions"
	mailboxes, orgOf := s.listableMailboxes()
	isGone := func(err error) bool {
		return isAPIErrorCode(err, "EntityNotFoundException") || isWorkMailOrgGone(err)
	}
	var orgState skipTally
	total, inserted, err := childFanOutSkipTransient(ctx, s.st, s.acct, s.region, op, mailboxes, isGone, byParentARN,
		func(ctx context.Context, mailbox childParent) ([]*store.Resource, error) {
			rows, err := s.listMailboxPermissions(ctx, orgOf[mailbox.arn], mailbox)
			if orgState.dropCode("OrganizationStateException", mailbox.arn, err) {
				return nil, nil
			}
			return rows, err
		})
	orgState.warnCode(s.st, op, s.acct, s.region, "OrganizationStateException", "mailbox(es)",
		"the organization's state (for example, being created or deleted) can refuse the call")
	return total, inserted, err
}

// listableMailboxes returns s.mailboxes outside the rejected organizations,
// as fan-out parents, and the organization of each keyed by its NativeID.
func (s *workMailScan) listableMailboxes() ([]childParent, map[string]childParent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var parents []childParent
	orgOf := map[string]childParent{}
	for _, m := range s.mailboxes {
		if s.rejected[m.org.id] {
			continue
		}
		parents = append(parents, m.entity)
		orgOf[m.entity.arn] = m.org
	}
	return parents, orgOf
}

func (s *workMailScan) listMailboxPermissions(ctx context.Context, org, mailbox childParent) ([]*store.Resource, error) {
	var rows []*store.Resource
	pager := workmail.NewListMailboxPermissionsPaginator(s.client, &workmail.ListMailboxPermissionsInput{
		OrganizationId: &org.id, EntityId: &mailbox.id, MaxResults: sdkaws.Int32(workMailPageSize),
	})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range out.Permissions {
			grantee := sv(p.GranteeId)
			if grantee == "" {
				continue
			}
			rows = append(rows, s.row(TypeWorkMailMailboxPermission, mailbox, "mailboxpermission", grantee, &grantee, nil, nil, p))
		}
	}
	return rows, nil
}
