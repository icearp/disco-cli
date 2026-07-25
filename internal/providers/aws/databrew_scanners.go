package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/databrew"
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

type databrewAPI interface {
	ListDatasets(context.Context, *databrew.ListDatasetsInput, ...func(*databrew.Options)) (*databrew.ListDatasetsOutput, error)
	ListJobs(context.Context, *databrew.ListJobsInput, ...func(*databrew.Options)) (*databrew.ListJobsOutput, error)
	ListProjects(context.Context, *databrew.ListProjectsInput, ...func(*databrew.Options)) (*databrew.ListProjectsOutput, error)
	ListRecipes(context.Context, *databrew.ListRecipesInput, ...func(*databrew.Options)) (*databrew.ListRecipesOutput, error)
	ListRulesets(context.Context, *databrew.ListRulesetsInput, ...func(*databrew.Options)) (*databrew.ListRulesetsOutput, error)
	ListSchedules(context.Context, *databrew.ListSchedulesInput, ...func(*databrew.Options)) (*databrew.ListSchedulesOutput, error)
}

// scanDataBrew discovers six DataBrew resource types via List* paginators.
// ResourceArn is native on every type.
func scanDataBrew(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := databrew.NewFromConfig(acct.cfg, func(o *databrew.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanDataBrewDatasets(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDataBrewJobs(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDataBrewProjects(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDataBrewRecipes(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDataBrewRulesets(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDataBrewSchedules(ctx, client, acct, region, st, scanID) },
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

func scanDataBrewDatasets(ctx context.Context, client databrewAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := databrew.NewListDatasetsPaginator(client, &databrew.ListDatasetsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "databrew:ListDatasets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("databrew:ListDatasets: %w", err)
		}
		for _, d := range out.Datasets {
			arn := sv(d.ResourceArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataBrewDataset, NativeID: arn,
				Name: d.Name, Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "databrew datasets")
}

func scanDataBrewJobs(ctx context.Context, client databrewAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := databrew.NewListJobsPaginator(client, &databrew.ListJobsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "databrew:ListJobs", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("databrew:ListJobs: %w", err)
		}
		for _, j := range out.Jobs {
			arn := sv(j.ResourceArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataBrewJob, NativeID: arn,
				Name: j.Name, Region: &region,
				AttributesJSON: mustJSON(j), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "databrew jobs")
}

func scanDataBrewProjects(ctx context.Context, client databrewAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := databrew.NewListProjectsPaginator(client, &databrew.ListProjectsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "databrew:ListProjects", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("databrew:ListProjects: %w", err)
		}
		for _, p := range out.Projects {
			arn := sv(p.ResourceArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataBrewProject, NativeID: arn,
				Name: p.Name, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "databrew projects")
}

func scanDataBrewRecipes(ctx context.Context, client databrewAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := databrew.NewListRecipesPaginator(client, &databrew.ListRecipesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "databrew:ListRecipes", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("databrew:ListRecipes: %w", err)
		}
		for _, r := range out.Recipes {
			arn := sv(r.ResourceArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataBrewRecipe, NativeID: arn,
				Name: r.Name, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "databrew recipes")
}

func scanDataBrewRulesets(ctx context.Context, client databrewAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := databrew.NewListRulesetsPaginator(client, &databrew.ListRulesetsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "databrew:ListRulesets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("databrew:ListRulesets: %w", err)
		}
		for _, r := range out.Rulesets {
			arn := sv(r.ResourceArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataBrewRuleset, NativeID: arn,
				Name: r.Name, Region: &region,
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "databrew rulesets")
}

func scanDataBrewSchedules(ctx context.Context, client databrewAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := databrew.NewListSchedulesPaginator(client, &databrew.ListSchedulesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "databrew:ListSchedules", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("databrew:ListSchedules: %w", err)
		}
		for _, s := range out.Schedules {
			arn := sv(s.ResourceArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataBrewSchedule, NativeID: arn,
				Name: s.Name, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "databrew schedules")
}
