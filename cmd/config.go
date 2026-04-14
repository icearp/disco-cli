package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// boilerplateConfig is the template written by `disco config init`.
// All sections are optional; omitting a provider causes disco to auto-detect
// accounts/projects/subscriptions via ambient cloud credentials.
const boilerplateConfig = `# ~/.disco/config.yaml
# All sections are optional. Omitting a provider section causes disco to
# auto-detect accounts/projects/subscriptions using ambient credentials.
# Credentials are never stored here; use each cloud's standard auth chain.

aws:
  # Regions scanned for every account that does not override this list.
  default_regions:
    - us-east-1
  # Explicit account list. If omitted, the ambient AWS identity is used.
  accounts: []
  # Example multi-account entry:
  # - id: "123456789012"
  #   name: production
  #   regions: [us-east-1, us-west-2]
  #   role_arn: "arn:aws:iam::123456789012:role/disco-scanner"

gcp:
  # Explicit project list. If omitted, all accessible projects are enumerated.
  projects: []
  # Example:
  # - id: my-project-id
  #   name: "My GCP Project"
  # Path to a service account JSON file. If omitted, Application Default Credentials are used.
  service_account_file: ""

azure:
  # Explicit subscription list. If omitted, all accessible subscriptions are enumerated.
  subscriptions: []
  # Example:
  # - id: "00000000-0000-0000-0000-000000000000"
  #   name: Production
`

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage disco configuration",
}

var configInitForce bool

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Generate a boilerplate config file at the default location",
	RunE:  runConfigInit,
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configInitCmd)
	configInitCmd.Flags().BoolVar(&configInitForce, "force", false, "Overwrite existing config file")
}

func runConfigInit(_ *cobra.Command, _ []string) error {
	path := configFilePath()

	// Refuse to clobber an existing file unless --force is set.
	if _, err := os.Stat(path); err == nil && !configInitForce {
		return fmt.Errorf("config file already exists: %s (use --force to overwrite)", path)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	if err := os.WriteFile(path, []byte(boilerplateConfig), 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	fmt.Printf("Config written to %s\n", path)
	return nil
}

// configFilePath returns the path where the config file should be written.
// Priority: viper already loaded a file → use that path; --config flag set → use that;
// fallback → default location $HOME/.disco/config.yaml.
func configFilePath() string {
	if p := viper.ConfigFileUsed(); p != "" {
		return p
	}
	if cfgFile != "" {
		return cfgFile
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(home, ".disco", "config.yaml")
}
