package gcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/icearp/disco-cli/store"
	"google.golang.org/api/option"
	"google.golang.org/api/storage/v1"
)

// fakeStorageService builds a *storage.Service pointed at the fake server.
// storage's BasePath already embeds "storage/v1/" — option.WithEndpoint
// replaces the entire BasePath (same gotcha as Compute/BigQuery), so route
// templates below have NO version prefix.
func fakeStorageService(t *testing.T, srv *httptest.Server) *storage.Service {
	t.Helper()
	svc, err := storage.NewService(
		t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithHTTPClient(srv.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("storage.NewService: %v", err)
	}
	return svc
}

func TestScanStorage_BucketsHmacKeysAndPerBucketSecondaryChain(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	bucketSelfLink := "https://storage.googleapis.com/storage/v1/b/bucket1"
	routes := map[string]string{
		"/b": marshalAttrs(t, storage.Buckets{
			Items: []*storage.Bucket{{Name: "bucket1", Location: "US", SelfLink: bucketSelfLink}},
		}),
		"/projects/proj1/hmacKeys": marshalAttrs(t, storage.HmacKeysMetadata{
			Items: []*storage.HmacKeyMetadata{{Id: "proj1/access1", AccessId: "access1", State: "ACTIVE"}},
		}),
		"/b/bucket1/notificationConfigs": marshalAttrs(t, storage.Notifications{
			Items: []*storage.Notification{{Id: "notif1", Topic: "//pubsub.googleapis.com/projects/proj1/topics/t1"}},
		}),
		"/b/bucket1/managedFolders": marshalAttrs(t, storage.ManagedFolders{
			Items: []*storage.ManagedFolder{{Id: "bucket1/mf1/", Name: "mf1/", Bucket: "bucket1"}},
		}),
		"/b/bucket1/anywhereCaches": marshalAttrs(t, storage.AnywhereCaches{
			Items: []*storage.AnywhereCache{{Id: "bucket1/ac1", AnywhereCacheId: "ac1", State: "RUNNING"}},
		}),
		"/b/bucket1/folders": marshalAttrs(t, storage.Folders{
			Items: []*storage.Folder{{Id: "bucket1/f1/", Name: "f1/", Bucket: "bucket1"}},
		}),
		"/b/bucket1/acl": marshalAttrs(t, storage.BucketAccessControls{
			Items: []*storage.BucketAccessControl{{Id: "bucket1/allUsers", Entity: "allUsers", Bucket: "bucket1"}},
		}),
		"/b/bucket1/defaultObjectAcl": marshalAttrs(t, storage.ObjectAccessControls{
			Items: []*storage.ObjectAccessControl{{Id: "bucket1/allUsers", Entity: "allUsers", Bucket: "bucket1"}},
		}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeStorageService(t, srv)

	total, inserted, err := scanStorageWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanStorageWithClient: %v", err)
	}
	// 1 bucket + 1 hmac key + 1 notification + 1 managed folder + 1 anywhere
	// cache + 1 folder + 1 bucket ACL + 1 default object ACL.
	if total != 8 || inserted != 8 {
		t.Fatalf("counts: got total=%d inserted=%d, want 8/8", total, inserted)
	}

	bucketID := store.ResourceID("gcp", p.ID, bucketSelfLink)
	for _, tc := range []struct {
		typ      string
		nativeID string
	}{
		{TypeStorageNotification, "bucket1/notificationConfigs/notif1"},
		{TypeStorageManagedFolder, "bucket1/managedFolders/mf1/"},
		{TypeStorageAnywhereCache, "bucket1/ac1"},
		{TypeStorageFolder, "bucket1/folders/f1/"},
		{TypeStorageBucketAccessControl, "bucket1/acl/allUsers"},
		{TypeStorageDefaultObjectAccessControl, "bucket1/defaultObjectAcl/allUsers"},
	} {
		id := store.ResourceID("gcp", p.ID, tc.nativeID)
		res, err := st.GetResource(id)
		if err != nil {
			t.Fatalf("GetResource(%s): %v", tc.typ, err)
		}
		if res == nil {
			t.Fatalf("%s %s not stored", tc.typ, tc.nativeID)
		}
	}

	hmacID := store.ResourceID("gcp", p.ID, "proj1/access1")
	hmacRes, err := st.GetResource(hmacID)
	if err != nil {
		t.Fatalf("GetResource(hmac key): %v", err)
	}
	if hmacRes == nil || hmacRes.Status == nil || *hmacRes.Status != "ACTIVE" {
		t.Errorf("hmac key status: got %+v, want ACTIVE", hmacRes)
	}

	rels, err := st.RelationshipsFrom(bucketID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) == 0 {
		t.Errorf("expected bucket to contain child rows via hierarchy closure, got none")
	}
}

func TestScanStorage_ManagedFolderNotApplicableContinues(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	notEnabledBody := `{"error":{"code":400,"message":"Bucket does not have hierarchical namespace enabled."}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/b":
			_, _ = w.Write([]byte(marshalAttrs(t, storage.Buckets{
				Items: []*storage.Bucket{{Name: "bucket1", Location: "US", SelfLink: "https://storage.googleapis.com/storage/v1/b/bucket1"}},
			})))
		case "/projects/proj1/hmacKeys":
			_, _ = w.Write([]byte(`{}`))
		case "/b/bucket1/managedFolders", "/b/bucket1/folders":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(notEnabledBody))
		case "/b/bucket1/notificationConfigs":
			_, _ = w.Write([]byte(`{}`))
		case "/b/bucket1/anywhereCaches":
			_, _ = w.Write([]byte(`{}`))
		case "/b/bucket1/acl":
			_, _ = w.Write([]byte(marshalAttrs(t, storage.BucketAccessControls{
				Items: []*storage.BucketAccessControl{{Id: "bucket1/allUsers", Entity: "allUsers", Bucket: "bucket1"}},
			})))
		case "/b/bucket1/defaultObjectAcl":
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeStorageService(t, srv)

	total, inserted, err := scanStorageWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanStorageWithClient: %v", err)
	}
	// 1 bucket + 1 bucket ACL — ManagedFolders/Folders 400s must not abort
	// the scan or the bucket's remaining sub-phases (ACL still lands).
	if total != 2 || inserted != 2 {
		t.Fatalf("counts: got total=%d inserted=%d, want 2/2 (feature-not-enabled 400s must not abort scan)", total, inserted)
	}
}

func TestScanStorage_BucketAccessControlsPermissionDenied(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	deniedBody := `{"error":{"code":403,"message":"caller does not have storage.buckets.getIamPolicy access"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/b":
			_, _ = w.Write([]byte(marshalAttrs(t, storage.Buckets{
				Items: []*storage.Bucket{{Name: "bucket1", Location: "US", SelfLink: "https://storage.googleapis.com/storage/v1/b/bucket1"}},
			})))
		case "/projects/proj1/hmacKeys":
			_, _ = w.Write([]byte(`{}`))
		case "/b/bucket1/acl":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(deniedBody))
		case "/b/bucket1/managedFolders", "/b/bucket1/folders", "/b/bucket1/notificationConfigs",
			"/b/bucket1/anywhereCaches", "/b/bucket1/defaultObjectAcl":
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unrouted GCP fake hit: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"no fake route"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	svc := fakeStorageService(t, srv)

	total, inserted, err := scanStorageWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanStorageWithClient: %v", err)
	}
	// Only the bucket itself lands; the ACL 403 must warn, not abort.
	if total != 1 || inserted != 1 {
		t.Fatalf("counts: got total=%d inserted=%d, want 1/1 (ACL 403 must warn, not abort)", total, inserted)
	}
}

func TestScanStorage_EmptyProjectNoBuckets(t *testing.T) {
	st := newTestStore(t)
	p := newTestProject("proj1")

	routes := map[string]string{
		"/b":                       marshalAttrs(t, storage.Buckets{}),
		"/projects/proj1/hmacKeys": marshalAttrs(t, storage.HmacKeysMetadata{}),
	}
	srv := fakeGCPServer(t, routes)
	svc := fakeStorageService(t, srv)

	total, inserted, err := scanStorageWithClient(t.Context(), svc, p, st, testScanID)
	if err != nil {
		t.Fatalf("scanStorageWithClient: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("counts: got total=%d inserted=%d, want 0/0", total, inserted)
	}
}
