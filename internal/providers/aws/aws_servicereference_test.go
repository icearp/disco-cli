package aws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// srTestServer serves a fake Service Reference index + per-service docs. The
// index URLs point back at the same server so fetchServiceReferenceFrom walks
// the real two-hop shape (index → per-service GET).
func srTestServer(t *testing.T, docs map[string][]string, failService string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/index", func(w http.ResponseWriter, _ *http.Request) {
		var b strings.Builder
		b.WriteString("[")
		first := true
		for svc := range docs {
			if !first {
				b.WriteString(",")
			}
			first = false
			b.WriteString(`{"service":"` + svc + `","url":"` + srv.URL + "/v1/" + svc + `"}`)
		}
		b.WriteString("]")
		_, _ = w.Write([]byte(b.String()))
	})

	for svc, resources := range docs {
		svc, resources := svc, resources
		mux.HandleFunc("/v1/"+svc, func(w http.ResponseWriter, _ *http.Request) {
			if svc == failService {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			var b strings.Builder
			b.WriteString(`{"Name":"` + svc + `","Resources":[`)
			for i, r := range resources {
				if i > 0 {
					b.WriteString(",")
				}
				b.WriteString(`{"Name":"` + r + `"}`)
			}
			b.WriteString("]}")
			_, _ = w.Write([]byte(b.String()))
		})
	}
	return srv
}

func TestFetchServiceReference_SynthesizesKeys(t *testing.T) {
	docs := map[string][]string{
		"dynamodb": {"table", "stream"},
		"macie2":   {"ClassificationJob"},
	}
	srv := srTestServer(t, docs, "")

	got, err := fetchServiceReferenceFrom(context.Background(), srv.URL+"/index", http.DefaultClient)
	if err != nil {
		t.Fatalf("fetchServiceReferenceFrom: %v", err)
	}

	want := []string{
		"AWS::dynamodb::stream",
		"AWS::dynamodb::table",
		"AWS::macie2::ClassificationJob",
	}
	keys := make([]string, len(got))
	for i, u := range got {
		keys[i] = u.Key
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Errorf("keys = %v; want %v", keys, want)
	}

	// Service grouping carries the SR service segment verbatim.
	for _, u := range got {
		if !strings.HasPrefix(u.Key, "AWS::"+u.Service+"::") {
			t.Errorf("key %q service segment %q mismatch", u.Key, u.Service)
		}
	}
}

func TestFetchServiceReference_PerServiceErrorIsFatal(t *testing.T) {
	docs := map[string][]string{
		"dynamodb": {"table"},
		"macie2":   {"ClassificationJob"},
	}
	srv := srTestServer(t, docs, "macie2")

	if _, err := fetchServiceReferenceFrom(context.Background(), srv.URL+"/index", http.DefaultClient); err == nil {
		t.Fatal("want error when a per-service fetch fails, got nil")
	}
}

func TestFetchServiceReference_IndexErrorIsFatal(t *testing.T) {
	srv := srTestServer(t, map[string][]string{"dynamodb": {"table"}}, "")
	// Point at a path the mux doesn't serve as an index array.
	if _, err := fetchServiceReferenceFrom(context.Background(), srv.URL+"/missing", http.DefaultClient); err == nil {
		t.Fatal("want error when the index fetch fails, got nil")
	}
}
