package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	azureprovider "codeburg.org/icearp/disco/internal/providers/azure"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription"
	"github.com/spf13/cobra"
)

var typesAzureCmd = &cobra.Command{
	Use:   "azure",
	Short: "List Azure resource types and disco coverage",
	Long: `Queries the Azure Resource Provider registry (Providers/List) to enumerate
all registered resource types, then cross-references with the types currently
covered by disco's scanners.

Uses the standard Azure credential chain (env vars, workload identity, Azure CLI).

Examples:
  disco types azure
  disco types azure --subscription <id>
  disco types azure --filter uncovered
  disco types azure --output json
  disco types azure --services microsoft.compute,microsoft.network`,
	RunE: runTypesAzure,
}

func init() {
	typesAzureCmd.Flags().String("subscription", "", "Azure subscription ID (auto-detects first available when empty)")
	typesAzureCmd.Flags().StringP("output", "o", "table", "Output format: table, json")
	typesAzureCmd.Flags().String("filter", "all", "Filter results: all, covered, uncovered")
	typesAzureCmd.Flags().StringSlice("services", nil, "Comma-separated Azure namespaces to include (e.g. microsoft.compute,microsoft.network); omit for all")
	typesCmd.AddCommand(typesAzureCmd)
}

// AzureTypeRow is one entry in the Azure gap-analysis output.
type AzureTypeRow struct {
	AzureType string `json:"azure_type"` // e.g. Microsoft.Compute/virtualMachines
	DiscoName string `json:"disco_name"` // e.g. azure:compute:virtual-machine
	Covered   bool   `json:"covered"`
}

func runTypesAzure(cmd *cobra.Command, _ []string) error {
	subscriptionID, _ := cmd.Flags().GetString("subscription")
	outputFmt, _ := cmd.Flags().GetString("output")
	filter, _ := cmd.Flags().GetString("filter")
	services, _ := cmd.Flags().GetStringSlice("services")

	if filter != "all" && filter != "covered" && filter != "uncovered" {
		return fmt.Errorf("--filter must be all, covered, or uncovered; got %q", filter)
	}

	ctx := context.Background()

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return fmt.Errorf("azure credential: %w", err)
	}

	// If no subscription provided, auto-detect the first available one.
	if subscriptionID == "" {
		subscriptionID, err = detectFirstSubscription(ctx, cred)
		if err != nil {
			return fmt.Errorf("detect subscription: %w", err)
		}
	}

	fmt.Fprintln(os.Stderr, "Fetching Azure provider registry...")
	azureTypes, err := listAzureProviderTypes(ctx, subscriptionID, cred)
	if err != nil {
		return fmt.Errorf("list azure provider types: %w", err)
	}

	rows := buildAzureRows(azureTypes, filter, services)

	switch outputFmt {
	case "json":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	default:
		return printAzureTypesTable(cmd, rows)
	}
}

// detectFirstSubscription returns the ID of the first accessible subscription.
func detectFirstSubscription(ctx context.Context, cred *azidentity.DefaultAzureCredential) (string, error) {
	client, err := armsubscription.NewSubscriptionsClient(cred, nil)
	if err != nil {
		return "", err
	}
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "", err
		}
		for _, s := range page.Value {
			if s.SubscriptionID != nil && *s.SubscriptionID != "" {
				return *s.SubscriptionID, nil
			}
		}
	}
	return "", fmt.Errorf("no accessible Azure subscriptions found; use --subscription to specify one")
}

// listAzureProviderTypes pages through the Azure provider registry and returns
// all fully-qualified resource type strings (e.g. "Microsoft.Compute/virtualMachines").
func listAzureProviderTypes(ctx context.Context, subscriptionID string, cred *azidentity.DefaultAzureCredential) ([]string, error) {
	client, err := armresources.NewProvidersClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, err
	}

	var types []string
	// Expand=resourceTypes ensures the resource-type list is populated.
	expand := "resourceTypes"
	pager := client.NewListPager(&armresources.ProvidersClientListOptions{Expand: &expand})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range page.Value {
			if p.Namespace == nil {
				continue
			}
			ns := *p.Namespace
			for _, rt := range p.ResourceTypes {
				if rt.ResourceType == nil {
					continue
				}
				types = append(types, ns+"/"+*rt.ResourceType)
			}
		}
	}
	return types, nil
}

// buildAzureRows normalises each Azure type, checks coverage, and applies filters.
func buildAzureRows(azureTypes []string, filter string, services []string) []AzureTypeRow {
	// Build O(1) service lookup set (lowercase full namespace, e.g. "microsoft.compute").
	serviceSet := make(map[string]bool, len(services))
	for _, s := range services {
		serviceSet[strings.ToLower(s)] = true
	}

	rows := make([]AzureTypeRow, 0, len(azureTypes))
	for _, at := range azureTypes {
		rawNS, _, ok := strings.Cut(at, "/")
		if !ok {
			continue // malformed; skip
		}
		// Lowercase the full namespace including "Microsoft." prefix for filtering.
		ns := strings.ToLower(rawNS)
		if len(serviceSet) > 0 && !serviceSet[ns] {
			continue
		}

		key := strings.ToLower(at)
		discoName, covered := azureprovider.LookupAzureType(key)
		if !covered {
			// Compute a best-effort disco name for display.
			discoName = azureNativeToDiscoName(at)
		}

		switch filter {
		case "covered":
			if !covered {
				continue
			}
		case "uncovered":
			if covered {
				continue
			}
		}

		rows = append(rows, AzureTypeRow{AzureType: at, DiscoName: discoName, Covered: covered})
	}
	return rows
}

// azureNativeToDiscoName converts an Azure resource type string to a best-effort
// disco-format name for display. The full namespace (including "Microsoft." prefix)
// is lowercased verbatim. This does not guarantee an exact match with disco's type
// constants (singular vs. plural differences); use LookupAzureType for covered types.
//
// Examples:
//
//	Microsoft.Compute/virtualMachines          → azure:microsoft.compute:virtual-machines
//	Microsoft.Network/virtualNetworks/subnets  → azure:microsoft.network:virtual-networks/subnets
func azureNativeToDiscoName(azureType string) string {
	rawNS, rawType, ok := strings.Cut(azureType, "/")
	if !ok {
		return strings.ToLower(azureType)
	}
	ns := strings.ToLower(rawNS) // e.g. "microsoft.compute"

	// Convert each path segment from PascalCase/camelCase to kebab-case.
	segments := strings.Split(rawType, "/")
	kebabSegs := make([]string, len(segments))
	for i, seg := range segments {
		kebabSegs[i] = pascalToKebab(seg) // reuse from types_aws.go (same package)
	}
	return "azure:" + ns + ":" + strings.Join(kebabSegs, "/")
}

func printAzureTypesTable(cmd *cobra.Command, rows []AzureTypeRow) error {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "AZURE TYPE\tDISCO TYPE\tCOVERED")
	for _, r := range rows {
		covered := "no"
		if r.Covered {
			covered = "yes"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", r.AzureType, r.DiscoName, covered)
	}
	return w.Flush()
}
