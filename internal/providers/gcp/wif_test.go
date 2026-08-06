package gcp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"golang.org/x/oauth2/google/externalaccount"
)

type stubCredsProvider struct {
	creds aws.Credentials
	err   error
}

func (s stubCredsProvider) Retrieve(_ context.Context) (aws.Credentials, error) {
	return s.creds, s.err
}

// TestEcsAwsSupplier_MapsCredentials verifies the supplier forwards the AWS
// SDK credentials into the externalaccount shape and reports the region.
func TestEcsAwsSupplier_MapsCredentials(t *testing.T) {
	sup := ecsAwsSupplier{
		region: "us-east-2",
		provider: stubCredsProvider{creds: aws.Credentials{
			AccessKeyID:     "AKIA",
			SecretAccessKey: "secret",
			SessionToken:    "token",
		}},
	}

	region, err := sup.AwsRegion(context.Background(), externalaccount.SupplierOptions{})
	if err != nil {
		t.Fatalf("region: %v", err)
	}
	if region != "us-east-2" {
		t.Errorf("region = %q, want us-east-2", region)
	}

	creds, err := sup.AwsSecurityCredentials(context.Background(), externalaccount.SupplierOptions{})
	if err != nil {
		t.Fatalf("creds: %v", err)
	}
	if creds.AccessKeyID != "AKIA" || creds.SecretAccessKey != "secret" || creds.SessionToken != "token" {
		t.Errorf("creds not mapped: %+v", creds)
	}
}

// TestEcsAwsSupplier_EmptyRegionErrors guards against silently signing with no
// region (which Google's STS exchange rejects with an opaque error).
func TestEcsAwsSupplier_EmptyRegionErrors(t *testing.T) {
	sup := ecsAwsSupplier{provider: stubCredsProvider{}}
	if _, err := sup.AwsRegion(context.Background(), externalaccount.SupplierOptions{}); err == nil {
		t.Error("expected error for empty region")
	}
}

// TestEcsAwsSupplier_RetrieveErrorPropagates verifies a credential-chain
// failure surfaces rather than yielding empty credentials.
func TestEcsAwsSupplier_RetrieveErrorPropagates(t *testing.T) {
	sup := ecsAwsSupplier{region: "us-east-2", provider: stubCredsProvider{err: errors.New("boom")}}
	if _, err := sup.AwsSecurityCredentials(context.Background(), externalaccount.SupplierOptions{}); err == nil {
		t.Error("expected retrieve error to propagate")
	}
}

const assumeRoleStubResponse = `<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <AssumeRoleResult>
    <Credentials>
      <AccessKeyId>ASIASTUB</AccessKeyId>
      <SecretAccessKey>stubsecret</SecretAccessKey>
      <SessionToken>stubtoken</SessionToken>
      <Expiration>2400-01-01T00:00:00Z</Expiration>
    </Credentials>
    <AssumedRoleUser>
      <Arn>arn:aws:sts::123456789012:assumed-role/disco-scanner-gcp/binding-abc</Arn>
      <AssumedRoleId>AROASTUB:binding-abc</AssumedRoleId>
    </AssumedRoleUser>
  </AssumeRoleResult>
</AssumeRoleResponse>`

// TestWifCredentials_AmbientWhenNoSession verifies the zero session presents
// the running identity unchanged — the single-tenant path.
func TestWifCredentials_AmbientWhenNoSession(t *testing.T) {
	ambient := stubCredsProvider{creds: aws.Credentials{AccessKeyID: "AMBIENT"}}
	got, err := wifSubjectCredentials(aws.Config{Region: "us-east-2", Credentials: ambient}, wifSession{})
	if err != nil {
		t.Fatalf("wifCredentials: %v", err)
	}
	creds, err := got.Retrieve(context.Background())
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if creds.AccessKeyID != "AMBIENT" {
		t.Errorf("AccessKeyID = %q, want the ambient credentials", creds.AccessKeyID)
	}
}

// TestWifCredentials_PresentsNamedSession is the load-bearing assertion for
// per-tenant federation: the session name must reach STS on the wire, because
// it is what lands in the assumed-role ARN a provider's attribute condition
// pins. A hop that assumed the role under any other session name would still
// return usable credentials and still fail against the customer.
func TestWifCredentials_PresentsNamedSession(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("Content-Type", "text/xml")
		_, _ = io.WriteString(w, assumeRoleStubResponse)
	}))
	defer srv.Close()

	cfg := aws.Config{
		Region:       "us-east-2",
		Credentials:  stubCredsProvider{creds: aws.Credentials{AccessKeyID: "AKIA", SecretAccessKey: "secret"}},
		BaseEndpoint: aws.String(srv.URL),
		HTTPClient:   srv.Client(),
	}
	const (
		roleARN = "arn:aws:iam::123456789012:role/disco-scanner-gcp"
		session = "binding-abc"
	)
	prov, err := wifSubjectCredentials(cfg, wifSession{roleARN: roleARN, sessionName: session})
	if err != nil {
		t.Fatalf("wifCredentials: %v", err)
	}
	creds, err := prov.Retrieve(context.Background())
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}

	if !strings.Contains(body, "RoleSessionName="+session) {
		t.Errorf("AssumeRole request did not carry RoleSessionName=%s: %s", session, body)
	}
	if !strings.Contains(body, "RoleArn=") || !strings.Contains(body, "disco-scanner-gcp") {
		t.Errorf("AssumeRole request did not carry the role arn: %s", body)
	}
	if creds.AccessKeyID != "ASIASTUB" {
		t.Errorf("AccessKeyID = %q, want the assumed session's key", creds.AccessKeyID)
	}
}

// TestWifCredentials_PartialSessionErrors pins the fail-closed half: a session
// configured with one env var missing must NOT quietly present the ambient
// identity, which is the exact substitution the session name exists to stop.
func TestWifCredentials_PartialSessionErrors(t *testing.T) {
	ambient := stubCredsProvider{creds: aws.Credentials{AccessKeyID: "AMBIENT"}}
	cfg := aws.Config{Region: "us-east-2", Credentials: ambient}
	for name, sess := range map[string]wifSession{
		"role arn only":     {roleARN: "arn:aws:iam::123456789012:role/disco-scanner-gcp"},
		"session name only": {sessionName: "binding-abc"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := wifSubjectCredentials(cfg, sess); err == nil {
				t.Fatal("expected a half-configured session to error")
			}
		})
	}
}

// TestWifEnvSource_FailsClosedWhenSessionRequested verifies a broken session
// yields a token source that errors, rather than ok=false which would send the
// caller to Application Default Credentials under a different identity.
func TestWifEnvSource_FailsClosedWhenSessionRequested(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-2")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv(envWIFRoleARN, "arn:aws:iam::123456789012:role/disco-scanner-gcp")
	t.Setenv(envWIFSessionName, "")

	ts, ok := wifEnvSource(context.Background(), "//iam.googleapis.com/x", "sa@p.iam.gserviceaccount.com", nil)
	if !ok {
		t.Fatal("expected the WIF source to be used, not an ADC fallback")
	}
	_, err := ts.Token()
	if err == nil {
		t.Fatal("expected every token request to fail")
	}
	if !strings.Contains(err.Error(), envWIFSessionName) {
		t.Errorf("error should name the missing env var, got %v", err)
	}
}
