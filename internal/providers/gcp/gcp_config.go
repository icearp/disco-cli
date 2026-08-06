package gcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/viper"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/cloudresourcemanager/v3"
	"google.golang.org/api/option"
)

// providerCfg mirrors the gcp: section of ~/.disco/config.yaml.
type providerCfg struct {
	Projects []projectCfg `mapstructure:"projects"`
	// CredentialConfigFile is a path to a credential-configuration file: a
	// Workload Identity Federation cred-config (gcloud iam
	// workload-identity-pools create-cred-config) for keyless auth, or a
	// traditional service-account key. Both consumed by
	// option.WithCredentialsFile, which parses external_account and
	// service_account JSON natively.
	CredentialConfigFile string `mapstructure:"credential_config_file"`
}

// projectCfg is one project entry in the config file.
type projectCfg struct {
	ID   string `mapstructure:"id"`
	Name string `mapstructure:"name"`
}

// loadProjects parses the viper config and returns resolved project structs.
// When no projects are configured, all accessible projects are enumerated via
// the Cloud Resource Manager API.
func loadProjects(ctx context.Context, credentialConfigOverride string) ([]project, error) {
	var cfg providerCfg
	if err := viper.UnmarshalKey("gcp", &cfg); err != nil {
		return nil, fmt.Errorf("parse gcp config: %w", err)
	}
	// A --credential-config flag pins auth for this scan, overriding the
	// config file (mirrors the AWS --role-arn override precedent).
	if credentialConfigOverride != "" {
		cfg.CredentialConfigFile = credentialConfigOverride
	}

	opts := clientOptions(ctx, cfg)

	if len(cfg.Projects) > 0 {
		// Use the explicitly configured project list.
		projects := make([]project, 0, len(cfg.Projects))
		for _, p := range cfg.Projects {
			projects = append(projects, project{ID: p.ID, Name: p.Name})
		}
		return projects, nil
	}

	// Auto-enumerate all projects accessible to the credential.
	crmSvc, err := cloudresourcemanager.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("cloudresourcemanager client: %w", err)
	}

	// Projects.Search() enumerates all projects the caller can access without
	// requiring a parent (org/folder). Projects.List() in v3 requires a parent.
	var projects []project
	req := crmSvc.Projects.Search()
	if err := req.Pages(ctx, func(page *cloudresourcemanager.SearchProjectsResponse) error {
		for _, p := range page.Projects {
			if p.State != "ACTIVE" {
				continue
			}
			projects = append(projects, project{
				ID:   p.ProjectId,
				Name: p.DisplayName,
				// Parent is set later by scanHierarchy.
			})
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("cloudresourcemanager:ListProjects: %w", err)
	}
	return projects, nil
}

// credentialMode names the credential path clientOptions selects, in
// precedence order. Typed string so the selection logic is unit-testable
// without real credentials or opaque option.ClientOption values.
type credentialMode string

const (
	credModeFile    credentialMode = "credential_config_file" // WIF cred-config or SA key file
	credModeWIFEnv  credentialMode = "wif_env"                // ECS/Fargate programmatic bridge
	credModeDefault credentialMode = "adc"                    // Application Default Credentials
)

// selectCredentialMode resolves which credential path to use from the config
// plus the WIF env contract, in precedence order. Pure + side-effect-free so
// the precedence is unit-testable.
func selectCredentialMode(cfg providerCfg, wifAudience, wifServiceAccount string) credentialMode {
	switch {
	case cfg.CredentialConfigFile != "":
		return credModeFile
	case wifConfigured(wifAudience, wifServiceAccount):
		return credModeWIFEnv
	default:
		return credModeDefault
	}
}

// clientOptions returns google.golang.org/api option.ClientOption values for
// the configured credential type. Defaults to Application Default Credentials.
func clientOptions(ctx context.Context, cfg providerCfg) []option.ClientOption {
	scopes := []string{
		"https://www.googleapis.com/auth/cloud-platform.read-only",
		// Workspace Directory + Cloud Identity for the tenant identity scanner
		// (cloudidentity_scanners.go). Scope-additive — APIs only request what
		// they need, so non-Workspace projects are unaffected.
		"https://www.googleapis.com/auth/admin.directory.user.readonly",
		"https://www.googleapis.com/auth/cloud-identity.groups.readonly",
	}

	wifAud, wifSA := wifEnvCredentials()
	switch selectCredentialMode(cfg, wifAud, wifSA) {
	case credModeFile:
		// WIF cred-config or a plain SA key. option.WithCredentialsFile parses
		// external_account and service_account JSON natively, so this single
		// path covers keyless auth on every platform Google's built-in sources
		// support (CI with OIDC, GCE, GKE, an AWS host with env/IMDS creds).
		return []option.ClientOption{
			option.WithCredentialsFile(cfg.CredentialConfigFile), //nolint:staticcheck // user-provided path; SA1019 deprecation does not apply here
			option.WithScopes(scopes...),
		}
	case credModeWIFEnv:
		// ECS/Fargate keyless bridge: exchange the running task's own AWS
		// identity for a short-lived token impersonating the read-only service
		// account — no key, no file. Covers the one case Google's built-in AWS
		// source can't: a Fargate task role, reachable only via the ECS
		// container-credentials endpoint.
		if ts, ok := wifEnvSource(ctx, wifAud, wifSA, scopes); ok {
			return []option.ClientOption{option.WithTokenSource(ts), option.WithScopes(scopes...)}
		}
		// Fall through to ADC on error so a misconfigured WIF env doesn't
		// hard-fail a scan that could still authenticate another way. A run
		// that named a session is exempt — see [wifEnvSource].
	}

	// ADC: env GOOGLE_APPLICATION_CREDENTIALS → gcloud default → metadata server.
	ts, err := google.DefaultTokenSource(ctx, scopes...)
	if err != nil {
		// Fall back to no explicit option; the API client will try ADC itself.
		return []option.ClientOption{option.WithScopes(scopes...)}
	}
	return []option.ClientOption{option.WithTokenSource(ts)}
}

// projectNumber extracts the numeric ID from a project resource name like
// "projects/123456789".
func projectNumber(name string) string {
	parts := strings.Split(name, "/")
	if len(parts) == 2 {
		return parts[1]
	}
	return name
}
