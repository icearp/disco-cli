package gcp

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"google.golang.org/api/accesscontextmanager/v1"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAccessPolicy, Service: "accesscontextmanager", Upstream: "accesscontextmanager.googleapis.com/AccessPolicy"})
	registerType(restype.Descriptor{Type: TypeServicePerimeter, Service: "accesscontextmanager", Upstream: "accesscontextmanager.googleapis.com/ServicePerimeter"})
	registerType(restype.Descriptor{Type: TypeAccessLevel, Service: "accesscontextmanager"})
	registerType(restype.Descriptor{Type: TypeAuthorizedOrgsDesc, Service: "accesscontextmanager"})
	registerType(restype.Descriptor{Type: TypeGcpUserAccessBinding, Service: "accesscontextmanager"})
	registerOrgService(orgServiceEntry{
		name: "gcp:vpcsc",
		fn:   scanVPCSC,
	})
}

// scanVPCSC discovers Access Context Manager access policies, the service
// perimeters / access levels / authorized-orgs-descs under each one, and the
// org's GcpUserAccessBindings (context-aware access for Workspace users —
// parented directly by the org, not by a policy). VPC-SC and Access Context
// Manager overall are exclusively org-scoped (folders carry no access
// policies or bindings), so folder scopes are skipped silently.
//
// Phase 1 of the once-per-scan org-service lane (see services.go). Sibling to
// scanIAMPoliciesOrg. Permission denial / API-not-enabled propagates via
// skipIfDenied.
func scanVPCSC(ctx context.Context, scopes []orgScope, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	svc, err := accesscontextmanager.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("accesscontextmanager client: %w", err)
	}

	for _, sc := range scopes {
		if sc.Kind != "organization" {
			continue
		}
		t, n, perr := scanVPCSCForOrg(ctx, svc, sc, st, scanID)
		total += t
		inserted += n
		if perr != nil {
			return total, inserted, perr
		}
		t, n, perr = scanGcpUserAccessBindingsForOrg(ctx, svc, sc, st, scanID)
		total += t
		inserted += n
		if perr != nil {
			return total, inserted, perr
		}
	}
	return total, inserted, nil
}

// scanVPCSCForOrg lists every access policy under the org and, for each, every
// service perimeter. Policies parent perimeters in the closure table.
func scanVPCSCForOrg(ctx context.Context, svc *accesscontextmanager.Service, sc orgScope, st *store.Store, scanID string) (total, inserted int, err error) {
	listCall := svc.AccessPolicies.List().Parent(sc.Name).Context(ctx)
	err = listCall.Pages(ctx, func(page *accesscontextmanager.ListAccessPoliciesResponse) error {
		var batch []*store.Resource
		var pairs [][2]string
		for _, ap := range page.AccessPolicies {
			if ap == nil || ap.Name == "" {
				continue
			}
			title := ap.Title
			r := &store.Resource{
				Provider:       "gcp",
				Region:         regionGlobal,
				AccountID:      sc.Name,
				Type:           TypeAccessPolicy,
				NativeID:       ap.Name, // "accessPolicies/{policyId}"
				Name:           &title,
				AttributesJSON: mustJSON(ap),
				DiscoveredBy:   scanID,
			}
			batch = append(batch, r)
			policyID := store.ResourceID("gcp", sc.Name, ap.Name)
			pairs = append(pairs, [2]string{policyID, sc.Resource})
		}
		if len(batch) == 0 {
			return nil
		}
		n, upErr := st.UpsertResources(batch)
		if upErr != nil {
			return fmt.Errorf("upsert access policies: %w", upErr)
		}
		total += len(batch)
		inserted += n
		if cErr := st.RecordHierarchyBatch(pairs); cErr != nil {
			return fmt.Errorf("closure access policies: %w", cErr)
		}
		// Per-policy service perimeters / access levels / authorized-orgs-descs
		// fan-out.
		for _, ap := range page.AccessPolicies {
			if ap == nil || ap.Name == "" {
				continue
			}
			t, n, pErr := scanServicePerimetersForPolicy(ctx, svc, sc, ap.Name, st, scanID)
			total += t
			inserted += n
			if pErr != nil {
				return pErr
			}
			t, n, pErr = scanAccessLevelsForPolicy(ctx, svc, sc, ap.Name, st, scanID)
			total += t
			inserted += n
			if pErr != nil {
				return pErr
			}
			t, n, pErr = scanAuthorizedOrgsDescsForPolicy(ctx, svc, sc, ap.Name, st, scanID)
			total += t
			inserted += n
			if pErr != nil {
				return pErr
			}
		}
		return nil
	})
	if err != nil {
		if isPermissionDenied(err) {
			return total, inserted, skipIfDenied(st, "accesscontextmanager:accessPolicies.list", sc.Name, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanServicePerimetersForPolicy lists every service perimeter under the
// access policy and persists each as its child in the closure table.
// Permission errors per policy degrade to a warning.
func scanServicePerimetersForPolicy(ctx context.Context, svc *accesscontextmanager.Service, sc orgScope, policyName string, st *store.Store, scanID string) (total, inserted int, err error) {
	policyID := store.ResourceID("gcp", sc.Name, policyName)
	err = svc.AccessPolicies.ServicePerimeters.List(policyName).Context(ctx).Pages(ctx,
		func(page *accesscontextmanager.ListServicePerimetersResponse) error {
			var batch []*store.Resource
			var pairs [][2]string
			for _, sp := range page.ServicePerimeters {
				if sp == nil || sp.Name == "" {
					continue
				}
				title := sp.Title
				r := &store.Resource{
					Provider:       "gcp",
					Region:         regionGlobal,
					AccountID:      sc.Name,
					Type:           TypeServicePerimeter,
					NativeID:       sp.Name, // "accessPolicies/{p}/servicePerimeters/{id}"
					Name:           &title,
					AttributesJSON: mustJSON(sp),
					DiscoveredBy:   scanID,
				}
				batch = append(batch, r)
				perimeterID := store.ResourceID("gcp", sc.Name, sp.Name)
				pairs = append(pairs, [2]string{perimeterID, policyID})
			}
			if len(batch) == 0 {
				return nil
			}
			n, upErr := st.UpsertResources(batch)
			if upErr != nil {
				return fmt.Errorf("upsert perimeters: %w", upErr)
			}
			total += len(batch)
			inserted += n
			if cErr := st.RecordHierarchyBatch(pairs); cErr != nil {
				return fmt.Errorf("closure perimeters: %w", cErr)
			}
			return nil
		})
	if err != nil {
		if isPermissionDenied(err) {
			return total, inserted, skipIfDenied(st, "accesscontextmanager:servicePerimeters.list", policyName, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanAccessLevelsForPolicy lists every access level under the access policy
// and persists each as its child in the closure table. Permission errors per
// policy degrade to a warning.
func scanAccessLevelsForPolicy(ctx context.Context, svc *accesscontextmanager.Service, sc orgScope, policyName string, st *store.Store, scanID string) (total, inserted int, err error) {
	policyID := store.ResourceID("gcp", sc.Name, policyName)
	err = svc.AccessPolicies.AccessLevels.List(policyName).Context(ctx).Pages(ctx,
		func(page *accesscontextmanager.ListAccessLevelsResponse) error {
			var batch []*store.Resource
			var pairs [][2]string
			for _, al := range page.AccessLevels {
				if al == nil || al.Name == "" {
					continue
				}
				title := al.Title
				r := &store.Resource{
					Provider:       "gcp",
					Region:         regionGlobal,
					AccountID:      sc.Name,
					Type:           TypeAccessLevel,
					NativeID:       al.Name, // "accessPolicies/{p}/accessLevels/{id}"
					Name:           &title,
					AttributesJSON: mustJSON(al),
					DiscoveredBy:   scanID,
				}
				batch = append(batch, r)
				levelID := store.ResourceID("gcp", sc.Name, al.Name)
				pairs = append(pairs, [2]string{levelID, policyID})
			}
			if len(batch) == 0 {
				return nil
			}
			n, upErr := st.UpsertResources(batch)
			if upErr != nil {
				return fmt.Errorf("upsert access levels: %w", upErr)
			}
			total += len(batch)
			inserted += n
			if cErr := st.RecordHierarchyBatch(pairs); cErr != nil {
				return fmt.Errorf("closure access levels: %w", cErr)
			}
			return nil
		})
	if err != nil {
		if isPermissionDenied(err) {
			return total, inserted, skipIfDenied(st, "accesscontextmanager:accessLevels.list", policyName, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanAuthorizedOrgsDescsForPolicy lists every authorized-orgs-desc under the
// access policy and persists each as its child in the closure table.
// Permission errors per policy degrade to a warning.
func scanAuthorizedOrgsDescsForPolicy(ctx context.Context, svc *accesscontextmanager.Service, sc orgScope, policyName string, st *store.Store, scanID string) (total, inserted int, err error) {
	policyID := store.ResourceID("gcp", sc.Name, policyName)
	err = svc.AccessPolicies.AuthorizedOrgsDescs.List(policyName).Context(ctx).Pages(ctx,
		func(page *accesscontextmanager.ListAuthorizedOrgsDescsResponse) error {
			var batch []*store.Resource
			var pairs [][2]string
			for _, aod := range page.AuthorizedOrgsDescs {
				if aod == nil || aod.Name == "" {
					continue
				}
				r := &store.Resource{
					Provider:       "gcp",
					Region:         regionGlobal,
					AccountID:      sc.Name,
					Type:           TypeAuthorizedOrgsDesc,
					NativeID:       aod.Name, // "accessPolicies/{p}/authorizedOrgsDescs/{id}"
					AttributesJSON: mustJSON(aod),
					DiscoveredBy:   scanID,
				}
				batch = append(batch, r)
				descID := store.ResourceID("gcp", sc.Name, aod.Name)
				pairs = append(pairs, [2]string{descID, policyID})
			}
			if len(batch) == 0 {
				return nil
			}
			n, upErr := st.UpsertResources(batch)
			if upErr != nil {
				return fmt.Errorf("upsert authorized orgs descs: %w", upErr)
			}
			total += len(batch)
			inserted += n
			if cErr := st.RecordHierarchyBatch(pairs); cErr != nil {
				return fmt.Errorf("closure authorized orgs descs: %w", cErr)
			}
			return nil
		})
	if err != nil {
		if isPermissionDenied(err) {
			return total, inserted, skipIfDenied(st, "accesscontextmanager:authorizedOrgsDescs.list", policyName, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}

// scanGcpUserAccessBindingsForOrg lists every GcpUserAccessBinding parented
// directly by the org — a Context-Aware Access control that restricts
// Workspace users/groups to specific access levels, independent of any
// AccessPolicy.
func scanGcpUserAccessBindingsForOrg(ctx context.Context, svc *accesscontextmanager.Service, sc orgScope, st *store.Store, scanID string) (total, inserted int, err error) {
	err = svc.Organizations.GcpUserAccessBindings.List(sc.Name).Context(ctx).Pages(ctx,
		func(page *accesscontextmanager.ListGcpUserAccessBindingsResponse) error {
			var batch []*store.Resource
			var pairs [][2]string
			for _, b := range page.GcpUserAccessBindings {
				if b == nil || b.Name == "" {
					continue
				}
				r := &store.Resource{
					Provider:       "gcp",
					Region:         regionGlobal,
					AccountID:      sc.Name,
					Type:           TypeGcpUserAccessBinding,
					NativeID:       b.Name, // "organizations/{id}/gcpUserAccessBindings/{id}"
					AttributesJSON: mustJSON(b),
					DiscoveredBy:   scanID,
				}
				batch = append(batch, r)
				bindingID := store.ResourceID("gcp", sc.Name, b.Name)
				pairs = append(pairs, [2]string{bindingID, sc.Resource})
			}
			if len(batch) == 0 {
				return nil
			}
			n, upErr := st.UpsertResources(batch)
			if upErr != nil {
				return fmt.Errorf("upsert gcp user access bindings: %w", upErr)
			}
			total += len(batch)
			inserted += n
			if cErr := st.RecordHierarchyBatch(pairs); cErr != nil {
				return fmt.Errorf("closure gcp user access bindings: %w", cErr)
			}
			return nil
		})
	if err != nil {
		if isPermissionDenied(err) {
			return total, inserted, skipIfDenied(st, "accesscontextmanager:gcpUserAccessBindings.list", sc.Name, err)
		}
		return total, inserted, err
	}
	return total, inserted, nil
}
