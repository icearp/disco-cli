package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	"github.com/icearp/disco-cli/store"
)

// scanGlueBlueprints stores the blueprints (workflow templates) with their
// tags. The listing returns names only, so NativeID is built in the
// documented blueprint ARN format; edge-less.
func scanGlueBlueprints(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "glue:ListBlueprints",
		func(ctx context.Context, token *string) ([]string, *string, error) {
			out, err := client.ListBlueprints(ctx, &glue.ListBlueprintsInput{NextToken: token, MaxResults: sdkaws.Int32(glueOrchestrationPageSize25)})
			if err != nil {
				return nil, nil, err
			}
			return out.Blueprints, out.NextToken, nil
		},
		func(name string) (*store.Resource, string, string) {
			if name == "" {
				return nil, "blueprints have an empty name", "(a blueprint with no name)"
			}
			// The listing returns the name alone: there is no element to
			// store as the attributes.
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlueBlueprint, NativeID: glueResourceARN(acct.partition(), region, acct.ID, "blueprint", name),
				Name: &name, Region: &region, DiscoveredBy: scanID,
			}, "", ""
		}, tags, p)
}

// scanGlueUserDefinedFunctions fans GetUserDefinedFunctions out per database.
// The summary carries no ARN; NativeID = {dbARN}/function/{name} so the resolver
// recovers the parent database.
func scanGlueUserDefinedFunctions(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string, dbNames []string) (int, int, error) {
	pattern := "*"
	var batch []*store.Resource
	for _, db := range dbNames {
		dbName := db
		pager := glue.NewGetUserDefinedFunctionsPaginator(client, &glue.GetUserDefinedFunctionsInput{DatabaseName: &dbName, Pattern: &pattern})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "glue:GetUserDefinedFunctions", acct.ID, region, err)
					break
				}
				return 0, 0, fmt.Errorf("glue:GetUserDefinedFunctions %s: %w", dbName, err)
			}
			for _, f := range out.UserDefinedFunctions {
				name := sv(f.FunctionName)
				if name == "" {
					continue
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeGlueUserDefinedFunction, NativeID: glueDatabaseARN(acct.partition(), region, acct.ID, dbName) + "/function/" + name,
					Name: f.FunctionName, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "glue user-defined-functions")
}
