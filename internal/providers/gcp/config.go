package gcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/viper"
	"google.golang.org/api/cloudresourcemanager/v3"
	"google.golang.org/api/option"
	"golang.org/x/oauth2/google"
)

// providerCfg mirrors the gcp: section of ~/.disco/config.yaml.
type providerCfg struct {
	Projects           []projectCfg `mapstructure:"projects"`
	ServiceAccountFile string       `mapstructure:"service_account_file"`
}

// projectCfg is one project entry in the config file.
type projectCfg struct {
	ID   string `mapstructure:"id"`
	Name string `mapstructure:"name"`
}

// loadProjects parses the viper config and returns resolved project structs.
// When no projects are configured, all accessible projects are enumerated via
// the Cloud Resource Manager API.
func loadProjects(ctx context.Context) ([]project, error) {
	var cfg providerCfg
	if err := viper.UnmarshalKey("gcp", &cfg); err != nil {
		return nil, fmt.Errorf("parse gcp config: %w", err)
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

// clientOptions returns google.golang.org/api option.ClientOption values for
// the configured credential type. Defaults to Application Default Credentials.
func clientOptions(ctx context.Context, cfg providerCfg) []option.ClientOption {
	scopes := []string{"https://www.googleapis.com/auth/cloud-platform.read-only"}

	if cfg.ServiceAccountFile != "" {
		return []option.ClientOption{
			option.WithCredentialsFile(cfg.ServiceAccountFile),
			option.WithScopes(scopes...),
		}
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
