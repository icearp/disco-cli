package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/personalize"
)

func init() {
	registerService(serviceEntry{
		name: "aws:personalize",
		fn:   scanPersonalize,
		emits: []coverage.TypeDecl{
			{Service: "personalize", DiscoType: TypePersonalizeDataset},
			{Service: "personalize", DiscoType: TypePersonalizeDatasetGroup},
			{Service: "personalize", DiscoType: TypePersonalizeSchema},
			{Service: "personalize", DiscoType: TypePersonalizeSolution},
		},
	})
}

type personalizeAPI interface {
	ListDatasets(context.Context, *personalize.ListDatasetsInput, ...func(*personalize.Options)) (*personalize.ListDatasetsOutput, error)
	ListDatasetGroups(context.Context, *personalize.ListDatasetGroupsInput, ...func(*personalize.Options)) (*personalize.ListDatasetGroupsOutput, error)
	ListSchemas(context.Context, *personalize.ListSchemasInput, ...func(*personalize.Options)) (*personalize.ListSchemasOutput, error)
	ListSolutions(context.Context, *personalize.ListSolutionsInput, ...func(*personalize.Options)) (*personalize.ListSolutionsOutput, error)
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
