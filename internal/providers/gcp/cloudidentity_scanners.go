package gcp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"codeberg.org/icearp/disco/internal/redact"
	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	directory "google.golang.org/api/admin/directory/v1"
	"google.golang.org/api/cloudidentity/v1"
)

func init() {
	registerType(restype.Descriptor{Type: TypeWorkspaceUser, Service: "admin", Upstream: "admin.googleapis.com/User", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudIdentityGroup, Service: "cloudidentity", Upstream: "cloudidentity.googleapis.com/Group", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudIdentityDevice, Service: "cloudidentity", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudIdentityDeviceUser, Service: "cloudidentity"})
	registerType(restype.Descriptor{Type: TypeCloudIdentityClientState, Service: "cloudidentity", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudIdentityMembership, Service: "cloudidentity"})
	registerType(restype.Descriptor{Type: TypeCloudIdentityInboundOidcSsoProfile, Service: "cloudidentity", Leaf: true, Redact: []redact.Rule{{Path: "rpConfig.clientSecret", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeCloudIdentityInboundSamlSsoProfile, Service: "cloudidentity", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudIdentityIdpCredential, Service: "cloudidentity", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCloudIdentityInboundSsoAssignment, Service: "cloudidentity"})
	registerType(restype.Descriptor{Type: TypeCloudIdentityPolicy, Service: "cloudidentity"})
	registerType(restype.Descriptor{Type: TypeCloudIdentityUserinvitation, Service: "cloudidentity", Leaf: true})
	registerOrgService(orgServiceEntry{
		name: "gcp:cloudidentity",
		fn:   scanCloudIdentity,
	})
}

// maxConcurrentCloudIdentityFanout caps per-group Membership and
// per-SAML-profile IdpCredential fan-out. Tenant-scoped, low cardinality —
// keep modest like DNS's per-zone fan-out.
const maxConcurrentCloudIdentityFanout = 10

// scanCloudIdentity discovers Workspace Directory users + Cloud Identity
// groups, devices, memberships, inbound SSO configuration, policies, and
// user invitations under the calling identity's customer (Workspace
// tenant). Tenant-scoped, runs once per scan via the org-service lane
// (folder/org orgScope arg ignored — Cloud Identity is parented above the
// GCP org tree).
//
//  1. Workspace Directory Users.List — resolves customerID for every later
//     phase.
//  2. Groups.List, parented at customers/{customerID}. Group refs collected
//     for phase 4's per-group Membership fan-out.
//  3. Devices.List, DeviceUsers.List (wildcard `devices/-`), ClientStates.List
//     (wildcard `devices/-/deviceUsers/-`) — DeviceUser/ClientState
//     closure-parent to their owning Device/DeviceUser, derived by trimming
//     the trailing path segment off their own resource name (no per-device
//     fan-out needed — the wildcard parent returns every device's children
//     in one paginated walk).
//  4. Memberships.List, fan-out per already-scanned Group (no wildcard
//     parent support — GCP requires an explicit `groups/{group}`).
//  5. InboundOidcSsoProfiles.List, InboundSamlSsoProfiles.List (both default
//     to the caller's own customer when no filter is set — this scanner
//     never targets any other customer, so the filter is omitted rather
//     than hand-built as a CEL expression). IdpCredentials.List fans out per
//     already-listed SAML profile.
//  6. InboundSsoAssignments.List, Policies.List — same no-filter default.
//  7. Userinvitations.List, parented at customers/{customerID} (required
//     path param, no default).
//
// AccountID for every emitted resource = the customer ID (e.g. "C03az79cb").
// NativeID = full resource name for every type (all ten SDK types return
// one — no synthesized keys needed this wave, unlike DNS's DnsKey).
//
// Failure modes:
//   - No Workspace tenant attached to the credential → every phase 403.
//     Reported as a warning per phase, scan continues.
//   - Insufficient OAuth scope → 403 with reason `insufficientPermissions`,
//     same warning path.
//   - API not enabled in the consumer project (rare for these tenant APIs) →
//     `errServiceDisabled` sentinel, scan continues with `(project: disabled)`.
func scanCloudIdentity(ctx context.Context, _ []orgScope, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})

	// Phase 1: Workspace Directory users (`customer=my_customer` alias resolves
	// to the calling identity's customer, no customer ID needed upfront).
	// Returns CustomerId per user — captured for every later phase.
	dirSvc, derr := directory.NewService(ctx, opts...)
	if derr != nil {
		return 0, 0, fmt.Errorf("admin/directory client: %w", derr)
	}
	customerID, t, n, err := scanWorkspaceUsers(ctx, dirSvc, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Phases 2+ need a resolved customer ID (no users, all 403 → skip).
	if customerID == "" {
		return total, inserted, nil
	}
	ciSvc, gerr := cloudidentity.NewService(ctx, opts...)
	if gerr != nil {
		return total, inserted, fmt.Errorf("cloudidentity client: %w", gerr)
	}

	// Phase 2: Cloud Identity groups.
	groupNames, t, n, err := scanCloudIdentityGroups(ctx, ciSvc, customerID, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Phase 3: devices, device users, client states.
	t, n, err = scanCloudIdentityDevices(ctx, ciSvc, customerID, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanCloudIdentityDeviceUsers(ctx, ciSvc, customerID, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanCloudIdentityClientStates(ctx, ciSvc, customerID, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Phase 4: memberships, fan-out per already-scanned group.
	t, n, err = scanCloudIdentityMemberships(ctx, ciSvc, customerID, groupNames, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Phase 5: inbound SSO profiles + per-SAML-profile IdP credentials.
	t, n, err = scanCloudIdentityInboundOidcSsoProfiles(ctx, ciSvc, customerID, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	samlProfileNames, t, n, err := scanCloudIdentityInboundSamlSsoProfiles(ctx, ciSvc, customerID, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanCloudIdentityIdpCredentials(ctx, ciSvc, customerID, samlProfileNames, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Phase 6: inbound SSO assignments + policies.
	t, n, err = scanCloudIdentityInboundSsoAssignments(ctx, ciSvc, customerID, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanCloudIdentityPolicies(ctx, ciSvc, customerID, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}

	// Phase 7: user invitations.
	t, n, err = scanCloudIdentityUserinvitations(ctx, ciSvc, customerID, st, scanID)
	total += t
	inserted += n
	return total, inserted, err
}

// scanWorkspaceUsers paginates over admin/directory/v1 Users.List with the
// `my_customer` alias. Returns the resolved customer ID plus counts so
// scanCloudIdentity can pass it to the groups phase.
func scanWorkspaceUsers(ctx context.Context, svc *directory.Service, st *store.Store, scanID string) (customerID string, total, inserted int, err error) {
	var batch []*store.Resource
	req := svc.Users.List().Customer("my_customer").MaxResults(500)
	if err := req.Pages(ctx, func(page *directory.Users) error {
		for _, u := range page.Users {
			if u == nil || u.Id == "" {
				continue
			}
			if customerID == "" && u.CustomerId != "" {
				customerID = u.CustomerId
			}
			name := u.PrimaryEmail
			nativeID := "users/" + u.Id
			r := &store.Resource{
				Provider:       "gcp",
				Region:         regionGlobal,
				AccountID:      u.CustomerId,
				Type:           TypeWorkspaceUser,
				NativeID:       nativeID,
				Name:           &name,
				AttributesJSON: mustJSON(u),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
		}
		return nil
	}); err != nil {
		if isPermissionDenied(err) {
			return customerID, 0, 0, skipIfDenied(st, "admin:directory.users.list", "tenant", err)
		}
		return customerID, 0, 0, err
	}
	if len(batch) == 0 {
		return customerID, 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return customerID, 0, 0, fmt.Errorf("upsert workspace users: %w", uerr)
	}
	return customerID, len(batch), n, nil
}

// scanCloudIdentityGroups paginates over cloudidentity/v1 Groups.List with
// `parent=customers/{customerID}` and view=BASIC. View=FULL would surface
// dynamic-group queries + extra metadata; BASIC is enough for the FK-by-email
// resolver wins (R5 GCP non-SA member edges). Returns every scanned group's
// resource name (`groups/{id}`) so scanCloudIdentityMemberships can fan out
// per group.
func scanCloudIdentityGroups(ctx context.Context, svc *cloudidentity.Service, customerID string, st *store.Store, scanID string) (groupNames []string, total, inserted int, err error) {
	var batch []*store.Resource
	parent := "customers/" + customerID
	req := svc.Groups.List().Parent(parent).View("BASIC").PageSize(500)
	if err := req.Pages(ctx, func(page *cloudidentity.ListGroupsResponse) error {
		for _, g := range page.Groups {
			if g == nil || g.Name == "" {
				continue
			}
			// g.Name is `groups/{id}` — the canonical resource name. Use as NativeID.
			displayName := g.DisplayName
			email := ""
			if g.GroupKey != nil {
				email = g.GroupKey.Id
			}
			label := displayName
			if label == "" {
				label = email
			}
			r := &store.Resource{
				Provider:       "gcp",
				Region:         regionGlobal,
				AccountID:      customerID,
				Type:           TypeCloudIdentityGroup,
				NativeID:       g.Name,
				Name:           &label,
				AttributesJSON: mustJSON(g),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
			groupNames = append(groupNames, g.Name)
		}
		return nil
	}); err != nil {
		if isPermissionDenied(err) {
			return nil, 0, 0, skipIfDenied(st, "cloudidentity:groups.list", parent, err)
		}
		return nil, 0, 0, err
	}
	if len(batch) == 0 {
		return groupNames, 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return nil, 0, 0, fmt.Errorf("upsert cloud-identity groups: %w", uerr)
	}
	return groupNames, len(batch), n, nil
}

// scanCloudIdentityDevices paginates over Devices.List, customer-scoped, no
// closure parent (flat tenant resource, like Group/WorkspaceUser).
func scanCloudIdentityDevices(ctx context.Context, svc *cloudidentity.Service, customerID string, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	req := svc.Devices.List().Customer("customers/" + customerID).PageSize(100)
	if err := req.Pages(ctx, func(page *cloudidentity.GoogleAppsCloudidentityDevicesV1ListDevicesResponse) error {
		for _, d := range page.Devices {
			if d == nil || d.Name == "" {
				continue
			}
			label := d.Model
			if label == "" {
				label = d.SerialNumber
			}
			if label == "" {
				label = d.DeviceId
			}
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				Region:         regionGlobal,
				AccountID:      customerID,
				Type:           TypeCloudIdentityDevice,
				NativeID:       d.Name,
				Name:           &label,
				CreatedAt:      strp(d.CreateTime),
				AttributesJSON: mustJSON(d),
				DiscoveredBy:   scanID,
			})
		}
		return nil
	}); err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "cloudidentity:devices.list", customerID, err)
		}
		return 0, 0, err
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert cloud-identity devices: %w", uerr)
	}
	return len(batch), n, nil
}

// scanCloudIdentityDeviceUsers paginates over DeviceUsers.List with the
// wildcard parent `devices/-` — returns every device's users across the
// customer in one paginated walk rather than fanning out per already-scanned
// Device. Each row closure-parents to its owning Device, derived by trimming
// the `/deviceUsers/{id}` suffix off the row's own resource name (KMS-style
// multi-parent batch — parent varies per row within the same page).
func scanCloudIdentityDeviceUsers(ctx context.Context, svc *cloudidentity.Service, customerID string, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var pairs [][2]string
	req := svc.Devices.DeviceUsers.List("devices/-").Customer("customers/" + customerID).PageSize(100)
	if err := req.Pages(ctx, func(page *cloudidentity.GoogleAppsCloudidentityDevicesV1ListDeviceUsersResponse) error {
		for _, du := range page.DeviceUsers {
			if du == nil || du.Name == "" {
				continue
			}
			deviceNativeID, _, ok := strings.Cut(du.Name, "/deviceUsers/")
			if !ok {
				continue
			}
			label := du.UserEmail
			r := &store.Resource{
				Provider:       "gcp",
				Region:         regionGlobal,
				AccountID:      customerID,
				Type:           TypeCloudIdentityDeviceUser,
				NativeID:       du.Name,
				Name:           &label,
				CreatedAt:      strp(du.CreateTime),
				AttributesJSON: mustJSON(du),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
			childID := store.ResourceID("gcp", customerID, TypeCloudIdentityDeviceUser, du.Name)
			parentID := store.ResourceID("gcp", customerID, TypeCloudIdentityDevice, deviceNativeID)
			pairs = append(pairs, [2]string{childID, parentID})
		}
		return nil
	}); err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "cloudidentity:deviceUsers.list", customerID, err)
		}
		return 0, 0, err
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert cloud-identity device users: %w", uerr)
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return len(batch), n, fmt.Errorf("hierarchy cloud-identity device users: %w", err)
	}
	return len(batch), n, nil
}

// scanCloudIdentityClientStates paginates over ClientStates.List with the
// wildcard parent `devices/-/deviceUsers/-`, closure-parenting each row to
// its owning DeviceUser (same multi-parent-batch shape as
// scanCloudIdentityDeviceUsers, one level deeper).
func scanCloudIdentityClientStates(ctx context.Context, svc *cloudidentity.Service, customerID string, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var pairs [][2]string
	req := svc.Devices.DeviceUsers.ClientStates.List("devices/-/deviceUsers/-").Customer("customers/" + customerID)
	if err := req.Pages(ctx, func(page *cloudidentity.GoogleAppsCloudidentityDevicesV1ListClientStatesResponse) error {
		for _, cs := range page.ClientStates {
			if cs == nil || cs.Name == "" {
				continue
			}
			deviceUserNativeID, _, ok := strings.Cut(cs.Name, "/clientStates/")
			if !ok {
				continue
			}
			label := cs.OwnerType
			if label == "" {
				label = cs.CustomId
			}
			r := &store.Resource{
				Provider:       "gcp",
				Region:         regionGlobal,
				AccountID:      customerID,
				Type:           TypeCloudIdentityClientState,
				NativeID:       cs.Name,
				Name:           &label,
				CreatedAt:      strp(cs.CreateTime),
				AttributesJSON: mustJSON(cs),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
			childID := store.ResourceID("gcp", customerID, TypeCloudIdentityClientState, cs.Name)
			parentID := store.ResourceID("gcp", customerID, TypeCloudIdentityDeviceUser, deviceUserNativeID)
			pairs = append(pairs, [2]string{childID, parentID})
		}
		return nil
	}); err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "cloudidentity:clientStates.list", customerID, err)
		}
		return 0, 0, err
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert cloud-identity client states: %w", uerr)
	}
	if err := st.RecordHierarchyBatch(pairs); err != nil {
		return len(batch), n, fmt.Errorf("hierarchy cloud-identity client states: %w", err)
	}
	return len(batch), n, nil
}

// scanCloudIdentityMemberships fans out Memberships.List per already-scanned
// Group — GCP requires an explicit `groups/{group}` parent, no wildcard.
func scanCloudIdentityMemberships(ctx context.Context, svc *cloudidentity.Service, customerID string, groupNames []string, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if err := forEachItem(ctx, maxConcurrentCloudIdentityFanout, groupNames, func(gctx context.Context, groupName string) error {
		groupID := store.ResourceID("gcp", customerID, TypeCloudIdentityGroup, groupName)
		var batch []*store.Resource
		listErr := svc.Groups.Memberships.List(groupName).PageSize(200).Pages(gctx, func(page *cloudidentity.ListMembershipsResponse) error {
			for _, m := range page.Memberships {
				if m == nil || m.Name == "" {
					continue
				}
				label := ""
				if m.PreferredMemberKey != nil {
					label = m.PreferredMemberKey.Id
				}
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					Region:         regionGlobal,
					AccountID:      customerID,
					Type:           TypeCloudIdentityMembership,
					NativeID:       m.Name,
					Name:           &label,
					CreatedAt:      strp(m.CreateTime),
					AttributesJSON: mustJSON(m),
					DiscoveredBy:   scanID,
				})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "cloudidentity:memberships.list", groupName, listErr)
			}
			return listErr
		}
		mu.Lock()
		defer mu.Unlock()
		t, n, uerr := upsertWithParent(st, batch, groupID)
		total += t
		inserted += n
		return uerr
	}); err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanCloudIdentityInboundOidcSsoProfiles paginates over
// InboundOidcSsoProfiles.List with no filter — omitting it defaults to the
// caller's own customer, which is exactly this scanner's single-tenant
// scope, so no hand-built CEL filter expression is needed. Flat tenant
// resource, no closure parent.
func scanCloudIdentityInboundOidcSsoProfiles(ctx context.Context, svc *cloudidentity.Service, customerID string, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	req := svc.InboundOidcSsoProfiles.List().PageSize(100)
	if err := req.Pages(ctx, func(page *cloudidentity.ListInboundOidcSsoProfilesResponse) error {
		for _, p := range page.InboundOidcSsoProfiles {
			if p == nil || p.Name == "" {
				continue
			}
			label := p.DisplayName
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				Region:         regionGlobal,
				AccountID:      customerID,
				Type:           TypeCloudIdentityInboundOidcSsoProfile,
				NativeID:       p.Name,
				Name:           &label,
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			})
		}
		return nil
	}); err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "cloudidentity:inboundOidcSsoProfiles.list", customerID, err)
		}
		return 0, 0, err
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert cloud-identity inbound OIDC SSO profiles: %w", uerr)
	}
	return len(batch), n, nil
}

// scanCloudIdentityInboundSamlSsoProfiles paginates over
// InboundSamlSsoProfiles.List (same no-filter default-customer rationale as
// the OIDC phase). Returns every scanned profile's resource name so
// scanCloudIdentityIdpCredentials can fan out per profile.
func scanCloudIdentityInboundSamlSsoProfiles(ctx context.Context, svc *cloudidentity.Service, customerID string, st *store.Store, scanID string) (profileNames []string, total, inserted int, err error) {
	var batch []*store.Resource
	req := svc.InboundSamlSsoProfiles.List().PageSize(100)
	if err := req.Pages(ctx, func(page *cloudidentity.ListInboundSamlSsoProfilesResponse) error {
		for _, p := range page.InboundSamlSsoProfiles {
			if p == nil || p.Name == "" {
				continue
			}
			label := p.DisplayName
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				Region:         regionGlobal,
				AccountID:      customerID,
				Type:           TypeCloudIdentityInboundSamlSsoProfile,
				NativeID:       p.Name,
				Name:           &label,
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			})
			profileNames = append(profileNames, p.Name)
		}
		return nil
	}); err != nil {
		if isPermissionDenied(err) {
			return nil, 0, 0, skipIfDenied(st, "cloudidentity:inboundSamlSsoProfiles.list", customerID, err)
		}
		return nil, 0, 0, err
	}
	if len(batch) == 0 {
		return profileNames, 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return nil, 0, 0, fmt.Errorf("upsert cloud-identity inbound SAML SSO profiles: %w", uerr)
	}
	return profileNames, len(batch), n, nil
}

// scanCloudIdentityIdpCredentials fans out IdpCredentials.List per
// already-scanned InboundSamlSsoProfile — the API has no wildcard/flat list.
func scanCloudIdentityIdpCredentials(ctx context.Context, svc *cloudidentity.Service, customerID string, profileNames []string, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if err := forEachItem(ctx, maxConcurrentCloudIdentityFanout, profileNames, func(gctx context.Context, profileName string) error {
		profileID := store.ResourceID("gcp", customerID, TypeCloudIdentityInboundSamlSsoProfile, profileName)
		var batch []*store.Resource
		listErr := svc.InboundSamlSsoProfiles.IdpCredentials.List(profileName).Pages(gctx, func(page *cloudidentity.ListIdpCredentialsResponse) error {
			for _, c := range page.IdpCredentials {
				if c == nil || c.Name == "" {
					continue
				}
				label := c.Name
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					Region:         regionGlobal,
					AccountID:      customerID,
					Type:           TypeCloudIdentityIdpCredential,
					NativeID:       c.Name,
					Name:           &label,
					AttributesJSON: mustJSON(c),
					DiscoveredBy:   scanID,
				})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "cloudidentity:idpCredentials.list", profileName, listErr)
			}
			return listErr
		}
		mu.Lock()
		defer mu.Unlock()
		t, n, uerr := upsertWithParent(st, batch, profileID)
		total += t
		inserted += n
		return uerr
	}); err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanCloudIdentityInboundSsoAssignments paginates over
// InboundSsoAssignments.List (same no-filter default-customer rationale).
// Flat tenant resource, no closure parent.
func scanCloudIdentityInboundSsoAssignments(ctx context.Context, svc *cloudidentity.Service, customerID string, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	req := svc.InboundSsoAssignments.List().PageSize(100)
	if err := req.Pages(ctx, func(page *cloudidentity.ListInboundSsoAssignmentsResponse) error {
		for _, a := range page.InboundSsoAssignments {
			if a == nil || a.Name == "" {
				continue
			}
			label := a.Name
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				Region:         regionGlobal,
				AccountID:      customerID,
				Type:           TypeCloudIdentityInboundSsoAssignment,
				NativeID:       a.Name,
				Name:           &label,
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			})
		}
		return nil
	}); err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "cloudidentity:inboundSsoAssignments.list", customerID, err)
		}
		return 0, 0, err
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert cloud-identity inbound SSO assignments: %w", uerr)
	}
	return len(batch), n, nil
}

// scanCloudIdentityPolicies paginates over Policies.List (same no-filter
// default-customer rationale). Flat tenant resource, no closure parent.
func scanCloudIdentityPolicies(ctx context.Context, svc *cloudidentity.Service, customerID string, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	req := svc.Policies.List().PageSize(100)
	if err := req.Pages(ctx, func(page *cloudidentity.ListPoliciesResponse) error {
		for _, p := range page.Policies {
			if p == nil || p.Name == "" {
				continue
			}
			label := p.Type
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				Region:         regionGlobal,
				AccountID:      customerID,
				Type:           TypeCloudIdentityPolicy,
				NativeID:       p.Name,
				Name:           &label,
				AttributesJSON: mustJSON(p),
				DiscoveredBy:   scanID,
			})
		}
		return nil
	}); err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "cloudidentity:policies.list", customerID, err)
		}
		return 0, 0, err
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert cloud-identity policies: %w", uerr)
	}
	return len(batch), n, nil
}

// scanCloudIdentityUserinvitations paginates over Userinvitations.List,
// parented at customers/{customerID} — a required path param with no
// default, unlike the filter-based phases above. Flat tenant resource, no
// closure parent.
func scanCloudIdentityUserinvitations(ctx context.Context, svc *cloudidentity.Service, customerID string, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	parent := "customers/" + customerID
	req := svc.Customers.Userinvitations.List(parent).PageSize(100)
	if err := req.Pages(ctx, func(page *cloudidentity.ListUserInvitationsResponse) error {
		for _, u := range page.UserInvitations {
			if u == nil || u.Name == "" {
				continue
			}
			label := lastSegment(u.Name)
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				Region:         regionGlobal,
				AccountID:      customerID,
				Type:           TypeCloudIdentityUserinvitation,
				NativeID:       u.Name,
				Name:           &label,
				AttributesJSON: mustJSON(u),
				DiscoveredBy:   scanID,
			})
		}
		return nil
	}); err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "cloudidentity:userinvitations.list", parent, err)
		}
		return 0, 0, err
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert cloud-identity user invitations: %w", uerr)
	}
	return len(batch), n, nil
}
