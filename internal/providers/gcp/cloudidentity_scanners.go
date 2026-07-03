package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	directory "google.golang.org/api/admin/directory/v1"
	"google.golang.org/api/cloudidentity/v1"
)

func init() {
	registerOrgService(orgServiceEntry{
		name: "gcp:cloudidentity",
		fn:   scanCloudIdentity,
		emits: []coverage.TypeDecl{
			{Service: "admin", DiscoType: TypeWorkspaceUser},
			{Service: "cloudidentity", DiscoType: TypeCloudIdentityGroup},
		},
	})
}

// scanCloudIdentity discovers Workspace Directory users + Cloud Identity
// groups under the calling identity's customer (Workspace tenant). Tenant-
// scope, runs once per scan via the org-service lane (folder/org orgScope
// arg ignored — Cloud Identity is parented above the GCP org tree).
//
// AccountID for emitted resources = the customer ID (e.g. "C03az79cb"),
// resolved from the first Directory.Users.List page or via the standard
// `customers/my_customer` alias for groups. NativeID = full resource name
// (`users/{id}` for directory users, `groups/{id}` for Cloud Identity).
//
// Failure modes:
//   - No Workspace tenant attached to the credential → both phases 403.
//     Reported as a single warning per phase, scan continues.
//   - Insufficient OAuth scope (cloud-identity.groups.readonly /
//     admin.directory.user.readonly) → 403 with reason `insufficientPermissions`,
//     same warning path.
//   - API not enabled in the consumer project (rare for these tenant APIs) →
//     `errServiceDisabled` sentinel, scan continues with `(project: disabled)`.
func scanCloudIdentity(ctx context.Context, _ []orgScope, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})

	// Phase 1: Workspace Directory users (`customer=my_customer` alias resolves
	// to the calling identity's customer; no need to know the customer ID up
	// front). Returns CustomerId on each user — captured for phase 2.
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

	// Phase 2: Cloud Identity groups parented at customers/{customerID}. If
	// phase 1 failed to determine a customer ID (no users, all 403) skip groups.
	if customerID == "" {
		return total, inserted, nil
	}
	ciSvc, gerr := cloudidentity.NewService(ctx, opts...)
	if gerr != nil {
		return total, inserted, fmt.Errorf("cloudidentity client: %w", gerr)
	}
	t, n, err = scanCloudIdentityGroups(ctx, ciSvc, customerID, st, scanID)
	total += t
	inserted += n
	return total, inserted, err
}

// scanWorkspaceUsers paginates over admin/directory/v1 Users.List with the
// `my_customer` alias. Returns the resolved customer ID along with counts so
// scanCloudIdentity can hand it off to the groups phase.
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
// resolver wins (R5 GCP non-SA member edges).
func scanCloudIdentityGroups(ctx context.Context, svc *cloudidentity.Service, customerID string, st *store.Store, scanID string) (total, inserted int, err error) {
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
		}
		return nil
	}); err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "cloudidentity:groups.list", parent, err)
		}
		return 0, 0, err
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert cloud-identity groups: %w", uerr)
	}
	return len(batch), n, nil
}
