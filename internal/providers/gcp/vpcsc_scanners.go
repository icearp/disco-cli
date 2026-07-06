package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/accesscontextmanager/v1"
)

func init() {
	registerOrgService(orgServiceEntry{
		name: "gcp:vpcsc",
		fn:   scanVPCSC,
		emits: []coverage.TypeDecl{
			{Service: "accesscontextmanager", DiscoType: TypeAccessPolicy},
			{Service: "accesscontextmanager", DiscoType: TypeServicePerimeter},
		},
	})
}

// scanVPCSC discovers Access Context Manager access policies and the service
// perimeters under each one. VPC-SC is exclusively org-scoped (folders carry
// no access policies), so folder scopes are skipped silently.
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
			policyID := store.ResourceID("gcp", sc.Name, TypeAccessPolicy, ap.Name)
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
		// Per-policy service perimeters fan-out.
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
	policyID := store.ResourceID("gcp", sc.Name, TypeAccessPolicy, policyName)
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
				perimeterID := store.ResourceID("gcp", sc.Name, TypeServicePerimeter, sp.Name)
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
