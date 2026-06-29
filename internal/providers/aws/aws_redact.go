package aws

import "codeberg.org/icearp/disco/internal/redact"

// Provider-declared redaction rules. Rules name the SDK-shape JSON paths
// disco wants redacted from AttributesJSON before write. Centralised here
// (rather than scattered across each <svc>_scanners.go init) so a reviewer
// can audit "what does aws scrub?" in one read.
//
// Path syntax: dotted literals; "*" map-key wildcard; "[*]" array wildcard.
// Modes: RedactScalar (leaf-only) and RedactSubtree (every scalar descendant).
//
// AccessKeyId is intentionally NOT redacted — IAM console / CloudTrail /
// ListAccessKeys all surface it unredacted. The credential is SecretAccessKey
// (rare, only on CreateAccessKey, which disco never invokes).
func init() {
	rules := []redact.TypeRules{
		// Lambda function — Environment.Variables is user-controlled key/value.
		{Type: TypeLambdaFunction, Attributes: []redact.Rule{
			{Path: "Environment.Variables.*", Mode: redact.RedactScalar},
		}},
		// CloudHSM cluster — PreCoPassword is the initial crypto-officer password.
		{Type: TypeCloudHSMCluster, Attributes: []redact.Rule{
			{Path: "PreCoPassword", Mode: redact.RedactScalar},
		}},
		// EC2 — UserData (init scripts often carry secrets), key material.
		{Type: TypeEC2Instance, Attributes: []redact.Rule{
			{Path: "UserData", Mode: redact.RedactScalar},
		}},
		{Type: TypeEC2LaunchTemplate, Attributes: []redact.Rule{
			{Path: "LaunchTemplateData.UserData", Mode: redact.RedactScalar},
		}},
		{Type: TypeEC2KeyPair, Attributes: []redact.Rule{
			{Path: "KeyMaterial", Mode: redact.RedactScalar},
		}},
		// Secrets Manager — SecretString (rare in scan; defensive).
		{Type: TypeSecretsManagerSecret, Attributes: []redact.Rule{
			{Path: "SecretString", Mode: redact.RedactScalar},
		}},
		// RDS / Redshift — master password (only on Create response, but
		// belt-and-braces).
		{Type: TypeRDSDBInstance, Attributes: []redact.Rule{
			{Path: "MasterUserPassword", Mode: redact.RedactScalar},
		}},
		{Type: TypeRDSDBCluster, Attributes: []redact.Rule{
			{Path: "MasterUserPassword", Mode: redact.RedactScalar},
		}},
		{Type: TypeRedshiftCluster, Attributes: []redact.Rule{
			{Path: "MasterUserPassword", Mode: redact.RedactScalar},
		}},
		// ElastiCache — auth tokens.
		{Type: TypeElastiCacheCacheCluster, Attributes: []redact.Rule{
			{Path: "AuthToken", Mode: redact.RedactScalar},
		}},
		{Type: TypeElastiCacheReplicationGroup, Attributes: []redact.Rule{
			{Path: "AuthToken", Mode: redact.RedactScalar},
		}},
		// IAM access key — only the secret half (AccessKeyId stays clear).
		{Type: TypeIAMAccessKey, Attributes: []redact.Rule{
			{Path: "SecretAccessKey", Mode: redact.RedactScalar},
		}},
		// Amplify webhook — WebhookUrl embeds a secret trigger token in its
		// query string (…/webhooks?id=…&token=…); redact the whole URL.
		{Type: TypeAmplifyWebhooks, Attributes: []redact.Rule{
			{Path: "WebhookUrl", Mode: redact.RedactScalar},
		}},
		// CodeBuild — plaintext env vars (Type=PLAINTEXT carries values; the
		// PARAMETER_STORE / SECRETS_MANAGER variants carry pointer refs which
		// resolvers consume — preserve those).
		{Type: TypeCodeBuildProject, Attributes: []redact.Rule{
			{Path: "Environment.EnvironmentVariables[*].Value", Mode: redact.RedactScalar},
		}},
	}
	for _, r := range rules {
		redact.Register(r)
	}
}
