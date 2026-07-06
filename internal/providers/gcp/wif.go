package gcp

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google/externalaccount"
)

// Workload Identity Federation keyless GCP access — AWS/ECS env contract.
//
// These two env vars enable keyless GCP auth when disco's AWS identity is
// reachable only via the ECS container-credentials endpoint (ECS/Fargate).
// Google's built-in AWS external-account source reads only AWS_* env vars or
// the EC2 IMDS endpoint — neither carries a Fargate task role — so this path
// supplies credentials programmatically via the AWS SDK default chain (which
// does speak the container-credentials endpoint). Elsewhere, prefer the
// standard gcp.credential_config_file / --credential-config path, which uses
// Google's native sources.
//
// Both values are non-secret public identifiers: audience names the
// workload-identity provider; service-account email names the read-only
// account to impersonate. The AWS subject the provider trusts is the running
// task's own role, retrieved at runtime — no key or token is stored or
// transferred. An external orchestrator can set these on its scanner task,
// but any disco run on ECS/Fargate can use them.
const (
	envWIFAudience       = "DISCO_GCP_WIF_AUDIENCE"
	envWIFServiceAccount = "DISCO_GCP_WIF_SERVICE_ACCOUNT"
)

// wifEnvCredentials returns the workload-identity audience and impersonated
// service-account email from the env contract. Either may be empty.
func wifEnvCredentials() (audience, serviceAccount string) {
	return os.Getenv(envWIFAudience), os.Getenv(envWIFServiceAccount)
}

// wifConfigured reports whether both halves of the WIF env contract are set.
func wifConfigured(audience, serviceAccount string) bool {
	return audience != "" && serviceAccount != ""
}

// wifTokenSource builds a keyless GCP token source that exchanges the running
// task's own AWS identity (via Google's STS) for a short-lived token
// impersonating serviceAccount. No service-account key is involved.
//
// The AWS subject credentials come from [ecsAwsSupplier], not a
// credential_source JSON file, because Google's built-in AWS source only
// reads env vars or the EC2 IMDS endpoint — neither carries a Fargate task
// role. The supplier delegates to the AWS SDK default chain, which speaks
// the ECS container-credentials endpoint.
func wifTokenSource(ctx context.Context, audience, serviceAccount string, scopes []string) (oauth2.TokenSource, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("wif: load aws config: %w", err)
	}

	conf := externalaccount.Config{
		Audience:                       audience,
		SubjectTokenType:               "urn:ietf:params:aws:token-type:aws4_request",
		ServiceAccountImpersonationURL: "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/" + serviceAccount + ":generateAccessToken",
		Scopes:                         scopes,
		AwsSecurityCredentialsSupplier: ecsAwsSupplier{region: awsCfg.Region, provider: awsCfg.Credentials},
	}
	return externalaccount.NewTokenSource(ctx, conf)
}

// ecsAwsSupplier supplies the running task's AWS identity to Google's
// external-account token exchange. Zero value unusable — build it from a
// loaded aws.Config so it resolves Fargate task-role credentials via the ECS
// container-credentials endpoint.
type ecsAwsSupplier struct {
	region   string
	provider aws.CredentialsProvider
}

// AwsRegion returns the region the AWS identity signs requests for.
func (s ecsAwsSupplier) AwsRegion(ctx context.Context, _ externalaccount.SupplierOptions) (string, error) {
	if s.region == "" {
		return "", fmt.Errorf("wif: no AWS region resolved (set AWS_REGION)")
	}
	return s.region, nil
}

// AwsSecurityCredentials retrieves the scanner task's current AWS credentials.
func (s ecsAwsSupplier) AwsSecurityCredentials(ctx context.Context, _ externalaccount.SupplierOptions) (*externalaccount.AwsSecurityCredentials, error) {
	creds, err := s.provider.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("wif: retrieve aws credentials: %w", err)
	}
	return &externalaccount.AwsSecurityCredentials{
		AccessKeyID:     creds.AccessKeyID,
		SecretAccessKey: creds.SecretAccessKey,
		SessionToken:    creds.SessionToken,
	}, nil
}
