package gcp

import (
	"context"
	"fmt"
	"sync"

	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"google.golang.org/api/iam/v1"
)

// Wave 8g of the GCP type-coverage buildout (docs/gcp-type-coverage.md),
// closes ROADMAP R4.23: IAM workforce/workload identity federation, OAuth
// clients, and custom roles.
//
// WorkforcePool is org-scoped (locations/workforcePools, Parent query param
// = organizations/{id}); WorkloadIdentityPool/OauthClient are project-scoped
// (projects/{p}/locations/global). Custom Role has both an org and a project
// List endpoint — scanIAMOrgRoles/scanIAMProjectRoles are separate small
// functions (distinct SDK service types, unlike CRM TagKeys which shares one
// List call across both scopes) but emit the same TypeIAMRole.
func init() {
	registerType(restype.Descriptor{Type: TypeIAMWorkforcePool, Service: "iam", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIAMProvider, Service: "iam", Leaf: true, Redact: []redact.Rule{{Path: "oidc.clientSecret.value.plainText", Mode: redact.RedactScalar}, {Path: "extendedAttributesOauth2Client.clientSecret.value.plainText", Mode: redact.RedactScalar}, {Path: "extraAttributesOauth2Client.clientSecret.value.plainText", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeIAMScimTenant, Service: "iam", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIAMRole, Service: "iam", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIAMWorkloadIdentityPool, Service: "iam", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIAMProvider, Service: "iam", Leaf: true, Redact: []redact.Rule{{Path: "oidc.clientSecret.value.plainText", Mode: redact.RedactScalar}, {Path: "extendedAttributesOauth2Client.clientSecret.value.plainText", Mode: redact.RedactScalar}, {Path: "extraAttributesOauth2Client.clientSecret.value.plainText", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeIAMNamespace, Service: "iam", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIAMManagedIdentity, Service: "iam", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIAMOauthClient, Service: "iam", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIAMCredential, Service: "iam", Leaf: true, Redact: []redact.Rule{{Path: "clientSecret", Mode: redact.RedactScalar}}})
	registerType(restype.Descriptor{Type: TypeIAMRole, Service: "iam", Leaf: true})
	registerOrgService(orgServiceEntry{
		name: "gcp:iam-org",
		fn:   scanIAMOrgScoped,
	})
	registerService(serviceEntry{
		name: "gcp:iam-project",
		fn:   scanIAMProjectScoped,
	})
}

// maxConcurrentIAMFanout caps per-pool Provider fan-out, per-provider
// ScimTenant fan-out, per-pool Namespace fan-out, per-namespace
// ManagedIdentity fan-out, and per-OauthClient Credential fan-out.
const maxConcurrentIAMFanout = 10

// scanIAMOrgScoped runs once per scan across every org scope (folders
// skipped — WorkforcePool and custom Role have no folder-level List
// endpoint): WorkforcePool → Provider → ScimTenant, plus org-scoped custom
// Roles.
func scanIAMOrgScoped(ctx context.Context, scopes []orgScope, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := iam.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("iam client: %w", err)
	}
	for _, sc := range scopes {
		if sc.Kind != "organization" {
			continue
		}
		t, n, poolNativeIDs, perr := scanIAMWorkforcePools(ctx, svc, sc, st, scanID)
		total += t
		inserted += n
		if perr != nil {
			return total, inserted, perr
		}
		t, n, providerNativeIDs, perr := scanIAMWorkforceProviders(ctx, svc, sc, poolNativeIDs, st, scanID)
		total += t
		inserted += n
		if perr != nil {
			return total, inserted, perr
		}
		t, n, perr = scanIAMWorkforceScimTenants(ctx, svc, sc, providerNativeIDs, st, scanID)
		total += t
		inserted += n
		if perr != nil {
			return total, inserted, perr
		}
		t, n, perr = scanIAMOrgRoles(ctx, svc, sc, st, scanID)
		total += t
		inserted += n
		if perr != nil {
			return total, inserted, perr
		}
	}
	return total, inserted, nil
}

// scanIAMWorkforcePools lists WorkforcePools for the given org scope
// (location fixed at "locations/global" — the only location this API
// supports today, same precedent as certificatemanager/cloudbuild). Returns
// every scanned pool's native ID so the Provider phase can fan out.
func scanIAMWorkforcePools(ctx context.Context, svc *iam.Service, sc orgScope, st *store.Store, scanID string) (total, inserted int, poolNativeIDs []string, err error) {
	var batch []*store.Resource
	listErr := svc.Locations.WorkforcePools.List("locations/global").Parent(sc.Name).Pages(ctx, func(page *iam.ListWorkforcePoolsResponse) error {
		for _, wp := range page.WorkforcePools {
			if wp == nil || wp.Name == "" {
				continue
			}
			label := wp.DisplayName
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				Region:         regionGlobal,
				AccountID:      sc.Name,
				Type:           TypeIAMWorkforcePool,
				NativeID:       wp.Name,
				Name:           &label,
				AttributesJSON: mustJSON(wp),
				DiscoveredBy:   scanID,
			})
			poolNativeIDs = append(poolNativeIDs, wp.Name)
		}
		return nil
	})
	if listErr != nil {
		if isPermissionDenied(listErr) {
			return 0, 0, nil, skipIfDenied(st, "iam:workforcePools.list", sc.Name, listErr)
		}
		return 0, 0, nil, listErr
	}
	if len(batch) == 0 {
		return 0, 0, poolNativeIDs, nil
	}
	t, n, uerr := upsertWithParent(st, batch, sc.Resource)
	if uerr != nil {
		return 0, 0, nil, fmt.Errorf("upsert IAM workforce pools: %w", uerr)
	}
	return t, n, poolNativeIDs, nil
}

// scanIAMWorkforceProviders fans out WorkforcePools.Providers.List per
// already-scanned pool. Returns every scanned provider's native ID so the
// ScimTenant phase can fan out.
func scanIAMWorkforceProviders(ctx context.Context, svc *iam.Service, sc orgScope, poolNativeIDs []string, st *store.Store, scanID string) (total, inserted int, providerNativeIDs []string, err error) {
	var mu sync.Mutex
	if err := forEachItem(ctx, maxConcurrentIAMFanout, poolNativeIDs, func(gctx context.Context, poolNativeID string) error {
		poolID := store.ResourceID("gcp", sc.Name, poolNativeID)
		var batch []*store.Resource
		var provNativeIDs []string
		listErr := svc.Locations.WorkforcePools.Providers.List(poolNativeID).Pages(gctx, func(page *iam.ListWorkforcePoolProvidersResponse) error {
			for _, wpp := range page.WorkforcePoolProviders {
				if wpp == nil || wpp.Name == "" {
					continue
				}
				label := wpp.DisplayName
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					Region:         regionGlobal,
					AccountID:      sc.Name,
					Type:           TypeIAMProvider,
					NativeID:       wpp.Name,
					Name:           &label,
					AttributesJSON: mustJSON(wpp),
					DiscoveredBy:   scanID,
				})
				provNativeIDs = append(provNativeIDs, wpp.Name)
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "iam:workforcePools.providers.list", poolNativeID, listErr)
			}
			return listErr
		}
		mu.Lock()
		defer mu.Unlock()
		t, n, uerr := upsertWithParent(st, batch, poolID)
		total += t
		inserted += n
		providerNativeIDs = append(providerNativeIDs, provNativeIDs...)
		return uerr
	}); err != nil {
		return total, inserted, nil, err
	}
	return total, inserted, providerNativeIDs, nil
}

// scanIAMWorkforceScimTenants fans out
// WorkforcePools.Providers.ScimTenants.List per already-scanned provider.
func scanIAMWorkforceScimTenants(ctx context.Context, svc *iam.Service, sc orgScope, providerNativeIDs []string, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if err := forEachItem(ctx, maxConcurrentIAMFanout, providerNativeIDs, func(gctx context.Context, providerNativeID string) error {
		providerID := store.ResourceID("gcp", sc.Name, providerNativeID)
		var batch []*store.Resource
		listErr := svc.Locations.WorkforcePools.Providers.ScimTenants.List(providerNativeID).Pages(gctx, func(page *iam.ListWorkforcePoolProviderScimTenantsResponse) error {
			for _, tenant := range page.WorkforcePoolProviderScimTenants {
				if tenant == nil || tenant.Name == "" {
					continue
				}
				label := tenant.DisplayName
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					Region:         regionGlobal,
					AccountID:      sc.Name,
					Type:           TypeIAMScimTenant,
					NativeID:       tenant.Name,
					Name:           &label,
					AttributesJSON: mustJSON(tenant),
					DiscoveredBy:   scanID,
				})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "iam:workforcePools.providers.scimTenants.list", providerNativeID, listErr)
			}
			return listErr
		}
		mu.Lock()
		defer mu.Unlock()
		t, n, uerr := upsertWithParent(st, batch, providerID)
		total += t
		inserted += n
		return uerr
	}); err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanIAMOrgRoles lists organization-scoped custom roles.
// OrganizationsRolesService.List only ever returns custom roles — the
// predefined-role catalog is a separate top-level RolesService this scanner
// never calls.
func scanIAMOrgRoles(ctx context.Context, svc *iam.Service, sc orgScope, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	parent := sc.Name
	listErr := svc.Organizations.Roles.List(parent).Pages(ctx, func(page *iam.ListRolesResponse) error {
		for _, r := range page.Roles {
			if r == nil || r.Name == "" {
				continue
			}
			label := r.Title
			if label == "" {
				label = lastSegment(r.Name)
			}
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				Region:         regionGlobal,
				AccountID:      sc.Name,
				Type:           TypeIAMRole,
				NativeID:       r.Name,
				Name:           &label,
				AttributesJSON: mustJSON(r),
				DiscoveredBy:   scanID,
			})
		}
		return nil
	})
	if listErr != nil {
		if isPermissionDenied(listErr) {
			return 0, 0, skipIfDenied(st, "iam:organizations.roles.list", parent, listErr)
		}
		return 0, 0, listErr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	t, n, uerr := upsertWithParent(st, batch, sc.Resource)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert IAM org roles: %w", uerr)
	}
	return t, n, nil
}

// scanIAMProjectScoped runs once per project: WorkloadIdentityPool →
// Provider, WorkloadIdentityPool → Namespace → ManagedIdentity, OauthClient
// → Credential, and project-scoped custom Roles.
func scanIAMProjectScoped(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := iam.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("iam client: %w", err)
	}

	t, n, poolNativeIDs, err := scanIAMWorkloadIdentityPools(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanIAMWorkloadIdentityProviders(ctx, svc, p, poolNativeIDs, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, namespaceNativeIDs, err := scanIAMWorkloadIdentityNamespaces(ctx, svc, p, poolNativeIDs, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanIAMWorkloadIdentityManagedIdentities(ctx, svc, p, namespaceNativeIDs, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, oauthClientNativeIDs, err := scanIAMOauthClients(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanIAMOauthClientCredentials(ctx, svc, p, oauthClientNativeIDs, st, scanID)
	total += t
	inserted += n
	if err != nil {
		return total, inserted, err
	}
	t, n, err = scanIAMProjectRoles(ctx, svc, p, st, scanID)
	total += t
	inserted += n
	return total, inserted, err
}

// scanIAMWorkloadIdentityPools lists WorkloadIdentityPools for the project
// (location fixed at "locations/global"). Returns every scanned pool's
// native ID so the Provider and Namespace phases can fan out.
func scanIAMWorkloadIdentityPools(ctx context.Context, svc *iam.Service, p *project, st *store.Store, scanID string) (total, inserted int, poolNativeIDs []string, err error) {
	parent := fmt.Sprintf("projects/%s/locations/global", p.ID)
	var batch []*store.Resource
	listErr := svc.Projects.Locations.WorkloadIdentityPools.List(parent).Pages(ctx, func(page *iam.ListWorkloadIdentityPoolsResponse) error {
		for _, wip := range page.WorkloadIdentityPools {
			if wip == nil || wip.Name == "" {
				continue
			}
			label := wip.DisplayName
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeIAMWorkloadIdentityPool,
				NativeID:       wip.Name,
				Name:           &label,
				AttributesJSON: mustJSON(wip),
				DiscoveredBy:   scanID,
			})
			poolNativeIDs = append(poolNativeIDs, wip.Name)
		}
		return nil
	})
	if listErr != nil {
		if isPermissionDenied(listErr) {
			return 0, 0, nil, skipIfDenied(st, "iam:workloadIdentityPools.list", parent, listErr)
		}
		return 0, 0, nil, listErr
	}
	if len(batch) == 0 {
		return 0, 0, poolNativeIDs, nil
	}
	t, n, uerr := upsertWithProjClosure(p, st, batch)
	if uerr != nil {
		return 0, 0, nil, fmt.Errorf("upsert IAM workload identity pools: %w", uerr)
	}
	return t, n, poolNativeIDs, nil
}

// scanIAMWorkloadIdentityProviders fans out
// WorkloadIdentityPools.Providers.List per already-scanned pool. Shares
// TypeIAMProvider with the workforce-side scanner (see TypeIAMProvider's doc
// comment in gcp_types.go).
func scanIAMWorkloadIdentityProviders(ctx context.Context, svc *iam.Service, p *project, poolNativeIDs []string, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if err := forEachItem(ctx, maxConcurrentIAMFanout, poolNativeIDs, func(gctx context.Context, poolNativeID string) error {
		poolID := store.ResourceID("gcp", p.ID, poolNativeID)
		var batch []*store.Resource
		listErr := svc.Projects.Locations.WorkloadIdentityPools.Providers.List(poolNativeID).Pages(gctx, func(page *iam.ListWorkloadIdentityPoolProvidersResponse) error {
			for _, wipp := range page.WorkloadIdentityPoolProviders {
				if wipp == nil || wipp.Name == "" {
					continue
				}
				label := wipp.DisplayName
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeIAMProvider,
					NativeID:       wipp.Name,
					Name:           &label,
					AttributesJSON: mustJSON(wipp),
					DiscoveredBy:   scanID,
				})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "iam:workloadIdentityPools.providers.list", poolNativeID, listErr)
			}
			return listErr
		}
		mu.Lock()
		defer mu.Unlock()
		t, n, uerr := upsertWithParent(st, batch, poolID)
		total += t
		inserted += n
		return uerr
	}); err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanIAMWorkloadIdentityNamespaces fans out
// WorkloadIdentityPools.Namespaces.List per already-scanned pool (a sibling
// of Providers under the pool, not nested beneath it). Returns every
// scanned namespace's native ID so the ManagedIdentity phase can fan out.
func scanIAMWorkloadIdentityNamespaces(ctx context.Context, svc *iam.Service, p *project, poolNativeIDs []string, st *store.Store, scanID string) (total, inserted int, namespaceNativeIDs []string, err error) {
	var mu sync.Mutex
	if err := forEachItem(ctx, maxConcurrentIAMFanout, poolNativeIDs, func(gctx context.Context, poolNativeID string) error {
		poolID := store.ResourceID("gcp", p.ID, poolNativeID)
		var batch []*store.Resource
		var nsNativeIDs []string
		listErr := svc.Projects.Locations.WorkloadIdentityPools.Namespaces.List(poolNativeID).Pages(gctx, func(page *iam.ListWorkloadIdentityPoolNamespacesResponse) error {
			for _, ns := range page.WorkloadIdentityPoolNamespaces {
				if ns == nil || ns.Name == "" {
					continue
				}
				label := lastSegment(ns.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeIAMNamespace,
					NativeID:       ns.Name,
					Name:           &label,
					AttributesJSON: mustJSON(ns),
					DiscoveredBy:   scanID,
				})
				nsNativeIDs = append(nsNativeIDs, ns.Name)
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "iam:workloadIdentityPools.namespaces.list", poolNativeID, listErr)
			}
			return listErr
		}
		mu.Lock()
		defer mu.Unlock()
		t, n, uerr := upsertWithParent(st, batch, poolID)
		total += t
		inserted += n
		namespaceNativeIDs = append(namespaceNativeIDs, nsNativeIDs...)
		return uerr
	}); err != nil {
		return total, inserted, nil, err
	}
	return total, inserted, namespaceNativeIDs, nil
}

// scanIAMWorkloadIdentityManagedIdentities fans out
// WorkloadIdentityPools.Namespaces.ManagedIdentities.List per already-scanned
// namespace.
func scanIAMWorkloadIdentityManagedIdentities(ctx context.Context, svc *iam.Service, p *project, namespaceNativeIDs []string, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if err := forEachItem(ctx, maxConcurrentIAMFanout, namespaceNativeIDs, func(gctx context.Context, nsNativeID string) error {
		nsID := store.ResourceID("gcp", p.ID, nsNativeID)
		var batch []*store.Resource
		listErr := svc.Projects.Locations.WorkloadIdentityPools.Namespaces.ManagedIdentities.List(nsNativeID).Pages(gctx, func(page *iam.ListWorkloadIdentityPoolManagedIdentitiesResponse) error {
			for _, mi := range page.WorkloadIdentityPoolManagedIdentities {
				if mi == nil || mi.Name == "" {
					continue
				}
				label := lastSegment(mi.Name)
				batch = append(batch, &store.Resource{
					Provider:       "gcp",
					AccountID:      p.ID,
					AccountName:    &p.Name,
					Type:           TypeIAMManagedIdentity,
					NativeID:       mi.Name,
					Name:           &label,
					AttributesJSON: mustJSON(mi),
					DiscoveredBy:   scanID,
				})
			}
			return nil
		})
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "iam:workloadIdentityPools.namespaces.managedIdentities.list", nsNativeID, listErr)
			}
			return listErr
		}
		mu.Lock()
		defer mu.Unlock()
		t, n, uerr := upsertWithParent(st, batch, nsID)
		total += t
		inserted += n
		return uerr
	}); err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanIAMOauthClients lists OauthClients for the project (location fixed at
// "locations/global"). Returns every scanned client's native ID so the
// Credential phase can fan out.
func scanIAMOauthClients(ctx context.Context, svc *iam.Service, p *project, st *store.Store, scanID string) (total, inserted int, clientNativeIDs []string, err error) {
	parent := fmt.Sprintf("projects/%s/locations/global", p.ID)
	var batch []*store.Resource
	listErr := svc.Projects.Locations.OauthClients.List(parent).Pages(ctx, func(page *iam.ListOauthClientsResponse) error {
		for _, oc := range page.OauthClients {
			if oc == nil || oc.Name == "" {
				continue
			}
			label := oc.DisplayName
			if label == "" {
				label = oc.ClientId
			}
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeIAMOauthClient,
				NativeID:       oc.Name,
				Name:           &label,
				AttributesJSON: mustJSON(oc),
				DiscoveredBy:   scanID,
			})
			clientNativeIDs = append(clientNativeIDs, oc.Name)
		}
		return nil
	})
	if listErr != nil {
		if isPermissionDenied(listErr) {
			return 0, 0, nil, skipIfDenied(st, "iam:oauthClients.list", parent, listErr)
		}
		return 0, 0, nil, listErr
	}
	if len(batch) == 0 {
		return 0, 0, clientNativeIDs, nil
	}
	t, n, uerr := upsertWithProjClosure(p, st, batch)
	if uerr != nil {
		return 0, 0, nil, fmt.Errorf("upsert IAM oauth clients: %w", uerr)
	}
	return t, n, clientNativeIDs, nil
}

// scanIAMOauthClientCredentials fans out OauthClients.Credentials.List per
// already-scanned OauthClient. Single-page `.Do()` call — no `.Pages()`
// method on this list call (same single-page shape as sqladmin's
// Databases/SslCerts/Users in Wave 8d).
func scanIAMOauthClientCredentials(ctx context.Context, svc *iam.Service, p *project, clientNativeIDs []string, st *store.Store, scanID string) (total, inserted int, err error) {
	var mu sync.Mutex
	if err := forEachItem(ctx, maxConcurrentIAMFanout, clientNativeIDs, func(gctx context.Context, clientNativeID string) error {
		clientID := store.ResourceID("gcp", p.ID, clientNativeID)
		resp, listErr := svc.Projects.Locations.OauthClients.Credentials.List(clientNativeID).Context(gctx).Do()
		if listErr != nil {
			if isPermissionDenied(listErr) {
				return skipIfDenied(st, "iam:oauthClients.credentials.list", clientNativeID, listErr)
			}
			return listErr
		}
		var batch []*store.Resource
		for _, cred := range resp.OauthClientCredentials {
			if cred == nil || cred.Name == "" {
				continue
			}
			label := cred.DisplayName
			if label == "" {
				label = lastSegment(cred.Name)
			}
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeIAMCredential,
				NativeID:       cred.Name,
				Name:           &label,
				AttributesJSON: mustJSON(cred),
				DiscoveredBy:   scanID,
			})
		}
		mu.Lock()
		defer mu.Unlock()
		t, n, uerr := upsertWithParent(st, batch, clientID)
		total += t
		inserted += n
		return uerr
	}); err != nil {
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanIAMProjectRoles lists project-scoped custom roles. Same rationale as
// scanIAMOrgRoles: ProjectsRolesService.List only ever returns custom roles.
func scanIAMProjectRoles(ctx context.Context, svc *iam.Service, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	parent := fmt.Sprintf("projects/%s", p.ID)
	var batch []*store.Resource
	listErr := svc.Projects.Roles.List(parent).Pages(ctx, func(page *iam.ListRolesResponse) error {
		for _, r := range page.Roles {
			if r == nil || r.Name == "" {
				continue
			}
			label := r.Title
			if label == "" {
				label = lastSegment(r.Name)
			}
			batch = append(batch, &store.Resource{
				Provider:       "gcp",
				AccountID:      p.ID,
				AccountName:    &p.Name,
				Type:           TypeIAMRole,
				NativeID:       r.Name,
				Name:           &label,
				AttributesJSON: mustJSON(r),
				DiscoveredBy:   scanID,
			})
		}
		return nil
	})
	if listErr != nil {
		if isPermissionDenied(listErr) {
			return 0, 0, skipIfDenied(st, "iam:projects.roles.list", parent, listErr)
		}
		return 0, 0, listErr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	t, n, uerr := upsertWithProjClosure(p, st, batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert IAM project roles: %w", uerr)
	}
	return t, n, nil
}
