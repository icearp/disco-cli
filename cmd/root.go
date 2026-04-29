// Package cmd contains the cobra-rooted CLI for `disco`. Each file
// hosts one subcommand; cross-cutting helpers live in helpers.go.
package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

// Version is set at build time via -ldflags "-X codeberg.org/icearp/disco/cmd.Version=<tag>".
// Defaults to "dev" for local builds.
var Version = "dev"

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
		fmt.Fprintln(os.Stderr, err)
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
		fmt.Sprintf("config file (default: %s)", filepath.Join(configDir(), "config.yaml")))
	rootCmd.PersistentFlags().String("db", "",
		fmt.Sprintf("database path (default: %s)", filepath.Join(dataDir(), "disco.db")))
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

	if err := viper.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}
}

// defaultDBPath returns the configured or default path for the disco database.
// It does not create any directories; that is store.Open's responsibility.
func defaultDBPath() string {
	if p := viper.GetString("db"); p != "" {
		return p
	}
	return filepath.Join(dataDir(), "disco.db")
}
