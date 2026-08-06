package gcp

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
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
//
// The second pair is optional and adds an AssumeRole hop before the exchange,
// so the presented identity carries a caller-chosen session name. See
// [wifSession] for why that matters. Both are likewise non-secret.
const (
	envWIFAudience       = "DISCO_GCP_WIF_AUDIENCE"
	envWIFServiceAccount = "DISCO_GCP_WIF_SERVICE_ACCOUNT"
	envWIFRoleARN        = "DISCO_GCP_WIF_ROLE_ARN"
	envWIFSessionName    = "DISCO_GCP_WIF_SESSION_NAME"
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

// wifSession is an optional AssumeRole hop taken before the token exchange.
//
// Google reads the AWS identity out of a signed GetCallerIdentity request, and
// an assumed-role ARN carries the session name:
// arn:aws:sts::<account>:assumed-role/<role>/<session>. A provider's attribute
// condition can therefore pin the session name, which is how one caller acting
// for many tenants proves WHICH grant it is exercising rather than only which
// role it holds. Without the hop the session name is whatever the platform
// chose — on ECS, the task id — which identifies nothing.
//
// Zero value means no hop: the running task's own identity is presented, which
// is what a single-tenant deployment wants.
type wifSession struct {
	roleARN     string
	sessionName string
}

// wifEnvSession reads the optional AssumeRole hop from the env contract.
func wifEnvSession() wifSession {
	return wifSession{roleARN: os.Getenv(envWIFRoleARN), sessionName: os.Getenv(envWIFSessionName)}
}

// requested reports whether either half was set, i.e. whether the caller meant
// to present a named session at all.
func (s wifSession) requested() bool { return s.roleARN != "" || s.sessionName != "" }

// complete reports whether both halves were set.
func (s wifSession) complete() bool { return s.roleARN != "" && s.sessionName != "" }

// wifSubjectCredentials resolves the AWS identity presented to Google's
// exchange: the ambient one, or an AssumeRole session named by sess.
//
// A half-set session is an error, not a fall back to the ambient identity. The
// caller named a specific identity; presenting a different one is how a
// federated grant gets exercised under the wrong name, which is the failure
// the session name exists to prevent.
func wifSubjectCredentials(awsCfg aws.Config, sess wifSession) (aws.CredentialsProvider, error) {
	if !sess.requested() {
		return awsCfg.Credentials, nil
	}
	if !sess.complete() {
		return nil, fmt.Errorf("wif: %s and %s must be set together", envWIFRoleARN, envWIFSessionName)
	}
	// CredentialsCache re-assumes on expiry — a scan outlives the one-hour
	// ceiling on an AssumeRole session.
	return aws.NewCredentialsCache(stscreds.NewAssumeRoleProvider(
		sts.NewFromConfig(awsCfg), sess.roleARN,
		func(o *stscreds.AssumeRoleOptions) { o.RoleSessionName = sess.sessionName },
	)), nil
}

// wifEnvSource builds the token source for the ECS/Fargate env contract and
// reports whether to use it. ok=false means fall back to Application Default
// Credentials.
//
// A failure falls back ONLY when no session was named: naming one pins which
// grant the run may exercise, so authenticating as anything else would defeat
// it. That case returns an [errTokenSource] with ok=true, which fails every
// request with the real cause rather than silently switching identity.
func wifEnvSource(ctx context.Context, audience, serviceAccount string, scopes []string) (oauth2.TokenSource, bool) {
	sess := wifEnvSession()
	ts, err := wifTokenSource(ctx, audience, serviceAccount, sess, scopes)
	switch {
	case err == nil:
		return ts, true
	case sess.requested():
		return errTokenSource{err}, true
	default:
		return nil, false
	}
}

// errTokenSource fails every token request with err.
//
// It exists because [clientOptions] returns no error: when a named session was
// requested and could not be built, falling through to Application Default
// Credentials would authenticate as a different identity than the caller asked
// for. Failing closed keeps the cause in the message instead.
type errTokenSource struct{ err error }

// Token always returns the construction error.
func (t errTokenSource) Token() (*oauth2.Token, error) { return nil, t.err }

// wifTokenSource builds a keyless GCP token source that exchanges the running
// task's own AWS identity (via Google's STS) for a short-lived token
// impersonating serviceAccount. No service-account key is involved.
//
// The AWS subject credentials come from [ecsAwsSupplier], not a
// credential_source JSON file, because Google's built-in AWS source only
// reads env vars or the EC2 IMDS endpoint — neither carries a Fargate task
// role. The supplier delegates to the AWS SDK default chain, which speaks
// the ECS container-credentials endpoint.
//
// sess optionally names an AssumeRole hop so the presented ARN carries a
// caller-chosen session name; its zero value presents the ambient identity.
func wifTokenSource(ctx context.Context, audience, serviceAccount string, sess wifSession, scopes []string) (oauth2.TokenSource, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("wif: load aws config: %w", err)
	}
	creds, err := wifSubjectCredentials(awsCfg, sess)
	if err != nil {
		return nil, err
	}

	conf := externalaccount.Config{
		Audience:                       audience,
		SubjectTokenType:               "urn:ietf:params:aws:token-type:aws4_request",
		ServiceAccountImpersonationURL: "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/" + serviceAccount + ":generateAccessToken",
		Scopes:                         scopes,
		AwsSecurityCredentialsSupplier: ecsAwsSupplier{region: awsCfg.Region, provider: creds},
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
