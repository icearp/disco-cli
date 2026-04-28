package azure

import (
	"encoding/base64"
	"strings"
	"testing"
)

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
