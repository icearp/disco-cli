package aws

import "testing"

func TestRDSARN(t *testing.T) {
	got := rdsARN("us-east-1", "123456789012", "db", "mydb")
	want := "arn:aws:rds:us-east-1:123456789012:db:mydb"
	if got != want {
		t.Errorf("rdsARN = %q, want %q", got, want)
	}
}

func TestApigatewayARN(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"single segment", []string{"restapis"}, "arn:aws:apigateway:us-east-1::/restapis"},
		{"multi segment", []string{"restapis", "abc", "stages", "prod"}, "arn:aws:apigateway:us-east-1::/restapis/abc/stages/prod"},
		{"empty path", nil, "arn:aws:apigateway:us-east-1::/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := apigatewayARN("us-east-1", tt.in...); got != tt.want {
				t.Errorf("apigatewayARN = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLogGroupNativeIDFromName(t *testing.T) {
	got := logGroupNativeIDFromName("123456789012", "us-east-1", "/aws/lambda/foo")
	want := "arn:aws:logs:us-east-1:123456789012:log-group:/aws/lambda/foo"
	if got != want {
		t.Errorf("logGroupNativeIDFromName = %q, want %q", got, want)
	}
}

func TestMacieSessionNativeID(t *testing.T) {
	got := macieSessionNativeID("123456789012", "us-east-1")
	want := "arn:aws:macie2:us-east-1:123456789012:session"
	if got != want {
		t.Errorf("macieSessionNativeID = %q, want %q", got, want)
	}
}

func TestSSOAssignmentNativeID(t *testing.T) {
	psArn := "arn:aws:sso:::permissionSet/ssoins-1/ps-2"
	got := ssoAssignmentNativeID(psArn, "111122223333", "USER", "u-1")
	want := psArn + "/account/111122223333/USER/u-1"
	if got != want {
		t.Errorf("ssoAssignmentNativeID = %q, want %q", got, want)
	}
}

func TestIdentityStoreNativeIDs(t *testing.T) {
	if got, want := identityStoreUserNativeID("123456789012", "d-9067", "u-1"),
		"arn:aws:identitystore::123456789012:user/d-9067/u-1"; got != want {
		t.Errorf("identityStoreUserNativeID = %q, want %q", got, want)
	}
	if got, want := identityStoreGroupNativeID("123456789012", "d-9067", "g-1"),
		"arn:aws:identitystore::123456789012:group/d-9067/g-1"; got != want {
		t.Errorf("identityStoreGroupNativeID = %q, want %q", got, want)
	}
}
