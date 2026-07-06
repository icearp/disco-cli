package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// boilerplateConfig is the template written by `disco config init`. All
// sections are optional — omitting a provider triggers auto-detect of
// accounts/projects/subscriptions via ambient credentials.
const boilerplateConfig = `# disco configuration file
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
  # Keyless auth (recommended): path to a Workload Identity Federation
  # credential-configuration file produced by
  #   gcloud iam workload-identity-pools create-cred-config
  # No service-account key is downloaded or stored. Also accepts a plain
  # service-account key JSON file. Overridable per-scan with --credential-config.
  # If omitted (and no WIF env below), Application Default Credentials are used.
  credential_config_file: ""
  # ECS / Fargate keyless bridge (env-only, advanced): when disco runs on AWS
  # ECS/Fargate and its task-role identity is reachable only via the container-
  # credentials endpoint, set these instead of a cred-config file:
  #   DISCO_GCP_WIF_AUDIENCE          - the workload-identity provider audience
  #   DISCO_GCP_WIF_SERVICE_ACCOUNT   - the service-account email to impersonate

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
	Long: `Manage disco configuration.

Resolution order (highest precedence first):
  1. DISCO_* environment variables (e.g. DISCO_DB)
  2. --config <path> on the command line
  3. ${XDG_CONFIG_HOME}/disco/config.yaml (Linux: ~/.config/disco/config.yaml;
     macOS/Windows: platform app-data dir resolved via github.com/adrg/xdg)

Credentials are never stored in this file. Each provider uses its native
auth chain (AWS shared config, gcloud ADC, Azure DefaultAzureCredential).

Subcommands:
  init   Generate a boilerplate config file at the default location.`,
}

var configInitForce bool

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Generate a boilerplate config file at the default location",
	Args:  cobra.NoArgs,
	Long: `Writes a starter config.yaml under the resolved config directory
(${XDG_CONFIG_HOME}/disco/config.yaml on Linux). The file enumerates
optional aws / gcp / azure sections; each is opt-in — disco auto-detects
accounts, projects, and subscriptions from ambient credentials when a
section is omitted.

Refuses to overwrite an existing file unless --force. Refuses to write at
all when --db-readonly is set (the global flag scopes the DB but disco
treats config writes as part of the same trust boundary).`,
	Example: `  disco config init
  disco config init --force
  disco --config /etc/disco/config.yaml config init`,
	RunE: runConfigInit,
}

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configInitCmd)
	configInitCmd.Flags().BoolVar(&configInitForce, "force", false, "Overwrite existing config file")
}

func runConfigInit(_ *cobra.Command, _ []string) error {
	if dbReadOnly {
		return fmt.Errorf("config init refused: --db-readonly is set (no write paths permitted)")
	}
	path := configFilePath()

	// Refuse to clobber an existing file unless --force is set.
	if _, err := os.Stat(path); err == nil && !configInitForce {
		return fmt.Errorf("%s already exists; pass --force to overwrite", path)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	if err := os.WriteFile(path, []byte(boilerplateConfig), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	// Success message goes to stderr (matches snapshot/verify), keeping
	// stdout clean for future machine output.
	fmt.Fprintf(os.Stderr, "Config written to %s\n", path)
	return nil
}

// configFilePath returns where the config file should be written: viper's
// loaded path, else --config, else the platform default (configDir()/config.yaml).
func configFilePath() string {
	if p := viper.ConfigFileUsed(); p != "" {
		return p
	}
	if cfgFile != "" {
		return cfgFile
	}
	return filepath.Join(configDir(), "config.yaml")
}
