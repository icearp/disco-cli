package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"unicode"

	awsprovider "codeberg.org/icearp/disco/internal/providers/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/spf13/cobra"
)

var typesAWSCmd = &cobra.Command{
	Use:   "aws",
	Short: "List AWS CloudFormation resource types and disco coverage",
	Long: `Queries the AWS CloudFormation type registry (ListTypes) to enumerate all
public AWS resource types, then cross-references with the resource types
currently covered by disco's scanners.

Uses the standard AWS credential chain (env vars, ~/.aws/credentials, IAM role).

Examples:
  disco types aws
  disco types aws --region us-west-2
  disco types aws --profile my-profile
  disco types aws --filter uncovered
  disco types aws --output json
  disco types aws --services ec2,rds
  disco types aws --services ec2 --filter uncovered`,
	RunE: runTypesAWS,
}

func init() {
	typesAWSCmd.Flags().String("region", "us-east-1", "AWS region for the CloudFormation API call")
	typesAWSCmd.Flags().String("profile", "", "AWS profile name (uses default credential chain when empty)")
	typesAWSCmd.Flags().StringP("output", "o", "table", "Output format: table, json")
	typesAWSCmd.Flags().String("filter", "all", "Filter results: all, covered, uncovered")
	typesAWSCmd.Flags().StringSlice("services", nil, "Comma-separated AWS services to include (e.g. ec2,rds); omit for all")
	typesCmd.AddCommand(typesAWSCmd)
}

// TypeRow is one entry in the gap-analysis output.
type TypeRow struct {
	CFNName   string `json:"cfn_name"`   // e.g. AWS::EC2::Instance
	DiscoName string `json:"disco_name"` // e.g. aws:ec2:instance (normalized)
	Covered   bool   `json:"covered"`    // true if disco scans this type
}

func runTypesAWS(cmd *cobra.Command, _ []string) error {
	region, _ := cmd.Flags().GetString("region")
	profile, _ := cmd.Flags().GetString("profile")
	outputFmt, _ := cmd.Flags().GetString("output")
	filter, _ := cmd.Flags().GetString("filter")
	services, _ := cmd.Flags().GetStringSlice("services")

	if filter != "all" && filter != "covered" && filter != "uncovered" {
		return fmt.Errorf("--filter must be all, covered, or uncovered; got %q", filter)
	}

	ctx := context.Background()

	// Build AWS config. Profile is optional; empty means use the default chain.
	opts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(region),
	}
	if profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return fmt.Errorf("load aws config: %w", err)
	}

	fmt.Fprintln(os.Stderr, "Fetching CloudFormation type registry...")
	cfnNames, err := listCFNTypes(ctx, cloudformation.NewFromConfig(cfg))
	if err != nil {
		return fmt.Errorf("list cloudformation types: %w", err)
	}

	// Build a set of disco-covered types for O(1) lookup.
	knownSet := make(map[string]bool, len(awsprovider.KnownTypes()))
	for _, t := range awsprovider.KnownTypes() {
		knownSet[t] = true
	}

	rows := buildRows(cfnNames, knownSet, filter, services)

	switch outputFmt {
	case "json":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	default:
		return printTypesTable(cmd, rows)
	}
}

// listCFNTypes pages through CloudFormation ListTypes (PUBLIC, RESOURCE) and
// returns all type names. AWS returns up to 100 per page.
func listCFNTypes(ctx context.Context, client *cloudformation.Client) ([]string, error) {
	var names []string
	input := &cloudformation.ListTypesInput{
		Visibility: cftypes.VisibilityPublic,
		Type:       cftypes.RegistryTypeResource,
	}
	paginator := cloudformation.NewListTypesPaginator(client, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range page.TypeSummaries {
			if s.TypeName != nil {
				names = append(names, *s.TypeName)
			}
		}
	}
	return names, nil
}

// buildRows normalises every CFN type name, checks coverage, and applies the filter.
// services is an optional list of lowercase service names (e.g. "ec2", "rds"); when
// non-empty only types whose disco service segment matches are included.
func buildRows(cfnNames []string, knownSet map[string]bool, filter string, services []string) []TypeRow {
	// Build O(1) service lookup set from the caller-supplied list.
	serviceSet := make(map[string]bool, len(services))
	for _, s := range services {
		serviceSet[strings.ToLower(s)] = true
	}

	rows := make([]TypeRow, 0, len(cfnNames))
	for _, cfn := range cfnNames {
		disco := cfnToDiscoName(cfn)

		// Filter by service (middle segment of disco name: aws:ec2:instance → "ec2").
		if len(serviceSet) > 0 {
			parts := strings.SplitN(disco, ":", 3)
			if len(parts) < 2 || !serviceSet[parts[1]] {
				continue
			}
		}

		covered := knownSet[disco]
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
		rows = append(rows, TypeRow{CFNName: cfn, DiscoName: disco, Covered: covered})
	}
	return rows
}

func printTypesTable(cmd *cobra.Command, rows []TypeRow) error {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "CFN TYPE\tDISCO TYPE\tCOVERED")
	for _, r := range rows {
		covered := "no"
		if r.Covered {
			covered = "yes"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", r.CFNName, r.DiscoName, covered)
	}
	return w.Flush()
}

// cfnToDiscoName converts a CloudFormation type name to disco's naming convention.
// The vendor prefix is lowercased verbatim; the service segment is lowercased
// verbatim; only the resource kind (last segment) is PascalCase→kebab converted.
// Examples:
//
//	AWS::EC2::Instance                            → aws:ec2:instance
//	AWS::ElasticLoadBalancingV2::LoadBalancer      → aws:elasticloadbalancingv2:load-balancer
//	AWS::RDS::DBInstance                          → aws:rds:db-instance
//	TF::Module::Resource                          → tf:module:resource
//	TrendMicro::DeepSecurity::Policy              → trendmicro:deepsecurity:policy
//
// Note: the CFN service name for ELBv2 is "ElasticLoadBalancingV2", which
// normalises to "elasticloadbalancingv2". Disco stores it as
// "aws:elasticloadbalancing:load-balancer" (without the V2 suffix). That type
// will appear as uncovered — intentional and accurate.
func cfnToDiscoName(cfn string) string {
	parts := strings.SplitN(cfn, "::", 3)
	if len(parts) != 3 {
		return strings.ToLower(cfn) // malformed; best effort
	}
	// vendor and service are lowercased verbatim; resource kind gets kebab conversion.
	vendor := strings.ToLower(parts[0])
	service := strings.ToLower(parts[1])
	kind := pascalToKebab(parts[2])
	return vendor + ":" + service + ":" + kind
}

// pascalToKebab converts a PascalCase or mixed-case identifier to kebab-case.
// It inserts a hyphen at each lower→upper transition and before the last
// capital in an acronym run (e.g. "DBInstance" → "db-instance").
func pascalToKebab(s string) string {
	if s == "" {
		return ""
	}
	runes := []rune(s)
	var b strings.Builder
	b.WriteRune(unicode.ToLower(runes[0]))
	for i := 1; i < len(runes); i++ {
		cur := runes[i]
		prev := runes[i-1]
		if unicode.IsUpper(cur) {
			if unicode.IsLower(prev) {
				// lower → upper: "Load|Balancer"
				b.WriteByte('-')
			} else if i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
				// upper run → upper+lower: "DB|Instance"
				b.WriteByte('-')
			}
		}
		b.WriteRune(unicode.ToLower(cur))
	}
	return b.String()
}
