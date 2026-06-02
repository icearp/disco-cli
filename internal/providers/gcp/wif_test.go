package gcp

import (
	"context"
	"errors"
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
