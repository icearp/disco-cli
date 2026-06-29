package aws

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/applicationsignals"
)

func init() {
	registerService(serviceEntry{
		name: "aws:applicationsignals",
		fn:   scanApplicationSignals,
		emits: []coverage.TypeDecl{
			{Service: "applicationsignals", DiscoType: TypeApplicationSignalsSLO, Leaf: true},
			{Service: "applicationsignals", DiscoType: TypeApplicationSignalsGroupingConfiguration, Leaf: true},
			// Upstream catalogs Application Signals services under "cloudwatch".
			{Service: "cloudwatch", DiscoType: TypeCloudWatchService, Leaf: true},
		},
	})
}

type applicationSignalsAPI interface {
	ListServiceLevelObjectives(context.Context, *applicationsignals.ListServiceLevelObjectivesInput, ...func(*applicationsignals.Options)) (*applicationsignals.ListServiceLevelObjectivesOutput, error)
	ListGroupingAttributeDefinitions(context.Context, *applicationsignals.ListGroupingAttributeDefinitionsInput, ...func(*applicationsignals.Options)) (*applicationsignals.ListGroupingAttributeDefinitionsOutput, error)
	ListServices(context.Context, *applicationsignals.ListServicesInput, ...func(*applicationsignals.Options)) (*applicationsignals.ListServicesOutput, error)
}

// applicationSignalsServiceNativeID synthesizes a stable NativeID for an
// Application Signals service. The SDK exposes no ARN — a service is identified
// by its required KeyAttributes map (Type/Name/Environment/…). Sorting the map
// into "k=v" pairs keeps the NativeID stable across scans regardless of Go's
// map-iteration order.
func applicationSignalsServiceNativeID(region, acct string, keyAttrs map[string]string) string {
	keys := make([]string, 0, len(keyAttrs))
	for k := range keyAttrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+keyAttrs[k])
	}
	return fmt.Sprintf("arn:aws:application-signals:%s:%s:service/%s", region, acct, strings.Join(pairs, ";"))
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
	svcTotal, svcInserted, err := scanApplicationSignalsServices(ctx, client, acct, region, st, scanID, time.Now())
	if err != nil {
		return sloTotal + gcTotal + svcTotal, sloInserted + gcInserted + svcInserted, err
	}
	return sloTotal + gcTotal + svcTotal, sloInserted + gcInserted + svcInserted, nil
}

// scanApplicationSignalsServices lists the Application Signals services
// discovered in the trailing 24h. ListServices requires a time window and
// returns only services with telemetry in that window, so a service quiet for
// longer than the window will not appear — an inherent API limitation. Services
// carry no ARN; the NativeID is synthesized from their KeyAttributes.
func scanApplicationSignalsServices(ctx context.Context, client applicationSignalsAPI, acct *account, region string, st *store.Store, scanID string, now time.Time) (total, inserted int, err error) {
	start := now.Add(-24 * time.Hour)
	in := &applicationsignals.ListServicesInput{
		StartTime: &start,
		EndTime:   &now,
	}
	p := applicationsignals.NewListServicesPaginator(client, in)
	for p.HasMorePages() {
		page, perr := p.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "applicationsignals:ListServices", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("applicationsignals:ListServices: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(page.ServiceSummaries))
		for _, s := range page.ServiceSummaries {
			if len(s.KeyAttributes) == 0 {
				continue
			}
			name := s.KeyAttributes["Name"]
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeCloudWatchService,
				NativeID:       applicationSignalsServiceNativeID(region, acct.ID, s.KeyAttributes),
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			})
		}
		if len(batch) > 0 {
			n, uerr := st.UpsertResources(batch)
			if uerr != nil {
				return total, inserted, fmt.Errorf("upsert applicationsignals services: %w", uerr)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
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
