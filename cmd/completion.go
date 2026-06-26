package cmd

import (
	"codeberg.org/icearp/disco/internal/providers"
	"github.com/spf13/cobra"
)

// staticCompletion returns a flag-completion func offering a fixed value set
// with no file completion. Used for the --output format flags so a shell
// `disco resources -o <TAB>` suggests table/json/... instead of filenames.
func staticCompletion(values ...string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}

// completeProviderNames offers the registered provider names, for completing
// the scan --providers flag value (`disco scan --providers <TAB>`).
func completeProviderNames(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return providers.Names(), cobra.ShellCompDirectiveNoFileComp
}
