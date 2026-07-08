package gcp

import (
	"strings"
	"testing"

	"codeberg.org/icearp/disco/store"
)

// TestResolveIAMPolicyRelationships verifies that a project IAM policy emits:
//   - a `uses` edge to every in-project serviceAccount: member whose SA row exists
//   - a `cross-project-iam` edge to the referenced project's self-node
//     placeholder for SA members from a project not in scan scope (R5).
func TestResolveIAMPolicyRelationships(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	saEmail := "sa1@my-project.iam.gserviceaccount.com"
	saNative := "projects/my-project/serviceAccounts/" + saEmail
	saID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMServiceAccount, saNative, "", "{}")

	policyAttrs := `{
		"bindings": [
			{"role": "roles/storage.admin", "members": ["serviceAccount:` + saEmail + `", "user:alice@example.com"]},
			{"role": "roles/viewer", "members": ["serviceAccount:cross-proj@other.iam.gserviceaccount.com"]}
		]
	}`
	policyNative := "projects/my-project/policy"
	policyID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMPolicy, policyNative, "", policyAttrs)

	if err := resolveIAMPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveIAMPolicyRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 edges (in-project uses + cross-project-iam placeholder), got %d: %+v", len(rels), rels)
	}
	byKind := map[string]store.Relationship{}
	for _, r := range rels {
		byKind[r.Kind] = r
	}
	usesEdge, ok := byKind[store.RelUses]
	if !ok || usesEdge.ToID != saID {
		t.Errorf("expected uses→in-project SA, got %+v", usesEdge)
	}
	if usesEdge.Attributes == nil || !strings.Contains(*usesEdge.Attributes, "roles/storage.admin") {
		t.Errorf("expected role attr roles/storage.admin, got %v", usesEdge.Attributes)
	}
	crossEdge, ok := byKind[store.RelCrossProjectIAM]
	if !ok {
		t.Fatalf("missing cross-project-iam edge, got kinds: %v", byKind)
	}
	wantID := store.ResourceID("gcp", "other", TypeProject, "other")
	if crossEdge.ToID != wantID {
		t.Errorf("cross-project-iam target: got %q want %q", crossEdge.ToID, wantID)
	}
	if crossEdge.Attributes == nil || !strings.Contains(*crossEdge.Attributes, "other") {
		t.Errorf("expected member-project attr 'other', got %v", crossEdge.Attributes)
	}
	// The referenced project exists as an empty-attribute self-node placeholder.
	r, err := st.GetResource(wantID)
	if err != nil {
		t.Fatalf("GetResource project placeholder: %v", err)
	}
	if r.AttributesJSON != "{}" {
		t.Errorf("placeholder attributes = %q, want empty {}", r.AttributesJSON)
	}
}

// TestResolveIAMPolicyRelationships_DoesNotClobberScannedProject verifies the
// cross-project placeholder is FK-safe but non-destructive: when the referenced
// project's self-node already exists populated (it was scanned), the resolver
// leaves its attributes intact and still points the edge at it.
func TestResolveIAMPolicyRelationships_DoesNotClobberScannedProject(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	populated := `{"projectId":"other","name":"projects/123"}`
	scannedID := upsertTestResource(t, st, "gcp", "other", TypeProject, "other", "", populated)

	policyAttrs := `{"bindings":[{"role":"roles/viewer","members":["serviceAccount:cross-proj@other.iam.gserviceaccount.com"]}]}`
	policyID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMPolicy, "projects/my-project/policy", "", policyAttrs)

	if err := resolveIAMPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveIAMPolicyRelationships: %v", err)
	}

	r, err := st.GetResource(scannedID)
	if err != nil {
		t.Fatalf("GetResource scanned project: %v", err)
	}
	if r.AttributesJSON != populated {
		t.Errorf("scanned project clobbered: attributes = %q, want %q", r.AttributesJSON, populated)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	var found bool
	for _, e := range rels {
		if e.Kind == store.RelCrossProjectIAM && e.ToID == scannedID {
			found = true
		}
	}
	if !found {
		t.Errorf("missing cross-project-iam edge to scanned project, got: %+v", rels)
	}
}

// TestResolveIAMPolicyRelationships_NonSAMembers verifies that user: and
// group: members emit `uses` edges to in-store Workspace user / Cloud
// Identity group rows when the emails match (case-insensitive). domain: and
// allUsers still skip with no resource rows.
func TestResolveIAMPolicyRelationships_NonSAMembers(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	const customerID = "C03az79cb"

	// Workspace user under the customer AccountID (tenant-scope) with primaryEmail.
	userID := upsertTestResource(t, st, "gcp", customerID, TypeWorkspaceUser, "users/12345",
		"", `{"id":"12345","primaryEmail":"alice@example.com"}`)
	// Cloud Identity group keyed on email.
	groupID := upsertTestResource(t, st, "gcp", customerID, TypeCloudIdentityGroup, "groups/g1",
		"", `{"name":"groups/g1","groupKey":{"id":"eng@example.com"}}`)

	// Mixed binding: same user + group + an unknown domain (skipped) + allUsers (skipped).
	policyAttrs := `{
		"bindings": [
			{"role": "roles/viewer", "members": [
				"user:Alice@Example.com",
				"group:eng@example.com",
				"domain:example.com",
				"allUsers"
			]}
		]
	}`
	policyID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMPolicy,
		"projects/my-project/policy", "", policyAttrs)

	if err := resolveIAMPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveIAMPolicyRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 edges (user + group), got %d: %+v", len(rels), rels)
	}
	hits := map[string]bool{}
	for _, r := range rels {
		hits[r.ToID] = true
	}
	if !hits[userID] {
		t.Errorf("missing edge to workspace user %q; got %+v", userID, rels)
	}
	if !hits[groupID] {
		t.Errorf("missing edge to cloud-identity group %q; got %+v", groupID, rels)
	}
}

// TestResolveIAMPolicyRelationships_FederationMembers covers Workforce and
// Workload Identity Pool principal/principalSet bindings (subject, group,
// attribute, and bare wildcard variants), plus an unscanned pool reference
// that must be silently skipped.
func TestResolveIAMPolicyRelationships_FederationMembers(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	wfPoolID := upsertTestResource(t, st, "gcp", "organizations/456", TypeIAMWorkforcePool,
		"locations/global/workforcePools/my-wf-pool", "", `{"name":"locations/global/workforcePools/my-wf-pool"}`)
	wlPoolID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMWorkloadIdentityPool,
		"projects/123456789012/locations/global/workloadIdentityPools/my-wl-pool", "",
		`{"name":"projects/123456789012/locations/global/workloadIdentityPools/my-wl-pool"}`)

	policyAttrs := `{
		"bindings": [
			{"role": "roles/viewer", "members": [
				"principal://iam.googleapis.com/locations/global/workforcePools/my-wf-pool/subject/alice",
				"principalSet://iam.googleapis.com/projects/123456789012/locations/global/workloadIdentityPools/my-wl-pool/group/eng",
				"principalSet://iam.googleapis.com/locations/global/workforcePools/unscanned-pool/*"
			]}
		]
	}`
	policyID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMPolicy,
		"projects/my-project/policy", "", policyAttrs)

	if err := resolveIAMPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveIAMPolicyRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 edges (workforce pool + workload pool), got %d: %+v", len(rels), rels)
	}
	hits := map[string]bool{}
	for _, r := range rels {
		if r.Kind != store.RelUses {
			t.Errorf("expected RelUses, got %q for %+v", r.Kind, r)
		}
		hits[r.ToID] = true
	}
	if !hits[wfPoolID] {
		t.Errorf("missing edge to workforce pool %q; got %+v", wfPoolID, rels)
	}
	if !hits[wlPoolID] {
		t.Errorf("missing edge to workload identity pool %q; got %+v", wlPoolID, rels)
	}
}

// TestFederationPoolFromPrincipal covers the principal-string parsing
// directly: subject/group/attribute/wildcard variants and non-matching
// members (SA/user/group/domain) that must return ok=false.
func TestFederationPoolFromPrincipal(t *testing.T) {
	cases := []struct {
		member   string
		wantPool string
		wantOK   bool
	}{
		{"principal://iam.googleapis.com/locations/global/workforcePools/p1/subject/alice", "locations/global/workforcePools/p1", true},
		{"principalSet://iam.googleapis.com/locations/global/workforcePools/p1/group/g1", "locations/global/workforcePools/p1", true},
		{"principalSet://iam.googleapis.com/locations/global/workforcePools/p1/attribute.department/eng", "locations/global/workforcePools/p1", true},
		{"principalSet://iam.googleapis.com/locations/global/workforcePools/p1/*", "locations/global/workforcePools/p1", true},
		{"principal://iam.googleapis.com/projects/123/locations/global/workloadIdentityPools/p2/subject/s", "projects/123/locations/global/workloadIdentityPools/p2", true},
		// A free-form IdP-asserted group ID containing "/subject/" as a
		// substring must not be mistaken for a later, unrelated separator —
		// the leftmost real separator ("/group/") must win.
		{"principalSet://iam.googleapis.com/locations/global/workforcePools/p1/group/team/subject/oddball", "locations/global/workforcePools/p1", true},
		{"serviceAccount:foo@my-project.iam.gserviceaccount.com", "", false},
		{"user:alice@example.com", "", false},
		{"domain:example.com", "", false},
		{"allUsers", "", false},
	}
	for _, c := range cases {
		gotPool, gotOK := federationPoolFromPrincipal(c.member)
		if gotOK != c.wantOK || gotPool != c.wantPool {
			t.Errorf("federationPoolFromPrincipal(%q) = (%q, %v); want (%q, %v)",
				c.member, gotPool, gotOK, c.wantPool, c.wantOK)
		}
	}
}

// TestResolveIAMPolicyRelationships_NoBindings verifies that a policy with no
// bindings produces no edges and no errors.
func TestResolveIAMPolicyRelationships_NoBindings(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")

	policyID := upsertTestResource(t, st, "gcp", p.ID, TypeIAMPolicy,
		"projects/my-project/policy", "", `{}`)

	if err := resolveIAMPolicyRelationships(p, st); err != nil {
		t.Fatalf("resolveIAMPolicyRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 edges, got %d", len(rels))
	}
}

// TestResolveIAMPolicyOrgRelationships verifies an organization-scoped policy
// (AccountID = "organizations/123", mirroring iampolicy_org_scanners.go's
// exact shape) resolves its serviceAccount: member as RelOrgIAM (not
// RelUses, not RelCrossProjectIAM) when the SA is found in some scanned
// project, plus user:/group: members via the same scope-agnostic indexes
// the per-project resolver uses.
func TestResolveIAMPolicyOrgRelationships(t *testing.T) {
	st := newTestStore(t)

	saEmail := "sa1@some-project.iam.gserviceaccount.com"
	saNative := "projects/some-project/serviceAccounts/" + saEmail
	saID := upsertTestResource(t, st, "gcp", "some-project", TypeIAMServiceAccount, saNative, "", "{}")

	userID := upsertTestResource(t, st, "gcp", "customer123", TypeWorkspaceUser, "users/1", "",
		`{"primaryEmail": "alice@example.com"}`)

	policyAttrs := `{
		"bindings": [
			{"role": "roles/owner", "members": ["serviceAccount:` + saEmail + `", "user:alice@example.com"]}
		]
	}`
	policyID := upsertTestResource(t, st, "gcp", "organizations/123", TypeIAMPolicy,
		"organizations/123/policy", "", policyAttrs)

	if err := resolveIAMPolicyOrgRelationships(st); err != nil {
		t.Fatalf("resolveIAMPolicyOrgRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 edges (org-iam SA + uses workspace user), got %d: %+v", len(rels), rels)
	}
	byKind := map[string]store.Relationship{}
	for _, r := range rels {
		byKind[r.Kind] = r
	}
	if _, ok := byKind[store.RelCrossProjectIAM]; ok {
		t.Errorf("org-scope SA member must not emit cross-project-iam, got %+v", byKind[store.RelCrossProjectIAM])
	}
	orgEdge, ok := byKind[store.RelOrgIAM]
	if !ok || orgEdge.ToID != saID {
		t.Errorf("expected org-iam edge to SA, got %+v", orgEdge)
	}
	userEdge, ok := byKind[store.RelUses]
	if !ok || userEdge.ToID != userID {
		t.Errorf("expected uses edge to workspace user, got %+v", userEdge)
	}
}

// TestResolveIAMPolicyOrgRelationships_UnscannedSAPlaceholder verifies that
// an org-scope SA member not found in any scanned project still gets a
// RelOrgIAM edge, to a project self-node placeholder (mirrors the
// per-project resolver's cross-project-iam placeholder mechanism, R5).
func TestResolveIAMPolicyOrgRelationships_UnscannedSAPlaceholder(t *testing.T) {
	st := newTestStore(t)

	policyAttrs := `{
		"bindings": [
			{"role": "roles/owner", "members": ["serviceAccount:ghost@unscanned-project.iam.gserviceaccount.com"]}
		]
	}`
	policyID := upsertTestResource(t, st, "gcp", "organizations/123", TypeIAMPolicy,
		"organizations/123/policy", "", policyAttrs)

	if err := resolveIAMPolicyOrgRelationships(st); err != nil {
		t.Fatalf("resolveIAMPolicyOrgRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].Kind != store.RelOrgIAM {
		t.Fatalf("expected 1 org-iam placeholder edge, got %+v", rels)
	}
	wantID := store.ResourceID("gcp", "unscanned-project", TypeProject, "unscanned-project")
	if rels[0].ToID != wantID {
		t.Errorf("placeholder target: got %q want %q", rels[0].ToID, wantID)
	}
}

// TestResolveIAMPolicyOrgRelationships_FolderScope proves the AccountID
// prefix filter also catches folder-scoped policies, not just
// organization-scoped ones.
func TestResolveIAMPolicyOrgRelationships_FolderScope(t *testing.T) {
	st := newTestStore(t)

	saEmail := "sa1@some-project.iam.gserviceaccount.com"
	saNative := "projects/some-project/serviceAccounts/" + saEmail
	saID := upsertTestResource(t, st, "gcp", "some-project", TypeIAMServiceAccount, saNative, "", "{}")

	policyAttrs := `{"bindings": [{"role": "roles/owner", "members": ["serviceAccount:` + saEmail + `"]}]}`
	policyID := upsertTestResource(t, st, "gcp", "folders/456", TypeIAMPolicy,
		"folders/456/policy", "", policyAttrs)

	if err := resolveIAMPolicyOrgRelationships(st); err != nil {
		t.Fatalf("resolveIAMPolicyOrgRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 || rels[0].Kind != store.RelOrgIAM || rels[0].ToID != saID {
		t.Fatalf("expected 1 org-iam edge to SA, got %+v", rels)
	}
}

// TestResolveIAMPolicyOrgRelationships_DoesNotReprocessProjectScopedPolicies
// seeds a project-scoped policy alongside an org-scoped one and confirms the
// org resolver only touches the org-scoped row — proving the AccountID
// prefix filter genuinely excludes project-scoped rows rather than
// double-processing them (which would misclassify an in-project uses edge
// as org-iam, since the org lane never builds a same-project SA index).
func TestResolveIAMPolicyOrgRelationships_DoesNotReprocessProjectScopedPolicies(t *testing.T) {
	st := newTestStore(t)

	saEmail := "sa1@my-project.iam.gserviceaccount.com"
	saNative := "projects/my-project/serviceAccounts/" + saEmail
	upsertTestResource(t, st, "gcp", "my-project", TypeIAMServiceAccount, saNative, "", "{}")

	projectPolicyAttrs := `{"bindings": [{"role": "roles/owner", "members": ["serviceAccount:` + saEmail + `"]}]}`
	projectPolicyID := upsertTestResource(t, st, "gcp", "my-project", TypeIAMPolicy,
		"projects/my-project/policy", "", projectPolicyAttrs)

	orgPolicyAttrs := `{"bindings": [{"role": "roles/owner", "members": ["serviceAccount:` + saEmail + `"]}]}`
	orgPolicyID := upsertTestResource(t, st, "gcp", "organizations/123", TypeIAMPolicy,
		"organizations/123/policy", "", orgPolicyAttrs)

	if err := resolveIAMPolicyOrgRelationships(st); err != nil {
		t.Fatalf("resolveIAMPolicyOrgRelationships: %v", err)
	}

	projectRels, err := st.RelationshipsFrom(projectPolicyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(project): %v", err)
	}
	if len(projectRels) != 0 {
		t.Errorf("org resolver must not touch project-scoped policy, got %+v", projectRels)
	}

	orgRels, err := st.RelationshipsFrom(orgPolicyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(org): %v", err)
	}
	if len(orgRels) != 1 || orgRels[0].Kind != store.RelOrgIAM {
		t.Fatalf("expected 1 org-iam edge for the org-scoped policy, got %+v", orgRels)
	}
}

// TestResolveIAMPolicyOrgRelationships_EmptyStoreNoResources verifies the
// empty-store case returns nil with no panic.
func TestResolveIAMPolicyOrgRelationships_EmptyStoreNoResources(t *testing.T) {
	st := newTestStore(t)
	if err := resolveIAMPolicyOrgRelationships(st); err != nil {
		t.Fatalf("resolveIAMPolicyOrgRelationships on empty store: %v", err)
	}
}
