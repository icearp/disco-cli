package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/personalize"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypePersonalizeDataset, Service: "personalize", Leaf: true})
	registerType(restype.Descriptor{Type: TypePersonalizeDatasetGroup, Service: "personalize", Leaf: true})
	registerType(restype.Descriptor{Type: TypePersonalizeSchema, Service: "personalize", Leaf: true})
	registerType(restype.Descriptor{Type: TypePersonalizeSolution, Service: "personalize", Leaf: true})
	registerType(restype.Descriptor{Type: TypePersonalizeCampaign, Service: "personalize", Leaf: true})
	registerType(restype.Descriptor{Type: TypePersonalizeEventTracker, Service: "personalize", Leaf: true})
	registerType(restype.Descriptor{Type: TypePersonalizeFilter, Service: "personalize"})
	registerType(restype.Descriptor{Type: TypePersonalizeMetricAttribution, Service: "personalize", Leaf: true})
	registerType(restype.Descriptor{Type: TypePersonalizeRecommender, Service: "personalize"})
	// An AWS-provided recipe ARN carries no region
	// (arn:aws:personalize:::recipe/...), so every region reports the same
	// natural key while AWS's per-region rollout gives each a different
	// LastUpdatedDateTime -- so the regions version-split each other within a
	// single scan. That is history churn, not a miscount: the versions are
	// superseded, and scans.resource_count counts current rows only. Declaring
	// the field volatile would end the churn but would drop it from stored
	// attributes, and attributes are recorded as the provider reported them.
	registerType(restype.Descriptor{Type: TypePersonalizeRecipe, Service: "personalize", Leaf: true, Managed: true})
	registerService(serviceEntry{
		name: "aws:personalize",
		fn:   scanPersonalize,
	})
}

type personalizeAPI interface {
	ListDatasets(context.Context, *personalize.ListDatasetsInput, ...func(*personalize.Options)) (*personalize.ListDatasetsOutput, error)
	ListDatasetGroups(context.Context, *personalize.ListDatasetGroupsInput, ...func(*personalize.Options)) (*personalize.ListDatasetGroupsOutput, error)
	ListSchemas(context.Context, *personalize.ListSchemasInput, ...func(*personalize.Options)) (*personalize.ListSchemasOutput, error)
	ListSolutions(context.Context, *personalize.ListSolutionsInput, ...func(*personalize.Options)) (*personalize.ListSolutionsOutput, error)
	ListCampaigns(context.Context, *personalize.ListCampaignsInput, ...func(*personalize.Options)) (*personalize.ListCampaignsOutput, error)
	ListEventTrackers(context.Context, *personalize.ListEventTrackersInput, ...func(*personalize.Options)) (*personalize.ListEventTrackersOutput, error)
	ListFilters(context.Context, *personalize.ListFiltersInput, ...func(*personalize.Options)) (*personalize.ListFiltersOutput, error)
	ListMetricAttributions(context.Context, *personalize.ListMetricAttributionsInput, ...func(*personalize.Options)) (*personalize.ListMetricAttributionsOutput, error)
	ListRecommenders(context.Context, *personalize.ListRecommendersInput, ...func(*personalize.Options)) (*personalize.ListRecommendersOutput, error)
	ListRecipes(context.Context, *personalize.ListRecipesInput, ...func(*personalize.Options)) (*personalize.ListRecipesOutput, error)
}

// scanPersonalize discovers Amazon Personalize datasets, dataset groups,
// schemas, and solutions via paginated List* calls.
func scanPersonalize(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := personalize.NewFromConfig(acct.cfg, func(o *personalize.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanPzDatasetGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanPzDatasets(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanPzSchemas(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanPzSolutions(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanPzCampaigns(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanPzEventTrackers(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanPzFilters(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanPzMetricAttributions(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanPzRecommenders(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanPzRecipes(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanPzDatasetGroups(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := personalize.NewListDatasetGroupsPaginator(client, &personalize.ListDatasetGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "personalize:ListDatasetGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("personalize:ListDatasetGroups: %w", err)
		}
		for _, g := range out.DatasetGroups {
			arn := sv(g.DatasetGroupArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePersonalizeDatasetGroup, NativeID: arn,
				Name: g.Name, Region: &region, Status: g.Status,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "personalize dataset-groups")
}

func scanPzDatasets(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := personalize.NewListDatasetsPaginator(client, &personalize.ListDatasetsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "personalize:ListDatasets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("personalize:ListDatasets: %w", err)
		}
		for _, d := range out.Datasets {
			arn := sv(d.DatasetArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePersonalizeDataset, NativeID: arn,
				Name: d.Name, Region: &region, Status: d.Status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "personalize datasets")
}

func scanPzSchemas(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := personalize.NewListSchemasPaginator(client, &personalize.ListSchemasInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "personalize:ListSchemas", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("personalize:ListSchemas: %w", err)
		}
		for _, s := range out.Schemas {
			arn := sv(s.SchemaArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePersonalizeSchema, NativeID: arn,
				Name: s.Name, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "personalize schemas")
}

func scanPzSolutions(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := personalize.NewListSolutionsPaginator(client, &personalize.ListSolutionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "personalize:ListSolutions", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("personalize:ListSolutions: %w", err)
		}
		for _, s := range out.Solutions {
			arn := sv(s.SolutionArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePersonalizeSolution, NativeID: arn,
				Name: s.Name, Region: &region, Status: s.Status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "personalize solutions")
}

// scanPzCampaigns lists campaigns account-wide (SolutionArn input is optional).
// CampaignSummary carries no solution ref, so campaign stays a leaf.
func scanPzCampaigns(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := personalize.NewListCampaignsPaginator(client, &personalize.ListCampaignsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "personalize:ListCampaigns", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("personalize:ListCampaigns: %w", err)
		}
		for _, c := range out.Campaigns {
			arn := sv(c.CampaignArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePersonalizeCampaign, NativeID: arn,
				Name: c.Name, Region: &region, Status: c.Status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "personalize campaigns")
}

func scanPzEventTrackers(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := personalize.NewListEventTrackersPaginator(client, &personalize.ListEventTrackersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "personalize:ListEventTrackers", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("personalize:ListEventTrackers: %w", err)
		}
		for _, e := range out.EventTrackers {
			arn := sv(e.EventTrackerArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePersonalizeEventTracker, NativeID: arn,
				Name: e.Name, Region: &region, Status: e.Status,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "personalize event-trackers")
}

// scanPzFilters lists filters account-wide; FilterSummary carries
// DatasetGroupArn, wired to its dataset group by the resolver.
func scanPzFilters(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := personalize.NewListFiltersPaginator(client, &personalize.ListFiltersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "personalize:ListFilters", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("personalize:ListFilters: %w", err)
		}
		for _, f := range out.Filters {
			arn := sv(f.FilterArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePersonalizeFilter, NativeID: arn,
				Name: f.Name, Region: &region, Status: f.Status,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "personalize filters")
}

func scanPzMetricAttributions(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := personalize.NewListMetricAttributionsPaginator(client, &personalize.ListMetricAttributionsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "personalize:ListMetricAttributions", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("personalize:ListMetricAttributions: %w", err)
		}
		for _, m := range out.MetricAttributions {
			arn := sv(m.MetricAttributionArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePersonalizeMetricAttribution, NativeID: arn,
				Name: m.Name, Region: &region, Status: m.Status,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "personalize metric-attributions")
}

// scanPzRecommenders lists recommenders account-wide; RecommenderSummary
// carries DatasetGroupArn, wired to its dataset group by the resolver.
func scanPzRecommenders(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := personalize.NewListRecommendersPaginator(client, &personalize.ListRecommendersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "personalize:ListRecommenders", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("personalize:ListRecommenders: %w", err)
		}
		for _, r := range out.Recommenders {
			arn := sv(r.RecommenderArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePersonalizeRecommender, NativeID: arn,
				Name: r.Name, Region: &region, Status: r.Status,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "personalize recommenders")
}

// scanPzRecipes lists AWS-provided recipes — provider-managed catalogue rows.
func scanPzRecipes(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := personalize.NewListRecipesPaginator(client, &personalize.ListRecipesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "personalize:ListRecipes", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("personalize:ListRecipes: %w", err)
		}
		for _, r := range out.Recipes {
			arn := sv(r.RecipeArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePersonalizeRecipe, NativeID: arn,
				Name: r.Name, Region: &region, Status: r.Status,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "personalize recipes")
}
