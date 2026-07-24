package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/icearp/disco-cli/store"
	"google.golang.org/api/artifactregistry/v1"
	"google.golang.org/api/option"
)

// fakeArtifactRegistryService builds a *artifactregistry.Service pointed at
// the fake server. artifactregistry's BasePath is a bare hostname (no
// embedded version segment), so route templates below carry the "v1/"
// prefix.
func fakeArtifactRegistryService(t *testing.T, srv *httptest.Server) *artifactregistry.Service {
	t.Helper()
	svc, err := artifactregistry.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("artifactregistry.NewService: %v", err)
	}
	return svc
}

func TestScanArtifactRegistry_RepoPackageTagRuleAttachmentChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	repoName := "projects/proj1/locations/us-central1/repositories/repo1"
	pkgName := repoName + "/packages/pkg1"
	tagName := pkgName + "/tags/tag1"
	ruleName := repoName + "/rules/rule1"
	attName := repoName + "/attachments/att1"

	routes := map[string]string{
		"/v1/projects/proj1/locations/-/repositories": marshalAttrs(t, artifactregistry.ListRepositoriesResponse{
			Repositories: []*artifactregistry.Repository{{Name: repoName, Format: "DOCKER"}},
		}),
		"/v1/" + repoName + "/packages": marshalAttrs(t, artifactregistry.ListPackagesResponse{
			Packages: []*artifactregistry.Package{{Name: pkgName, DisplayName: "pkg1"}},
		}),
		"/v1/" + repoName + "/rules": marshalAttrs(t, artifactregistry.ListRulesResponse{
			Rules: []*artifactregistry.GoogleDevtoolsArtifactregistryV1Rule{{Name: ruleName, Action: "ALLOW"}},
		}),
		"/v1/" + repoName + "/attachments": marshalAttrs(t, artifactregistry.ListAttachmentsResponse{
			Attachments: []*artifactregistry.Attachment{{Name: attName, AttachmentNamespace: "artifactanalysis.googleapis.com"}},
		}),
		"/v1/" + pkgName + "/tags": marshalAttrs(t, artifactregistry.ListTagsResponse{
			Tags: []*artifactregistry.Tag{{Name: tagName, Version: pkgName + "/versions/v1"}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeArtifactRegistryService(t, srv)

	total, inserted, err := scanArtifactRegistryWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanArtifactRegistryWithClient: %v", err)
	}
	// 1 repository + 1 package + 1 rule + 1 attachment + 1 tag.
	if total != 5 || inserted != 5 {
		t.Fatalf("counts: got total=%d inserted=%d, want 5/5", total, inserted)
	}

	for _, tc := range []struct {
		typ      string
		nativeID string
	}{
		{TypeArtifactRepository, repoName},
		{TypeArtifactPackage, pkgName},
		{TypeArtifactRule, ruleName},
		{TypeArtifactAttachment, attName},
		{TypeArtifactTag, tagName},
	} {
		id := store.ResourceID("gcp", p.ID, tc.nativeID)
		res, err := st.GetResource(id)
		if err != nil {
			t.Fatalf("GetResource(%s): %v", tc.typ, err)
		}
		if res == nil {
			t.Fatalf("%s %s not stored", tc.typ, tc.nativeID)
		}
		if res.Region == nil || *res.Region != "us-central1" {
			t.Errorf("%s region: got %+v, want us-central1", tc.typ, res.Region)
		}
	}

	repoID := store.ResourceID("gcp", p.ID, repoName)
	rels, err := st.RelationshipsFrom(repoID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(repo): %v", err)
	}
	if len(rels) == 0 {
		t.Errorf("expected repository to contain child rows via hierarchy closure, got none")
	}

	pkgID := store.ResourceID("gcp", p.ID, pkgName)
	pkgRels, err := st.RelationshipsFrom(pkgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom(package): %v", err)
	}
	if len(pkgRels) == 0 {
		t.Errorf("expected package to contain the tag row via hierarchy closure, got none")
	}
}

func TestScanArtifactRegistry_PackagesAPINotEnabledShapeDoesNotDisableWholeService(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	repoName := "projects/proj1/locations/us-central1/repositories/repo1"
	ruleName := repoName + "/rules/rule1"
	notEnabledBody := `{"error":{"code":403,"message":"Artifact Registry API has not been used in project proj1 before or it is disabled"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/projects/proj1/locations/-/repositories":
			_, _ = w.Write([]byte(marshalAttrs(t, artifactregistry.ListRepositoriesResponse{
				Repositories: []*artifactregistry.Repository{{Name: repoName, Format: "DOCKER"}},
			})))
		case "/v1/" + repoName + "/packages":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(notEnabledBody))
		case "/v1/" + repoName + "/rules":
			_, _ = w.Write([]byte(marshalAttrs(t, artifactregistry.ListRulesResponse{
				Rules: []*artifactregistry.GoogleDevtoolsArtifactregistryV1Rule{{Name: ruleName, Action: "DENY"}},
			})))
		case "/v1/" + repoName + "/attachments":
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeArtifactRegistryService(t, srv)

	total, inserted, err := scanArtifactRegistryWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanArtifactRegistryWithClient: %v (Packages.List's isAPINotEnabled-shaped 403 must not escalate to the whole-service disabled sentinel — Repositories.List already proved the API enabled)", err)
	}
	// 1 repository + 1 rule; Packages 403 must warn-and-continue, not abort
	// the repository's remaining sub-phases or the scan as a whole.
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2", total, inserted)
	}
}

func TestScanArtifactRegistry_RulesPermissionDeniedContinuesToAttachments(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	repoName := "projects/proj1/locations/us-central1/repositories/repo1"
	attName := repoName + "/attachments/att1"
	deniedBody := `{"error":{"code":403,"message":"caller does not have artifactregistry.rules.list access"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/projects/proj1/locations/-/repositories":
			_, _ = w.Write([]byte(marshalAttrs(t, artifactregistry.ListRepositoriesResponse{
				Repositories: []*artifactregistry.Repository{{Name: repoName, Format: "DOCKER"}},
			})))
		case "/v1/" + repoName + "/packages":
			_, _ = w.Write([]byte(`{}`))
		case "/v1/" + repoName + "/rules":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(deniedBody))
		case "/v1/" + repoName + "/attachments":
			_, _ = w.Write([]byte(marshalAttrs(t, artifactregistry.ListAttachmentsResponse{
				Attachments: []*artifactregistry.Attachment{{Name: attName}},
			})))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeArtifactRegistryService(t, srv)

	total, inserted, err := scanArtifactRegistryWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanArtifactRegistryWithClient: %v", err)
	}
	// 1 repository + 1 attachment; Rules 403 must warn, not abort the
	// remaining Attachments sub-phase for the same repository.
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2 (rules 403 must not block attachments)", total, inserted)
	}
}

func TestScanArtifactRegistry_EmptyProjectNoRepositories(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	routes := map[string]string{
		"/v1/projects/proj1/locations/-/repositories": marshalAttrs(t, artifactregistry.ListRepositoriesResponse{}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeArtifactRegistryService(t, srv)

	total, inserted, err := scanArtifactRegistryWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanArtifactRegistryWithClient: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}
