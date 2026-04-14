package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/spf13/cobra"
)

var (
	listProvider  string
	listType      string
	listRegion    string
	listStatus    string
	listTagKey    string
	listTagValue  string
	listOutputFmt string
	listLimit     uint64
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List discovered resources",
	Long: `List resources from the local database with optional filters.

Examples:
  disco list
  disco list --provider aws --type aws:ec2:instance
  disco list --provider gcp --region us-central1 --status running
  disco list --tag-key env --tag-value production -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := store.Open(defaultDBPath())
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer db.Close()

		var types []string
		if listType != "" {
			types = []string{listType}
		}
		var regions []string
		if listRegion != "" {
			regions = []string{listRegion}
		}

		f := store.ResourceFilter{
			Provider: listProvider,
			Types:    types,
			Regions:  regions,
			Status:   listStatus,
			TagKey:   listTagKey,
			TagValue: listTagValue,
			Limit:    listLimit,
		}

		resources, err := db.ListResources(f)
		if err != nil {
			return fmt.Errorf("list resources: %w", err)
		}

		switch listOutputFmt {
		case "json":
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(resources)
		default:
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PROVIDER\tACCOUNT ID\tRESOURCE TYPE\tNAME\tREGION\tSTATUS")
			for _, r := range resources {
				name := "-"
				if r.Name != nil {
					name = *r.Name
				}
				region := "-"
				if r.Region != nil {
					region = *r.Region
				}
				status := "-"
				if r.Status != nil {
					status = *r.Status
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					r.Provider, r.AccountID, r.Type, name, region, status)
			}
			return w.Flush()
		}
	},
}

func init() {
	listCmd.Flags().StringVarP(&listProvider, "provider", "p", "", "Filter by provider (aws, azure, gcp)")
	listCmd.Flags().StringVarP(&listType, "type", "t", "", "Filter by resource type (e.g. aws:ec2:instance)")
	listCmd.Flags().StringVarP(&listRegion, "region", "r", "", "Filter by region")
	listCmd.Flags().StringVar(&listStatus, "status", "", "Filter by status")
	listCmd.Flags().StringVar(&listTagKey, "tag-key", "", "Filter by tag key")
	listCmd.Flags().StringVar(&listTagValue, "tag-value", "", "Filter by tag value (requires --tag-key)")
	listCmd.Flags().StringVarP(&listOutputFmt, "output", "o", "table", "Output format: table, json")
	listCmd.Flags().Uint64Var(&listLimit, "limit", 500, "Maximum number of results")
	rootCmd.AddCommand(listCmd)
}
