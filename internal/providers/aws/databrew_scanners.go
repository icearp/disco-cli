package aws

import (
	"cmp"
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/databrew"
	databrewtypes "github.com/aws/aws-sdk-go-v2/service/databrew/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDataBrewDataset, Service: "databrew"})
	registerType(restype.Descriptor{Type: TypeDataBrewJob, Service: "databrew"})
	registerType(restype.Descriptor{Type: TypeDataBrewProject, Service: "databrew"})
	registerType(restype.Descriptor{Type: TypeDataBrewRecipe, Service: "databrew"})
	registerType(restype.Descriptor{Type: TypeDataBrewRuleset, Service: "databrew"})
	registerType(restype.Descriptor{Type: TypeDataBrewSchedule, Service: "databrew"})
	registerService(serviceEntry{
		name: "aws:databrew",
		fn:   scanDataBrew,
	})
}

const (
	dataBrewListDatasetsOp  = "databrew:ListDatasets"
	dataBrewListJobsOp      = "databrew:ListJobs"
	dataBrewListProjectsOp  = "databrew:ListProjects"
	dataBrewListRecipesOp   = "databrew:ListRecipes"
	dataBrewListRulesetsOp  = "databrew:ListRulesets"
	dataBrewListSchedulesOp = "databrew:ListSchedules"
)

// dataBrewPageSize is the cap every DataBrew List op puts on MaxResults
// (the model's MaxResults100 shape, range 1-100).
const dataBrewPageSize int32 = 100

type databrewAPI interface {
	ListDatasets(context.Context, *databrew.ListDatasetsInput, ...func(*databrew.Options)) (*databrew.ListDatasetsOutput, error)
	ListJobs(context.Context, *databrew.ListJobsInput, ...func(*databrew.Options)) (*databrew.ListJobsOutput, error)
	ListProjects(context.Context, *databrew.ListProjectsInput, ...func(*databrew.Options)) (*databrew.ListProjectsOutput, error)
	ListRecipes(context.Context, *databrew.ListRecipesInput, ...func(*databrew.Options)) (*databrew.ListRecipesOutput, error)
	ListRulesets(context.Context, *databrew.ListRulesetsInput, ...func(*databrew.Options)) (*databrew.ListRulesetsOutput, error)
	ListSchedules(context.Context, *databrew.ListSchedulesInput, ...func(*databrew.Options)) (*databrew.ListSchedulesOutput, error)
}

func scanDataBrew(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := databrew.NewFromConfig(acct.cfg, func(o *databrew.Options) { o.Region = region })
	return scanDataBrewAll(ctx, client, acct, region, st, scanID)
}

// scanDataBrewAll runs the six independent DataBrew listings and joins their
// errors; only a store write failure stops it. Each listing leaves its
// filters (DatasetName, ProjectName, RecipeVersion, TargetArn, JobName) unset
// so it lists the whole account.
func scanDataBrewAll(ctx context.Context, client databrewAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var phases phaseTotals
	for _, phase := range []struct {
		op string
		fn func(context.Context, databrewAPI, *account, string, *store.Store, string) (int, int, error)
	}{
		{dataBrewListDatasetsOp, scanDataBrewDatasets},
		{dataBrewListJobsOp, scanDataBrewJobs},
		{dataBrewListProjectsOp, scanDataBrewProjects},
		{dataBrewListRecipesOp, scanDataBrewRecipes},
		{dataBrewListRulesetsOp, scanDataBrewRulesets},
		{dataBrewListSchedulesOp, scanDataBrewSchedules},
	} {
		t, i, err := phase.fn(ctx, client, acct, region, st, scanID)
		if phases.addPhase(phase.op, t, i, err) {
			break
		}
	}
	return phases.result(st, acct, region)
}

// dataBrewRow builds the row of a listed DataBrew element, keyed by its
// ResourceArn; one without it is not stored, and kind names the element in
// that warning.
func dataBrewRow(acct *account, region, scanID, rtype, kind string, arn, name *string, element any, tags map[string]string) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, kind + " have an empty ResourceArn", cmp.Or(sv(name), "one without a Name")
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: sv(arn), Name: name, Region: &region,
		AttributesJSON: mustJSON(element), TagsJSON: mapTagsJSON(tags), DiscoveredBy: scanID,
	}, "", ""
}

func scanDataBrewDatasets(ctx context.Context, client databrewAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, dataBrewListDatasetsOp,
		func(ctx context.Context, token *string) ([]databrewtypes.Dataset, *string, error) {
			out, err := client.ListDatasets(ctx, &databrew.ListDatasetsInput{MaxResults: sdkaws.Int32(dataBrewPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Datasets, out.NextToken, nil
		},
		func(d databrewtypes.Dataset) (*store.Resource, string, string) {
			return dataBrewRow(acct, region, scanID, TypeDataBrewDataset, "datasets", d.ResourceArn, d.Name, d, d.Tags)
		})
}

func scanDataBrewJobs(ctx context.Context, client databrewAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, dataBrewListJobsOp,
		func(ctx context.Context, token *string) ([]databrewtypes.Job, *string, error) {
			out, err := client.ListJobs(ctx, &databrew.ListJobsInput{MaxResults: sdkaws.Int32(dataBrewPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Jobs, out.NextToken, nil
		},
		func(j databrewtypes.Job) (*store.Resource, string, string) {
			return dataBrewRow(acct, region, scanID, TypeDataBrewJob, "jobs", j.ResourceArn, j.Name, j, j.Tags)
		})
}

func scanDataBrewProjects(ctx context.Context, client databrewAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, dataBrewListProjectsOp,
		func(ctx context.Context, token *string) ([]databrewtypes.Project, *string, error) {
			out, err := client.ListProjects(ctx, &databrew.ListProjectsInput{MaxResults: sdkaws.Int32(dataBrewPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Projects, out.NextToken, nil
		},
		func(p databrewtypes.Project) (*store.Resource, string, string) {
			return dataBrewRow(acct, region, scanID, TypeDataBrewProject, "projects", p.ResourceArn, p.Name, p, p.Tags)
		})
}

// scanDataBrewRecipes leaves RecipeVersion unset, which the op documents as
// listing each recipe's LATEST_PUBLISHED version only, so a recipe never
// published is likely not listed (not verified live).
func scanDataBrewRecipes(ctx context.Context, client databrewAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, dataBrewListRecipesOp,
		func(ctx context.Context, token *string) ([]databrewtypes.Recipe, *string, error) {
			out, err := client.ListRecipes(ctx, &databrew.ListRecipesInput{MaxResults: sdkaws.Int32(dataBrewPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Recipes, out.NextToken, nil
		},
		func(r databrewtypes.Recipe) (*store.Resource, string, string) {
			return dataBrewRow(acct, region, scanID, TypeDataBrewRecipe, "recipes", r.ResourceArn, r.Name, r, r.Tags)
		})
}

func scanDataBrewRulesets(ctx context.Context, client databrewAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, dataBrewListRulesetsOp,
		func(ctx context.Context, token *string) ([]databrewtypes.RulesetItem, *string, error) {
			out, err := client.ListRulesets(ctx, &databrew.ListRulesetsInput{MaxResults: sdkaws.Int32(dataBrewPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Rulesets, out.NextToken, nil
		},
		func(r databrewtypes.RulesetItem) (*store.Resource, string, string) {
			return dataBrewRow(acct, region, scanID, TypeDataBrewRuleset, "rulesets", r.ResourceArn, r.Name, r, r.Tags)
		})
}

func scanDataBrewSchedules(ctx context.Context, client databrewAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, dataBrewListSchedulesOp,
		func(ctx context.Context, token *string) ([]databrewtypes.Schedule, *string, error) {
			out, err := client.ListSchedules(ctx, &databrew.ListSchedulesInput{MaxResults: sdkaws.Int32(dataBrewPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Schedules, out.NextToken, nil
		},
		func(s databrewtypes.Schedule) (*store.Resource, string, string) {
			return dataBrewRow(acct, region, scanID, TypeDataBrewSchedule, "schedules", s.ResourceArn, s.Name, s, s.Tags)
		})
}
