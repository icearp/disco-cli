package cmd

import "github.com/spf13/cobra"

// typesCmd is the parent for provider-specific type listing commands.
// Usage: disco types aws
var typesCmd = &cobra.Command{
	Use:   "types",
	Short: "List cloud provider resource types and disco coverage",
	Long: `Show resource types available from a cloud provider's registry and
indicate which ones are currently covered by disco's scanners.

Examples:
  disco types aws
  disco types aws --filter uncovered
  disco types aws --output json
  disco types azure
  disco types azure --filter uncovered`,
}

func init() { rootCmd.AddCommand(typesCmd) }
