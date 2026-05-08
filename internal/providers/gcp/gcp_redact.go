package gcp

import "codeberg.org/icearp/disco/internal/redact"

// Provider-declared redaction rules. GCP API responses are mostly
// pointer-style — Secret Manager payload is fetched via a separate
// AccessSecretVersion call (disco does not invoke it), SA keys carry
// privateKeyData only on Create. The rules below cover env-variable maps
// surfaced by Cloud Functions / Cloud Run / Composer / Cloud Build, plus
// defensive password rules for the few fields that surface on Create echoes.
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
		// Service-account key — privateKeyData (Create echo only).
		{Type: TypeIAMSAKey, Attributes: []redact.Rule{
			{Path: "privateKeyData", Mode: redact.RedactScalar},
		}},
		// Secret Manager — payload data, if any future scanner ever fetches
		// it (current scanner deliberately does not — see comment in
		// secretmanager_scanners.go).
		{Type: TypeSecret, Attributes: []redact.Rule{
			{Path: "payload.data", Mode: redact.RedactScalar},
		}},
	}
	for _, r := range rules {
		redact.Register(r)
	}
}
