package gcp

import "codeberg.org/icearp/disco/internal/redact"

// Provider-declared redaction rules. GCP responses are mostly pointer-style:
// Secret Manager payload needs a separate AccessSecretVersion call (disco
// doesn't invoke it); SA keys carry privateKeyData only on Create. Rules below
// cover env-var maps from Cloud Functions/Run/Composer/Build, plus defensive
// password rules for fields that surface on Create echoes.
func init() {
	rules := []redact.TypeRules{
		// Cloud Functions Gen2: serviceConfig.environmentVariables (plain),
		// secretEnvironmentVariables are pointer refs (preserved by omission).
		{Type: TypeCloudFunction, Attributes: []redact.Rule{
			{Path: "serviceConfig.environmentVariables.*", Mode: redact.RedactScalar},
			// Gen1 shape carries top-level environmentVariables.
			{Path: "environmentVariables.*", Mode: redact.RedactScalar},
		}},
		// Cloud Run service / job — env vars on every container.
		{Type: TypeCloudRunSvc, Attributes: []redact.Rule{
			{Path: "template.containers[*].env[*].value", Mode: redact.RedactScalar},
		}},
		{Type: TypeCloudRunJob, Attributes: []redact.Rule{
			{Path: "template.template.containers[*].env[*].value", Mode: redact.RedactScalar},
		}},
		// Cloud Build trigger — substitution map values + per-step env values.
		{Type: TypeCloudBuildTrigger, Attributes: []redact.Rule{
			{Path: "substitutions.*", Mode: redact.RedactScalar},
			{Path: "build.steps[*].env[*]", Mode: redact.RedactScalar},
		}},
		// Composer environment — software-config env vars.
		{Type: TypeComposerEnv, Attributes: []redact.Rule{
			{Path: "config.softwareConfig.envVariables.*", Mode: redact.RedactScalar},
		}},
		// SQL instance — rootPassword (Create echo only, defensive).
		{Type: TypeSQLInstance, Attributes: []redact.Rule{
			{Path: "rootPassword", Mode: redact.RedactScalar},
		}},
		// SQL user — password (List doesn't populate it, but the field is
		// shared with Insert/Update payloads; defensive against a future SDK
		// where it does echo).
		{Type: TypeSQLUser, Attributes: []redact.Rule{
			{Path: "password", Mode: redact.RedactScalar},
		}},
		// Service-account key — privateKeyData (Create echo only).
		{Type: TypeIAMSAKey, Attributes: []redact.Rule{
			{Path: "privateKeyData", Mode: redact.RedactScalar},
		}},
		// Secret Manager — payload data, in case a future scanner fetches it
		// (current scanner deliberately doesn't — see secretmanager_scanners.go).
		{Type: TypeSecret, Attributes: []redact.Rule{
			{Path: "payload.data", Mode: redact.RedactScalar},
		}},
		// Cloud Identity OIDC SSO profile — rpConfig.clientSecret (input-only per
		// SDK doc; defensive against a future SDK where List echoes it, same
		// rationale as SQL user's password above).
		{Type: TypeCloudIdentityInboundOidcSsoProfile, Attributes: []redact.Rule{
			{Path: "rpConfig.clientSecret", Mode: redact.RedactScalar},
		}},
		// IAM OAuth client credential — clientSecret is SDK-documented "Output
		// only": the system-generated secret genuinely comes back on List, not
		// just Create (unlike the two defensive rules above).
		{Type: TypeIAMCredential, Attributes: []redact.Rule{
			{Path: "clientSecret", Mode: redact.RedactScalar},
		}},
		// IAM Provider — only the WorkforcePoolProvider side carries an OIDC/
		// OAuth2 client secret (workload identity federation's Oidc config has
		// no secret — token verification only). Both secret fields are
		// SDK-documented "Input only... will never be populated in any
		// response"; defensive against a future echo, same rationale as the
		// SQL user password / Cloud Identity OIDC rules above.
		{Type: TypeIAMProvider, Attributes: []redact.Rule{
			{Path: "oidc.clientSecret.value.plainText", Mode: redact.RedactScalar},
			{Path: "extendedAttributesOauth2Client.clientSecret.value.plainText", Mode: redact.RedactScalar},
			{Path: "extraAttributesOauth2Client.clientSecret.value.plainText", Mode: redact.RedactScalar},
		}},
	}
	for _, r := range rules {
		redact.Register(r)
	}
}
