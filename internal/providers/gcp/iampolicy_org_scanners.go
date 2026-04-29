package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"google.golang.org/api/cloudresourcemanager/v3"
)

func init() {
	registerOrgService(orgServiceEntry{
		name: "gcp:iam-policy-org",
		fn:   scanIAMPoliciesOrg,
		emits: []coverage.TypeDecl{
			// IAM policy is a per-resource synthetic — no upstream Discovery
			// resource collection (GetIamPolicy is a method on every parent
			// scope, not its own resource).
			{Service: "iam", DiscoType: TypeIAMPolicy, Synthetic: true},
		},
	})
}

// scanIAMPoliciesOrg fetches the IAM policy attached to each org+folder scope
// discovered by scanHierarchy. Sibling to scanIAMPolicies (per-project) — runs
// ONCE per scan because folder/org GetIamPolicy is invoked at the parent scope,
// not duplicated under each child project.
//
// One synthesized gcp:iam:policy resource per scope carries every binding
// (role, members, condition) under attributes; phase-2 resolvers pivot on those
// bindings to emit policy → service-account edges (same shape as the per-project
// resolver, which is scope-agnostic).
//
// AccountID for the synthesized resource is the GCP-canonical scope name
// ("organizations/123" / "folders/456"), matching the per-project AccountID
// convention (project ID for project-scope policies). Closure links the policy
// to its scope's hierarchy resource so the org/folder appears as the parent.
func scanIAMPoliciesOrg(ctx context.Context, scopes []orgScope, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	crmSvc, err := cloudresourcemanager.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("cloudresourcemanager client: %w", err)
	}

	for _, sc := range scopes {
		policy, getErr := getOrgScopePolicy(ctx, crmSvc, sc)
		if getErr != nil {
			if isPermissionDenied(getErr) {
				_ = skipIfDenied(st, "cloudresourcemanager:"+sc.Kind+".getIamPolicy", sc.Name, getErr)
				continue
			}
			return total, inserted, getErr
		}
		nativeID := sc.Name + "/policy"
		name := nativeID
		var typ string
		switch sc.Kind {
		case "organization":
			typ = TypeOrganization
		case "folder":
			typ = TypeFolder
		default:
			continue
		}
		// Use a stable AccountName derived from the scope name; we lack a
		// display name here without an extra Get call.
		acctName := sc.Name
		_ = typ // kept for future use; resource's Type is still TypeIAMPolicy.

		r := &store.Resource{
			Provider:       "gcp",
			AccountID:      sc.Name,
			AccountName:    &acctName,
			Type:           TypeIAMPolicy,
			NativeID:       nativeID,
			Name:           &name,
			AttributesJSON: mustJSON(policy),
			DiscoveredBy:   scanID,
		}
		n, upErr := st.UpsertResources([]*store.Resource{r})
		if upErr != nil {
			return total, inserted, fmt.Errorf("upsert org IAM policy %s: %w", sc.Name, upErr)
		}
		total++
		inserted += n

		policyID := store.ResourceID("gcp", sc.Name, TypeIAMPolicy, nativeID)
		if cErr := st.RecordHierarchy(policyID, sc.Resource); cErr != nil {
			return total, inserted, fmt.Errorf("closure org IAM policy %s: %w", sc.Name, cErr)
		}
	}
	return total, inserted, nil
}

// getOrgScopePolicy dispatches to the right CRM client method per scope kind.
// Wraps the two SDK call shapes (Organizations.GetIamPolicy vs Folders.GetIamPolicy)
// with identical PolicyVersion=3 options.
func getOrgScopePolicy(ctx context.Context, crm *cloudresourcemanager.Service, sc orgScope) (*cloudresourcemanager.Policy, error) {
	req := &cloudresourcemanager.GetIamPolicyRequest{
		Options: &cloudresourcemanager.GetPolicyOptions{RequestedPolicyVersion: 3},
	}
	switch sc.Kind {
	case "organization":
		return crm.Organizations.GetIamPolicy(sc.Name, req).Context(ctx).Do()
	case "folder":
		return crm.Folders.GetIamPolicy(sc.Name, req).Context(ctx).Do()
	default:
		return nil, fmt.Errorf("unknown scope kind %q", sc.Kind)
	}
}
