// Package cmd contains the cobra-rooted CLI for `disco`. Each file
// hosts one subcommand; cross-cutting helpers live in helpers.go.
package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile    string
	verbose    bool
	dbReadOnly bool
)

// Version is set at build time via -ldflags "-X codeberg.org/icearp/disco/cmd.Version=<tag>".
// Defaults to "dev" for local builds, but when produced by `go build .` from
// inside a git checkout we substitute the VCS revision via build-info so the
// stamp propagated to snapshot manifests + SARIF tool.driver.version is at
// least traceable. Falls through to the literal "dev" only when no VCS info
// is available (e.g. `go test`, `go install` from a tarball).
var Version = resolveVersion()

func resolveVersion() string {
	const fallback = "dev"
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return fallback
	}
	var rev, modified string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if rev != "" {
		if len(rev) > 12 {
			rev = rev[:12]
		}
		if modified == "true" {
			return rev + "+dirty"
		}
		return rev
	}
	// `go install codeberg.org/...@<tag>` from a versioned module records
	// the tag here; useful when no VCS info is embedded.
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	return fallback
}

var rootCmd = &cobra.Command{
	Use:           "disco",
	Version:       Version,
	SilenceUsage:  true, // don't dump --help on runtime errors
	SilenceErrors: true, // Execute() prints the error; suppress Cobra's duplicate print
	Short:         "Cloud resource discovery tool for AWS, Azure, and GCP",
	Long: `disco scans cloud accounts and resolves relationships between discovered resources.

Supported providers: AWS (accounts), Azure (subscriptions/resource groups), GCP (organizations/folders/projects).`,
}

// Execute runs the root command.
func Execute() {
	cmd, err := rootCmd.ExecuteC()
	if err != nil {
		// `graph path` exits 1 silently when the two resources are unreachable
		// — the absence of a path is a query result, not an error to print.
		if errors.Is(err, store.ErrNoPath) {
			os.Exit(1)
		}
		// `tag-coverage --min-coverage` and `check` (default findings-gate)
		// already rendered their report; the sentinel only carries the
		// exit-code gate. Suppress Cobra's duplicate stderr print for both.
		if errors.Is(err, errTagCoverageBelow) || errors.Is(err, errFindingsReported) {
			os.Exit(1)
		}
		// --require-resources / --min-resources gate (Phase 3.9). Print the
		// wrapped error to stderr so operators see "have N, want >= M" but
		// pipelines that already parsed stdout get an unambiguous exit 1.
		if errors.Is(err, errResourcesBelowMin) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		// `coverage --check-strict` distinguishes transient registry-fetch
		// failure (exit 2) from genuine drift (exit 1) so CI pipelines can
		// retry-with-backoff vs. file-a-ticket.
		if errors.Is(err, errCoverageRegistryUnreachable) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		// When the command already wrote a structured-JSON error envelope to
		// stdout (json/jsonl outputs), don't duplicate the message on stderr
		// — pipelines see one signal, not two.
		if !structuredErrorEmitted {
			fmt.Fprintln(os.Stderr, err)
		}
		// Unknown command / subcommand: print the matched parent's usage so
		// the user sees the available subcommands inline with the error.
		if cmd != nil && strings.HasPrefix(err.Error(), "unknown command") {
			_ = cmd.Usage()
		}
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "",
		fmt.Sprintf("config file (default: %s)", tildify(filepath.Join(configDir(), "config.yaml"))))
	rootCmd.PersistentFlags().String("db", "",
		fmt.Sprintf("database path (default: %s)", tildify(filepath.Join(dataDir(), "disco.db"))))
	// Register -v as --verbose BEFORE cobra lazily binds it to --version
	// (InitDefaultVersionFlag skips -v when the shorthand is already taken).
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false,
		"print diagnostic banners (config file path, etc.) on stderr")
	rootCmd.PersistentFlags().BoolVar(&dbReadOnly, "db-readonly", false,
		"open the local DB read-only; rejects scan and any write path")
	cobra.CheckErr(viper.BindPFlag("db", rootCmd.PersistentFlags().Lookup("db")))
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.AddConfigPath(configDir())
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	viper.SetEnvPrefix("DISCO")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil && verbose {
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}
}

// tildify replaces a leading $HOME with "~" so help-text default values
// don't leak the build/run host's home directory. Falls through unchanged
// when $HOME is unresolvable or the path doesn't sit under it.
func tildify(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return p
}

// defaultDBPath returns the configured or default path for the disco database.
// It does not create any directories; that is store.Open's responsibility.
func defaultDBPath() string {
	if p := viper.GetString("db"); p != "" {
		return p
	}
	return filepath.Join(dataDir(), "disco.db")
}
