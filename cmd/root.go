package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:           "disco",
	SilenceUsage:  true, // don't dump --help on runtime errors
	SilenceErrors: true, // Execute() prints the error; suppress Cobra's duplicate print
	Short:         "Cloud resource discovery tool for AWS, Azure, and GCP",
	Long: `disco scans cloud accounts and resolves relationships between discovered resources.

Supported providers: AWS (accounts), Azure (subscriptions/resource groups), GCP (organizations/folders).`,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default: $HOME/.disco/config.yaml)")
	rootCmd.PersistentFlags().String("db", "", "database path (default: $HOME/.disco/disco.db)")
	cobra.CheckErr(viper.BindPFlag("db", rootCmd.PersistentFlags().Lookup("db")))
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		viper.AddConfigPath(filepath.Join(home, ".disco"))
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
	home, err := os.UserHomeDir()
	if err != nil {
		return "disco.db"
	}
	return filepath.Join(home, ".disco", "disco.db")
}
