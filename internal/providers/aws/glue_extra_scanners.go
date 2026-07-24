package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/glue"
)

// scanGlueBlueprints discovers Glue blueprints (workflow templates). The list
// summary carries no ARN, so NativeID is synthesized. Leaf.
func scanGlueBlueprints(ctx context.Context, client glueAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := glue.NewListBlueprintsPaginator(client, &glue.ListBlueprintsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "glue:ListBlueprints", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("glue:ListBlueprints: %w", err)
		}
		for _, name := range out.Blueprints {
			if name == "" {
				continue
			}
			n := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeGlueBlueprint, NativeID: fmt.Sprintf("arn:aws:glue:%s:%s:blueprint/%s", region, acct.ID, name),
				Name: &n, Region: &region, DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "glue blueprints")
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
					Type: TypeGlueUserDefinedFunction, NativeID: glueDatabaseARN(region, acct.ID, dbName) + "/function/" + name,
					Name: f.FunctionName, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "glue user-defined-functions")
}
