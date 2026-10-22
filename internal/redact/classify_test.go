package redact

import (
	"encoding/json"
	"regexp"
	"testing"
)

const (
	testJWT         = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	testPEM         = "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA\n-----END RSA PRIVATE KEY-----"
	testUserinfoURL = "postgres://app:hunter2@db.example.com:5432/app"
	testTokenURL    = "https://ghp_tokenonly@github.com/org/repo.git"
	testConnString  = "Server=db.example.com;Database=app;User Id=app;Password=hunter2;"
	testQueryToken  = "https://api.example.com/v1/hook?region=us&token=abc"
	testRandomToken = "q2Wx8zPl0vB7nR4tY6uK3mHs"

	// Values of the shapes testSignatures registers.
	testCred  = "TESTCRED1234"
	testRef   = "ref://vault/db-pass"
	testResID = "res-0123abcd"
)

// testSignatures stand in for a provider package's: this package's tests
// cannot import one.
var testSignatures = Signatures{
	Credential: []func(string) bool{regexp.MustCompile(`TESTCRED[0-9]{4}`).MatchString},
	Reference:  []func(string) bool{regexp.MustCompile(`^ref://[A-Za-z0-9/-]+$`).MatchString},
	ResourceID: []func(string) bool{regexp.MustCompile(`^res-[0-9a-f]{8}$`).MatchString},
}

// withSignatures clears the registered signatures, registers each of sigs,
// and restores the previous set when the test ends.
func withSignatures(t *testing.T, sigs ...Signatures) {
	t.Helper()
	sigMu.Lock()
	saved := registered
	registered = Signatures{}
	sigMu.Unlock()
	t.Cleanup(func() {
		sigMu.Lock()
		registered = saved
		sigMu.Unlock()
	})
	for _, s := range sigs {
		RegisterSignatures(s)
	}
}

func TestIsCredentialShape(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"https://h.example.com/p=1?token=abc", true},
		{` {"a":1}`, true},
		{"ref:iam::123456789012:user/x:password=hunter2", true},
		{testJWT, true},
		{testPEM, true},
		{testUserinfoURL, true},
		{testTokenURL, true},
		{testConnString, true},
		{testQueryToken, true},
		{`{"API_TOKEN":"x"}`, true},
		{`["a","b"]`, true},
		{"eyJhbGciOiJIUzI1NiJ9", false},
		{"-----BEGIN", false},
		{"https://db.example.com:5432/app", false},
		{"https://example.com/users/@me", false},
		{"Server=db.example.com;Database=app", false},
		{"https://api.example.com/v1/hook?region=us", false},
		{"{not json", false},
		{"t3.micro", false},
	} {
		if got := isCredentialShape(tc.in); got != tc.want {
			t.Errorf("isCredentialShape(%q) = %v; want %v", tc.in, got, tc.want)
		}
	}
}

func TestIsBoolString(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"true", true},
		{"FALSE", true},
		{"True", true},
		{"yes", false},
		{"1", false},
		{"truthy", false},
		{"", false},
	} {
		if got := isBoolString(tc.in); got != tc.want {
			t.Errorf("isBoolString(%q) = %v; want %v", tc.in, got, tc.want)
		}
	}
}

func TestIsSensitiveKey(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"DB_PASSWORD", true},
		{"dbPassword", true},
		{"DBPASSWORD", true},
		{"db_passwd", true},
		{"DB_PASS", true},
		{"dbPass", true},
		{"PWD", true},
		{"PIN", true},
		{"BASIC_AUTH", true},
		{"creds", true},
		{"ClientSecret", true},
		{"GITHUB_TOKEN", true},
		{"AWS_ACCESS_KEY_ID", true},
		{"credentials", true},
		{"API_KEY", true},
		{"apiKey", true},
		{"stripeKey", true},
		{"private-key-pem", true},
		{"ssh_passphrase", true},
		{"InstanceType", false},
		{"EC2KeyName", false},
		{"KMS_KEY_ID", false},
		{"BYPASS_CACHE", false},
		{"OAUTH_CLIENT_ID", false},
		{"Spinner", false},
		{"Value", false},
		{"", false},
	} {
		if got := isSensitiveKey(tc.in); got != tc.want {
			t.Errorf("isSensitiveKey(%q) = %v; want %v", tc.in, got, tc.want)
		}
	}
}

func TestClassify_ReasonTags(t *testing.T) {
	withSignatures(t, testSignatures)
	for _, tc := range []struct {
		name      string
		sensitive bool
		in        any
		want      any
	}{
		{"registered credential under innocuous key", false, testCred, PlaceholderValueShape},
		{"jwt", false, testJWT, PlaceholderValueShape},
		{"pem", false, testPEM, PlaceholderValueShape},
		{"userinfo url", false, testUserinfoURL, PlaceholderValueShape},
		{"connection string", false, testConnString, PlaceholderValueShape},
		{"embedded json", false, `{"API_TOKEN":"x"}`, PlaceholderValueShape},
		{"random token under innocuous key", false, testRandomToken, PlaceholderRule},
		{"credential shape under sensitive key", true, testCred, PlaceholderValueShape},
		{"credential shape inside a reference", true, "ref://uploads/" + testCred, PlaceholderValueShape},
		{"password under sensitive key", true, "hunter2", PlaceholderKeyName},
		{"numeric string under sensitive key", true, "4821", PlaceholderKeyName},
		{"numeric string", false, "8080", PlaceholderRule},
		{"json number", false, float64(4111111111111111), PlaceholderRule},
		{"json number under sensitive key", true, float64(4821), PlaceholderKeyName},
		{"resource id under sensitive key", true, testResID, PlaceholderKeyName},
		{"object at terminal", false, map[string]any{"a": "x", "b": []any{"y", true}}, map[string]any{"a": PlaceholderRule, "b": []any{PlaceholderRule, PlaceholderRule}}},
		{"array at terminal", false, []any{"x"}, []any{PlaceholderRule}},
		{"reference under sensitive key", true, testRef, testRef},
		{"bool string under sensitive key", true, "true", "true"},
		{"json bool under sensitive key", true, true, true},
		{"nil", true, nil, nil},
		{"resource id", false, testResID, testResID},
		{"bool string", false, "False", "False"},
		{"reference under innocuous key", false, testRef, testRef},
		{"resource id list is not one id", false, testResID + "," + testResID, PlaceholderRule},
		{"plain word under innocuous key", false, "t3.micro", PlaceholderRule},
		{"password under innocuous key", false, "hunter2", PlaceholderRule},
		{"empty string", false, "", PlaceholderRule},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(tc.sensitive, tc.in)
			if mustJSON(t, got) != mustJSON(t, tc.want) {
				t.Errorf("classify(%v, %v) = %v; want %v", tc.sensitive, tc.in, got, tc.want)
			}
		})
	}
}

// The classifier knows only the core shapes plus what was registered, and
// applies every registration together: a binary that links no provider
// package does not know any provider's shapes.
func TestClassify_MergesRegisteredSignatures(t *testing.T) {
	cases := []struct {
		name      string
		sensitive bool
		in        string
		unknown   any
		known     any
	}{
		{"credential", false, "x" + testCred, PlaceholderRule, PlaceholderValueShape},
		{"reference", true, testRef, PlaceholderKeyName, testRef},
		{"resource id", false, testResID, PlaceholderRule, testResID},
		{"core credential", false, testJWT, PlaceholderValueShape, PlaceholderValueShape},
	}
	run := func(t *testing.T, want func(int) any) {
		t.Helper()
		for i, tc := range cases {
			if got := classify(tc.sensitive, tc.in); got != want(i) {
				t.Errorf("%s: classify(%v, %q) = %v; want %v", tc.name, tc.sensitive, tc.in, got, want(i))
			}
		}
	}
	t.Run("none registered", func(t *testing.T) {
		withSignatures(t)
		run(t, func(i int) any { return cases[i].unknown })
	})
	t.Run("two registrations", func(t *testing.T) {
		withSignatures(t,
			Signatures{Credential: testSignatures.Credential},
			Signatures{Reference: testSignatures.Reference, ResourceID: testSignatures.ResourceID},
		)
		run(t, func(i int) any { return cases[i].known })
	})
}

// The key name RedactUnlessSafe classifies by is the KeyField sibling for an
// array of name/value objects (a missing or non-string one counting as
// sensitive), the map key under a map wildcard, the literal for a literal
// terminal, and the array's key for an array element.
func TestApply_RedactUnlessSafeKeyName(t *testing.T) {
	resetForTest()
	Register(TypeRules{Type: "sibling", Attributes: []Rule{{Path: "Settings[*].Value", Mode: RedactUnlessSafe, KeyField: "Name"}}})
	Register(TypeRules{Type: "map", Attributes: []Rule{{Path: "Env.*", Mode: RedactUnlessSafe}}})
	Register(TypeRules{Type: "literal", Attributes: []Rule{{Path: "Auth.Password", Mode: RedactUnlessSafe}}})
	Register(TypeRules{Type: "array", Attributes: []Rule{{Path: "*[*]", Mode: RedactUnlessSafe}}})
	for _, tc := range []struct {
		typ, in string
		path    func(map[string]any) any
		want    string
	}{
		{"sibling", `{"Settings":[{"Name":"DB_PASSWORD","Value":"hunter2"}]}`, settingValue, PlaceholderKeyName},
		{"sibling", `{"Settings":[{"Name":"DB_HOST","Value":"hunter2"}]}`, settingValue, PlaceholderRule},
		{"sibling", `{"Settings":[{"Value":"hunter2"}]}`, settingValue, PlaceholderKeyName},
		{"sibling", `{"Settings":[{"Name":7,"Value":"hunter2"}]}`, settingValue, PlaceholderKeyName},
		{"array", `{"Tokens":["hunter2"]}`, func(m map[string]any) any { return m["Tokens"].([]any)[0] }, PlaceholderKeyName},
		{"array", `{"Hosts":["hunter2"]}`, func(m map[string]any) any { return m["Hosts"].([]any)[0] }, PlaceholderRule},
		{"map", `{"Env":{"DB_PASSWORD":"hunter2"}}`, func(m map[string]any) any { return m["Env"].(map[string]any)["DB_PASSWORD"] }, PlaceholderKeyName},
		{"map", `{"Env":{"DB_HOST":"hunter2"}}`, func(m map[string]any) any { return m["Env"].(map[string]any)["DB_HOST"] }, PlaceholderRule},
		{"literal", `{"Auth":{"Password":"hunter2"}}`, func(m map[string]any) any { return m["Auth"].(map[string]any)["Password"] }, PlaceholderKeyName},
	} {
		var got map[string]any
		if err := json.Unmarshal([]byte(mustApply(t, tc.typ, tc.in)), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if v := tc.path(got); v != tc.want {
			t.Errorf("Apply(%q, %s) value = %v; want %v", tc.typ, tc.in, v, tc.want)
		}
	}
}

func settingValue(m map[string]any) any {
	return m["Settings"].([]any)[0].(map[string]any)["Value"]
}

// KeyField names only the terminal's sibling: the sibling and the other
// fields of the object stay as they are.
func TestApply_RedactUnlessSafeKeepsSiblings(t *testing.T) {
	resetForTest()
	Register(TypeRules{Type: "t", Attributes: []Rule{{Path: "Settings[*].Value", Mode: RedactUnlessSafe, KeyField: "Name"}}})
	var got map[string]any
	if err := json.Unmarshal([]byte(mustApply(t, "t", `{"Settings":[{"Name":"DB_PASSWORD","Ns":"vendor:rds","Value":"x"}]}`)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s := got["Settings"].([]any)[0].(map[string]any)
	if s["Name"] != "DB_PASSWORD" || s["Ns"] != "vendor:rds" {
		t.Errorf("siblings changed: %v", s)
	}
}

// KeyIsSensitive judges a value under an innocuous literal as if its key
// were secret-looking: a plain value is redacted, a reference kept.
func TestApply_RedactUnlessSafeKeyIsSensitive(t *testing.T) {
	resetForTest()
	withSignatures(t, testSignatures)
	Register(TypeRules{Type: "t", Attributes: []Rule{{Path: "Auth[*].Value", Mode: RedactUnlessSafe, KeyIsSensitive: true}}})
	var got map[string]any
	if err := json.Unmarshal([]byte(mustApply(t, "t", `{"Auth":[{"Value":"hunter2"},{"Value":"`+testRef+`"}]}`)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	auth := got["Auth"].([]any)
	if v := auth[0].(map[string]any)["Value"]; v != PlaceholderKeyName {
		t.Errorf("plain value = %v; want %v", v, PlaceholderKeyName)
	}
	if v := auth[1].(map[string]any)["Value"]; v != testRef {
		t.Errorf("reference = %v; want %v", v, testRef)
	}
}

// Every leak the security review found is redacted, except the documented
// residual: a secret written in an allowlisted shape with no credential
// shape inside it. The provider-shaped cases of the same review run where a
// provider is linked (internal/providers/all).
func TestApply_RedactUnlessSafeReviewLeaks(t *testing.T) {
	resetForTest()
	withSignatures(t, testSignatures)
	Register(TypeRules{Type: "t", Attributes: []Rule{{Path: "Settings[*].Value", Mode: RedactUnlessSafe, KeyField: "Name"}}})
	for _, tc := range []struct {
		setting string
		want    any
	}{
		{`{"Name":"ADMIN_PASSCODE","Value":"482915"}`, PlaceholderRule},
		{`{"Name":"STRIPE_KEY_LIVE","Value":"4111111111111111"}`, PlaceholderRule},
		{`{"Name":"X","Value":4111111111111111}`, PlaceholderRule},
		{`{"Name":"PIN","Value":4821}`, PlaceholderKeyName},
		{`{"Name":"BACKUP_ROLE","Value":"ref://x/Hunter2!pass"}`, PlaceholderRule},
		{`{"Name":"DB_PW","Value":"app-cafe1234"}`, PlaceholderRule},
		{`{"Value":"998877"}`, PlaceholderKeyName},
		{`{"Name":"X","Value":"ref://a/x` + testCred + `"}`, PlaceholderValueShape},
		{`{"Name":"X","Value":"ref://a/secret a ` + testCred + `"}`, PlaceholderValueShape},
		// Residual: reference-shaped, no credential shape inside, so kept.
		{`{"Name":"X","Value":"ref://a/ghp-abcdefghijklmnopqrstuvwxyz0123456789"}`, "ref://a/ghp-abcdefghijklmnopqrstuvwxyz0123456789"},
	} {
		var got map[string]any
		if err := json.Unmarshal([]byte(mustApply(t, "t", `{"Settings":[`+tc.setting+`]}`)), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if v := settingValue(got); v != tc.want {
			t.Errorf("Apply(%s) value = %v; want %v", tc.setting, v, tc.want)
		}
	}
}
