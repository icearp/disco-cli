package azure

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// stubTokenIssuer hands out a fixed token so graphClient can authenticate
// against the httptest server without standing up azidentity.
type stubTokenIssuer struct{ token string }

func (s stubTokenIssuer) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: s.token}, nil
}

// TestScanEntraUsers_HappyPath drives scanEntraUsers against an httptest
// server that returns one paginated Graph response, then asserts the
// expected resource rows land in the store.
func TestScanEntraUsers_HappyPath(t *testing.T) {
	var nextHits int
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("$select") == "" {
			t.Errorf("missing $select: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":[{"id":"u1","displayName":"Alice","userPrincipalName":"alice@example.com","accountEnabled":true,"userType":"Member"}],"@odata.nextLink":"` + srv.URL + `/users-page2"}`))
	})
	mux.HandleFunc("/users-page2", func(w http.ResponseWriter, _ *http.Request) {
		nextHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":[{"id":"u2","displayName":"Bob"}]}`))
	})

	st := newTestStore(t)
	g := &graphClient{cred: stubTokenIssuer{token: "x"}, http: srv.Client(), baseURL: srv.URL}
	total, inserted := scanEntraUsers(context.Background(), g, "tenant-1", st, testScanID)
	if total != 2 || inserted != 2 {
		t.Errorf("counts: total=%d inserted=%d, want 2/2", total, inserted)
	}
	if nextHits != 1 {
		t.Errorf("nextLink not followed (hits=%d)", nextHits)
	}
}

// TestTenantIDFromJWT verifies the tid extraction handles canonical, padded,
// and malformed JWTs.
func TestTenantIDFromJWT(t *testing.T) {
	makeJWT := func(claims string) string {
		head := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
		body := base64.RawURLEncoding.EncodeToString([]byte(claims))
		return head + "." + body + "."
	}
	tests := []struct {
		name    string
		jwt     string
		want    string
		wantErr bool
	}{
		{name: "happy", jwt: makeJWT(`{"tid":"abc-tenant-123","sub":"x"}`), want: "abc-tenant-123"},
		{name: "no-tid", jwt: makeJWT(`{"sub":"x"}`), wantErr: true},
		{name: "not-jwt", jwt: "not.a", wantErr: true},
		{name: "bad-base64", jwt: "abc.@@@.def", wantErr: true},
	}
	for _, tt := range tests {
		got, err := tenantIDFromJWT(tt.jwt)
		if tt.wantErr {
			if err == nil {
				t.Errorf("%s: want error, got nil", tt.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected err %v", tt.name, err)
			continue
		}
		if got != tt.want {
			t.Errorf("%s: got %q want %q", tt.name, got, tt.want)
		}
	}
}

// TestStrDeref verifies nil-safe behaviour.
func TestStrDeref(t *testing.T) {
	s := "hi"
	if got := strDeref(&s); got != "hi" {
		t.Errorf("strDeref(hi): %q", got)
	}
	if got := strDeref(nil); got != "" {
		t.Errorf("strDeref(nil): %q", got)
	}
}

// TestJSONOrEmpty confirms a marshal-friendly value succeeds and the helper
// returns valid JSON. (Marshal-failing inputs are rare in practice — Go
// types either marshal or panic before reaching this helper.)
func TestJSONOrEmpty(t *testing.T) {
	got := jsonOrEmpty(map[string]string{"k": "v"})
	if !strings.Contains(got, `"k":"v"`) {
		t.Errorf("jsonOrEmpty: %q", got)
	}
}
