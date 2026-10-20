package redact

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	testAccessKey   = "AKIAIOSFODNN7EXAMPLE"
	testSessionKey  = "ASIAIOSFODNN7EXAMPLE"
	testJWT         = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	testPEM         = "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA\n-----END RSA PRIVATE KEY-----"
	testUserinfoURL = "postgres://app:hunter2@db.example.com:5432/app"
	testTokenURL    = "https://ghp_tokenonly@github.com/org/repo.git"
	testConnString  = "Server=db.example.com;Database=app;User Id=app;Password=hunter2;"
	testQueryToken  = "https://api.example.com/v1/hook?region=us&token=abc"
	testRandomToken = "q2Wx8zPl0vB7nR4tY6uK3mHs"
	testUUID        = "9f8e7d6c-5b4a-4321-8fed-cba987654321"
	testARN         = "arn:aws:secretsmanager:us-east-1:123456789012:secret:db-AbCdEf"
	testSSMRef      = "{{resolve:ssm:/app/db-host:1}}"
)

func TestIsCredentialShape(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{testAccessKey, true},
		{"key " + testSessionKey + " in prose", true},
		{"arn:aws:s3:::x" + testAccessKey, true},
		{"https://h.example.com/p=1?token=abc", true},
		{` {"a":1}`, true},
		{"arn:aws:iam::123456789012:user/x:password=hunter2", true},
		{testJWT, true},
		{testPEM, true},
		{testUserinfoURL, true},
		{testTokenURL, true},
		{testConnString, true},
		{testQueryToken, true},
		{`{"API_TOKEN":"x"}`, true},
		{`["a","b"]`, true},
		{"AKIA-not-a-key", false},
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

func TestIsReference(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{testARN, true},
		{"arn:aws:s3:::my-bucket", true},
		{"arn:aws:iam::aws:policy/ReadOnlyAccess", true},
		{"arn:aws-us-gov:ssm:us-gov-west-1:123456789012:parameter/app/db", true},
		{testSSMRef, true},
		{"{{resolve:ssm-secure:/app/db-pass:3}}", true},
		{"{{resolve:secretsmanager:db:SecretString:password}}", true},
		{"/aws/reference/secretsmanager/db-creds", true},
		{"arn:aws-cn:s3:::my-bucket", true},
		{"arn:aws-eusc:s3:eusc-de-east-1:123456789012:b", true},
		{"arn:aws:lambda:us-east-1:123456789012:function:f:$LATEST", true},
		{"{{resolve:ssm:arn:aws:ssm:us-east-1:123456789012:parameter/app/db:2}}", true},
		{"arn:aws-iso-b:s3:::my-bucket", true},
		{"{{resolve:ssm:/app/db-host}}", true},
		{"arn:aws:iam::12345:role/x", false},
		{"arn:awsHUNTER:a:::b", false},
		{"arn:awshunter:a:::b", false},
		{"arn:aws:s3:hunter2:123456789012:b", false},
		{"arn:aws:lambda:us-east-1:123456789012:function:f$LATEST", false},
		{"arn:aws:lambda:us-east-1:123456789012:function:$LATEST:f", false},
		{"{{resolve:ssm:arn:aws:ssm:us-east-1:123456789012:parameter/x$y}}", false},
		{"arn:aws:x:::Hunter2!pass", false},
		{"arn:aws:s3:::bucket/x#password:hunter2", false},
		{"arn:aws:s3:Hunter2:123456789012:b", false},
		{"arn:aws:s3:us-east-1:hunter2:b", false},
		{"arn:aws:s3:::" + strings.Repeat("a", 2048-len("arn:aws:s3:::")), true},
		{"arn:aws:s3:::" + strings.Repeat("a", 2049-len("arn:aws:s3:::")), false},
		{"{{resolve:ssm:Hunter2!pass}}", false},
		{"{{resolve:ssm:/a:latest}}", false},
		{"{{resolve:ssm:a\nhunter2 is the password}}", false},
		{"{{resolve:secretsmanager:x:SecretString:pw}}\n", false},
		{"{{resolve:secretsmanager:a b}}", false},
		{"/aws/reference/secretsmanager/a b", false},
		{"see arn:aws:s3:::my-bucket", false},
		{"{{resolve:other:x}}", false},
		{"/app/db-host", false},
		{"", false},
	} {
		if got := isReference(tc.in); got != tc.want {
			t.Errorf("isReference(%q) = %v; want %v", tc.in, got, tc.want)
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

func TestIsResourceID(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"sg-0123abcd", true},
		{"subnet-0123456789abcdef0", true},
		{"eipalloc-0123456789abcdef0", true},
		{"i-0123456789abcdef0", true},
		{"sg-0123abc", false},
		{"sg-0123456789ABCDEF0", false},
		{"sg-0123456789abcdef01", false},
		{"my-secret-password", false},
		{"app-cafe1234", false},
		{"token-0123456789abcdef0", false},
		{"tgw-attach-0123456789abcdef0", true},
		{"vpce-0123abcd", true},
		{testUUID, false},
	} {
		if got := isResourceID(tc.in); got != tc.want {
			t.Errorf("isResourceID(%q) = %v; want %v", tc.in, got, tc.want)
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
	for _, tc := range []struct {
		name      string
		sensitive bool
		in        any
		want      any
	}{
		{"access key under innocuous key", false, testAccessKey, PlaceholderValueShape},
		{"jwt", false, testJWT, PlaceholderValueShape},
		{"pem", false, testPEM, PlaceholderValueShape},
		{"userinfo url", false, testUserinfoURL, PlaceholderValueShape},
		{"connection string", false, testConnString, PlaceholderValueShape},
		{"embedded json", false, `{"API_TOKEN":"x"}`, PlaceholderValueShape},
		{"random token under innocuous key", false, testRandomToken, PlaceholderRule},
		{"credential shape under sensitive key", true, testAccessKey, PlaceholderValueShape},
		{"credential shape inside an arn", true, "arn:aws:s3:::uploads/" + testAccessKey, PlaceholderValueShape},
		{"password under sensitive key", true, "hunter2", PlaceholderKeyName},
		{"numeric string under sensitive key", true, "4821", PlaceholderKeyName},
		{"numeric string", false, "8080", PlaceholderRule},
		{"json number", false, float64(4111111111111111), PlaceholderRule},
		{"json number under sensitive key", true, float64(4821), PlaceholderKeyName},
		{"resource id under sensitive key", true, "sg-0123abcd", PlaceholderKeyName},
		{"object at terminal", false, map[string]any{"a": "x", "b": []any{"y", true}}, map[string]any{"a": PlaceholderRule, "b": []any{PlaceholderRule, PlaceholderRule}}},
		{"array at terminal", false, []any{"x"}, []any{PlaceholderRule}},
		{"arn under sensitive key", true, testARN, testARN},
		{"ssm reference under sensitive key", true, testSSMRef, testSSMRef},
		{"bool string under sensitive key", true, "true", "true"},
		{"json bool under sensitive key", true, true, true},
		{"nil", true, nil, nil},
		{"resource id", false, "subnet-0123456789abcdef0", "subnet-0123456789abcdef0"},
		{"bool string", false, "False", "False"},
		{"arn under innocuous key", false, testARN, testARN},
		{"resource id list is not one id", false, "sg-0123456789abcdef0,sg-0fedcba9876543210", PlaceholderRule},
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
		if err := json.Unmarshal([]byte(Apply(tc.typ, tc.in)), &got); err != nil {
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
	if err := json.Unmarshal([]byte(Apply("t", `{"Settings":[{"Name":"DB_PASSWORD","Ns":"aws:rds","Value":"x"}]}`)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	s := got["Settings"].([]any)[0].(map[string]any)
	if s["Name"] != "DB_PASSWORD" || s["Ns"] != "aws:rds" {
		t.Errorf("siblings changed: %v", s)
	}
}

// KeyIsSensitive judges a value under an innocuous literal as if its key
// were secret-looking: a plain value is redacted, a reference kept.
func TestApply_RedactUnlessSafeKeyIsSensitive(t *testing.T) {
	resetForTest()
	Register(TypeRules{Type: "t", Attributes: []Rule{{Path: "Auth[*].Value", Mode: RedactUnlessSafe, KeyIsSensitive: true}}})
	var got map[string]any
	if err := json.Unmarshal([]byte(Apply("t", `{"Auth":[{"Value":"hunter2"},{"Value":"`+testARN+`"}]}`)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	auth := got["Auth"].([]any)
	if v := auth[0].(map[string]any)["Value"]; v != PlaceholderKeyName {
		t.Errorf("plain value = %v; want %v", v, PlaceholderKeyName)
	}
	if v := auth[1].(map[string]any)["Value"]; v != testARN {
		t.Errorf("arn = %v; want %v", v, testARN)
	}
}

// Every leak the security review found is redacted, except the documented
// residual: a secret written in an allowlisted shape with no credential
// shape inside it.
func TestApply_RedactUnlessSafeReviewLeaks(t *testing.T) {
	resetForTest()
	Register(TypeRules{Type: "t", Attributes: []Rule{{Path: "Settings[*].Value", Mode: RedactUnlessSafe, KeyField: "Name"}}})
	for _, tc := range []struct {
		setting string
		want    any
	}{
		{`{"Name":"ADMIN_PASSCODE","Value":"482915"}`, PlaceholderRule},
		{`{"Name":"STRIPE_KEY_LIVE","Value":"4111111111111111"}`, PlaceholderRule},
		{`{"Name":"X","Value":4111111111111111}`, PlaceholderRule},
		{`{"Name":"PIN","Value":4821}`, PlaceholderKeyName},
		{`{"Name":"BACKUP_ROLE","Value":"arn:aws:x:::Hunter2!pass"}`, PlaceholderRule},
		{`{"Name":"LEGACY","Value":"{{resolve:ssm:Hunter2!pass}}"}`, PlaceholderRule},
		{`{"Name":"DB_PW","Value":"app-cafe1234"}`, PlaceholderRule},
		{`{"Value":"998877"}`, PlaceholderKeyName},
		{`{"Name":"X","Value":"arn:aws:a:::xAKIAIOSFODNN7EXAMPLE"}`, PlaceholderValueShape},
		{`{"Name":"X","Value":"arn:aws:secretsmanager:us-east-1:123456789012:secret:a AKIAIOSFODNN7EXAMPLE"}`, PlaceholderValueShape},
		// Residual: ARN-shaped, no credential shape inside, so kept.
		{`{"Name":"X","Value":"arn:aws:a:::ghp_abcdefghijklmnopqrstuvwxyz0123456789"}`, "arn:aws:a:::ghp_abcdefghijklmnopqrstuvwxyz0123456789"},
	} {
		var got map[string]any
		if err := json.Unmarshal([]byte(Apply("t", `{"Settings":[`+tc.setting+`]}`)), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if v := settingValue(got); v != tc.want {
			t.Errorf("Apply(%s) value = %v; want %v", tc.setting, v, tc.want)
		}
	}
}
