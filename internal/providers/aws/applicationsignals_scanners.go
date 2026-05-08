package aws

import (
	"context"
	"fmt"
	"time"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/applicationsignals"
)

func init() {
	registerService(serviceEntry{
		name: "aws:applicationsignals",
		fn:   scanApplicationSignals,
		emits: []coverage.TypeDecl{
			{Service: "applicationsignals", DiscoType: TypeApplicationSignalsSLO, Leaf: true},
			{Service: "applicationsignals", DiscoType: TypeApplicationSignalsGroupingConfiguration, Leaf: true},
		},
	})
}

type applicationSignalsAPI interface {
	ListServiceLevelObjectives(context.Context, *applicationsignals.ListServiceLevelObjectivesInput, ...func(*applicationsignals.Options)) (*applicationsignals.ListServiceLevelObjectivesOutput, error)
	ListGroupingAttributeDefinitions(context.Context, *applicationsignals.ListGroupingAttributeDefinitionsInput, ...func(*applicationsignals.Options)) (*applicationsignals.ListGroupingAttributeDefinitionsOutput, error)
}

// applicationSignalsGroupingConfigurationNativeID synthesizes the singleton
// per-(account, region) NativeID for the grouping configuration. The SDK
// exposes no ARN; the configuration is a one-per-account-region resource
// whose body is enumerated via ListGroupingAttributeDefinitions.
func applicationSignalsGroupingConfigurationNativeID(region, acct string) string {
	return fmt.Sprintf("arn:aws:application-signals:%s:%s:grouping-configuration", region, acct)
}

func scanApplicationSignals(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := applicationsignals.NewFromConfig(acct.cfg, func(o *applicationsignals.Options) { o.Region = region })
	sloTotal, sloInserted, err := scanApplicationSignalsSLOs(ctx, client, acct, region, st, scanID)
	if err != nil {
		return sloTotal, sloInserted, err
	}
	gcTotal, gcInserted, err := scanApplicationSignalsGroupingConfiguration(ctx, client, acct, region, st, scanID)
	if err != nil {
		return sloTotal + gcTotal, sloInserted + gcInserted, err
	}
	return sloTotal + gcTotal, sloInserted + gcInserted, nil
}

// scanApplicationSignalsGroupingConfiguration upserts the singleton
// grouping-configuration resource by aggregating ListGroupingAttributeDefinitions
// pages into a single attrs body. The configuration row is emitted only when
// the API returns at least one definition or a non-zero UpdatedAt — an empty
// response indicates no configuration was created in this region.
func scanApplicationSignalsGroupingConfiguration(ctx context.Context, client applicationSignalsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	type aggregate struct {
		GroupingAttributeDefinitions []applicationsignalsListGroupingAttributeDefinitionsItem `json:"GroupingAttributeDefinitions"`
		UpdatedAt                    *time.Time                                               `json:"UpdatedAt,omitempty"`
	}
	var agg aggregate
	var nextToken *string
	for {
		out, lerr := client.ListGroupingAttributeDefinitions(ctx, &applicationsignals.ListGroupingAttributeDefinitionsInput{NextToken: nextToken})
		if lerr != nil {
			if isAccessDenied(lerr) {
				return total, inserted, skipIfAccessDenied(st, "applicationsignals:ListGroupingAttributeDefinitions", acct.ID, region, lerr)
			}
			return total, inserted, fmt.Errorf("applicationsignals:ListGroupingAttributeDefinitions: %w", lerr)
		}
		for _, d := range out.GroupingAttributeDefinitions {
			agg.GroupingAttributeDefinitions = append(agg.GroupingAttributeDefinitions, applicationsignalsListGroupingAttributeDefinitionsItem{
				GroupingName:         sv(d.GroupingName),
				DefaultGroupingValue: sv(d.DefaultGroupingValue),
				GroupingSourceKeys:   d.GroupingSourceKeys,
			})
		}
		if out.UpdatedAt != nil {
			agg.UpdatedAt = out.UpdatedAt
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	// No configuration created in this region — skip the singleton row.
	if len(agg.GroupingAttributeDefinitions) == 0 && agg.UpdatedAt == nil {
		return 0, 0, nil
	}
	nativeID := applicationSignalsGroupingConfigurationNativeID(region, acct.ID)
	name := "grouping-configuration"
	r := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeApplicationSignalsGroupingConfiguration,
		NativeID:       nativeID,
		Name:           &name,
		Region:         &region,
		CreatedAt:      tp(agg.UpdatedAt),
		AttributesJSON: mustJSON(agg),
		DiscoveredBy:   scanID,
	}
	n, err := st.UpsertResources([]*store.Resource{r})
	if err != nil {
		return 0, 0, fmt.Errorf("upsert applicationsignals grouping-configuration: %w", err)
	}
	return 1, n, nil
}

// applicationsignalsListGroupingAttributeDefinitionsItem is a JSON-friendly
// projection of `applicationsignalstypes.GroupingAttributeDefinition`. The
// SDK type carries an unexported smithy serde marker that breaks
// `json.Marshal` when nested inside the aggregate; this projection captures
// the fields that matter for graph analysis.
type applicationsignalsListGroupingAttributeDefinitionsItem struct {
	GroupingName         string   `json:"GroupingName"`
	DefaultGroupingValue string   `json:"DefaultGroupingValue,omitempty"`
	GroupingSourceKeys   []string `json:"GroupingSourceKeys,omitempty"`
}

func scanApplicationSignalsSLOs(ctx context.Context, client applicationSignalsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := applicationsignals.NewListServiceLevelObjectivesPaginator(client, &applicationsignals.ListServiceLevelObjectivesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return total, inserted, skipIfAccessDenied(st, "applicationsignals:ListServiceLevelObjectives", acct.ID, region, err)
			}
			return total, inserted, fmt.Errorf("applicationsignals:ListServiceLevelObjectives: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.SloSummaries))
		for _, s := range page.SloSummaries {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			name := sv(s.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeApplicationSignalsSLO,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				CreatedAt:      tp(s.CreatedTime),
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return total, inserted, fmt.Errorf("upsert applicationsignals slos: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
