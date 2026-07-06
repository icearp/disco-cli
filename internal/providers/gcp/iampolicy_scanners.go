package gcp

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"google.golang.org/api/cloudresourcemanager/v3"
)

func init() {
	registerService(serviceEntry{
		name: "gcp:iam-policy",
		fn:   scanIAMPolicies,
		emits: []coverage.TypeDecl{
			// Discovery lists iam.googleapis.com/Policy, which the algorithmic
			// key matches — covered, no flag needed.
			{Service: "iam", DiscoType: TypeIAMPolicy},
		},
	})
}

// scanIAMPolicies fetches the IAM policy attached to the project scope. One
// synthesized gcp:iam:policy resource per project carries every binding
// (role, members, condition) in the resource's attributes; phase-2 resolvers
// pivot on those bindings to emit policy → service-account edges.
//
// Folder/organization scope policies are deferred — they require running once
// per scan rather than per project; tracked under R4.1 follow-up.
func scanIAMPolicies(ctx context.Context, p *project, st *store.Store, scanID string) (total, inserted int, err error) {
	opts := clientOptions(ctx, providerCfg{})
	crmSvc, err := cloudresourcemanager.NewService(ctx, opts...)
	if err != nil {
		return 0, 0, fmt.Errorf("cloudresourcemanager client: %w", err)
	}

	resource := fmt.Sprintf("projects/%s", p.ID)
	policy, err := crmSvc.Projects.GetIamPolicy(resource, &cloudresourcemanager.GetIamPolicyRequest{
		Options: &cloudresourcemanager.GetPolicyOptions{RequestedPolicyVersion: 3},
	}).Context(ctx).Do()
	if err != nil {
		if isPermissionDenied(err) {
			return 0, 0, skipIfDenied(st, "cloudresourcemanager:projects.getIamPolicy", p.ID, err)
		}
		return 0, 0, err
	}

	nativeID := resource + "/policy"
	name := nativeID
	r := &store.Resource{
		Provider:       "gcp",
		AccountID:      p.ID,
		AccountName:    &p.Name,
		Region:         regionGlobal,
		Type:           TypeIAMPolicy,
		NativeID:       nativeID,
		Name:           &name,
		AttributesJSON: mustJSON(policy),
		DiscoveredBy:   scanID,
	}
	n, e := st.UpsertResources([]*store.Resource{r})
	if e != nil {
		return 0, 0, fmt.Errorf("upsert IAM policy: %w", e)
	}
	total = 1
	inserted = n

	// Closure: policy → project parent.
	policyID := store.ResourceID("gcp", p.ID, TypeIAMPolicy, nativeID)
	projParentID := store.ResourceID("gcp", p.ID, TypeProject, p.ID)
	if err := st.RecordHierarchy(policyID, projParentID); err != nil {
		return total, inserted, fmt.Errorf("closure IAM policy: %w", err)
	}
	return total, inserted, nil
}
