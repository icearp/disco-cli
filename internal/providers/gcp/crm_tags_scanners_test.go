package gcp

import (
	"net/http/httptest"
	"testing"

	"github.com/icearp/disco-cli/store"
	"google.golang.org/api/cloudresourcemanager/v3"
	"google.golang.org/api/option"
)

// fakeCRMService builds a *cloudresourcemanager.Service pointed at the fake
// server. cloudresourcemanager's BasePath carries no version segment, so
// every route template embeds "v3/" itself — route keys below are "/v3/...".
//
// Note: scanCRMTags / scanCRMLiensAndBindings (the registerOrgService /
// registerService entry points) build their own real ADC-backed client
// internally and are therefore NOT reachable through a fake server — same
// convention as vpcsc_scanners.go / kms_scanners.go's outer scanCloudKMS.
// Tests exercise the *WithClient-equivalent inner cores directly.
func fakeCRMService(t *testing.T, srv *httptest.Server) *cloudresourcemanager.Service {
	t.Helper()
	svc, err := cloudresourcemanager.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("cloudresourcemanager.NewService: %v", err)
	}
	return svc
}

func TestScanCRMTagsUnder_OrgNestedFanout(t *testing.T) {
	st := newTestStore(t)
	orgName := "organizations/123"
	orgResourceID := store.ResourceID("gcp", orgName, orgName)
	upsertTestResource(t, st, "gcp", orgName, TypeOrganization, orgName, "", "{}")

	routes := map[string]string{
		"/v3/tagKeys": marshalAttrs(t, cloudresourcemanager.ListTagKeysResponse{
			TagKeys: []*cloudresourcemanager.TagKey{{Name: "tagKeys/1", ShortName: "env", Parent: orgName}},
		}),
		"/v3/tagValues": marshalAttrs(t, cloudresourcemanager.ListTagValuesResponse{
			TagValues: []*cloudresourcemanager.TagValue{{Name: "tagValues/1", ShortName: "prod", Parent: "tagKeys/1"}},
		}),
		"/v3/tagValues/1/tagHolds": marshalAttrs(t, cloudresourcemanager.ListTagHoldsResponse{
			TagHolds: []*cloudresourcemanager.TagHold{{Name: "tagValues/1/tagHolds/1", Holder: "//compute.googleapis.com/projects/p/zones/z/instances/i"}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeCRMService(t, srv)

	total, inserted, err := scanCRMTagsUnder(t.Context(), svc, orgName, orgName, orgResourceID, st, testScanID)
	if err != nil {
		t.Fatalf("scanCRMTagsUnder: %v", err)
	}
	if total != 3 || inserted != 3 {
		t.Fatalf("counts: got total=%d inserted=%d, want 3/3 (tagkey+tagvalue+taghold)", total, inserted)
	}

	orgID := orgResourceID
	tkID := store.ResourceID("gcp", orgName, "tagKeys/1")
	tvID := store.ResourceID("gcp", orgName, "tagValues/1")

	assertParent := func(childID, wantParentID string) {
		t.Helper()
		rels, err := st.RelationshipsFrom(wantParentID, store.RelContains)
		if err != nil {
			t.Fatalf("RelationshipsFrom(%s): %v", wantParentID, err)
		}
		for _, r := range rels {
			if r.ToID == childID {
				return
			}
		}
		t.Errorf("%s not found as child of %s; got %+v", childID, wantParentID, rels)
	}

	assertParent(tkID, orgID)
	assertParent(tvID, tkID)
	assertParent(store.ResourceID("gcp", orgName, "tagValues/1/tagHolds/1"), tvID)
}

// TestScanCRMTagsUnder_ProjectParented guards the coverage gap an adversarial
// review caught: a TagKey can be parented directly by a project (bypassing
// any organization entirely), so the project-scoped entry point must list
// TagKeys.List(parent="projects/{id}") itself rather than relying solely on
// the org-wide pass.
func TestScanCRMTagsUnder_ProjectParented(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	projParentID := store.ResourceID("gcp", p.ID, p.ID)
	upsertTestResource(t, st, "gcp", p.ID, TypeProject, p.ID, "", "{}")

	routes := map[string]string{
		"/v3/tagKeys": marshalAttrs(t, cloudresourcemanager.ListTagKeysResponse{
			TagKeys: []*cloudresourcemanager.TagKey{{Name: "tagKeys/9", ShortName: "team", Parent: "projects/my-project"}},
		}),
		"/v3/tagValues":            marshalAttrs(t, cloudresourcemanager.ListTagValuesResponse{}),
		"/v3/tagValues/1/tagHolds": marshalAttrs(t, cloudresourcemanager.ListTagHoldsResponse{}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeCRMService(t, srv)

	total, inserted, err := scanCRMTagsUnder(t.Context(), svc, p.ID, "projects/"+p.ID, projParentID, st, testScanID)
	if err != nil {
		t.Fatalf("scanCRMTagsUnder: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1 (project-parented tagkey)", total, inserted)
	}
	tkID := store.ResourceID("gcp", p.ID, "tagKeys/9")
	rels, err := st.RelationshipsFrom(projParentID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(project): %v", err)
	}
	for _, r := range rels {
		if r.ToID == tkID {
			return
		}
	}
	t.Errorf("%s not found as child of project; got %+v", tkID, rels)
}

func TestScanCRMLiensAndBindings_ProjectScoped(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("my-project")
	p.Number = "987654"
	upsertTestResource(t, st, "gcp", p.ID, TypeProject, p.ID, "", "{}")

	fullResourceName := "//cloudresourcemanager.googleapis.com/projects/" + p.Number
	routes := map[string]string{
		"/v3/liens": marshalAttrs(t, cloudresourcemanager.ListLiensResponse{
			Liens: []*cloudresourcemanager.Lien{{Name: "liens/1", Parent: "projects/my-project"}},
		}),
		"/v3/tagBindings": marshalAttrs(t, cloudresourcemanager.ListTagBindingsResponse{
			TagBindings: []*cloudresourcemanager.TagBinding{{Name: "tagBindings/x", Parent: fullResourceName, TagValue: "tagValues/1"}},
		}),
		"/v3/effectiveTags": marshalAttrs(t, cloudresourcemanager.ListEffectiveTagsResponse{
			EffectiveTags: []*cloudresourcemanager.EffectiveTag{{TagValue: "tagValues/1", NamespacedTagValue: "myorg/env/prod"}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeCRMService(t, srv)

	projParentID := store.ResourceID("gcp", p.ID, p.ID)
	t1, n1, err := scanCRMLiens(t.Context(), svc, p, projParentID, st, testScanID)
	if err != nil {
		t.Fatalf("scanCRMLiens: %v", err)
	}
	t2, n2, err := scanCRMTagBindings(t.Context(), svc, p, fullResourceName, projParentID, st, testScanID)
	if err != nil {
		t.Fatalf("scanCRMTagBindings: %v", err)
	}
	t3, n3, err := scanCRMEffectiveTags(t.Context(), svc, p, fullResourceName, projParentID, st, testScanID)
	if err != nil {
		t.Fatalf("scanCRMEffectiveTags: %v", err)
	}

	if total, inserted := t1+t2+t3, n1+n2+n3; total != 3 || inserted != 3 {
		t.Fatalf("counts: got total=%d inserted=%d, want 3/3 (lien+tagbinding+effectivetag)", total, inserted)
	}

	etID := store.ResourceID("gcp", p.ID, fullResourceName+"/effectiveTags/1")
	if _, err := st.GetResource(etID); err != nil {
		t.Errorf("GetResource(effective tag): %v", err)
	}

	rels, err := st.RelationshipsFrom(projParentID, store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom(project): %v", err)
	}
	wantChildren := []string{
		store.ResourceID("gcp", p.ID, "liens/1"),
		store.ResourceID("gcp", p.ID, "tagBindings/x"),
		etID,
	}
	for _, want := range wantChildren {
		found := false
		for _, r := range rels {
			if r.ToID == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s not found as child of project; got %+v", want, rels)
		}
	}
}

func TestCRMProjectFullResourceName(t *testing.T) {
	tests := []struct {
		number   string
		wantName string
		wantOK   bool
	}{
		{number: "123456789", wantName: "//cloudresourcemanager.googleapis.com/projects/123456789", wantOK: true},
		{number: "", wantName: "", wantOK: false},
	}
	for _, tc := range tests {
		name, ok := crmProjectFullResourceName(tc.number)
		if name != tc.wantName || ok != tc.wantOK {
			t.Errorf("crmProjectFullResourceName(%q) = (%q, %v); want (%q, %v)", tc.number, name, ok, tc.wantName, tc.wantOK)
		}
	}
}
