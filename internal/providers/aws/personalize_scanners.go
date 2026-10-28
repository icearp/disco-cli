package aws

import (
	"cmp"
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/personalize"
	pztypes "github.com/aws/aws-sdk-go-v2/service/personalize/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypePersonalizeDataset, Service: "personalize"})
	registerType(restype.Descriptor{Type: TypePersonalizeDatasetGroup, Service: "personalize"})
	registerType(restype.Descriptor{Type: TypePersonalizeSchema, Service: "personalize", Untaggable: true})
	registerType(restype.Descriptor{Type: TypePersonalizeSolution, Service: "personalize"})
	registerType(restype.Descriptor{Type: TypePersonalizeCampaign, Service: "personalize"})
	registerType(restype.Descriptor{Type: TypePersonalizeEventTracker, Service: "personalize"})
	registerType(restype.Descriptor{Type: TypePersonalizeFilter, Service: "personalize"})
	registerType(restype.Descriptor{Type: TypePersonalizeMetricAttribution, Service: "personalize", Untaggable: true})
	registerType(restype.Descriptor{Type: TypePersonalizeRecommender, Service: "personalize"})
	registerType(restype.Descriptor{Type: TypePersonalizeSolutionVersion, Service: "personalize"})
	// LastUpdatedDateTime qualifies as volatile under the region-collision
	// reason: an AWS-provided recipe ARN carries no region
	// (arn:aws:personalize:::recipe/...), so every region reports the same
	// natural key, and AWS's per-region rollout gives each region a different
	// value for a resource that is one partition-global object. Under a shared
	// key that is indistinguishable from the resource changing, so the regions
	// version-split each other within a single scan.
	//
	// Nothing is lost that the key could have kept: the field cannot be stored
	// per region when there is no per-region row. See
	// TestVolatileRulesAreJustified, which holds this reason to region-less
	// ARNs only.
	registerType(restype.Descriptor{Type: TypePersonalizeRecipe, Service: "personalize", Managed: true, Volatile: []string{"LastUpdatedDateTime"}, Untaggable: true})
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
	ListSolutionVersions(context.Context, *personalize.ListSolutionVersionsInput, ...func(*personalize.Options)) (*personalize.ListSolutionVersionsOutput, error)
	ListTagsForResource(context.Context, *personalize.ListTagsForResourceInput, ...func(*personalize.Options)) (*personalize.ListTagsForResourceOutput, error)
}

const (
	pzListDatasetGroupsOp      = "personalize:ListDatasetGroups"
	pzListDatasetsOp           = "personalize:ListDatasets"
	pzListSchemasOp            = "personalize:ListSchemas"
	pzListSolutionsOp          = "personalize:ListSolutions"
	pzListCampaignsOp          = "personalize:ListCampaigns"
	pzListEventTrackersOp      = "personalize:ListEventTrackers"
	pzListFiltersOp            = "personalize:ListFilters"
	pzListMetricAttributionsOp = "personalize:ListMetricAttributions"
	pzListRecommendersOp       = "personalize:ListRecommenders"
	pzListRecipesOp            = "personalize:ListRecipes"
	pzListSolutionVersionsOp   = "personalize:ListSolutionVersions"
	pzTagsOp                   = "personalize:ListTagsForResource"
	// pzPageLimit is the maxResults range maximum the model gives every
	// list op here.
	pzPageLimit = 100
)

func scanPersonalize(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := personalize.NewFromConfig(acct.cfg, func(o *personalize.Options) { o.Region = region })
	return scanPersonalizeAll(ctx, client, acct, region, st, scanID)
}

// scanPersonalizeAll runs every listing as its own phase, one at a time
// (runHostPhases): a phase's error does not stop the later ones; a failed
// write and ctx's end do. A taggable type's listing is written a page at a
// time with its tags (pzTags), and every tag denial of the scan is one
// warning. Schemas, metric attributions and recipes get no tag read: their
// Create ops take no tags (recipes are AWS's own), and the Service Reference
// lists none of them for ListTagsForResource. Each of those listings is
// written once read whole (storeTokenList), as base did: a per-account
// catalogue, not a listing that grows with use.
func scanPersonalizeAll(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tags := pzTags(client).sharingDenials()
	total, inserted, err := runHostPhases(ctx, st, acct, region, "personalize", 0, []hostPhase{
		{op: pzListDatasetGroupsOp, run: func(p *phaseTotals) bool {
			return scanPzDatasetGroups(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: pzListDatasetsOp, run: func(p *phaseTotals) bool {
			return scanPzDatasets(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, pzListSchemasOp, func() (int, int, error) {
			return scanPzSchemas(ctx, client, acct, region, st, scanID)
		}),
		{op: pzListSolutionsOp, run: func(p *phaseTotals) bool {
			return scanPzSolutions(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: pzListCampaignsOp, run: func(p *phaseTotals) bool {
			return scanPzCampaigns(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: pzListEventTrackersOp, run: func(p *phaseTotals) bool {
			return scanPzEventTrackers(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: pzListFiltersOp, run: func(p *phaseTotals) bool {
			return scanPzFilters(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, pzListMetricAttributionsOp, func() (int, int, error) {
			return scanPzMetricAttributions(ctx, client, acct, region, st, scanID)
		}),
		{op: pzListRecommendersOp, run: func(p *phaseTotals) bool {
			return scanPzRecommenders(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, pzListRecipesOp, func() (int, int, error) {
			return scanPzRecipes(ctx, client, acct, region, st, scanID)
		}),
		{op: pzListSolutionVersionsOp, run: func(p *phaseTotals) bool {
			return scanPzSolutionVersions(ctx, client, acct, region, st, scanID, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// pzTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, which is the ARN its listing returned. The op does not page.
// Every type read here has a Create op that takes tags. The Service
// Reference lists each for ListTagsForResource except solution versions,
// which have no resource entry of their own: it binds CreateSolutionVersion
// and DescribeSolutionVersion to the solution resource type, so IAM treats a
// version's ARN (solution/<name>/<id>) as a solution resource. That
// ListTagsForResource accepts a version's ARN is unverified live. The op
// models ResourceNotFoundException, for a resource deleted after it was
// listed.
func pzTags(client personalizeAPI) rowTagReader {
	return rowTagReader{
		op:       pzTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &personalize.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t pztypes.Tag) (*string, *string) { return t.TagKey, t.TagValue }), nil
		},
	}
}

// pzRow is the row of a listed element of typ keyed by its ARN. An element
// without an ARN is not stored; what names the elements in the listing's one
// warning.
func pzRow(acct *account, region, scanID, typ, what, arn string, name, status *string, item any) (*store.Resource, string, string) {
	if arn == "" {
		return nil, what + " have no ARN", cmp.Or(sv(name), "(an element with no name)")
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arn,
		Name: name, Region: &region, Status: status,
		AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}, "", ""
}

func scanPzDatasetGroups(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, pzListDatasetGroupsOp,
		func(ctx context.Context, token *string) ([]pztypes.DatasetGroupSummary, *string, error) {
			out, err := client.ListDatasetGroups(ctx, &personalize.ListDatasetGroupsInput{MaxResults: sdkaws.Int32(pzPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DatasetGroups, out.NextToken, nil
		},
		func(g pztypes.DatasetGroupSummary) (*store.Resource, string, string) {
			return pzRow(acct, region, scanID, TypePersonalizeDatasetGroup, "dataset groups", sv(g.DatasetGroupArn), g.Name, g.Status, g)
		}, tags, p)
}

func scanPzDatasets(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, pzListDatasetsOp,
		func(ctx context.Context, token *string) ([]pztypes.DatasetSummary, *string, error) {
			out, err := client.ListDatasets(ctx, &personalize.ListDatasetsInput{MaxResults: sdkaws.Int32(pzPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Datasets, out.NextToken, nil
		},
		func(d pztypes.DatasetSummary) (*store.Resource, string, string) {
			return pzRow(acct, region, scanID, TypePersonalizeDataset, "datasets", sv(d.DatasetArn), d.Name, d.Status, d)
		}, tags, p)
}

func scanPzSchemas(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, pzListSchemasOp,
		func(ctx context.Context, token *string) ([]pztypes.DatasetSchemaSummary, *string, error) {
			out, err := client.ListSchemas(ctx, &personalize.ListSchemasInput{MaxResults: sdkaws.Int32(pzPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Schemas, out.NextToken, nil
		},
		func(s pztypes.DatasetSchemaSummary) (*store.Resource, string, string) {
			return pzRow(acct, region, scanID, TypePersonalizeSchema, "schemas", sv(s.SchemaArn), s.Name, nil, s)
		})
}

func scanPzSolutions(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, pzListSolutionsOp,
		func(ctx context.Context, token *string) ([]pztypes.SolutionSummary, *string, error) {
			out, err := client.ListSolutions(ctx, &personalize.ListSolutionsInput{MaxResults: sdkaws.Int32(pzPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Solutions, out.NextToken, nil
		},
		func(s pztypes.SolutionSummary) (*store.Resource, string, string) {
			return pzRow(acct, region, scanID, TypePersonalizeSolution, "solutions", sv(s.SolutionArn), s.Name, s.Status, s)
		}, tags, p)
}

// scanPzCampaigns lists campaigns account-wide (SolutionArn input is optional).
// CampaignSummary carries no solution ref, so campaign stays a leaf.
func scanPzCampaigns(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, pzListCampaignsOp,
		func(ctx context.Context, token *string) ([]pztypes.CampaignSummary, *string, error) {
			out, err := client.ListCampaigns(ctx, &personalize.ListCampaignsInput{MaxResults: sdkaws.Int32(pzPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Campaigns, out.NextToken, nil
		},
		func(c pztypes.CampaignSummary) (*store.Resource, string, string) {
			return pzRow(acct, region, scanID, TypePersonalizeCampaign, "campaigns", sv(c.CampaignArn), c.Name, c.Status, c)
		}, tags, p)
}

func scanPzEventTrackers(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, pzListEventTrackersOp,
		func(ctx context.Context, token *string) ([]pztypes.EventTrackerSummary, *string, error) {
			out, err := client.ListEventTrackers(ctx, &personalize.ListEventTrackersInput{MaxResults: sdkaws.Int32(pzPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.EventTrackers, out.NextToken, nil
		},
		func(e pztypes.EventTrackerSummary) (*store.Resource, string, string) {
			return pzRow(acct, region, scanID, TypePersonalizeEventTracker, "event trackers", sv(e.EventTrackerArn), e.Name, e.Status, e)
		}, tags, p)
}

// scanPzFilters lists filters account-wide; FilterSummary carries
// DatasetGroupArn, wired to its dataset group by the resolver.
func scanPzFilters(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, pzListFiltersOp,
		func(ctx context.Context, token *string) ([]pztypes.FilterSummary, *string, error) {
			out, err := client.ListFilters(ctx, &personalize.ListFiltersInput{MaxResults: sdkaws.Int32(pzPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Filters, out.NextToken, nil
		},
		func(f pztypes.FilterSummary) (*store.Resource, string, string) {
			return pzRow(acct, region, scanID, TypePersonalizeFilter, "filters", sv(f.FilterArn), f.Name, f.Status, f)
		}, tags, p)
}

func scanPzMetricAttributions(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, pzListMetricAttributionsOp,
		func(ctx context.Context, token *string) ([]pztypes.MetricAttributionSummary, *string, error) {
			out, err := client.ListMetricAttributions(ctx, &personalize.ListMetricAttributionsInput{MaxResults: sdkaws.Int32(pzPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.MetricAttributions, out.NextToken, nil
		},
		func(m pztypes.MetricAttributionSummary) (*store.Resource, string, string) {
			return pzRow(acct, region, scanID, TypePersonalizeMetricAttribution, "metric attributions", sv(m.MetricAttributionArn), m.Name, m.Status, m)
		})
}

// scanPzRecommenders lists recommenders account-wide; RecommenderSummary
// carries DatasetGroupArn, wired to its dataset group by the resolver.
func scanPzRecommenders(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, pzListRecommendersOp,
		func(ctx context.Context, token *string) ([]pztypes.RecommenderSummary, *string, error) {
			out, err := client.ListRecommenders(ctx, &personalize.ListRecommendersInput{MaxResults: sdkaws.Int32(pzPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Recommenders, out.NextToken, nil
		},
		func(r pztypes.RecommenderSummary) (*store.Resource, string, string) {
			return pzRow(acct, region, scanID, TypePersonalizeRecommender, "recommenders", sv(r.RecommenderArn), r.Name, r.Status, r)
		}, tags, p)
}

// scanPzRecipes lists AWS-provided recipes — provider-managed catalogue rows.
func scanPzRecipes(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, pzListRecipesOp,
		func(ctx context.Context, token *string) ([]pztypes.RecipeSummary, *string, error) {
			out, err := client.ListRecipes(ctx, &personalize.ListRecipesInput{MaxResults: sdkaws.Int32(pzPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Recipes, out.NextToken, nil
		},
		func(r pztypes.RecipeSummary) (*store.Resource, string, string) {
			return pzRow(acct, region, scanID, TypePersonalizeRecipe, "recipes", sv(r.RecipeArn), r.Name, r.Status, r)
		})
}

// scanPzSolutionVersions lists solution versions account-wide: SolutionArn is
// optional, and omitting it saves one call per solution. A solution version
// is a trained model that campaigns deploy, so it is stored. Rows grow with
// every training run (automatic training adds one per schedule tick) and last
// until the solution is deleted: the API has no per-version delete. The
// summary has no name, so no Name is set. Its parent solution is recoverable
// from the ARN prefix, but like the other Personalize types no solution edge
// is recorded.
func scanPzSolutionVersions(ctx context.Context, client personalizeAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, pzListSolutionVersionsOp,
		func(ctx context.Context, token *string) ([]pztypes.SolutionVersionSummary, *string, error) {
			out, err := client.ListSolutionVersions(ctx, &personalize.ListSolutionVersionsInput{MaxResults: sdkaws.Int32(pzPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.SolutionVersions, out.NextToken, nil
		},
		func(v pztypes.SolutionVersionSummary) (*store.Resource, string, string) {
			return pzRow(acct, region, scanID, TypePersonalizeSolutionVersion, "solution versions", sv(v.SolutionVersionArn), nil, v.Status, v)
		}, tags, p)
}
